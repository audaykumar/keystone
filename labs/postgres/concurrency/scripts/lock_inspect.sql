-- Snapshot of every lock the lab's backends hold or wait for, with query context.
-- Run while `make test` (locked mode) is active to see rows queueing on the hot product.
SELECT
  a.pid,
  a.state,
  a.wait_event_type,
  a.wait_event,
  l.locktype,
  l.mode,
  l.granted,
  CASE WHEN l.relation IS NOT NULL THEN l.relation::regclass::text END AS relation,
  l.transactionid::text AS xid,
  now() - a.xact_start AS xact_age,
  left(a.query, 60) AS query
FROM pg_locks l
JOIN pg_stat_activity a ON a.pid = l.pid
WHERE a.datname = current_database()
  AND a.pid <> pg_backend_pid()
ORDER BY l.granted, a.pid, l.locktype;
