# Lab: PostgreSQL WAL for CDC

This lab shows the PostgreSQL side of CDC without Debezium in front of it. It
uses logical decoding and the built-in `test_decoding` output plugin so the WAL
mechanics are visible directly. Consumers here are the SQL functions
`pg_logical_slot_get_changes` and `pg_logical_slot_peek_changes`, so the slot is
active only during each decoding call; idle inspections show `active = false`.
A real connector holds the slot open through a walsender.

## What This Proves

- Buffered SQL decoding used here emits committed changes. Rows from an open
  transaction are held back even after their WAL has been flushed. Streaming
  plugins can send tentative changes before commit, followed by commit or abort.
- A replication slot pins WAL on disk: with the consumer stalled, `pg_wal`
  stays well above `max_wal_size` after a checkpoint.
- `restart_lsn` is what pins WAL, and it does not move just because the
  consumer confirms. It advances only after decoding passes a new
  running-transactions record and the consumer has confirmed past it. Only then
  can a checkpoint remove or recycle the old segments. Some may linger behind
  the slot until the server restarts.
- `max_slot_wal_keep_size` bounds the disk cost by invalidating the slot. The
  consumer then cannot resume and must resnapshot.
- `REPLICA IDENTITY` decides what UPDATE and DELETE carry in WAL.

## Run

```bash
make up
make test
make break
make cap
make identity
make down
```

`make down` removes the container, network, and named volume. The scenario
scripts use `mktemp` files that are removed on exit, and the open-transaction
session is terminated server-side if a run is interrupted.

## Targets

```bash
make up          # Start Postgres with wal_level=logical and apply schema
make test        # Uncommitted rows are invisible before COMMIT (asserts)
make break       # Stalled consumer pins WAL; restart_lsn advance; on-disk evidence (asserts)
make cap         # max_slot_wal_keep_size invalidates the slot
make identity    # REPLICA IDENTITY DEFAULT vs FULL
make lag         # Slot cursor, wal_status, consumer lag
make wal         # pg_wal segments versus slot and insert position
make reset-slot  # Recreate the logical slot
make seed        # Restore baseline: identity, WAL cap, empty tables
make consume     # Read pending changes from the slot
make slots       # List replication slots
make psql        # psql shell
make logs        # Follow Postgres logs
make down        # Full teardown
make reset       # down, then up
```

Settings live in `compose.yaml` (`wal_level=logical`, `min_wal_size=32MB`,
`max_wal_size=64MB`, `wal_keep_size=0`). Copy `.env.example` to `.env` to
override the user, database, or port; `POSTGRES_*` only applies at first
initdb, so changing them needs `make reset`. The WAL sizes are deliberately
small so slot-pinned WAL is visible at lab scale.

## Predictions

Written from the lab design before the final runs.

- `make test`: zero rows from the open transaction before COMMIT, and the
  probe message is visible, so the decoder reached past the open transaction.
  After COMMIT, the transaction is decoded whole.
- `make break`: writing ~160MB with the slot stalled leaves `pg_wal` far above
  `max_wal_size` after `CHECKPOINT`, with 0 files removed. Consuming moves
  `confirmed_flush_lsn` immediately but not necessarily `restart_lsn`. After
  `pg_log_standby_snapshot()` and one or two more consumes, `restart_lsn`
  jumps and the next checkpoint removes segments. Segments may linger a little
  behind the slot's segment (not predicted; see step 7).
- `make cap`: with a 16MB cap, ~64MB of stalled writes plus `CHECKPOINT` gives
  `wal_status = lost`, and `get_changes` errors.
- `make identity`: DEFAULT shows the new tuple for UPDATE and the primary key
  only for DELETE. FULL shows the whole old row for both.

## Lifecycle recheck (2026-10-31)

After reconciling with the merged WAL lab, `make -j4 reset test break cap identity`
and separate `make test` and `make break` invocations passed. Every scenario
resets its own slot and baseline rows, even when requested in one invocation;
the shared database lifecycle runs serially under parallel make.

The test observed zero open-transaction rows and one flushed probe before the
commit barrier was released. Exact five-record and six-record decoding checks,
including BEGIN/COMMIT markers, passed. Break passed its on-disk retention and
release assertions; cap reached `wal_status=lost` and rejected consumption;
identity completed DEFAULT and FULL comparisons.

## Measured Output

All outputs below are unedited runs. Lines removed are only `Expanded display
is on.` (psql `\x` toggle noise). Each scenario was run from a freshly reset
slot on PostgreSQL 17.2; LSNs and timings differ per run.

### `make test`

The open transaction has inserted (`backend_xid` is set) and then waits at a commit
barrier: the test keeps its psql input open and sends COMMIT only after the peek.
`pg_logical_emit_message(..., flush => true)` writes a non-transactional probe
after the open transaction's records and flushes all earlier WAL. Seeing the
probe in the peek while `open_tx_rows` is 0 shows the decoder read past the
open transaction and still held its rows back. The probe is then decoded
before the transaction, even though its LSN is higher: output follows commit
order, not WAL position.

```text
 slot_name  |    lsn
------------+-----------
 orders_cdc | 0/194ABA8
(1 row)

== WAL CDC test: only committed changes leave the slot ==
A transaction inserts an order, waits at a commit barrier, then commits.
The slot is peeked only after the open transaction has provably written.

>> open transaction state (backend_xid set means it has written):
 application_name |        state        | backend_xid
------------------+---------------------+-------------
 wal-cdc-open-tx  | idle in transaction |         744
(1 row)

>> before COMMIT, peeking the slot:
 open_tx_rows | probe_seen
--------------+------------
            0 | 1

>> after COMMIT, consuming decoded WAL (probe message first, then the transaction):
SELECT 5
    lsn    | xid |                                                                                                          data
-----------+-----+------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
 0/194D388 |   0 | message: transactional: 0 prefix: cdc-probe, sz: 13 content:before-commit
 0/194CFD0 | 744 | BEGIN 744
 0/194CFD0 | 744 | table public.orders: INSERT: id[bigint]:1 order_ref[text]:'ord-open' status[text]:'created' total_cents[integer]:4200 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 07:49:09.141322+00'
 0/194D1E8 | 744 | table public.order_events: INSERT: id[bigint]:1 order_ref[text]:'ord-open' event_type[text]:'OrderCreated' payload[jsonb]:'{"source": "open-tx"}' created_at[timestamp with time zone]:'2026-10-31 07:49:09.141322+00'
 0/194D3B8 | 744 | COMMIT 744
(5 rows)

 assertion_passed
------------------
                1
(1 row)

>> a committed multi-row transaction is decoded as one ordered unit:
SELECT 6
    lsn    | xid |                                                                                                       data
-----------+-----+-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
 0/1955718 | 747 | BEGIN 747
 0/1955718 | 747 | table public.orders: INSERT: id[bigint]:2 order_ref[text]:'ord-2' status[text]:'created' total_cents[integer]:1100 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 07:49:09.662796+00'
 0/19557F8 | 747 | table public.orders: INSERT: id[bigint]:3 order_ref[text]:'ord-3' status[text]:'created' total_cents[integer]:1200 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 07:49:09.662796+00'
 0/19558D8 | 747 | table public.order_events: INSERT: id[bigint]:2 order_ref[text]:'ord-2' event_type[text]:'OrderCreated' payload[jsonb]:'{"source": "batch"}' created_at[timestamp with time zone]:'2026-10-31 07:49:09.662796+00'
 0/1955990 | 747 | table public.order_events: INSERT: id[bigint]:3 order_ref[text]:'ord-3' event_type[text]:'OrderCreated' payload[jsonb]:'{"source": "batch"}' created_at[timestamp with time zone]:'2026-10-31 07:49:09.662796+00'
 0/1955AE8 | 747 | COMMIT 747
(6 rows)

 assertion_passed
------------------
                1
(1 row)

>> slot after the test:
-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 07:49:09.758767+00
wal_status            | reserved
invalidation_reason   |
restart_lsn           | 0/194AB70
confirmed_flush_lsn   | 0/1955AE8
catalog_xmin          | 742
wal_since_restart_lsn | 72 kB
consumer_lag          | 28 kB
safe_wal_size         |

PASS: uncommitted rows invisible before COMMIT, decoded after.
```

### `make break`

```text
 slot_name  |    lsn
------------+-----------
 orders_cdc | 0/194D7C0
(1 row)

== WAL CDC break: a stalled consumer pins WAL ==
max_wal_size is 64MB. WAL beyond that, still on disk after CHECKPOINT, is
held by the slot (wal_keep_size=0, archiving off, no other slots).

>> 1. baseline, slot freshly consumed
-[ RECORD 1 ]------------+-------------------------
total_files              | 1
pg_wal_size              | 16 MB
max_wal_size             | 64MB
segs_at_or_behind_insert | 1
oldest_seg               | 000000010000000000000001
slot_restart_seg         | 000000010000000000000001
current_seg              | 000000010000000000000001

-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 06:33:57.101723+00
wal_status            | reserved
invalidation_reason   |
restart_lsn           | 0/194D788
confirmed_flush_lsn   | 0/194FAF0
catalog_xmin          | 746
wal_since_restart_lsn | 9272 bytes
consumer_lag          | 208 bytes
safe_wal_size         |

>> 2. writing ~160MB while nobody consumes the slot
batches=15 rows=150000 wal_written=167 MB (target 160 MB)
checkpoint: 2026-10-31 06:34:00.890 UTC [54] LOG:  checkpoint complete: wrote 1404 buffers (8.6%); 0 WAL file(s) added, 0 removed, 0 recycled; write=0.006 s, sync=0.004 s, total=0.014 s; sync files=4, longest=0.002 s, average=0.001 s; distance=12811 kB, estimate=44147 kB; lsn=0/C0832A8, redo lsn=0/C083250

>> 3. after CHECKPOINT: pg_wal stays above max_wal_size, oldest segment is the slot's
-[ RECORD 1 ]------------+-------------------------
total_files              | 12
pg_wal_size              | 192 MB
max_wal_size             | 64MB
segs_at_or_behind_insert | 12
oldest_seg               | 000000010000000000000001
slot_restart_seg         | 000000010000000000000001
current_seg              | 00000001000000000000000C

-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 06:33:57.101723+00
wal_status            | extended
invalidation_reason   |
restart_lsn           | 0/194D788
confirmed_flush_lsn   | 0/194FAF0
catalog_xmin          | 746
wal_since_restart_lsn | 167 MB
consumer_lag          | 167 MB
safe_wal_size         |

PASS: pg_wal above max_wal_size, nothing removed, wal_status=extended

>> 4. consume the slot (logical_decoding_work_mem=1MB so the reorder buffer spills)
SET
 decoded_rows
--------------
       150030
(1 row)

-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 06:34:02.495698+00
wal_status            | extended
invalidation_reason   |
restart_lsn           | 0/194FB10
confirmed_flush_lsn   | 0/C083320
catalog_xmin          | 748
wal_since_restart_lsn | 167 MB
consumer_lag          | 0 bytes
safe_wal_size         |

 slot_name  | spill_txns | spill_count | spill_bytes
------------+------------+-------------+-------------
 orders_cdc |         15 |         165 | 153 MB
(1 row)

>> 5. restart_lsn only moves once decoding passes a new running-xacts record
   marker (insert position before consuming) = 0/C083320
   pg_log_standby_snapshot() + consume, round 1: restart_lsn >= marker? f
   pg_log_standby_snapshot() + consume, round 2: restart_lsn >= marker? t
   restart_lsn passed the marker after 2 extra round(s)
-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 06:34:03.274792+00
wal_status            | reserved
invalidation_reason   |
restart_lsn           | 0/C083358
confirmed_flush_lsn   | 0/C083390
catalog_xmin          | 763
wal_since_restart_lsn | 56 bytes
consumer_lag          | 0 bytes
safe_wal_size         |

>> 6. now a checkpoint can remove or recycle the old segments
checkpoint: 2026-10-31 06:34:03.604 UTC [54] LOG:  checkpoint complete: wrote 0 buffers (0.0%); 0 WAL file(s) added, 6 removed, 3 recycled; write=0.001 s, sync=0.001 s, total=0.018 s; sync files=0, longest=0.000 s, average=0.000 s; distance=0 kB, estimate=39732 kB; lsn=0/C0833E8, redo lsn=0/C083390
-[ RECORD 1 ]------------+-------------------------
total_files              | 6
pg_wal_size              | 96 MB
max_wal_size             | 64MB
segs_at_or_behind_insert | 3
oldest_seg               | 00000001000000000000000A
slot_restart_seg         | 00000001000000000000000C
current_seg              | 00000001000000000000000C

PASS: oldest segment moved forward 000000010000000000000001 -> 00000001000000000000000A

>> 7. segments left behind the slot are held by server memory, not by the slot
oldest segment 00000001000000000000000A is older than the slot's 00000001000000000000000C. A second plain checkpoint:
checkpoint: 2026-10-31 06:34:04.182 UTC [54] LOG:  checkpoint complete: wrote 0 buffers (0.0%); 0 WAL file(s) added, 0 removed, 0 recycled; write=0.001 s, sync=0.001 s, total=0.011 s; sync files=0, longest=0.000 s, average=0.000 s; distance=0 kB, estimate=35759 kB; lsn=0/C0834B8, redo lsn=0/C083460
oldest segment after the second checkpoint: 00000001000000000000000A (slot: 00000001000000000000000C)
Still older than the slot's. Restarting the server and checkpointing again:
checkpoint: 2026-10-31 06:34:06.991 UTC [26] LOG:  checkpoint complete: wrote 3 buffers (0.0%); 0 WAL file(s) added, 2 removed, 0 recycled; write=0.001 s, sync=0.003 s, total=0.014 s; sync files=2, longest=0.002 s, average=0.002 s; distance=0 kB, estimate=0 kB; lsn=0/C083600, redo lsn=0/C0835A8
-[ RECORD 1 ]------------+-------------------------
total_files              | 4
pg_wal_size              | 64 MB
max_wal_size             | 64MB
segs_at_or_behind_insert | 1
oldest_seg               | 00000001000000000000000C
slot_restart_seg         | 00000001000000000000000C
current_seg              | 00000001000000000000000C

PASS: after restart + CHECKPOINT the oldest segment equals the slot's
```

How to read it:

- Step 3: `pg_wal` is 192MB against `max_wal_size = 64MB`, the oldest segment is
  the slot's segment, `wal_status` is `extended`, and the checkpoint removed 0
  files. Nothing else pins WAL here (`wal_keep_size=0`, no archiving, one
  slot), so the excess is the slot's.
- Step 4: consuming moved `confirmed_flush_lsn` to the insert position, but
  `restart_lsn` barely moved and `wal_since_restart_lsn` is still 167 MB.
  Confirming is not enough.
- Step 5: `restart_lsn` passed the marker only after `pg_log_standby_snapshot()`
  and a second consume. The number of rounds varies run to run; the loop is
  bounded at 5.
- Step 6: now the checkpoint removed 6 files and recycled 3, and the oldest
  segment moved from `...01` to `...0A` (asserted). It stops at `...0A`, two
  segments short of the slot's `...0C`.
- Step 7: those two leftover segments are not pinned by the slot. A second plain
  checkpoint removes nothing (shown in the run), but restarting the server and
  checkpointing again removes both and leaves the oldest segment equal to the
  slot's (asserted). So the removal cutoff in the running server lags the
  slot's actual `restart_lsn`. The lab does not trace which internal state
  holds it, and in ad hoc runs outside the scripts the leftover varied from
  none to a couple of segments, so the exact amount is not stable.
- `logical_decoding_work_mem` was set to 1MB for the consume, so each ~10MB
  transaction spilled to `pg_replslot/`: `spill_txns = 15`, `153 MB` spilled.
  With the default 64MB the same transactions would decode in memory.

### `make cap`

`max_slot_wal_keep_size` is set at runtime (`ALTER SYSTEM` + reload) and reset
by a trap on exit, so it never leaks into other targets. In PostgreSQL 17 an
invalidated slot has `restart_lsn = NULL`.

```text
 slot_name  |    lsn
------------+-----------
 orders_cdc | 0/C0835E0
(1 row)

== WAL CDC cap: max_slot_wal_keep_size invalidates a stalled slot ==
>> max_slot_wal_keep_size in a fresh session: 16MB

>> 1. before writes: slot is reserved, safe_wal_size is populated
-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 06:32:32.907743+00
wal_status            | reserved
invalidation_reason   |
restart_lsn           | 0/C0835A8
confirmed_flush_lsn   | 0/C08CC20
catalog_xmin          | 763
wal_since_restart_lsn | 38 kB
consumer_lag          | 0 bytes
safe_wal_size         | 31 MB

>> 2. writing ~64MB with the slot stalled
batches=6 rows=60000 wal_written=66 MB (target 64 MB)

>> 3. after CHECKPOINT: the slot is invalidated
-[ RECORD 1 ]---------+------------------------------
slot_name             | orders_cdc
active                | f
inactive_since        | 2026-10-31 06:32:35.496219+00
wal_status            | lost
invalidation_reason   | wal_removed
restart_lsn           |
confirmed_flush_lsn   | 0/C08CC20
catalog_xmin          | 763
wal_since_restart_lsn |
consumer_lag          | 67 MB
safe_wal_size         |

>> 4. the consumer can no longer read the slot:
ERROR:  can no longer get changes from replication slot "orders_cdc"
DETAIL:  This slot has been invalidated because it exceeded the maximum reserved size.

Recovery is drop the slot, recreate it, and resnapshot: the changes
between the lost position and the new slot are gone from the stream.
```

### `make identity`

`ALTER TABLE ... REPLICA IDENTITY` is DDL, so it does not appear in the decoded
stream. It must run before the DML it should affect, because identity is
applied when the row change is written to WAL.

```text
 slot_name  |    lsn
------------+------------
 orders_cdc | 0/10358960
(1 row)

== WAL CDC identity: what UPDATE and DELETE carry ==

>> DEFAULT identity (primary key only)
774 | table public.orders: UPDATE: id[bigint]:1 order_ref[text]:'ord-a' status[text]:'paid' total_cents[integer]:100 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 06:32:36.803374+00'
775 | table public.orders: DELETE: id[bigint]:2

>> ALTER TABLE orders REPLICA IDENTITY FULL (DDL: not in the decoded stream)
777 | table public.orders: UPDATE: old-key: id[bigint]:3 order_ref[text]:'ord-c' status[text]:'created' total_cents[integer]:300 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 06:32:36.803374+00' new-tuple: id[bigint]:3 order_ref[text]:'ord-c' status[text]:'paid' total_cents[integer]:300 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 06:32:36.803374+00'
778 | table public.orders: DELETE: id[bigint]:4 order_ref[text]:'ord-d' status[text]:'created' total_cents[integer]:400 notes[text]:'' created_at[timestamp with time zone]:'2026-10-31 06:32:36.803374+00'
```

## Notes

- `restart_lsn` is the oldest WAL position this slot still needs. It cannot
  pass the start of any transaction that was running at the last
  running-transactions record.
- `confirmed_flush_lsn` is the latest position the consumer has confirmed.
- `wal_since_restart_lsn` in `make lag` is an LSN distance, not bytes on disk.
  Use `make wal` for disk. File count misleads because checkpoints recycle
  (rename) old segments; compare segment names.
- `safe_wal_size` is NULL while `max_slot_wal_keep_size = -1`.
- Debezium stores its own offsets too, but the PostgreSQL slot is still the
  database-side retention boundary.
- Logical decoding buffers each transaction until commit, so transaction size
  affects latency and server memory (the reorder buffer spills past
  `logical_decoding_work_mem`).
- An inactive logical slot also holds `catalog_xmin`, which blocks vacuum on
  system catalogs.

## References

- PostgreSQL docs: [Logical Decoding](https://www.postgresql.org/docs/current/logicaldecoding.html)
- PostgreSQL docs: [Replication Slots](https://www.postgresql.org/docs/current/warm-standby.html#STREAMING-REPLICATION-SLOTS)
- PostgreSQL docs: [pg_replication_slots](https://www.postgresql.org/docs/current/view-pg-replication-slots.html)
- PostgreSQL docs: [Write-Ahead Logging](https://www.postgresql.org/docs/current/wal-intro.html)
- Debezium docs: [PostgreSQL connector](https://debezium.io/documentation/reference/stable/connectors/postgresql.html)
