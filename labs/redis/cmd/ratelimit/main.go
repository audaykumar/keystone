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

// Fixed-window limiter using clock-aligned windows (e.g. [0,5s), [5s,10s)...).
//
// Clock alignment is required to reproduce the boundary burst. Without it, the
// phase-1 entries and the fixed-window expiry both happen at t=windowSec, so
// the sliding window also resets at the same instant and the two limiters look
// identical.
//
// Returns {ok, count, seconds-until-reset}
const fixedWindowScript = `
local limit    = tonumber(ARGV[1])
local windowSec = tonumber(ARGV[2])
local nowSec   = math.floor(tonumber(ARGV[3]) / 1000)
local winStart = nowSec - (nowSec % windowSec)
local key      = KEYS[1] .. ':' .. tostring(winStart)
local count    = redis.call('INCR', key)
if count == 1 then
    redis.call('EXPIRE', key, windowSec * 2)
end
local remaining = windowSec - (nowSec % windowSec)
if count > limit then
    return {0, count, remaining}
end
return {1, count, remaining}
`

// Sliding-window limiter using a sorted set. Score = request time in ms.
// No clock alignment needed — rolling by design.
const slidingWindowScript = `
local key      = KEYS[1]
local limit    = tonumber(ARGV[1])
local now      = tonumber(ARGV[2])
local windowMs = tonumber(ARGV[3])
local uid      = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - windowMs)
local count = redis.call('ZCARD', key)
if count >= limit then
    return {0, count}
end
redis.call('ZADD', key, now, uid)
redis.call('PEXPIRE', key, windowMs)
return {1, count + 1}
`

const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	bold   = "\033[1m"
	dim    = "\033[2m"
)

var (
	start        time.Time
	printMu      sync.Mutex
	phase1Allowed atomic.Int64
	phase2Allowed atomic.Int64
	phase2Denied  atomic.Int64
)

func ts() string {
	return fmt.Sprintf("%7dms", time.Since(start).Milliseconds())
}

func logLine(color, format string, args ...any) {
	printMu.Lock()
	defer printMu.Unlock()
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[%s] %s%s%s\n", ts(), color, msg, reset)
}

// nextWindowBoundary returns the next clock-aligned window boundary after now.
func nextWindowBoundary(windowSec int) time.Time {
	now := time.Now().Unix()
	w := int64(windowSec)
	nextSec := (now/w + 1) * w
	return time.Unix(nextSec, 0)
}

// runFixed demonstrates the clock-aligned fixed-window boundary burst.
//
// Phase 1: send limit requests just BEFORE the window boundary (fill the window).
// Phase 2: send limit requests just AFTER the boundary (new window resets counter).
// Both phases run within a ~400ms real-time span, but the fixed-window counter
// sees them as separate windows — each at count=1 — allowing 2× limit.
func runFixed(ctx context.Context, rdb *redis.Client, limit int, windowSec int) {
	rdb.FlushDB(ctx)

	boundary := nextWindowBoundary(windowSec)
	waitFor := time.Until(boundary) - 200*time.Millisecond
	fmt.Printf("\n  next window boundary at %s\n", boundary.Format("15:04:05"))
	if waitFor > 0 {
		logLine(yellow, "  waiting %s to reach boundary...", waitFor.Round(time.Millisecond))
		time.Sleep(waitFor)
	}

	fmt.Printf("\n%s┄┄┄ phase 1: %d requests BEFORE window boundary ┄┄┄%s\n\n", bold, limit, reset)

	windowKey := "rl:fixed"
	for i := 0; i < limit; i++ {
		nowMs := time.Now().UnixMilli()
		res, err := rdb.Eval(ctx, fixedWindowScript,
			[]string{windowKey}, limit, windowSec, nowMs).Int64Slice()
		if err != nil || len(res) < 3 {
			logLine(red, "  error: %v", err)
			continue
		}
		ok, count, remaining := res[0], res[1], res[2]
		if ok == 1 {
			phase1Allowed.Add(1)
			logLine(green, "  ALLOW  req=%-3d  count=%d/%d  window-resets-in=%ds", i+1, count, limit, remaining)
		} else {
			logLine(red, "  DENY   req=%-3d  count=%d/%d  window-resets-in=%ds", i+1, count, limit, remaining)
		}
	}

	// Wait until 200ms after the boundary — new fixed window has started.
	waitFor2 := time.Until(boundary) + 200*time.Millisecond
	if waitFor2 > 0 {
		logLine(yellow, "\n  waiting %s past boundary — fixed window resets NOW...", (200 * time.Millisecond).String())
		time.Sleep(waitFor2)
	}

	fmt.Printf("\n%s┄┄┄ phase 2: %d requests AFTER window boundary (burst) ┄┄┄%s\n\n", bold, limit, reset)
	logLine(yellow, "  fixed window counter reset to 0 — new window open")
	fmt.Println()

	for i := 0; i < limit; i++ {
		nowMs := time.Now().UnixMilli()
		res, err := rdb.Eval(ctx, fixedWindowScript,
			[]string{windowKey}, limit, windowSec, nowMs).Int64Slice()
		if err != nil || len(res) < 3 {
			logLine(red, "  error: %v", err)
			continue
		}
		ok, count, remaining := res[0], res[1], res[2]
		if ok == 1 {
			phase2Allowed.Add(1)
			logLine(green, "  ALLOW  burst=%-3d  count=%d/%d  window-resets-in=%ds", i+1, count, limit, remaining)
		} else {
			phase2Denied.Add(1)
			logLine(red, "  DENY   burst=%-3d  count=%d/%d  window-resets-in=%ds", i+1, count, limit, remaining)
		}
	}
}

// runSliding demonstrates that a sorted-set sliding window prevents the same burst.
//
// Same boundary timing as runFixed. At the boundary, the sliding window still
// sees the phase-1 entries (they're only ~400ms old in a windowSec window),
// so phase-2 requests are rejected — no burst.
func runSliding(ctx context.Context, rdb *redis.Client, limit int, windowSec int) {
	rdb.FlushDB(ctx)
	windowMs := int64(windowSec) * 1000

	boundary := nextWindowBoundary(windowSec)
	waitFor := time.Until(boundary) - 200*time.Millisecond
	fmt.Printf("\n  next window boundary at %s\n", boundary.Format("15:04:05"))
	if waitFor > 0 {
		logLine(yellow, "  waiting %s to same boundary point as fixed-window test...", waitFor.Round(time.Millisecond))
		time.Sleep(waitFor)
	}

	fmt.Printf("\n%s┄┄┄ phase 1: %d requests BEFORE boundary ┄┄┄%s\n\n", bold, limit, reset)

	windowKey := "rl:sliding"
	for i := 0; i < limit; i++ {
		now := time.Now().UnixMilli()
		uid := fmt.Sprintf("p1-%d-%d", i, now)
		res, err := rdb.Eval(ctx, slidingWindowScript,
			[]string{windowKey}, limit, now, windowMs, uid).Int64Slice()
		if err != nil || len(res) < 2 {
			logLine(red, "  error: %v", err)
			continue
		}
		ok, count := res[0], res[1]
		if ok == 1 {
			phase1Allowed.Add(1)
			logLine(green, "  ALLOW  req=%-3d  window-count=%d/%d", i+1, count, limit)
		} else {
			logLine(red, "  DENY   req=%-3d  window-count=%d/%d", i+1, count, limit)
		}
	}

	// Same wait as fixed-window test — boundary has passed but phase-1 entries
	// are only ~400ms old, well within the windowMs sliding window.
	waitFor2 := time.Until(boundary) + 200*time.Millisecond
	if waitFor2 > 0 {
		time.Sleep(waitFor2)
	}

	fmt.Printf("\n%s┄┄┄ phase 2: %d requests AFTER boundary ┄┄┄%s\n\n", bold, limit, reset)
	logLine(yellow, "  sliding window still contains phase-1 entries (only ~400ms old, window=%ds)", windowSec)
	fmt.Println()

	for i := 0; i < limit; i++ {
		now := time.Now().UnixMilli()
		uid := fmt.Sprintf("p2-%d-%d", i, now)
		res, err := rdb.Eval(ctx, slidingWindowScript,
			[]string{windowKey}, limit, now, windowMs, uid).Int64Slice()
		if err != nil || len(res) < 2 {
			logLine(red, "  error: %v", err)
			continue
		}
		ok, count := res[0], res[1]
		if ok == 1 {
			phase2Allowed.Add(1)
			logLine(green, "  ALLOW  burst=%-3d  window-count=%d/%d", i+1, count, limit)
		} else {
			phase2Denied.Add(1)
			logLine(red, "  DENY   burst=%-3d  window-count=%d/%d  (phase-1 still counted)", i+1, count, limit)
		}
	}
}

func main() {
	mode   := flag.String("mode", "fixed", "fixed | sliding")
	limit  := flag.Int("limit", 8, "max requests per window")
	window := flag.Int("window", 10, "window size in seconds (clock-aligned for fixed)")
	check  := flag.Bool("check", false, "exit 1 if invariant violated")
	flag.Parse()

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
	fmt.Println(bold + "  REDIS RATE LIMIT LAB — BOUNDARY BURST" + reset)
	fmt.Printf("  mode=%-10s  limit=%d/window  window=%ds\n", *mode, *limit, *window)
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)
	fmt.Printf("\n  scenario: %d requests before boundary, %d after — same %dms real-time span\n",
		*limit, *limit, 400)
	fmt.Printf("  fixed-window: counter resets → both bursts allowed (2× limit)\n")
	fmt.Printf("  sliding-window: phase-1 entries still counted → phase-2 rejected\n")

	switch *mode {
	case "fixed":
		runFixed(ctx, rdb, *limit, *window)
	case "sliding":
		runSliding(ctx, rdb, *limit, *window)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s\n", *mode)
		os.Exit(1)
	}

	p1a := phase1Allowed.Load()
	p2a := phase2Allowed.Load()
	burst := p1a + p2a

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  phase-1 allowed : %d  (before boundary)\n", p1a)
	fmt.Printf("  phase-2 allowed : %d  (after boundary)\n", p2a)
	fmt.Printf("  phase-2 denied  : %d\n", phase2Denied.Load())
	fmt.Printf("  total in ~400ms span: %d  (limit=%d)\n", burst, *limit)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  burst=%d  limit=%d  mode=%s\n", burst, *limit, *mode)

	switch *mode {
	case "fixed":
		if burst > int64(*limit) {
			fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  fixed-window burst: %d requests in ~400ms span (limit=%d)\n", burst, *limit)
			fmt.Printf("  counter reset at boundary allowed phase-2 as a fresh window\n\n")
			if *check {
				os.Exit(1)
			}
		} else {
			fmt.Printf("\n  "+yellow+"! timing missed boundary — re-run"+reset+"\n\n")
		}
	case "sliding":
		if burst <= int64(*limit) {
			fmt.Printf("\n  "+green+bold+"✓ PASSED"+reset+"  sliding window held: %d total in ~400ms (limit=%d)\n", burst, *limit)
			fmt.Printf("  phase-1 entries still in window prevented phase-2 burst\n\n")
		} else {
			fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  %d requests slipped through (limit=%d)\n\n", burst, *limit)
			if *check {
				os.Exit(1)
			}
		}
	}
}
