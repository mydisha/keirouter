package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProxyPool is a named proxy endpoint that can be bound to provider accounts.
type ProxyPool struct {
	ID         string
	Name       string
	Type       string // http | vercel | cloudflare | deno
	ProxyURL   string
	NoProxy    string
	Strict     bool
	IsActive   bool
	TestStatus string // unknown | testing | active | error
	LastTested *time.Time
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ProxyPoolRepo persists proxy pools.
type ProxyPoolRepo struct{ db *DB }

// ProxyPools returns the proxy pool repository.
func (db *DB) ProxyPools() *ProxyPoolRepo { return &ProxyPoolRepo{db: db} }

const poolColumns = `id, name, type, proxy_url, no_proxy, strict,
	is_active, test_status, last_tested, last_error, created_at, updated_at`

// Get returns one proxy pool by id.
func (r *ProxyPoolRepo) Get(ctx context.Context, id string) (ProxyPool, error) {
	q := r.db.rebind("SELECT " + poolColumns + " FROM proxy_pools WHERE id = ?")
	row := r.db.sql.QueryRowContext(ctx, q, id)
	p, err := scanPoolRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ProxyPool{}, ErrNotFound
	}
	return p, err
}

// List returns all proxy pools.
func (r *ProxyPoolRepo) List(ctx context.Context) ([]ProxyPool, error) {
	rows, err := r.db.sql.QueryContext(ctx, "SELECT "+poolColumns+" FROM proxy_pools ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("store: list pools: %w", err)
	}
	defer rows.Close()

	var out []ProxyPool
	for rows.Next() {
		p, err := scanPoolRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Create inserts a new proxy pool.
func (r *ProxyPoolRepo) Create(ctx context.Context, p ProxyPool) error {
	q := r.db.rebind(`INSERT INTO proxy_pools (` + poolColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q,
		p.ID, p.Name, p.Type, p.ProxyURL, p.NoProxy, boolToInt(p.Strict),
		boolToInt(p.IsActive), p.TestStatus, nullTime(p.LastTested), p.LastError,
		formatTime(p.CreatedAt), formatTime(p.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: create pool: %w", err)
	}
	return nil
}

// Update writes changes to a proxy pool.
func (r *ProxyPoolRepo) Update(ctx context.Context, p ProxyPool) error {
	q := r.db.rebind(`UPDATE proxy_pools SET
		name = ?, type = ?, proxy_url = ?, no_proxy = ?, strict = ?,
		is_active = ?, test_status = ?, last_tested = ?, last_error = ?,
		updated_at = ?
		WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q,
		p.Name, p.Type, p.ProxyURL, p.NoProxy, boolToInt(p.Strict),
		boolToInt(p.IsActive), p.TestStatus, nullTime(p.LastTested), p.LastError,
		formatTime(time.Now()), p.ID)
	return err
}

// RecordTest stores a connectivity-test outcome without touching the pool's
// operator-managed fields (name, URL, bypass list, strict). Tests run for
// seconds, so a full-row Update from a stale copy would silently revert any
// edit saved in the meantime. activate, when non-nil, also sets is_active;
// only the relay readiness monitor uses it to enable a freshly deployed relay.
func (r *ProxyPoolRepo) RecordTest(ctx context.Context, id, status string, tested time.Time, lastError string, activate *bool) error {
	now := formatTime(time.Now())
	if activate != nil {
		q := r.db.rebind(`UPDATE proxy_pools SET test_status = ?, last_tested = ?, last_error = ?,
			is_active = ?, updated_at = ? WHERE id = ?`)
		_, err := r.db.sql.ExecContext(ctx, q, status, formatTime(tested), lastError, boolToInt(*activate), now, id)
		return err
	}
	q := r.db.rebind(`UPDATE proxy_pools SET test_status = ?, last_tested = ?, last_error = ?,
		updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, status, formatTime(tested), lastError, now, id)
	return err
}

// Delete removes a proxy pool.
func (r *ProxyPoolRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind("DELETE FROM proxy_pools WHERE id = ?")
	_, err := r.db.sql.ExecContext(ctx, q, id)
	return err
}

func scanPoolRow(scan func(dest ...any) error) (ProxyPool, error) {
	var (
		p          ProxyPool
		strict     int
		active     int
		lastTested sql.NullString
		lastError  sql.NullString
		createdRaw string
		updatedRaw string
	)
	err := scan(
		&p.ID, &p.Name, &p.Type, &p.ProxyURL, &p.NoProxy, &strict,
		&active, &p.TestStatus, &lastTested, &lastError,
		&createdRaw, &updatedRaw,
	)
	if err != nil {
		return ProxyPool{}, err
	}
	p.Strict = strict != 0
	p.IsActive = active != 0
	p.LastError = lastError.String
	p.CreatedAt = parseTime(createdRaw)
	p.UpdatedAt = parseTime(updatedRaw)
	if lastTested.Valid {
		t := parseTime(lastTested.String)
		p.LastTested = &t
	}
	return p, nil
}
