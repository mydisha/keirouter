-- Audit trail fields. The 0001 audit_log table keeps its columns with these
-- meanings: actor = actor id, target = resource id, detail = JSON attributes,
-- created_at = when the action happened. The new columns map one-to-one onto
-- OpenTelemetry log record attributes so rows export without reshaping.
--
-- chain_id / chain_seq / prev_hash / hash make the log tamper-evident: each
-- writer process keeps its own chain where every row's HMAC covers the
-- previous row's hash, so an edited, inserted or deleted row breaks it.
ALTER TABLE audit_log ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN severity TEXT NOT NULL DEFAULT 'INFO';
ALTER TABLE audit_log ADD COLUMN actor_type TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN client_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN http_method TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN http_route TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN status_code INTEGER NOT NULL DEFAULT 0;
ALTER TABLE audit_log ADD COLUMN resource_type TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN span_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN chain_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN chain_seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE audit_log ADD COLUMN prev_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN hash TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_audit_tenant_action_time ON audit_log(tenant_id, action, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_chain_seq ON audit_log(chain_id, chain_seq);
