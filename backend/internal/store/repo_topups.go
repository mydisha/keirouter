package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// KeyTopupRepo persists append-only API key budget top-ups.
type KeyTopupRepo struct{ db *DB }

// Topups returns the key top-up repository.
func (db *DB) Topups() *KeyTopupRepo { return &KeyTopupRepo{db: db} }

const keyTopupSelectCols = `id, tenant_id, key_id, budget_id, amount_micros, reason,
	limit_before_micros, limit_after_micros, idempotency_key, actor, created_at`

// Create inserts a top-up record.
func (r *KeyTopupRepo) Create(ctx context.Context, t KeyTopup) error {
	return r.insert(ctx, r.db.sql, t)
}

// CreateOnTx inserts a top-up record within an existing transaction.
func (r *KeyTopupRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, t KeyTopup) error {
	return r.insert(ctx, tx, t)
}

func (r *KeyTopupRepo) insert(ctx context.Context, ex sqlExec, t KeyTopup) error {
	q := r.db.rebind(`INSERT INTO key_topups
		(id, tenant_id, key_id, budget_id, amount_micros, reason, limit_before_micros,
		 limit_after_micros, idempotency_key, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q, t.ID, t.TenantID, t.KeyID, t.BudgetID, t.AmountMicros,
		t.Reason, t.LimitBeforeMicros, t.LimitAfterMicros, t.IdempotencyKey, t.Actor,
		formatTime(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: create key topup: %w", err)
	}
	return nil
}

// ListByKey returns a key's top-ups, newest first.
func (r *KeyTopupRepo) ListByKey(ctx context.Context, keyID string) ([]KeyTopup, error) {
	q := r.db.rebind(`SELECT ` + keyTopupSelectCols + ` FROM key_topups WHERE key_id = ? ORDER BY created_at DESC, id DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, keyID)
	if err != nil {
		return nil, fmt.Errorf("store: list key topups: %w", err)
	}
	defer rows.Close()

	var out []KeyTopup
	for rows.Next() {
		t, err := scanKeyTopup(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetByIdempotencyKey returns the top-up matching (keyID, idem), or ErrNotFound.
func (r *KeyTopupRepo) GetByIdempotencyKey(ctx context.Context, keyID, idem string) (KeyTopup, error) {
	q := r.db.rebind(`SELECT ` + keyTopupSelectCols + ` FROM key_topups WHERE key_id = ? AND idempotency_key = ?`)
	t, err := scanKeyTopup(r.db.sql.QueryRowContext(ctx, q, keyID, idem).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyTopup{}, ErrNotFound
	}
	return t, err
}

func scanKeyTopup(scan func(dest ...any) error) (KeyTopup, error) {
	var (
		t       KeyTopup
		created string
	)
	err := scan(&t.ID, &t.TenantID, &t.KeyID, &t.BudgetID, &t.AmountMicros, &t.Reason,
		&t.LimitBeforeMicros, &t.LimitAfterMicros, &t.IdempotencyKey, &t.Actor, &created)
	if err != nil {
		return KeyTopup{}, err
	}
	t.CreatedAt = parseTime(created)
	return t, nil
}
