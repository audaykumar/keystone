# Lab: Kafka internals

A 3-broker KRaft cluster exposing the mechanics that matter: partitions,
keys, leadership, the in-sync replica set (ISR), consumer groups, rebalances,
and the acks=1 loss window. One question drives the lab: what exactly does a
producer acknowledgement promise, and what does a broker failure do to that
promise?

## Scenario

The `payments` topic has 6 partitions, replication factor 3, and
`min.insync.replicas=2`. The Go driver produces sequence-numbered messages
keyed by account id, consumes them as a group member, and audits the log by
scanning every partition and reporting unique, duplicate, and missing
sequences. Loss is therefore measured, not asserted.

## Commands

```bash
make up        # 3-broker KRaft cluster + topic (6 partitions, RF 3, min ISR 2)
make describe  # partitions, leaders, ISR
make produce   # 10k keyed messages, acks=all
make consume   # group member; run in its own terminal
make consume2  # second member: watch a rebalance move partitions
make lag       # consumer group members, assignments, lag
make break     # acks=1 producer; run 'make kill' mid-run; audit exits 1 only if an acknowledged ID is missing
make test      # acks=all producer under the same kill; audit exits 0 unless an acknowledged ID is missing
make kill      # stop broker 2
make revive    # restart broker 2, watch ISR recover
make audit     # compare current run against its persisted acknowledged-ID manifest
make down      # full teardown
```

## What to observe

### Keys and partitions

Same key always hashes to the same partition, which is the only ordering
guarantee Kafka gives: per partition, not per topic. The audit output shows
the per-partition distribution of the 10 account keys.

### Consumer groups and rebalances

One member owns all 6 partitions. Start `make consume2` and the group
coordinator revokes and reassigns partitions (3/3 split); stop it and they
move back. This is the mechanism behind horizontal consumer scaling, and also
behind rebalance storms when members flap.

### Leadership and ISR (`make kill` + `make describe`)

Each partition has one leader; followers replicate its log. Stopping broker 2
forces leadership of its partitions onto surviving replicas and shrinks the
ISR from 3 to 2. Reviving it shows catch-up then ISR re-entry.

### The acks=1 loss window (`make break`)

With acks=1 the leader acknowledges before followers replicate. Kill the
leader in that window: a clean election picks an in-sync follower, but that
follower may not have the acked message. The producer saw success; the log
lost data. The audit makes it concrete when it happens: acknowledged sequence
numbers missing from the log, exit 1. Whether any go missing depends on the
kill landing inside the replication window.

With `acks=all` and `min.insync.replicas=2`, acknowledged writes have the
ISR durability boundary. A single broker loss need not produce an application
error: remaining ISR members and retries may keep writes succeeding. Errors
appear if the minimum cannot be met or recovery exceeds client limits.

The driver persists a completed run manifest in a named evidence volume: unique
run ID, topic, attempted count, and exact acknowledged and failed IDs. Records
carry the run ID so older runs cannot mask missing data. Audit compares only
acknowledged IDs, reports failed sends separately (some may have reached Kafka),
and refuses an absent/incomplete manifest. A missing attempted-but-failed send
is not evidence of acknowledged loss. `make down` removes the manifest volume.
A broker-kill loss window is timing-dependent; a clean audit does not prove that
acks=1 is safe. Use `COUNT=600 make test` for a shorter run.

Driver commands run with `--no-deps`, so they never start or restart a broker
(otherwise `make audit` would bring a killed broker back). Run `make up` first;
broker 2 stays down after `make kill` until `make revive`.

## Measured results (2026-07-07, original audit format)

These runs predate the manifest audit: sequences were counted without a run ID.

Verified end to end: cluster formation, topic creation, keyed produce, and
the partition audit.

```text
make describe:
  Topic: payments  PartitionCount: 6  ReplicationFactor: 3  Configs: min.insync.replicas=2
    Partition: 0  Leader: 3  Replicas: 3,1,2  Isr: 3,1,2
    Partition: 1  Leader: 1  Replicas: 1,2,3  Isr: 1,2,3
    ... (leaders spread across all three brokers)

produce 600 keyed messages (10 keys), acks=all, then audit:
  partition 0: 60   partition 1: 60   partition 2: 60
  partition 3: 60   partition 4: 180  partition 5: 180
  unique sequences found: 600, duplicates: 0, missing: 0 → exit 0
```

Note the key skew: 10 keys hashed onto 6 partitions gave two partitions 3x
the traffic of the others. That is the hot-partition lesson arriving
uninvited — keys shape load, and few distinct keys means unbalanced
partitions no matter how many you provision.

The `make break` / `make kill` loss-window scenario is scripted but its
evidence has not been captured yet; run it and record the audit output here.

## References

- [Kafka documentation: Design](https://kafka.apache.org/documentation/#design) — replication, ISR, and the producer acknowledgement contract, from the source
- Jay Kreps, [The Log: What every software engineer should know about real-time data's unifying abstraction](https://engineering.linkedin.com/distributed-systems/log-what-every-software-engineer-should-know-about-real-time-datas-unifying) — the essay behind Kafka's model
- [Kafka: a Distributed Messaging System for Log Processing](https://notes.stephenholiday.com/Kafka.pdf) — the original paper
- Confluent: [Hands-free Kafka Replication](https://www.confluent.io/blog/hands-free-kafka-replication-a-lesson-in-operational-simplicity/) — why ISR instead of quorum replication
- Jack Vanlightly, [Kafka replication internals series](https://jack-vanlightly.com/blog/2023/4/24/why-apache-kafka-doesnt-need-fsync-to-be-safe) — recovery, fsync, and what acks really promise
- Martin Kleppmann, *Designing Data-Intensive Applications*, ch. 11 "Stream Processing" and ch. 5 "Replication"
- [KRaft: Apache Kafka Without ZooKeeper](https://developer.confluent.io/learn/kraft/) — the metadata quorum this lab runs on

## Recheck (2026-10-31)

A 600-attempt `acks=all` run (`COUNT=600 make test`) with `make kill` issued about
five seconds in. `make kill` is `docker compose stop kafka2`, a graceful stop: the
broker runs controlled shutdown and hands its partition leadership to other
replicas before it exits, so this is not a crash test. Real output; make banner
text, build output, the per-second progress lines, and blank lines are removed,
values unedited:

```text
done: acked=600 failed=0
manifest saved: run=386214e2965c25275d57ab8c attempted=600 acked=600 failed=0
audit: topic=payments run=386214e2965c25275d57ab8c attempted=600 acked=600 failed=0 failed_present=0
  partition 0: 60 messages
  partition 1: 60 messages
  partition 2: 60 messages
  partition 3: 60 messages
  partition 4: 180 messages
  partition 5: 180 messages
  unique sequences found: 600
  duplicates: 0
  missing: 0
RESULT: every acknowledged sequence is present; failed sends are separate. exit 0
```

No send failed and no duplicate appeared: with `acks=all` and
`min.insync.replicas=2`, a graceful stop of one broker did not interrupt writes.
A graceful stop also makes the acks=1 loss window much less likely to appear;
testing a crash would need `docker compose kill`. The audit compared the log with this run's own manifest. An earlier
recheck that showed failed sends and retry duplicates was not reproduced in two
fresh runs and its raw output was not kept, so it is not quoted. This run does
not capture the acks=1 loss window.

The producer sustains about 33 messages per second, far below the `-rate=1000`
flag, so the default 20,000-message run takes about ten minutes. It issues one
synchronous write per message and the writer holds a partial batch for its 20ms
`BatchTimeout`, which is most of each write's latency. Use `COUNT=600`.
