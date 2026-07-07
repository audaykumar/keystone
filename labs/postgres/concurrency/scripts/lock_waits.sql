-- Blocking chains: which backend waits on which, and what each is running.
-- pg_blocking_pids() resolves the actual blocker even through lock queues,
-- which is more direct than joining pg_locks against itself.
SELECT
  waiter.pid  AS waiting_pid,
  blocker.pid AS blocking_pid,
  waiter.wait_event_type,
  waiter.wait_event,
  now() - waiter.xact_start AS waiting_xact_age,
  left(waiter.query, 50)  AS waiting_query,
  left(blocker.query, 50) AS blocking_query
FROM pg_stat_activity waiter
JOIN LATERAL unnest(pg_blocking_pids(waiter.pid)) AS b(pid) ON true
JOIN pg_stat_activity blocker ON blocker.pid = b.pid
WHERE waiter.datname = current_database()
ORDER BY waiter.pid;
