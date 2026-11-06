-- Keyed lookup index for API keys: HMAC-SHA256 over the key's SHA-256 digest,
-- under a key derived from the master key. Startup fills it for existing rows
-- from lookup_hash, so no key has to be reissued. lookup_kid records which
-- master key a row was indexed under.
--
-- lookup_hash and key_hash stay as they are: a release without this column
-- still authenticates every key, including ones minted after this migration.
ALTER TABLE api_keys ADD COLUMN lookup_hmac TEXT;
ALTER TABLE api_keys ADD COLUMN lookup_kid TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_lookup_hmac ON api_keys(lookup_hmac);
