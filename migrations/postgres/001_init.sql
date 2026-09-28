-- 001_init.sql — reservation workload schema (spec §4.1, verbatim contract).
-- PostgreSQL. Idempotent replays keyed on idempotency_key; inventory
-- decremented exactly once per unique key.

CREATE TABLE IF NOT EXISTS inventory (
  sku TEXT PRIMARY KEY,
  initial_stock BIGINT NOT NULL CHECK (initial_stock >= 0),
  available BIGINT NOT NULL CHECK (available >= 0)
);

CREATE TABLE IF NOT EXISTS reservations (
  id UUID PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  sku TEXT NOT NULL REFERENCES inventory(sku),
  quantity BIGINT NOT NULL CHECK (quantity > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Ordinary runs: large stock so faults, not stockouts, drive failures.
INSERT INTO inventory (sku, initial_stock, available)
VALUES ('demo-item', 100000, 100000)
ON CONFLICT (sku) DO NOTHING;
