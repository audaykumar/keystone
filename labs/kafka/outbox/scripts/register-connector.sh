#!/usr/bin/env sh
# Register (or re-register) the Debezium outbox connector.
# Kafka Connect takes a little while to open its REST port; retry until it does.
set -eu
HOST="${CONNECT_URL:-http://localhost:8083}"
DIR="$(cd "$(dirname "$0")" && pwd)"

echo ">> waiting for Kafka Connect at $HOST"
i=0
until curl -sf "$HOST/connectors" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 60 ]; then
    echo "Kafka Connect did not become ready" >&2
    exit 1
  fi
  sleep 2
done

curl -sf -X DELETE "$HOST/connectors/outbox-connector" >/dev/null 2>&1 || true
sleep 1

echo ">> registering outbox-connector"
curl -sf -X POST -H "Content-Type: application/json" \
  --data @"$DIR/connector.json" "$HOST/connectors" >/dev/null

sleep 2
echo ">> connector status:"
curl -sf "$HOST/connectors/outbox-connector/status"
echo ""
