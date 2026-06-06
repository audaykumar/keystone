# CLAUDE.md — Keystone

## What this is

A hands-on distributed systems and fintech engineering project. Every topic is learned via a 3-pass loop: primer read → lab (build + break) → teach-back (wiki note + HTML explainer). The goal is staff-level depth, not breadth.

## Repo structure

```
labs/       Go — one directory per DS concept lab
ledger/     Python — multi-currency payment ledger (accounts, transfers, FX, events)
infra/      OrbStack compose files (Postgres, Kafka, Redis, RabbitMQ)
docs/       GitHub Pages — HTML visual explainers, served at learn.aniketudaykumar.com
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

Every DS concept maps to a real problem in this domain. See concept map in wiki tracker.

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
2. HTML visual explainer in `docs/concepts/<topic>.html`
3. Concept note in wiki (`learning/ds/<topic>.md`)
4. One system design problem solved

## Lab approach

Build it, break it deliberately. Every lab has a failure scenario — kill a node, pause a process, saturate a queue. The failure teaches more than the happy path.

## Running infra

```bash
cd infra/
docker compose up -d      # starts Postgres, Kafka, Redis, RabbitMQ via OrbStack
docker compose down       # stop all
```

## Wiki

Full tracker and concept reference live in the personal wiki (not in this repo):
- Tracker: `projects/keystone.md`
- Concept reference: `projects/staff-engineer-learning-plan.md`
