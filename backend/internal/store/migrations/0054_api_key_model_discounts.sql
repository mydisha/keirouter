-- Per-model minimum discount overrides for an API key. A row overrides the
-- key's default (api_keys.min_discount_pct) for that model; 0 turns the
-- minimum off for the model even when the key has a default.
CREATE TABLE IF NOT EXISTS api_key_model_discounts (
    api_key_id        TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    model             TEXT NOT NULL,
    min_discount_pct  INTEGER NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (api_key_id, model)
);
