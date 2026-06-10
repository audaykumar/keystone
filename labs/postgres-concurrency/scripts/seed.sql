-- Deterministic seed. Safe to rerun: balances reset to their initial values.
INSERT INTO accounts (id, name, initial_balance, balance) VALUES
  ('alice', 'Alice', 1000000, 1000000),
  ('bob',   'Bob',         0,       0)
ON CONFLICT (id) DO UPDATE
  SET initial_balance = EXCLUDED.initial_balance,
      balance         = EXCLUDED.balance,
      version         = 0;
