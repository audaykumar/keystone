// RabbitMQ lab: exchange -> queue -> consumer, manual ack, dead-letter
// exchange, and a poison-message retry-count guard.
//
// One binary, two roles (ROLE env): producer and consumer. The topology is a
// direct exchange "lab.direct" bound to queue "work.queue", plus a dead-letter
// exchange "lab.dlx" bound to queue "work.dlq". Both are always declared;
// what changes between the break and test scenarios is whether "work.queue"
// carries x-dead-letter-exchange (MODE=test) or not (MODE=break).
//
//	ROLE=producer   deletes and redeclares the topology for MODE, then
//	                publishes COUNT labeled messages, one of them "poison"
//	                (a body the consumer always fails to process).
//	ROLE=consumer   declares the same topology (idempotent, must match what
//	                the producer just created) and manually acks/nacks:
//	                MODE=break  poison is nack'd with requeue forever
//	                            (capped at BREAK_CAP redeliveries so the
//	                            demo terminates); no dead-letter target is
//	                            configured on the queue, so it never leaves.
//	                MODE=test   poison carries an x-retry-count header the
//	                            consumer increments by re-publishing to the
//	                            tail of the queue and acking the original,
//	                            up to MAX_ATTEMPTS. On the final attempt it
//	                            is nack'd without requeue, and the queue's
//	                            x-dead-letter-exchange routes it to the DLQ.
//
// Env vars: RABBITMQ_URL, ROLE, MODE, COUNT, POISON_AT, PREFETCH,
// MAX_ATTEMPTS, BREAK_CAP.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	mainExchange   = "lab.direct"
	mainQueue      = "work.queue"
	mainRoutingKey = "work"
	dlx            = "lab.dlx"
	dlq            = "work.dlq"
	dlRoutingKey   = "work.dead"
)

func main() {
	role := getenv("ROLE", "producer")
	switch role {
	case "producer":
		runProducer()
	case "consumer":
		runConsumer()
	default:
		fmt.Fprintf(os.Stderr, "unknown ROLE %q\n", role)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// topology
// ---------------------------------------------------------------------------

func dial() *amqp.Connection {
	url := getenv("RABBITMQ_URL", "amqp://lab:lab@rabbitmq:5672/")
	var conn *amqp.Connection
	var err error
	for i := 0; i < 30; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			return conn
		}
		time.Sleep(time.Second)
	}
	fmt.Fprintf(os.Stderr, "dial %s: %v\n", url, err)
	os.Exit(1)
	return nil
}

// declareTopology is idempotent: same exchange/queue names, types, and
// arguments every time, so a rerun with the same MODE never conflicts. The
// dead-letter exchange and queue always exist; only whether "work.queue"
// points at them (MODE=test) or not (MODE=break) changes.
func declareTopology(ch *amqp.Channel, dlqEnabled bool) error {
	if err := ch.ExchangeDeclare(dlx, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dlx: %w", err)
	}
	if _, err := ch.QueueDeclare(dlq, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dlq: %w", err)
	}
	if err := ch.QueueBind(dlq, dlRoutingKey, dlx, false, nil); err != nil {
		return fmt.Errorf("bind dlq: %w", err)
	}

	if err := ch.ExchangeDeclare(mainExchange, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare main exchange: %w", err)
	}
	args := amqp.Table{}
	if dlqEnabled {
		args["x-dead-letter-exchange"] = dlx
		args["x-dead-letter-routing-key"] = dlRoutingKey
	}
	if _, err := ch.QueueDeclare(mainQueue, true, false, false, false, args); err != nil {
		return fmt.Errorf("declare main queue: %w", err)
	}
	if err := ch.QueueBind(mainQueue, mainRoutingKey, mainExchange, false, nil); err != nil {
		return fmt.Errorf("bind main queue: %w", err)
	}
	return nil
}

// purgeTopology deletes the main and dead-letter queues if they exist, so a
// rerun with a different MODE does not hit a 406 PRECONDITION_FAILED from
// redeclaring a queue with different x-dead-letter-exchange arguments. Each
// delete gets its own short-lived channel: a NOT_FOUND error closes the
// channel it was issued on.
func purgeTopology(conn *amqp.Connection) {
	for _, q := range []string{mainQueue, dlq} {
		ch, err := conn.Channel()
		if err != nil {
			continue
		}
		_, _ = ch.QueueDelete(q, false, false, false)
		ch.Close()
	}
}

// ---------------------------------------------------------------------------
// producer
// ---------------------------------------------------------------------------

func runProducer() {
	mode := getenv("MODE", "break")
	count := getenvInt("COUNT", 30)
	poisonAt := getenvInt("POISON_AT", 5)

	conn := dial()
	defer conn.Close()

	purgeTopology(conn)

	ch, err := conn.Channel()
	must(err, "open channel")
	defer ch.Close()
	must(declareTopology(ch, mode == "test"), "declare topology")

	published := 0
	for seq := 1; seq <= count; seq++ {
		kind := "good"
		if seq == poisonAt {
			kind = "poison"
		}
		body := fmt.Sprintf("seq=%d kind=%s", seq, kind)
		headers := amqp.Table{
			"x-seq":  int32(seq),
			"x-kind": kind,
		}
		err := ch.PublishWithContext(context.Background(), mainExchange, mainRoutingKey, false, false, amqp.Publishing{
			ContentType:  "text/plain",
			Body:         []byte(body),
			Headers:      headers,
			DeliveryMode: amqp.Persistent,
			MessageId:    fmt.Sprintf("msg-%d", seq),
		})
		must(err, "publish")
		published++
	}
	fmt.Printf("producer: mode=%s published=%d poison_at=%d exchange=%s queue=%s dlq_on_queue=%v\n",
		mode, published, poisonAt, mainExchange, mainQueue, mode == "test")
}

// ---------------------------------------------------------------------------
// consumer
// ---------------------------------------------------------------------------

func runConsumer() {
	mode := getenv("MODE", "break")
	count := getenvInt("COUNT", 30)
	prefetch := getenvInt("PREFETCH", 5)
	maxAttempts := getenvInt("MAX_ATTEMPTS", 3)
	breakCap := getenvInt("BREAK_CAP", 20)
	processMs := getenvInt("PROCESS_MS", 5) // simulated work per good message, so throughput is meaningful

	conn := dial()
	defer conn.Close()

	ch, err := conn.Channel()
	must(err, "open channel")
	defer ch.Close()
	must(declareTopology(ch, mode == "test"), "declare topology")
	must(ch.Qos(prefetch, 0, false), "set qos")

	deliveries, err := ch.Consume(mainQueue, "lab-consumer", false, false, false, false, nil)
	must(err, "consume")

	var (
		goodProcessed   int
		poisonAttempts  int // redeliveries (break) or retry-header attempts (test)
		poisonResolved  bool
		producedOrder   []int
		completedOrder  []int
		start           = time.Now()
		firstDeliveryAt time.Time
	)
	for seq := 1; seq <= count; seq++ {
		producedOrder = append(producedOrder, seq)
	}

	timeout := time.After(60 * time.Second)
consumeLoop:
	for {
		select {
		case d, ok := <-deliveries:
			if !ok {
				break consumeLoop
			}
			if firstDeliveryAt.IsZero() {
				firstDeliveryAt = time.Now()
			}
			seq := headerInt(d.Headers, "x-seq")
			kind, _ := d.Headers["x-kind"].(string)

			if kind != "poison" {
				time.Sleep(time.Duration(processMs) * time.Millisecond) // simulated work
				_ = d.Ack(false)
				goodProcessed++
				completedOrder = append(completedOrder, seq)
				if goodProcessed >= count-1 && poisonResolved {
					break consumeLoop
				}
				continue
			}

			// poison message
			switch mode {
			case "break":
				poisonAttempts++
				if poisonAttempts >= breakCap {
					// One last requeue to stay faithful to "redelivers
					// forever, uncapped by the broker" -- we are the ones
					// stopping, not RabbitMQ.
					_ = d.Nack(false, true)
					fmt.Printf("consumer: break cap reached at %d redeliveries; poison message left in %s, requeued and unresolved\n", poisonAttempts, mainQueue)
					break consumeLoop
				}
				_ = d.Nack(false, true) // requeue: no DLX configured, stays in work.queue forever

			case "test":
				attempt := headerInt(d.Headers, "x-retry-count") + 1
				poisonAttempts = attempt
				if attempt < maxAttempts {
					retryHeaders := amqp.Table{
						"x-seq":         int32(seq),
						"x-kind":        "poison",
						"x-retry-count": int32(attempt),
					}
					pubErr := ch.PublishWithContext(context.Background(), mainExchange, mainRoutingKey, false, false, amqp.Publishing{
						ContentType:  d.ContentType,
						Body:         d.Body,
						Headers:      retryHeaders,
						DeliveryMode: amqp.Persistent,
						MessageId:    d.MessageId,
					})
					must(pubErr, "republish retry")
					_ = d.Ack(false) // remove the original; the retry re-enters at the tail
				} else {
					_ = d.Nack(false, false) // final attempt: dead-letter via x-dead-letter-exchange
					poisonResolved = true
					completedOrder = append(completedOrder, seq)
					fmt.Printf("consumer: attempt %d/%d exhausted; poison seq=%d dead-lettered to %s\n", attempt, maxAttempts, seq, dlq)
					if goodProcessed >= count-1 {
						break consumeLoop
					}
				}
			}

		case <-timeout:
			fmt.Println("consumer: 60s timeout waiting for deliveries; stopping")
			break consumeLoop
		}
	}

	elapsed := time.Since(start)
	throughput := 0.0
	if elapsed > 0 {
		throughput = float64(goodProcessed) / elapsed.Seconds()
	}

	fmt.Printf("\n=== consumer report: mode=%s ===\n", mode)
	fmt.Printf("good_processed=%d/%d poison_attempts=%d poison_resolved=%v elapsed=%s good_throughput=%.1f msg/s\n",
		goodProcessed, count-1, poisonAttempts, poisonResolved, elapsed.Round(time.Millisecond), throughput)
	fmt.Printf("produced_order:  %v\n", producedOrder)
	fmt.Printf("completed_order: %v\n", completedOrder)
	reportOrdering(producedOrder, completedOrder, poisonAt(count))
}

// poisonAt recomputes the poison position from the same default/env used by
// the producer, so the consumer's ordering report is self-contained.
func poisonAt(count int) int {
	return getenvInt("POISON_AT", 5)
}

func reportOrdering(produced, completed []int, poisonSeq int) {
	// Where did the poison message land in completion order versus where it
	// was produced? A queue that preserved order would complete it at the
	// same rank it was produced at (rank = its produced index).
	producedRank := -1
	for i, s := range produced {
		if s == poisonSeq {
			producedRank = i + 1
			break
		}
	}
	completedRank := -1
	for i, s := range completed {
		if s == poisonSeq {
			completedRank = i + 1
			break
		}
	}
	if completedRank == -1 {
		fmt.Printf("ordering: poison seq=%d produced at position %d of %d; never completed within this run\n",
			poisonSeq, producedRank, len(produced))
		return
	}
	fmt.Printf("ordering: poison seq=%d produced at position %d of %d; completed at position %d of %d (delta=%d)\n",
		poisonSeq, producedRank, len(produced), completedRank, len(completed), completedRank-producedRank)

	// Inversions among the non-poison messages: any pair whose completion
	// order disagrees with their produced order.
	inversions := 0
	for i := 0; i < len(completed); i++ {
		for j := i + 1; j < len(completed); j++ {
			if completed[i] > completed[j] {
				inversions++
			}
		}
	}
	fmt.Printf("ordering: inversions in completed_order=%d (0 means every completed message finished in produced order)\n", inversions)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func headerInt(h amqp.Table, key string) int {
	if v, ok := h[key]; ok {
		switch n := v.(type) {
		case int32:
			return int(n)
		case int64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
