#!/bin/sh
# Stalled consumer: writes continue, the slot pins WAL on disk, and
# restart_lsn only advances after a running-transactions record plus a
# consumer confirmation past it. Assertions fail the run.
set -eu
. scripts/lib.sh

removed_in() { echo "$1" | sed -E 's/.* ([0-9]+) removed.*/\1/'; }
# gt A B: segment name A sorts after B
gt() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort | tail -1)" = "$1" ]; }

max_bytes=$(p -qAt -c "SELECT pg_size_bytes(current_setting('max_wal_size'))")

printf '\n== WAL CDC break: a stalled consumer pins WAL ==\n'
printf 'max_wal_size is 64MB. WAL beyond that, still on disk after CHECKPOINT, is\n'
printf 'held by the slot (wal_keep_size=0, archiving off, no other slots).\n'

discard
p -q -c "CHECKPOINT"

printf '\n>> 1. baseline, slot freshly consumed\n'
pf wal.sql
pf lag.sql

printf '\n>> 2. writing ~160MB while nobody consumes the slot\n'
sh scripts/write_wal.sh 160
line=$(do_checkpoint)
printf 'checkpoint: %s\n' "$line"

printf '\n>> 3. after CHECKPOINT: pg_wal stays above max_wal_size, oldest segment is the slot'\''s\n'
pf wal.sql
pf lag.sql
set -- $(wal_facts); total=$1; oldest=$2; slotseg=$3
[ "$total" -gt "$max_bytes" ] || fail "pg_wal ($total bytes) is not above max_wal_size ($max_bytes)"
[ "$oldest" = "$slotseg" ] || fail "oldest segment $oldest is not the slot's segment $slotseg"
[ "$(removed_in "$line")" = "0" ] || fail "checkpoint removed WAL while the slot was stalled"
status=$(p -qAt -c "SELECT wal_status FROM pg_replication_slots WHERE slot_name = '$SLOT'")
[ "$status" = "extended" ] || fail "expected wal_status=extended, got '$status'"
oldest_before=$oldest
printf 'PASS: pg_wal above max_wal_size, nothing removed, wal_status=extended\n'

marker=$(p -qAt -c "SELECT pg_current_wal_lsn()")
printf '\n>> 4. consume the slot (logical_decoding_work_mem=1MB so the reorder buffer spills)\n'
p -c "SET logical_decoding_work_mem = '1MB'; SELECT count(*) AS decoded_rows FROM pg_logical_slot_get_changes('$SLOT', NULL, NULL)"
pf lag.sql
p -c "SELECT slot_name, spill_txns, spill_count, pg_size_pretty(spill_bytes) AS spill_bytes FROM pg_stat_replication_slots WHERE slot_name = '$SLOT'"

printf '\n>> 5. restart_lsn only moves once decoding passes a new running-xacts record\n'
printf '   marker (insert position before consuming) = %s\n' "$marker"
moved=$(p -qAt -c "SELECT restart_lsn >= '$marker'::pg_lsn FROM pg_replication_slots WHERE slot_name = '$SLOT'")
tries=0
while [ "$moved" != "t" ] && [ "$tries" -lt 5 ]; do
  tries=$((tries + 1))
  p -qAt -c "SELECT pg_log_standby_snapshot()" >/dev/null
  discard
  moved=$(p -qAt -c "SELECT restart_lsn >= '$marker'::pg_lsn FROM pg_replication_slots WHERE slot_name = '$SLOT'")
  printf '   pg_log_standby_snapshot() + consume, round %s: restart_lsn >= marker? %s\n' "$tries" "$moved"
done
[ "$moved" = "t" ] || fail "restart_lsn did not pass the marker after $tries rounds"
printf '   restart_lsn passed the marker after %s extra round(s)\n' "$tries"
pf lag.sql

printf '\n>> 6. now a checkpoint can remove or recycle the old segments\n'
line=$(do_checkpoint)
printf 'checkpoint: %s\n' "$line"
pf wal.sql
set -- $(wal_facts); oldest=$2; slotseg=$3
[ "$(removed_in "$line")" -ge 1 ] || fail "checkpoint removed nothing after restart_lsn advanced"
gt "$oldest" "$oldest_before" || fail "oldest segment did not move forward ($oldest_before -> $oldest)"
printf 'PASS: oldest segment moved forward %s -> %s\n' "$oldest_before" "$oldest"

printf '\n>> 7. segments left behind the slot are held by server memory, not by the slot\n'
if [ "$oldest" = "$slotseg" ]; then
  printf 'No segment older than the slot'\''s (%s) remained in this run; nothing to show.\n' "$slotseg"
else
  printf 'oldest segment %s is older than the slot'\''s %s. A second plain checkpoint:\n' "$oldest" "$slotseg"
  line=$(do_checkpoint)
  printf 'checkpoint: %s\n' "$line"
  set -- $(wal_facts); oldest=$2; slotseg=$3
  printf 'oldest segment after the second checkpoint: %s (slot: %s)\n' "$oldest" "$slotseg"
fi
if [ "$oldest" != "$slotseg" ]; then
  printf 'Still older than the slot'\''s. Restarting the server and checkpointing again:\n'
  $COMPOSE restart postgres >/dev/null 2>&1
  $COMPOSE up -d --wait postgres >/dev/null 2>&1
  line=$(do_checkpoint)
  printf 'checkpoint: %s\n' "$line"
  pf wal.sql
  set -- $(wal_facts); oldest=$2; slotseg=$3
  [ "$oldest" = "$slotseg" ] || fail "oldest segment $oldest still differs from slot segment $slotseg after restart"
  printf 'PASS: after restart + CHECKPOINT the oldest segment equals the slot'\''s\n'
fi
