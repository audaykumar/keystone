COMPOSE := docker compose
PG_USER ?= lab
PG_DB   ?= warehouse

.PHONY: up down reset migrate seed build test break load serializable logs psql locks waits locks-watch

up: ## Start postgres, run migrations and seed
	$(COMPOSE) up -d --wait postgres
	$(MAKE) migrate
	$(MAKE) seed
	@echo ""
	@echo "ready. try:  make break   (watch available stock drift)"
	@echo "        then make test    (watch a fix hold)"

migrate: ## Apply versioned migrations in order
	@for f in migrations/*.sql; do \
		echo ">> applying $$f"; \
		$(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U $(PG_USER) -d $(PG_DB) -f - < "$$f" >/dev/null; \
	done

seed: ## Load deterministic seed data
	@echo ">> seeding"
	@$(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U $(PG_USER) -d $(PG_DB) -f - < scripts/seed.sql >/dev/null

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Naive mode: reproduce lost stock reservations (expected to violate the invariant)
	-$(COMPOSE) run --rm driver -mode=naive -workers=8 -iterations=250 -check

test: build ## Locked mode: reservations must preserve the invariant
	$(COMPOSE) run --rm driver -mode=locked -workers=8 -iterations=250 -check

load: build ## Generate representative traffic with the atomic fix
	$(COMPOSE) run --rm driver -mode=atomic -workers=16 -iterations=1000

serializable: build ## Run with serializable isolation and bounded retries
	$(COMPOSE) run --rm driver -mode=serializable -workers=8 -iterations=250 -max-retries=5 -check

logs: ## Follow postgres logs
	$(COMPOSE) logs -f postgres

locks: ## Snapshot pg_locks + pg_stat_activity (run during make test)
	@$(COMPOSE) exec -T postgres psql -U $(PG_USER) -d $(PG_DB) -f - < scripts/lock_inspect.sql

waits: ## Show blocking chains: who waits on whom
	@$(COMPOSE) exec -T postgres psql -U $(PG_USER) -d $(PG_DB) -f - < scripts/lock_waits.sql

locks-watch: ## Poll blocking chains every 500ms while a run is active
	@while true; do clear; date; \
		$(COMPOSE) exec -T postgres psql -U $(PG_USER) -d $(PG_DB) -f - < scripts/lock_waits.sql; \
		sleep 0.5; done

psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql -U $(PG_USER) -d $(PG_DB)

down: ## Remove containers, networks, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild a clean environment
