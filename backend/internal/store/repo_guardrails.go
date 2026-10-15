package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GuardrailRepo persists guardrail policies. Policies are looked up by
// (tenant, scope, scope_id) at request time and merged by the guardrails
// package's resolver.
type GuardrailRepo struct{ db *DB }

// Guardrails returns the guardrail policy repository.
func (db *DB) Guardrails() *GuardrailRepo { return &GuardrailRepo{db: db} }

const guardrailSelectCols = `id, tenant_id, scope, scope_id, name, enabled, config, created_at, updated_at`

// Upsert inserts a policy, or replaces an existing one at the same
// (tenant, scope, scope_id) tuple. This matches how the dashboard edits
// policies — there is one logical policy per scope tuple.
func (r *GuardrailRepo) Upsert(ctx context.Context, p GuardrailPolicy) error {
	// Try update first (matched by scope tuple); fall back to insert if no row.
	upd := r.db.rebind(`UPDATE guardrail_policies
		SET name = ?, enabled = ?, config = ?, updated_at = ?
		WHERE tenant_id = ? AND scope = ? AND scope_id = ?`)
	res, err := r.db.sql.ExecContext(ctx, upd,
		p.Name, boolToInt(p.Enabled), p.Config, formatTime(p.UpdatedAt),
		p.TenantID, string(p.Scope), p.ScopeID)
	if err != nil {
		return fmt.Errorf("store: upsert guardrail (update): %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		// Backfill the caller's ID so subsequent calls reference the existing row.
		if p.ID == "" {
			q := r.db.rebind(`SELECT id FROM guardrail_policies WHERE tenant_id = ? AND scope = ? AND scope_id = ?`)
			_ = r.db.sql.QueryRowContext(ctx, q, p.TenantID, string(p.Scope), p.ScopeID).Scan(&p.ID)
		}
		return nil
	}

	ins := r.db.rebind(`INSERT INTO guardrail_policies
		(id, tenant_id, scope, scope_id, name, enabled, config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = r.db.sql.ExecContext(ctx, ins,
		p.ID, p.TenantID, string(p.Scope), p.ScopeID, p.Name, boolToInt(p.Enabled),
		p.Config, formatTime(p.CreatedAt), formatTime(p.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: upsert guardrail (insert): %w", err)
	}
	return nil
}

// Get returns a policy by id.
func (r *GuardrailRepo) Get(ctx context.Context, id string) (GuardrailPolicy, error) {
	q := r.db.rebind(`SELECT ` + guardrailSelectCols + ` FROM guardrail_policies WHERE id = ?`)
	p, err := scanGuardrailPolicy(r.db.sql.QueryRowContext(ctx, q, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return GuardrailPolicy{}, ErrNotFound
	}
	return p, err
}

// GetByScope returns the (at most one) policy attached to a scope tuple.
// Returns ErrNotFound when no row exists — the resolver treats that as "no
// override at this layer".
func (r *GuardrailRepo) GetByScope(ctx context.Context, tenantID string, scope GuardrailScope, scopeID string) (GuardrailPolicy, error) {
	q := r.db.rebind(`SELECT ` + guardrailSelectCols + ` FROM guardrail_policies
		WHERE tenant_id = ? AND scope = ? AND scope_id = ?`)
	p, err := scanGuardrailPolicy(r.db.sql.QueryRowContext(ctx, q, tenantID, string(scope), scopeID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return GuardrailPolicy{}, ErrNotFound
	}
	return p, err
}

// List returns all policies for a tenant, newest first. The optional scope
// argument narrows to a single scope when non-empty.
func (r *GuardrailRepo) List(ctx context.Context, tenantID string, scope GuardrailScope) ([]GuardrailPolicy, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if scope == "" {
		q := r.db.rebind(`SELECT ` + guardrailSelectCols + ` FROM guardrail_policies
			WHERE tenant_id = ? ORDER BY scope, created_at DESC`)
		rows, err = r.db.sql.QueryContext(ctx, q, tenantID)
	} else {
		q := r.db.rebind(`SELECT ` + guardrailSelectCols + ` FROM guardrail_policies
			WHERE tenant_id = ? AND scope = ? ORDER BY created_at DESC`)
		rows, err = r.db.sql.QueryContext(ctx, q, tenantID, string(scope))
	}
	if err != nil {
		return nil, fmt.Errorf("store: list guardrails: %w", err)
	}
	defer rows.Close()

	var out []GuardrailPolicy
	for rows.Next() {
		p, err := scanGuardrailPolicyRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete removes a policy by id.
func (r *GuardrailRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM guardrail_policies WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, id)
	return err
}

func scanGuardrailPolicy(scan func(dest ...any) error) (GuardrailPolicy, error) {
	var (
		p       GuardrailPolicy
		scope   string
		enabled int
		created string
		updated string
	)
	err := scan(&p.ID, &p.TenantID, &scope, &p.ScopeID, &p.Name, &enabled, &p.Config, &created, &updated)
	if err != nil {
		return GuardrailPolicy{}, err
	}
	p.Scope = GuardrailScope(scope)
	p.Enabled = enabled != 0
	p.CreatedAt = parseTime(created)
	p.UpdatedAt = parseTime(updated)
	return p, nil
}

func scanGuardrailPolicyRows(rows *sql.Rows) (GuardrailPolicy, error) {
	var (
		p       GuardrailPolicy
		scope   string
		enabled int
		created string
		updated string
	)
	err := rows.Scan(&p.ID, &p.TenantID, &scope, &p.ScopeID, &p.Name, &enabled, &p.Config, &created, &updated)
	if err != nil {
		return GuardrailPolicy{}, fmt.Errorf("store: scan guardrail: %w", err)
	}
	p.Scope = GuardrailScope(scope)
	p.Enabled = enabled != 0
	p.CreatedAt = parseTime(created)
	p.UpdatedAt = parseTime(updated)
	return p, nil
}

// GuardrailLogRepo persists detector decisions for audit. Logs are append-only
// and batch-inserted from a buffered channel — never on the request path.
type GuardrailLogRepo struct{ db *DB }

// GuardrailLogs returns the audit log repository.
func (db *DB) GuardrailLogs() *GuardrailLogRepo { return &GuardrailLogRepo{db: db} }

const guardrailLogCols = `id, tenant_id, request_id, api_key_id, provider, model, chain_id, detector, direction, action, severity, reason, findings, created_at`

// Insert writes a single decision. Used by tests and low-volume callers; the
// hot path uses BatchInsert.
func (r *GuardrailLogRepo) Insert(ctx context.Context, e GuardrailLog) error {
	q := r.db.rebind(`INSERT INTO guardrail_logs (` + guardrailLogCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q,
		e.ID, e.TenantID, e.RequestID, e.APIKeyID, e.Provider, e.Model, e.ChainID,
		e.Detector, e.Direction, e.Action, e.Severity, e.Reason, e.Findings,
		formatTime(e.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert guardrail log: %w", err)
	}
	return nil
}

// BatchInsert writes a batch of decisions in a single transaction. Used by the
// async audit writer to amortize per-row overhead.
func (r *GuardrailLogRepo) BatchInsert(ctx context.Context, entries []GuardrailLog) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := r.db.rebind(`INSERT INTO guardrail_logs (` + guardrailLogCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("store: prepare guardrail log: %w", err)
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx,
			e.ID, e.TenantID, e.RequestID, e.APIKeyID, e.Provider, e.Model, e.ChainID,
			e.Detector, e.Direction, e.Action, e.Severity, e.Reason, e.Findings,
			formatTime(e.CreatedAt)); err != nil {
			return fmt.Errorf("store: batch insert guardrail log: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteOlderThan removes audit log rows whose created_at is strictly older
// than cutoff. Returns the number of rows deleted. Used by the retention
// sweeper to enforce KEIROUTER_GUARDRAILS__AUDIT_RETENTION_DAYS.
func (r *GuardrailLogRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	q := r.db.rebind(`DELETE FROM guardrail_logs WHERE created_at < ?`)
	res, err := r.db.sql.ExecContext(ctx, q, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("store: delete guardrail logs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListFilter narrows audit log queries. Empty fields are ignored.
type GuardrailLogFilter struct {
	APIKeyID  string
	Detector  string
	Action    string
	Direction string
	Model     string
	ChainID   string
	RequestID string
	// Since/Until bound created_at; zero values leave that side open.
	Since, Until time.Time
	Limit        int
}

// List returns recent audit log rows for a tenant matching the filter, newest
// first.
func (r *GuardrailLogRepo) List(ctx context.Context, tenantID string, f GuardrailLogFilter) ([]GuardrailLog, error) {
	clauses := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if f.APIKeyID != "" {
		clauses = append(clauses, "api_key_id = ?")
		args = append(args, f.APIKeyID)
	}
	if f.Detector != "" {
		clauses = append(clauses, "detector = ?")
		args = append(args, f.Detector)
	}
	if f.Action != "" {
		clauses = append(clauses, "action = ?")
		args = append(args, f.Action)
	}
	if f.Direction != "" {
		clauses = append(clauses, "direction = ?")
		args = append(args, f.Direction)
	}
	if f.Model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, f.Model)
	}
	if f.ChainID != "" {
		clauses = append(clauses, "chain_id = ?")
		args = append(args, f.ChainID)
	}
	if f.RequestID != "" {
		clauses = append(clauses, "request_id = ?")
		args = append(args, f.RequestID)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, formatTime(f.Since))
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "created_at < ?")
		args = append(args, formatTime(f.Until))
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	q := r.db.rebind(`SELECT ` + guardrailLogCols + ` FROM guardrail_logs
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY created_at DESC LIMIT ` + fmt.Sprintf("%d", limit))
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list guardrail logs: %w", err)
	}
	defer rows.Close()

	var out []GuardrailLog
	for rows.Next() {
		var (
			e       GuardrailLog
			created string
		)
		if err := rows.Scan(&e.ID, &e.TenantID, &e.RequestID, &e.APIKeyID, &e.Provider, &e.Model,
			&e.ChainID, &e.Detector, &e.Direction, &e.Action, &e.Severity, &e.Reason, &e.Findings,
			&created); err != nil {
			return nil, fmt.Errorf("store: scan guardrail log: %w", err)
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}
