# Pub/Sub --- Fire-and-Forget vs Streams

## What this exposes

Redis Pub/Sub delivers messages to currently-connected subscribers only. There is no buffer, no persistence, and no replay. A subscriber that disconnects misses those messages permanently. This contrasts directly with Streams, where disconnected consumers catch up on reconnect.

## Redis commands used

| Command | Purpose |
|---|---|
| `PUBLISH channel message` | Send message to all current subscribers |
| `SUBSCRIBE channel` | Receive messages on a channel |
| `XADD` / `XREADGROUP` / `XACK` | Streams equivalent (used in `-mode=streams` for contrast) |

## Modes

| Mode | Behaviour |
|---|---|
| `pubsub` | SUB-B goes offline mid-run --- missed messages are gone |
| `streams` | Same scenario via streams --- consumer catches up on reconnect |

## Run

```bash
make up
make break   # pubsub: offline subscriber misses messages --- exit 1
make test    # streams: offline consumer catches up --- exit 0
make cli     # redis-cli: PUBSUB CHANNELS, PUBSUB NUMSUB events
make down
```

## Invariant

All published messages should be received by all subscribers. Pub/Sub violates this for any subscriber that is not connected at the moment of publication.

## When to use Pub/Sub vs Streams

| | Pub/Sub | Streams |
|---|---|---|
| Persistence | No | Configurable AOF/RDB; loss window and failover behavior depend on settings |
| Replay | No | Yes (via consumer groups) |
| Offline consumers | Messages lost | Stored entries can be read later while retained |
| Fan-out | All current subscribers | Consumer groups |
| Use case | Live notifications, presence, cache invalidation | Durable event log, task queues |

## What to try in redis-cli

```
PUBSUB CHANNELS              # active channels
PUBSUB NUMSUB events         # subscriber count on 'events'
PUBLISH events "hello"       # send from cli while driver runs
```

## Reconnect recheck (2026-10-31)

Real output of `make test` (the streams mode). Make banner text, ANSI colour codes, build output, and blank lines are removed; values are unedited.

```text
docker compose run --rm driver -mode=streams -messages=30 -check
═══════════════════════════════════════════════════════════
  REDIS PUB/SUB LAB — FIRE-AND-FORGET vs STREAMS
  mode=streams     messages=30  channel=events
═══════════════════════════════════════════════════════════
[     0ms] OFFLINE consumer connection closed; processed=10
[     0ms] ONLINE new consumer connection; catching up 10 stored messages
═══════════════════════ RESULTS ════════════════════════
  messages produced  : 30
  consumer-A processed: 30  (including 10 'missed' while offline)
  remaining in PEL   : 0
══════════════════ INVARIANT CHECK ═════════════════════
  processed=30  expected=30
  ✓ PASSED  all 30 messages processed after 'reconnect'
  streams buffer messages — offline consumers catch up on reconnect
  contrast: Pub/Sub (make break) loses those same messages permanently
```

The consumer client is closed, ten entries are produced while it is absent, and a new connection catches up. This tests consumer continuity, not Redis server-crash durability.
