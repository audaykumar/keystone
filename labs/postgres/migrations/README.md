# Lab: PostgreSQL zero-downtime migrations

Schema changes are code deploys against shared mutable state. This lab
reproduces the three classic ways a "trivial" migration takes production down,
then applies the standard safe pattern for each. One question drives the lab:
what does a migration lock, and who queues behind it?

## Scenario

An `orders` table with 300,000 rows receives steady mixed traffic
(60% `INSERT`, 40% `SELECT`) from the Go load driver, which prints per-second
latency percentiles. Every migration demo runs while that traffic is live, so
the cost of the migration is measured, not assumed.

## The three failure modes

| # | Unsafe change | What actually happens | Safe pattern |
|---|---|---|---|
| 1 | `ALTER TABLE` while any long transaction touches the table | ALTER waits for ACCESS EXCLUSIVE; every later query queues behind the waiting ALTER. The queue is the outage, not the ALTER. | `lock_timeout` + retry loop |
| 2 | Plain `CREATE INDEX` | SHARE lock blocks all writes for the whole build | `CREATE INDEX CONCURRENTLY` |
| 3 | `ALTER COLUMN ... SET NOT NULL` / giant single-transaction backfill | Full-table scan or hours of row locks under one transaction | expand → batched backfill → `CHECK ... NOT VALID` → `VALIDATE` → `SET NOT NULL` |

## Commands

```bash
make up          # postgres + migrations + 300k seeded orders
make load        # terminal 1: steady traffic, per-second p50/p99/max
make break       # terminal 2: lock-queue outage (unsafe ALTER)
make test        # terminal 2: lock_timeout + retry (bounded blip)
make index-break # plain CREATE INDEX blocks writes
make index-safe  # CREATE INDEX CONCURRENTLY keeps writes flowing
make backfill    # add nullable column, backfill in 5k batches
make validate    # NOT VALID -> VALIDATE -> SET NOT NULL, no long scan
make locks       # blocking chains while a demo runs
make undo        # drop demo artifacts to rerun scenarios
make down        # full teardown: containers, volumes, images
```

## What to observe

### Lock-queue outage (`make break`)

The background reader holds only ACCESS SHARE, the weakest lock. The ALTER
still cannot proceed, because ACCESS EXCLUSIVE conflicts with everything. The
lethal part: PostgreSQL grants locks in queue order, so every new SELECT and
INSERT queues *behind* the waiting ALTER. The load terminal shows `ops=0`
stall lines until the reader commits. A one-millisecond catalog change caused
a 30-second outage.

### Bounded version (`make test`)

`SET lock_timeout = '1s'` makes the ALTER abort if the lock is not granted in
time. Each attempt costs at most a ~1s queue-behind window; the Makefile
retries every 2s until the reader finishes. The load terminal shows brief p99
blips instead of a stall.

### Index build (`make index-break` vs `make index-safe`)

Plain `CREATE INDEX` blocks INSERTs for the entire build; SELECT latency stays
flat. `CONCURRENTLY` does two table scans plus a wait for old snapshots, so it
is slower and cannot run inside a transaction, and a failed build leaves an
INVALID index to drop — the price of not blocking writes.

### Backfill (`make backfill` then `make validate`)

Adding a nullable column is catalog-only. The driver then fills it in batches
of 5,000 with a 100ms pause, committing each batch, so no long transaction
pins rows or bloats the lock table. `VALIDATE CONSTRAINT` scans under
SHARE UPDATE EXCLUSIVE (reads and writes continue), and PostgreSQL 12+ turns
`SET NOT NULL` into a catalog change when a valid CHECK constraint already
proves the invariant.

## Measured results (2026-07-07)

All scenarios ran against live `make load` traffic (8 workers, ~900 ops/s).

**`make break`** — the ALTER completed in 28.1s of wall time (all of it
waiting), and the load driver recorded a total outage for the entire wait:

```text
t=  8s ops=  611 errs=0 p50=6.9ms    p99=22.1ms   max=26.6ms
t=  9s ops=0 errs=0  << STALL: no query completed this second >>
...                     (27 consecutive stall seconds)
t= 35s ops=0 errs=0  << STALL: no query completed this second >>
t= 36s ops=  303 errs=0 p50=7.3ms    p99=28.0328s max=28.0372s
t= 37s ops=  975 errs=0 p50=5.1ms    p99=23.7ms   max=30.2ms

done. worst single-query latency or stall: 28.0372s
```

**`make test`** — same 30s reader, `lock_timeout='1s'` + retry. The ALTER
succeeded on attempt 10, and the worst any query saw across the whole run
was 1.03s:

```text
t= 12s ops=  188 errs=0 p50=6.4ms    p99=1.0276s  max=1.0324s   << stalled >>
t= 16s ops=  948 errs=0 p50=6.1ms    p99=25.7ms   max=1.0343s   << stalled >>
...
done. worst single-query latency or stall: 1.0343s
```

Identical migration, identical blocking reader: 28.0s total outage vs 1.03s
worst-case latency. The only difference is one `SET lock_timeout`.

**`make backfill`** — 329,081 rows in 66 batches of 5,000, 9.7s total, each
batch committing in 30-60ms. **`make validate`** — the NOT VALID → VALIDATE →
SET NOT NULL sequence completed in 0.111s. **`make index-safe`** —
`CREATE INDEX CONCURRENTLY` on the same table completed in 0.242s with no
write stall recorded.

## References

- PostgreSQL docs: [ALTER TABLE](https://www.postgresql.org/docs/current/sql-altertable.html) (per-form lock levels), [Building Indexes Concurrently](https://www.postgresql.org/docs/current/sql-createindex.html#SQL-CREATEINDEX-CONCURRENTLY), [Explicit Locking](https://www.postgresql.org/docs/current/explicit-locking.html)
- [PostgreSQL at Scale: Database Schema Changes Without Downtime](https://medium.com/paypal-tech/postgresql-at-scale-database-schema-changes-without-downtime-20d3749ed680) — Braintree/PayPal, the canonical operation-by-operation safety catalog
- [GitLab migration style guide](https://docs.gitlab.com/ee/development/migration_style_guide.html) — production rules for a very large Postgres install
- [strong_migrations](https://github.com/ankane/strong_migrations) — encoded dangerous-operation checklist; the README doubles as a reference
- [Zero-downtime Postgres migrations - the hard parts](https://gocardless.com/blog/zero-downtime-postgres-migrations-the-hard-parts/) — GoCardless on the lock-queue trap
- [When Postgres blocks: 7 tips for dealing with locks](https://www.citusdata.com/blog/2018/02/22/seven-tips-for-dealing-with-postgres-locks/) — Marco Slot
- Martin Kleppmann, *Designing Data-Intensive Applications*, ch. 4 (schema evolution) and ch. 7 (transactions)
