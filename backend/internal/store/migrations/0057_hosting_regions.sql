-- Hosting regions: an instance records the region it was bought in, so its
-- price and the node it is created on follow that region. Instances from
-- before regions keep '' and take the region of their node.
ALTER TABLE hosting_instances ADD COLUMN region TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_hosting_instances_region ON hosting_instances(region, status);
