package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PortalUserRepo persists portal_users bindings.
type PortalUserRepo struct{ db *DB }

// PortalUsers returns the portal user repository.
func (db *DB) PortalUsers() *PortalUserRepo { return &PortalUserRepo{db: db} }

// Upsert inserts or updates a portal user binding. It returns ErrAlreadyExists
// when the target key is already bound to a different Google subject.
func (r *PortalUserRepo) Upsert(ctx context.Context, u PortalUser) error {
	if existing, err := r.GetByKey(ctx, u.KeyID); err == nil && existing.GoogleSub != u.GoogleSub {
		return ErrAlreadyExists
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	now := time.Now()
	q := r.db.rebind(`INSERT INTO portal_users (google_sub, email, key_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (google_sub) DO UPDATE SET email = excluded.email, key_id = excluded.key_id, updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, u.GoogleSub, u.Email, u.KeyID, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("store: upsert portal user: %w", err)
	}
	return nil
}

// GetBySub returns the binding for a Google subject.
func (r *PortalUserRepo) GetBySub(ctx context.Context, sub string) (PortalUser, error) {
	q := r.db.rebind(`SELECT google_sub, email, key_id, created_at, updated_at FROM portal_users WHERE google_sub = ?`)
	return r.scanRow(ctx, q, sub)
}

// GetByKey returns the binding for an API key id.
func (r *PortalUserRepo) GetByKey(ctx context.Context, keyID string) (PortalUser, error) {
	q := r.db.rebind(`SELECT google_sub, email, key_id, created_at, updated_at FROM portal_users WHERE key_id = ?`)
	return r.scanRow(ctx, q, keyID)
}

func (r *PortalUserRepo) scanRow(ctx context.Context, q, arg string) (PortalUser, error) {
	var (
		u                PortalUser
		created, updated string
	)
	err := r.db.sql.QueryRowContext(ctx, q, arg).Scan(&u.GoogleSub, &u.Email, &u.KeyID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PortalUser{}, ErrNotFound
	}
	if err != nil {
		return PortalUser{}, fmt.Errorf("store: get portal user: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = parseTime(created), parseTime(updated)
	return u, nil
}

// Delete removes a binding (revocation).
func (r *PortalUserRepo) Delete(ctx context.Context, sub string) error {
	q := r.db.rebind(`DELETE FROM portal_users WHERE google_sub = ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, sub); err != nil {
		return fmt.Errorf("store: delete portal user: %w", err)
	}
	return nil
}
