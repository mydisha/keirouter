-- Append-only ledger of manual API key budget limit adjustments. Unlike
-- key_topups (always a credit), an adjustment can raise or lower the limit, so
-- delta_micros is signed and the operator reason is required to make the change
-- auditable. limit_before/after mirror the budget at the moment of the change.
CREATE TABLE IF NOT EXISTS key_limit_adjustments (
  id                   TEXT PRIMARY KEY,
  tenant_id            TEXT NOT NULL,
  key_id               TEXT NOT NULL,
  budget_id            TEXT NOT NULL,
  delta_micros         BIGINT NOT NULL,
  reason               TEXT NOT NULL DEFAULT '',
  limit_before_micros  BIGINT NOT NULL,
  limit_after_micros   BIGINT NOT NULL,
  idempotency_key      TEXT NOT NULL DEFAULT '',
  actor                TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_key_limit_adjustments_key ON key_limit_adjustments(key_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_key_limit_adjustments_idem
  ON key_limit_adjustments(key_id, idempotency_key) WHERE idempotency_key <> '';
