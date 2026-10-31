#!/bin/sh
# REPLICA IDENTITY decides what UPDATE and DELETE write into WAL at write time,
# so the ALTER must come before the DML it is meant to affect.
set -eu
. scripts/lib.sh

cleanup() { p -q -c "ALTER TABLE orders REPLICA IDENTITY DEFAULT" >/dev/null || true; }
trap cleanup EXIT
trap 'exit 1' INT TERM

printf '\n== WAL CDC identity: what UPDATE and DELETE carry ==\n'

p -q -c "INSERT INTO orders(order_ref, status, total_cents) VALUES ('ord-a','created',100), ('ord-b','created',200), ('ord-c','created',300), ('ord-d','created',400)"
discard

printf '\n>> DEFAULT identity (primary key only)\n'
p -q -c "UPDATE orders SET status = 'paid' WHERE order_ref = 'ord-a'" -c "DELETE FROM orders WHERE order_ref = 'ord-b'"
p -qAt -F ' | ' -c "SELECT xid, data FROM pg_logical_slot_get_changes('$SLOT', NULL, NULL) WHERE data NOT LIKE 'BEGIN%' AND data NOT LIKE 'COMMIT%'"

printf '\n>> ALTER TABLE orders REPLICA IDENTITY FULL (DDL: not in the decoded stream)\n'
p -q -c "ALTER TABLE orders REPLICA IDENTITY FULL"
p -q -c "UPDATE orders SET status = 'paid' WHERE order_ref = 'ord-c'" -c "DELETE FROM orders WHERE order_ref = 'ord-d'"
p -qAt -F ' | ' -c "SELECT xid, data FROM pg_logical_slot_get_changes('$SLOT', NULL, NULL) WHERE data NOT LIKE 'BEGIN%' AND data NOT LIKE 'COMMIT%'"
