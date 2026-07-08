COMPOSE := docker compose

# Unprotected: no breaker, no bulkhead (shared capacity pool), no shedding
# (a full pool blocks instead of failing fast). B's slowness is free to drain
# the shared pool and take A down with it.
BREAK_ENV := BREAKER=off BULKHEAD=off SHED=off \
	BREAKER_THRESHOLD=5 BREAKER_COOLDOWN_MS=2000 BULKHEAD_SIZE=20 ATTEMPT_TIMEOUT_MS=800

# Protected: breaker trips on B, bulkhead isolates A's pool from B's, and
# shedding fast-fails instead of queueing once a pool or breaker is exhausted.
TEST_ENV := BREAKER=on BULKHEAD=on SHED=on \
	BREAKER_THRESHOLD=5 BREAKER_COOLDOWN_MS=2000 BULKHEAD_SIZE=20 ATTEMPT_TIMEOUT_MS=800

.PHONY: build up break-up test-up break breaker bulkhead test logs down reset

build: ## Build the driver image
	$(COMPOSE) build backend-a

up: test-up ## Default: bring the chain up with full protection

break-up: ## Recreate backend-a/backend-b/api with protection off
	$(BREAK_ENV) $(COMPOSE) up -d --force-recreate --wait backend-a backend-b api
	@echo "chain up (unprotected). run:  make break"

test-up: ## Recreate backend-a/backend-b/api with full protection
	$(TEST_ENV) $(COMPOSE) up -d --force-recreate --wait backend-a backend-b api
	@echo "chain up (protected). run:  make test"

break: build break-up ## Cascade: B's slowness drains the shared pool and takes A down too
	@printf "\n== Unprotected: no breaker, no bulkhead, no shedding ==\n"
	@printf "backend-b is degraded (slow + 40%% base failure past capacity). api has\n"
	@printf "ONE shared capacity pool for both downstreams and blocks instead of\n"
	@printf "shedding, so B's stuck calls starve A's slots too. Watch A's success%%\n"
	@printf "and p99 crater even though backend-a is perfectly healthy.\n\n"
	$(BREAK_ENV) LABEL="unprotected (cascade)" $(COMPOSE) run --rm --no-deps load

test: build test-up ## Isolation: breaker + bulkhead + shedding keep A healthy
	@printf "\n== Protected: breaker + bulkhead + shedding ==\n"
	@printf "Same degraded backend-b. B gets its own bounded pool, its breaker trips\n"
	@printf "after 5 consecutive failures and fast-fails for 2s cool-offs, and a full\n"
	@printf "pool sheds instead of queueing. Watch A stay fast and successful while B\n"
	@printf "fast-fails.\n\n"
	$(TEST_ENV) LABEL="protected (isolated)" $(COMPOSE) run --rm --no-deps load

breaker: build ## Isolate the breaker: on, bulkhead+shed off
	BREAKER=on BULKHEAD=off SHED=off BREAKER_THRESHOLD=5 BREAKER_COOLDOWN_MS=2000 \
		BULKHEAD_SIZE=20 ATTEMPT_TIMEOUT_MS=800 \
		$(COMPOSE) up -d --force-recreate --wait backend-a backend-b api
	BREAKER=on BULKHEAD=off SHED=off LABEL="breaker only" $(COMPOSE) run --rm --no-deps load

bulkhead: build ## Isolate the bulkhead: on, breaker+shed off
	BREAKER=off BULKHEAD=on SHED=off BREAKER_THRESHOLD=5 BREAKER_COOLDOWN_MS=2000 \
		BULKHEAD_SIZE=20 ATTEMPT_TIMEOUT_MS=800 \
		$(COMPOSE) up -d --force-recreate --wait backend-a backend-b api
	BREAKER=off BULKHEAD=on SHED=off LABEL="bulkhead only" $(COMPOSE) run --rm --no-deps load

single: ## One request through the chain (debug)
	$(COMPOSE) exec api wget -qO- http://localhost:8080/a || true

logs: ## Follow all service logs
	$(COMPOSE) logs -f

down: ## Remove containers, networks, volumes, and the built image
	$(COMPOSE) --profile tools down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
