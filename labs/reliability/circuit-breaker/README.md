# Lab: Circuit breaker, bulkhead, and load shedding

A single slow dependency should not be able to take down calls to an unrelated,
healthy one. This lab reproduces that cascade with a real HTTP chain, then
stops it with a per-dependency circuit breaker, a bounded bulkhead pool, and
fast load shedding.

## Scenario

A real request chain over HTTP: `load -> api -> {backend-a, backend-b}`.
`backend-a` and `backend-b` are the same rate-based capacity model used in
`labs/reliability/retry-storm`; `backend-a` stays healthy the whole run,
`backend-b` is pushed into a degraded, overloaded state. `api` calls both
downstreams (via separate `/a` and `/b` endpoints so each side's outcome is
measurable on its own) and applies a per-downstream policy controlled by
environment variables:

| Knob | Off | On |
|---|---|---|
| `BREAKER` | Every call goes straight to the downstream | Trips open after 5 consecutive failures, fails fast for a 2s cool-off, then lets one probe through (half-open) |
| `BULKHEAD` | Both downstreams share ONE capacity pool | Each downstream gets its own capacity pool, same total budget, split |
| `SHED` | A full pool blocks the caller until a slot frees | A full pool (or an open breaker) rejects immediately |

Load is **open-loop**: two independent ticker-driven generators, one hitting
`/a` and one hitting `/b`, firing at a fixed rate regardless of how slow
responses get. This is the same choice `retry-storm` makes and for the same
reason: closed-loop workers self-throttle as latency climbs and hide the
cascade this lab exists to show.

## The headline: an unrelated healthy dependency should not collapse

- **Unprotected** (`make break`): `BREAKER=off BULKHEAD=off SHED=off`. `api`
  has one shared capacity pool for both downstreams and blocks instead of
  shedding, so `backend-b`'s stuck calls occupy slots that `backend-a`'s
  requests need too. `backend-a` is never touched, but its callers still pay
  for `backend-b`'s outage.
- **Protected** (`make test`): `BREAKER=on BULKHEAD=on SHED=on`. `backend-b`
  gets its own bounded pool that cannot borrow from `backend-a`'s, its breaker
  trips after a handful of consecutive failures and fast-fails for a cool-off,
  and a full pool or open breaker rejects immediately instead of queueing.

## Commands

```bash
make up       # bring the chain up (full protection)
make break    # cascade: unprotected, B's slowness drains the shared pool and takes A down
make test     # isolation: breaker + bulkhead + shedding keep A healthy
make breaker  # isolate the breaker alone (bulkhead + shed off)
make bulkhead # isolate the bulkhead alone (breaker + shed off)
make logs     # follow all service logs
make down     # full teardown (containers, network, volumes, image)
```

`make break` and `make test` each recreate `backend-a`, `backend-b`, and `api`
in one `docker compose up -d --force-recreate --wait` call with the scenario's
env prefix, degrade `backend-b`, run the open-loop load with `--no-deps` and
the same env prefix (so `compose run` cannot silently recreate the chain with
default env), and print a per-backend report plus `api`'s own `/stats`.

## Measured results (2026-07-08)

Offered load: `backend-a` at 60 req/s, `backend-b` at 200 req/s (above its
degraded capacity of 90 req/s, base failure 40%), both for 20s.
`BULKHEAD_SIZE=20`, `BREAKER_THRESHOLD=5`, `BREAKER_COOLDOWN_MS=2000`,
`ATTEMPT_TIMEOUT_MS=800`. Load's own client timeout is 3s.

```text
=== unprotected (cascade) ===
A (healthy):   requests=1200  succeeded=239 (19.9%)  p50=3s       p99=3.002s
B (degraded):  requests=3999  succeeded=378 (9.5%)  p50=3s       p99=3.002s

api /stats:
policy breaker=false bulkhead=false shed=false
A requests=1200 ok=751 ok_pct=62.6 fail_calls=0 shed_breaker=0 shed_bulkhead=0 breaker_state=closed breaker_trips=0
B requests=3999 ok=1198 ok_pct=30.0 fail_calls=1285 shed_breaker=0 shed_bulkhead=0 breaker_state=closed breaker_trips=0

=== protected (isolated) ===
A (healthy):   requests=1200  succeeded=1200 (100.0%)  p50=7ms      p99=9ms
B (degraded):  requests=3999  succeeded=227 (5.7%)  p50=1ms      p99=108ms

api /stats:
policy breaker=true bulkhead=true shed=true
A requests=1200 ok=1200 ok_pct=100.0 fail_calls=0 shed_breaker=0 shed_bulkhead=0 breaker_state=closed breaker_trips=0
B requests=3999 ok=227 ok_pct=5.7 fail_calls=172 shed_breaker=3458 shed_bulkhead=142 breaker_state=open breaker_trips=7
```

**A, the perfectly healthy dependency, goes from 19.9% success and 3.0s p99
(pinned at the client's own timeout) to 100% success and 9ms p99** just by
isolating it from B's pool. `api`'s own view of A shows why: 62.6% of A's
calls actually completed downstream in the unprotected run, but by the time
they did, the caller had already given up. Nothing about `backend-a` changed
between the two runs; only whether `backend-b` could borrow its capacity did.

B's numbers tell the other half of the story. Client-observed success for B is
low in both runs (9.5% vs 5.7%), because B genuinely is overloaded
past what it can serve. What changes is *how* B fails: unprotected, B's
requests queue for a shared slot and eventually time out at 3s, burning
capacity the whole time. Protected, B's breaker trips 7 times over the run,
sits open, and fast-fails 3458 of 3999 requests (86.5%) in around a millisecond
plus another 142 rejected by its own full bulkhead pool: B fails often
but 1000x faster and while spending none of A's capacity.

Isolated single-knob runs (`make breaker`, `make bulkhead`) show each
mechanism's individual contribution. The breaker alone keeps A at 100%
success by stopping most B calls before they touch the shared pool
(`breaker_trips=5`, `shed_breaker=2959`), but A's p99 still reaches 967ms
because B can still occupy the shared pool during closed and half-open
periods. The bulkhead alone keeps A at 100%/10ms purely by capacity
isolation, even with B still queueing into its own pool and timing out
(B p99=3.002s, `breaker_state=closed` the whole run because the breaker is
off).

## What each part does

- **Circuit breaker.** A per-downstream closed -> open -> half-open state
  machine. Closed: calls pass through and failures accumulate a streak. After
  `BREAKER_THRESHOLD` consecutive failures, it trips open: every call fails
  fast, no downstream request is even attempted. After `BREAKER_COOLDOWN_MS`,
  exactly one call is let through as a half-open probe; success closes the
  breaker and clears the streak, failure reopens it and restarts the cool-off.
- **Bulkhead.** A bounded, per-downstream concurrency pool (a buffered
  channel used as a token). Without it, both downstreams draw from the same
  pool and a slow one can hold every slot. With it, the same total capacity is
  split so one dependency's queue cannot starve another's.
- **Load shedding.** When a pool is full or a breaker is open, `SHED=on`
  rejects immediately instead of blocking the caller until a slot frees.
  Shedding is what turns "the breaker/bulkhead exist" into "callers actually
  see the benefit": without it, blocked goroutines pile up and open-loop
  callers still pay the full queueing delay even though nothing was ever going
  to succeed.

## Implementation hooks

- **gRPC client interceptor.** Protects the caller before an RPC leaves the
  process: classify by `Service/Method`, acquire a per-dependency token, check
  the breaker, then call or fail fast.
- **gRPC server interceptor.** Protects the callee before the handler runs:
  classify by method, caller, tenant, workload, or priority, acquire that
  pool's token, then run the handler. If the pool is full, return
  `codes.ResourceExhausted`.
- **HTTP middleware.** Same placement as gRPC: outbound middleware for
  caller-side dependency limits, inbound middleware for callee-side workload
  limits.
- **Queue workers and background jobs.** Use separate worker pools or
  semaphores per queue, message type, tenant, or priority so a slow background
  workload cannot spend foreground request capacity.
- **Database-heavy services.** Keep independent connection or concurrency
  limits for foreground requests, background jobs, reporting queries, and
  backfills. A single global DB pool is often a hidden shared bulkhead with no
  isolation.

The useful bulkhead key is the thing that should be isolated: dependency,
endpoint, method, caller, tenant, workload, or priority. A full pool should
produce an explicit, observable rejection, not a hidden timeout.

## What to observe

- In `make break`, A and B share one pool. `backend-b`'s calls, throttled to
  an ~800ms attempt timeout but still slow and often failing, occupy slots
  long enough that `backend-a`'s fast, healthy calls cannot get in either. The
  client sees this as A's success rate collapsing and its p99 pinned at
  whatever the client's own timeout is, not as any change in `backend-a`
  itself.
- In `make test`, the same degraded `backend-b` still fails for its own
  callers, but B's breaker trips and its own bulkhead is what absorbs the
  damage. A's separate pool and closed breaker never see any of it.
- A breaker and a bulkhead solve different problems and compose: the breaker
  stops *sending* calls to a dependency that is failing; the bulkhead limits
  the *blast radius* if calls are still being sent (e.g., during the
  threshold window, or when failures are slow rather than fast). Shedding is
  what makes both of those decisions actually fail fast for the caller instead
  of just failing eventually.
- A retry budget (see `labs/reliability/retry-storm`) and a circuit breaker
  are not the same tool: a budget throttles the volume of retries smoothly, a
  breaker stops calling a dependency abruptly. They compose too.

## References

- Michael Nygard, *Release It!* — Circuit Breaker, Bulkhead, and Fail Fast as named stability patterns
- Marc Brooker, [Will circuit breakers solve my problems?](https://brooker.co.za/blog/2022/02/16/circuit-breakers.html)
- Martin Fowler, [CircuitBreaker](https://martinfowler.com/bliki/CircuitBreaker.html)
- Netflix, [Hystrix wiki: How it Works](https://github.com/Netflix/Hystrix/wiki/How-it-Works) — bulkheads and isolation strategies
- resilience4j, [CircuitBreaker documentation](https://resilience4j.readme.io/docs/circuitbreaker) — closed/open/half-open state machine reference
- Google SRE Book, [Handling Overload](https://sre.google/sre-book/handling-overload/) — load shedding and graceful degradation
- `labs/reliability/retry-storm` — the companion lab on retry amplification and timeout budgets; this lab reuses its backend capacity model and open-loop load generator
