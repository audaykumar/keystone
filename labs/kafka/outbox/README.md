# Lab: Transactional outbox + CDC

A service that writes to its database *and* publishes an event performs two
writes with no transaction spanning them. This lab crashes a writer inside
that gap to lose an event, then closes the gap with the transactional outbox
pattern: the event commits in the same database transaction as the state
change, and Debezium streams it to Kafka by tailing the WAL.

## Scenario

Each of 1,000 orders should produce one `order.created` event. The writer
crashes (hard `os.Exit`, no cleanup) after committing order 500, before
publishing its event. The restarted writer skips orders that already exist,
exactly like a real idempotent retry would — which is precisely why the lost
event never reappears in the dual-write version.

| Mode | Write path | After the crash |
|---|---|---|
| `dualwrite` | commit order, then publish to Kafka | order 500 exists, its event does not; audit exits 1 |
| `outbox` | order + outbox row in one transaction; Debezium publishes | no gap existed; audit matches all orders, exit 0 |

The audit is set arithmetic, not trust: read `orders` from Postgres, scan the
event topic from offset zero, and report every committed order that has no
event.

## Stack

- Postgres 17 with `wal_level=logical`
- Kafka 3.8 (KRaft, single broker)
- Debezium Connect 3.0 with the Postgres connector (`pgoutput` plugin) watching
  only `public.outbox`
- Go driver for writes, crashes, and the audit

## Commands

```bash
make up           # postgres + kafka + connect + migrations + connector
make status       # Debezium connector status
make break        # dual-write + crash: audit exits 1 with the missing order
make test         # outbox + same crash: audit exits 0
make audit-direct # re-run the dual-write audit
make audit-cdc    # re-run the CDC audit
make outbox-rows  # peek at the outbox table
make logs         # follow connect logs
make down         # full teardown
```

## What to observe

- The dual-write loss needs no exotic failure: any crash, deploy, or OOM kill
  between commit and publish loses the event. Retries do not help, because
  the state write is idempotent and skips the row.
- Reversing the order (publish first, then commit) converts loss into ghost
  events: consumers see an order that was never committed. The gap moves; it
  does not close.
- In outbox mode, the crash lands in the same spot, but there is nothing to
  lose: the event row committed with the order. Debezium reads committed
  transactions from the replication slot, so consumers see exactly the
  committed history, in commit order.
- CDC delivery is at-least-once: after a connector restart, some outbox rows
  may be re-emitted. Downstream consumers still need idempotency (see the
  delivery-semantics lab); the outbox solves atomicity, not deduplication.
- The outbox table grows forever unless trimmed. Production setups delete
  published rows or use Debezium's outbox event router with a retention job.

## Measured results (2026-07-07)

```text
make break (dual write):
  CRASH injected after committing ord-500, before publishing its event
  audit(direct): orders in postgres=1000, events in order-events=999
    orders with no event: 1 [ord-500]
  RESULT: committed state and published events diverged. exit 1

make test (outbox):
  CRASH injected after committing ord-500
  audit(cdc): orders in postgres=1000, events in shop.public.outbox=1000
    orders with no event: 0
  RESULT: every committed order has an event. exit 0
```

The restarted dual-write run finished the remaining 499 orders perfectly and
still could not recover ord-500's event — idempotent retries skip the
committed order and therefore never re-publish. In outbox mode the identical
crash left nothing to lose: Debezium delivered all 1,000 events, including
ord-500's, from the WAL.

## References

- [Pattern: Transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html) — Chris Richardson, the canonical write-up
- Debezium: [Reliable Microservices Data Exchange With the Outbox Pattern](https://debezium.io/blog/2019/02/19/reliable-microservices-data-exchange-with-the-outbox-pattern/) — Gunnar Morling
- Debezium docs: [PostgreSQL connector](https://debezium.io/documentation/reference/stable/connectors/postgresql.html) and [Outbox Event Router](https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html)
- Martin Kleppmann, [Using logs to build a solid data infrastructure](https://www.confluent.io/blog/using-logs-to-build-a-solid-data-infrastructure-or-why-dual-writes-are-a-bad-idea/) — why dual writes are a bad idea, from first principles
- Martin Kleppmann, *Designing Data-Intensive Applications*, ch. 11 — change data capture and event sourcing
- PostgreSQL docs: [Logical Decoding](https://www.postgresql.org/docs/current/logicaldecoding.html)
- [Revisiting the Outbox Pattern](https://www.decodable.co/blog/revisiting-the-outbox-pattern) — Gunnar Morling, trade-offs and alternatives (listen-to-yourself, CDC-only)
