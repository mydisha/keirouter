-- Upstream provider accounts: prepaid balances, provider mapping and top-ups
-- for the admin finance dashboard. All amounts are USD micros.
CREATE TABLE IF NOT EXISTS upstream_accounts (
    id                     TEXT PRIMARY KEY,
    name                   TEXT NOT NULL,
    url                    TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL DEFAULT 'active',
    opening_balance_micros BIGINT NOT NULL DEFAULT 0,
    opening_at             TEXT NOT NULL,
    low_balance_micros     BIGINT NOT NULL DEFAULT 0,
    alert_fired_at         TEXT NOT NULL DEFAULT '',
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS upstream_account_providers (
    provider_id TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS upstream_topups (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
    amount_micros BIGINT NOT NULL,
    topup_at      TEXT NOT NULL,
    note          TEXT NOT NULL DEFAULT '',
    actor         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_upstream_topups_account
    ON upstream_topups(account_id, topup_at);

-- opening_at = migration time, in the store's RFC3339 UTC format.
INSERT INTO upstream_accounts (id, name, url, status, opening_balance_micros, opening_at, low_balance_micros, alert_fired_at, created_at, updated_at)
VALUES ('surplus', 'Surplus Intelligence', 'https://surplusintelligence.ai', 'active', 0, to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), 0, '', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
ON CONFLICT DO NOTHING;
INSERT INTO upstream_accounts (id, name, url, status, opening_balance_micros, opening_at, low_balance_micros, alert_fired_at, created_at, updated_at)
VALUES ('inferhub', 'Inferhub', 'https://inferhub.dev', 'active', 0, to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), 0, '', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
ON CONFLICT DO NOTHING;
INSERT INTO upstream_accounts (id, name, url, status, opening_balance_micros, opening_at, low_balance_micros, alert_fired_at, created_at, updated_at)
VALUES ('crowbot', 'Crowbot', 'https://crowbot.sh', 'planned', 0, to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), 0, '', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
ON CONFLICT DO NOTHING;
