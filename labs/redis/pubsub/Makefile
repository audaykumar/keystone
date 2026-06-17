COMPOSE := docker compose

.PHONY: up build break test load cli logs down reset

up: ## Start Redis and wait healthy
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready.  try:  make break   (watch offline subscriber miss messages)"
	@echo "         then make test    (contrast: streams mode catches subscriber up)"

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Pub/Sub: offline subscriber misses messages permanently (expected exit 1)
	$(COMPOSE) run --rm driver -mode=pubsub -messages=30 -check

test: build ## Streams: offline subscriber catches up on reconnect (exit 0)
	$(COMPOSE) run --rm driver -mode=streams -messages=30 -check

load: build ## Sustained stream traffic with replayable delivery
	$(COMPOSE) run --rm driver -mode=streams -messages=100

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
