COMPOSE := docker compose

SECURE_ENV := VERIFY_TIMESTAMP=true VERIFY_NONCE=true TOLERANCE_S=300
INSECURE_ENV := VERIFY_TIMESTAMP=false VERIFY_NONCE=false TOLERANCE_S=300

.PHONY: build up secure-up insecure-up valid tamper stale replay break test logs down reset

build: ## Build the webhook receiver/client image
	$(COMPOSE) build receiver

up: secure-up ## Default: secure receiver

secure-up: ## Start receiver with timestamp and nonce replay checks
	$(SECURE_ENV) $(COMPOSE) up -d --force-recreate --wait receiver
	@echo "secure receiver up. run: make test"

insecure-up: ## Start receiver with HMAC only, no timestamp/nonce checks
	$(INSECURE_ENV) $(COMPOSE) up -d --force-recreate --wait receiver
	@echo "insecure receiver up. run: make break"

valid: build secure-up ## Send one valid signed webhook
	$(SECURE_ENV) SCENARIO=valid $(COMPOSE) run --rm --no-deps client

tamper: build secure-up ## Send body changed after signing, expect rejection
	$(SECURE_ENV) SCENARIO=tamper $(COMPOSE) run --rm --no-deps client

stale: build secure-up ## Send old timestamp, expect rejection
	$(SECURE_ENV) SCENARIO=stale $(COMPOSE) run --rm --no-deps client

replay: build secure-up ## Send same signed webhook twice, expect second rejection
	$(SECURE_ENV) SCENARIO=replay $(COMPOSE) run --rm --no-deps client

break: build insecure-up ## Demonstrate HMAC-only verification accepts replay
	@printf "\n== Broken receiver: HMAC without replay protection ==\n"
	@printf "The same signed request is delivered twice. Signature is valid both times,\n"
	@printf "so the receiver applies the event twice.\n\n"
	$(INSECURE_ENV) SCENARIO=replay-vulnerable $(COMPOSE) run --rm --no-deps client

test: build secure-up ## Secure receiver rejects tamper, stale timestamp, and replay
	@printf "\n== Secure receiver: HMAC + timestamp + nonce ==\n"
	@printf "Valid request succeeds. Tampered body, stale timestamp, and replay all fail.\n\n"
	$(SECURE_ENV) SCENARIO=all $(COMPOSE) run --rm --no-deps client

logs: ## Follow service logs
	$(COMPOSE) logs -f

down: ## Remove containers, network, and built image
	$(COMPOSE) --profile tools down -v --rmi local --remove-orphans

reset: down up ## Tear down and rebuild clean
