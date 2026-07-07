COMPOSE := docker compose
TOPIC   ?= payments
KCMD     = $(COMPOSE) exec kafka1 /opt/kafka/bin

.PHONY: up topic describe build produce consume consume2 lag break test audit kill revive logs down reset

up: ## Start the 3-broker KRaft cluster and create the lab topic
	$(COMPOSE) up -d --wait kafka1 kafka2 kafka3
	$(MAKE) topic
	@echo ""
	@echo "ready. try:  make describe       (partitions, leaders, ISR)"
	@echo "        then make produce        (keyed, sequenced messages)"
	@echo "        then make consume        (group member; run twice for a rebalance)"
	@echo "        then make break          (acks=1 + broker kill = loss window)"

topic: ## Create the lab topic: 6 partitions, RF 3, min.insync.replicas 2
	-$(KCMD)/kafka-topics.sh --bootstrap-server localhost:9092 --create \
		--topic $(TOPIC) --partitions 6 --replication-factor 3 \
		--config min.insync.replicas=2

describe: ## Show partitions, leaders, and ISR for the lab topic
	$(KCMD)/kafka-topics.sh --bootstrap-server localhost:9092 --describe --topic $(TOPIC)

build: ## Build the Go driver image
	$(COMPOSE) build driver

produce: build ## 10k keyed messages, acks=all
	$(COMPOSE) run --rm driver -mode=produce -count=10000 -rate=1000 -acks=all

consume: build ## Join the consumer group (run in its own terminal)
	$(COMPOSE) run --rm driver -mode=consume -verbose

consume2: build ## Second group member: watch partitions move (rebalance)
	$(COMPOSE) run --rm driver -mode=consume -verbose

lag: ## Consumer group state: members, assignments, lag per partition
	$(KCMD)/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
		--describe --group lab-consumers

break: build ## acks=1 producer; kill a broker mid-run in another terminal
	@printf "\n== Kafka break: acks=1 loss window ==\n"
	@printf "What will happen: the producer treats leader-only acknowledgement as success.\n"
	@printf "While this runs, execute 'make kill' in another terminal.\n"
	@printf "Messages acked by the dead leader but not yet replicated are gone.\n"
	@printf "Result to review: 'make audit' reports missing sequences and exits 1.\n\n"
	$(COMPOSE) run --rm driver -mode=produce -count=20000 -rate=1000 -acks=one
	$(MAKE) audit

test: build ## acks=all + min.insync.replicas=2; same kill, no acked loss
	@printf "\n== Kafka test: acks=all survives the same broker kill ==\n"
	@printf "What will happen: acks require the full in-sync quorum, so a message is\n"
	@printf "either acked and replicated, or it fails and the driver reports the error.\n"
	@printf "Run 'make kill' in another terminal during this run too.\n"
	@printf "Result to review: audit finds every acked sequence. Failed sends are\n"
	@printf "visible errors, not silent loss.\n\n"
	$(COMPOSE) run --rm driver -mode=produce -count=20000 -rate=1000 -acks=all
	$(MAKE) audit

audit: build ## Scan all partitions; report unique/duplicate/missing sequences
	$(COMPOSE) run --rm driver -mode=audit -count=20000

kill: ## Stop broker 2 (partition leaders fail over)
	$(COMPOSE) stop kafka2
	@echo "kafka2 stopped. run 'make describe' and watch leadership + ISR shrink."

revive: ## Restart broker 2 (watch it rejoin the ISR)
	$(COMPOSE) start kafka2
	@echo "kafka2 restarting. run 'make describe' until ISR is 3/3 again."

logs: ## Follow broker logs
	$(COMPOSE) logs -f kafka1 kafka2 kafka3

down: ## Remove containers, networks, volumes, and built images
	$(COMPOSE) down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
