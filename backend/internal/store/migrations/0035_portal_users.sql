-- Portal users: a Google identity bound to exactly one API key. The Google
-- subject (never the email) is the stable identity. key_id is UNIQUE so a key
-- can be claimed by at most one user; deleting the key removes the binding.
CREATE TABLE IF NOT EXISTS portal_users (
    google_sub TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    key_id     TEXT NOT NULL UNIQUE REFERENCES api_keys(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_portal_users_email ON portal_users(email);
