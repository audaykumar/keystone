COMPOSE ?= docker compose
SLOT    ?= orders_cdc

# PGUSER/PGDATABASE come from compose, so no -U/-d here.
PSQL = $(COMPOSE) exec -T postgres psql -X -v ON_ERROR_STOP=1
RUN  = COMPOSE="$(COMPOSE)" PSQL_CMD="$(PSQL)" SLOT="$(SLOT)" sh

.PHONY: up migrate reset-slot seed test break cap identity lag wal consume slots psql logs down reset

up: ## Start Postgres with logical WAL and apply schema
	$(COMPOSE) up -d --wait postgres
	$(MAKE) migrate
	@echo ""
	@echo "ready. try:  make test      (only committed changes leave the slot)"
	@echo "        then make break     (stalled slot pins WAL on disk)"
	@echo "        then make cap       (max_slot_wal_keep_size invalidates the slot)"
	@echo "        then make identity  (REPLICA IDENTITY: what UPDATE/DELETE carry)"

migrate: ## Apply versioned migrations in order
	@for f in migrations/*.sql; do \
		echo ">> applying $$f"; \
		$(PSQL) -f - < "$$f" >/dev/null; \
	done

reset-slot: ## Recreate the logical replication slot used by the lab
	@$(PSQL) -qAt -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = 'wal-cdc-open-tx'" >/dev/null
	@$(PSQL) -qAt -c "SELECT pg_terminate_backend(active_pid) FROM pg_replication_slots WHERE slot_name = '$(SLOT)' AND active_pid IS NOT NULL" >/dev/null
	@$(PSQL) -qAt -c "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '$(SLOT)'" >/dev/null
	@$(PSQL) -c "SELECT slot_name, lsn FROM pg_create_logical_replication_slot('$(SLOT)', 'test_decoding');"

seed: ## Restore baseline state: identity, WAL cap, deterministic rows
	@$(PSQL) -q -c "ALTER TABLE orders REPLICA IDENTITY DEFAULT"
	@$(PSQL) -q -c "ALTER SYSTEM RESET max_slot_wal_keep_size" -c "SELECT pg_reload_conf()" >/dev/null
	@$(PSQL) -q -c "TRUNCATE order_events, orders RESTART IDENTITY"

test: reset-slot seed ## Only committed changes leave the slot
	@$(RUN) scripts/test.sh

break: reset-slot seed ## Stalled consumer: writes continue, slot pins WAL on disk
	@$(RUN) scripts/break.sh

cap: reset-slot seed ## max_slot_wal_keep_size caps retention by invalidating the slot
	@$(RUN) scripts/cap.sh

identity: reset-slot seed ## REPLICA IDENTITY DEFAULT vs FULL for UPDATE/DELETE
	@$(RUN) scripts/identity.sh

lag: ## Show logical slot cursor, status, and consumer lag
	@$(PSQL) -v slot="$(SLOT)" -f - < scripts/lag.sql

wal: ## Show pg_wal segments versus slot and insert position
	@$(PSQL) -v slot="$(SLOT)" -f - < scripts/wal.sql

consume: ## Consume any currently pending logical changes
	@$(PSQL) -c "SELECT lsn, xid, data FROM pg_logical_slot_get_changes('$(SLOT)', NULL, NULL);"

slots: ## List replication slots
	@$(PSQL) -c "SELECT slot_name, plugin, slot_type, active, wal_status, restart_lsn, confirmed_flush_lsn FROM pg_replication_slots ORDER BY slot_name;"

psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql

logs: ## Follow postgres logs
	$(COMPOSE) logs -f postgres

down: ## Remove containers, networks, and volumes
	$(COMPOSE) down -v --remove-orphans

reset: ## Tear down and rebuild clean
	$(MAKE) down
	$(MAKE) up
