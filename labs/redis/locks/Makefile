COMPOSE := docker compose

.PHONY: up build break test load cli logs down reset

up: ## Start Redis and wait healthy
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready.  try:  make break   (watch stale holder delete successor's lock)"
	@echo "         then make test    (watch Lua compare-and-delete prevent it)"

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Naive DEL: stale lock holder deletes successor (expected exit 1)
	$(COMPOSE) run --rm driver -mode=naive -workers=6 -iterations=20 -check

test: build ## Lua safe-DEL: only holder can release its own lock (exit 0)
	$(COMPOSE) run --rm driver -mode=safe -workers=6 -iterations=20 -check

load: build ## Sustained contention with the safe lock
	$(COMPOSE) run --rm driver -mode=safe -workers=10 -iterations=100

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
