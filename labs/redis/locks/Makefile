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
	@printf "\n== Locks break: stale holder plain DEL ==\n"
	@printf "What will happen: work takes longer than the lock TTL, then the stale holder runs DEL.\n"
	@printf "Watch for: WRONG-OWNER DEL lines where one worker deletes another worker's lock.\n"
	@printf "Result to review: wrong-owner DEL count is greater than zero, so this exits 1.\n\n"
	$(COMPOSE) run --rm driver -mode=naive -workers=6 -iterations=20 -check

test: build ## Lua safe-DEL: only holder can release its own lock (exit 0)
	@printf "\n== Locks test: Lua compare-and-delete ==\n"
	@printf "What will happen: locks still expire mid-work, but release checks the owner token.\n"
	@printf "Watch for: Lua skipped DEL when the stored token no longer matches.\n"
	@printf "Result to review: wrong-owner DEL count is zero, so this exits 0.\n\n"
	$(COMPOSE) run --rm driver -mode=safe -workers=6 -iterations=20 -check

load: build ## Sustained contention with the safe lock
	@printf "\n== Locks load: sustained safe release under contention ==\n"
	@printf "What will happen: more workers repeatedly acquire locks with Lua safe release.\n"
	@printf "Watch for: concurrent occupancy may still occur when TTL expires, but no wrong-owner DEL.\n"
	@printf "Result to review: wrong-owner DEL count and max occupancy in final summary.\n\n"
	$(COMPOSE) run --rm driver -mode=safe -workers=10 -iterations=100

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
