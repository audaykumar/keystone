# Lab: Redis by hand (CLI drills)

The Go labs under `labs/redis/` prove behavior with instrumented drivers. This
lab is the muscle-memory companion: the same five mechanisms, plus sorted
sets, driven one `redis-cli` command at a time so each return value can be
read and understood. Every script prints the command before running it; the
transcript is the lesson.

## Commands

```bash
make up         # start Redis with scripts mounted read-only
make cache      # cache-aside: TTL, miss, NX fill lock
make ratelimit  # fixed-window INCR vs sliding-window sorted set
make lock       # SET NX EX, ownership tokens, safe Lua release
make streams    # XADD, XREADGROUP, XPENDING, XAUTOCLAIM, XACK
make pubsub     # PUBLISH return value, the missed-message trap
make zset       # leaderboards, ZINCRBY, score ranges, time indexes
make all        # run everything in sequence
make cli        # free-form redis-cli shell
make down       # full teardown
```

## What each drill teaches

- **cache**: the value of `SET ... NX` as a fill lock, and what every worker
  sees the instant a hot key expires.
- **ratelimit**: why the fixed-window key resets at the boundary and how the
  sorted-set cutoff (`ZREMRANGEBYSCORE`) rolls continuously instead.
- **lock**: blind `DEL` releases other people's locks; the Lua
  compare-and-delete is the smallest correct release.
- **streams**: the pending entries list is the crash-recovery story —
  delivered-but-unacked is a first-class state you can inspect and claim.
- **pubsub**: `PUBLISH` returns the number of receivers *right now*; zero
  means the message never existed as far as Redis is concerned.
- **zset**: one structure covering leaderboards, score ranges, and
  time-indexed lookups; ties break lexically by member.

Deeper treatment with instrumented evidence: the topic pages under
[docs/redis/](../../../docs/redis/labs/ratelimit.html) and the Go labs beside
this directory.

## References

- [Redis command reference](https://redis.io/docs/latest/commands/) and [Redis data types](https://redis.io/docs/latest/develop/data-types/)
- [Redis Streams tutorial](https://redis.io/docs/latest/develop/data-types/streams/) — pending entries and consumer groups, official walk-through
- [Distributed Locks with Redis](https://redis.io/docs/latest/develop/use/patterns/distributed-locks/) — the Redlock page; read together with the critique below
- Martin Kleppmann, [How to do distributed locking](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html) — fencing tokens, why lock TTLs cannot be trusted alone
- Salvatore Sanfilippo (antirez), [Is Redlock safe?](http://antirez.com/news/101) — the author's reply; read both sides
- *Redis in Action*, Josiah Carlson — free chapters on redis.io; ch. 6 covers locks and semaphores
