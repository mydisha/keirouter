package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MCP access modes. They are stored explicitly; readers never infer "all"
// from an empty child list.
const (
	MCPModeNone     = "none"
	MCPModeAll      = "all"
	MCPModeSelected = "selected"
)

// ErrConflict is returned when an insert collides with a unique constraint.
var ErrConflict = errors.New("store: conflict")

// ErrInvalid is returned when input refers to something that does not exist
// or breaks a store-level rule.
var ErrInvalid = errors.New("store: invalid input")

// isUniqueViolation recognises unique-constraint errors on both engines:
// modernc SQLite reports "UNIQUE constraint failed", Postgres SQLSTATE 23505.
func isUniqueViolation(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "SQLSTATE 23505")
}

// MCP server health states.
const (
	MCPHealthUnknown = "unknown"
	MCPHealthHealthy = "healthy"
	MCPHealthFailing = "failing"
)

// MCPServer is an upstream MCP server registered by the operator.
type MCPServer struct {
	ID               string
	TenantID         string
	Slug             string
	Name             string
	Description      string
	PresetID         string
	URL              string
	AuthType         string
	AuthHeader       string
	SecretWrappedDEK string
	SecretCipher     string
	ForwardHeaders   []string
	ToolMode         string
	TimeoutMS        int
	Enabled          bool
	ProtocolVersion  string
	HealthStatus     string
	HealthError      string
	HealthCheckedAt  *time.Time
	ToolsSyncedAt    *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// MCPServerTool is one tool from a server's synced catalog.
type MCPServerTool struct {
	ServerID       string
	Name           string
	Definition     string
	DefinitionHash string
	Enabled        bool
	InvalidReason  string
	ChangedAt      *time.Time
}

// MCPSyncedTool is a tool as returned by the latest upstream sync.
type MCPSyncedTool struct {
	Name           string
	Definition     string
	DefinitionHash string
	InvalidReason  string
}

// MCPKeyGrant is one server a key may use when its mode is selected.
type MCPKeyGrant struct {
	ServerID string
	ToolMode string
	Tools    []string
}

// MCPRepo persists MCP servers, tool catalogs and per-key access.
type MCPRepo struct{ db *DB }

// MCP returns the MCP repository.
func (db *DB) MCP() *MCPRepo { return &MCPRepo{db: db} }

const mcpServerColumns = `id, tenant_id, slug, name, description, preset_id, url, auth_type, auth_header,
	secret_wrapped_dek, secret_ciphertext, forward_headers, tool_mode, timeout_ms, enabled,
	protocol_version, health_status, health_error, health_checked_at, tools_synced_at, created_at, updated_at`

// CreateServer inserts a server. ErrConflict is returned when the slug is
// already used in the tenant.
func (r *MCPRepo) CreateServer(ctx context.Context, s MCPServer) error {
	fwd, err := json.Marshal(nonNilStrings(s.ForwardHeaders))
	if err != nil {
		return fmt.Errorf("store: create mcp server: %w", err)
	}
	q := r.db.rebind(`INSERT INTO mcp_servers (` + mcpServerColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = r.db.sql.ExecContext(ctx, q,
		s.ID, s.TenantID, s.Slug, s.Name, s.Description, s.PresetID, s.URL, s.AuthType, s.AuthHeader,
		s.SecretWrappedDEK, s.SecretCipher, string(fwd), s.ToolMode, s.TimeoutMS, boolToInt(s.Enabled),
		s.ProtocolVersion, defaultString(s.HealthStatus, MCPHealthUnknown), s.HealthError,
		nullTime(s.HealthCheckedAt), nullTime(s.ToolsSyncedAt), formatTime(s.CreatedAt), formatTime(s.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("store: create mcp server: %w", err)
	}
	return nil
}

// UpdateServer rewrites the operator-editable fields of a server. Slug,
// tenant, health and sync columns are not touched.
func (r *MCPRepo) UpdateServer(ctx context.Context, s MCPServer) error {
	fwd, err := json.Marshal(nonNilStrings(s.ForwardHeaders))
	if err != nil {
		return fmt.Errorf("store: update mcp server: %w", err)
	}
	q := r.db.rebind(`UPDATE mcp_servers SET name = ?, description = ?, preset_id = ?, url = ?,
		auth_type = ?, auth_header = ?, secret_wrapped_dek = ?, secret_ciphertext = ?, forward_headers = ?,
		timeout_ms = ?, enabled = ?, protocol_version = ?, updated_at = ?
		WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, s.Name, s.Description, s.PresetID, s.URL,
		s.AuthType, s.AuthHeader, s.SecretWrappedDEK, s.SecretCipher, string(fwd),
		s.TimeoutMS, boolToInt(s.Enabled), s.ProtocolVersion, formatTime(s.UpdatedAt), s.ID)
	if err != nil {
		return fmt.Errorf("store: update mcp server: %w", err)
	}
	return requireOneRow(res)
}

// GetServer returns one server by id.
func (r *MCPRepo) GetServer(ctx context.Context, id string) (MCPServer, error) {
	q := r.db.rebind(`SELECT ` + mcpServerColumns + ` FROM mcp_servers WHERE id = ?`)
	s, err := scanMCPServer(r.db.sql.QueryRowContext(ctx, q, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPServer{}, ErrNotFound
	}
	return s, err
}

// ListServers returns every server of a tenant ordered by slug.
func (r *MCPRepo) ListServers(ctx context.Context, tenantID string) ([]MCPServer, error) {
	q := r.db.rebind(`SELECT ` + mcpServerColumns + ` FROM mcp_servers WHERE tenant_id = ? ORDER BY slug`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list mcp servers: %w", err)
	}
	defer rows.Close()
	var out []MCPServer
	for rows.Next() {
		s, err := scanMCPServer(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: list mcp servers: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteServer removes a server; its tools and key grants cascade.
func (r *MCPRepo) DeleteServer(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM mcp_servers WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: delete mcp server: %w", err)
	}
	return requireOneRow(res)
}

// SetServerHealth records the outcome of the latest sync or probe.
func (r *MCPRepo) SetServerHealth(ctx context.Context, id, status, errText, protocolVersion string, at time.Time) error {
	q := r.db.rebind(`UPDATE mcp_servers SET health_status = ?, health_error = ?, health_checked_at = ?,
		protocol_version = CASE WHEN ? = '' THEN protocol_version ELSE ? END WHERE id = ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, status, errText, formatTime(at), protocolVersion, protocolVersion, id); err != nil {
		return fmt.Errorf("store: set mcp server health: %w", err)
	}
	return nil
}

// ListTools returns a server's tools ordered by name.
func (r *MCPRepo) ListTools(ctx context.Context, serverID string) ([]MCPServerTool, error) {
	q := r.db.rebind(`SELECT server_id, name, definition, definition_hash, enabled, invalid_reason, changed_at
		FROM mcp_server_tools WHERE server_id = ? ORDER BY name`)
	return r.queryTools(ctx, q, serverID)
}

// ListTenantTools returns the tools of every server in a tenant, ordered by
// server id then tool name.
func (r *MCPRepo) ListTenantTools(ctx context.Context, tenantID string) ([]MCPServerTool, error) {
	q := r.db.rebind(`SELECT t.server_id, t.name, t.definition, t.definition_hash, t.enabled, t.invalid_reason, t.changed_at
		FROM mcp_server_tools t JOIN mcp_servers s ON s.id = t.server_id
		WHERE s.tenant_id = ? ORDER BY t.server_id, t.name`)
	return r.queryTools(ctx, q, tenantID)
}

func (r *MCPRepo) queryTools(ctx context.Context, q string, arg string) ([]MCPServerTool, error) {
	rows, err := r.db.sql.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("store: list mcp tools: %w", err)
	}
	defer rows.Close()
	var out []MCPServerTool
	for rows.Next() {
		var (
			t       MCPServerTool
			enabled int
			changed sql.NullString
		)
		if err := rows.Scan(&t.ServerID, &t.Name, &t.Definition, &t.DefinitionHash, &enabled, &t.InvalidReason, &changed); err != nil {
			return nil, fmt.Errorf("store: list mcp tools: %w", err)
		}
		t.Enabled = enabled != 0
		t.ChangedAt = parseNullTime(changed)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReplaceTools stores the result of a successful sync in one transaction.
// Tools that disappeared upstream are removed. A new tool is enabled when the
// server exposes every tool and disabled when the operator picks tools one by
// one. An existing tool keeps its enabled flag; when its definition changed,
// changed_at is set so the dashboard can flag it.
func (r *MCPRepo) ReplaceTools(ctx context.Context, serverID string, tools []MCPSyncedTool, at time.Time) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: replace mcp tools: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var toolMode string
	if err := tx.QueryRowContext(ctx, r.db.rebind(`SELECT tool_mode FROM mcp_servers WHERE id = ?`), serverID).Scan(&toolMode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("store: replace mcp tools: %w", err)
	}

	existing := map[string]string{}
	rows, err := tx.QueryContext(ctx, r.db.rebind(`SELECT name, definition_hash FROM mcp_server_tools WHERE server_id = ?`), serverID)
	if err != nil {
		return fmt.Errorf("store: replace mcp tools: %w", err)
	}
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			rows.Close()
			return fmt.Errorf("store: replace mcp tools: %w", err)
		}
		existing[name] = hash
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: replace mcp tools: %w", err)
	}

	now := formatTime(at)
	insert := r.db.rebind(`INSERT INTO mcp_server_tools (server_id, name, definition, definition_hash, enabled, invalid_reason, changed_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)`)
	update := r.db.rebind(`UPDATE mcp_server_tools SET definition = ?, definition_hash = ?, invalid_reason = ?,
		changed_at = CASE WHEN ? THEN ? ELSE changed_at END WHERE server_id = ? AND name = ?`)
	seen := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if _, dup := seen[t.Name]; dup {
			continue
		}
		seen[t.Name] = struct{}{}
		oldHash, ok := existing[t.Name]
		if !ok {
			if _, err := tx.ExecContext(ctx, insert, serverID, t.Name, t.Definition, t.DefinitionHash,
				boolToInt(toolMode != MCPModeSelected), t.InvalidReason); err != nil {
				return fmt.Errorf("store: replace mcp tools insert: %w", err)
			}
			continue
		}
		changed := oldHash != t.DefinitionHash
		if _, err := tx.ExecContext(ctx, update, t.Definition, t.DefinitionHash, t.InvalidReason,
			changed, now, serverID, t.Name); err != nil {
			return fmt.Errorf("store: replace mcp tools update: %w", err)
		}
	}
	del := r.db.rebind(`DELETE FROM mcp_server_tools WHERE server_id = ? AND name = ?`)
	for name := range existing {
		if _, ok := seen[name]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, del, serverID, name); err != nil {
			return fmt.Errorf("store: replace mcp tools delete: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, r.db.rebind(`UPDATE mcp_servers SET tools_synced_at = ? WHERE id = ?`), now, serverID); err != nil {
		return fmt.Errorf("store: replace mcp tools: %w", err)
	}
	return tx.Commit()
}

// SetToolSelection saves the server's tool mode and, in selected mode, which
// tools are enabled. Saving also clears every changed_at flag: the operator
// has reviewed the current definitions. Unknown tool names are rejected.
func (r *MCPRepo) SetToolSelection(ctx context.Context, serverID, toolMode string, enabled []string) error {
	if toolMode != MCPModeAll && toolMode != MCPModeSelected {
		return fmt.Errorf("store: invalid tool mode %q", toolMode)
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set mcp tool selection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, r.db.rebind(`UPDATE mcp_servers SET tool_mode = ? WHERE id = ?`), toolMode, serverID)
	if err != nil {
		return fmt.Errorf("store: set mcp tool selection: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return err
	}
	if toolMode == MCPModeAll {
		if _, err := tx.ExecContext(ctx, r.db.rebind(`UPDATE mcp_server_tools SET enabled = 1, changed_at = NULL WHERE server_id = ?`), serverID); err != nil {
			return fmt.Errorf("store: set mcp tool selection: %w", err)
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, r.db.rebind(`UPDATE mcp_server_tools SET enabled = 0, changed_at = NULL WHERE server_id = ?`), serverID); err != nil {
		return fmt.Errorf("store: set mcp tool selection: %w", err)
	}
	on := r.db.rebind(`UPDATE mcp_server_tools SET enabled = 1 WHERE server_id = ? AND name = ?`)
	for _, name := range enabled {
		res, err := tx.ExecContext(ctx, on, serverID, name)
		if err != nil {
			return fmt.Errorf("store: set mcp tool selection: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: unknown tool %q", ErrInvalid, name)
		}
	}
	return tx.Commit()
}

// CountKeysWithMCPMode counts a tenant's operator keys in the given mode.
func (r *MCPRepo) CountKeysWithMCPMode(ctx context.Context, tenantID, mode string) (int, error) {
	q := r.db.rebind(`SELECT COUNT(*) FROM api_keys WHERE tenant_id = ? AND kind = '' AND disabled = 0 AND mcp_mode = ?`)
	var n int
	if err := r.db.sql.QueryRowContext(ctx, q, tenantID, mode).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count mcp keys: %w", err)
	}
	return n, nil
}

func scanMCPServer(scan func(dest ...any) error) (MCPServer, error) {
	var (
		s                MCPServer
		fwd              string
		enabled          int
		checked, synced  sql.NullString
		created, updated string
	)
	if err := scan(&s.ID, &s.TenantID, &s.Slug, &s.Name, &s.Description, &s.PresetID, &s.URL, &s.AuthType, &s.AuthHeader,
		&s.SecretWrappedDEK, &s.SecretCipher, &fwd, &s.ToolMode, &s.TimeoutMS, &enabled,
		&s.ProtocolVersion, &s.HealthStatus, &s.HealthError, &checked, &synced, &created, &updated); err != nil {
		return MCPServer{}, err
	}
	if fwd != "" {
		if err := json.Unmarshal([]byte(fwd), &s.ForwardHeaders); err != nil {
			return MCPServer{}, fmt.Errorf("forward_headers: %w", err)
		}
	}
	s.Enabled = enabled != 0
	s.HealthCheckedAt = parseNullTime(checked)
	s.ToolsSyncedAt = parseNullTime(synced)
	s.CreatedAt = parseTime(created)
	s.UpdatedAt = parseTime(updated)
	return s, nil
}

func parseNullTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t := parseTime(v.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func requireOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
