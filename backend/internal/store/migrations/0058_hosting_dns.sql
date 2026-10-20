-- Hosting DNS: each instance has its own Cloudflare A record on the main
-- domain. The record id is kept so a retry never creates a second record and
-- deleting the instance removes it.
ALTER TABLE hosting_instances ADD COLUMN dns_record_id TEXT NOT NULL DEFAULT '';
