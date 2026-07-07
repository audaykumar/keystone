-- UNSAFE: plain CREATE INDEX takes SHARE lock on orders for the whole build.
-- Reads continue; every INSERT/UPDATE/DELETE blocks until the index is done.
CREATE INDEX idx_orders_customer ON orders (customer_ref);
