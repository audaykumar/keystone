CREATE TABLE IF NOT EXISTS orders (
  id bigserial PRIMARY KEY,
  order_ref text NOT NULL UNIQUE,
  status text NOT NULL,
  total_cents integer NOT NULL CHECK (total_cents >= 0),
  notes text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS order_events (
  id bigserial PRIMARY KEY,
  order_ref text NOT NULL REFERENCES orders(order_ref),
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
