# Redis Labs

Five isolated scenarios. Each exposes one Redis mechanism through a Go driver, a deliberate failure, and a fix.

| Scenario | Mechanism | Break |
|---|---|---|
| `cache/` | GET/SET/TTL, key expiry | Thundering herd: N workers all miss simultaneously |
| `ratelimit/` | INCR, EXPIRE, sorted sets, Lua | Fixed-window burst at boundary; non-atomic race |
| `locks/` | SET NX, Lua compare-and-delete | Lock expires mid-work; stale holder deletes successor's lock |
| `streams/` | XADD, XREADGROUP, XACK, XAUTOCLAIM | Consumer crashes before ack; PEL grows; duplicates on recovery |
| `pubsub/` | PUBLISH, SUBSCRIBE | Offline subscriber misses messages permanently |

## Run order

Run in order: each scenario builds on Redis patterns from the previous one.

```bash
(cd cache     && make up && (make break || true) && make test && make down)
(cd ratelimit && make up && (make break || true) && make test && make down)
(cd locks     && make up && (make break || true) && make test && make down)
(cd streams   && make up && (make break || true) && make test && make down)
(cd pubsub    && make up && (make break || true) && make test && make down)
```

## Commands available in each scenario

```bash
make up      # Start Redis and wait for healthy
make build   # Build the Go driver image
make break   # Run broken mode, invariant will fail (exit 1)
make test    # Run fixed mode, invariant holds (exit 0)
make load    # Sustained traffic with the fix
make cli     # Open redis-cli against the running instance
make logs    # Follow Redis logs
make down    # Remove containers, volumes, images
make reset   # down + up
```
