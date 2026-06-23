-- Add Headroom token-saving analytics columns.
-- Headroom reports exact token counts from its compression proxy, unlike RTK's
-- byte/4 estimate, so store both systems separately and aggregate in analytics.
ALTER TABLE usage_records ADD COLUMN headroom_active INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN headroom_tokens_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN headroom_tokens_after INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN headroom_tokens_saved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN headroom_compression_ratio REAL NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN headroom_transforms TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN headroom_ccr_hashes TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_usage_headroom_savings ON usage_records(tenant_id, created_at, headroom_tokens_saved);