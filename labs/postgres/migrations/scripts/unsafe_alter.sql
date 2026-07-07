-- UNSAFE: no lock_timeout. ALTER TABLE needs ACCESS EXCLUSIVE. It waits for
-- the long reader, and while it waits every new query on orders queues
-- behind it. The ALTER itself is instant; the queue is the outage.
ALTER TABLE orders ADD COLUMN note text;
