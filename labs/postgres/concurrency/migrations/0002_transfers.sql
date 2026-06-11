CREATE TABLE IF NOT EXISTS transfers (
  id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  from_account    text        NOT NULL REFERENCES accounts(id),
  to_account      text        NOT NULL REFERENCES accounts(id),
  amount          bigint      NOT NULL CHECK (amount > 0),
  idempotency_key text        UNIQUE,
  created_at      timestamptz NOT NULL DEFAULT now()
);
