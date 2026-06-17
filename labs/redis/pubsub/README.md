# Pub/Sub — Fire-and-Forget vs Streams

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
| `pubsub` | SUB-B goes offline mid-run — missed messages are gone |
| `streams` | Same scenario via streams — consumer catches up on reconnect |

## Run

```bash
make up
make break   # pubsub: offline subscriber misses messages — exit 1
make test    # streams: offline consumer catches up — exit 0
make cli     # redis-cli: PUBSUB CHANNELS, PUBSUB NUMSUB events
make down
```

## Invariant

All published messages should be received by all subscribers. Pub/Sub violates this for any subscriber that is not connected at the moment of publication.

## When to use Pub/Sub vs Streams

| | Pub/Sub | Streams |
|---|---|---|
| Persistence | No | Yes |
| Replay | No | Yes (via consumer groups) |
| Offline consumers | Messages lost | Messages buffered |
| Fan-out | All current subscribers | Consumer groups |
| Use case | Live notifications, presence, cache invalidation | Durable event log, task queues |

## What to try in redis-cli

```
PUBSUB CHANNELS              # active channels
PUBSUB NUMSUB events         # subscriber count on 'events'
PUBLISH events "hello"       # send from cli while driver runs
```
