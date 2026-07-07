COMPOSE := docker compose
EXEC     = $(COMPOSE) exec redis sh

.PHONY: up all cache ratelimit lock streams pubsub zset cli logs down reset

up: ## Start Redis with the scripts mounted read-only
	$(COMPOSE) up -d --wait redis
	@echo ""
	@echo "ready. try:  make cache | ratelimit | lock | streams | pubsub | zset"
	@echo "        or:  make all   (run every script in sequence)"

all: cache ratelimit lock streams pubsub zset ## Run every script in sequence

cache: ## Cache-aside: TTLs, misses, and a fill lock
	$(EXEC) /scripts/cache.sh

ratelimit: ## Fixed-window counter vs sliding-window sorted set
	$(EXEC) /scripts/ratelimit.sh

lock: ## SET NX EX, ownership tokens, and safe Lua release
	$(EXEC) /scripts/lock.sh

streams: ## XADD, consumer groups, pending entries, claim, ack
	$(EXEC) /scripts/streams.sh

pubsub: ## Fire-and-forget fan-out and the missed-message trap
	$(EXEC) /scripts/pubsub.sh

zset: ## Leaderboards, score ranges, and time indexes
	$(EXEC) /scripts/zset.sh

cli: ## Open redis-cli
	$(COMPOSE) exec redis redis-cli

logs: ## Follow Redis logs
	$(COMPOSE) logs -f redis

down: ## Remove containers, networks, and volumes
	$(COMPOSE) down -v --remove-orphans

reset: down up ## Tear down and rebuild clean
