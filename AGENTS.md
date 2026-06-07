# AGENTS.md — Keystone

## What this is

A hands-on distributed systems and fintech engineering project. Every topic is learned via a 3-pass loop: primer read → lab (build + break) → teach-back (HTML explainer). The goal is staff-level depth, not breadth.

## Repo structure

```
labs/       Go — one directory per DS concept lab
ledger/     Python — multi-currency payment ledger (accounts, transfers, FX, events)
infra/      OrbStack compose files (Postgres, Kafka, Redis, RabbitMQ)
docs/       GitHub Pages — HTML learning explainers, served at learn.aniketudaykumar.com
bench/      Load tests and benchmarks
```

## Stack decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| Lab language | Go | Concurrency primitives, performance benchmarks, all DS labs written in Go |
| Application language | Python | Ledger API, fintech domain services |
| Containerisation | OrbStack | Apple Silicon native, drop-in Docker replacement, same `docker compose` CLI |
| Hosting | GitHub Pages from `/docs` | Custom domain via CNAME, zero infra |

## Build project

Multi-currency payment ledger. Three core entities:
- `accounts` — multi-currency balances
- `transfers` — FX conversion, debit source + credit destination
- `events` — Kafka-driven, outbox pattern from day one

Every distributed-systems concept should map to a concrete problem in this domain.

## Learning phases

| Phase | Topics |
|-------|--------|
| 1 | Postgres internals, zero-downtime migrations |
| 2 | Kafka internals, delivery semantics + idempotency |
| 3 | Redis, message queues (SQS + RabbitMQ) |
| 4 | Consistency + consensus, time + ordering, distributed locks, partitioning |
| 5 | Failure-mode engineering, distributed txns + recon, ledger design |
| 6 | Queueing theory, durable execution |

Fintech domain (F1-F11) interleaved throughout.

## Per-topic deliverables

Each topic produces:
1. Go lab in `labs/<topic>/`
2. HTML learning explainer in `docs/<topic>/`
3. One system design problem solved

## HTML learning docs

The HTML pages are the durable teach-back and revision interface for this project. They should capture the understanding produced by reading, discussion, and hands-on experiments. They must not become condensed copies of official product documentation.

### Structure

- Use one directory per topic: `docs/<topic>/`.
- Start with `docs/<topic>/overview.html` for the concise mental model.
- Put advanced mechanics in focused deep-dive pages beside the overview.
- Add every page to `docs/index.html`.
- Load `docs/components/nav.js` so the return-to-index control remains available while scrolling.

### Content

Each page should include the parts that help explain or recall the concept:

- A plain-language mental model.
- Why it matters in production.
- Concrete examples from the multi-currency ledger where relevant.
- Observations from labs and deliberate failure scenarios.
- Practical decision rules and trade-offs.
- Diagrams or small interactive visuals when they clarify behavior.
- Inline links to source material beside the claims or concepts they support.
- A curated resources section for further reading or watching.

Good sources include official documentation, engineering articles, papers, books with chapter references, talks, videos, and interactive visualizations. Official documentation is used to verify facts and version-sensitive behavior, but it should not dictate the page structure or prose.

Do not add generic "incorrect assumptions" sections or "You know it when" checkpoints. Do not include detail in an overview merely because it exists in the source material; move advanced detail into a deep dive.

## Lab approach

Build it, break it deliberately. Every lab has a failure scenario — kill a node, pause a process, saturate a queue. The failure teaches more than the happy path.

## Running infra

```bash
cd infra/
docker compose up -d      # starts Postgres, Kafka, Redis, RabbitMQ via OrbStack
docker compose down       # stop all
```
