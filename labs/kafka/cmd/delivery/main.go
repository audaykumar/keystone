// Delivery semantics lab driver.
//
// mode=produce  emit N payment events (event id + fixed amount) to Kafka.
// mode=consume  apply events to a Postgres balance under one of three
//               semantics, with an optional crash injected mid-stream:
//                 atmost      commit offset, then apply  -> crash loses events
//                 atleast     apply, then commit offset  -> crash duplicates events
//                 idempotent  atleast + processed_events dedupe in the same
//                              DB transaction              -> crash changes nothing
// mode=verify   compare the balance against expected; exit 1 on mismatch.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

const amountCents = 100 // every event credits exactly one dollar

func main() {
	mode := flag.String("mode", "produce", "produce|consume|verify")
	topic := flag.String("topic", "payment-events", "topic name")
	count := flag.Int("count", 1000, "events to produce / expected in verify")
	semantics := flag.String("semantics", "atleast", "atmost|atleast|idempotent")
	crashAt := flag.Int("crash-at", 0, "crash after applying this many events (0 = never)")
	group := flag.String("group", "ledger-appliers", "consumer group id")
	idle := flag.Int("idle", 5, "seconds without messages before the consumer exits")
	flag.Parse()

	brokers := strings.Split(env("KAFKA_BROKERS", "kafka:9092"), ",")
	ctx := context.Background()

	switch *mode {
	case "produce":
		produce(ctx, brokers, *topic, *count)
	case "consume":
		consume(ctx, brokers, *topic, *group, *semantics, *crashAt, time.Duration(*idle)*time.Second)
	case "verify":
		verify(ctx, *count)
	default:
		fatalf("unknown mode %q", *mode)
	}
}

func produce(ctx context.Context, brokers []string, topic string, count int) {
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		BatchTimeout: 10 * time.Millisecond,
	}
	defer w.Close()

	fmt.Printf("produce: %d payment events of %d cents each\n", count, amountCents)
	batch := make([]kafka.Message, 0, 100)
	for i := 0; i < count; i++ {
		batch = append(batch, kafka.Message{
			Key:   []byte("acct-1"),
			Value: []byte(fmt.Sprintf("pay-%d", i)),
		})
		if len(batch) == cap(batch) || i == count-1 {
			if err := w.WriteMessages(ctx, batch...); err != nil {
				fatalf("write: %v", err)
			}
			batch = batch[:0]
		}
	}
	fmt.Printf("done: %d events. expected final balance: %d cents\n", count, count*amountCents)
}

func consume(ctx context.Context, brokers []string, topic, group, semantics string, crashAt int, idle time.Duration) {
	pool := connect(ctx)
	defer pool.Close()

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     group,
		StartOffset: kafka.FirstOffset,
	})
	defer r.Close()

	fmt.Printf("consume: semantics=%s crash-at=%d\n", semantics, crashAt)
	applied, skipped := 0, 0
	var lastMsg kafka.Message
	uncommitted := 0
	commit := func() {
		if uncommitted == 0 {
			return
		}
		if err := r.CommitMessages(ctx, lastMsg); err != nil {
			fatalf("commit: %v", err)
		}
		uncommitted = 0
	}

	// Joining the group and receiving an assignment can take a while; only
	// after the first message does the short idle timeout make sense.
	fetchTimeout := 60 * time.Second
	for {
		fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
		msg, err := r.FetchMessage(fetchCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				commit()
				fmt.Printf("idle, stopping. applied=%d skipped-as-duplicate=%d\n", applied, skipped)
				return
			}
			fatalf("fetch: %v", err)
		}
		fetchTimeout = idle
		eventID := string(msg.Value)

		if semantics == "atmost" {
			// Offset first: once committed, a crash means the event is never applied.
			if err := r.CommitMessages(ctx, msg); err != nil {
				fatalf("commit: %v", err)
			}
			if crashAt > 0 && applied+1 == crashAt {
				// The at-most-once loss window: the offset is committed, the
				// work is not done. The restarted member resumes after this
				// event, which is now gone forever.
				fmt.Printf("CRASH injected after committing the offset for %s, before applying it\n", eventID)
				os.Exit(1)
			}
		}

		ok, err := apply(ctx, pool, eventID, semantics == "idempotent")
		if err != nil {
			fatalf("apply %s: %v", eventID, err)
		}
		if ok {
			applied++
		} else {
			skipped++
		}

		if crashAt > 0 && applied == crashAt {
			// Simulated crash between processing and offset commit (or after
			// commit for atmost). os.Exit skips every deferred cleanup,
			// exactly like a SIGKILL would. Everything applied since the last
			// batch commit will be redelivered to the next group member.
			fmt.Printf("CRASH injected after applying %d events, before committing the offset\n", applied)
			os.Exit(1)
		}

		if semantics != "atmost" {
			// Batch commits every 100 messages, like a real consumer would.
			// This widens the replay window a crash leaves behind.
			lastMsg = msg
			uncommitted++
			if uncommitted >= 100 {
				commit()
			}
		}
	}
}

// apply credits the balance. With dedupe enabled, the processed_events insert
// and the balance update share one transaction: replaying an event becomes a
// no-op instead of a double credit.
func apply(ctx context.Context, pool *pgxpool.Pool, eventID string, dedupe bool) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if dedupe {
		tag, err := tx.Exec(ctx,
			`INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 0 {
			return false, tx.Commit(ctx) // already applied; skip the credit
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE balances SET balance_cents = balance_cents + $1 WHERE account_id = 'acct-1'`,
		amountCents); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func verify(ctx context.Context, count int) {
	pool := connect(ctx)
	defer pool.Close()

	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT balance_cents FROM balances WHERE account_id = 'acct-1'`).Scan(&balance); err != nil {
		fatalf("read balance: %v", err)
	}
	expected := int64(count * amountCents)
	fmt.Printf("verify: balance=%d cents, expected=%d cents\n", balance, expected)
	switch {
	case balance == expected:
		fmt.Println("RESULT: exact. every event applied exactly once. exit 0")
	case balance > expected:
		fmt.Printf("RESULT: overshoot of %d cents — duplicated events (at-least-once without dedupe). exit 1\n", balance-expected)
		os.Exit(1)
	default:
		fmt.Printf("RESULT: shortfall of %d cents — lost events (at-most-once). exit 1\n", expected-balance)
		os.Exit(1)
	}
}

func connect(ctx context.Context) *pgxpool.Pool {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatalf("DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatalf("connect: %v", err)
	}
	return pool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
