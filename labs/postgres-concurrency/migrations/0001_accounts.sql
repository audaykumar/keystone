CREATE TABLE IF NOT EXISTS accounts (
  id              text        PRIMARY KEY,
  name            text        NOT NULL,
  initial_balance bigint      NOT NULL,
  balance         bigint      NOT NULL,
  version         int         NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now()
);
