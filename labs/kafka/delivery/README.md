# Lab: Delivery semantics and idempotency

"Exactly-once delivery" does not exist between separate systems; exactly-once
*effect* does, and it is something the consumer builds. This lab makes the
difference concrete with money: a Kafka topic of payment events, a Postgres
balance, and a consumer that crashes at the worst possible moment under three
different semantics.

## Scenario

1,000 events each credit account `acct-1` with 100 cents. The correct final
balance is exactly 100,000 cents. The consumer's crash point is between
"apply the credit" and "commit the offset" — the window every consumer has,
usually invisible until an incident makes it visible.

| Semantics | Order of operations | After a crash | Balance |
|---|---|---|---|
| at-most-once | commit offset, then apply | events skipped forever | shortfall |
| at-least-once | apply, then commit offset | events replayed | overshoot |
| idempotent | at-least-once + dedupe insert in the same DB txn | replays become no-ops | exact |

The dedupe mechanism is one table: `processed_events(event_id PRIMARY KEY)`.
Inserting a seen id conflicts, and because the insert shares a transaction
with the balance update, "check" and "apply" cannot be separated by a crash.

## Commands

```bash
make up      # kafka + postgres + topic + migrations
make break   # at-least-once + crash at event 500 -> overshoot, exit 1
make lose    # at-most-once + crash at event 500 -> shortfall, exit 1
make test    # idempotent + same crash -> exact balance, exit 0
make verify  # re-check the balance any time
make psql    # inspect balances and processed_events
make down    # full teardown
```

Each scenario reseeds: balance to zero, dedupe table truncated, topic
recreated, group offsets deleted. Scenarios are order-independent.

## What to observe

- The crash uses `os.Exit(1)`: no deferred cleanup, no graceful shutdown,
  exactly like a SIGKILL or OOM kill. The offset commit that "should" have
  happened does not.
- In `make break`, the restarted consumer re-fetches from the last committed
  offset and re-applies credits that already happened. Money is created.
- In `make lose`, the offsets were committed ahead of processing, so the
  restarted consumer never sees the crashed batch. Money vanishes.
- In `make test`, the replayed events hit the `processed_events` primary key
  and are skipped inside the same transaction as the credit. The balance is
  exact, and the consumer reports how many events it skipped as duplicates.
- Kafka's own "exactly-once semantics" (idempotent producer + transactions)
  covers Kafka-to-Kafka pipelines. The moment the side effect leaves Kafka —
  a database row, an HTTP call, an email — the consumer owns idempotency.

## Measured results (2026-07-07)

All three scenarios run with 1,000 events, crash injected around event 500,
consumer batch-committing offsets every 100 messages:

```text
make break (at-least-once):
  CRASH injected after applying 500 events, before committing the offset
  idle, stopping. applied=600 skipped-as-duplicate=0
  verify: balance=110000 cents, expected=100000 cents
  RESULT: overshoot of 10000 cents — duplicated events. exit 1

make lose (at-most-once):
  CRASH injected after committing the offset for pay-499, before applying it
  verify: balance=99900 cents, expected=100000 cents
  RESULT: shortfall of 100 cents — lost events. exit 1

make test (idempotent):
  CRASH injected after applying 500 events, before committing the offset
  idle, stopping. applied=500 skipped-as-duplicate=100
  verify: balance=100000 cents, expected=100000 cents
  RESULT: exact. every event applied exactly once. exit 0
```

The overshoot is exactly the replay window: the last batch commit covered
events through 400, so the restarted member replayed 100 already-applied
events. The idempotent run replayed the same 100 and skipped every one.

## References

- [You Cannot Have Exactly-Once Delivery](https://bravenewgeek.com/you-cannot-have-exactly-once-delivery/) — Tyler Treat; the crisp impossibility argument
- Confluent: [Exactly-Once Semantics Are Possible: Here's How Kafka Does It](https://www.confluent.io/blog/exactly-once-semantics-are-possible-heres-how-apache-kafka-does-it/) — Neha Narkhede, on what EOS covers and what it does not
- [Enabling Exactly-Once in Kafka Streams](https://www.confluent.io/blog/enabling-exactly-once-kafka-streams/) — Guozhang Wang
- Stripe: [Designing robust and predictable APIs with idempotency](https://stripe.com/blog/idempotency) — the same dedupe idea at the API boundary
- AWS Builders' Library: [Making retries safe with idempotent APIs](https://aws.amazon.com/builders-library/making-retries-safe-with-idempotent-APIs/) — Malcolm Featonby
- Martin Kleppmann, *Designing Data-Intensive Applications*, ch. 11 — fault tolerance in stream processing, idempotence, and "exactly-once" unpacked
- Pat Helland, [Idempotence Is Not a Medical Condition](https://queue.acm.org/detail.cfm?id=2187821) — ACM Queue classic on messaging reality
