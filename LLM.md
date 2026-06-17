# Keystone Project Instructions

## What This Is

Keystone is a hands-on backend engineering, distributed-systems, security, and fintech engineering repository. Concepts are learned through a three-pass loop:

1. Read a focused primer.
2. Build and break a working system or isolated lab.
3. Teach the concept back through an HTML learning page.

The goal is staff-level depth, not broad technology sampling.

## Repository Structure

```text
projects/   Realistic systems that evolve across multiple concepts
labs/       Small, isolated experiments that expose one mechanism
infra/      Shared local infrastructure and reusable Compose configuration
docs/       GitHub Pages learning explainers
```

Keep all distributed-systems learning work in this monorepo unless a project becomes an independent product with its own lifecycle.

## Projects and Labs

Use a project when the question is:

> How does this concept affect application correctness and architecture?

Use a lab when the question is:

> How does this mechanism work internally?

Choose the project whose core invariant naturally exposes the concept. Do not force every topic into one application.

Example projects:

- `projects/ledger/`: transactions, money correctness, idempotency, outbox, sagas, and reconciliation
- `projects/chat/`: realtime delivery, ordering, presence, fan-out, and offline messages
- `projects/job-runner/`: queues, leases, retries, fencing, and durable execution

Example labs:

- `labs/postgres-storage/`: heap pages, tuples, MVCC, and vacuum
- `labs/kafka-delivery/`: duplicates, loss windows, and consumer rebalancing
- `labs/retry-storm/`: timeout budgets and retry amplification
- `labs/fencing-tokens/`: lease expiry and stale writers
- `labs/consistent-hashing/`: key movement, virtual nodes, and hot partitions

Labs should remain independently understandable. Shared infrastructure must not make one lab depend on completing another lab.

## Deployable Project Contract

Every project must be independently buildable, deployable, and testable with Docker.

Recommended structure:

```text
projects/<name>/
├── compose.yaml
├── Makefile
├── README.md
├── .env.example
├── src/
├── migrations/
├── tests/
├── scripts/
└── observability/
```

Required commands:

```bash
make up       # Build and start the complete project
make down     # Stop the project
make reset    # Delete local state and recreate the environment
make test     # Run unit and integration tests
make load     # Generate representative traffic
make break    # Trigger the documented failure scenario
make logs     # Follow the relevant service logs
```

Project requirements:

- Do not depend on another project.
- Run every project and lab through Docker Compose. Do not require host-installed databases, brokers, or application runtimes beyond Docker-compatible tooling and `make`.
- Pin container image and dependency versions.
- Include migrations and deterministic seed data where applicable.
- Add health checks for long-running services.
- Store local state in named volumes.
- Keep secrets in an ignored `.env`; commit `.env.example`.
- Document architecture, invariants, startup commands, and failure exercises.
- Prefer project-local Compose files.
- Share scripts or base configuration only after meaningful duplication appears.

### Environment Lifecycle Contract

Every build must support a clean, repeatable lifecycle:

```bash
make up       # Build and start from declared configuration
make reset    # Remove state, recreate services, run migrations, and load deterministic seed data
make down     # Stop services and remove containers, networks, volumes, and generated runtime artifacts
```

`make down` must leave no project runtime state behind:

- Remove project containers and Compose networks.
- Remove named and anonymous volumes created by the project.
- Remove generated data directories, temporary files, and local runtime artifacts.
- Do not delete committed source, migrations, scripts, fixtures, or documentation.
- Document any unavoidable host-level cache that Docker or the build system owns.

Never rely on manually edited database state. Schema and required data must be reproducible:

- Use versioned migrations for schema changes.
- Use committed scripts or fixtures for seed and scenario data.
- Make migration and seed commands safe to rerun where practical.
- Keep test data deterministic so failures can be reproduced.
- After teardown, `make up` or `make reset` must reconstruct the expected environment without manual steps.

## Technology Choices

| Area | Default | Purpose |
|---|---|---|
| Application services | Python | Fintech and domain-facing projects |
| Mechanism labs and project-local load tools | Go | Concurrency, failure experiments, and benchmarks |
| Local containers | OrbStack / Docker Compose | Reproducible deployment and failure testing |
| Learning site | GitHub Pages from `docs/` | Durable revision material |

These are defaults, not restrictions. Use another language when it materially improves the learning objective.

## Learning Sequence

| Phase | Topics |
|---|---|
| 1 | PostgreSQL internals and zero-downtime migrations |
| 2 | Kafka internals, delivery semantics, and idempotency |
| 3 | Redis and message queues |
| 4 | Consistency, consensus, time, ordering, locks, and partitioning |
| 5 | Failure engineering, distributed transactions, reconciliation, and ledger design |
| 6 | Queueing theory and durable execution |

Fintech topics are interleaved where they naturally affect a project.

## Per-Topic Deliverables

Each topic should produce:

1. A working project change or isolated lab.
2. A deliberate failure scenario.
3. An HTML learning explainer under `docs/<topic>/`.
4. One system-design exercise.

## Lab Approach

Build the happy path, then break it deliberately. Examples:

- Kill a database during sustained writes.
- Pause a lock holder past its lease.
- Slow a consumer until lag grows.
- Saturate a bounded queue.
- Partition a dependency.
- Replay a request or event.

Record predictions, commands, observed behavior, and conclusions in the lab or project README.

## HTML Learning Docs

The HTML pages are the durable teach-back and revision interface. They should capture understanding produced by reading, discussion, code, deployment, and experiments. They must not become condensed copies of official documentation.

### Structure

- Use one directory per topic: `docs/<topic>/`.
- Start with `docs/<topic>/overview.html` for the concise mental model.
- Add focused pages only when the deeper material exists.
- Link new pages from `docs/index.html` and relevant existing pages.
- Load `docs/components/nav.js` so return-to-index navigation remains visible.

### Content

Include the parts that materially improve understanding:

- A plain-language mental model.
- Why the concept matters in production.
- Examples from the most suitable project.
- Observations from code, deployments, and deliberate failures.
- Practical decision rules and trade-offs.
- Diagrams or interactive flows when they clarify behavior.
- Inline source links beside supported claims.
- Curated resources for further reading or watching.

Good sources include official documentation, engineering articles, papers, books with chapter references, conference talks, videos, and interactive visualizations. Official documentation verifies facts and version-sensitive behavior; it should not dictate the prose or page structure.

Share specific candidate videos with the user before adding them. Add only approved videos.

Do not add generic "incorrect assumptions" sections or "You know it when" checkpoints.

## Current Learning Lab

The first PostgreSQL concurrency lab uses warehouse inventory reservation as a
neutral scenario. A product has mutable available stock plus immutable stock
movements. Concurrent workers expose lost updates and compare row locking,
conditional atomic updates, and serializable isolation.

## Current State

Completed documentation:

- `docs/postgres/overview.html`: standalone PostgreSQL overview and request-flow visual
- `docs/postgres/lost-update.html`: interactive warehouse-stock lost-update explainer for the concurrency lab
- `docs/redis/overview.html`: Redis internals, data structures, use cases, persistence, eviction, and Lua scripting overview
- `docs/toolbox/overview.html`: systems-tool mental models, comparisons, workload selector, and clickable payment architecture
- `docs/index.html`: backend engineering map with tracks for protocols, storage, distributed systems, messaging, reliability, security, observability, APIs, deployment, and runtime topics

In-progress lab: `labs/postgres/concurrency/` reproduces a lost update while
reserving warehouse stock and compares four modes (naive, locked, atomic,
serializable). Schema
migrations, seed, `compose.yaml`, `Makefile`, and the Go driver are built. `make up`
is verified. All four modes have been run and measured, atomic oversubscription
stops exactly at zero stock, and complete teardown is verified. See
`labs/postgres/concurrency/README.md` for results.

The toolbox architecture distinguishes synchronous requests, Kafka streams, queue delivery, workflows, storage writes, replication/CDC, and metrics. Components and connections expose compact popovers plus detailed explanations.

Repository structure now uses:

```text
projects/   Independently deployable systems
labs/       Independently runnable mechanism experiments
infra/      Shared infrastructure only after real reuse appears
docs/       Durable HTML learning pages
```

## Next Session

The warehouse concurrency lab is implemented, measured, documented, and cleanly
torn down. Redis overview documentation is published. Continue with one of these:

1. Run locked PostgreSQL mode while inspecting `pg_locks` and `pg_stat_activity`, then add lock evidence to the README and HTML explainer.
2. Build a no-application-code Redis CLI lab with Docker Compose, `redis-cli`, and command scripts for cache, rate limiting, locks, streams, Pub/Sub, and sorted sets.
