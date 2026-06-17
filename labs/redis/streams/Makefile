COMPOSE := docker compose

.PHONY: up build break test load cli logs down reset

up: ## Start Redis and wait healthy
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready.  try:  make break   (watch PEL grow when consumer crashes)"
	@echo "         then make test    (watch XAUTOCLAIM recover stuck messages)"

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Consumer crashes before XACK — messages stuck in PEL (expected exit 1)
	-$(COMPOSE) run --rm driver -mode=break -messages=20 -check

test: build ## Recovery consumer uses XAUTOCLAIM to reclaim and reprocess (exit 0)
	$(COMPOSE) run --rm driver -mode=fix -messages=20 -check

load: build ## Sustained producer + consumer group with recovery
	$(COMPOSE) run --rm driver -mode=fix -messages=100

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
