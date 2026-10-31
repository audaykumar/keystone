# Lab: RabbitMQ dead-letter queues and poison-message redelivery

RabbitMQ deletes a message the moment it is acked and redelivers it the
moment it is nacked with requeue. Without a place for a message that can
never succeed to go, that redelivery loop runs forever and, with prefetch
bounding how many messages a consumer can hold at once, starves the good
work sitting behind it. A dead-letter exchange plus a retry-count guard give
the poison message somewhere to go instead.

## Scenario

One binary, two roles (`ROLE` env), one topology: a direct exchange
`lab.direct` bound to queue `work.queue`, plus a dead-letter exchange
`lab.dlx` bound to queue `work.dlq`. Both are always declared; what changes
between scenarios is the consumer retry policy and whether `work.queue` carries
`x-dead-letter-exchange`/`x-dead-letter-routing-key` pointing at them.

`producer` deletes and redeclares the topology for the chosen `MODE`, then
publishes 30 labeled messages, one of them (`seq=5`) a poison message the
consumer always fails to process. Both current scenarios use manual ack and
prefetch 1; they differ in retry policy and dead-letter handling:

| | `MODE=break` (prefetch=1) | `MODE=test` (prefetch=1) |
|---|---|---|
| `work.queue` dead-letter args | none | `x-dead-letter-exchange: lab.dlx` |
| Poison handling | `nack(requeue=true)` every time, capped at `BREAK_CAP` redeliveries so the demo terminates | Retries via `x-retry-count` header: republish to the tail of the queue and ack the original, up to `MAX_ATTEMPTS`; final attempt is `nack(requeue=false)`, which RabbitMQ dead-letters into `work.dlq` |
| Where the poison ends up | Stuck in `work.queue`, requeued and unresolved | `work.dlq`, exactly 1 message |
| What it demonstrates | Head-of-line blocking: with only 1 unacked slot, the broker keeps handing the consumer the requeued poison instead of anything queued behind it | Bounded republish-to-tail retries let good work advance, even with prefetch 1, so it visibly finishes out of its produced position instead of blocking everything |

Good messages are acked immediately (after a small simulated 5ms of work) and
are never touched by the retry/dead-letter logic. The consumer's own report
tracks produced order (1..30) versus completed order (the order messages are
finally acked or dead-lettered), so the ordering effect of requeue is visible
directly instead of inferred.

## Commands

```bash
make up      # start RabbitMQ (management image, healthcheck + --wait)
make break   # no DLQ: poison redelivers forever, capped and reported
make test    # DLX + retry-count guard: poison dead-lettered after 3 attempts
make prefetch # same retry policy at prefetch 1 then 5, with queue depths
make dlq     # inspect work.dlq via the management API
make depths  # print work.queue and work.dlq depths
make logs    # follow the broker's logs
make down    # full teardown: containers, network, volumes, built image
```

`RABBITMQ_DEFAULT_USER`/`RABBITMQ_DEFAULT_PASS` create a real `lab` user for
the broker, because the built-in `guest` user is restricted to loopback
connections by default and would refuse AMQP logins from another container.

## Historical results (2026-07-08, before factual-audit corrections)

These runs changed both retry strategy and prefetch; they do not isolate prefetch.
The current test fixes prefetch at 1, confirms replacement publishes before ack,
and checks unroutable returns. The captured numbers below are historical.

Real output from `make break` (30 messages, poison at seq=5, prefetch=1,
`BREAK_CAP=20`):

```text
producer: mode=break published=30 poison_at=5 exchange=lab.direct queue=work.queue dlq_on_queue=false
consumer: break cap reached at 20 redeliveries; poison message left in work.queue, requeued and unresolved

=== consumer report: mode=break ===
good_processed=4/29 poison_attempts=20 poison_resolved=false elapsed=24ms good_throughput=169.1 msg/s
produced_order:  [1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30]
completed_order: [1 2 3 4]
ordering: poison seq=5 produced at position 5 of 30; never completed within this run

>> queue depths:
work.queue messages=26
work.dlq   messages=0
```

Real output from `make test` (same 30 messages, prefetch=5, `MAX_ATTEMPTS=3`):

```text
producer: mode=test published=30 poison_at=5 exchange=lab.direct queue=work.queue dlq_on_queue=true
consumer: attempt 3/3 exhausted; poison seq=5 dead-lettered to work.dlq

=== consumer report: mode=test ===
good_processed=29/29 poison_attempts=3 poison_resolved=true elapsed=172ms good_throughput=168.9 msg/s
produced_order:  [1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30]
completed_order: [1 2 3 4 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 5]
ordering: poison seq=5 produced at position 5 of 30; completed at position 30 of 30 (delta=25)
ordering: inversions in completed_order=25 (0 means every completed message finished in produced order)

>> queue depths:
work.queue messages=0
work.dlq   messages=1
```

The historical break run showed native requeue repeatedly occupying the only
prefetch slot. The historical test used republish-to-tail, a retry limit, DLX,
and prefetch 5. These results do not isolate prefetch: placing the replacement
at the tail allows good work to advance even with prefetch 1. The corrected
comparison fixes prefetch at 1; `make prefetch` holds the bounded retry policy
constant while comparing 1 and 5. Historical completion order is an observation
of that run, not a guarantee for arbitrary broker scheduling.

## What each part does

- **Manual ack / nack.** `basic.consume` with `auto_ack=false` means the
  broker holds a message as unacknowledged until the consumer explicitly
  acks (done, remove it) or nacks (requeue it, or drop/dead-letter it).
- **Prefetch (QoS).** `basic.qos(prefetch_count=N)` caps how many unacked
  messages a consumer can hold at once. It bounds a slow or crashed
  consumer's blast radius, but it is also the mechanism behind head-of-line
  blocking: a small prefetch means a stuck message occupies a
  disproportionate share of the consumer's available delivery slots.
- **Dead-letter exchange.** A queue argument, `x-dead-letter-exchange`
  (optionally `x-dead-letter-routing-key`), that tells RabbitMQ where to
  route a message that is rejected without requeue (`nack`/`reject` with
  `requeue=false`) or that expires via TTL or queue-length overflow. Without
  it, a rejected-without-requeue message is simply discarded.
- **Retry-count guard.** RabbitMQ does not track a redelivery count per
  message on classic queues the way a `x-death` array does once a message
  has actually been dead-lettered at least once. To cap retries *before*
  the first dead-letter, this lab has the consumer republish the poison to
  the tail of the queue with an incremented `x-retry-count` header and ack
  the original, so it can count attempts and only dead-letter on the final
  one. `x-death` becomes populated once a message actually is dead-lettered
  and is the right tool for counting dead-letter *cycles* (e.g., a DLQ that
  parks messages and re-publishes them back to the main queue after a TTL);
  this lab's guard runs before that point.

## What to observe

- Requeue is not "put it back where it was." It is "make it available for
  redelivery," and what a consumer does with the next available message
  (retry the same one immediately, or let others through) is entirely a
  function of prefetch and consumer logic, not a broker ordering guarantee.
- A DLQ does not require giving up on retries; it requires deciding, in
  advance, how many retries are worth attempting and what "attempt" means
  for a message that carries no built-in retry counter.
- The republish-to-tail retry pattern used here trades one property for
  another: it avoids a poison message monopolizing the front of the queue
  (unlike raw `nack(requeue=true)`), but it also means a message can outlive
  several redeliveries' worth of the *other* messages that were produced
  after it, which is its own kind of reordering.
- `work.dlq` depth is the operational signal: 0 means nothing has needed
  intervention; 1 means exactly one message is sitting there for a human (or
  a replay job) to look at. That is a materially different alerting signal
  than "redelivery count is climbing," which this lab's `break` scenario
  shows can climb indefinitely with nobody watching.

## References

- RabbitMQ docs: [Consumer Acknowledgements & Publisher Confirms](https://www.rabbitmq.com/docs/confirms)
- RabbitMQ docs: [Dead Letter Exchanges](https://www.rabbitmq.com/docs/dlx)
- RabbitMQ docs: [Quorum Queues](https://www.rabbitmq.com/docs/quorum-queues)
- RabbitMQ docs: [Consumer Prefetch](https://www.rabbitmq.com/docs/consumer-prefetch)
- CloudAMQP, [13 Common RabbitMQ Mistakes](https://www.cloudamqp.com/blog/part4-rabbitmq-13-common-errors.html)
- CloudAMQP, [Message Dead Lettering with RabbitMQ](https://www.cloudamqp.com/blog/when-and-how-to-use-the-rabbitmq-dead-letter-exchange.html)
- AWS, [Amazon SQS dead-letter queues](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html) (for the contrast with a competing-consumer queue that has its own DLQ mechanism)
- Hohpe & Woolf, *Enterprise Integration Patterns* — Dead Letter Channel, Guaranteed Delivery, Competing Consumers
- Book: Martin Kleppmann, *Designing Data-Intensive Applications*, ch. 11, on message brokers versus logs
- `labs/kafka/delivery` and `docs/delivery-semantics/overview.html` — the companion topic on at-least-once delivery and idempotent consumers; RabbitMQ redelivery is the same at-least-once contract from a queue instead of a log.

### Delivery safety

Replacement publishes use confirms and mandatory routing before the original ack.
A crash between confirmation and ack can duplicate work. Default classic-queue
DLX forwarding can still lose messages when its target is unavailable; it is not
a durable transfer guarantee. See [DLX safety](https://www.rabbitmq.com/docs/3.13/dlx#safety).

## Controlled recheck (2026-10-31)

Same bounded republish-to-tail policy (30 messages, poison at seq 5, 3 attempts), prefetch 1 then 5, from `make prefetch`. Build output and blank lines are removed; values are unedited.

```text
== prefetch=1 ==
MODE=test COUNT=30 POISON_AT=5 MAX_ATTEMPTS=3 PROCESS_MS=5 PREFETCH=1 docker compose run --rm producer
producer: mode=test published=30 poison_at=5 exchange=lab.direct queue=work.queue dlq_on_queue=true
MODE=test COUNT=30 POISON_AT=5 MAX_ATTEMPTS=3 PROCESS_MS=5 PREFETCH=1 docker compose run --rm consumer
consumer: attempt 3/3 exhausted; poison seq=5 dead-lettered to work.dlq
=== consumer report: mode=test prefetch=1 ===
good_processed=29/29 poison_attempts=3 poison_resolved=true elapsed=191ms good_throughput=151.9 msg/s
produced_order:  [1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30]
completed_order: [1 2 3 4 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 5]
ordering: poison seq=5 produced at position 5 of 30; completed at position 30 of 30 (delta=25)
ordering: inversions in completed_order=25 (0 means every completed message finished in produced order)
>> queue depths:
work.queue messages=0
work.dlq   messages=1
== prefetch=5 ==
MODE=test COUNT=30 POISON_AT=5 MAX_ATTEMPTS=3 PROCESS_MS=5 PREFETCH=5 docker compose run --rm producer
producer: mode=test published=30 poison_at=5 exchange=lab.direct queue=work.queue dlq_on_queue=true
MODE=test COUNT=30 POISON_AT=5 MAX_ATTEMPTS=3 PROCESS_MS=5 PREFETCH=5 docker compose run --rm consumer
consumer: attempt 3/3 exhausted; poison seq=5 dead-lettered to work.dlq
=== consumer report: mode=test prefetch=5 ===
good_processed=29/29 poison_attempts=3 poison_resolved=true elapsed=201ms good_throughput=143.9 msg/s
produced_order:  [1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30]
completed_order: [1 2 3 4 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 5]
ordering: poison seq=5 produced at position 5 of 30; completed at position 30 of 30 (delta=25)
ordering: inversions in completed_order=25 (0 means every completed message finished in produced order)
>> queue depths:
work.queue messages=0
work.dlq   messages=1
```

Both settings completed all 29 good messages, dead-lettered the poison, and left `work.queue` empty with one message in `work.dlq`. The elapsed times differ by about 10ms, which a single short run cannot attribute to prefetch. These are single short runs, not a throughput benchmark. The historical native-requeue `make break` run (2026-07-08) processed only 4/29 good messages before its 20-attempt cap.
