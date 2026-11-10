-- MCP gateway: upstream MCP servers, their synced tool catalogs, per-key
-- access, and the tool-call log.

-- One row per upstream MCP server. slug prefixes tool names (slug__tool) and
-- never changes after create. secret_* holds the sealed JSON blob with the
-- auth secret, basic-auth username and static headers.
CREATE TABLE IF NOT EXISTS mcp_servers (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL,
    slug               TEXT NOT NULL,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    preset_id          TEXT NOT NULL DEFAULT '',
    url                TEXT NOT NULL,
    auth_type          TEXT NOT NULL DEFAULT 'none',
    auth_header        TEXT NOT NULL DEFAULT '',
    secret_wrapped_dek TEXT NOT NULL DEFAULT '',
    secret_ciphertext  TEXT NOT NULL DEFAULT '',
    forward_headers    TEXT NOT NULL DEFAULT '[]',
    tool_mode          TEXT NOT NULL DEFAULT 'all',
    timeout_ms         INTEGER NOT NULL DEFAULT 60000,
    enabled            INTEGER NOT NULL DEFAULT 1,
    protocol_version   TEXT NOT NULL DEFAULT '',
    health_status      TEXT NOT NULL DEFAULT 'unknown',
    health_error       TEXT NOT NULL DEFAULT '',
    health_checked_at  TEXT,
    tools_synced_at    TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_servers_slug ON mcp_servers(tenant_id, slug);

-- Tool catalog as last synced from the upstream server. definition is the
-- tool JSON as received. invalid_reason is set when the tool must be hidden
-- from clients (for example a malformed x-mcp-header annotation). changed_at
-- marks a definition that changed since the operator last saved the tool list.
CREATE TABLE IF NOT EXISTS mcp_server_tools (
    server_id       TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    definition      TEXT NOT NULL,
    definition_hash TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    invalid_reason  TEXT NOT NULL DEFAULT '',
    changed_at      TEXT,
    PRIMARY KEY (server_id, name)
);

-- Explicit MCP access mode per API key: none, all or selected. Existing keys
-- default to all.
ALTER TABLE api_keys ADD COLUMN mcp_mode TEXT NOT NULL DEFAULT 'all';

-- Servers a key may use when its mcp_mode is selected. tool_mode is explicit
-- as well: all, or selected with the tool names in tools (JSON array).
CREATE TABLE IF NOT EXISTS api_key_mcp_access (
    api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    server_id  TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_mode  TEXT NOT NULL DEFAULT 'all',
    tools      TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (api_key_id, server_id)
);
CREATE INDEX IF NOT EXISTS idx_akmcp_server ON api_key_mcp_access(server_id);

-- One row per tools/call through the gateway. Arguments and results are not
-- stored, only their sizes.
CREATE TABLE IF NOT EXISTS mcp_call_logs (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    request_id     TEXT NOT NULL,
    api_key_id     TEXT NOT NULL,
    server_id      TEXT NOT NULL,
    server_slug    TEXT NOT NULL,
    tool           TEXT NOT NULL,
    status         TEXT NOT NULL,
    error          TEXT NOT NULL DEFAULT '',
    latency_ms     INTEGER NOT NULL,
    upstream_ms    INTEGER NOT NULL DEFAULT 0,
    request_bytes  INTEGER NOT NULL DEFAULT 0,
    response_bytes INTEGER NOT NULL DEFAULT 0,
    client         TEXT NOT NULL DEFAULT '',
    trace_id       TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mcp_logs_time ON mcp_call_logs(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_mcp_logs_server ON mcp_call_logs(server_id, created_at);
CREATE INDEX IF NOT EXISTS idx_mcp_logs_key ON mcp_call_logs(api_key_id, created_at);
