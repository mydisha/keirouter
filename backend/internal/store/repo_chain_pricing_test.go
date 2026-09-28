package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChainStepPricingRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	now := time.Now().UTC()
	c := Chain{
		ID: "c-price", TenantID: DefaultTenantID, Name: "priced", Strategy: "priority",
		CreatedAt: now, UpdatedAt: now,
		Steps: []ChainStep{{
			ID: "s1", ChainID: "c-price", Position: 0, Provider: "openai", Model: "gpt-4o",
			InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25, CreatedAt: now,
		}},
	}
	require.NoError(t, db.Chains().Create(ctx, c))

	got, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Len(t, got.Steps, 1)
	require.Equal(t, 2.5, got.Steps[0].InputPerM)
	require.Equal(t, 10.0, got.Steps[0].OutputPerM)
	require.Equal(t, 3.125, got.Steps[0].CacheWritePerM)
	require.Equal(t, 0.25, got.Steps[0].CacheReadPerM)

	// Update path replaces steps and must keep rates.
	c.Steps[0].CacheReadPerM = 0.5
	require.NoError(t, db.Chains().Update(ctx, c))
	again, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Equal(t, 0.5, again.Steps[0].CacheReadPerM)

	list, err := db.Chains().ListByTenant(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 3.125, list[0].Steps[0].CacheWritePerM)
}
