package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Hosting coupon kinds.
const (
	CouponKindDiscount  = "discount"
	CouponKindBonusDays = "bonus_days"
)

// Hosting coupon discount types.
const (
	CouponDiscountPercent = "percent"
	CouponDiscountFixed   = "fixed"
)

// Coupon redemption statuses.
const (
	RedemptionReserved = "reserved"
	RedemptionUsed     = "used"
	RedemptionReleased = "released"
)

// Coupon errors returned by Reserve.
var (
	ErrCouponExhausted   = errors.New("store: coupon has no uses left")
	ErrCouponAlreadyUsed = errors.New("store: coupon already used by this key")
	ErrCouponInactive    = errors.New("store: coupon is not active")
	ErrCouponInUse       = errors.New("store: coupon has redemptions; deactivate it instead")
)

// HostingCoupon is an admin-defined coupon for the hosting product.
// MaxUses 0 means unlimited. UsedCount counts paid redemptions; HeldCount
// counts redemptions whose payment is still pending.
type HostingCoupon struct {
	ID            string
	Code          string
	Kind          string
	DiscountType  string
	DiscountValue int64
	BonusDays     int
	MaxUses       int
	UsedCount     int
	HeldCount     int
	StartsAt      time.Time
	ExpiresAt     time.Time
	Active        bool
	Note          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// HostingCouponRepo persists hosting coupons and their redemptions.
type HostingCouponRepo struct{ db *DB }

// HostingCoupons returns the hosting coupon repository.
func (db *DB) HostingCoupons() *HostingCouponRepo { return &HostingCouponRepo{db: db} }

const hostingCouponCols = `id, code, kind, discount_type, discount_value, bonus_days,
	max_uses, used_count, held_count, starts_at, expires_at, active, note, created_at, updated_at`

// Create inserts a coupon. A duplicate code returns ErrAlreadyExists.
func (r *HostingCouponRepo) Create(ctx context.Context, c HostingCoupon) error {
	q := r.db.rebind(`INSERT INTO hosting_coupons (` + hostingCouponCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q, c.ID, c.Code, c.Kind, c.DiscountType, c.DiscountValue,
		c.BonusDays, c.MaxUses, formatTime(c.StartsAt), formatTime(c.ExpiresAt), boolToInt(c.Active),
		c.Note, formatTime(c.CreatedAt), formatTime(c.UpdatedAt))
	if isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("store: create hosting coupon: %w", err)
	}
	return nil
}

// Update changes a coupon's editable fields. Counters are never touched here.
func (r *HostingCouponRepo) Update(ctx context.Context, c HostingCoupon) error {
	q := r.db.rebind(`UPDATE hosting_coupons SET code = ?, kind = ?, discount_type = ?,
		discount_value = ?, bonus_days = ?, max_uses = ?, starts_at = ?, expires_at = ?,
		active = ?, note = ?, updated_at = ? WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, c.Code, c.Kind, c.DiscountType, c.DiscountValue,
		c.BonusDays, c.MaxUses, formatTime(c.StartsAt), formatTime(c.ExpiresAt), boolToInt(c.Active),
		c.Note, formatTime(time.Now()), c.ID)
	if isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("store: update hosting coupon: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a coupon that was never redeemed. A coupon with any
// redemption history returns ErrCouponInUse; deactivate it instead.
func (r *HostingCouponRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM hosting_coupons WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM hosting_coupon_redemptions WHERE coupon_id = ?)`)
	res, err := r.db.sql.ExecContext(ctx, q, id, id)
	if err != nil {
		return fmt.Errorf("store: delete hosting coupon: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, gerr := r.Get(ctx, id); gerr != nil {
		return gerr
	}
	return ErrCouponInUse
}

// Get returns one coupon by id.
func (r *HostingCouponRepo) Get(ctx context.Context, id string) (HostingCoupon, error) {
	q := r.db.rebind(`SELECT ` + hostingCouponCols + ` FROM hosting_coupons WHERE id = ?`)
	return r.one(r.db.sql.QueryRowContext(ctx, q, id).Scan)
}

// GetByCode returns one coupon by its normalized code.
func (r *HostingCouponRepo) GetByCode(ctx context.Context, code string) (HostingCoupon, error) {
	q := r.db.rebind(`SELECT ` + hostingCouponCols + ` FROM hosting_coupons WHERE code = ?`)
	return r.one(r.db.sql.QueryRowContext(ctx, q, code).Scan)
}

// List returns every coupon, newest first.
func (r *HostingCouponRepo) List(ctx context.Context) ([]HostingCoupon, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT `+hostingCouponCols+` FROM hosting_coupons ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list hosting coupons: %w", err)
	}
	defer rows.Close()
	var out []HostingCoupon
	for rows.Next() {
		c, err := scanHostingCoupon(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// HasLiveRedemption reports whether key already holds or used the coupon.
func (r *HostingCouponRepo) HasLiveRedemption(ctx context.Context, couponID, keyID string) (bool, error) {
	q := r.db.rebind(`SELECT COUNT(*) FROM hosting_coupon_redemptions
		WHERE coupon_id = ? AND key_id = ? AND status IN (?, ?)`)
	var n int
	if err := r.db.sql.QueryRowContext(ctx, q, couponID, keyID, RedemptionReserved, RedemptionUsed).Scan(&n); err != nil {
		return false, fmt.Errorf("store: hosting coupon redemption lookup: %w", err)
	}
	return n > 0, nil
}

// ReserveOnTx takes one use of the coupon for orderID until reservedUntil.
// The slot is taken with a single guarded UPDATE, so concurrent checkouts
// can never take more slots than max_uses allows. The live-redemption unique
// index is the backstop for the one-use-per-key rule. The write comes first
// so the transaction holds the write lock before it reads (SQLite cannot
// upgrade a read transaction under contention); every error path is undone
// by the caller's rollback.
func (r *HostingCouponRepo) ReserveOnTx(ctx context.Context, tx *sql.Tx, couponID, keyID, orderID string, reservedUntil, now time.Time) error {
	q := r.db.rebind(`UPDATE hosting_coupons SET held_count = held_count + 1, updated_at = ?
		WHERE id = ? AND active = 1 AND (max_uses = 0 OR used_count + held_count < max_uses)`)
	res, err := tx.ExecContext(ctx, q, formatTime(now), couponID)
	if err != nil {
		return fmt.Errorf("store: reserve hosting coupon: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		var active int
		aq := r.db.rebind(`SELECT active FROM hosting_coupons WHERE id = ?`)
		if err := tx.QueryRowContext(ctx, aq, couponID).Scan(&active); err != nil || active != 1 {
			return ErrCouponInactive
		}
		return ErrCouponExhausted
	}
	var live int
	q = r.db.rebind(`SELECT COUNT(*) FROM hosting_coupon_redemptions
		WHERE coupon_id = ? AND key_id = ? AND status IN (?, ?)`)
	if err := tx.QueryRowContext(ctx, q, couponID, keyID, RedemptionReserved, RedemptionUsed).Scan(&live); err != nil {
		return fmt.Errorf("store: hosting coupon redemption lookup: %w", err)
	}
	if live > 0 {
		return ErrCouponAlreadyUsed
	}
	q = r.db.rebind(`INSERT INTO hosting_coupon_redemptions
		(id, coupon_id, key_id, order_id, status, reserved_until, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = tx.ExecContext(ctx, q, uuid.NewString(), couponID, keyID, orderID, RedemptionReserved,
		formatTime(reservedUntil), formatTime(now), formatTime(now))
	if isUniqueViolation(err) {
		return ErrCouponAlreadyUsed
	}
	if err != nil {
		return fmt.Errorf("store: insert hosting coupon redemption: %w", err)
	}
	return nil
}

// ConsumeOnTx marks the order's coupon use as paid. A reserved slot moves
// from held to used. A slot that was already released (the payment landed
// after its reservation lapsed) is reclaimed unless the key holds another
// live use of the same coupon; the payment is honoured either way, so the
// cap can be exceeded by such a late payment. It reports whether the use
// was counted.
func (r *HostingCouponRepo) ConsumeOnTx(ctx context.Context, tx *sql.Tx, orderID string, now time.Time) (bool, error) {
	var couponID, keyID, status string
	q := r.db.rebind(`SELECT coupon_id, key_id, status FROM hosting_coupon_redemptions WHERE order_id = ?`)
	if err := tx.QueryRowContext(ctx, q, orderID).Scan(&couponID, &keyID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("store: load hosting coupon redemption: %w", err)
	}
	switch status {
	case RedemptionUsed:
		return true, nil
	case RedemptionReserved:
		if ok, err := r.moveRedemption(ctx, tx, orderID, RedemptionReserved, RedemptionUsed, now); err != nil || !ok {
			return false, err
		}
		return true, r.bumpCounters(ctx, tx, couponID, -1, 1, now)
	default: // released
		var live int
		lq := r.db.rebind(`SELECT COUNT(*) FROM hosting_coupon_redemptions
			WHERE coupon_id = ? AND key_id = ? AND order_id <> ? AND status IN (?, ?)`)
		if err := tx.QueryRowContext(ctx, lq, couponID, keyID, orderID, RedemptionReserved, RedemptionUsed).Scan(&live); err != nil {
			return false, fmt.Errorf("store: hosting coupon live lookup: %w", err)
		}
		if live > 0 {
			return false, nil
		}
		if ok, err := r.moveRedemption(ctx, tx, orderID, RedemptionReleased, RedemptionUsed, now); err != nil || !ok {
			return false, err
		}
		return true, r.bumpCounters(ctx, tx, couponID, 0, 1, now)
	}
}

// ReleaseOnTx gives back the order's reserved coupon use. It is a no-op when
// the order has no reservation or it was already consumed or released.
func (r *HostingCouponRepo) ReleaseOnTx(ctx context.Context, tx *sql.Tx, orderID string, now time.Time) (bool, error) {
	// Guarded write first (see ReserveOnTx), then read which coupon it was.
	ok, err := r.moveRedemption(ctx, tx, orderID, RedemptionReserved, RedemptionReleased, now)
	if err != nil || !ok {
		return false, err
	}
	var couponID string
	q := r.db.rebind(`SELECT coupon_id FROM hosting_coupon_redemptions WHERE order_id = ?`)
	if err := tx.QueryRowContext(ctx, q, orderID).Scan(&couponID); err != nil {
		return false, fmt.Errorf("store: load hosting coupon reservation: %w", err)
	}
	return true, r.bumpCounters(ctx, tx, couponID, -1, 0, now)
}

// ReleaseExpired gives back every reservation whose hold lapsed before now,
// optionally only for one coupon, and returns how many were released. Each
// release runs in its own transaction and is guarded, so it is safe to run
// concurrently with settlement.
func (r *HostingCouponRepo) ReleaseExpired(ctx context.Context, couponID string, now time.Time) (int, error) {
	q := `SELECT order_id FROM hosting_coupon_redemptions WHERE status = ? AND reserved_until <> '' AND reserved_until < ?`
	args := []any{RedemptionReserved, formatTime(now)}
	if couponID != "" {
		q += ` AND coupon_id = ?`
		args = append(args, couponID)
	}
	rows, err := r.db.sql.QueryContext(ctx, r.db.rebind(q), args...)
	if err != nil {
		return 0, fmt.Errorf("store: list lapsed hosting coupon reservations: %w", err)
	}
	var orders []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		orders = append(orders, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	released := 0
	for _, id := range orders {
		tx, err := r.db.sql.BeginTx(ctx, nil)
		if err != nil {
			return released, err
		}
		ok, err := r.ReleaseOnTx(ctx, tx, id, now)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
			return released, err
		}
		if ok {
			released++
		}
	}
	return released, nil
}

func (r *HostingCouponRepo) moveRedemption(ctx context.Context, tx *sql.Tx, orderID, from, to string, now time.Time) (bool, error) {
	q := r.db.rebind(`UPDATE hosting_coupon_redemptions SET status = ?, updated_at = ? WHERE order_id = ? AND status = ?`)
	res, err := tx.ExecContext(ctx, q, to, formatTime(now), orderID, from)
	if err != nil {
		return false, fmt.Errorf("store: update hosting coupon redemption: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *HostingCouponRepo) bumpCounters(ctx context.Context, tx *sql.Tx, couponID string, held, used int, now time.Time) error {
	q := r.db.rebind(`UPDATE hosting_coupons SET held_count = held_count + ?, used_count = used_count + ?, updated_at = ? WHERE id = ?`)
	if _, err := tx.ExecContext(ctx, q, held, used, formatTime(now), couponID); err != nil {
		return fmt.Errorf("store: update hosting coupon counters: %w", err)
	}
	return nil
}

func (r *HostingCouponRepo) one(scan func(dest ...any) error) (HostingCoupon, error) {
	c, err := scanHostingCoupon(scan)
	if errors.Is(err, sql.ErrNoRows) {
		return HostingCoupon{}, ErrNotFound
	}
	return c, err
}

func scanHostingCoupon(scan func(dest ...any) error) (HostingCoupon, error) {
	var (
		c                                 HostingCoupon
		starts, expires, created, updated string
		active                            int
	)
	err := scan(&c.ID, &c.Code, &c.Kind, &c.DiscountType, &c.DiscountValue, &c.BonusDays,
		&c.MaxUses, &c.UsedCount, &c.HeldCount, &starts, &expires, &active, &c.Note, &created, &updated)
	if err != nil {
		return HostingCoupon{}, err
	}
	c.Active = active == 1
	c.StartsAt = parseTime(starts)
	c.ExpiresAt = parseTime(expires)
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	return c, nil
}
