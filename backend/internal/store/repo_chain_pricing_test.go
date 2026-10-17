package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChainPricingRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	now := time.Now().UTC()
	c := Chain{
		ID: "c-price", TenantID: DefaultTenantID, Name: "priced", Strategy: "priority",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		CreatedAt: now, UpdatedAt: now,
		Steps: []ChainStep{{
			ID: "s1", ChainID: "c-price", Position: 0, Provider: "openai", Model: "gpt-4o", CreatedAt: now,
		}},
	}
	require.NoError(t, db.Chains().Create(ctx, c))

	got, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Equal(t, 2.5, got.InputPerM)
	require.Equal(t, 10.0, got.OutputPerM)
	require.Equal(t, 3.125, got.CacheWritePerM)
	require.Equal(t, 0.25, got.CacheReadPerM)

	// Update path must keep chain rates.
	c.CacheReadPerM = 0.5
	require.NoError(t, db.Chains().Update(ctx, c))
	again, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Equal(t, 0.5, again.CacheReadPerM)

	list, err := db.Chains().ListByTenant(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 3.125, list[0].CacheWritePerM)
}

func TestChainMarkupOverrideRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	now := time.Now().UTC()
	step := []ChainStep{{ID: "s", ChainID: "c-mk", Position: 0, Provider: "openai", Model: "gpt-4o", CreatedAt: now}}
	c := Chain{ID: "c-mk", TenantID: DefaultTenantID, Name: "mk", Strategy: "priority", CreatedAt: now, UpdatedAt: now, Steps: step}
	require.NoError(t, db.Chains().Create(ctx, c))
	got, err := db.Chains().Get(ctx, "c-mk")
	require.NoError(t, err)
	require.Nil(t, got.MarkupPercent, "unset markup must follow global (nil)")

	zero := 0.0
	c.MarkupPercent = &zero
	require.NoError(t, db.Chains().Update(ctx, c))
	got, err = db.Chains().Get(ctx, "c-mk")
	require.NoError(t, err)
	require.NotNil(t, got.MarkupPercent, "explicit 0 must survive as an override")
	require.Equal(t, 0.0, *got.MarkupPercent)

	v := 25.5
	c.MarkupPercent = &v
	require.NoError(t, db.Chains().Update(ctx, c))
	list, err := db.Chains().ListByTenant(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Equal(t, 25.5, *list[0].MarkupPercent)

	c.MarkupPercent = nil
	require.NoError(t, db.Chains().Update(ctx, c))
	got, err = db.Chains().Get(ctx, "c-mk")
	require.NoError(t, err)
	require.Nil(t, got.MarkupPercent)
}

func TestChainOfficialPriceRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	now := time.Now().UTC()
	in, out := 0.36, 6.84
	c := Chain{
		ID: "c-off", TenantID: DefaultTenantID, Name: "official", Strategy: "priority",
		OfficialInputPerM: &in, OfficialOutputPerM: &out, CreatedAt: now, UpdatedAt: now,
		Steps: []ChainStep{{ID: "s1", ChainID: "c-off", Provider: "openai", Model: "gpt-4o", CreatedAt: now}},
	}
	require.NoError(t, db.Chains().Create(ctx, c))
	got, err := db.Chains().Get(ctx, "c-off")
	require.NoError(t, err)
	require.Equal(t, 0.36, *got.OfficialInputPerM)
	require.Equal(t, 6.84, *got.OfficialOutputPerM)

	c.OfficialInputPerM, c.OfficialOutputPerM = nil, nil
	require.NoError(t, db.Chains().Update(ctx, c))
	list, err := db.Chains().ListByTenant(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Nil(t, list[0].OfficialInputPerM)
	require.Nil(t, list[0].OfficialOutputPerM)
}
