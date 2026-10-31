# Lab: Retry amplification and timeout budgets

Retries make a single request more likely to succeed. Stacked at every hop of
a call chain, they also multiply load, and against a struggling dependency that
multiplication is a self-inflicted denial of service. This lab reproduces the
storm with a real HTTP chain, then puts it out with jitter, a timeout budget,
and a retry budget.

## Scenario

A real request chain over HTTP: `load -> edge -> api -> backend`. `edge` and
`api` are retrying proxies; `backend` is a dependency whose capacity can be
dropped to simulate lost nodes. Load is **open-loop**: a fixed arrival rate that
does not slow down when responses do, which preserves offered arrival rate as latency rises. Closed-loop tests can
also show amplification, but their new-request rate self-throttles.

The offered rate is set *below* the degraded backend's capacity, so the load
itself is survivable. Only amplification can push the backend over the edge.

| Hop | Retries (naive) | Retries (fixed) |
|---|---|---|
| edge | 3 attempts, fixed backoff | 3 attempts, full jitter |
| api | 3 attempts, fixed backoff | 3 attempts, full jitter |
| both | no budget, no cap | 1100ms end-to-end budget at the edge (inherited by api) + per-hop retry budget |

Worst-case amplification is 3 x 3 = 9 backend calls per user request.

## The metastable loop

1. The degraded backend has a base error rate: callers start retrying.
2. Blind retries raise the arrival rate at the backend.
3. Higher rate pushes the backend past capacity: latency and errors climb.
4. More errors cause more retries. Go to 2.

The offered traffic is below degraded capacity; retries can push it into
overload. `make break` keeps the degradation active throughout, so on its own it
shows amplification. `make recovery` removes the degradation after 12s while
traffic continues for 24s: if the system recovers, the storm was only the
trigger; if it stays down, the retries are sustaining it. See the
trigger-removal recheck below.

## Commands

```bash
make up      # bring the chain up (naive policy)
make break   # storm: blind retries against a degraded backend
make test    # fix: jitter + timeout budget + retry budget
make recovery # naive policy; remove degradation mid-run and watch recovery
make logs    # follow all service logs
make down    # full teardown (containers, network, image)
```

`make break` and `make test` each recreate the two proxies with the right
policy, run the open-loop load, and print the amplification factor.

## Historical results (2026-07-08, before inherited-deadline correction)

The old API parsed but did not enforce the inherited budget, so the fixed-policy
numbers below come from code that has since changed. The naive baseline is
unchanged and is re-measured in the recheck sections.

Offered load 60 req/s for 20s; degraded backend capacity 90 req/s with a 40%
base error rate; both hops 3 attempts.

```text
naive (storm):
  client requests:   1200
    succeeded:       273 (22.8%)
  backend calls:     8763
  AMPLIFICATION:     7.30x
  client latency:    p50=1.879s p99=1.884s

fixed (budgeted):
  client requests:   1200
    succeeded:       921 (76.8%)
  backend calls:     1899
  AMPLIFICATION:     1.58x
  client latency:    p50=10ms p99=1.103s
```

The fix wins on every axis at once: success 22.8% -> 76.8%, backend load 7.30x
-> 1.58x, p50 latency 1.88s -> 10ms. The storm's exact numbers vary run to run
(it is a stochastic feedback loop), but the separation is stable: blind retries
collapse the chain, the combined policy reduced amplification. These aggregates do not prove
that capacity was never exceeded at any instant.

## What each part of the fix does

- **Full jitter** on backoff decorrelates retries. Without it, everyone who
  failed at the same instant retries at the same instant: a synchronized second
  wave. `sleep = random(0, base * 2^attempt)`.
- **Timeout budget (deadline propagation).** The edge sets an end-to-end
  deadline and passes the shrinking remainder down via a header. Each hop enforces inherited time remaining, clips attempt timeouts to it,
  and propagates cancellation. It cannot predict whether a live attempt will succeed.
  The historical edge p99 was near its local budget; it did not prove
  that the API enforced inherited time remaining.
- **Retry budget.** A per-hop token bucket: each request adds 0.2 token, each
  retry spends one, with a 100-token initial burst. When failures spike, retries
  are allowed to rise only so far, which breaks the feedback loop at its source.
  This is the Google SRE "retry budget".

Any one of these helps; together they convert collapse into degraded-but-serving.

## What to observe

- In `make break`, `AMPLIFICATION` climbs toward 9x and p50 pins near the
  attempt ceiling: the backend is slow because it is being hammered by the very
  retries meant to route around its slowness.
- In `make test`, amplification stays near 1x-1.6x and p50 drops back to single
  digits: the combined policy reduced retry load in the historical run. The
  aggregate ratio alone does not prove capacity was never exceeded.
- Retries belong at one level, not every level. Retrying at edge and api
  multiplies; picking a single retry tier and making the others fail fast is
  often the biggest single win.
- Retries require idempotency. Retrying a non-idempotent write turns one storm
  into a correctness bug on top of an availability one.

## References

- Marc Brooker (AWS), [Timeouts, retries, and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/) — the definitive practitioner treatment; the jitter math in this lab is from here
- Marc Brooker, [Exponential Backoff And Jitter](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/) — the original "full jitter" experiment
- Google SRE Book, [Handling Overload](https://sre.google/sre-book/handling-overload/) and [Addressing Cascading Failures](https://sre.google/sre-book/addressing-cascading-failures/) — retry budgets and the cascading-failure anatomy
- Bronson et al., [Metastable Failures in Distributed Systems](https://sigops.org/s/conferences/hotos/2021/papers/hotos21-s11-bronson.pdf) (HotOS '21) — the formal frame for the feedback loop this lab builds
- Marc Brooker, [Will circuit breakers solve my problems?](https://brooker.co.za/blog/2022/02/16/circuit-breakers.html) — why budgets often beat breakers (the next lab)
- Kyle Kingsbury / AWS, and Google SRE ch. on load shedding — graceful degradation as the escape hatch
- Book: *Release It!*, Michael Nygard — Retry, Timeout, Circuit Breaker, and Bulkhead as named stability patterns

## Naive storm recheck (2026-10-31)

`make break`: naive policy, 60 req/s for 20s, backend degraded. Blank lines and build output are removed; values are unedited.

```text
[naive (storm)] backend degraded; offering 60 req/s for 20s
=== naive (storm) ===
client requests:   1200
  succeeded:       142 (11.8%)
  failed:          1058
backend calls:     9725
AMPLIFICATION:     8.10x  (backend calls / client requests)
client latency:    p50=1.883s p99=1.89s
```

Blind retries at both hops drove 1200 requests to 9725 backend calls (8.10x) and 11.8% success, with every latency pinned near the 1.9s attempt ceiling. The historical run (2026-07-08) measured 22.8% success at 7.30x; the storm's exact numbers vary run to run.

## Trigger-removal recheck (2026-10-31)

`make recovery`: naive policy, 60 req/s for 24s, degradation removed at 12s. Blank lines and build output are removed; values are unedited.

```text
[trigger removal (naive policy)] backend degraded; offering 60 req/s for 24s
trigger removed at 12s; offered traffic continues
=== trigger removal (naive policy) ===
client requests:   1440
  succeeded:       136 (9.4%)
  failed:          1304
backend calls:     11921
AMPLIFICATION:     8.28x  (backend calls / client requests)
pre-removal new requests:  ok=136 failed=584 (started before the trigger was removed)
post-removal new requests: ok=0 failed=720 (includes recovery transient; not a steady-state proof)
client latency:    p50=1.883s p99=1.89s
```

Requests started before removal succeeded 136 of 720 times (18.9%). After the trigger was removed, none of the 720 requests started succeeded (0 of 720) and latency stayed pinned near 1.9s: the amplified retry load (8.28x) kept the backend overloaded even though its degradation was gone. This is one run with the lab's fixed rates and capacities, so it shows the loop can sustain itself, not how often it does. An earlier recovery run, made while the naive policy was still cancelling orphaned downstream calls, recovered; that baseline was corrected and is now covered by a test.

## Corrected fixed-policy recheck (2026-10-31)

`make test`: corrected fixed policy, 60 req/s for 20s, backend continuously degraded. Blank lines and build output are removed; values are unedited.

```text
[fixed (budgeted)] backend degraded; offering 60 req/s for 20s
=== fixed (budgeted) ===
client requests:   1200
  succeeded:       932 (77.7%)
  failed:          268
backend calls:     1862
AMPLIFICATION:     1.55x  (backend calls / client requests)
client latency:    p50=10ms p99=1.104s
```

```text
[fixed (budgeted)] backend degraded; offering 60 req/s for 20s
=== fixed (budgeted) ===
client requests:   1200
  succeeded:       1035 (86.2%)
  failed:          165
backend calls:     1877
AMPLIFICATION:     1.56x  (backend calls / client requests)
client latency:    p50=20ms p99=1.103s
```

Retry buckets are per hop: each request adds 0.2 token, each retry spends one, with a 100-token initial burst. The 0.2 refill is not a global 20% amplification bound. Two runs of the same target are shown; success varies between them (77.7% and 86.2%) while amplification stays near 1.5x.
