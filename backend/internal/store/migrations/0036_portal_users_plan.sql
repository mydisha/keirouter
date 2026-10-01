-- Record which plan was used when a portal user self-provisioned their key, so
-- the admin Portal Users view can show and change it. Nullable: bindings made
-- before this migration (or manually claimed) have no plan attribution.
ALTER TABLE portal_users ADD COLUMN plan_id TEXT;
CREATE INDEX IF NOT EXISTS idx_portal_users_plan ON portal_users(plan_id);
