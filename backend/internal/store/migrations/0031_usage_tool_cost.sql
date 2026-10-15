-- Price classes that the token columns alone cannot express:
--   cache_write_1h_tokens  subset of cache_write_tokens written with a 1h TTL
--                          (Anthropic bills these at 2x input vs 1.25x for 5m)
--   web_search_requests    provider-side searches billed per request
--   tool_cost_nanos        the per-request tool charge, kept separate so the
--                          token cost components still sum to cost_nanos
ALTER TABLE usage_records ADD COLUMN cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN web_search_requests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN tool_cost_nanos BIGINT NOT NULL DEFAULT 0;
