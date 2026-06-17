COMPOSE := docker compose

.PHONY: up build break test load cli logs down reset

up: ## Start Redis and wait healthy
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready.  try:  make break   (watch PEL grow when consumer crashes)"
	@echo "         then make test    (watch XAUTOCLAIM recover stuck messages)"

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build ## Consumer crashes before XACK, messages stuck in PEL (expected exit 1)
	@printf "\n== Streams break: consumer crash before XACK ==\n"
	@printf "What will happen: a consumer reads messages, acknowledges half, then crashes.\n"
	@printf "Watch for: XPENDING shows unacknowledged messages stuck in the PEL.\n"
	@printf "Result to review: pending count stays above zero, so this exits 1.\n\n"
	$(COMPOSE) run --rm driver -mode=break -messages=20 -check

test: build ## Recovery consumer uses XAUTOCLAIM to reclaim and reprocess (exit 0)
	@printf "\n== Streams test: XAUTOCLAIM recovery ==\n"
	@printf "What will happen: same crash, then a recovery consumer claims idle pending messages.\n"
	@printf "Watch for: XCLAIM and recovered XACK lines.\n"
	@printf "Result to review: processed equals produced and pending is zero, so this exits 0.\n\n"
	$(COMPOSE) run --rm driver -mode=fix -messages=20 -check

load: build ## Sustained producer + consumer group with recovery
	@printf "\n== Streams load: larger recovery run ==\n"
	@printf "What will happen: more stream messages are produced and recovered after a simulated crash.\n"
	@printf "Watch for: recovered count and remaining PEL size.\n"
	@printf "Result to review: total processed and pending count in final summary.\n\n"
	$(COMPOSE) run --rm driver -mode=fix -messages=100

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
