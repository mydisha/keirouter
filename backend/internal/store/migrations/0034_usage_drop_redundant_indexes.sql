-- Every usage row updates every index on usage_records, and that table is
-- written once per proxied request. These three indexes are left prefixes of
-- wider (tenant_id, created_at, ...) indexes that remain, and a B-tree serves a
-- DESC scan by reading backwards, so the planner never chose them: dashboard
-- queries keep the same plans without them while each insert does less work.
DROP INDEX IF EXISTS idx_usage_tenant_time;
DROP INDEX IF EXISTS idx_usage_tenant_time_desc;
DROP INDEX IF EXISTS idx_usage_tenant_time_provider;
