-- Simulates a long-running transaction that touched `orders`: a report,
-- a slow batch job, or an idle-in-transaction app connection.
-- It only holds ACCESS SHARE on orders, the weakest lock, yet it is enough
-- to make a later ALTER TABLE queue, and everything queues behind the ALTER.
BEGIN;
SELECT count(*) FROM orders;
SELECT pg_sleep(30);
COMMIT;
