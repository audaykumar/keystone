package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	lockResource = "lock:inventory"

	// lockTTL expires before workDuration in both modes.
	// This causes concurrent occupancy in both modes — that is intentional.
	// The invariant being tested is wrong-owner DEL, not mutual exclusion.
	lockTTL      = 200 * time.Millisecond
	workDuration = 350 * time.Millisecond // intentionally longer than lockTTL
	retryDelay   = 10 * time.Millisecond
)

// releaseLua atomically checks ownership before deleting.
// Returns 1 if deleted (owned by caller), 0 if token mismatch (already expired and re-acquired).
const releaseLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
else
    return 0
end
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
	successOps   atomic.Int64
	wrongOwnerDEL atomic.Int64 // naive: DEL fired when lock belonged to someone else

	// occupancy is informational — both modes show violations due to TTL expiry.
	occupancyMu  sync.Mutex
	occupancy    int
	maxOccupancy atomic.Int64
)

func ts() string {
	return fmt.Sprintf("%6dms", time.Since(start).Milliseconds())
}

func logLine(color, format string, args ...any) {
	printMu.Lock()
	defer printMu.Unlock()
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[%s] %s%s%s\n", ts(), color, msg, reset)
}

func enterCritical(workerID int) {
	occupancyMu.Lock()
	occupancy++
	curr := occupancy
	occupancyMu.Unlock()

	if int64(curr) > maxOccupancy.Load() {
		maxOccupancy.Store(int64(curr))
	}
	if curr > 1 {
		logLine(yellow, "  ENTER critical  worker=%-3d  occupancy=%d  (lock expired mid-work — concurrent)", workerID, curr)
	} else {
		logLine(cyan, "  ENTER critical  worker=%-3d  occupancy=%d", workerID, curr)
	}
}

func exitCritical(workerID int) {
	occupancyMu.Lock()
	occupancy--
	occupancyMu.Unlock()
	logLine(dim, "  EXIT  critical  worker=%-3d", workerID)
}

func acquireLock(ctx context.Context, rdb *redis.Client, token string) bool {
	ok, err := rdb.SetNX(ctx, lockResource, token, lockTTL).Result()
	return err == nil && ok
}

// runNaive uses plain DEL — deletes the lock regardless of who currently owns it.
// When work (350ms) outlasts the lock TTL (200ms), the lock expires and another
// worker acquires it. When the original holder wakes up and calls DEL, it deletes
// the new holder's lock — making a third worker able to acquire immediately.
// This is wrong-owner DEL. It is distinct from, though related to, concurrent occupancy.
func runNaive(ctx context.Context, rdb *redis.Client, workerID int, iterations int) {
	for i := 0; i < iterations; i++ {
		token := fmt.Sprintf("w%d-i%d", workerID, i)

		for {
			if acquireLock(ctx, rdb, token) {
				break
			}
			time.Sleep(retryDelay)
		}
		logLine(green, "  LOCK acquired   worker=%-3d  token=%s", workerID, token)

		enterCritical(workerID)
		time.Sleep(workDuration + time.Duration(rand.Intn(30))*time.Millisecond)
		successOps.Add(1)
		exitCritical(workerID)

		// Check who owns the lock before deleting.
		// If not us, this DEL will erase a different worker's lock.
		currentOwner, _ := rdb.Get(ctx, lockResource).Result()
		if currentOwner != token && currentOwner != "" {
			wrongOwnerDEL.Add(1)
			logLine(red, "  WRONG-OWNER DEL worker=%-3d  my-token=%s  lock-owner=%s  ← deleting successor's lock",
				workerID, token, currentOwner)
		} else {
			logLine(yellow, "  DEL lock        worker=%-3d  token=%s", workerID, token)
		}
		rdb.Del(ctx, lockResource)
	}
}

// runSafe uses Lua compare-and-delete. The lock is only deleted if the stored
// token still matches. If the lock expired and was re-acquired, Lua returns 0
// and the holder's lock is preserved.
//
// NOTE: work duration is the same as naive (350ms > lockTTL 200ms). Concurrent
// occupancy still occurs — Lua CAD does NOT prevent that. What it prevents is
// a holder deleting a successor's lock after the TTL expires.
func runSafe(ctx context.Context, rdb *redis.Client, workerID int, iterations int) {
	for i := 0; i < iterations; i++ {
		token := fmt.Sprintf("w%d-i%d", workerID, i)

		for {
			if acquireLock(ctx, rdb, token) {
				break
			}
			time.Sleep(retryDelay)
		}
		logLine(green, "  LOCK acquired   worker=%-3d  token=%s", workerID, token)

		enterCritical(workerID)
		time.Sleep(workDuration + time.Duration(rand.Intn(30))*time.Millisecond)
		successOps.Add(1)
		exitCritical(workerID)

		// Lua atomic check: only DEL if we still own it.
		released, _ := rdb.Eval(ctx, releaseLua, []string{lockResource}, token).Int()
		if released == 1 {
			logLine(cyan, "  LOCK released   worker=%-3d  token=%s  (owned — deleted)", workerID, token)
		} else {
			logLine(dim, "  LOCK expired    worker=%-3d  token=%s  (expired — Lua skipped DEL, successor's lock safe)",
				workerID, token)
		}
	}
}

func main() {
	mode       := flag.String("mode", "naive", "naive | safe")
	workers    := flag.Int("workers", 6, "concurrent workers competing for the lock")
	iterations := flag.Int("iterations", 20, "lock-acquire cycles per worker")
	check      := flag.Bool("check", false, "exit 1 if invariant violated")
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
	rdb.Del(ctx, lockResource)

	start = time.Now()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)
	fmt.Println(bold + "  REDIS LOCKS LAB — WRONG-OWNER DEL" + reset)
	fmt.Printf("  mode=%-8s  workers=%-3d  iterations=%d  lock-ttl=%s  work=%s\n",
		*mode, *workers, *iterations, lockTTL, workDuration)
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)
	fmt.Printf("\n  %sNOTE: work (%s) > lock-ttl (%s) in both modes.%s\n", yellow, workDuration, lockTTL, reset)
	fmt.Printf("  %sConcurrent occupancy will occur in both modes — this is expected.%s\n", yellow, reset)
	fmt.Printf("  %sThe invariant is wrong-owner DEL: naive produces it, safe (Lua) prevents it.%s\n\n", yellow, reset)

	var wg sync.WaitGroup
	for i := 1; i <= *workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			switch *mode {
			case "naive":
				runNaive(ctx, rdb, id, *iterations)
			case "safe":
				runSafe(ctx, rdb, id, *iterations)
			}
		}(i)
	}
	wg.Wait()

	woDEL := wrongOwnerDEL.Load()
	maxO := maxOccupancy.Load()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  successful ops        : %d\n", successOps.Load())
	fmt.Printf("  max concurrent holders: %d  (both modes show this — TTL expiry, not a bug)\n", maxO)
	fmt.Printf("  wrong-owner DEL count : %d  (this is the invariant)\n", woDEL)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  wrong-owner-del=%d  mode=%s\n", woDEL, *mode)
	fmt.Printf("  (occupancy=%d is informational — both modes exceed 1 due to TTL expiry)\n\n", maxO)

	if woDEL > 0 {
		fmt.Printf("  "+red+bold+"✗ FAILED"+reset+"  %d wrong-owner DEL(s) — naive DEL deleted a successor's lock\n", woDEL)
		fmt.Printf("  a holder whose lock expired silently wiped another worker's acquired lock\n\n")
		if *check {
			os.Exit(1)
		}
	} else {
		fmt.Printf("  "+green+bold+"✓ PASSED"+reset+"  0 wrong-owner DELs — Lua compare-and-delete held the invariant\n")
		fmt.Printf("  when lock expired mid-work, Lua refused to DEL (token mismatch)\n")
		fmt.Printf("  "+yellow+"NOTE"+reset+": concurrent occupancy still occurred (%d max) — that requires lock extension or fencing\n\n", maxO)
	}
}
