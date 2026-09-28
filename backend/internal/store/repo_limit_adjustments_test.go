package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// setLimitTx runs SetLimitOnTx in its own committed transaction.
func setLimitTx(t *testing.T, db *DB, id string, newLimit int64) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	before, after, err := db.Budgets().SetLimitOnTx(ctx, tx, id, newLimit)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return before, after
}

func TestBudgetRepo_SetLimitOnTx_ReducesAndRaises(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.Budgets().Create(ctx, Budget{
		ID: "b1", TenantID: DefaultTenantID, ScopeKind: ScopeAPIKey, ScopeID: "key1",
		LimitMicros: 6_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	// Reduce (undo a mistaken top-up).
	before, after := setLimitTx(t, db, "b1", 1_000_000)
	require.Equal(t, int64(6_000_000), before)
	require.Equal(t, int64(1_000_000), after)
	got, err := db.Budgets().Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000), got.LimitMicros)

	// Raise.
	before, after = setLimitTx(t, db, "b1", 2_500_000)
	require.Equal(t, int64(1_000_000), before)
	require.Equal(t, int64(2_500_000), after)

	// A zero limit is allowed (explicit "no budget").
	_, after = setLimitTx(t, db, "b1", 0)
	require.Equal(t, int64(0), after)
}

func TestBudgetRepo_SetLimitOnTx_RejectsNegativeAndMissing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.Budgets().Create(ctx, Budget{
		ID: "b1", TenantID: DefaultTenantID, ScopeKind: ScopeAPIKey, ScopeID: "key1",
		LimitMicros: 1_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	_, _, err = db.Budgets().SetLimitOnTx(ctx, tx, "b1", -1)
	require.ErrorIs(t, err, ErrInvalidLimit)

	_, _, err = db.Budgets().SetLimitOnTx(ctx, tx, "missing", 1)
	require.ErrorIs(t, err, ErrNotFound)

	// The rejected negative write must not have mutated the row.
	got, err := db.Budgets().Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000), got.LimitMicros)
}

func TestKeyLimitAdjustmentRepo_CreateAndList(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.LimitAdjustments()

	older := KeyLimitAdjustment{
		ID: "adj-1", TenantID: DefaultTenantID, KeyID: "key1", BudgetID: "b1",
		DeltaMicros: -5_000_000, Reason: "undo mistaken top-up",
		LimitBeforeMicros: 6_000_000, LimitAfterMicros: 1_000_000,
		IdempotencyKey: "idem-a", Actor: "dashboard", CreatedAt: time.Now().Add(-time.Hour),
	}
	newer := KeyLimitAdjustment{
		ID: "adj-2", TenantID: DefaultTenantID, KeyID: "key1", BudgetID: "b1",
		DeltaMicros: 1_500_000, Reason: "correction up",
		LimitBeforeMicros: 1_000_000, LimitAfterMicros: 2_500_000,
		IdempotencyKey: "idem-b", Actor: "dashboard", CreatedAt: time.Now(),
	}
	require.NoError(t, repo.Create(ctx, older))
	require.NoError(t, repo.Create(ctx, newer))

	got, err := repo.ListByKey(ctx, "key1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "adj-2", got[0].ID) // newest first
	require.Equal(t, "adj-1", got[1].ID)
	require.Equal(t, int64(-5_000_000), got[1].DeltaMicros)

	byIdem, err := repo.GetByIdempotencyKey(ctx, "key1", "idem-a")
	require.NoError(t, err)
	require.Equal(t, "adj-1", byIdem.ID)

	_, err = repo.GetByIdempotencyKey(ctx, "key1", "nope")
	require.ErrorIs(t, err, ErrNotFound)
}
