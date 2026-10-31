# Streams --- Consumer Crash and PEL Recovery

## What this exposes

Redis Streams deliver messages to consumer groups with at-least-once semantics. XACK normally removes successfully processed entries from the PEL. In Redis 7+, XAUTOCLAIM also clears pending references whose payloads were deleted or trimmed. XCLAIM or reading a consumer's own pending history are other recovery paths. Retention, persistence, recovery and ack ordering determine message safety.

## Redis commands used

| Command | Purpose |
|---|---|
| `XADD stream * field val` | Append message to stream |
| `XGROUP CREATE stream group id MKSTREAM` | Create consumer group |
| `XREADGROUP GROUP g consumer STREAMS stream >` | Read new messages |
| `XACK stream group id` | Acknowledge processed message (remove from PEL) |
| `XPENDING stream group` | Inspect unacknowledged messages |
| `XAUTOCLAIM stream group consumer min-idle start` | Reclaim stuck messages |

## Modes

| Mode | Behaviour |
|---|---|
| `break` | Consumer crashes mid-batch --- messages stuck in PEL |
| `fix` | Recovery consumer uses XAUTOCLAIM to reclaim and process |

## Run

```bash
make up
make break   # consumer crashes --- PEL grows, exit 1
make test    # XAUTOCLAIM recovers stuck messages, exit 0
make cli     # redis-cli: inspect PEL live
make down
```

## Invariant

For this finite retained dataset, recovery must acknowledge all produced entries and leave the PEL empty. This is not an exactly-once business-effect guarantee. Break leaves pending work until recovery or cleanup; fix reclaims it.

## What to try in redis-cli

```
XLEN orders                              # total messages in stream
XPENDING orders processors - + 10       # messages in PEL with details
XRANGE orders - +                        # all messages (raw)
XINFO GROUPS orders                      # group state and lag
```
