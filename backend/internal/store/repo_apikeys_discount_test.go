package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyMinDiscountPctRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	k := APIKey{
		ID: "k-floor", TenantID: DefaultTenantID, Name: "floor", KeyHash: "h",
		LookupHash: "lookup-floor", Display: "kr-...", CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.APIKeys().Create(ctx, k))

	got, err := db.APIKeys().Get(ctx, "k-floor")
	require.NoError(t, err)
	require.Equal(t, 0, got.MinDiscountPct, "new keys have no floor")

	require.NoError(t, db.APIKeys().SetMinDiscountPct(ctx, "k-floor", 30))
	got, err = db.APIKeys().FindByLookup(ctx, "lookup-floor")
	require.NoError(t, err)
	require.Equal(t, 30, got.MinDiscountPct)

	list, err := db.APIKeys().List(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 30, list[0].MinDiscountPct)

	require.ErrorIs(t, db.APIKeys().SetMinDiscountPct(ctx, "missing", 10), ErrNotFound)
}
