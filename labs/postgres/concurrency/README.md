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

## Lock inspection

Two committed queries expose what the locked mode actually does inside PostgreSQL:

- `scripts/lock_inspect.sql`: every lock held or awaited by lab backends, joined
  to `pg_stat_activity` for state, wait events, and the running query.
- `scripts/lock_waits.sql`: blocking chains via `pg_blocking_pids()`, which
  resolves the true blocker even when several waiters queue behind one holder.

Run them while a locked-mode run is active:

```bash
make test          # terminal 1: 8 workers contend on the hot product row
make locks         # terminal 2: one lock snapshot
make waits         # terminal 2: who waits on whom
make locks-watch   # terminal 2: poll blocking chains every 500ms
```

### Measured lock evidence (2026-07-07, locked mode, 8 workers, 5ms jitter)

`make locks` during the run shows the queue has two tiers, not one:

```text
 pid | state  | wait_event    | locktype      | mode                | granted | relation | xid
 147 | active | transactionid | transactionid | ShareLock           | f       |          | 1138
 142 | active | tuple         | tuple         | AccessExclusiveLock | f       | products |
 143 | active | tuple         | tuple         | AccessExclusiveLock | f       | products |
 144 | active | tuple         | tuple         | AccessExclusiveLock | f       | products |
 ...
 146 | idle in transaction |  | transactionid | ExclusiveLock       | t       |          | 1138
```

And `make waits` (via `pg_blocking_pids()`):

```text
 waiting_pid | blocking_pid | wait_event    | waiting_query
         143 |          148 | transactionid | SELECT available_stock ... FOR UPDATE
         142 |          146 | tuple         | SELECT available_stock ... FOR UPDATE
         144 |          143 | tuple         | SELECT available_stock ... FOR UPDATE
         145 |          143 | tuple         | SELECT available_stock ... FOR UPDATE
```

What the snapshot teaches:

- **Row locks are not in `pg_locks` per row** (that would not scale). The
  current holder (pid 146) marks the tuple on disk and holds an
  `ExclusiveLock` on its own transaction id (`xid 1138`).
- **Exactly one waiter is "next in line":** it holds a granted
  `AccessExclusiveLock` on the *tuple* and waits with a `ShareLock` on the
  holder's *transaction id* (`wait_event = transactionid`). It wakes the
  moment xid 1138 commits.
- **Everyone else queues on the tuple lock** (`wait_event = tuple`,
  `granted = f`). This two-tier structure is how PostgreSQL keeps row-lock
  handoff fair (FIFO) without storing per-row lock state in shared memory.
- The holder shows `idle in transaction` between its SELECT ... FOR UPDATE
  and its UPDATE — the driver's 5ms jitter window, which is exactly where
  the queue builds. That wait chain is the low throughput of locked mode
  (162/s vs atomic's 1,577/s above).

## References

- PostgreSQL docs: [Explicit Locking](https://www.postgresql.org/docs/current/explicit-locking.html), [pg_locks view](https://www.postgresql.org/docs/current/view-pg-locks.html), [Transaction Isolation](https://www.postgresql.org/docs/current/transaction-iso.html)
- Martin Kleppmann, *Designing Data-Intensive Applications*, ch. 7 "Transactions" (weak isolation, lost updates, SSI)
- [PostgreSQL rocks, except when it blocks: understanding locks](https://www.citusdata.com/blog/2018/02/15/when-postgresql-blocks/) — Marco Slot, Citus
- [Postgres Locking Revealed](https://engineering.nordeus.com/postgres-locking-revealed/) — Nordeus engineering
- [Lock Monitoring](https://wiki.postgresql.org/wiki/Lock_Monitoring) — PostgreSQL wiki, canonical lock-debugging queries
- Jepsen: [PostgreSQL 12.3 analysis](https://jepsen.io/analyses/postgresql-12.3) — serializability edge cases under test
