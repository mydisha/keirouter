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

func TestAPIKeyModelDiscountsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	require.NoError(t, db.APIKeys().Create(ctx, APIKey{
		ID: "k-md", TenantID: DefaultTenantID, Name: "md", KeyHash: "h",
		LookupHash: "lookup-md", Display: "kr-...", CreatedAt: time.Now().UTC(),
	}))
	repo := db.APIKeys()

	got, err := repo.ModelDiscounts(ctx, "k-md")
	require.NoError(t, err)
	require.Empty(t, got)

	require.NoError(t, repo.SetModelDiscount(ctx, "k-md", "chain:Gemini-Trusted", 50))
	require.NoError(t, repo.SetModelDiscount(ctx, "k-md", "glm-trusted", 0))
	require.NoError(t, repo.SetModelDiscount(ctx, "k-md", "gemini-trusted", 55), "same model, other spelling: updates")
	got, err = repo.ModelDiscounts(ctx, "k-md")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"gemini-trusted": 55, "glm-trusted": 0}, got)

	require.NoError(t, repo.DeleteModelDiscount(ctx, "k-md", "GEMINI-trusted"))
	require.NoError(t, repo.DeleteModelDiscount(ctx, "k-md", "never-set"))
	got, err = repo.ModelDiscounts(ctx, "k-md")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"glm-trusted": 0}, got)
}

func TestAPIKeyEffectiveMinDiscount(t *testing.T) {
	k := APIKey{MinDiscountPct: 30, ModelDiscounts: map[string]int{"gemini-trusted": 50, "glm-trusted": 0}}
	require.Equal(t, 50, k.EffectiveMinDiscount("gemini-trusted"))
	require.Equal(t, 50, k.EffectiveMinDiscount("chain:Gemini-Trusted"))
	require.Equal(t, 0, k.EffectiveMinDiscount("glm-trusted"), "an override of 0 turns the default off")
	require.Equal(t, 30, k.EffectiveMinDiscount("luna-trusted"), "no override: key default")
	require.Equal(t, 0, APIKey{}.EffectiveMinDiscount("luna-trusted"))
}
