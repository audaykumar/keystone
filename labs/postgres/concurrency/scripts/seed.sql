-- Deterministic seed. Safe to rerun: available stock resets to its initial value.
INSERT INTO products (id, name, initial_stock, available_stock) VALUES
  ('widget', 'Warehouse Widget', 10000, 10000)
ON CONFLICT (id) DO UPDATE
  SET name            = EXCLUDED.name,
      initial_stock   = EXCLUDED.initial_stock,
      available_stock = EXCLUDED.available_stock,
      version         = 0;
