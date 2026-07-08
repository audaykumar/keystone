COMPOSE := docker compose

# Naive policy: 3 blind attempts per hop, fixed backoff, no jitter, no budget.
NAIVE_ENV := RETRY_ATTEMPTS=3 BACKOFF_MS=25 JITTER=none ATTEMPT_TIMEOUT=600 \
	EDGE_DEADLINE_MS=0 API_DEADLINE_MS=0 RETRY_BUDGET=0

# Fixed policy: same 3 attempts, but full jitter, an 800ms end-to-end budget
# propagated down the chain, and a retry budget capping retries at 20% of calls.
# attempt_timeout must exceed the dependency's real (slow) response time, or
# legitimate slow successes get killed. The budget, not the per-attempt
# timeout, is what caps the storm.
FIXED_ENV := RETRY_ATTEMPTS=3 BACKOFF_MS=25 JITTER=full ATTEMPT_TIMEOUT=550 \
	EDGE_DEADLINE_MS=1100 API_DEADLINE_MS=0 RETRY_BUDGET=0.2

.PHONY: build up naive-up fixed-up break test single logs down reset

build: ## Build the driver image
	$(COMPOSE) build backend

up: naive-up ## Default: bring the chain up with the naive policy

naive-up: ## Recreate edge/api/backend with the naive retry policy
	$(NAIVE_ENV) $(COMPOSE) up -d --force-recreate --wait backend api edge
	@echo "chain up (naive policy). run:  make break"

fixed-up: ## Recreate edge/api/backend with the fixed retry policy
	$(FIXED_ENV) $(COMPOSE) up -d --force-recreate --wait backend api edge
	@echo "chain up (fixed policy). run:  make test"

break: build naive-up ## Storm: naive retries against a degraded backend
	@printf "\n== Retry storm: blind retries at every hop ==\n"
	@printf "3 attempts at edge x 3 at api = up to 9 backend calls per request.\n"
	@printf "Backend is degraded (slow + 80%% failing). Watch AMPLIFICATION climb\n"
	@printf "and success collapse: the retries are attacking the sick dependency.\n\n"
	$(NAIVE_ENV) LABEL="naive (storm)" $(COMPOSE) run --rm --no-deps load

test: build fixed-up ## Fixed: jitter + timeout budget + retry budget
	@printf "\n== Retry fix: jitter, deadline propagation, retry budget ==\n"
	@printf "Same 3 attempts, but full jitter decorrelates spikes, an 800ms\n"
	@printf "end-to-end budget stops doomed retries, and a 20%% retry budget caps\n"
	@printf "total retries. Watch AMPLIFICATION fall toward 1x and success recover.\n\n"
	$(FIXED_ENV) LABEL="fixed (budgeted)" $(COMPOSE) run --rm --no-deps load

single: ## One request through the chain (debug)
	$(COMPOSE) exec edge wget -qO- http://localhost:8080/work || true

logs: ## Follow all service logs
	$(COMPOSE) logs -f

down: ## Remove containers, networks, and built image
	$(COMPOSE) --profile tools down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
