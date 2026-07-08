# Lab: HMAC webhook signatures and replay protection

Webhook providers usually cannot use a browser session, OAuth redirect, or
mutual TLS for every delivery. Instead, they sign each request with a shared
secret. The receiver recomputes the HMAC over the exact bytes it received and
compares signatures before trusting the event.

This lab shows the boundary of that guarantee. HMAC proves the sender knew the
secret and the body was not changed. It does not, by itself, prove the request
is fresh. A captured valid request can be sent again unless the receiver also
checks a timestamp and remembers a nonce or event id.

## Scenario

The receiver exposes one endpoint:

```text
POST /webhook
X-Webhook-Timestamp: unix seconds
X-Webhook-Nonce: unique id for this delivery
X-Webhook-Signature: v1=<hex hmac sha256>
```

The signed string is:

```text
timestamp + "." + nonce + "." + raw_body
```

The lab has two receiver modes:

| Mode | Checks | Result |
|---|---|---|
| insecure | HMAC only | accepts a replayed request |
| secure | HMAC + timestamp tolerance + nonce cache | rejects body tampering, stale requests, and replay |

## Commands

```bash
make up       # secure receiver
make valid    # accepted signed request
make tamper   # body changed after signing, rejected
make stale    # old timestamp, rejected
make replay   # same nonce/signature twice, second rejected
make break    # insecure receiver accepts replay
make test     # secure receiver accepts valid and rejects tamper/stale/replay
make logs     # follow receiver logs
make down     # full teardown
```

## Measured results (2026-07-08)

```text
make break:
  replay-vulnerable first:  got=200 want=200 accepted event: evt-replay
  replay-vulnerable second: got=200 want=200 accepted event: evt-replay

make test:
  valid:          got=200 want=200 accepted event: evt-valid
  tamper:         got=401 want=401 bad signature
  stale:          got=401 want=401 timestamp outside tolerance
  replay first:   got=200 want=200 accepted event: evt-replay
  replay second:  got=409 want=409 replay detected
```

## What to observe

- Signature verification must use the exact raw request body. Parsing and
  re-serializing JSON before verification changes the bytes being checked.
- Use a stable signed prefix such as timestamp and nonce before the body. This
  binds freshness metadata into the signature instead of trusting unsigned
  headers.
- Compare MACs with a constant-time comparison. Normal string equality can leak
  how much of the signature matched.
- Timestamp checks need a tolerance window for clock skew. Five minutes is a
  common starting point; high-risk flows can use a shorter window.
- Nonce or event-id storage must live at least as long as the timestamp
  tolerance window. For durable webhook processing, the event id is often stored
  with the idempotency record.
- Replay protection and idempotency are related but separate. Replay protection
  rejects duplicate deliveries at the security boundary; idempotency prevents a
  duplicate that gets past the boundary from applying the effect twice.

## References

- Stripe docs, webhook signature verification
- GitHub docs, validating webhook deliveries
- Slack docs, verifying request signatures
- RFC 2104, HMAC
- RFC 6238, time-based one-time password, useful background on timestamp windows
