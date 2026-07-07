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
make break     # acks=1 producer; run 'make kill' mid-run, then audit exits 1
make test      # acks=all producer under the same kill; audit exits 0
make kill      # stop broker 2
make revive    # restart broker 2, watch ISR recover
make audit     # scan all partitions for missing/duplicate sequences
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
lost data. The audit makes it concrete: missing sequence numbers, exit 1.

With `acks=all` and `min.insync.replicas=2` (`make test`) the same kill
produces visible produce errors (retried or reported) instead of silent loss.
That is the fundamental trade: acks=all costs latency and can refuse writes
when ISR shrinks below minimum; acks=1 silently converts broker failure into
data loss.

## Measured results (2026-07-07)

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
