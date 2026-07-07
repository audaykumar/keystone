-- Deterministic seed: 300,000 orders. Enough rows that a plain CREATE INDEX
-- takes visible time and a backfill needs batching. setseed() keeps the
-- pseudo-random distribution identical across resets.
TRUNCATE orders RESTART IDENTITY;
SELECT setseed(0.42);
INSERT INTO orders (customer_ref, amount_cents, status, created_at)
SELECT
    'cust-' || (floor(random() * 5000))::int,
    (floor(random() * 100000))::bigint + 100,
    (ARRAY['created', 'paid', 'shipped', 'refunded'])[1 + floor(random() * 4)::int],
    now() - (random() * interval '180 days')
FROM generate_series(1, 300000);
ANALYZE orders;
