CREATE TABLE IF NOT EXISTS stock_movements (
  id              bigserial   PRIMARY KEY,
  order_id        uuid        NOT NULL REFERENCES orders(id),
  product_id      text        NOT NULL REFERENCES products(id),
  quantity_change bigint      NOT NULL CHECK (quantity_change < 0),
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS stock_movements_product_created_idx
  ON stock_movements (product_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS stock_movements_order_idx
  ON stock_movements (order_id);
