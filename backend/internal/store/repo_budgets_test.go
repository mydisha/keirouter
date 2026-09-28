package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// incrementBudgetTx runs IncrementLimitOnTx in its own committed transaction.
func incrementBudgetTx(t *testing.T, db *DB, id string, delta int64) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	before, after, err := db.Budgets().IncrementLimitOnTx(ctx, tx, id, delta)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return before, after
}

func TestBudgetRepo_IncrementLimitOnTx(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	b := Budget{
		ID: "b1", TenantID: DefaultTenantID, ScopeKind: ScopeAPIKey, ScopeID: "key1",
		LimitMicros: 1_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, db.Budgets().Create(ctx, b))

	before, after := incrementBudgetTx(t, db, "b1", 2_500_000)
	require.Equal(t, int64(1_000_000), before)
	require.Equal(t, int64(3_500_000), after)

	got, err := db.Budgets().Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(3_500_000), got.LimitMicros)

	// Unknown id -> ErrNotFound, no row written.
	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, _, err = db.Budgets().IncrementLimitOnTx(ctx, tx, "missing", 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestBudgetRepo_IncrementLimitOnTx_Overflow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	b := Budget{
		ID: "ovf", TenantID: DefaultTenantID, ScopeKind: ScopeAPIKey, ScopeID: "key1",
		LimitMicros: math.MaxInt64 - 5, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, db.Budgets().Create(ctx, b))

	// A delta that would exceed MaxInt64 must be rejected before any write.
	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	before, after, err := db.Budgets().IncrementLimitOnTx(ctx, tx, "ovf", 10)
	require.ErrorIs(t, err, ErrLimitOverflow)
	require.Equal(t, int64(math.MaxInt64-5), before)
	require.Equal(t, int64(math.MaxInt64-5), after)
	require.NoError(t, tx.Rollback())

	// The stored row must be unchanged: no partially-written state.
	got, err := db.Budgets().Get(ctx, "ovf")
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64-5), got.LimitMicros)
}
