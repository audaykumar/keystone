package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errInsufficientStock = errors.New("insufficient stock")

type config struct {
	mode       string
	workers    int
	iterations int
	quantity   int64
	jitter     time.Duration
	check      bool
	product    string
	maxRetries int
}

func main() {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "naive", "reservation mode: naive|locked|atomic|serializable")
	flag.IntVar(&cfg.workers, "workers", 8, "concurrent workers")
	flag.IntVar(&cfg.iterations, "iterations", 200, "reservations per worker")
	quantity := flag.Int64("quantity", 1, "units reserved per order")
	jitterMs := flag.Int("jitter", 2, "ms sleep between read and write")
	flag.BoolVar(&cfg.check, "check", false, "exit non-zero if a stock invariant is violated")
	flag.StringVar(&cfg.product, "product", "widget", "product to reserve")
	flag.IntVar(&cfg.maxRetries, "max-retries", 5, "maximum retries after a serialization failure")
	flag.Parse()
	cfg.quantity = *quantity
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
	fmt.Printf("mode=%s product=%s workers=%d iterations=%d total=%d quantity=%d jitter=%s\n",
		cfg.mode, cfg.product, cfg.workers, cfg.iterations, total, cfg.quantity, cfg.jitter)

	start := time.Now()
	var committed, failed, retries, insufficient int64
	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < cfg.iterations; i++ {
				r, err := reserve(ctx, pool, cfg)
				atomic.AddInt64(&retries, int64(r))
				if err != nil {
					atomic.AddInt64(&failed, 1)
					if errors.Is(err, errInsufficientStock) {
						atomic.AddInt64(&insufficient, 1)
					}
					continue
				}
				atomic.AddInt64(&committed, 1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("committed=%d failed=%d insufficient=%d retries=%d elapsed=%s (%.0f reservations/s)\n\n",
		committed, failed, insufficient, retries, elapsed.Round(time.Millisecond),
		float64(committed)/elapsed.Seconds())

	ok, err := checkInvariants(ctx, pool)
	if err != nil {
		fatalf("check: %v", err)
	}
	if cfg.check && !ok {
		os.Exit(1)
	}
}

func reserve(ctx context.Context, pool *pgxpool.Pool, cfg config) (int, error) {
	switch cfg.mode {
	case "naive":
		return 0, reserveReadModifyWrite(ctx, pool, cfg, false)
	case "locked":
		return 0, reserveReadModifyWrite(ctx, pool, cfg, true)
	case "atomic":
		return 0, reserveAtomic(ctx, pool, cfg)
	case "serializable":
		return reserveSerializable(ctx, pool, cfg)
	default:
		return 0, fmt.Errorf("unknown mode %q", cfg.mode)
	}
}

// reserveReadModifyWrite reads stock, validates it, computes the new value in
// application memory, then writes it back. Without FOR UPDATE, concurrent
// transactions can read the same value and overwrite each other's decrement.
func reserveReadModifyWrite(ctx context.Context, pool *pgxpool.Pool, cfg config, forUpdate bool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	query := "SELECT available_stock FROM products WHERE id = $1"
	if forUpdate {
		query += " FOR UPDATE"
	}

	var available int64
	if err := tx.QueryRow(ctx, query, cfg.product).Scan(&available); err != nil {
		return err
	}
	if available < cfg.quantity {
		return errInsufficientStock
	}

	if cfg.jitter > 0 {
		time.Sleep(cfg.jitter)
	}

	if _, err := tx.Exec(ctx,
		"UPDATE products SET available_stock = $1, version = version + 1 WHERE id = $2",
		available-cfg.quantity, cfg.product); err != nil {
		return err
	}
	if err := writeReservation(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reserveAtomic combines validation and decrement in one statement. PostgreSQL
// evaluates the predicate against the current row while holding its update lock.
func reserveAtomic(ctx context.Context, pool *pgxpool.Pool, cfg config) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var remaining int64
	err = tx.QueryRow(ctx, `
		UPDATE products
		SET available_stock = available_stock - $1,
		    version = version + 1
		WHERE id = $2
		  AND available_stock >= $1
		RETURNING available_stock`,
		cfg.quantity, cfg.product).Scan(&remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return errInsufficientStock
	}
	if err != nil {
		return err
	}

	if err := writeReservation(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reserveSerializable keeps the naive read-modify-write shape but asks
// PostgreSQL to reject unsafe executions. Retries are bounded because heavy
// contention can otherwise keep a request retrying indefinitely.
func reserveSerializable(ctx context.Context, pool *pgxpool.Pool, cfg config) (int, error) {
	retries := 0
	for {
		err := func() error {
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)

			var available int64
			if err := tx.QueryRow(ctx,
				"SELECT available_stock FROM products WHERE id = $1",
				cfg.product).Scan(&available); err != nil {
				return err
			}
			if available < cfg.quantity {
				return errInsufficientStock
			}
			if cfg.jitter > 0 {
				time.Sleep(cfg.jitter)
			}
			if _, err := tx.Exec(ctx,
				"UPDATE products SET available_stock = $1, version = version + 1 WHERE id = $2",
				available-cfg.quantity, cfg.product); err != nil {
				return err
			}
			if err := writeReservation(ctx, tx, cfg); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}()

		if err == nil {
			return retries, nil
		}
		if !isSerializationFailure(err) || retries >= cfg.maxRetries {
			return retries, err
		}

		retries++
		backoff := time.Duration(1<<min(retries, 6)) * time.Millisecond
		time.Sleep(backoff + time.Duration(rand.IntN(4))*time.Millisecond)
	}
}

func writeReservation(ctx context.Context, tx pgx.Tx, cfg config) error {
	var orderID string
	if err := tx.QueryRow(ctx,
		"INSERT INTO orders (product_id, quantity) VALUES ($1, $2) RETURNING id",
		cfg.product, cfg.quantity).Scan(&orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (order_id, product_id, quantity_change)
		VALUES ($1, $2, $3)`,
		orderID, cfg.product, -cfg.quantity)
	return err
}

func resetScenario(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		TRUNCATE stock_movements, orders RESTART IDENTITY;
		UPDATE products SET available_stock = initial_stock, version = 0;`)
	return err
}

// I1: available stock equals initial stock plus immutable movements.
// I2: available stock never becomes negative.
func checkInvariants(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.id,
		       p.available_stock,
		       p.initial_stock + COALESCE(m.quantity_change, 0) AS expected_stock
		FROM products p
		LEFT JOIN (
			SELECT product_id, SUM(quantity_change) AS quantity_change
			FROM stock_movements
			GROUP BY product_id
		) m ON m.product_id = p.id
		ORDER BY p.id;`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	fmt.Printf("%-14s %18s %18s %12s\n", "product", "available(stock)", "expected(init+sum)", "drift")
	fmt.Println("------------------------------------------------------------------")
	ok := true
	for rows.Next() {
		var id string
		var available, expected int64
		if err := rows.Scan(&id, &available, &expected); err != nil {
			return false, err
		}
		drift := available - expected
		rowOK := drift == 0 && available >= 0
		if !rowOK {
			ok = false
		}
		fmt.Printf("%-14s %18d %18d %12d %s\n",
			id, available, expected, drift, mark(rowOK))
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	fmt.Println()
	if ok {
		fmt.Println("I1 + I2 hold: available stock matches reservation history and is non-negative.")
	} else {
		fmt.Println("LOST UPDATE: available stock disagrees with immutable stock movements.")
	}
	return ok, nil
}

func mark(ok bool) string {
	if ok {
		return "OK"
	}
	return "<< VIOLATION"
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001" || pgErr.Code == "40P01"
	}
	return false
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
