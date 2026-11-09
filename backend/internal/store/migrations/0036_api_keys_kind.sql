-- kind marks keys KeiRouter manages itself. '' is an ordinary key an operator
-- created; 'playground' is the dashboard playground's identity: its secret is
-- discarded at creation, so it never authenticates on /v1, and the admin API
-- neither lists nor edits it. Usage it records still shows under its name.
ALTER TABLE api_keys ADD COLUMN kind TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_one_playground ON api_keys(tenant_id) WHERE kind = 'playground';
