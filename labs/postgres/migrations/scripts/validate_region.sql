-- Contract step: enforce NOT NULL without a long ACCESS EXCLUSIVE scan.
--
-- 1. NOT VALID: instant, applies only to new writes.
-- 2. VALIDATE: full scan under SHARE UPDATE EXCLUSIVE — reads and writes continue.
-- 3. SET NOT NULL: PostgreSQL 12+ proves it from the valid CHECK constraint,
--    so this is a catalog-only change, no scan.
-- 4. Drop the now-redundant CHECK constraint.
SET lock_timeout = '2s';
ALTER TABLE orders ADD CONSTRAINT orders_region_not_null CHECK (region IS NOT NULL) NOT VALID;
ALTER TABLE orders VALIDATE CONSTRAINT orders_region_not_null;
ALTER TABLE orders ALTER COLUMN region SET NOT NULL;
ALTER TABLE orders DROP CONSTRAINT orders_region_not_null;
