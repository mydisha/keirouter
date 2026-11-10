package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MCPKeyAccess is a key's explicit MCP access: the mode, and in selected
// mode the servers (and per server, the tools) it may use.
type MCPKeyAccess struct {
	Mode   string
	Grants []MCPKeyGrant
}

// GetKeyAccess returns a key's MCP access. ErrNotFound when the key does not
// exist.
func (r *MCPRepo) GetKeyAccess(ctx context.Context, keyID string) (MCPKeyAccess, error) {
	var a MCPKeyAccess
	err := r.db.sql.QueryRowContext(ctx, r.db.rebind(`SELECT mcp_mode FROM api_keys WHERE id = ?`), keyID).Scan(&a.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPKeyAccess{}, ErrNotFound
	}
	if err != nil {
		return MCPKeyAccess{}, fmt.Errorf("store: get mcp access: %w", err)
	}
	rows, err := r.db.sql.QueryContext(ctx, r.db.rebind(`SELECT server_id, tool_mode, tools FROM api_key_mcp_access WHERE api_key_id = ? ORDER BY server_id`), keyID)
	if err != nil {
		return MCPKeyAccess{}, fmt.Errorf("store: get mcp access: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var g MCPKeyGrant
		var tools string
		if err := rows.Scan(&g.ServerID, &g.ToolMode, &tools); err != nil {
			return MCPKeyAccess{}, fmt.Errorf("store: get mcp access: %w", err)
		}
		if err := json.Unmarshal([]byte(tools), &g.Tools); err != nil {
			return MCPKeyAccess{}, fmt.Errorf("store: get mcp access tools: %w", err)
		}
		a.Grants = append(a.Grants, g)
	}
	return a, rows.Err()
}

// SetKeyAccess replaces a key's MCP access in one transaction. Grants are
// only stored in selected mode; other modes clear them.
func (r *MCPRepo) SetKeyAccess(ctx context.Context, keyID string, a MCPKeyAccess) error {
	switch a.Mode {
	case MCPModeNone, MCPModeAll, MCPModeSelected:
	default:
		return fmt.Errorf("%w: mcp mode %q", ErrInvalid, a.Mode)
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set mcp access: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, r.db.rebind(`UPDATE api_keys SET mcp_mode = ? WHERE id = ?`), a.Mode, keyID)
	if err != nil {
		return fmt.Errorf("store: set mcp access: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, r.db.rebind(`DELETE FROM api_key_mcp_access WHERE api_key_id = ?`), keyID); err != nil {
		return fmt.Errorf("store: set mcp access: %w", err)
	}
	if a.Mode == MCPModeSelected {
		ins := r.db.rebind(`INSERT INTO api_key_mcp_access (api_key_id, server_id, tool_mode, tools) VALUES (?, ?, ?, ?)`)
		for _, g := range a.Grants {
			if g.ToolMode != MCPModeAll && g.ToolMode != MCPModeSelected {
				return fmt.Errorf("%w: tool mode %q", ErrInvalid, g.ToolMode)
			}
			tools := []string{}
			if g.ToolMode == MCPModeSelected {
				tools = append(tools, g.Tools...)
			}
			raw, _ := json.Marshal(tools)
			if _, err := tx.ExecContext(ctx, ins, keyID, g.ServerID, g.ToolMode, string(raw)); err != nil {
				return fmt.Errorf("store: set mcp access grant: %w", err)
			}
		}
	}
	return tx.Commit()
}

// SetMCPModeOnTx sets a new key's MCP mode inside the transaction that
// creates it.
func (r *APIKeyRepo) SetMCPModeOnTx(ctx context.Context, tx *sql.Tx, keyID, mode string) error {
	if mode != MCPModeNone && mode != MCPModeAll {
		return fmt.Errorf("%w: mcp mode %q", ErrInvalid, mode)
	}
	res, err := tx.ExecContext(ctx, r.db.rebind(`UPDATE api_keys SET mcp_mode = ? WHERE id = ?`), mode, keyID)
	if err != nil {
		return fmt.Errorf("store: set key mcp mode: %w", err)
	}
	return requireOneRow(res)
}

// ListKeysForServer returns the ids of operator keys that may use a server:
// keys in all mode, plus selected-mode keys with a grant for it. The map
// value is nil for every tool, or the granted tool names.
func (r *MCPRepo) ListKeysForServer(ctx context.Context, tenantID, serverID string) (map[string][]string, error) {
	out := map[string][]string{}
	rows, err := r.db.sql.QueryContext(ctx, r.db.rebind(`SELECT id FROM api_keys WHERE tenant_id = ? AND kind = '' AND mcp_mode = ?`), tenantID, MCPModeAll)
	if err != nil {
		return nil, fmt.Errorf("store: list mcp keys: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = nil
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = r.db.sql.QueryContext(ctx, r.db.rebind(`SELECT a.api_key_id, a.tool_mode, a.tools FROM api_key_mcp_access a
		JOIN api_keys k ON k.id = a.api_key_id
		WHERE a.server_id = ? AND k.mcp_mode = ? AND k.kind = ''`), serverID, MCPModeSelected)
	if err != nil {
		return nil, fmt.Errorf("store: list mcp keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, mode, tools string
		if err := rows.Scan(&id, &mode, &tools); err != nil {
			return nil, err
		}
		if mode == MCPModeAll {
			out[id] = nil
			continue
		}
		var names []string
		_ = json.Unmarshal([]byte(tools), &names)
		if names == nil {
			names = []string{}
		}
		out[id] = names
	}
	return out, rows.Err()
}

// MCPCallLog is one tools/call through the gateway.
type MCPCallLog struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"-"`
	RequestID     string    `json:"request_id"`
	APIKeyID      string    `json:"api_key_id"`
	ServerID      string    `json:"server_id"`
	ServerSlug    string    `json:"server_slug"`
	Tool          string    `json:"tool"`
	Status        string    `json:"status"`
	Error         string    `json:"error"`
	LatencyMS     int64     `json:"latency_ms"`
	UpstreamMS    int64     `json:"upstream_ms"`
	RequestBytes  int64     `json:"request_bytes"`
	ResponseBytes int64     `json:"response_bytes"`
	Client        string    `json:"client"`
	TraceID       string    `json:"trace_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// InsertCallLogs writes a batch of call logs in one transaction.
func (r *MCPRepo) InsertCallLogs(ctx context.Context, logs []MCPCallLog) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: insert mcp logs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := r.db.rebind(`INSERT INTO mcp_call_logs (id, tenant_id, request_id, api_key_id, server_id, server_slug, tool,
		status, error, latency_ms, upstream_ms, request_bytes, response_bytes, client, trace_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("store: insert mcp logs: %w", err)
	}
	defer stmt.Close()
	for _, l := range logs {
		if _, err := stmt.ExecContext(ctx, l.ID, l.TenantID, l.RequestID, l.APIKeyID, l.ServerID, l.ServerSlug, l.Tool,
			l.Status, l.Error, l.LatencyMS, l.UpstreamMS, l.RequestBytes, l.ResponseBytes, l.Client, l.TraceID,
			formatTime(l.CreatedAt)); err != nil {
			return fmt.Errorf("store: insert mcp log: %w", err)
		}
	}
	return tx.Commit()
}

// MCPLogFilter narrows ListCallLogs.
type MCPLogFilter struct {
	TenantID string
	ServerID string
	APIKeyID string
	Status   string
	Tool     string // substring match
	// Cursor continues an earlier page: the created_at|id of its last row.
	Cursor string
	Limit  int
}

// ListCallLogs returns call logs newest first. next is the cursor for the
// following page, empty on the last page.
func (r *MCPRepo) ListCallLogs(ctx context.Context, f MCPLogFilter) (logs []MCPCallLog, next string, err error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where := []string{"tenant_id = ?"}
	args := []any{f.TenantID}
	if f.ServerID != "" {
		where = append(where, "server_id = ?")
		args = append(args, f.ServerID)
	}
	if f.APIKeyID != "" {
		where = append(where, "api_key_id = ?")
		args = append(args, f.APIKeyID)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Tool != "" {
		where = append(where, "tool LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(f.Tool)+"%")
	}
	if f.Cursor != "" {
		at, id, ok := strings.Cut(f.Cursor, "|")
		if !ok {
			return nil, "", fmt.Errorf("%w: cursor", ErrInvalid)
		}
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, at, at, id)
	}
	q := `SELECT id, request_id, api_key_id, server_id, server_slug, tool, status, error, latency_ms, upstream_ms,
		request_bytes, response_bytes, client, trace_id, created_at FROM mcp_call_logs
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(f.Limit+1)
	rows, err := r.db.sql.QueryContext(ctx, r.db.rebind(q), args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: list mcp logs: %w", err)
	}
	defer rows.Close()
	var created []string
	for rows.Next() {
		var l MCPCallLog
		var at string
		if err := rows.Scan(&l.ID, &l.RequestID, &l.APIKeyID, &l.ServerID, &l.ServerSlug, &l.Tool, &l.Status, &l.Error,
			&l.LatencyMS, &l.UpstreamMS, &l.RequestBytes, &l.ResponseBytes, &l.Client, &l.TraceID, &at); err != nil {
			return nil, "", fmt.Errorf("store: list mcp logs: %w", err)
		}
		l.CreatedAt = parseTime(at)
		logs = append(logs, l)
		created = append(created, at)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(logs) > f.Limit {
		logs = logs[:f.Limit]
		next = created[f.Limit-1] + "|" + logs[f.Limit-1].ID
	}
	return logs, next, nil
}

// MCPStats aggregates call logs since a time.
type MCPStats struct {
	Calls     int64            `json:"calls"`
	ByStatus  map[string]int64 `json:"by_status"`
	P95MS     *int64           `json:"p95_latency_ms"`
	ByServer  map[string]int64 `json:"by_server"`
	ErrorRate *float64         `json:"error_rate"`
}

// CallStats aggregates a tenant's call logs since since.
func (r *MCPRepo) CallStats(ctx context.Context, tenantID string, since time.Time) (MCPStats, error) {
	st := MCPStats{ByStatus: map[string]int64{}, ByServer: map[string]int64{}}
	s := formatTime(since)
	rows, err := r.db.sql.QueryContext(ctx, r.db.rebind(`SELECT server_id, status, COUNT(*) FROM mcp_call_logs
		WHERE tenant_id = ? AND created_at >= ? GROUP BY server_id, status`), tenantID, s)
	if err != nil {
		return st, fmt.Errorf("store: mcp stats: %w", err)
	}
	for rows.Next() {
		var server, status string
		var n int64
		if err := rows.Scan(&server, &status, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.Calls += n
		st.ByStatus[status] += n
		st.ByServer[server] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	if st.Calls == 0 {
		return st, nil
	}
	failed := st.Calls - st.ByStatus["success"] - st.ByStatus["denied"] - st.ByStatus["rate_limited"]
	served := st.Calls - st.ByStatus["denied"] - st.ByStatus["rate_limited"]
	if served > 0 {
		rate := float64(failed) / float64(served)
		st.ErrorRate = &rate
		// p95 over calls that reached an upstream; ORDER BY + OFFSET works on
		// both engines and reads one index range.
		offset := int64(float64(served) * 0.95)
		if offset >= served {
			offset = served - 1
		}
		var p int64
		err := r.db.sql.QueryRowContext(ctx, r.db.rebind(`SELECT latency_ms FROM mcp_call_logs
			WHERE tenant_id = ? AND created_at >= ? AND status NOT IN ('denied', 'rate_limited')
			ORDER BY latency_ms LIMIT 1 OFFSET `+strconv.FormatInt(offset, 10)), tenantID, s).Scan(&p)
		if err == nil {
			st.P95MS = &p
		} else if !errors.Is(err, sql.ErrNoRows) {
			return st, fmt.Errorf("store: mcp p95: %w", err)
		}
	}
	return st, nil
}

// DeleteCallLogsBefore drops call logs older than t.
func (r *MCPRepo) DeleteCallLogsBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := r.db.sql.ExecContext(ctx, r.db.rebind(`DELETE FROM mcp_call_logs WHERE created_at < ?`), formatTime(t))
	if err != nil {
		return 0, fmt.Errorf("store: delete mcp logs: %w", err)
	}
	return res.RowsAffected()
}
