CREATE TABLE IF NOT EXISTS postings (
  id          bigserial   PRIMARY KEY,
  transfer_id uuid        NOT NULL REFERENCES transfers(id),
  account_id  text        NOT NULL REFERENCES accounts(id),
  amount      bigint      NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS postings_account_created_idx ON postings (account_id, created_at);
CREATE INDEX IF NOT EXISTS postings_transfer_idx        ON postings (transfer_id);
