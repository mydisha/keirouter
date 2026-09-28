package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeyTopupRepo_CreateListIdempotency(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	rec := KeyTopup{
		ID:                "t1",
		TenantID:          DefaultTenantID,
		KeyID:             "key1",
		BudgetID:          "b1",
		AmountMicros:      25_500_000,
		Reason:            "invoice #1",
		LimitBeforeMicros: 10_000_000,
		LimitAfterMicros:  35_500_000,
		IdempotencyKey:    "idem-1",
		Actor:             "dashboard",
		CreatedAt:         time.Now(),
	}
	require.NoError(t, db.Topups().Create(ctx, rec))

	got, err := db.Topups().ListByKey(ctx, "key1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(25_500_000), got[0].AmountMicros)
	require.Equal(t, rec.LimitBeforeMicros, got[0].LimitBeforeMicros)
	require.Equal(t, rec.LimitAfterMicros, got[0].LimitAfterMicros)
	require.Equal(t, "invoice #1", got[0].Reason)

	dup, err := db.Topups().GetByIdempotencyKey(ctx, "key1", "idem-1")
	require.NoError(t, err)
	require.Equal(t, "t1", dup.ID)

	_, err = db.Topups().GetByIdempotencyKey(ctx, "key1", "missing")
	require.ErrorIs(t, err, ErrNotFound)

	// The unique index must reject a second row with the same idempotency key.
	rec2 := rec
	rec2.ID = "t2"
	require.Error(t, db.Topups().Create(ctx, rec2))

	// An empty idempotency key is exempt (multiple manual rows allowed).
	rec3 := rec
	rec3.ID = "t3"
	rec3.IdempotencyKey = ""
	require.NoError(t, db.Topups().Create(ctx, rec3))
}
