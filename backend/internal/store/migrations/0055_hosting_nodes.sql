-- Multi-node hosting: each instance remembers the Dokploy node it runs on.
-- Instances created before multi-node support belong to the "default" node,
-- which is what the old single server_id/root_domain settings become.
ALTER TABLE hosting_instances ADD COLUMN node_id TEXT NOT NULL DEFAULT '';
UPDATE hosting_instances SET node_id = 'default' WHERE subdomain <> '' OR dokploy_app_id <> '';
CREATE INDEX IF NOT EXISTS idx_hosting_instances_node ON hosting_instances(node_id, status);
