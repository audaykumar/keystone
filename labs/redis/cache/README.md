# Cache --- Thundering Herd

## What this exposes

When a cached key expires, multiple concurrent readers miss simultaneously and all call the backend to recompute the same value. This is the thundering herd problem.

## Redis commands used

| Command | Purpose |
|---|---|
| `GET key` | Read from cache --- returns value or nil on miss |
| `SET key val EX ttl` | Write to cache with expiry |
| `SET key val NX PX ttl` | Acquire fill lock (only succeeds if key absent) |
| `DEL key` | Release fill lock |
| `TTL key` | Inspect remaining lifetime |

## Modes

| Mode | Behaviour |
|---|---|
| `naive` | Every worker calls backend on miss --- N backend calls per expiry |
| `fix` | First worker to miss acquires a fill lock; others wait and retry |

## Run

```bash
make up
make break   # naive: 20 workers all hit backend --- exit 1
make test    # fix: single-flight, 1 backend call --- exit 0
make cli     # redis-cli: try TTL product:42:price, KEYS *
make down
```

## Invariant

Backend calls per expiry cycle must be -‰¤ 2 (allowing a small race window). Naive mode violates this by calling the backend once per worker.

## Predicted vs observed

| Mode | Expected backend calls | Observed |
|---|---|---|
| naive | 1 | ~20 |
| fix | 1 | 1-2 |

## What to try in redis-cli

```
SET product:42:price 299.99 EX 10   # manually set with 10s TTL
TTL product:42:price                 # watch it count down
GET product:42:price                 # nil after expiry
```
