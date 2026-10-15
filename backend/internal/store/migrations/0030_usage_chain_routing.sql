-- Persist which routing chain resolved each request and how many attempts
-- failed over before the terminal one. Together with the serving provider and
-- model this yields durable per-step chain statistics (first-choice hit rate,
-- fallback rate, where traffic actually landed) that survive restarts, unlike
-- the in-memory health telemetry window.
-- chain_id itself is added by 0030_usage_chain_id (this fork's earlier
-- migration, applied first in filename order), so only fallback_count is new.
ALTER TABLE usage_records ADD COLUMN fallback_count INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_usage_chain_time ON usage_records(tenant_id, chain_id, created_at);
