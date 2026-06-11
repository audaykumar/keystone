# Lab: PostgreSQL concurrency

Reproduce a lost update while reserving warehouse stock, then compare three
correct fixes. The lab isolates one question: what protects a read, validation,
and write when many transactions target the same row?

## Scenario

A warehouse stores stock in two forms:

- `stock_movements` is the immutable reservation history.
- `products.available_stock` is the mutable value used for fast availability checks.

Each successful order appends one negative stock movement and decrements
`available_stock`.

| # | Invariant | Breaks under |
|---|---|---|
| I1 | `available_stock == initial_stock + SUM(stock_movements.quantity_change)` | naive concurrent writes |
| I2 | `available_stock >= 0` | unsafe validation or decrement logic |

Interactive explainer: `docs/postgres/lost-update.html`.

## Reservation modes

| Mode | Strategy | Result |
|---|---|---|
| `naive` | read stock into Go, validate, calculate, write | Incorrect: concurrent writers lose decrements |
| `locked` | `SELECT ... FOR UPDATE`, then validate and write | Correct: conflicting reservations wait |
| `atomic` | conditional `UPDATE ... WHERE available_stock >= quantity` | Correct: validation and decrement happen together |
| `serializable` | naive shape under `SERIALIZABLE`, with bounded retries | Correct when it commits; may fail after retry exhaustion |

Driver flags:

```text
-mode -workers -iterations -quantity -jitter -check -product -max-retries
```

## Commands

```bash
make up      # start PostgreSQL, apply migrations, seed 10,000 widgets
make break   # naive mode, expected to violate I1
make test    # locked mode, I1 and I2 must hold
make load    # atomic mode with heavier traffic
make serializable # serializable mode with at most five retries
make logs    # follow PostgreSQL logs
make psql    # open a psql shell
make down    # remove containers, networks, volumes, and local images
make reset   # down followed by up
```

Runs on Docker Desktop and OrbStack using Compose v2. No host-installed database
or Go runtime is required.

## What to observe

### Naive

Several workers read the same stock value. Each calculates the same new value.
Their `UPDATE`s overwrite one another, while every committed order still inserts
its own stock movement. `available_stock` therefore reports more stock than the
reservation history permits.

### Locked

`SELECT ... FOR UPDATE` makes conflicting workers wait. Each worker reads the
latest committed stock after acquiring the row lock. Correct, but latency grows
as reservations queue behind one hot product.

### Atomic

The database performs validation and arithmetic in one statement:

```sql
UPDATE products
SET available_stock = available_stock - $1
WHERE id = $2
  AND available_stock >= $1
RETURNING available_stock;
```

This is the smallest transaction shape for this specific invariant.

### Serializable

PostgreSQL detects unsafe concurrent executions and aborts some transactions
with SQLSTATE `40001`. The driver retries with exponential backoff and jitter,
but stops after `-max-retries`. Serializable isolation protects correctness; it
does not guarantee low latency or eventual success under heavy contention.

## Measured results

Run on 2026-06-11 with 8 workers, 250 reservations per worker, quantity 1,
and 2 ms read/write jitter:

| Mode | Committed | Failed | Retries | Throughput | Drift |
|---|---:|---:|---:|---:|---:|
| naive | 2,000 | 0 | 0 | 1,698/s | +1,750 |
| locked | 2,000 | 0 | 0 | 162/s | 0 |
| atomic | 2,000 | 0 | 0 | 1,577/s | 0 |
| serializable | 1,552 | 448 | 3,087 | 167/s | 0 |

These figures are machine-specific, but the behavior is the lesson:

- Naive mode completed quickly while silently overstating stock.
- Row locking preserved correctness by serializing access to the hot product.
- Atomic mode preserved correctness with a much smaller critical section.
- Serializable mode preserved committed-state correctness, but 448 reservations
  exhausted five retries under contention.

Oversubscription check: atomic mode received 12,000 one-unit reservation
attempts against 10,000 units. Exactly 10,000 committed, 2,000 returned
insufficient stock, and availability stopped at zero with no drift.

## Status

Implementation complete:

- Pinned PostgreSQL container and health check
- Versioned product, order, and stock-movement migrations
- Deterministic seed data
- Go driver with four reservation modes
- Invariant checker
- Complete teardown command

All four modes and the atomic oversubscription case have been run successfully.
Complete teardown has also been verified: no project container, network, volume,
or local driver image remained. Lock inspection remains.

## Lab checklist

1. Inspect `pg_stat_activity` and `pg_locks` during locked mode.
2. Add lock evidence to this README and the HTML explainer.
