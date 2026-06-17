package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	channel    = "events"
	streamName = "events-stream"
	groupName  = "readers"
)

const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	bold   = "\033[1m"
	dim    = "\033[2m"
)

var start time.Time

func ts() string {
	return fmt.Sprintf("%6dms", time.Since(start).Milliseconds())
}

func logf(color, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[%s] %s%s%s\n", ts(), color, msg, reset)
}

// runPubSub demonstrates fire-and-forget: Pub/Sub has no persistence.
// Subscriber B goes offline during the middle third of messages and
// misses them permanently — there is no way to replay them.
func runPubSub(ctx context.Context, rdb *redis.Client, messages int) {
	var (
		receivedA atomic.Int64
		receivedB atomic.Int64
		subBOnline atomic.Bool
	)
	subBOnline.Store(true)

	third := messages / 3

	// Subscriber A: always online.
	subA := rdb.Subscribe(ctx, channel)
	go func() {
		chA := subA.Channel()
		for msg := range chA {
			receivedA.Add(1)
			logf(green, "  SUB-A  received  %s", msg.Payload)
		}
	}()

	// Subscriber B: goes offline after the first third of messages.
	subB := rdb.Subscribe(ctx, channel)
	go func() {
		chB := subB.Channel()
		for msg := range chB {
			if !subBOnline.Load() {
				break
			}
			receivedB.Add(1)
			logf(cyan, "  SUB-B  received  %s", msg.Payload)
		}
	}()

	// Give subscribers time to connect.
	time.Sleep(200 * time.Millisecond)

	fmt.Println()
	logf(bold, "┄┄┄ publisher: sending %d messages ┄┄┄", messages)
	fmt.Println()

	for i := 1; i <= messages; i++ {
		payload := fmt.Sprintf("order:ORD-%04d", i)

		if i == third+1 {
			logf(yellow, "  SUB-B  went offline — will miss messages %d-%d", i, third*2)
			subBOnline.Store(false)
			subB.Close()
		}
		if i == third*2+1 {
			logf(yellow, "  SUB-B  reconnected — but missed messages are gone forever")
			// Reconnect — but messages sent while offline are lost.
			subB = rdb.Subscribe(ctx, channel)
			subBOnline.Store(true)
			go func() {
				chB := subB.Channel()
				for msg := range chB {
					if !subBOnline.Load() {
						break
					}
					receivedB.Add(1)
					logf(cyan, "  SUB-B  received  %s  (after reconnect)", msg.Payload)
				}
			}()
			time.Sleep(100 * time.Millisecond)
		}

		rdb.Publish(ctx, channel, payload)
		logf(dim, "  PUB    sent     %s", payload)
		time.Sleep(50 * time.Millisecond)
	}

	// Allow final messages to flush.
	time.Sleep(300 * time.Millisecond)
	subA.Close()
	subB.Close()
	subBOnline.Store(false)
	time.Sleep(100 * time.Millisecond)

	missed := int64(messages) - receivedB.Load()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  messages published : %d\n", messages)
	fmt.Printf("  SUB-A received     : %d  (always online)\n", receivedA.Load())
	fmt.Printf("  SUB-B received     : %d  (went offline for %d messages)\n", receivedB.Load(), third)
	fmt.Printf("  SUB-B missed       : %d  (permanently lost — no replay possible)\n", missed)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  sub-b-received=%d  expected=%d\n", receivedB.Load(), messages)

	if missed > 0 {
		fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  SUB-B missed %d messages\n", missed)
		fmt.Printf("  Pub/Sub is fire-and-forget: no buffer, no replay, no persistence\n\n")
		os.Exit(1)
	} else {
		fmt.Printf("\n  "+green+bold+"✓ PASSED"+reset+"  SUB-B received all %d messages\n\n", messages)
	}
}

// runStreams demonstrates that a stream consumer can reconnect and catch up.
// The same "offline" scenario that breaks Pub/Sub is recoverable with streams.
func runStreams(ctx context.Context, rdb *redis.Client, messages int) {
	rdb.Del(ctx, streamName)
	rdb.XGroupCreateMkStream(ctx, streamName, groupName, "0")

	third := messages / 3
	logf(cyan, "  stream=%-12s  group=%s  created", streamName, groupName)

	fmt.Println()
	logf(bold, "┄┄┄ producer: adding %d messages to stream ┄┄┄", messages)
	fmt.Println()

	// Produce all messages up front (simulates messages arriving while consumer is offline).
	for i := 1; i <= messages; i++ {
		rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: streamName,
			Values: map[string]any{"order": fmt.Sprintf("ORD-%04d", i)},
		})
		logf(dim, "  XADD   order=ORD-%04d", i)
		time.Sleep(20 * time.Millisecond)
	}

	// Consumer A: processes first and last thirds.
	fmt.Println()
	logf(bold, "┄┄┄ consumer-A: reads all — streams buffer messages while offline ┄┄┄")
	fmt.Println()

	processedA := 0
	for processedA < messages {
		streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    groupName,
			Consumer: "consumer-a",
			Streams:  []string{streamName, ">"},
			Count:    10,
			Block:    500 * time.Millisecond,
		}).Result()
		if err == redis.Nil || (err != nil && err.Error() == "redis: nil") {
			break
		}
		if err != nil {
			break
		}
		for _, s := range streams {
			for _, msg := range s.Messages {
				rdb.XAck(ctx, streamName, groupName, msg.ID)
				processedA++
				if processedA == third {
					logf(yellow, "  OFFLINE  consumer-A went offline at message %d", processedA)
				}
				if processedA == third*2 {
					logf(yellow, "  ONLINE   consumer-A reconnected at message %d — catching up", processedA)
				}
				logf(green, "  XACK   id=%-22s  order=%s", msg.ID, msg.Values["order"])
			}
		}
	}

	pending, _ := rdb.XPending(ctx, streamName, groupName).Result()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  messages produced  : %d\n", messages)
	fmt.Printf("  consumer-A processed: %d  (including %d 'missed' while offline)\n",
		processedA, messages-third*2+third)
	fmt.Printf("  remaining in PEL   : %d\n", pending.Count)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  processed=%d  expected=%d\n", processedA, messages)

	if processedA == messages && pending.Count == 0 {
		fmt.Printf("\n  "+green+bold+"✓ PASSED"+reset+"  all %d messages processed after 'reconnect'\n", messages)
		fmt.Printf("  streams buffer messages — offline consumers catch up on reconnect\n")
		fmt.Printf("  contrast: Pub/Sub (make break) loses those same messages permanently\n\n")
	} else {
		fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  processed=%d  pending=%d\n\n",
			processedA, pending.Count)
		os.Exit(1)
	}
}

func main() {
	mode     := flag.String("mode", "pubsub", "pubsub | streams")
	messages := flag.Int("messages", 30, "number of messages to publish")
	check    := flag.Bool("check", false, "exit 1 if invariant violated")
	flag.Parse()

	// pubsub always exits non-zero on violation; -check is accepted for CLI consistency

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "redis unreachable at %s: %v\n", addr, err)
		os.Exit(1)
	}

	start = time.Now()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)
	fmt.Println(bold + "  REDIS PUB/SUB LAB — FIRE-AND-FORGET vs STREAMS" + reset)
	fmt.Printf("  mode=%-10s  messages=%d  channel=%s\n", *mode, *messages, channel)
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		switch *mode {
		case "pubsub":
			runPubSub(ctx, rdb, *messages)
		case "streams":
			runStreams(ctx, rdb, *messages)
		default:
			fmt.Fprintf(os.Stderr, "unknown mode: %s\n", *mode)
			os.Exit(1)
		}
	}()
	wg.Wait()
}
