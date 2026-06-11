CREATE TABLE IF NOT EXISTS products (
  id              text        PRIMARY KEY,
  name            text        NOT NULL,
  initial_stock   bigint      NOT NULL CHECK (initial_stock >= 0),
  available_stock bigint      NOT NULL CHECK (available_stock >= 0),
  version         int         NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now()
);
