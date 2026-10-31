-- Tagged so the Makefile and scripts can find and terminate this session.
SET application_name = 'wal-cdc-open-tx';

BEGIN;

INSERT INTO orders(order_ref, status, total_cents)
VALUES ('ord-open', 'created', 4200);

INSERT INTO order_events(order_ref, event_type, payload)
VALUES ('ord-open', 'OrderCreated', '{"source":"open-tx"}');

-- Session-level advisory lock taken after the inserts: its presence in
-- pg_locks proves the inserts above have already executed.
SELECT pg_advisory_lock(42, 4242);

-- The test keeps stdin open and sends COMMIT only after its decoding probe.
