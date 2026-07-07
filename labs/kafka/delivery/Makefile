COMPOSE := docker compose
PG_USER ?= lab
PG_DB   ?= ledger
TOPIC   ?= payment-events
COUNT   ?= 1000
PSQL     = $(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U $(PG_USER) -d $(PG_DB)
KCMD     = $(COMPOSE) exec kafka /opt/kafka/bin

.PHONY: up topic migrate seed build produce break lose test verify psql logs down reset

up: ## Start Kafka + Postgres, create topic, run migrations
	$(COMPOSE) up -d --wait kafka postgres
	$(MAKE) topic
	$(MAKE) migrate
	@echo ""
	@echo "ready. try:  make break   (at-least-once + crash = double credit)"
	@echo "        then make lose    (at-most-once + crash = missing money)"
	@echo "        then make test    (idempotent consumer survives the same crash)"

topic: ## Create the payment-events topic
	-$(KCMD)/kafka-topics.sh --bootstrap-server localhost:9092 --create \
		--topic $(TOPIC) --partitions 3 --replication-factor 1

migrate: ## Apply versioned migrations in order
	@for f in migrations/*.sql; do \
		echo ">> applying $$f"; \
		$(PSQL) -f - < "$$f" >/dev/null; \
	done

seed: ## Reset ledger state and consumer offsets for a fresh scenario
	@echo ">> resetting balances, dedupe table, topic, and group offsets"
	@$(PSQL) -c "UPDATE balances SET balance_cents = 0; TRUNCATE processed_events;" >/dev/null
	-@$(KCMD)/kafka-topics.sh --bootstrap-server localhost:9092 --delete --topic $(TOPIC) >/dev/null 2>&1
	-@$(KCMD)/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --delete --group ledger-appliers >/dev/null 2>&1
	@sleep 1
	@$(MAKE) topic >/dev/null 2>&1 || true

build: ## Build the Go driver image
	$(COMPOSE) build driver

produce: build ## Emit $(COUNT) payment events
	$(COMPOSE) run --rm driver -mode=produce -count=$(COUNT)

break: build seed ## At-least-once + crash: the balance overshoots
	@printf "\n== Delivery break: at-least-once duplicates ==\n"
	@printf "What will happen: the consumer applies each credit, then commits the offset.\n"
	@printf "A crash lands between the two, so on restart the last events replay.\n"
	@printf "Result to review: verify reports an overshoot and exits 1.\n\n"
	$(MAKE) produce
	-$(COMPOSE) run --rm driver -mode=consume -semantics=atleast -crash-at=500
	@echo ">> consumer crashed. restarting it to drain the rest"
	$(COMPOSE) run --rm driver -mode=consume -semantics=atleast
	-$(COMPOSE) run --rm driver -mode=verify -count=$(COUNT)

lose: build seed ## At-most-once + crash: the balance falls short
	@printf "\n== Delivery break: at-most-once loss ==\n"
	@printf "What will happen: the consumer commits the offset first, then applies.\n"
	@printf "A crash between the two skips events forever.\n"
	@printf "Result to review: verify reports a shortfall and exits 1.\n\n"
	$(MAKE) produce
	-$(COMPOSE) run --rm driver -mode=consume -semantics=atmost -crash-at=500
	@echo ">> consumer crashed. restarting it to drain the rest"
	$(COMPOSE) run --rm driver -mode=consume -semantics=atmost
	-$(COMPOSE) run --rm driver -mode=verify -count=$(COUNT)

test: build seed ## Idempotent consumer under the same crash: exact balance
	@printf "\n== Delivery test: idempotent consumer ==\n"
	@printf "What will happen: same at-least-once shape and same crash, but the\n"
	@printf "dedupe insert and the credit share one DB transaction.\n"
	@printf "Result to review: replayed events are skipped; verify exits 0.\n\n"
	$(MAKE) produce
	-$(COMPOSE) run --rm driver -mode=consume -semantics=idempotent -crash-at=500
	@echo ">> consumer crashed. restarting it to drain the rest"
	$(COMPOSE) run --rm driver -mode=consume -semantics=idempotent
	$(COMPOSE) run --rm driver -mode=verify -count=$(COUNT)

verify: build ## Compare the balance against COUNT events
	$(COMPOSE) run --rm driver -mode=verify -count=$(COUNT)

psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql -U $(PG_USER) -d $(PG_DB)

logs: ## Follow kafka and postgres logs
	$(COMPOSE) logs -f kafka postgres

down: ## Remove containers, networks, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
