COMPOSE := docker compose
MGMT     = curl -sS -u lab:lab http://localhost:15672/api

# Break: no DLX on the main queue, poison is nack'd with requeue forever
# (capped at BREAK_CAP redeliveries so the demo terminates). PREFETCH=1 makes
# the head-of-line block real: with only one unacked slot, the broker keeps
# handing the consumer the requeued poison instead of the good messages
# queued behind it, so they never get delivered at all until the poison is
# resolved (which, in this mode, it never is).
BREAK_ENV := MODE=break COUNT=30 POISON_AT=5 PREFETCH=1 BREAK_CAP=20 PROCESS_MS=5

# Test: main queue carries x-dead-letter-exchange. Poison retries via a
# republish-to-tail with an incremented x-retry-count header up to
# MAX_ATTEMPTS, then the final nack (no requeue) dead-letters it. A wider
# prefetch here shows the ordering-loss measurement without also blocking.
TEST_ENV := MODE=test COUNT=30 POISON_AT=5 PREFETCH=5 MAX_ATTEMPTS=3 PROCESS_MS=5

.PHONY: build up down reset logs break test dlq depths

build: ## Build the driver image
	$(COMPOSE) build producer

up: ## Start RabbitMQ (broker only, waits for healthcheck)
	$(COMPOSE) up -d --wait rabbitmq
	@echo "ready. try:  make break   (no DLQ: poison redelivers forever, capped)"
	@echo "        then make test    (DLX + retry-count guard: poison dead-lettered after N attempts)"
	@echo "        then make dlq     (inspect the dead-letter queue)"

break: build up ## No DLQ + prefetch=1: poison redelivers forever (capped) and blocks everything behind it
	@printf "\n== Break: poison message, no dead-letter exchange, prefetch=1 ==\n"
	@printf "work.queue has no x-dead-letter-exchange. The consumer nacks the poison\n"
	@printf "with requeue every time, and with only one unacked slot the broker keeps\n"
	@printf "handing it straight back. Watch good_processed stall at the messages\n"
	@printf "produced before the poison; nothing behind it is ever delivered.\n\n"
	$(BREAK_ENV) $(COMPOSE) run --rm producer
	-$(BREAK_ENV) $(COMPOSE) run --rm consumer
	@sleep 6
	@$(MAKE) depths

test: build up ## DLX + retry-count guard: poison dead-lettered after N attempts
	@printf "\n== Test: dead-letter exchange + retry-count guard ==\n"
	@printf "work.queue carries x-dead-letter-exchange. The consumer retries the\n"
	@printf "poison via x-retry-count up to MAX_ATTEMPTS, then nacks without requeue\n"
	@printf "so RabbitMQ routes it to work.dlq. The main queue should drain to 0 and\n"
	@printf "work.dlq should hold exactly 1 message.\n\n"
	$(TEST_ENV) $(COMPOSE) run --rm producer
	$(TEST_ENV) $(COMPOSE) run --rm consumer
	@sleep 6
	@$(MAKE) depths

dlq: ## Inspect the dead-letter queue via the management API
	@echo ">> work.dlq:"
	@$(MGMT)/queues/%2f/work.dlq 2>/dev/null | jq '{messages, consumers}' 2>/dev/null || echo "(queue not found or broker not up)"

depths: ## Print main queue and DLQ depths from the management API
	@echo ">> queue depths:"
	@printf "work.queue messages=" && ($(MGMT)/queues/%2f/work.queue 2>/dev/null | jq '.messages' 2>/dev/null || echo "n/a")
	@printf "work.dlq   messages=" && ($(MGMT)/queues/%2f/work.dlq 2>/dev/null | jq '.messages' 2>/dev/null || echo "n/a")

logs: ## Follow the broker's logs
	$(COMPOSE) logs -f rabbitmq

down: ## Remove containers, network, volumes, and the built image
	$(COMPOSE) --profile tools down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
