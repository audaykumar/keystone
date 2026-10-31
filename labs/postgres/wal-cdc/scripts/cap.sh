#!/bin/sh
# max_slot_wal_keep_size trades disk safety for the consumer: past the cap the
# slot is invalidated and the consumer must resnapshot.
set -eu
. scripts/lib.sh

err=$(mktemp "${TMPDIR:-/tmp}/wal-cdc.XXXXXX")
cleanup() {
  p -q -c "ALTER SYSTEM RESET max_slot_wal_keep_size" -c "SELECT pg_reload_conf()" >/dev/null || true
  rm -f "$err"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

printf '\n== WAL CDC cap: max_slot_wal_keep_size invalidates a stalled slot ==\n'

discard
p -q -c "ALTER SYSTEM SET max_slot_wal_keep_size = '16MB'" -c "SELECT pg_reload_conf()" >/dev/null
sleep 1
printf '>> max_slot_wal_keep_size in a fresh session: %s\n' "$(p -qAt -c 'SHOW max_slot_wal_keep_size')"

printf '\n>> 1. before writes: slot is reserved, safe_wal_size is populated\n'
pf lag.sql

printf '\n>> 2. writing ~64MB with the slot stalled\n'
sh scripts/write_wal.sh 64
p -q -c "CHECKPOINT"

printf '\n>> 3. after CHECKPOINT: the slot is invalidated\n'
pf lag.sql
status=$(p -qAt -c "SELECT wal_status FROM pg_replication_slots WHERE slot_name = '$SLOT'")
[ "$status" = "lost" ] || fail "expected wal_status=lost, got '$status'"

printf '>> 4. the consumer can no longer read the slot:\n'
if p -c "SELECT count(*) FROM pg_logical_slot_get_changes('$SLOT', NULL, NULL)" 2> "$err"; then
  fail "expected get_changes to fail on an invalidated slot"
fi
cat "$err"
printf '\nRecovery is drop the slot, recreate it, and resnapshot: the changes\n'
printf 'between the lost position and the new slot are gone from the stream.\n'
