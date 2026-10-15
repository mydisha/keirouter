package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpstreamRepo persists upstream provider accounts, their provider mapping
// and top-ups for the finance dashboard.
type UpstreamRepo struct{ db *DB }

// Upstream returns the upstream finance repository.
func (db *DB) Upstream() *UpstreamRepo { return &UpstreamRepo{db: db} }

const upstreamAccountColumns = `id, name, url, status, opening_balance_micros, opening_at,
	low_balance_micros, alert_fired_at, created_at, updated_at`

const upstreamTopupColumns = `id, account_id, amount_micros, topup_at, note, actor, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanUpstreamAccount(s rowScanner) (UpstreamAccount, error) {
	var a UpstreamAccount
	var opening, fired, created, updated string
	if err := s.Scan(&a.ID, &a.Name, &a.URL, &a.Status, &a.OpeningBalanceMicros, &opening,
		&a.LowBalanceMicros, &fired, &created, &updated); err != nil {
		return a, err
	}
	a.OpeningAt = parseTime(opening)
	a.AlertFiredAt = parseTime(fired)
	a.CreatedAt = parseTime(created)
	a.UpdatedAt = parseTime(updated)
	return a, nil
}

// providerMap returns provider ids keyed by account id, sorted by provider id.
func (r *UpstreamRepo) providerMap(ctx context.Context) (map[string][]string, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT account_id, provider_id FROM upstream_account_providers ORDER BY provider_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list upstream providers: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var acc, prov string
		if err := rows.Scan(&acc, &prov); err != nil {
			return nil, err
		}
		out[acc] = append(out[acc], prov)
	}
	return out, rows.Err()
}

// List returns all upstream accounts, active first, then by name.
func (r *UpstreamRepo) List(ctx context.Context) ([]UpstreamAccount, error) {
	q := `SELECT ` + upstreamAccountColumns + ` FROM upstream_accounts
		ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END, name`
	rows, err := r.db.sql.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list upstream accounts: %w", err)
	}
	var out []UpstreamAccount
	for rows.Next() {
		a, err := scanUpstreamAccount(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	provs, err := r.providerMap(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Providers = provs[out[i].ID]
	}
	return out, nil
}

// Get returns one upstream account or ErrNotFound.
func (r *UpstreamRepo) Get(ctx context.Context, id string) (UpstreamAccount, error) {
	q := r.db.rebind(`SELECT ` + upstreamAccountColumns + ` FROM upstream_accounts WHERE id = ?`)
	a, err := scanUpstreamAccount(r.db.sql.QueryRowContext(ctx, q, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UpstreamAccount{}, ErrNotFound
		}
		return UpstreamAccount{}, fmt.Errorf("store: get upstream account: %w", err)
	}
	q = r.db.rebind(`SELECT provider_id FROM upstream_account_providers WHERE account_id = ? ORDER BY provider_id`)
	rows, err := r.db.sql.QueryContext(ctx, q, id)
	if err != nil {
		return a, fmt.Errorf("store: get upstream providers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return a, err
		}
		a.Providers = append(a.Providers, p)
	}
	return a, rows.Err()
}

// Create inserts an upstream account.
func (r *UpstreamRepo) Create(ctx context.Context, a UpstreamAccount) error {
	now := formatTime(time.Now())
	q := r.db.rebind(`INSERT INTO upstream_accounts (` + upstreamAccountColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q, a.ID, a.Name, a.URL, a.Status,
		a.OpeningBalanceMicros, formatTime(a.OpeningAt), a.LowBalanceMicros,
		formatTime(a.AlertFiredAt), now, now)
	if err != nil {
		return fmt.Errorf("store: create upstream account: %w", err)
	}
	return nil
}

// Update changes an account's name, url, status, opening balance and threshold.
func (r *UpstreamRepo) Update(ctx context.Context, a UpstreamAccount) error {
	q := r.db.rebind(`UPDATE upstream_accounts SET name = ?, url = ?, status = ?,
		opening_balance_micros = ?, opening_at = ?, low_balance_micros = ?, updated_at = ?
		WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, a.Name, a.URL, a.Status, a.OpeningBalanceMicros,
		formatTime(a.OpeningAt), a.LowBalanceMicros, formatTime(time.Now()), a.ID)
	if err != nil {
		return fmt.Errorf("store: update upstream account: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetProviders replaces the account's provider set. A provider already mapped
// to another account is moved here.
func (r *UpstreamRepo) SetProviders(ctx context.Context, accountID string, providers []string) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set upstream providers: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		r.db.rebind(`DELETE FROM upstream_account_providers WHERE account_id = ?`), accountID); err != nil {
		return fmt.Errorf("store: set upstream providers: %w", err)
	}
	up := r.db.rebind(`INSERT INTO upstream_account_providers (provider_id, account_id) VALUES (?, ?)
		ON CONFLICT (provider_id) DO UPDATE SET account_id = excluded.account_id`)
	for _, p := range providers {
		if _, err := tx.ExecContext(ctx, up, p, accountID); err != nil {
			return fmt.Errorf("store: set upstream providers: %w", err)
		}
	}
	return tx.Commit()
}

// SetAlertFiredAt records when the low-balance alert fired; zero re-arms it.
func (r *UpstreamRepo) SetAlertFiredAt(ctx context.Context, id string, t time.Time) error {
	q := r.db.rebind(`UPDATE upstream_accounts SET alert_fired_at = ? WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, formatTime(t), id)
	if err != nil {
		return fmt.Errorf("store: set upstream alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddTopup records a top-up.
func (r *UpstreamRepo) AddTopup(ctx context.Context, t UpstreamTopup) error {
	q := r.db.rebind(`INSERT INTO upstream_topups (` + upstreamTopupColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q, t.ID, t.AccountID, t.AmountMicros,
		formatTime(t.TopupAt), t.Note, t.Actor, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: add upstream topup: %w", err)
	}
	return nil
}

// ListTopups returns top-ups newest first; an empty accountID returns all.
func (r *UpstreamRepo) ListTopups(ctx context.Context, accountID string) ([]UpstreamTopup, error) {
	q := `SELECT ` + upstreamTopupColumns + ` FROM upstream_topups`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	q += ` ORDER BY topup_at DESC, created_at DESC`
	rows, err := r.db.sql.QueryContext(ctx, r.db.rebind(q), args...)
	if err != nil {
		return nil, fmt.Errorf("store: list upstream topups: %w", err)
	}
	defer rows.Close()
	var out []UpstreamTopup
	for rows.Next() {
		var t UpstreamTopup
		var at, created string
		if err := rows.Scan(&t.ID, &t.AccountID, &t.AmountMicros, &at, &t.Note, &t.Actor, &created); err != nil {
			return nil, err
		}
		t.TopupAt = parseTime(at)
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTopup removes a top-up by id.
func (r *UpstreamRepo) DeleteTopup(ctx context.Context, id string) error {
	res, err := r.db.sql.ExecContext(ctx, r.db.rebind(`DELETE FROM upstream_topups WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("store: delete upstream topup: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TopupTotals returns, per account, the sum of top-ups made at or after the
// account's opening_at.
func (r *UpstreamRepo) TopupTotals(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT t.account_id, SUM(t.amount_micros)
		FROM upstream_topups t JOIN upstream_accounts a ON a.id = t.account_id
		WHERE t.topup_at >= a.opening_at GROUP BY t.account_id`)
	if err != nil {
		return nil, fmt.Errorf("store: upstream topup totals: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var sum int64
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, err
		}
		out[id] = sum
	}
	return out, rows.Err()
}
