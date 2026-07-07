-- Minimal ledger state for the delivery-semantics lab.
CREATE TABLE IF NOT EXISTS balances (
    account_id    text PRIMARY KEY,
    balance_cents bigint NOT NULL DEFAULT 0
);

-- Dedupe table: the primary key is the idempotency mechanism. Inserting a
-- seen event_id conflicts, which tells the consumer to skip the credit.
CREATE TABLE IF NOT EXISTS processed_events (
    event_id     text PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO balances (account_id, balance_cents)
VALUES ('acct-1', 0)
ON CONFLICT (account_id) DO NOTHING;
