# Sourced by the scenario scripts. Expects PSQL_CMD, SLOT and COMPOSE in the
# environment (the Makefile sets them) and to run from the lab directory.

p() { $PSQL_CMD "$@"; }

# Run scripts/<file> through psql with :slot bound to the slot name.
pf() {
  _f="$1"; shift
  $PSQL_CMD -v slot="$SLOT" "$@" -f - < "scripts/$_f"
}

# Roll back any leftover open-transaction session from `make test`.
terminate_open_tx() {
  p -qAt -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = 'wal-cdc-open-tx'" >/dev/null
}

# Consume and drop whatever the slot has pending (setup noise such as TRUNCATE).
discard() {
  p -qAt -c "SELECT count(*) FROM pg_logical_slot_get_changes('$SLOT', NULL, NULL)" >/dev/null
}

_checkpoints() { $COMPOSE logs --no-log-prefix postgres 2>&1 | grep -c 'checkpoint complete' || true; }

# Issue CHECKPOINT and print the log line of that checkpoint (WAL files added,
# removed and recycled), not an earlier background one.
do_checkpoint() {
  _before=$(_checkpoints)
  p -q -c "CHECKPOINT"
  for _ in $(seq 1 25); do
    [ "$(_checkpoints)" -gt "$_before" ] && break
    sleep 0.2
  done
  [ "$(_checkpoints)" -gt "$_before" ] || fail "CHECKPOINT did not appear in the postgres log"
  $COMPOSE logs --no-log-prefix postgres 2>&1 | grep 'checkpoint complete' | tail -1
}

# pg_wal facts as one line: <total_bytes> <oldest_segment> <slot_restart_segment>
wal_facts() {
  p -qAt -F ' ' -c "SELECT (SELECT sum(size) FROM pg_ls_waldir() WHERE name ~ '^[0-9A-F]{24}\$'), (SELECT min(name) FROM pg_ls_waldir() WHERE name ~ '^[0-9A-F]{24}\$'), (SELECT pg_walfile_name(restart_lsn) FROM pg_replication_slots WHERE slot_name = '$SLOT')"
}

fail() { echo "FAIL: $*" >&2; exit 1; }
