# Lab: postgres-concurrency

Reproduce a **lost update** on a money balance under concurrent transfers, then fix
it three ways and compare. Isolates one PostgreSQL mechanism: what protects a
read-modify-write against concurrent writers.

## The invariant

Money is stored twice:

- `postings` is the immutable double-entry truth (each posting is its own INSERT).
- `accounts.balance` is a mutable cache for fast reads (the fragile one).

| # | Invariant | Breaks under |
|---|---|---|
| I1 | `balance == initial_balance + Σ postings.amount` per account | naive concurrent writes |
| I2 | `Σ postings.amount == 0` per transfer | never (independent INSERTs) |
| I3 | `Σ balance == Σ initial_balance` system-wide | naive concurrent writes |

Interactive explainer: `docs/postgres/lost-update.html`.

## Transfer modes (`src/main.go`)

| Mode | Strategy | Correct? |
|---|---|---|
| `naive` | read balance into Go, compute, write back | No: lost update under READ COMMITTED |
| `locked` | `SELECT ... FOR UPDATE` then write | Yes: row lock serializes writers |
| `atomic` | `UPDATE SET balance = balance + delta` | Yes: arithmetic under the write lock |
| `serializable` | naive shape under SERIALIZABLE, retry on 40001 | Yes: PG aborts the dependency cycle |

Driver flags: `-mode -workers -iterations -amount -jitter -check -from -to`.

## Commands

```bash
make up      # start postgres (pinned 17.2-alpine), apply migrations, seed
make break   # naive mode -> expected to VIOLATE I1/I3 (prints drift table)
make test    # locked mode -> must HOLD the invariant
make load    # atomic mode, heavier traffic
make logs    # follow postgres logs
make psql    # psql shell
make down    # remove containers, networks, volumes, built images
make reset   # down + up
```

Runs identically on Docker Desktop and OrbStack (plain Compose v2, multi-arch
pinned image, named volume). No backend detection.

## Status (2026-06-10)

Done:
- Schema migrations `0001`-`0003`, deterministic seed (alice 1,000,000c, bob 0c).
- `compose.yaml`, `Makefile`, Go driver with all four modes + invariant checker.
- `make up` verified: postgres healthy, migrations applied, seed loaded.
- Part 1 interactive explainer at `docs/postgres/lost-update.html`.

Not done yet:
- `make break` has not been run (driver image not built). Build the image and
  observe the real drift table.
- `make test` not run (confirm the locked fix holds).
- No predictions/observations recorded below.
- `docs/postgres/lost-update.html` not linked from `docs/index.html`.

## Next session

1. `make break` — build the driver, watch naive mode violate I1/I3. Note the
   drift magnitude and tx/s.
2. `make test` — confirm locked mode holds the invariant at zero drift.
3. Compare `atomic` and `serializable`: run each, compare tx/s, retry counts,
   and failure counts. Record the throughput vs contention trade-off.
4. Observe the *why* (Part 5): during a `naive` run, open `make psql` in another
   shell and inspect `pg_locks` and `pg_stat_activity`. Confirm nothing guards
   the read in naive mode; confirm `FOR UPDATE` rows show up as locked.
5. Verify the lifecycle (Part 7): `make down` removes containers, the
   `pg-concurrency_default` network, the `pgdata` volume, and the
   `pg-concurrency-driver:local` image. Confirm `docker volume ls` / `docker
   image ls` are clean afterward.
6. Teach-back (Part 8): fold real observations into
   `docs/postgres/lost-update.html` (add the fix-comparison and the pg_locks
   evidence), then link it from `docs/index.html`.

## Predictions / observations / conclusions

(Record here as the runs happen. Empty until `make break` / `make test` are run.)
