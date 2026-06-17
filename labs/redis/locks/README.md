# Locks --- Stale Lock Holder

## What this exposes

`SET key val NX PX ttl` acquires a lock that auto-expires. If the holder's work takes longer than the TTL, the lock expires, a second worker acquires it, and the first worker waking up can `DEL` the second worker's lock --- allowing two holders in the critical section simultaneously.

## Redis commands used

| Command | Purpose |
|---|---|
| `SET key val NX PX ttl` | Acquire lock (NX = only if not exists) |
| `GET key` | Read lock value to check ownership |
| `DEL key` | Release lock (unsafe: deletes regardless of owner) |
| `EVAL script keys args` | Atomic compare-and-delete (safe release) |

## Modes

Both modes use work duration (350ms) longer than lock TTL (200ms). The lock expires mid-work in both modes, causing concurrent occupancy in both. That is expected and intentional --- the modes differ on a different invariant.

| Mode | Behaviour | Invariant |
|---|---|---|
| `naive` | Plain DEL after work | Wrong-owner DEL occurs --- stale holder deletes successor's lock |
| `safe` | Lua compare-and-delete | Wrong-owner DEL never occurs --- Lua refuses to DEL on token mismatch |

## The bug sequence (naive mode)

```
t=0ms   Worker A: SET lock:inventory "token-A" NX PX 200   -†’ acquired
t=0ms   Worker A: enters critical section, starts work (350ms)
t=200ms lock TTL expires
t=201ms Worker B: SET lock:inventory "token-B" NX PX 200   -†’ acquired
t=201ms Worker B: enters critical section        -† concurrent (due to TTL expiry)
t=350ms Worker A: work done, DEL lock:inventory    -† deletes B's lock!
t=351ms Worker C: SET lock:inventory "token-C" NX PX 200   -†’ acquired immediately
```

Worker A deleted Worker B's lock. Worker C enters before Worker B finishes.

## What Lua CAD prevents vs what it does not

- **Prevents:** wrong-owner DEL --- a holder whose lock expired cannot silently wipe the successor's lock
- **Does not prevent:** concurrent occupancy when work outlasts the TTL --- for that, use lock extension (re-SET with new TTL before expiry) or fencing tokens

## Run

```bash
make up
make break   # naive: wrong-owner DEL detected --- exit 1
make test    # safe: Lua prevents wrong-owner DEL (occupancy still > 1, noted) --- exit 0
make cli     # redis-cli: try GET lock:inventory during a run
make down
```

## Invariant

Wrong-owner DEL count must be 0. Concurrent occupancy is informational in both modes.

## What to try in redis-cli

```
GET lock:inventory              # see current token (or nil if unlocked)
SET lock:inventory "manual" NX  # try to steal the lock
```
