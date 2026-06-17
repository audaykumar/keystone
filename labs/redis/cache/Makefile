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
	@printf "\n== Cache break: naive cache fill ==\n"
	@printf "What will happen: 20 workers hit an expired key at the same time.\n"
	@printf "Watch for: many GET MISS lines followed by many backend CALL lines.\n"
	@printf "Result to review: backend calls should exceed the safe threshold, so this exits 1.\n\n"
	$(COMPOSE) run --rm driver -mode=naive -workers=20 -rounds=2 -check

test: build ## Fixed cache: single-flight prevents thundering herd (exit 0)
	@printf "\n== Cache test: single-flight cache fill ==\n"
	@printf "What will happen: workers still miss together, but one worker owns the fill lock.\n"
	@printf "Watch for: one backend CALL per expiry cycle, other workers WAIT then GET HIT.\n"
	@printf "Result to review: backend calls stay near one per round, so this exits 0.\n\n"
	$(COMPOSE) run --rm driver -mode=fix -workers=20 -rounds=2 -check

load: build ## Sustained traffic with the fix
	@printf "\n== Cache load: repeated fixed cache fills ==\n"
	@printf "What will happen: five expiry cycles run with the single-flight fix.\n"
	@printf "Watch for: backend calls bounded across repeated cache expiry.\n"
	@printf "Result to review: final backend call count versus ideal rounds.\n\n"
	$(COMPOSE) run --rm driver -mode=fix -workers=20 -rounds=5

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
