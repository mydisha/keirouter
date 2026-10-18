package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestCoupon(t *testing.T, db *DB, code string, maxUses int) HostingCoupon {
	t.Helper()
	now := time.Now()
	c := HostingCoupon{
		ID: "c-" + code, Code: code, Kind: CouponKindDiscount, DiscountType: CouponDiscountPercent,
		DiscountValue: 10, MaxUses: maxUses, Active: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, db.HostingCoupons().Create(context.Background(), c))
	return c
}

func reserve(ctx context.Context, db *DB, couponID, keyID, orderID string, until time.Time) error {
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := db.HostingCoupons().ReserveOnTx(ctx, tx, couponID, keyID, orderID, until, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

func onTx(t *testing.T, db *DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	tx, err := db.SQL().BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, fn(tx))
	require.NoError(t, tx.Commit())
}

// Concurrent checkouts can never take more uses than max_uses.
func TestHostingCoupon_ReserveNeverExceedsMaxUses(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	c := newTestCoupon(t, db, "RACE", 5)

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, exhausted, other := 0, 0, 0
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			// SQLite may report a busy database under contention; retry
			// like a client would.
			for range 50 {
				err = reserve(ctx, db, c.ID, fmt.Sprintf("key-%d", i), fmt.Sprintf("order-%d", i), time.Now().Add(time.Hour))
				if err == nil || errors.Is(err, ErrCouponExhausted) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrCouponExhausted):
				exhausted++
			default:
				other++
				t.Logf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 5, ok)
	require.Equal(t, 15, exhausted)
	require.Zero(t, other)
	got, err := db.HostingCoupons().Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, 5, got.HeldCount)
	require.Zero(t, got.UsedCount)
}

func TestHostingCoupon_OneLiveUsePerKey(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	c := newTestCoupon(t, db, "ONCE", 0)
	until := time.Now().Add(time.Hour)

	require.NoError(t, reserve(ctx, db, c.ID, "k1", "o1", until))
	require.ErrorIs(t, reserve(ctx, db, c.ID, "k1", "o2", until), ErrCouponAlreadyUsed)

	// Released: the key may use it again.
	onTx(t, db, func(tx *sql.Tx) error {
		released, err := db.HostingCoupons().ReleaseOnTx(ctx, tx, "o1", time.Now())
		require.True(t, released)
		return err
	})
	require.NoError(t, reserve(ctx, db, c.ID, "k1", "o2", until))

	// Paid: never again.
	onTx(t, db, func(tx *sql.Tx) error {
		counted, err := db.HostingCoupons().ConsumeOnTx(ctx, tx, "o2", time.Now())
		require.True(t, counted)
		return err
	})
	require.ErrorIs(t, reserve(ctx, db, c.ID, "k1", "o3", until), ErrCouponAlreadyUsed)

	got, err := db.HostingCoupons().Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.UsedCount)
	require.Zero(t, got.HeldCount)
}

// A hold whose payment lapsed goes back to the pool; consuming or releasing
// twice changes the counters once.
func TestHostingCoupon_ReleaseExpiredAndIdempotence(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	c := newTestCoupon(t, db, "LAPSE", 1)

	require.NoError(t, reserve(ctx, db, c.ID, "k1", "o1", time.Now().Add(-time.Minute)))
	require.ErrorIs(t, reserve(ctx, db, c.ID, "k2", "o2", time.Now().Add(time.Hour)), ErrCouponExhausted)

	n, err := db.HostingCoupons().ReleaseExpired(ctx, c.ID, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = db.HostingCoupons().ReleaseExpired(ctx, "", time.Now())
	require.NoError(t, err)
	require.Zero(t, n)

	require.NoError(t, reserve(ctx, db, c.ID, "k2", "o2", time.Now().Add(time.Hour)))
	for range 2 {
		onTx(t, db, func(tx *sql.Tx) error {
			_, err := db.HostingCoupons().ConsumeOnTx(ctx, tx, "o2", time.Now())
			return err
		})
		onTx(t, db, func(tx *sql.Tx) error {
			_, err := db.HostingCoupons().ReleaseOnTx(ctx, tx, "o2", time.Now())
			return err
		})
	}
	got, err := db.HostingCoupons().Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.UsedCount)
	require.Zero(t, got.HeldCount)

	// The late payment of the lapsed o1 is still honoured and counted.
	onTx(t, db, func(tx *sql.Tx) error {
		counted, err := db.HostingCoupons().ConsumeOnTx(ctx, tx, "o1", time.Now())
		require.True(t, counted)
		return err
	})
	got, err = db.HostingCoupons().Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.UsedCount)
}

func TestHostingCoupon_DeleteOnlyUnused(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	unused := newTestCoupon(t, db, "UNUSED", 0)
	used := newTestCoupon(t, db, "USED", 0)
	require.NoError(t, reserve(ctx, db, used.ID, "k", "o", time.Now().Add(time.Hour)))

	require.NoError(t, db.HostingCoupons().Delete(ctx, unused.ID))
	require.ErrorIs(t, db.HostingCoupons().Delete(ctx, used.ID), ErrCouponInUse)
	require.ErrorIs(t, db.HostingCoupons().Delete(ctx, "missing"), ErrNotFound)
	require.ErrorIs(t, db.HostingCoupons().Create(ctx, HostingCoupon{
		ID: "dup", Code: "USED", Kind: CouponKindBonusDays, BonusDays: 3, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}), ErrAlreadyExists)
}
