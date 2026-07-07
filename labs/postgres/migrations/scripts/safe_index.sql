-- SAFE: CONCURRENTLY builds with two passes plus a catalog wait instead of a
-- long SHARE lock, so writes continue. Trade-offs: slower, cannot run inside
-- a transaction, and a failure leaves an INVALID index that must be dropped.
CREATE INDEX CONCURRENTLY idx_orders_customer ON orders (customer_ref);
