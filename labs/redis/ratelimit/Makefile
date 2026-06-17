COMPOSE := docker compose

.PHONY: up build break test load cli logs down reset

up: ## Start Redis and wait healthy
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready.  try:  make break   (watch fixed-window burst at boundary)"
	@echo "         then make test    (watch sliding-window enforce the limit)"

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Fixed-window: reproduce boundary burst (expected exit 1)
	@printf "\n== Rate limit break: fixed-window boundary burst ==\n"
	@printf "What will happen: one burst lands before a window boundary and another just after it.\n"
	@printf "Watch for: both phases allowed because the fixed counter resets at the boundary.\n"
	@printf "Result to review: total allowed in the short span exceeds the limit, so this exits 1.\n\n"
	$(COMPOSE) run --rm driver -mode=fixed -check

test: build ## Sliding-window: sorted-set enforces limit across boundary (exit 0)
	@printf "\n== Rate limit test: sliding-window limiter ==\n"
	@printf "What will happen: same boundary timing, but requests are counted in a rolling window.\n"
	@printf "Watch for: phase-2 requests denied because phase-1 entries are still recent.\n"
	@printf "Result to review: total allowed stays within the limit, so this exits 0.\n\n"
	$(COMPOSE) run --rm driver -mode=sliding -check

load: build ## Sustained traffic with the sliding-window fix
	@printf "\n== Rate limit load: larger sliding-window scenario ==\n"
	@printf "What will happen: sliding-window mode runs with a higher limit.\n"
	@printf "Watch for: sorted-set counts and denials around the rolling window.\n"
	@printf "Result to review: final allowed count compared with configured limit.\n\n"
	$(COMPOSE) run --rm driver -mode=sliding -limit=50 -window=10

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
