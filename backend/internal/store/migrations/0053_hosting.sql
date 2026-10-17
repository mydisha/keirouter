-- 9router hosting: an order can now buy a hosted instance instead of credit.
-- product tells the webhook which fulfilment path to run. terms_* record the
-- hosting terms version the user accepted at checkout.
ALTER TABLE payment_orders ADD COLUMN product TEXT NOT NULL DEFAULT 'credit';
ALTER TABLE payment_orders ADD COLUMN hosting_instance_id TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_orders ADD COLUMN terms_version TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_orders ADD COLUMN terms_accepted_at TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS hosting_instances (
  id                 TEXT PRIMARY KEY,
  tenant_id          TEXT NOT NULL,
  key_id             TEXT NOT NULL,
  status             TEXT NOT NULL,
  image              TEXT NOT NULL DEFAULT '',
  subdomain          TEXT NOT NULL DEFAULT '',
  fqdn               TEXT NOT NULL DEFAULT '',
  dokploy_app_id     TEXT NOT NULL DEFAULT '',
  dokploy_app_name   TEXT NOT NULL DEFAULT '',
  dokploy_domain_id  TEXT NOT NULL DEFAULT '',
  data_volume        TEXT NOT NULL DEFAULT '',
  last_error         TEXT NOT NULL DEFAULT '',
  paid_until         TEXT NOT NULL DEFAULT '',
  prepaid_days       INTEGER NOT NULL DEFAULT 0,
  password_wrapped_dek TEXT NOT NULL DEFAULT '',
  password_ciphertext  TEXT NOT NULL DEFAULT '',
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL,
  provisioned_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_hosting_instances_key ON hosting_instances(key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_hosting_instances_status ON hosting_instances(status, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_hosting_instances_subdomain
  ON hosting_instances(subdomain) WHERE subdomain <> '';
