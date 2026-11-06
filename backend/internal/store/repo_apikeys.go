package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("store: not found")

// sqlExec abstracts *sql.DB and *sql.Tx so repository helpers can run on
// either a direct connection or inside an open transaction.
type sqlExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// APIKeyRepo persists inbound API keys.
type APIKeyRepo struct{ db *DB }

// APIKeys returns the API key repository.
func (db *DB) APIKeys() *APIKeyRepo { return &APIKeyRepo{db: db} }

// Create inserts a new API key record.
func (r *APIKeyRepo) Create(ctx context.Context, k APIKey) error {
	return r.insert(ctx, r.db.sql, k)
}

// CreateOnTx inserts a key within an existing transaction.
func (r *APIKeyRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, k APIKey) error {
	return r.insert(ctx, tx, k)
}

func (r *APIKeyRepo) insert(ctx context.Context, ex sqlExec, k APIKey) error {
	q := r.db.rebind(`
		INSERT INTO api_keys
			(id, tenant_id, project_id, plan_id, name, key_hash, lookup_hash, lookup_hmac, lookup_kid,
			 display, scopes, disabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q,
		k.ID, k.TenantID, nullString(k.ProjectID), k.PlanID, k.Name, k.KeyHash, k.LookupHash,
		nullString(k.LookupHMAC), k.LookupKID,
		k.Display, k.Scopes, boolToInt(k.Disabled), formatTime(k.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: create api key: %w", err)
	}
	return nil
}

// apiKeyColumns is the column list every api_keys read scans, in scan order.
const apiKeyColumns = `id, tenant_id, project_id, plan_id, name, key_hash, lookup_hash, lookup_hmac, lookup_kid,
	display, scopes, disabled, last_used_at, created_at`

// FindByLookupHMAC returns the key whose keyed lookup index (crypto.KeyLookup)
// matches. The index is a 256-bit keyed hash of a 192-bit random key, so a
// match authenticates the key without further verification.
func (r *APIKeyRepo) FindByLookupHMAC(ctx context.Context, lookup string) (APIKey, error) {
	q := r.db.rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE lookup_hmac = ?`)
	return r.scanOne(r.db.sql.QueryRowContext(ctx, q, lookup))
}

// FindByLookup returns the key matching the legacy sha-256 lookup index. It
// covers rows not yet given a keyed index, such as a key minted by a replica
// still running an older release during a rolling deploy.
func (r *APIKeyRepo) FindByLookup(ctx context.Context, lookup string) (APIKey, error) {
	q := r.db.rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE lookup_hash = ?`)
	return r.scanOne(r.db.sql.QueryRowContext(ctx, q, lookup))
}

// LookupIndexUpdate assigns a row its keyed lookup index.
type LookupIndexUpdate struct {
	ID   string
	HMAC string
	KID  string
}

// SetLookupHMAC records a row's keyed lookup index. It reports whether the
// row changed; writing the value a row already has is a no-op, so replicas
// that reconcile the same row concurrently do not conflict.
func (r *APIKeyRepo) SetLookupHMAC(ctx context.Context, u LookupIndexUpdate) (bool, error) {
	return r.setLookupHMAC(ctx, r.db.sql, u)
}

// SetLookupHMACs applies several updates in one transaction and returns how
// many rows changed.
func (r *APIKeyRepo) SetLookupHMACs(ctx context.Context, updates []LookupIndexUpdate) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: set lookup index begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	changed := 0
	for _, u := range updates {
		ok, err := r.setLookupHMAC(ctx, tx, u)
		if err != nil {
			return 0, err
		}
		if ok {
			changed++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: set lookup index commit: %w", err)
	}
	return changed, nil
}

func (r *APIKeyRepo) setLookupHMAC(ctx context.Context, ex sqlExec, u LookupIndexUpdate) (bool, error) {
	q := r.db.rebind(`
		UPDATE api_keys SET lookup_hmac = ?, lookup_kid = ?
		WHERE id = ? AND (lookup_hmac IS NULL OR lookup_hmac <> ? OR lookup_kid <> ?)`)
	res, err := ex.ExecContext(ctx, q, u.HMAC, u.KID, u.ID, u.HMAC, u.KID)
	if err != nil {
		return false, fmt.Errorf("store: set lookup index: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: set lookup index: %w", err)
	}
	return n > 0, nil
}

// PendingLookup is a row whose keyed lookup index is missing or was computed
// under another lookup key.
type PendingLookup struct {
	ID         string
	LookupHash string // legacy sha-256 index
	LookupHMAC string // empty when missing
	LookupKID  string
}

// ListPendingLookups returns up to limit rows, ordered by id and starting
// after afterID, whose keyed index is missing or not under kid. Paging by id
// lets a caller skip rows it cannot convert without revisiting them.
func (r *APIKeyRepo) ListPendingLookups(ctx context.Context, kid, afterID string, limit int) ([]PendingLookup, error) {
	q := r.db.rebind(`
		SELECT id, lookup_hash, lookup_hmac, lookup_kid FROM api_keys
		WHERE (lookup_hmac IS NULL OR lookup_kid <> ?) AND id > ?
		ORDER BY id LIMIT ?`)
	rows, err := r.db.sql.QueryContext(ctx, q, kid, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list pending lookups: %w", err)
	}
	defer rows.Close()

	var out []PendingLookup
	for rows.Next() {
		var (
			p    PendingLookup
			hmac sql.NullString
		)
		if err := rows.Scan(&p.ID, &p.LookupHash, &hmac, &p.LookupKID); err != nil {
			return nil, fmt.Errorf("store: scan pending lookup: %w", err)
		}
		p.LookupHMAC = hmac.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns a single API key by id.
func (r *APIKeyRepo) Get(ctx context.Context, id string) (APIKey, error) {
	q := r.db.rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE id = ?`)
	return r.scanOne(r.db.sql.QueryRowContext(ctx, q, id))
}

// List returns all keys for a tenant, newest first.
func (r *APIKeyRepo) List(ctx context.Context, tenantID string) ([]APIKey, error) {
	q := r.db.rebind(`SELECT ` + apiKeyColumns + ` FROM api_keys WHERE tenant_id = ? ORDER BY created_at DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list api keys: %w", err)
	}
	defer rows.Close()

	var out []APIKey
	for rows.Next() {
		k, err := r.scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// TouchLastUsed records that a key authenticated a request.
func (r *APIKeyRepo) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	q := r.db.rebind(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, formatTime(at), id)
	return err
}

// SetDisabled enables or disables a key.
func (r *APIKeyRepo) SetDisabled(ctx context.Context, id string, disabled bool) error {
	q := r.db.rebind(`UPDATE api_keys SET disabled = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, boolToInt(disabled), id)
	return err
}

// Delete removes a key.
func (r *APIKeyRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM api_keys WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, id)
	return err
}

func (r *APIKeyRepo) scanOne(row *sql.Row) (APIKey, error) {
	k, err := scanAPIKey(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	return k, err
}

func (r *APIKeyRepo) scanRows(rows *sql.Rows) (APIKey, error) {
	return scanAPIKey(rows.Scan)
}

// scanAPIKey scans one row selected with apiKeyColumns.
func scanAPIKey(scan func(dest ...any) error) (APIKey, error) {
	var (
		k          APIKey
		projectID  sql.NullString
		planID     sql.NullString
		lookupHMAC sql.NullString
		lastUsed   sql.NullString
		disabled   int
		createdRaw string
	)
	err := scan(&k.ID, &k.TenantID, &projectID, &planID, &k.Name, &k.KeyHash, &k.LookupHash,
		&lookupHMAC, &k.LookupKID, &k.Display, &k.Scopes, &disabled, &lastUsed, &createdRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, err
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("store: scan api key: %w", err)
	}
	k.ProjectID = projectID.String
	k.PlanID = planID.String
	k.LookupHMAC = lookupHMAC.String
	k.Disabled = disabled != 0
	k.CreatedAt = parseTime(createdRaw)
	if lastUsed.Valid {
		t := parseTime(lastUsed.String)
		k.LastUsedAt = &t
	}
	return k, nil
}

// SetPlanID updates the plan assignment for a key.
func (r *APIKeyRepo) SetPlanID(ctx context.Context, id string, planID string) error {
	q := r.db.rebind(`UPDATE api_keys SET plan_id = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, planID, id)
	return err
}

// ---- per-key model access ---------------------------------------------------

// SetAllowedModels replaces the allowed-models list for an API key. An empty
// slice removes all restrictions (all models allowed).
func (r *APIKeyRepo) SetAllowedModels(ctx context.Context, keyID string, models []string) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set allowed models begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	dq := r.db.rebind(`DELETE FROM api_key_model_access WHERE api_key_id = ?`)
	if _, err := tx.ExecContext(ctx, dq, keyID); err != nil {
		return fmt.Errorf("store: set allowed models delete: %w", err)
	}

	if len(models) > 0 {
		now := formatTime(time.Now())
		iq := r.db.rebind(`INSERT INTO api_key_model_access (api_key_id, model, created_at) VALUES (?, ?, ?)`)
		for _, m := range models {
			if _, err := tx.ExecContext(ctx, iq, keyID, m, now); err != nil {
				return fmt.Errorf("store: set allowed models insert: %w", err)
			}
		}
	}
	return tx.Commit()
}

// GetAllowedModels returns the models allowed for a key. Empty slice means no
// restriction (all models permitted).
func (r *APIKeyRepo) GetAllowedModels(ctx context.Context, keyID string) ([]string, error) {
	q := r.db.rebind(`SELECT model FROM api_key_model_access WHERE api_key_id = ? ORDER BY model`)
	rows, err := r.db.sql.QueryContext(ctx, q, keyID)
	if err != nil {
		return nil, fmt.Errorf("store: get allowed models: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// IsModelAllowed reports whether a model is permitted for the given key. When
// no restriction rows exist, all models are allowed. Supports prefix wildcard
// matching: a stored pattern "claude-*" matches "claude-opus-4-6".
func (r *APIKeyRepo) IsModelAllowed(ctx context.Context, keyID string, model string) (bool, error) {
	allowed, err := r.GetAllowedModels(ctx, keyID)
	if err != nil {
		return false, err
	}
	if len(allowed) == 0 {
		return true, nil // no restriction
	}
	lower := strings.ToLower(model)
	for _, pattern := range allowed {
		lp := strings.ToLower(pattern)
		if strings.HasSuffix(lp, "*") {
			prefix := lp[:len(lp)-1]
			if strings.HasPrefix(lower, prefix) {
				return true, nil
			}
		} else if lp == lower {
			return true, nil
		}
	}
	return false, nil
}

// SetAllowedModelsOnTx replaces allowed models within an existing transaction.
func (r *APIKeyRepo) SetAllowedModelsOnTx(ctx context.Context, tx *sql.Tx, keyID string, models []string) error {
	dq := r.db.rebind(`DELETE FROM api_key_model_access WHERE api_key_id = ?`)
	if _, err := tx.ExecContext(ctx, dq, keyID); err != nil {
		return fmt.Errorf("store: set allowed models delete: %w", err)
	}
	if len(models) > 0 {
		now := formatTime(time.Now())
		iq := r.db.rebind(`INSERT INTO api_key_model_access (api_key_id, model, created_at) VALUES (?, ?, ?)`)
		for _, m := range models {
			if _, err := tx.ExecContext(ctx, iq, keyID, m, now); err != nil {
				return fmt.Errorf("store: set allowed models insert: %w", err)
			}
		}
	}
	return nil
}
