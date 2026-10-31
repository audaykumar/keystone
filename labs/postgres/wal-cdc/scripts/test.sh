#!/bin/sh
# Only committed changes leave the slot. Assertions fail the run.
set -eu
. scripts/lib.sh

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/wal-cdc.XXXXXX")
log="$work_dir/output"
tx_pid=""
cleanup() {
  exec 3>&-
  terminate_open_tx || true
  if [ -n "$tx_pid" ]; then
    kill "$tx_pid" 2>/dev/null || true
    wait "$tx_pid" 2>/dev/null || true
  fi
  rm -f "$work_dir/input" "$log"
  rmdir "$work_dir"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

printf '\n== WAL CDC test: only committed changes leave the slot ==\n'
printf 'A transaction inserts an order, waits at a commit barrier, then commits.\n'
printf 'The slot is peeked only after the open transaction has provably written.\n\n'

discard

mkfifo "$work_dir/input"
( p -f - < "$work_dir/input" > "$log" 2>&1 ) &
tx_pid=$!
exec 3>"$work_dir/input"
cat scripts/open_transaction.sql >&3

ready=0
for _ in $(seq 1 80); do
  ready=$(p -qAt -c "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = 42 AND objid = 4242 AND granted")
  [ "$ready" = "1" ] && break
  sleep 0.1
done
[ "$ready" = "1" ] || { cat "$log"; fail "open transaction never reached the probe lock"; }

printf '>> open transaction state (backend_xid set means it has written):\n'
p -c "SELECT application_name, state, backend_xid FROM pg_stat_activity WHERE application_name = 'wal-cdc-open-tx'"

# Non-transactional message sits after the open transaction's WAL records and
# (flush=true) forces all earlier WAL to disk. Seeing it in the peek proves
# the decoder reached past the open transaction and still held its rows back.
p -qAt -c "SELECT pg_logical_emit_message(false, 'cdc-probe', 'before-commit', true)" >/dev/null

printf '\n>> before COMMIT, peeking the slot:\n'
# One peek, shown and asserted from the same result.
res=$(p -qAt -F ' ' -c "SELECT count(*) FILTER (WHERE data LIKE '%ord-open%'), count(*) FILTER (WHERE data LIKE 'message:%cdc-probe%') FROM pg_logical_slot_peek_changes('$SLOT', NULL, NULL)")
set -- $res
printf ' open_tx_rows | probe_seen\n--------------+------------\n %12s | %s\n' "$1" "$2"
[ "$res" = "0 1" ] || fail "expected '0 1' (open rows hidden, probe seen), got '$res'"

printf 'COMMIT;\nSELECT pg_advisory_unlock(42, 4242);\n' >&3
exec 3>&-
wait "$tx_pid" || { cat "$log"; fail "open transaction failed"; }
tx_pid=""

printf '\n>> after COMMIT, consuming decoded WAL (probe message first, then the transaction):\n'
# One probe message plus BEGIN, two row changes, and COMMIT.
pf assert_decoded.sql -v expected=5

printf '\n>> a committed multi-row transaction is decoded as one ordered unit:\n'
p -q -c "BEGIN; INSERT INTO orders(order_ref, status, total_cents) VALUES ('ord-2', 'created', 1100), ('ord-3', 'created', 1200); INSERT INTO order_events(order_ref, event_type, payload) VALUES ('ord-2', 'OrderCreated', '{\"source\":\"batch\"}'), ('ord-3', 'OrderCreated', '{\"source\":\"batch\"}'); COMMIT;"
pf assert_decoded.sql -v expected=6

printf '\n>> slot after the test:\n'
pf lag.sql
printf '\nPASS: uncommitted rows invisible before COMMIT, decoded after.\n'
