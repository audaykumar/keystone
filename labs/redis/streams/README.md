# Streams --- Consumer Crash and PEL Recovery

## What this exposes

Redis Streams deliver messages to consumer groups with at-least-once semantics. A message is removed from the Pending Entry List (PEL) only when the consumer calls XACK. If a consumer crashes before acknowledging, messages stay in the PEL indefinitely. XAUTOCLAIM reclaims them for a recovery consumer.

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

Every produced message must be acknowledged exactly once. Break mode leaves messages in PEL permanently. Fix mode recovers all of them.

## What to try in redis-cli

```
XLEN orders                              # total messages in stream
XPENDING orders processors - + 10       # messages in PEL with details
XRANGE orders - +                        # all messages (raw)
XINFO GROUPS orders                      # group state and lag
```
