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
	@printf "\n== Pub/Sub break: offline subscriber loses messages ==\n"
	@printf "What will happen: subscriber B disconnects while messages are published.\n"
	@printf "Watch for: SUB-B reconnects, but missed messages are not replayed.\n"
	@printf "Result to review: SUB-B missed count is greater than zero, so this exits 1.\n\n"
	$(COMPOSE) run --rm driver -mode=pubsub -messages=30 -check

test: build ## Streams: offline subscriber catches up on reconnect (exit 0)
	@printf "\n== Pub/Sub test: streams contrast ==\n"
	@printf "What will happen: the same offline gap is modeled with Redis Streams.\n"
	@printf "Watch for: stream messages buffered and consumed after reconnect.\n"
	@printf "Result to review: processed equals produced and pending is zero, so this exits 0.\n\n"
	$(COMPOSE) run --rm driver -mode=streams -messages=30 -check

load: build ## Sustained stream traffic with replayable delivery
	@printf "\n== Pub/Sub load: larger streams contrast ==\n"
	@printf "What will happen: more stream messages are produced for the replayable-delivery path.\n"
	@printf "Watch for: catch-up behavior after reconnect.\n"
	@printf "Result to review: processed count and remaining PEL size.\n\n"
	$(COMPOSE) run --rm driver -mode=streams -messages=100

cli: ## Open redis-cli against the running instance
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
