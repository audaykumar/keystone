-- Base schema for the zero-downtime migrations lab.
CREATE TABLE IF NOT EXISTS orders (
    id            bigserial PRIMARY KEY,
    customer_ref  text        NOT NULL,
    amount_cents  bigint      NOT NULL,
    status        text        NOT NULL DEFAULT 'created',
    created_at    timestamptz NOT NULL DEFAULT now()
);
