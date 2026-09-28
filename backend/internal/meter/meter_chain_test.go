package meter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// chainCaptureStore records the last UsageRecord the meter persisted.
type chainCaptureStore struct{ last store.UsageRecord }

func (c *chainCaptureStore) Record(_ context.Context, u store.UsageRecord) error {
	c.last = u
	return nil
}
func (c *chainCaptureStore) RecordBatch(_ context.Context, rs []store.UsageRecord) error {
	if len(rs) > 0 {
		c.last = rs[len(rs)-1]
	}
	return nil
}

func TestRecordPersistsChainID(t *testing.T) {
	cap := &chainCaptureStore{}
	m := New(cap, nil, nil)
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o",
		ChainID: "chain-9", Status: "success",
		Usage: core.Usage{PromptTokens: 1, CompletionTokens: 1, Source: core.UsageSourceProvider},
	})
	require.NoError(t, err)
	require.Equal(t, "chain-9", cap.last.ChainID)
}

func TestRecordUsesChainStepPrice(t *testing.T) {
	cap := &chainCaptureStore{}
	m := New(cap, nil, nil) // no catalog prices at all
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o", ChainID: "c1",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		Status: "success",
		Usage:  core.Usage{PromptTokens: 1000, CompletionTokens: 500, Source: core.UsageSourceProvider},
	})
	require.NoError(t, err)
	require.Equal(t, "chain", cap.last.PricingSource)
	// 1000/1M*2.5 = 0.0025 USD input; 500/1M*10 = 0.005 USD output
	// = 0.0075 USD → 7,500,000 nanodollars (tokenCostNanos: tokens*rate*1000).
	require.Equal(t, int64(7_500_000), cap.last.CostNanos)
}

func TestRecordChainPriceSplitsAllFourClasses(t *testing.T) {
	cap := &chainCaptureStore{}
	m := New(cap, nil, nil)
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o", ChainID: "c1",
		InputPerM: 2, OutputPerM: 10, CacheWritePerM: 2.5, CacheReadPerM: 0.2,
		Status: "success",
		Usage: core.Usage{
			PromptTokens: 1000, CompletionTokens: 500,
			CachedTokens: 200, CacheWriteTokens: 100, Source: core.UsageSourceProvider,
		},
	})
	require.NoError(t, err)
	// standardInput = 1000-200-100 = 700 → 700*2*1000 = 1,400,000
	require.Equal(t, int64(1_400_000), cap.last.InputCostNanos)
	// cached = 200*0.2*1000 = 40,000
	require.Equal(t, int64(40_000), cap.last.CachedCostNanos)
	// cache write = 100*2.5*1000 = 250,000
	require.Equal(t, int64(250_000), cap.last.CacheWriteCostNanos)
	// output = 500*10*1000 = 5,000,000
	require.Equal(t, int64(5_000_000), cap.last.OutputCostNanos)
	require.Equal(t, int64(6_690_000), cap.last.CostNanos)
}

func TestRecordFallsBackToCatalogWhenNoChainPrice(t *testing.T) {
	cap := &chainCaptureStore{}
	m := New(cap, nil, map[string]Price{"openai/gpt-4o": {InputPerM: 1, OutputPerM: 2, Source: "catalog"}})
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o", ChainID: "c1",
		Status: "success",
		Usage:  core.Usage{PromptTokens: 1000, CompletionTokens: 500, Source: core.UsageSourceProvider},
	})
	require.NoError(t, err)
	require.Equal(t, "catalog", cap.last.PricingSource)
	require.Equal(t, int64(2_000_000), cap.last.CostNanos) // 1,000,000 + 1,000,000 nanos
}
