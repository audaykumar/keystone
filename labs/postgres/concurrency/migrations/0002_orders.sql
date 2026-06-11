CREATE TABLE IF NOT EXISTS orders (
  id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id      text        NOT NULL REFERENCES products(id),
  quantity        bigint      NOT NULL CHECK (quantity > 0),
  idempotency_key text        UNIQUE,
  created_at      timestamptz NOT NULL DEFAULT now()
);
