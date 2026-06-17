# Rate Limit --- Fixed Window vs Sliding Window

## What this exposes

Fixed-window rate limiting (INCR + EXPIRE) allows a 2× burst at window boundaries. A sliding window using a sorted set (ZADD / ZREMRANGEBYSCORE / ZCARD) enforces the limit across any rolling time interval.

## Redis commands used

| Command | Purpose |
|---|---|
| `INCR key` | Increment request counter |
| `EXPIRE key ttl` | Set window expiry |
| `ZADD key score member` | Record request timestamp in sorted set |
| `ZREMRANGEBYSCORE key 0 cutoff` | Remove entries older than the window |
| `ZCARD key` | Count current requests in window |
| `EVAL script keys args` | Execute Lua atomically |

## Why Lua

Both fixed and sliding window scripts use Lua `EVAL` --- each request is fully atomic. The bug in fixed-window is not a race condition. It is a boundary semantics problem: the window counter resets on expiry, so two bursts on either side of a window boundary each see a fresh counter, allowing 2× the limit across a short real-time span.

## Modes

| Mode | Behaviour | Break |
|---|---|---|
| `fixed` | INCR + EXPIRE per window | 2× burst possible at boundary |
| `sliding` | Sorted set + Lua | Limit enforced across any rolling window |

## Run

```bash
make up
make break   # fixed-window: boundary burst allows 2× limit --- exit 1
make test    # sliding-window: limit holds --- exit 0
make cli     # redis-cli: try ZRANGE rl:sliding 0 -1 WITHSCORES
make down
```

## Invariant

Total allowed requests must not exceed the limit. Fixed-window violates this at every window boundary.

## What to try in redis-cli

```
ZRANGE rl:sliding 0 -1 WITHSCORES   # see timestamp scores
ZCARD  rl:sliding                    # current count in window
TTL    rl:fixed                      # seconds until fixed window resets
```
