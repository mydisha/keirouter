package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// HealthRepo persists background account/model health probe results.
type HealthRepo struct{ db *DB }

// Health returns the account health repository.
func (db *DB) Health() *HealthRepo { return &HealthRepo{db: db} }

const accountHealthColumns = `id, tenant_id, account_id, provider, model, status,
	latency_ms, consecutive_failures, consecutive_successes, last_ok_at,
	last_checked_at, last_error, updated_at`

// Upsert inserts or updates one account/model health row.
func (r *HealthRepo) Upsert(ctx context.Context, h AccountHealth) error {
	q := r.db.rebind(`INSERT INTO account_health (` + accountHealthColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, model) DO UPDATE SET
			tenant_id = excluded.tenant_id,
			provider = excluded.provider,
			status = excluded.status,
			latency_ms = excluded.latency_ms,
			consecutive_failures = excluded.consecutive_failures,
			consecutive_successes = excluded.consecutive_successes,
			last_ok_at = excluded.last_ok_at,
			last_checked_at = excluded.last_checked_at,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q,
		h.ID, h.TenantID, h.AccountID, h.Provider, h.Model, h.Status,
		h.LatencyMS, h.ConsecutiveFailures, h.ConsecutiveSuccesses, nullTime(h.LastOKAt),
		formatTime(h.LastCheckedAt), h.LastError, formatTime(h.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: upsert account health: %w", err)
	}
	return nil
}

// Get returns one account/model health row.
func (r *HealthRepo) Get(ctx context.Context, accountID, model string) (AccountHealth, error) {
	q := r.db.rebind(`SELECT ` + accountHealthColumns + ` FROM account_health WHERE account_id = ? AND model = ?`)
	h, err := scanAccountHealth(r.db.sql.QueryRowContext(ctx, q, accountID, model).Scan)
	if err == sql.ErrNoRows {
		return AccountHealth{}, ErrNotFound
	}
	if err != nil {
		return AccountHealth{}, fmt.Errorf("store: get account health: %w", err)
	}
	return h, nil
}

// IsUnhealthy reports whether the specific model or provider-level row is
// unhealthy for this account.
func (r *HealthRepo) IsUnhealthy(ctx context.Context, accountID, model string) (bool, error) {
	q := r.db.rebind(`SELECT status FROM account_health
		WHERE account_id = ? AND model IN (?, '__all__')`)
	rows, err := r.db.sql.QueryContext(ctx, q, accountID, model)
	if err != nil {
		return false, fmt.Errorf("store: check account health: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return false, err
		}
		if status == "unhealthy" {
			return true, nil
		}
	}
	return false, rows.Err()
}

// UnhealthyAccounts returns the set of account IDs (from accountIDs) marked
// unhealthy for model or '__all__'. Batches per-target health checks into a
// single query instead of one IsUnhealthy query per account.
func (r *HealthRepo) UnhealthyAccounts(ctx context.Context, accountIDs []string, model string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(accountIDs) == 0 {
		return out, nil
	}

	placeholders := make([]string, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+1)
	for i, id := range accountIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, model)

	q := r.db.rebind(`SELECT DISTINCT account_id FROM account_health
		WHERE account_id IN (` + strings.Join(placeholders, ",") + `)
		  AND model IN (?, '__all__')
		  AND status = 'unhealthy'`)

	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: unhealthy accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out[id] = true
	}
	return out, rows.Err()
}

// MarkHealthy sets an account/model pair to healthy when real traffic succeeds,
// resetting the failure counter so the background probe's unhealthy verdict
// can be overridden by production success. Only updates existing rows; new
// account/model pairs are created by the background checker.
func (r *HealthRepo) MarkHealthy(ctx context.Context, accountID, model string) error {
	now := formatTime(time.Now())
	q := r.db.rebind(`UPDATE account_health
		SET status = 'healthy', consecutive_failures = 0,
		    consecutive_successes = consecutive_successes + 1,
		    last_ok_at = ?, last_checked_at = ?, updated_at = ?
		WHERE account_id = ? AND model IN (?, '__all__')
		  AND status != 'healthy'`)
	_, err := r.db.sql.ExecContext(ctx, q, now, now, now, accountID, model)
	if err != nil {
		return fmt.Errorf("store: mark account healthy: %w", err)
	}
	return nil
}

// List returns health rows for a tenant.
func (r *HealthRepo) List(ctx context.Context, tenantID string) ([]AccountHealth, error) {
	q := r.db.rebind(`SELECT ` + accountHealthColumns + `
		FROM account_health WHERE tenant_id = ?
		ORDER BY provider, account_id, model`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list account health: %w", err)
	}
	defer rows.Close()
	var out []AccountHealth
	for rows.Next() {
		h, err := scanAccountHealth(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RecentSuccessfulAccountModels returns the account/model pairs that served a
// successful real request since the given time. Real traffic is the best
// health signal there is; the checker skips probing these pairs.
func (r *HealthRepo) RecentSuccessfulAccountModels(ctx context.Context, tenantID string, since time.Time) (map[string]time.Time, error) {
	q := r.db.rebind(`SELECT COALESCE(account_id, ''), model, MAX(created_at)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ? AND status = 'success'
		  AND COALESCE(account_id, '') != '' AND model != ''
		GROUP BY account_id, model`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: recent successful account models: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var accountID, model, at string
		if err := rows.Scan(&accountID, &model, &at); err != nil {
			return nil, err
		}
		out[accountID+"\x00"+model] = parseTime(at)
	}
	return out, rows.Err()
}

// RecentAccountModels returns recently used account/model pairs to health-check.
func (r *HealthRepo) RecentAccountModels(ctx context.Context, tenantID string, since time.Time, limit int) ([]AccountHealth, error) {
	if limit <= 0 {
		limit = 100
	}
	q := r.db.rebind(`SELECT COALESCE(account_id, ''), provider, model, MAX(created_at)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ? AND COALESCE(account_id, '') != '' AND model != ''
		GROUP BY account_id, provider, model
		ORDER BY MAX(created_at) DESC
		LIMIT ?`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent account models: %w", err)
	}
	defer rows.Close()
	var out []AccountHealth
	for rows.Next() {
		var h AccountHealth
		var ignored string
		if err := rows.Scan(&h.AccountID, &h.Provider, &h.Model, &ignored); err != nil {
			return nil, err
		}
		h.TenantID = tenantID
		out = append(out, h)
	}
	return out, rows.Err()
}

func scanAccountHealth(scan func(dest ...any) error) (AccountHealth, error) {
	var (
		h       AccountHealth
		lastOK  sql.NullString
		checked string
		updated string
	)
	if err := scan(
		&h.ID, &h.TenantID, &h.AccountID, &h.Provider, &h.Model, &h.Status,
		&h.LatencyMS, &h.ConsecutiveFailures, &h.ConsecutiveSuccesses, &lastOK,
		&checked, &h.LastError, &updated,
	); err != nil {
		return AccountHealth{}, err
	}
	if lastOK.Valid {
		t := parseTime(lastOK.String)
		h.LastOKAt = &t
	}
	h.LastCheckedAt = parseTime(checked)
	h.UpdatedAt = parseTime(updated)
	return h, nil
}
