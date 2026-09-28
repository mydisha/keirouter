package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// KeyLimitAdjustmentRepo persists append-only manual budget limit corrections.
type KeyLimitAdjustmentRepo struct{ db *DB }

// LimitAdjustments returns the key limit adjustment repository.
func (db *DB) LimitAdjustments() *KeyLimitAdjustmentRepo {
	return &KeyLimitAdjustmentRepo{db: db}
}

const keyLimitAdjustmentSelectCols = `id, tenant_id, key_id, budget_id, delta_micros, reason,
	limit_before_micros, limit_after_micros, idempotency_key, actor, created_at`

// Create inserts an adjustment record.
func (r *KeyLimitAdjustmentRepo) Create(ctx context.Context, a KeyLimitAdjustment) error {
	return r.insert(ctx, r.db.sql, a)
}

// CreateOnTx inserts an adjustment record within an existing transaction.
func (r *KeyLimitAdjustmentRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, a KeyLimitAdjustment) error {
	return r.insert(ctx, tx, a)
}

func (r *KeyLimitAdjustmentRepo) insert(ctx context.Context, ex sqlExec, a KeyLimitAdjustment) error {
	q := r.db.rebind(`INSERT INTO key_limit_adjustments
		(id, tenant_id, key_id, budget_id, delta_micros, reason, limit_before_micros,
		 limit_after_micros, idempotency_key, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q, a.ID, a.TenantID, a.KeyID, a.BudgetID, a.DeltaMicros,
		a.Reason, a.LimitBeforeMicros, a.LimitAfterMicros, a.IdempotencyKey, a.Actor,
		formatTime(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: create key limit adjustment: %w", err)
	}
	return nil
}

// ListByKey returns a key's limit adjustments, newest first. Unlike top-ups the
// delta can be negative, so limit_after_micros is not monotonic and cannot be
// used as an insertion-order tiebreak. created_at is second precision, so id is
// used instead: the order is deterministic across runs, though two adjustments
// in the same second may not render in insertion order.
func (r *KeyLimitAdjustmentRepo) ListByKey(ctx context.Context, keyID string) ([]KeyLimitAdjustment, error) {
	q := r.db.rebind(`SELECT ` + keyLimitAdjustmentSelectCols + ` FROM key_limit_adjustments WHERE key_id = ? ORDER BY created_at DESC, id DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, keyID)
	if err != nil {
		return nil, fmt.Errorf("store: list key limit adjustments: %w", err)
	}
	defer rows.Close()

	var out []KeyLimitAdjustment
	for rows.Next() {
		a, err := scanKeyLimitAdjustment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetByIdempotencyKey returns the adjustment matching (keyID, idem), or ErrNotFound.
func (r *KeyLimitAdjustmentRepo) GetByIdempotencyKey(ctx context.Context, keyID, idem string) (KeyLimitAdjustment, error) {
	q := r.db.rebind(`SELECT ` + keyLimitAdjustmentSelectCols + ` FROM key_limit_adjustments WHERE key_id = ? AND idempotency_key = ?`)
	a, err := scanKeyLimitAdjustment(r.db.sql.QueryRowContext(ctx, q, keyID, idem).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyLimitAdjustment{}, ErrNotFound
	}
	return a, err
}

func scanKeyLimitAdjustment(scan func(dest ...any) error) (KeyLimitAdjustment, error) {
	var (
		a       KeyLimitAdjustment
		created string
	)
	err := scan(&a.ID, &a.TenantID, &a.KeyID, &a.BudgetID, &a.DeltaMicros, &a.Reason,
		&a.LimitBeforeMicros, &a.LimitAfterMicros, &a.IdempotencyKey, &a.Actor, &created)
	if err != nil {
		return KeyLimitAdjustment{}, err
	}
	a.CreatedAt = parseTime(created)
	return a, nil
}
