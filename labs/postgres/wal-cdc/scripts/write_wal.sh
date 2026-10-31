#!/bin/sh
# Usage: write_wal.sh <target_mb> [max_batches]
# Insert ~10k-row batches (one transaction each) until <target_mb> of WAL has
# been written since this script started. Measured from a start position, not
# from the slot, because the slot's restart_lsn becomes NULL if it is
# invalidated mid-write.
# Rows are ~900 bytes: under the TOAST threshold, so nothing gets compressed
# and the WAL volume tracks the data written.
set -eu
. scripts/lib.sh

target_mb="$1"
max_batches="${2:-40}"
target=$((target_mb * 1024 * 1024))
start=$(p -qAt -c "SELECT pg_current_wal_lsn()")
b=0

written() {
  p -qAt -c "SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), '$start'::pg_lsn)::bigint"
}

while [ "$b" -lt "$max_batches" ]; do
  if [ "$(written)" -ge "$target" ]; then break; fi
  b=$((b + 1))
  p -q -c "INSERT INTO orders(order_ref, status, total_cents, notes) SELECT 'ord-$b-' || gs, 'created', 1000 + gs, repeat('x', 900) FROM generate_series(1, 10000) gs"
done

w=$(written)
echo "batches=$b rows=$((b * 10000)) wal_written=$((w / 1024 / 1024)) MB (target ${target_mb} MB)"
[ "$w" -ge "$target" ] || fail "wrote only $((w / 1024 / 1024)) MB in $b batches"
