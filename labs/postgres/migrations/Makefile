COMPOSE := docker compose
PG_USER ?= lab
PG_DB   ?= shop

PSQL     = $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U $(PG_USER) -d $(PG_DB)
PSQL_BG  = $(COMPOSE) exec -T postgres psql -U $(PG_USER) -d $(PG_DB)

.PHONY: up down reset migrate seed build load undo break test index-break index-safe backfill validate logs psql locks

up: ## Start postgres, run migrations and seed 300k orders
	$(COMPOSE) up -d --wait postgres
	$(MAKE) migrate
	$(MAKE) seed
	@echo ""
	@echo "ready. terminal 1: make load     (steady traffic + latency report)"
	@echo "       terminal 2: make break    (unsafe ALTER stalls all traffic)"
	@echo "       then:       make test     (lock_timeout keeps the stall bounded)"

migrate: ## Apply versioned migrations in order
	@for f in migrations/*.sql; do \
		echo ">> applying $$f"; \
		$(PSQL) -f - < "$$f" >/dev/null; \
	done

seed: ## Load deterministic seed data (300,000 orders)
	@echo ">> seeding 300k orders (a few seconds)"
	@$(PSQL) -f - < scripts/seed.sql >/dev/null

build: ## Build the Go driver image
	$(COMPOSE) build driver

load: build ## Steady mixed traffic with per-second latency percentiles
	$(COMPOSE) run --rm driver -mode=load -workers=8 -duration=60

undo: ## Drop demo artifacts so break/test can be rerun
	@$(PSQL_BG) -f - < scripts/undo.sql >/dev/null 2>&1 || true

break: undo ## Lock-queue outage: long reader + ALTER without lock_timeout
	@printf "\n== Migration break: the lock queue outage ==\n"
	@printf "A 30s reader transaction holds ACCESS SHARE on orders.\n"
	@printf "ALTER TABLE (ACCESS EXCLUSIVE) queues behind it, and every new\n"
	@printf "query queues behind the ALTER. Run 'make load' in another terminal\n"
	@printf "first and watch ops drop to zero.\n\n"
	@echo ">> starting 30s reader transaction in background"
	@($(PSQL_BG) -f - < scripts/long_reader.sql >/dev/null 2>&1 &) ; sleep 2
	@echo ">> running ALTER TABLE without lock_timeout (this will hang ~28s)"
	@time $(PSQL) -f - < scripts/unsafe_alter.sql
	@echo ">> ALTER finished. Check the load terminal for the stall window."

test: undo ## Same scenario with lock_timeout: bounded, retried, no outage
	@printf "\n== Migration test: lock_timeout + retry ==\n"
	@printf "Same 30s reader, but the ALTER gives up after 1s and retries.\n"
	@printf "The load terminal should show at most a ~1s p99 blip per attempt.\n\n"
	@echo ">> starting 30s reader transaction in background"
	@($(PSQL_BG) -f - < scripts/long_reader.sql >/dev/null 2>&1 &) ; sleep 2
	@for i in $$(seq 1 40); do \
		echo ">> attempt $$i: ALTER with lock_timeout=1s"; \
		if $(PSQL) -f - < scripts/safe_alter.sql 2>/dev/null; then \
			echo ">> succeeded on attempt $$i"; exit 0; \
		fi; \
		sleep 2; \
	done; \
	echo ">> gave up after 40 attempts"; exit 1

index-break: undo build ## Plain CREATE INDEX: writes block for the whole build
	@printf "\n== Index break: CREATE INDEX takes SHARE lock ==\n"
	@printf "Run 'make load' in another terminal. INSERTs will stall until the\n"
	@printf "index build finishes; SELECTs keep working.\n\n"
	@time $(PSQL) -f - < scripts/unsafe_index.sql

index-safe: undo build ## CREATE INDEX CONCURRENTLY: writes continue
	@printf "\n== Index safe: CREATE INDEX CONCURRENTLY ==\n"
	@printf "Slower build, but the load terminal should stay smooth.\n\n"
	@time $(PSQL) -f - < scripts/safe_index.sql

backfill: build ## Expand + batched backfill of the region column
	@printf "\n== Backfill: expand-contract, batched writes ==\n"
	@$(PSQL) -f - < scripts/expand_region.sql
	$(COMPOSE) run --rm driver -mode=backfill -batch=5000 -pause=100

validate: ## Enforce NOT NULL via NOT VALID -> VALIDATE -> SET NOT NULL
	@time $(PSQL) -f - < scripts/validate_region.sql
	@echo ">> region is now NOT NULL without a long exclusive-lock scan"

locks: ## Blocking chains during a demo (who waits on whom)
	@$(PSQL_BG) -c "SELECT waiter.pid AS waiting_pid, blocker.pid AS blocking_pid, waiter.wait_event, left(waiter.query, 50) AS waiting_query, left(blocker.query, 50) AS blocking_query FROM pg_stat_activity waiter JOIN LATERAL unnest(pg_blocking_pids(waiter.pid)) AS b(pid) ON true JOIN pg_stat_activity blocker ON blocker.pid = b.pid WHERE waiter.datname = current_database();"

logs: ## Follow postgres logs
	$(COMPOSE) logs -f postgres

psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql -U $(PG_USER) -d $(PG_DB)

down: ## Remove containers, networks, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild a clean environment
