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
does not slow down when responses do, which is the only load model that exposes
a storm (closed-loop workers self-throttle as latency climbs and hide it).

The offered rate is set *below* the degraded backend's capacity, so the load
itself is survivable. Only amplification can push the backend over the edge.

| Hop | Retries (naive) | Retries (fixed) |
|---|---|---|
| edge | 3 attempts, fixed backoff | 3 attempts, full jitter |
| api | 3 attempts, fixed backoff | 3 attempts, full jitter |
| both | no budget, no cap | 800-1100ms end-to-end budget + 20% retry budget |

Worst-case amplification is 3 x 3 = 9 backend calls per user request.

## The metastable loop

1. The degraded backend has a base error rate: callers start retrying.
2. Blind retries raise the arrival rate at the backend.
3. Higher rate pushes the backend past capacity: latency and errors climb.
4. More errors cause more retries. Go to 2.

The system had enough capacity for the real traffic. The retries, not the
traffic, are what sustain the outage. That is a metastable failure: it persists
even after the original trigger is gone, until load is actively shed.

## Commands

```bash
make up      # bring the chain up (naive policy)
make break   # storm: blind retries against a degraded backend
make test    # fix: jitter + timeout budget + retry budget
make logs    # follow all service logs
make down    # full teardown (containers, network, image)
```

`make break` and `make test` each recreate the two proxies with the right
policy, run the open-loop load, and print the amplification factor.

## Measured results (2026-07-08)

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
collapse the chain, budgeted retries keep the backend under capacity so it stays
healthy.

## What each part of the fix does

- **Full jitter** on backoff decorrelates retries. Without it, everyone who
  failed at the same instant retries at the same instant: a synchronized second
  wave. `sleep = random(0, base * 2^attempt)`.
- **Timeout budget (deadline propagation).** The edge sets an end-to-end
  deadline and passes the shrinking remainder down via a header. A hop will not
  start an attempt it cannot finish in time, so doomed retries are never sent.
  This is also why fixed p99 sits at ~1.1s: the budget caps it.
- **Retry budget.** A token bucket caps retries at a fraction (20%) of requests.
  When failures spike, retries are allowed to rise only so far, which breaks the
  feedback loop at its source. This is the Google SRE "retry budget".

Any one of these helps; together they convert collapse into degraded-but-serving.

## What to observe

- In `make break`, `AMPLIFICATION` climbs toward 9x and p50 pins near the
  attempt ceiling: the backend is slow because it is being hammered by the very
  retries meant to route around its slowness.
- In `make test`, amplification stays near 1x-1.6x and p50 drops back to single
  digits: the backend was never pushed over capacity, so most first attempts
  simply succeed.
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
