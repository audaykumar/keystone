-- Expand step: add the column nullable. Adding a nullable column (or one with
-- a constant default, PG11+) only updates the catalog. No table rewrite.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS region text;
