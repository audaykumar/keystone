-- SAFE: bounded wait. If the ACCESS EXCLUSIVE lock is not granted within
-- lock_timeout, the ALTER aborts instead of stalling all traffic behind it.
-- The deploy tool retries later; readers and writers barely notice.
SET lock_timeout = '1s';
ALTER TABLE orders ADD COLUMN note text;
