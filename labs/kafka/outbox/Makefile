COMPOSE := docker compose
PG_USER ?= lab
PG_DB   ?= shop
COUNT   ?= 1000
PSQL     = $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U $(PG_USER) -d $(PG_DB)

.PHONY: up migrate register status seed build break test audit-direct audit-cdc outbox-rows psql logs down reset

up: ## Start postgres (logical WAL) + kafka + debezium connect, migrate, register
	$(COMPOSE) up -d --wait postgres kafka connect
	$(MAKE) migrate
	$(MAKE) register
	@echo ""
	@echo "ready. try:  make break   (dual-write + crash = lost event, audit exits 1)"
	@echo "        then make test    (outbox + same crash = no divergence, audit exits 0)"

migrate: ## Apply versioned migrations in order
	@for f in migrations/*.sql; do \
		echo ">> applying $$f"; \
		$(PSQL) -f - < "$$f" >/dev/null; \
	done

register: ## Register the Debezium connector against the outbox table
	sh scripts/register-connector.sh

status: ## Show connector status
	curl -sf http://localhost:$${CONNECT_PORT:-8083}/connectors/outbox-connector/status; echo ""

seed: ## Reset scenario state: orders, outbox, and both event topics
	@echo ">> truncating orders and outbox"
	@$(PSQL) -c "TRUNCATE orders, outbox RESTART IDENTITY;" >/dev/null
	-@$(COMPOSE) exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --delete --topic order-events >/dev/null 2>&1
	-@$(COMPOSE) exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --delete --topic shop.public.outbox >/dev/null 2>&1
	@sleep 1
	-@$(COMPOSE) exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --create --topic order-events --partitions 3 --replication-factor 1 >/dev/null 2>&1

build: ## Build the Go driver image
	$(COMPOSE) build driver

break: build seed ## Dual-write + crash: one committed order never gets its event
	@printf "\n== Outbox break: the dual-write gap ==\n"
	@printf "What will happen: each order commits to Postgres, then publishes to Kafka.\n"
	@printf "The driver crashes after committing order 500, before publishing it.\n"
	@printf "The restarted run skips existing orders, so the event is gone forever.\n"
	@printf "Result to review: audit finds an order with no event and exits 1.\n\n"
	-$(COMPOSE) run --rm driver -mode=dualwrite -count=$(COUNT) -crash-at=500
	@echo ">> restarting the crashed writer to finish the remaining orders"
	$(COMPOSE) run --rm driver -mode=dualwrite -count=$(COUNT)
	-$(COMPOSE) run --rm driver -mode=audit -source=direct

test: build seed ## Outbox + same crash: Debezium delivers every committed event
	@printf "\n== Outbox test: order + event commit atomically ==\n"
	@printf "What will happen: same crash point, but the event row commits inside\n"
	@printf "the same transaction as the order. Debezium tails the WAL and\n"
	@printf "publishes everything that committed.\n"
	@printf "Result to review: audit matches every order to an event, exit 0.\n\n"
	-$(COMPOSE) run --rm driver -mode=outbox -count=$(COUNT) -crash-at=500
	@echo ">> restarting the crashed writer to finish the remaining orders"
	$(COMPOSE) run --rm driver -mode=outbox -count=$(COUNT)
	@echo ">> waiting 10s for Debezium to drain the outbox to Kafka"
	@sleep 10
	$(COMPOSE) run --rm driver -mode=audit -source=cdc

audit-direct: build ## Compare orders vs the dual-write topic
	$(COMPOSE) run --rm driver -mode=audit -source=direct

audit-cdc: build ## Compare orders vs the Debezium CDC topic
	$(COMPOSE) run --rm driver -mode=audit -source=cdc

outbox-rows: ## Peek at the outbox table
	$(PSQL) -c "SELECT id, aggregate_id, event_type, created_at FROM outbox ORDER BY id DESC LIMIT 10;"

psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql -U $(PG_USER) -d $(PG_DB)

logs: ## Follow connect logs (register/streaming activity)
	$(COMPOSE) logs -f connect

down: ## Remove containers, networks, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
