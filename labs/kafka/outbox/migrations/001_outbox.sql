-- Orders plus their transactional outbox.
CREATE TABLE IF NOT EXISTS orders (
    id           bigserial PRIMARY KEY,
    order_ref    text        NOT NULL UNIQUE,
    amount_cents bigint      NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- The outbox row commits in the same transaction as the order. Debezium
-- streams inserts to Kafka by tailing the WAL; the database transaction is
-- the only atomicity boundary the pattern needs.
CREATE TABLE IF NOT EXISTS outbox (
    id             bigserial   PRIMARY KEY,
    aggregate_type text        NOT NULL,
    aggregate_id   text        NOT NULL,
    event_type     text        NOT NULL,
    payload        jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
