package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
)

// PortalUserRepo persists portal_users bindings.
type PortalUserRepo struct{ db *DB }

// PortalUsers returns the portal user repository.
func (db *DB) PortalUsers() *PortalUserRepo { return &PortalUserRepo{db: db} }

// portalUserCols is the shared projection for portal user scans.
const portalUserCols = `google_sub, email, key_id, COALESCE(plan_id, ''), COALESCE(sealed_wrapped_dek, ''), COALESCE(sealed_ciphertext, ''), created_at, updated_at`

// sealedArgs returns the two sealed-column bind values, using NULL when the
// binding has no stored plaintext (manual claims).
func sealedArgs(u PortalUser) (any, any) {
	if u.SealedKey.WrappedDEK == "" || u.SealedKey.Ciphertext == "" {
		return nil, nil
	}
	return u.SealedKey.WrappedDEK, u.SealedKey.Ciphertext
}

// Upsert inserts or updates a portal user binding. It returns ErrAlreadyExists
// when the target key is already bound to a different Google subject.
func (r *PortalUserRepo) Upsert(ctx context.Context, u PortalUser) error {
	if existing, err := r.GetByKey(ctx, u.KeyID); err == nil && existing.GoogleSub != u.GoogleSub {
		return ErrAlreadyExists
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	now := time.Now()
	wrapped, ciphertext := sealedArgs(u)
	q := r.db.rebind(`INSERT INTO portal_users (google_sub, email, key_id, plan_id, sealed_wrapped_dek, sealed_ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (google_sub) DO UPDATE SET email = excluded.email, key_id = excluded.key_id, plan_id = excluded.plan_id, sealed_wrapped_dek = excluded.sealed_wrapped_dek, sealed_ciphertext = excluded.sealed_ciphertext, updated_at = excluded.updated_at`)
	var planID any
	if u.PlanID != "" {
		planID = u.PlanID
	}
	_, err := r.db.sql.ExecContext(ctx, q, u.GoogleSub, u.Email, u.KeyID, planID, wrapped, ciphertext, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("store: upsert portal user: %w", err)
	}
	return nil
}

// UpsertOnTx inserts or updates a portal user binding inside an existing
// transaction (used when provisioning a key). The uniqueness check on key_id
// is enforced by the DB constraint; on violation it returns ErrAlreadyExists.
func (r *PortalUserRepo) UpsertOnTx(ctx context.Context, tx *sql.Tx, u PortalUser) error {
	now := time.Now()
	wrapped, ciphertext := sealedArgs(u)
	q := r.db.rebind(`INSERT INTO portal_users (google_sub, email, key_id, plan_id, sealed_wrapped_dek, sealed_ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (google_sub) DO UPDATE SET email = excluded.email, key_id = excluded.key_id, plan_id = excluded.plan_id, sealed_wrapped_dek = excluded.sealed_wrapped_dek, sealed_ciphertext = excluded.sealed_ciphertext, updated_at = excluded.updated_at`)
	var planID any
	if u.PlanID != "" {
		planID = u.PlanID
	}
	if _, err := tx.ExecContext(ctx, q, u.GoogleSub, u.Email, u.KeyID, planID, wrapped, ciphertext, formatTime(now), formatTime(now)); err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("store: upsert portal user: %w", err)
	}
	return nil
}

// GetBySub returns the binding for a Google subject.
func (r *PortalUserRepo) GetBySub(ctx context.Context, sub string) (PortalUser, error) {
	q := r.db.rebind(`SELECT ` + portalUserCols + ` FROM portal_users WHERE google_sub = ?`)
	return r.scanRow(ctx, q, sub)
}

// GetByKey returns the binding for an API key id.
func (r *PortalUserRepo) GetByKey(ctx context.Context, keyID string) (PortalUser, error) {
	q := r.db.rebind(`SELECT ` + portalUserCols + ` FROM portal_users WHERE key_id = ?`)
	return r.scanRow(ctx, q, keyID)
}

// List returns every portal user binding, newest first.
func (r *PortalUserRepo) List(ctx context.Context) ([]PortalUser, error) {
	q := r.db.rebind(`SELECT ` + portalUserCols + ` FROM portal_users ORDER BY created_at DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list portal users: %w", err)
	}
	defer rows.Close()
	var out []PortalUser
	for rows.Next() {
		u, err := scanPortalUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPlanID updates the recorded plan for a binding (admin action).
func (r *PortalUserRepo) SetPlanID(ctx context.Context, sub, planID string) error {
	q := r.db.rebind(`UPDATE portal_users SET plan_id = ?, updated_at = ? WHERE google_sub = ?`)
	var pid any
	if planID != "" {
		pid = planID
	}
	res, err := r.db.sql.ExecContext(ctx, q, pid, formatTime(time.Now()), sub)
	if err != nil {
		return fmt.Errorf("store: set portal user plan: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetKeyID rebinds a user to a different key (admin rotate/replace). The
// stored plaintext is cleared because it no longer matches the new key.
func (r *PortalUserRepo) SetKeyID(ctx context.Context, sub, keyID string) error {
	if existing, err := r.GetByKey(ctx, keyID); err == nil && existing.GoogleSub != sub {
		return ErrAlreadyExists
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	q := r.db.rebind(`UPDATE portal_users SET key_id = ?, sealed_wrapped_dek = NULL, sealed_ciphertext = NULL, updated_at = ? WHERE google_sub = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, keyID, formatTime(time.Now()), sub)
	if err != nil {
		return fmt.Errorf("store: set portal user key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSealedKey replaces the stored encrypted plaintext for a binding. Used
// after a key rotation so a reveal returns the current key. Passing an empty
// Sealed clears it (the binding becomes masked-only).
func (r *PortalUserRepo) SetSealedKey(ctx context.Context, sub string, sealed crypto.Sealed) error {
	var wrapped, ciphertext any
	if sealed.WrappedDEK != "" && sealed.Ciphertext != "" {
		wrapped, ciphertext = sealed.WrappedDEK, sealed.Ciphertext
	}
	q := r.db.rebind(`UPDATE portal_users SET sealed_wrapped_dek = ?, sealed_ciphertext = ?, updated_at = ? WHERE google_sub = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, wrapped, ciphertext, formatTime(time.Now()), sub)
	if err != nil {
		return fmt.Errorf("store: set portal user sealed key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a binding (revocation).
func (r *PortalUserRepo) Delete(ctx context.Context, sub string) error {
	q := r.db.rebind(`DELETE FROM portal_users WHERE google_sub = ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, sub); err != nil {
		return fmt.Errorf("store: delete portal user: %w", err)
	}
	return nil
}

func (r *PortalUserRepo) scanRow(ctx context.Context, q, arg string) (PortalUser, error) {
	row := r.db.sql.QueryRowContext(ctx, q, arg)
	u, err := scanPortalUser(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return PortalUser{}, ErrNotFound
	}
	if err != nil {
		return PortalUser{}, err
	}
	return u, nil
}

// scanPortalUser scans the shared portal user projection.
func scanPortalUser(scan func(...any) error) (PortalUser, error) {
	var (
		u                PortalUser
		wrapped, cipher  string
		created, updated string
	)
	if err := scan(&u.GoogleSub, &u.Email, &u.KeyID, &u.PlanID, &wrapped, &cipher, &created, &updated); err != nil {
		return PortalUser{}, err
	}
	u.SealedKey = crypto.Sealed{WrappedDEK: wrapped, Ciphertext: cipher}
	u.CreatedAt, u.UpdatedAt = parseTime(created), parseTime(updated)
	return u, nil
}
