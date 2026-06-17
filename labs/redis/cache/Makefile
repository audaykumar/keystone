COMPOSE := docker compose

.PHONY: up build break test load cli logs down reset

up: ## Start Redis and wait healthy
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready.  try:  make break   (watch thundering herd)"
	@echo "         then make test    (watch single-flight fix)"

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Naive cache: reproduce thundering herd (expected exit 1)
	-$(COMPOSE) run --rm driver -mode=naive -workers=20 -rounds=2 -check

test: build ## Fixed cache: single-flight prevents thundering herd (exit 0)
	$(COMPOSE) run --rm driver -mode=fix -workers=20 -rounds=2 -check

load: build ## Sustained traffic with the fix
	$(COMPOSE) run --rm driver -mode=fix -workers=20 -rounds=5

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
