-- Append-only ledger of API key budget top-ups. Each row records the exact
-- micro-USD amount credited, the operator reason, and the budget limit before
-- and after so the balance history is auditable.
CREATE TABLE IF NOT EXISTS key_topups (
  id                   TEXT PRIMARY KEY,
  tenant_id            TEXT NOT NULL,
  key_id               TEXT NOT NULL,
  budget_id            TEXT NOT NULL,
  amount_micros        BIGINT NOT NULL,
  reason               TEXT NOT NULL DEFAULT '',
  limit_before_micros  BIGINT NOT NULL,
  limit_after_micros   BIGINT NOT NULL,
  idempotency_key      TEXT NOT NULL DEFAULT '',
  actor                TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_key_topups_key ON key_topups(key_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_key_topups_idem
  ON key_topups(key_id, idempotency_key) WHERE idempotency_key <> '';
