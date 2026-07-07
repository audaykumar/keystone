// pgmig drives the zero-downtime migrations lab.
//
// mode=load     mixed INSERT/SELECT traffic against orders, reporting per-second
//               latency percentiles so migration-induced stalls are visible.
// mode=backfill batched UPDATE that fills the nullable region column without
//               holding one giant row-locking transaction.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	mode     string
	workers  int
	duration time.Duration
	batch    int
	pause    time.Duration
}

func main() {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "load", "load|backfill")
	flag.IntVar(&cfg.workers, "workers", 8, "concurrent workers (load mode)")
	durationSec := flag.Int("duration", 60, "seconds to run load")
	flag.IntVar(&cfg.batch, "batch", 5000, "rows per backfill batch")
	pauseMs := flag.Int("pause", 100, "ms sleep between backfill batches")
	flag.Parse()
	cfg.duration = time.Duration(*durationSec) * time.Second
	cfg.pause = time.Duration(*pauseMs) * time.Millisecond

	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatalf("DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatalf("connect: %v", err)
	}
	defer pool.Close()

	switch cfg.mode {
	case "load":
		runLoad(ctx, pool, cfg)
	case "backfill":
		runBackfill(ctx, pool, cfg)
	default:
		fatalf("unknown mode %q", cfg.mode)
	}
}

// second collects latencies observed during one wall-clock second.
type second struct {
	mu   sync.Mutex
	lats []time.Duration
	errs int
}

func (s *second) add(d time.Duration) {
	s.mu.Lock()
	s.lats = append(s.lats, d)
	s.mu.Unlock()
}

func (s *second) fail() {
	s.mu.Lock()
	s.errs++
	s.mu.Unlock()
}

func runLoad(ctx context.Context, pool *pgxpool.Pool, cfg config) {
	fmt.Printf("load: %d workers for %s (60%% insert / 40%% select)\n", cfg.workers, cfg.duration)
	fmt.Println("watch p99 and max: a queued ACCESS EXCLUSIVE lock shows up as a full-second stall")

	deadline := time.Now().Add(cfg.duration)
	cur := &second{}
	var curMu sync.Mutex

	swap := func() *second {
		curMu.Lock()
		old := cur
		cur = &second{}
		curMu.Unlock()
		return old
	}
	record := func(d time.Duration, ok bool) {
		curMu.Lock()
		s := cur
		curMu.Unlock()
		if ok {
			s.add(d)
		} else {
			s.fail()
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				start := time.Now()
				var err error
				if rand.IntN(10) < 6 {
					_, err = pool.Exec(ctx,
						`INSERT INTO orders (customer_ref, amount_cents, status) VALUES ($1, $2, 'created')`,
						fmt.Sprintf("cust-%d", rand.IntN(5000)), int64(rand.IntN(100000)+100))
				} else {
					var n int64
					err = pool.QueryRow(ctx,
						`SELECT count(*) FROM orders WHERE customer_ref = $1`,
						fmt.Sprintf("cust-%d", rand.IntN(5000))).Scan(&n)
				}
				record(time.Since(start), err == nil)
			}
		}()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	startAt := time.Now()
	var worstStall time.Duration
	for time.Now().Before(deadline) {
		<-ticker.C
		s := swap()
		s.mu.Lock()
		lats := s.lats
		errs := s.errs
		s.mu.Unlock()

		elapsed := int(time.Since(startAt).Seconds())
		if len(lats) == 0 {
			fmt.Printf("t=%3ds ops=0 errs=%d  << STALL: no query completed this second >>\n", elapsed, errs)
			worstStall += time.Second
			continue
		}
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		p50 := lats[len(lats)/2]
		p99 := lats[len(lats)*99/100]
		max := lats[len(lats)-1]
		if max > worstStall {
			worstStall = max
		}
		mark := ""
		if max > time.Second {
			mark = "  << queries stalled behind a lock >>"
		}
		fmt.Printf("t=%3ds ops=%5d errs=%d p50=%-8s p99=%-8s max=%-8s%s\n",
			elapsed, len(lats), errs, rnd(p50), rnd(p99), rnd(max), mark)
	}
	wg.Wait()
	fmt.Printf("\ndone. worst single-query latency or stall: %s\n", rnd(worstStall))
}

func runBackfill(ctx context.Context, pool *pgxpool.Pool, cfg config) {
	fmt.Printf("backfill: region column, batches of %d, %s pause between batches\n", cfg.batch, cfg.pause)
	start := time.Now()
	total := int64(0)
	for batch := 1; ; batch++ {
		t := time.Now()
		tag, err := pool.Exec(ctx, `
			WITH batch AS (
				SELECT id FROM orders WHERE region IS NULL LIMIT $1
			)
			UPDATE orders o
			SET region = 'r-' || (o.id % 4)
			FROM batch
			WHERE o.id = batch.id`, cfg.batch)
		if err != nil {
			fatalf("backfill batch %d: %v", batch, err)
		}
		n := tag.RowsAffected()
		total += n
		if n == 0 {
			break
		}
		fmt.Printf("batch %3d: %5d rows in %s (total %d)\n", batch, n, rnd(time.Since(t)), total)
		time.Sleep(cfg.pause)
	}
	fmt.Printf("backfill complete: %d rows in %s\n", total, rnd(time.Since(start)))
	fmt.Println("next: make validate  (NOT VALID check -> VALIDATE -> SET NOT NULL)")
}

func rnd(d time.Duration) time.Duration { return d.Round(100 * time.Microsecond) }

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
