package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	mode       string
	workers    int
	iterations int
	amount     int64
	jitter     time.Duration
	check      bool
	from       string
	to         string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "naive", "transfer mode: naive|locked|atomic|serializable")
	flag.IntVar(&cfg.workers, "workers", 8, "concurrent workers")
	flag.IntVar(&cfg.iterations, "iterations", 200, "transfers per worker")
	amount := flag.Int64("amount", 1, "transfer amount in cents")
	jitterMs := flag.Int("jitter", 2, "ms sleep between read and write (widens the race window in naive/locked/serializable)")
	flag.BoolVar(&cfg.check, "check", false, "exit non-zero if invariant I1 is violated")
	flag.StringVar(&cfg.from, "from", "alice", "source account")
	flag.StringVar(&cfg.to, "to", "bob", "destination account")
	flag.Parse()
	cfg.amount = *amount
	cfg.jitter = time.Duration(*jitterMs) * time.Millisecond

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

	if err := resetScenario(ctx, pool); err != nil {
		fatalf("reset scenario: %v", err)
	}

	total := cfg.workers * cfg.iterations
	fmt.Printf("mode=%s workers=%d iterations=%d total=%d amount=%dc jitter=%s\n",
		cfg.mode, cfg.workers, cfg.iterations, total, cfg.amount, cfg.jitter)

	start := time.Now()
	var committed, failed, retries int64
	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < cfg.iterations; i++ {
				r, err := doTransfer(ctx, pool, cfg)
				atomic.AddInt64(&retries, int64(r))
				if err != nil {
					atomic.AddInt64(&failed, 1)
					continue
				}
				atomic.AddInt64(&committed, 1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("committed=%d failed=%d retries=%d elapsed=%s (%.0f tx/s)\n\n",
		committed, failed, retries, elapsed.Round(time.Millisecond),
		float64(committed)/elapsed.Seconds())

	ok, err := checkInvariant(ctx, pool)
	if err != nil {
		fatalf("check: %v", err)
	}
	if cfg.check && !ok {
		os.Exit(1)
	}
}

// doTransfer dispatches to the chosen strategy. Returns the number of retries
// performed (serializable mode) and a terminal error if the transfer failed.
func doTransfer(ctx context.Context, pool *pgxpool.Pool, cfg config) (int, error) {
	switch cfg.mode {
	case "naive":
		return 0, transferReadModifyWrite(ctx, pool, cfg, false)
	case "locked":
		return 0, transferReadModifyWrite(ctx, pool, cfg, true)
	case "atomic":
		return 0, transferAtomic(ctx, pool, cfg)
	case "serializable":
		return transferSerializable(ctx, pool, cfg)
	default:
		return 0, fmt.Errorf("unknown mode %q", cfg.mode)
	}
}

// transferReadModifyWrite reads both balances, computes the new values in
// application memory, then writes them back. This is the classic lost-update
// shape. With forUpdate=false (naive) concurrent transactions read the same
// starting balance and clobber each other. With forUpdate=true the SELECT takes
// a row lock, serializing conflicting writers so no update is lost.
func transferReadModifyWrite(ctx context.Context, pool *pgxpool.Pool, cfg config, forUpdate bool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	lockClause := ""
	if forUpdate {
		lockClause = " FOR UPDATE"
	}

	// Lock/read in a stable id order so concurrent transfers cannot deadlock.
	ids := []string{cfg.from, cfg.to}
	sort.Strings(ids)
	bal := map[string]int64{}
	for _, id := range ids {
		var b int64
		if err := tx.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1"+lockClause, id).Scan(&b); err != nil {
			return err
		}
		bal[id] = b
	}

	// Widen the window between read and write so the race is reliable to observe.
	if cfg.jitter > 0 {
		time.Sleep(cfg.jitter)
	}

	if _, err := tx.Exec(ctx,
		"UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2",
		bal[cfg.from]-cfg.amount, cfg.from); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		"UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2",
		bal[cfg.to]+cfg.amount, cfg.to); err != nil {
		return err
	}

	if err := writePostings(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// transferAtomic moves the arithmetic into the UPDATE statement itself. Each
// UPDATE takes a row lock and reads the current value under that lock, so there
// is no stale application-side value to lose. Correct without an explicit
// SELECT ... FOR UPDATE.
func transferAtomic(ctx context.Context, pool *pgxpool.Pool, cfg config) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Apply in stable id order to avoid deadlocks under mixed-direction load.
	type delta struct {
		id  string
		amt int64
	}
	deltas := []delta{{cfg.from, -cfg.amount}, {cfg.to, cfg.amount}}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].id < deltas[j].id })
	for _, d := range deltas {
		if _, err := tx.Exec(ctx,
			"UPDATE accounts SET balance = balance + $1, version = version + 1 WHERE id = $2",
			d.amt, d.id); err != nil {
			return err
		}
	}

	if err := writePostings(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// transferSerializable uses the naive read-modify-write shape but under
// SERIALIZABLE isolation. PostgreSQL detects the read/write dependency cycle and
// aborts one transaction with SQLSTATE 40001; the application must retry.
func transferSerializable(ctx context.Context, pool *pgxpool.Pool, cfg config) (int, error) {
	retries := 0
	for {
		err := func() error {
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)

			ids := []string{cfg.from, cfg.to}
			sort.Strings(ids)
			bal := map[string]int64{}
			for _, id := range ids {
				var b int64
				if err := tx.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", id).Scan(&b); err != nil {
					return err
				}
				bal[id] = b
			}
			if cfg.jitter > 0 {
				time.Sleep(cfg.jitter)
			}
			if _, err := tx.Exec(ctx,
				"UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2",
				bal[cfg.from]-cfg.amount, cfg.from); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				"UPDATE accounts SET balance = $1, version = version + 1 WHERE id = $2",
				bal[cfg.to]+cfg.amount, cfg.to); err != nil {
				return err
			}
			if err := writePostings(ctx, tx, cfg); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}()

		if err == nil {
			return retries, nil
		}
		if isSerializationFailure(err) {
			retries++
			continue
		}
		return retries, err
	}
}

// writePostings records the transfer intent and its two balanced postings.
// These are independent INSERTs, so concurrency never unbalances them: the
// postings are always the trustworthy double-entry truth.
func writePostings(ctx context.Context, tx pgx.Tx, cfg config) error {
	var tid string
	if err := tx.QueryRow(ctx,
		"INSERT INTO transfers (from_account, to_account, amount) VALUES ($1, $2, $3) RETURNING id",
		cfg.from, cfg.to, cfg.amount).Scan(&tid); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		"INSERT INTO postings (transfer_id, account_id, amount) VALUES ($1, $2, $3), ($1, $4, $5)",
		tid, cfg.from, -cfg.amount, cfg.to, cfg.amount)
	return err
}

func resetScenario(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		"TRUNCATE postings, transfers RESTART IDENTITY; UPDATE accounts SET balance = initial_balance, version = 0;")
	return err
}

// checkInvariant verifies I1 (cache equals truth) per account and I3 (money
// conserved system-wide), printing a drift table. Returns true if both hold.
func checkInvariant(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id,
		       a.balance,
		       a.initial_balance + COALESCE(p.s, 0) AS truth
		FROM accounts a
		LEFT JOIN (
			SELECT account_id, SUM(amount) AS s FROM postings GROUP BY account_id
		) p ON p.account_id = a.id
		ORDER BY a.id;`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	fmt.Printf("%-10s %16s %16s %12s\n", "account", "cache(balance)", "truth(init+Σ)", "drift")
	fmt.Println("---------------------------------------------------------------")
	ok := true
	var sumCache, sumTruth int64
	for rows.Next() {
		var id string
		var cache, truth int64
		if err := rows.Scan(&id, &cache, &truth); err != nil {
			return false, err
		}
		drift := cache - truth
		if drift != 0 {
			ok = false
		}
		sumCache += cache
		sumTruth += truth
		fmt.Printf("%-10s %16d %16d %12d %s\n", id, cache, truth, drift, mark(drift == 0))
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	fmt.Println("---------------------------------------------------------------")
	fmt.Printf("%-10s %16d %16d %12d %s\n", "TOTAL", sumCache, sumTruth, sumCache-sumTruth, mark(sumCache == sumTruth))
	fmt.Println()
	if ok {
		fmt.Println("I1 + I3 hold: the cache agrees with the postings. No money created or destroyed.")
	} else {
		fmt.Printf("LOST UPDATE: the cache disagrees with the postings by %d cents. Money was created or destroyed in the cache.\n", sumCache-sumTruth)
	}
	return ok, nil
}

func mark(ok bool) string {
	if ok {
		return "OK"
	}
	return "<< DRIFT"
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 40001 serialization_failure, 40P01 deadlock_detected
		return pgErr.Code == "40001" || pgErr.Code == "40P01"
	}
	return false
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
