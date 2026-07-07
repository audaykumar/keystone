#!/bin/sh
# Streams: append, consumer groups, pending entries, claim, ack.
set -e
. /scripts/common.sh

say "append three transfer events"
run DEL transfers
run XADD transfers "*" from acct-1 to acct-2 amount 100
run XADD transfers "*" from acct-2 to acct-3 amount 250
run XADD transfers "*" from acct-1 to acct-3 amount 75
run XLEN transfers

say "consumer group reads new entries; each entry goes to one member"
run XGROUP CREATE transfers settlement 0
run XREADGROUP GROUP settlement worker-1 COUNT 2 STREAMS transfers ">"
run XREADGROUP GROUP settlement worker-2 COUNT 2 STREAMS transfers ">"

say "pending entries list: delivered but not acked (crash recovery state)"
run XPENDING transfers settlement

say "worker-1 dies; worker-2 claims its stuck entries after 0ms idle"
run XAUTOCLAIM transfers settlement worker-2 0 0

say "ack completes the lifecycle; pending count drops"
FIRST_ID=$(redis-cli XRANGE transfers - + COUNT 1 | head -1)
run XACK transfers settlement "$FIRST_ID"
run XPENDING transfers settlement

say "cleanup"
run DEL transfers
