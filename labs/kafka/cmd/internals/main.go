// Kafka internals lab driver.
//
// mode=produce  keyed, sequence-numbered messages at a fixed rate; counts acks
//               and failures so a broker kill mid-produce is measurable.
// mode=consume  consumer-group member; prints partition assignment activity so
//               rebalances are visible when members join or leave.
// mode=audit    scans every partition from the beginning and reports unique,
//               duplicate, and missing sequence numbers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	mode := flag.String("mode", "produce", "produce|consume|audit")
	topic := flag.String("topic", "payments", "topic name")
	count := flag.Int("count", 10000, "messages to produce / expected in audit")
	rate := flag.Int("rate", 500, "produce rate, messages per second")
	acks := flag.String("acks", "all", "producer acks: one|all")
	keys := flag.Int("keys", 10, "distinct message keys (accounts)")
	group := flag.String("group", "lab-consumers", "consumer group id")
	verbose := flag.Bool("verbose", false, "log group rebalance activity")
	flag.Parse()

	brokers := strings.Split(env("KAFKA_BROKERS", "kafka1:9092,kafka2:9092,kafka3:9092"), ",")
	ctx := context.Background()

	switch *mode {
	case "produce":
		produce(ctx, brokers, *topic, *count, *rate, *acks, *keys)
	case "consume":
		consume(ctx, brokers, *topic, *group, *verbose)
	case "audit":
		audit(ctx, brokers, *topic, *count)
	default:
		fatalf("unknown mode %q", *mode)
	}
}

func produce(ctx context.Context, brokers []string, topic string, count, rate int, acks string, keys int) {
	required := kafka.RequireAll
	if acks == "one" {
		required = kafka.RequireOne
	}
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: required,
		BatchTimeout: 20 * time.Millisecond,
		WriteTimeout: 5 * time.Second,
		// Allow retries to a new leader after a broker dies.
		MaxAttempts: 10,
	}
	defer w.Close()

	fmt.Printf("produce: %d messages at %d/s, acks=%s, %d keys, topic=%s\n", count, rate, acks, keys, topic)
	var acked, failed atomic.Int64
	interval := time.Second / time.Duration(rate)
	lastReport := time.Now()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("acct-%d", i%keys)
		err := w.WriteMessages(ctx, kafka.Message{
			Key:   []byte(key),
			Value: []byte(fmt.Sprintf("seq-%d", i)),
		})
		if err != nil {
			failed.Add(1)
			fmt.Printf("write seq-%d failed: %v\n", i, err)
		} else {
			acked.Add(1)
		}
		if time.Since(lastReport) >= time.Second {
			fmt.Printf("  acked=%d failed=%d\n", acked.Load(), failed.Load())
			lastReport = time.Now()
		}
		time.Sleep(interval)
	}
	fmt.Printf("done: acked=%d failed=%d\n", acked.Load(), failed.Load())
	fmt.Println("run 'make audit' to compare acked count against what the log actually holds")
}

func consume(ctx context.Context, brokers []string, topic, group string, verbose bool) {
	cfg := kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        group,
		CommitInterval: time.Second,
		StartOffset:    kafka.FirstOffset,
	}
	if verbose {
		cfg.Logger = kafka.LoggerFunc(func(msg string, args ...any) {
			line := fmt.Sprintf(msg, args...)
			// Surface only membership and assignment lines; the rest is noise.
			if strings.Contains(line, "rebalance") || strings.Contains(line, "assign") ||
				strings.Contains(line, "joined group") || strings.Contains(line, "Joined group") {
				fmt.Println("[group] " + line)
			}
		})
	}
	r := kafka.NewReader(cfg)
	defer r.Close()

	fmt.Printf("consume: group=%s topic=%s (start another member to trigger a rebalance)\n", group, topic)
	seen := map[int]int64{}
	total := 0
	lastReport := time.Now()
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				break
			}
			fmt.Printf("read error: %v\n", err)
			continue
		}
		if _, ok := seen[msg.Partition]; !ok {
			fmt.Printf("  now reading partition %d (first offset seen: %d)\n", msg.Partition, msg.Offset)
		}
		seen[msg.Partition] = msg.Offset
		total++
		if time.Since(lastReport) >= 2*time.Second {
			parts := make([]int, 0, len(seen))
			for p := range seen {
				parts = append(parts, p)
			}
			sort.Ints(parts)
			fmt.Printf("  consumed=%d partitions=%v\n", total, parts)
			lastReport = time.Now()
		}
	}
}

func audit(ctx context.Context, brokers []string, topic string, expect int) {
	conn, err := kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		fatalf("dial: %v", err)
	}
	parts, err := conn.ReadPartitions(topic)
	conn.Close()
	if err != nil {
		fatalf("read partitions: %v", err)
	}

	counts := map[int64]int{}
	perPartition := map[int]int{}
	for _, p := range parts {
		leader := net.JoinHostPort(p.Leader.Host, strconv.Itoa(p.Leader.Port))
		c, err := kafka.DialLeader(ctx, "tcp", leader, topic, p.ID)
		if err != nil {
			fatalf("dial leader for partition %d: %v", p.ID, err)
		}
		first, last, err := c.ReadOffsets()
		if err != nil {
			fatalf("offsets partition %d: %v", p.ID, err)
		}
		if last > first {
			c.Seek(first, kafka.SeekAbsolute)
			for read := first; read < last; {
				c.SetReadDeadline(time.Now().Add(10 * time.Second))
				batch := c.ReadBatch(1, 1<<20)
				for {
					msg, err := batch.ReadMessage()
					if err != nil {
						break
					}
					read = msg.Offset + 1
					perPartition[p.ID]++
					if seq, ok := strings.CutPrefix(string(msg.Value), "seq-"); ok {
						n, _ := strconv.ParseInt(seq, 10, 64)
						counts[n]++
					}
				}
				batch.Close()
			}
		}
		c.Close()
	}

	dups, missing := 0, []int64{}
	for n := int64(0); n < int64(expect); n++ {
		switch c := counts[n]; {
		case c == 0:
			missing = append(missing, n)
		case c > 1:
			dups += c - 1
		}
	}
	pids := make([]int, 0, len(perPartition))
	for p := range perPartition {
		pids = append(pids, p)
	}
	sort.Ints(pids)
	fmt.Printf("audit: topic=%s expected=%d\n", topic, expect)
	for _, p := range pids {
		fmt.Printf("  partition %d: %d messages\n", p, perPartition[p])
	}
	fmt.Printf("  unique sequences found: %d\n", len(counts))
	fmt.Printf("  duplicates: %d\n", dups)
	fmt.Printf("  missing: %d", len(missing))
	if n := len(missing); n > 0 && n <= 20 {
		fmt.Printf(" %v", missing)
	}
	fmt.Println()
	if len(missing) > 0 {
		fmt.Println("RESULT: acked messages are gone — the loss window is real. exit 1")
		os.Exit(1)
	}
	fmt.Println("RESULT: every expected sequence is present. exit 0")
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
