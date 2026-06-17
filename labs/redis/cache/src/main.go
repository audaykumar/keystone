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
	cacheKey     = "product:42:price"
	cachedValue  = "299.99"
	cacheTTL     = 3 * time.Second
	backendDelay = 400 * time.Millisecond
	lockKey      = "product:42:price:filling"
	lockTTL      = 10 * time.Second
	retryDelay   = 30 * time.Millisecond
)

var (
	backendCalls atomic.Int64
	cacheHits    atomic.Int64
	cacheMisses  atomic.Int64
	start        time.Time
	printMu      sync.Mutex
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

func ts() string {
	return fmt.Sprintf("%6dms", time.Since(start).Milliseconds())
}

func logLine(color, format string, args ...any) {
	printMu.Lock()
	defer printMu.Unlock()
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[%s] %s%s%s\n", ts(), color, msg, reset)
}

func slowBackend(workerID int) string {
	logLine(yellow, "  backend CALL   worker=%-3d  (simulating DB query, %s latency)", workerID, backendDelay)
	time.Sleep(backendDelay)
	backendCalls.Add(1)
	logLine(yellow, "  backend DONE   worker=%-3d  price=%s", workerID, cachedValue)
	return cachedValue
}

func fetchNaive(ctx context.Context, rdb *redis.Client, workerID int) {
	val, err := rdb.Get(ctx, cacheKey).Result()
	if err == redis.Nil {
		cacheMisses.Add(1)
		logLine(red, "  GET MISS       worker=%-3d  key=%s", workerID, cacheKey)
		val = slowBackend(workerID)
		rdb.Set(ctx, cacheKey, val, cacheTTL)
		logLine(cyan, "  SET            worker=%-3d  key=%s  ttl=%s", workerID, cacheKey, cacheTTL)
		return
	}
	cacheHits.Add(1)
	logLine(green, "  GET HIT        worker=%-3d  key=%s  val=%s", workerID, cacheKey, val)
}

// fetchFix uses SET NX to ensure only one worker calls the backend on a miss.
// Other workers wait and retry until the key is populated.
func fetchFix(ctx context.Context, rdb *redis.Client, workerID int) {
	for {
		val, err := rdb.Get(ctx, cacheKey).Result()
		if err == redis.Nil {
			cacheMisses.Add(1)
			logLine(red, "  GET MISS       worker=%-3d  key=%s", workerID, cacheKey)

			acquired, _ := rdb.SetNX(ctx, lockKey, "1", lockTTL).Result()
			if acquired {
				// Won the fill race — compute and populate cache.
				val = slowBackend(workerID)
				rdb.Set(ctx, cacheKey, val, cacheTTL)
				rdb.Del(ctx, lockKey)
				logLine(cyan, "  SET + UNLOCK   worker=%-3d  key=%s  ttl=%s", workerID, cacheKey, cacheTTL)
				return
			}
			// Lost the fill race — wait for the winner to populate the key.
			logLine(dim, "  WAIT fill      worker=%-3d  (another worker is computing)", workerID)
			time.Sleep(retryDelay)
			continue
		}
		cacheHits.Add(1)
		logLine(green, "  GET HIT        worker=%-3d  key=%s  val=%s", workerID, cacheKey, val)
		return
	}
}

func main() {
	mode    := flag.String("mode", "naive", "naive | fix")
	workers := flag.Int("workers", 20, "concurrent workers")
	rounds  := flag.Int("rounds", 2, "how many TTL expiry cycles to observe")
	check   := flag.Bool("check", false, "exit 1 if invariant violated")
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
	rdb.Del(ctx, cacheKey, lockKey)

	start = time.Now()

	fmt.Println()
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)
	fmt.Println(bold + "  REDIS CACHE LAB — THUNDERING HERD" + reset)
	fmt.Printf("  mode=%-8s  workers=%-4d  ttl=%s  backend-latency=%s\n",
		*mode, *workers, cacheTTL, backendDelay)
	fmt.Println(bold + "═══════════════════════════════════════════════════════════" + reset)

	for round := 1; round <= *rounds; round++ {
		fmt.Println()
		fmt.Printf(bold+"┄┄┄ Round %d/%d — key expires, %d workers read simultaneously ┄┄┄"+reset+"\n\n",
			round, *rounds, *workers)

		if round > 1 {
			logLine(yellow, "  waiting %s for key to expire...", cacheTTL)
			time.Sleep(cacheTTL + 200*time.Millisecond)
			logLine(yellow, "  key expired — releasing workers")
			fmt.Println()
		}

		var wg sync.WaitGroup
		gate := make(chan struct{})

		for i := 1; i <= *workers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-gate
				switch *mode {
				case "naive":
					fetchNaive(ctx, rdb, id)
				case "fix":
					fetchFix(ctx, rdb, id)
				}
			}(i)
		}

		close(gate) // release all workers at the same instant
		wg.Wait()
	}

	totalReqs := cacheHits.Load() + cacheMisses.Load()
	calls := backendCalls.Load()
	// Allow up to 2x the rounds as threshold (small race window acceptable in fix mode).
	threshold := int64(*rounds * 2)

	fmt.Println()
	fmt.Println(bold + "═══════════════════════ RESULTS ════════════════════════" + reset)
	fmt.Printf("  total requests : %d\n", totalReqs)
	fmt.Printf("  cache hits     : %d\n", cacheHits.Load())
	fmt.Printf("  cache misses   : %d\n", cacheMisses.Load())
	fmt.Printf("  backend calls  : %d  (ideal = %d, one per expiry cycle)\n", calls, *rounds)

	fmt.Println()
	fmt.Println(bold + "══════════════════ INVARIANT CHECK ═════════════════════" + reset)
	fmt.Printf("  backend-calls=%d  threshold=%d  workers=%d  rounds=%d\n",
		calls, threshold, *workers, *rounds)

	if calls <= threshold {
		fmt.Printf("\n  "+green+bold+"✓ PASSED"+reset+"  single-flight contained backend calls to %d\n\n", calls)
	} else {
		fmt.Printf("\n  "+red+bold+"✗ FAILED"+reset+"  thundering herd: %d workers all called backend simultaneously\n", calls)
		fmt.Printf("  expected ≤ %d calls (one per expiry cycle)\n\n", threshold)
		if *check {
			os.Exit(1)
		}
	}
}
