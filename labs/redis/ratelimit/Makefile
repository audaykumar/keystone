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
	-$(COMPOSE) run --rm driver -mode=fixed -check

test: build ## Sliding-window: sorted-set enforces limit across boundary (exit 0)
	$(COMPOSE) run --rm driver -mode=sliding -check

load: build ## Sustained traffic with the sliding-window fix
	$(COMPOSE) run --rm driver -mode=sliding -requests=500

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
