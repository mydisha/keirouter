-- Record what the guardrails did to each request on its terminal usage row.
-- guardrail_logs keeps one row per detector decision (several per request,
-- and streamed masks repeat per release), so it cannot answer "what share of
-- traffic was blocked". These columns hold one per-request verdict next to
-- the traffic denominator:
--   guardrail_action      strictest action: allow | log_only | warn | mask |
--                         block; '' when no guardrail policy applied
--   guardrail_detector    detector that produced that action
--   guardrail_direction   inbound | outbound
--   guardrail_mid_stream  1 when a block cut a stream the client was
--                         already receiving
--   guardrail_ms          total detector evaluation time (includes LLM judge
--                         and external engine calls)
-- Rows written before this migration keep the defaults; there is no backfill.
ALTER TABLE usage_records ADD COLUMN guardrail_action TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN guardrail_detector TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN guardrail_direction TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN guardrail_mid_stream INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN guardrail_ms INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_usage_guardrail_time ON usage_records(tenant_id, guardrail_action, created_at);
-- The stats breakdowns scan guardrail_logs by time window.
CREATE INDEX IF NOT EXISTS idx_guardrail_logs_time ON guardrail_logs(tenant_id, created_at, action);
