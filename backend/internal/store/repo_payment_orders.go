package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PaymentOrderRepo persists payment-gateway orders.
type PaymentOrderRepo struct{ db *DB }

// PaymentOrders returns the payment order repository.
func (db *DB) PaymentOrders() *PaymentOrderRepo { return &PaymentOrderRepo{db: db} }

const paymentOrderCols = `id, tenant_id, key_id, google_sub, amount_idr, credit_micros,
	fx_rate_micros, status, provider, provider_payment_id, payment_link_url,
	idempotency_key, actor, reason, created_at, updated_at, paid_at, expires_at,
	product, hosting_instance_id, terms_version, terms_accepted_at`

// Create inserts a payment order.
func (r *PaymentOrderRepo) Create(ctx context.Context, o PaymentOrder) error {
	return r.insert(ctx, r.db.sql, o)
}

// CreateOnTx inserts a payment order within a transaction.
func (r *PaymentOrderRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, o PaymentOrder) error {
	return r.insert(ctx, tx, o)
}

func (r *PaymentOrderRepo) insert(ctx context.Context, ex sqlExec, o PaymentOrder) error {
	product := o.Product
	if product == "" {
		product = ProductCredit
	}
	q := r.db.rebind(`INSERT INTO payment_orders (` + paymentOrderCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q,
		o.ID, o.TenantID, o.KeyID, o.GoogleSub, o.AmountIDR, o.CreditMicros,
		o.FxRateMicros, string(o.Status), o.Provider, o.ProviderPaymentID,
		o.PaymentLinkURL, o.IdempotencyKey, o.Actor, o.Reason,
		formatTime(o.CreatedAt), formatTime(o.UpdatedAt), o.PaidAt, o.ExpiresAt,
		product, o.HostingInstanceID, o.TermsVersion, o.TermsAcceptedAt)
	if err != nil {
		return fmt.Errorf("store: create payment order: %w", err)
	}
	return nil
}

// Get returns one order by id.
func (r *PaymentOrderRepo) Get(ctx context.Context, id string) (PaymentOrder, error) {
	q := r.db.rebind(`SELECT ` + paymentOrderCols + ` FROM payment_orders WHERE id = ?`)
	o, err := scanPaymentOrder(r.db.sql.QueryRowContext(ctx, q, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentOrder{}, ErrNotFound
	}
	return o, err
}

// GetByProviderPaymentID returns the order matching a gateway payment id.
func (r *PaymentOrderRepo) GetByProviderPaymentID(ctx context.Context, provider, id string) (PaymentOrder, error) {
	q := r.db.rebind(`SELECT ` + paymentOrderCols + ` FROM payment_orders WHERE provider = ? AND provider_payment_id = ?`)
	o, err := scanPaymentOrder(r.db.sql.QueryRowContext(ctx, q, provider, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentOrder{}, ErrNotFound
	}
	return o, err
}

// GetByIdempotencyKey returns the order matching a client idempotency key.
func (r *PaymentOrderRepo) GetByIdempotencyKey(ctx context.Context, idem string) (PaymentOrder, error) {
	q := r.db.rebind(`SELECT ` + paymentOrderCols + ` FROM payment_orders WHERE idempotency_key = ?`)
	o, err := scanPaymentOrder(r.db.sql.QueryRowContext(ctx, q, idem).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentOrder{}, ErrNotFound
	}
	return o, err
}

// ListByKey returns a key's orders, newest first.
func (r *PaymentOrderRepo) ListByKey(ctx context.Context, keyID string) ([]PaymentOrder, error) {
	q := r.db.rebind(`SELECT ` + paymentOrderCols + ` FROM payment_orders WHERE key_id = ? ORDER BY created_at DESC, id DESC`)
	return r.queryList(ctx, q, keyID)
}

// ListAll returns every order, newest first.
func (r *PaymentOrderRepo) ListAll(ctx context.Context) ([]PaymentOrder, error) {
	q := r.db.rebind(`SELECT ` + paymentOrderCols + ` FROM payment_orders ORDER BY created_at DESC, id DESC`)
	return r.queryList(ctx, q)
}

// ListByStatus returns orders in a given status, newest first.
func (r *PaymentOrderRepo) ListByStatus(ctx context.Context, status PaymentOrderStatus) ([]PaymentOrder, error) {
	q := r.db.rebind(`SELECT ` + paymentOrderCols + ` FROM payment_orders WHERE status = ? ORDER BY created_at DESC, id DESC`)
	return r.queryList(ctx, q, string(status))
}

// TransitionOnTx conditionally moves an order from one status to another and
// returns whether the transition applied (RowsAffected == 1). Callers use the
// false return to detect an already-handled order and avoid double-crediting.
func (r *PaymentOrderRepo) TransitionOnTx(ctx context.Context, tx *sql.Tx, id string, from, to PaymentOrderStatus, actor, reason, paidAt string) (bool, error) {
	q := r.db.rebind(`UPDATE payment_orders SET status = ?, actor = ?, reason = ?, paid_at = ?, updated_at = ?
		WHERE id = ? AND status = ?`)
	res, err := tx.ExecContext(ctx, q, string(to), actor, reason, paidAt, formatTime(time.Now()), id, string(from))
	if err != nil {
		return false, fmt.Errorf("store: transition payment order: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: transition payment order rows affected: %w", err)
	}
	return n == 1, nil
}

// SumCompletedCreditByKey sums credit_micros across completed/manual orders for
// a key. Used when re-basing a portal plan so paid balance is preserved.
func (r *PaymentOrderRepo) SumCompletedCreditByKey(ctx context.Context, keyID string) (int64, error) {
	q := r.db.rebind(`SELECT COALESCE(SUM(credit_micros), 0) FROM payment_orders
		WHERE key_id = ? AND status IN (?, ?)`)
	var sum int64
	if err := r.db.sql.QueryRowContext(ctx, q, keyID, string(PaymentCompleted), string(PaymentManual)).Scan(&sum); err != nil {
		return 0, fmt.Errorf("store: sum payment credit: %w", err)
	}
	return sum, nil
}

func (r *PaymentOrderRepo) queryList(ctx context.Context, q string, args ...any) ([]PaymentOrder, error) {
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list payment orders: %w", err)
	}
	defer rows.Close()
	var out []PaymentOrder
	for rows.Next() {
		o, err := scanPaymentOrder(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanPaymentOrder(scan func(dest ...any) error) (PaymentOrder, error) {
	var (
		o                PaymentOrder
		status           string
		created, updated string
	)
	err := scan(&o.ID, &o.TenantID, &o.KeyID, &o.GoogleSub, &o.AmountIDR, &o.CreditMicros,
		&o.FxRateMicros, &status, &o.Provider, &o.ProviderPaymentID, &o.PaymentLinkURL,
		&o.IdempotencyKey, &o.Actor, &o.Reason, &created, &updated, &o.PaidAt, &o.ExpiresAt,
		&o.Product, &o.HostingInstanceID, &o.TermsVersion, &o.TermsAcceptedAt)
	if err != nil {
		return PaymentOrder{}, err
	}
	o.Status = PaymentOrderStatus(status)
	o.CreatedAt = parseTime(created)
	o.UpdatedAt = parseTime(updated)
	return o, nil
}

// PaidOrderTotals aggregates settled orders.
type PaidOrderTotals struct{ Count, AmountIDR, CreditMicros int64 }

// PaidTotalsSince totals completed and manual orders paid at or after since.
// Orders with an empty paid_at fall back to created_at.
func (r *PaymentOrderRepo) PaidTotalsSince(ctx context.Context, since time.Time) (PaidOrderTotals, error) {
	q := r.db.rebind(`SELECT COUNT(*), COALESCE(SUM(amount_idr), 0), COALESCE(SUM(credit_micros), 0)
		FROM payment_orders
		WHERE status IN (?, ?) AND COALESCE(NULLIF(paid_at, ''), created_at) >= ?`)
	var t PaidOrderTotals
	if err := r.db.sql.QueryRowContext(ctx, q, string(PaymentCompleted), string(PaymentManual), formatTime(since)).
		Scan(&t.Count, &t.AmountIDR, &t.CreditMicros); err != nil {
		return PaidOrderTotals{}, fmt.Errorf("store: paid totals: %w", err)
	}
	return t, nil
}

// KeyPaidTotals is one API key's settled top-ups.
type KeyPaidTotals struct {
	KeyID string
	PaidOrderTotals
}

// PaidTotalsByKeySince is PaidTotalsSince grouped by the credited key.
func (r *PaymentOrderRepo) PaidTotalsByKeySince(ctx context.Context, since time.Time) ([]KeyPaidTotals, error) {
	q := r.db.rebind(`SELECT key_id, COUNT(*), COALESCE(SUM(amount_idr), 0), COALESCE(SUM(credit_micros), 0)
		FROM payment_orders
		WHERE status IN (?, ?) AND COALESCE(NULLIF(paid_at, ''), created_at) >= ?
		GROUP BY key_id`)
	rows, err := r.db.sql.QueryContext(ctx, q, string(PaymentCompleted), string(PaymentManual), formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: paid totals by key: %w", err)
	}
	defer rows.Close()
	var out []KeyPaidTotals
	for rows.Next() {
		var k KeyPaidTotals
		if err := rows.Scan(&k.KeyID, &k.Count, &k.AmountIDR, &k.CreditMicros); err != nil {
			return nil, fmt.Errorf("store: paid totals by key scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
