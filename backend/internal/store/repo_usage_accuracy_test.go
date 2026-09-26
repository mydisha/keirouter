package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestByModelAccurateSeparatesLegacyAndMissingPricing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []UsageRecord{
		{
			ID: "legacy-1", TenantID: DefaultTenantID, Provider: "anthropic", Model: "claude-legacy",
			Status: "success", PromptTokens: 100, CompletionTokens: 20, UsageSource: "legacy",
			CostNanos: 42_000_000, PricingStatus: "legacy", PricingSource: "legacy", CreatedAt: now,
		},
		{
			ID: "legacy-2", TenantID: DefaultTenantID, Provider: "anthropic", Model: "claude-legacy",
			Status: "success", PromptTokens: 80, CompletionTokens: 10, UsageSource: "legacy",
			CostNanos: 21_000_000, PricingStatus: "legacy", PricingSource: "legacy", CreatedAt: now,
		},
		{
			ID: "missing-1", TenantID: DefaultTenantID, Provider: "custom", Model: "unknown-model",
			Status: "success", PromptTokens: 40, CompletionTokens: 5, UsageSource: "provider",
			PricingStatus: "missing", CreatedAt: now,
		},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	models, err := db.Usage().ByModelAccurate(ctx, DefaultTenantID, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, models, 2)

	byModel := make(map[string]AccurateModelUsage, len(models))
	for _, model := range models {
		byModel[model.Model] = model
	}
	legacy := byModel["claude-legacy"]
	require.Equal(t, int64(2), legacy.PricingEligibleRequests)
	require.Equal(t, int64(2), legacy.UnpricedRequests)
	require.Equal(t, int64(0), legacy.MissingPricingRequests)
	require.Equal(t, int64(2), legacy.LegacyPricingRequests)
	require.Equal(t, int64(63_000_000), legacy.CostNanos)

	missing := byModel["unknown-model"]
	require.Equal(t, int64(1), missing.MissingPricingRequests)
	require.Equal(t, int64(0), missing.LegacyPricingRequests)
}

func TestDistinctKeysPerModelCounts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []UsageRecord{
		{ID: "k1", TenantID: DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", CreatedAt: now},
		{ID: "k2", TenantID: DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", CreatedAt: now},
		{ID: "k3", TenantID: DefaultTenantID, APIKeyID: "key-b", Provider: "openai", Model: "gpt-4o", Status: "success", CreatedAt: now},
		{ID: "k4", TenantID: DefaultTenantID, APIKeyID: "key-c", Provider: "anthropic", Model: "claude", Status: "success", CreatedAt: now},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	counts, err := db.Usage().DistinctKeysPerModel(ctx, DefaultTenantID, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, 2, counts["openai\x00gpt-4o"])
	require.Equal(t, 1, counts["anthropic\x00claude"])
}
