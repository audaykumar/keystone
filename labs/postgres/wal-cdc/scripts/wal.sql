-- On-disk WAL in pg_wal versus where the slot and the insert position sit.
-- File count alone misleads: checkpoints recycle (rename) old segments instead
-- of deleting them, so compare segment names.
\x on
WITH f AS (
  SELECT name, size FROM pg_ls_waldir() WHERE name ~ '^[0-9A-F]{24}$'
), c AS (
  SELECT pg_walfile_name(pg_current_wal_lsn()) AS cur,
         (SELECT pg_walfile_name(restart_lsn) FROM pg_replication_slots WHERE slot_name = :'slot') AS slot_seg
)
SELECT count(*)                                  AS total_files,
       pg_size_pretty(sum(size))                 AS pg_wal_size,
       current_setting('max_wal_size')           AS max_wal_size,
       count(*) FILTER (WHERE name <= cur)       AS segs_at_or_behind_insert,
       min(name)                                 AS oldest_seg,
       max(slot_seg)                             AS slot_restart_seg,
       max(cur)                                  AS current_seg
FROM f, c;
