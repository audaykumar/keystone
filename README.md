# keystone

Personal engineering depth project. Distributed systems, fintech domain, and systems tooling — learned by building and breaking real things.

## What this is

A monorepo for learning distributed systems and fintech engineering at depth. Every concept gets:
- A Go lab (build it, break it)
- An HTML visual explainer (hosted at [learn.aniketudaykumar.com](https://learn.aniketudaykumar.com))
- A system design problem

## Stack

- **Go** — DS labs, concurrency experiments, benchmarks
- **Python** — multi-currency payment ledger (the build project)
- **OrbStack** — local infra (Postgres, Kafka, Redis, RabbitMQ)
- **GitHub Pages** — hosts learning artifacts at `learn.aniketudaykumar.com`

## Structure

```
labs/       Go labs — one dir per DS concept
ledger/     Python multi-currency payment ledger
infra/      OrbStack compose files
docs/       GitHub Pages — HTML learning files
bench/      load tests and benchmarks
```

## Topics

Postgres internals · Kafka · Delivery semantics · Redis · Message queues · Consistency models · Consensus · Time + ordering · Distributed locks · Partitioning · Failure-mode engineering · Distributed transactions · Ledger design · Queueing theory · Durable execution
