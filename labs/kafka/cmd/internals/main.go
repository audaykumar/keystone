// Kafka internals lab driver.
//
// mode=produce writes keyed, run-tagged, sequence-numbered messages at a fixed
// rate and saves a manifest of acknowledged and failed IDs when the run ends.
//
// mode=consume joins a consumer group and prints partition assignment activity
// so rebalances are visible when members join or leave.
//
// mode=audit reads the manifest, scans every partition from the beginning, and
// reports missing acknowledged IDs, duplicates, and failed sends found in the log.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
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
	manifest := flag.String("manifest", "/evidence/run.json", "completed producer run manifest")
	verbose := flag.Bool("verbose", false, "log group rebalance activity")
	flag.Parse()

	brokers := strings.Split(env("KAFKA_BROKERS", "kafka1:9092,kafka2:9092,kafka3:9092"), ",")
	ctx := context.Background()

	switch *mode {
	case "produce":
		produce(ctx, brokers, *topic, *count, *rate, *acks, *keys, *manifest)
	case "consume":
		consume(ctx, brokers, *topic, *group, *verbose)
	case "audit":
		audit(ctx, brokers, *topic, *manifest)
	default:
		fatalf("unknown mode %q", *mode)
	}
}

func produce(ctx context.Context, brokers []string, topic string, count, rate int, acks string, keys int, manifestPath string) {
	f, err := os.Create(manifestPath)
	if err != nil {
		fatalf("create manifest: %v", err)
	}
	defer f.Close()
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		fatalf("run id: %v", err)
	}
	run := runManifest{RunID: fmt.Sprintf("%x", token), Topic: topic, Attempted: count}

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
			Value: []byte(fmt.Sprintf("%s:seq-%d", run.RunID, i)),
		})
		if err != nil {
			failed.Add(1)
			run.Failed = append(run.Failed, int64(i))
			fmt.Printf("write seq-%d failed: %v\n", i, err)
		} else {
			acked.Add(1)
			run.Acked = append(run.Acked, int64(i))
		}
		if time.Since(lastReport) >= time.Second {
			fmt.Printf("  acked=%d failed=%d\n", acked.Load(), failed.Load())
			lastReport = time.Now()
		}
		time.Sleep(interval)
	}
	fmt.Printf("done: acked=%d failed=%d\n", acked.Load(), failed.Load())
	if err := json.NewEncoder(f).Encode(run); err != nil {
		fatalf("save manifest: %v", err)
	}
	if err := f.Sync(); err != nil {
		fatalf("sync manifest: %v", err)
	}
	fmt.Printf("manifest saved: run=%s attempted=%d acked=%d failed=%d\n", run.RunID, run.Attempted, len(run.Acked), len(run.Failed))
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

type runManifest struct {
	RunID     string
	Topic     string
	Attempted int
	Acked     []int64
	Failed    []int64
}

func classify(run runManifest, counts map[int64]int) (missing []int64, duplicates, failedPresent int) {
	for _, n := range run.Acked {
		if counts[n] == 0 {
			missing = append(missing, n)
		}
	}
	for _, c := range counts {
		if c > 1 {
			duplicates += c - 1
		}
	}
	for _, n := range run.Failed {
		if counts[n] > 0 {
			failedPresent++
		}
	}
	return
}

func audit(ctx context.Context, brokers []string, topic, manifestPath string) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fatalf("read manifest: %v", err)
	}
	var run runManifest
	if err := json.Unmarshal(data, &run); err != nil {
		fatalf("incomplete/invalid manifest: %v", err)
	}
	if run.RunID == "" || run.Topic != topic || len(run.Acked)+len(run.Failed) != run.Attempted || len(run.Acked) == 0 {
		fatalf("manifest does not match a completed run for topic %s", topic)
	}

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
						if !errors.Is(err, io.EOF) {
							fatalf("audit batch: %v", err)
						}
						break
					}
					read = msg.Offset + 1
					if seq, ok := strings.CutPrefix(string(msg.Value), run.RunID+":seq-"); ok {
						n, err := strconv.ParseInt(seq, 10, 64)
						if err != nil {
							fatalf("invalid sequence in run %s: %q", run.RunID, seq)
						}
						perPartition[p.ID]++
						counts[n]++
					}
				}
				batch.Close()
			}
		}
		c.Close()
	}

	missing, dups, failedPresent := classify(run, counts)
	pids := make([]int, 0, len(perPartition))
	for p := range perPartition {
		pids = append(pids, p)
	}
	sort.Ints(pids)
	fmt.Printf("audit: topic=%s run=%s attempted=%d acked=%d failed=%d failed_present=%d\n", topic, run.RunID, run.Attempted, len(run.Acked), len(run.Failed), failedPresent)
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
		fmt.Println("RESULT: acknowledged sequences missing from this run. exit 1")
		os.Exit(1)
	}
	fmt.Println("RESULT: every acknowledged sequence is present; failed sends are separate. exit 0")
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
