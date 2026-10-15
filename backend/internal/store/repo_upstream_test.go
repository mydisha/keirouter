package store

import (
	"context"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/stretchr/testify/require"
)

func openUpstreamDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(ctx))
	return db, ctx
}

func TestUpstreamRepo_Seeded(t *testing.T) {
	db, ctx := openUpstreamDB(t)
	list, err := db.Upstream().List(ctx)
	require.NoError(t, err)
	var ids []string
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	require.ElementsMatch(t, []string{"surplus", "inferhub", "crowbot"}, ids)
	crow, err := db.Upstream().Get(ctx, "crowbot")
	require.NoError(t, err)
	require.Equal(t, "planned", crow.Status)
	require.Equal(t, "planned", list[len(list)-1].Status)
	for _, a := range list {
		require.WithinDuration(t, time.Now(), a.OpeningAt, time.Minute, a.ID)
		require.WithinDuration(t, time.Now(), a.CreatedAt, time.Minute, a.ID)
		require.WithinDuration(t, time.Now(), a.UpdatedAt, time.Minute, a.ID)
	}
}

func TestUpstreamRepo_RemapProvider(t *testing.T) {
	db, ctx := openUpstreamDB(t)
	r := db.Upstream()
	require.NoError(t, r.SetProviders(ctx, "surplus", []string{"custom-openai-a"}))
	require.NoError(t, r.SetProviders(ctx, "inferhub", []string{"custom-openai-a"}))
	s, err := r.Get(ctx, "surplus")
	require.NoError(t, err)
	require.Empty(t, s.Providers)
	i, err := r.Get(ctx, "inferhub")
	require.NoError(t, err)
	require.Equal(t, []string{"custom-openai-a"}, i.Providers)
}

func TestUpstreamRepo_TopupsAndTotals(t *testing.T) {
	db, ctx := openUpstreamDB(t)
	r := db.Upstream()
	a, err := r.Get(ctx, "surplus")
	require.NoError(t, err)
	a.OpeningAt = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, r.Update(ctx, a))
	require.NoError(t, r.AddTopup(ctx, UpstreamTopup{ID: "t1", AccountID: "surplus", AmountMicros: 5_000_000, TopupAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}))
	require.NoError(t, r.AddTopup(ctx, UpstreamTopup{ID: "t2", AccountID: "surplus", AmountMicros: 20_000_000, TopupAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}))
	totals, err := r.TopupTotals(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 20_000_000, totals["surplus"])
	all, err := r.ListTopups(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "t2", all[0].ID)
	require.ErrorIs(t, r.DeleteTopup(ctx, "nope"), ErrNotFound)
	require.NoError(t, r.DeleteTopup(ctx, "t1"))
}

func TestUpstreamRepo_AlertFiredRoundTrip(t *testing.T) {
	db, ctx := openUpstreamDB(t)
	r := db.Upstream()
	require.NoError(t, r.SetAlertFiredAt(ctx, "surplus", time.Now()))
	a, err := r.Get(ctx, "surplus")
	require.NoError(t, err)
	require.False(t, a.AlertFiredAt.IsZero())
	require.NoError(t, r.SetAlertFiredAt(ctx, "surplus", time.Time{}))
	a, err = r.Get(ctx, "surplus")
	require.NoError(t, err)
	require.True(t, a.AlertFiredAt.IsZero())
}

func TestUpstreamRepo_TopupAtOpeningIncluded(t *testing.T) {
	db, ctx := openUpstreamDB(t)
	r := db.Upstream()
	a, err := r.Get(ctx, "surplus")
	require.NoError(t, err)
	opening := time.Date(2026, 10, 15, 7, 30, 0, 0, time.UTC)
	a.OpeningAt = opening
	require.NoError(t, r.Update(ctx, a))
	require.NoError(t, r.AddTopup(ctx, UpstreamTopup{ID: "t1", AccountID: "surplus", AmountMicros: 7_000_000, TopupAt: opening}))
	totals, err := r.TopupTotals(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 7_000_000, totals["surplus"])
}
