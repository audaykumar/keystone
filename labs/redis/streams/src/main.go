package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	streamName = "orders"
	groupName  = "processors"
	consumer1  = "consumer-1"
	recovery   = "consumer-recovery"

	// claimAfter is how long a message must be pending before XAUTOCLAIM can steal it.
	claimAfter = 2 * time.Second
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

func setup(ctx context.Context, rdb *redis.Client) {
	rdb.Del(ctx, streamName)
	// XGROUP CREATE with MKSTREAM creates the stream if it doesn't exist.
	err := rdb.XGroupCreateMkStream(ctx, streamName, groupName, "0").Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "XGroupCreateMkStream: %v\n", err)
		os.Exit(1)
	}
	logf(cyan, "  stream=%-10s  group=%s  created", streamName, groupName)
}

func produce(ctx context.Context, rdb *redis.Client, count int) []string {
	fmt.Println()
	logf(bold, "┄┄┄ producer: adding %d messages to stream ┄┄┄", count)
	fmt.Println()

	var ids []string
	for i := 1; i <= count; i++ {
		id, err := rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: streamName,
			Values: map[string]any{
				"order_id": fmt.Sprintf("ORD-%04d", i),
				"item":     fmt.Sprintf("item-%d", i%5+1),
				"qty":      i % 10 + 1,
			},
		}).Result()
		if err != nil {
			fmt.Fprintf(os.Stderr, "XADD: %v\n", err)
			continue
		}
		ids = append(ids, id)
		logf(cyan, "  XADD  id=%-22s  order=ORD-%04d", id, i)
	}
	return ids
}

// runBreak: consumer-1 reads messages, processes half, then crashes (simulated).
// The other half are left unacknowledged and accumulate in the PEL.
func runBreak(ctx context.Context, rdb *redis.Client, messageCount int) {
	setup(ctx, rdb)
	ids := produce(ctx, rdb, messageCount)
	_ = ids

	fmt.Println()
	logf(bold, "┄┄┄ consumer-1: reading %d messages, will crash halfway ┄┄┄", messageCount)
	fmt.Println()

	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    groupName,
		Consumer: consumer1,
		Streams:  []string{streamName, ">"},
		Count:    int64(messageCount),
		Block:    2 * time.Second,
	}).Result()
	if err != nil {
		fmt.Fprintf(os.Stderr, "XReadGroup: %v\n", err)
		os.Exit(1)
	}

	processed := 0
	crashAt := messageCount / 2

	for _, stream := range streams {
		for _, msg := range stream.Messages {
			if processed >= crashAt {
				logf(red, "  CRASH  consumer-1 crashed after %d messages — %d messages unacknowledged",
					processed, messageCount-processed)
				break
			}
			// Acknowledge successfully processed messages.
			rdb.XAck(ctx, streamName, groupName, msg.ID)
			logf(green, "  XACK   id=%-22s  order=%s", msg.ID, msg.Values["order_id"])
			processed++
		}
	}

	// Show PEL state after crash.
	fmt.Println()
	logf(bold, "┄┄┄ PEL (Pending Entry List) after crash ┄┄┄")
	fmt.Println()

	pending, err := rdb.XPending(ctx, streamName, groupName).Result()
	if err != nil {
		fmt.Fprintf(os.Stderr, "XPENDING: %v\n", err)
		os.Exit(1)
	}

	logf(yellow, "  XPENDING  total=%d  (messages read but never acknowledged)", pending.Count)
	for consumer, count := range pending.Consumers {
		logf(yellow, "  XPENDING  consumer=%-15s  stuck=%d", consumer, count)
	}

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  messages produced  : %d\n", messageCount)
	fmt.Printf("  messages processed : %d\n", processed)
	fmt.Printf("  messages in PEL    : %d  (stuck, will not be redelivered without intervention)\n", pending.Count)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  pending=%d  expected=0\n", pending.Count)

	if pending.Count > 0 {
		fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  %d messages stuck in PEL after consumer crash\n", pending.Count)
		fmt.Printf("  these messages will never be redelivered unless reclaimed with XAUTOCLAIM\n\n")
		os.Exit(1)
	}
}

// runFix: consumer-1 reads messages, crashes halfway. A recovery consumer uses
// XAUTOCLAIM to reclaim messages idle in PEL for more than claimAfter, then
// processes and acknowledges them.
func runFix(ctx context.Context, rdb *redis.Client, messageCount int) {
	setup(ctx, rdb)
	ids := produce(ctx, rdb, messageCount)
	_ = ids

	fmt.Println()
	logf(bold, "┄┄┄ consumer-1: reading %d messages, will crash halfway ┄┄┄", messageCount)
	fmt.Println()

	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    groupName,
		Consumer: consumer1,
		Streams:  []string{streamName, ">"},
		Count:    int64(messageCount),
		Block:    2 * time.Second,
	}).Result()
	if err != nil {
		fmt.Fprintf(os.Stderr, "XReadGroup: %v\n", err)
		os.Exit(1)
	}

	processed := 0
	crashAt := messageCount / 2
	for _, stream := range streams {
		for _, msg := range stream.Messages {
			if processed >= crashAt {
				logf(red, "  CRASH  consumer-1 crashed after %d messages", processed)
				break
			}
			rdb.XAck(ctx, streamName, groupName, msg.ID)
			logf(green, "  XACK   id=%-22s  order=%s", msg.ID, msg.Values["order_id"])
			processed++
		}
	}

	// Wait so messages become idle enough to claim.
	fmt.Println()
	logf(yellow, "  waiting %s for stuck messages to become claimable...", claimAfter)
	time.Sleep(claimAfter + 500*time.Millisecond)

	// Recovery consumer claims and processes stuck messages.
	fmt.Println()
	logf(bold, "┄┄┄ consumer-recovery: claiming stuck messages via XAUTOCLAIM ┄┄┄")
	fmt.Println()

	recovered := 0
	for {
		result, err := rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   streamName,
			Group:    groupName,
			Consumer: recovery,
			MinIdle:  claimAfter,
			Start:    "0-0",
			Count:    10,
		}).Result()
		if err != nil {
			fmt.Fprintf(os.Stderr, "XAUTOCLAIM: %v\n", err)
			break
		}
		if len(result.Messages) == 0 {
			break
		}
		for _, msg := range result.Messages {
			logf(cyan, "  XCLAIM id=%-22s  order=%s  (reclaimed from consumer-1)", msg.ID, msg.Values["order_id"])
			// Idempotent processing: same order_id handled again after crash.
			rdb.XAck(ctx, streamName, groupName, msg.ID)
			logf(green, "  XACK   id=%-22s  order=%s  (recovered)", msg.ID, msg.Values["order_id"])
			recovered++
			processed++
		}
		if result.NextStartID == "0-0" {
			break
		}
	}

	pending, _ := rdb.XPending(ctx, streamName, groupName).Result()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  messages produced  : %d\n", messageCount)
	fmt.Printf("  processed normally : %d\n", processed-recovered)
	fmt.Printf("  recovered via claim: %d\n", recovered)
	fmt.Printf("  total processed    : %d\n", processed)
	fmt.Printf("  remaining in PEL   : %d\n", pending.Count)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  total-processed=%d  expected=%d  pending=%d\n",
		processed, messageCount, pending.Count)

	if processed == messageCount && pending.Count == 0 {
		fmt.Printf("\n  "+green+bold+"✓ PASSED"+reset+"  all %d messages processed after recovery\n", messageCount)
		fmt.Printf("  XAUTOCLAIM recovered %d stuck messages from crashed consumer-1\n\n", recovered)
	} else {
		fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  processed=%d  expected=%d  pending=%d\n\n",
			processed, messageCount, pending.Count)
		os.Exit(1)
	}
}

func main() {
	mode     := flag.String("mode", "break", "break | fix")
	messages := flag.Int("messages", 20, "number of messages to produce")
	check    := flag.Bool("check", false, "exit 1 if invariant violated")
	flag.Parse()

	// streams always exits non-zero on violation; -check is accepted for CLI consistency

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
	fmt.Println(bold + "  REDIS STREAMS LAB — CONSUMER CRASH & PEL RECOVERY" + reset)
	fmt.Printf("  mode=%-8s  messages=%d  group=%s  claim-after=%s\n",
		*mode, *messages, groupName, claimAfter)
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)

	switch *mode {
	case "break":
		runBreak(ctx, rdb, *messages)
	case "fix":
		runFix(ctx, rdb, *messages)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s\n", *mode)
		os.Exit(1)
	}
}
