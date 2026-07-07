#!/bin/sh
# Cache-aside basics: set with TTL, hit, miss, and stampede-prone expiry.
set -e
. /scripts/common.sh

say "cache fill with TTL (cache-aside write path)"
run SET fx:USD:SGD 1.3542 EX 5
run GET fx:USD:SGD
run TTL fx:USD:SGD

say "conditional fill: only set if absent (a poor man's fill lock)"
run SET fx:USD:SGD 9.9999 NX EX 5
run GET fx:USD:SGD

say "wait for expiry, then observe the miss every worker sees at once"
sleep 6
run GET fx:USD:SGD
run SET "lock:fill:fx:USD:SGD" worker-1 NX EX 3
run SET "lock:fill:fx:USD:SGD" worker-2 NX EX 3

say "worker-1 holds the fill lock; worker-2 got nil and must wait or serve stale"
run GET lock:fill:fx:USD:SGD
run DEL lock:fill:fx:USD:SGD fx:USD:SGD
