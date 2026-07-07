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
- Topic pages should read as learning notes, not as lab manuals. The page should teach the topic first and pass the lab-deletion test: removing the "Lab evidence" section should leave the concept explanation complete.
- Sections before "Lab evidence" must not reference the lab, its Make targets, its scenario nouns, or its file paths. The lab appears in exactly one evidence section plus the code-pointers table.
- Preferred topic page order:
  1. Topic title and one-sentence purpose.
  2. TLDR or Quick Grasp with the most important details.
  3. Mental model.
  4. Ground-up explanation.
  5. Concept deep dive.
  6. Implementation details.
  7. Lab evidence and real output.
  8. Production notes.
  9. Code pointers and further reading.
- Avoid naming pages or index entries as "lab X" when the concept is the important part. Use topic-first labels such as "Rate limiting: fixed window vs sliding window"; the lab is the supporting exercise.

### Content

Include the parts that materially improve understanding:

- A plain-language mental model.
- Why the concept matters in production.
- Examples from the most suitable project.
- Observations from code, deployments, and deliberate failures.
- Practical decision rules and trade-offs.
- Diagrams or interactive flows when they clarify behavior.
- Inline source links beside supported claims.
- Real terminal output or screenshots when a lab is used as evidence. Do not invent representative output when the command can be run.
- Curated resources for further reading or watching.

### Redis Documentation Direction

Redis pages should separate common command reference from topic explanations:

- Add a shared `docs/redis/commands.html` page for Redis commands and Lua basics used across topics.
- Topic pages should link to command anchors instead of repeating full command reference blocks.
- Topic pages should still explain commands in context, for example why `ZREMRANGEBYSCORE` matters for a sliding-window limiter.
- The current Redis topic pages to align to this model are caching, rate limiting, distributed locks, streams, and Pub/Sub.
- Use real lab output screenshots for topic proof, but keep the page centered on the concept.

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
- `docs/postgres/migrations.html`: zero-downtime migrations with measured lock-queue outage evidence
- `docs/kafka/overview.html`: Kafka internals: partitions, ISR, acks contract, consumer groups
- `docs/kafka/outbox.html`: transactional outbox + CDC with measured dual-write loss evidence
- `docs/delivery-semantics/overview.html`: delivery semantics and idempotent consumers with measured balance evidence
- `docs/redis/overview.html`: Redis internals, data structures, use cases, persistence, eviction, and Lua scripting overview
- `docs/redis/labs/`: Redis topic pages for caching, rate limiting, locks, streams, and Pub/Sub. These need another pass so they read as topic-first learning notes instead of lab-first pages.
- `docs/toolbox/overview.html`: systems-tool mental models, comparisons, workload selector, and clickable payment architecture
- `docs/index.html`: backend engineering map with tracks for protocols, storage, distributed systems, messaging, reliability, security, observability, APIs, deployment, and runtime topics

All new topic pages share `docs/assets/topic.css` (copied from the Redis lab
stylesheet) and end with a "Further Reading & Watching" references section;
every lab README carries a matching References section.

Completed labs (all verified end to end with real captured evidence, full
teardown confirmed):

- `labs/postgres/concurrency/`: lost update + four fix modes, measured; now
  includes lock inspection (`make locks`, `make waits`) with captured
  two-tier tuple/transactionid queue evidence.
- `labs/postgres/migrations/`: zero-downtime migrations. Measured: 28.0s
  lock-queue outage vs 1.03s worst-case with `lock_timeout`; batched
  backfill; NOT VALID, VALIDATE, SET NOT NULL; CREATE INDEX CONCURRENTLY.
- `labs/kafka/internals/`: 3-broker KRaft cluster, keyed produce, consumer
  groups, sequence audit. Happy path measured (incl. a 3x hot-partition skew
  from 10 keys on 6 partitions); the broker-kill acks=1 loss scenario is
  scripted but not yet captured.
- `labs/kafka/delivery/`: at-most-once/at-least-once/idempotent consumer
  against a Postgres balance with injected crashes. All three outcomes
  measured exactly (shortfall 100 / overshoot 10000 / exact).
- `labs/kafka/outbox/`: dual-write gap vs transactional outbox with Debezium
  CDC. Measured: dual write loses exactly the crashed order's event; outbox
  loses nothing.
- `labs/redis/cli/`: redis-cli drill scripts (cache, ratelimit, lock,
  streams, pubsub, zset), all verified.

Kafka labs share one Go module at `labs/kafka/` (`cmd/<lab>` binaries, one
Dockerfile with an `ARG LAB`), mirroring the Redis labs layout.

The toolbox architecture distinguishes synchronous requests, Kafka streams, queue delivery, workflows, storage writes, replication/CDC, and metrics. Components and connections expose compact popovers plus detailed explanations.

Repository structure now uses:

```text
projects/   Independently deployable systems
labs/       Independently runnable mechanism experiments
infra/      Shared infrastructure only after real reuse appears
docs/       Durable HTML learning pages
```

## Next Session

Phase 1 (Postgres) and Phase 2 (Kafka) labs are implemented, measured, and
documented. Continue with one of these:

1. Start `projects/ledger/`: the Python multi-currency ledger, thin first
   slice: accounts + single-currency transfer, Postgres, migrations, Docker
   lifecycle contract. Pair with fintech topic F1 (money data correctness).
2. Run and capture the `labs/kafka/internals` broker-kill acks=1 loss
   scenario (`make break` + `make kill` + `make audit`) and add the evidence
   to the README and `docs/kafka/overview.html`.
3. Create `docs/redis/commands.html` as the shared Redis command and Lua
   reference, then rewrite the Redis topic pages topic-first.
4. Add lock evidence from `labs/postgres/concurrency/README.md` to the
   `docs/postgres/lost-update.html` explainer.
