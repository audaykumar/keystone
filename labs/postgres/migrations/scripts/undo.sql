-- Reset demo artifacts so break/test targets can be rerun.
DROP INDEX IF EXISTS idx_orders_customer;
ALTER TABLE orders DROP COLUMN IF EXISTS note;
ALTER TABLE orders DROP COLUMN IF EXISTS region;
