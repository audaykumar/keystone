\x on
SELECT slot_name,
       active,
       inactive_since,
       wal_status,
       invalidation_reason,
       restart_lsn,
       confirmed_flush_lsn,
       catalog_xmin,
       pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn))         AS wal_since_restart_lsn,
       pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)) AS consumer_lag,
       pg_size_pretty(safe_wal_size)                                              AS safe_wal_size
FROM pg_replication_slots
WHERE slot_name = :'slot';
