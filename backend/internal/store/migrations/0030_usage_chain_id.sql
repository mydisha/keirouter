-- Attribute usage to the routing chain that served it (empty = direct target).
ALTER TABLE usage_records ADD COLUMN chain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_usage_tenant_chain ON usage_records(tenant_id, chain_id);
