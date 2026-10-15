package meter

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func newTestMeter() *Meter {
	return New(nil, nil, map[string]Price{
		"anthropic/claude-sonnet-4-5": {InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.3, CacheWritePerM: 3.75, CacheWrite1hPerM: 6, WebSearchPerK: 10, Source: "official"},
		"openai/gpt-5":                {InputPerM: 1.25, OutputPerM: 10, CachedInputPerM: 0.125, Source: "official"},
		"openai/gpt-4o":               {InputPerM: 2.5, OutputPerM: 10, Source: "official"},
		"openrouter/some/model":       {InputPerM: 1, OutputPerM: 2, Source: "catalog"},
	})
}

func TestCacheWrite1hBilledAtPremiumRate(t *testing.T) {
	m := newTestMeter()
	u := core.Usage{PromptTokens: 1_000_000, CacheWriteTokens: 1_000_000, CacheWrite1hTokens: 400_000, CompletionTokens: 0}
	cost := m.CalculateCost("anthropic", "claude-sonnet-4-5", u, false, 0)
	// 600k @ $3.75/M + 400k @ $6/M = $2.25 + $2.40 = $4.65 → 4.65e9 nanos.
	require.EqualValues(t, 4_650_000_000, cost.CacheWriteCostNanos)
	require.EqualValues(t, 4_650_000_000, cost.CostNanos)

	// Without an explicit 1h rate the 1.6× premium applies.
	m2 := New(nil, nil, map[string]Price{"anthropic/x": {InputPerM: 3, OutputPerM: 15, CacheWritePerM: 3.75}})
	cost = m2.CalculateCost("anthropic", "x", core.Usage{PromptTokens: 1_000_000, CacheWriteTokens: 1_000_000, CacheWrite1hTokens: 1_000_000}, false, 0)
	require.EqualValues(t, 6_000_000_000, cost.CacheWriteCostNanos)
}

func TestWebSearchRequestsAreBilled(t *testing.T) {
	m := newTestMeter()
	u := core.Usage{PromptTokens: 1000, CompletionTokens: 100, WebSearchRequests: 3}
	cost := m.CalculateCost("anthropic", "claude-sonnet-4-5", u, false, 0)
	require.EqualValues(t, 30_000_000, cost.ToolCostNanos, "3 searches at $10/1k = $0.03")
	tokens := cost.InputCostNanos + cost.OutputCostNanos
	require.EqualValues(t, tokens+cost.ToolCostNanos, cost.CostNanos)
}

func TestServiceTierMultipliers(t *testing.T) {
	m := newTestMeter()
	u := core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}
	std := m.CalculateCostWith("openai", "gpt-5", u, false, 0, "")
	flex := m.CalculateCostWith("openai", "gpt-5", u, false, 0, "flex")
	prio := m.CalculateCostWith("openai", "gpt-5", u, false, 0, "priority")
	require.EqualValues(t, std.CostNanos/2, flex.CostNanos)
	require.EqualValues(t, std.CostNanos*2, prio.CostNanos)
	// Other providers ignore the tier.
	ant := m.CalculateCostWith("anthropic", "claude-sonnet-4-5", u, false, 0, "flex")
	require.EqualValues(t, m.CalculateCost("anthropic", "claude-sonnet-4-5", u, false, 0).CostNanos, ant.CostNanos)
}

func TestProviderReportedCostWins(t *testing.T) {
	m := newTestMeter()
	u := core.Usage{PromptTokens: 1000, CompletionTokens: 1000, ProviderCostNanos: 123_456_789}
	cost := m.CalculateCost("openrouter", "some/model", u, false, 0)
	require.EqualValues(t, 123_456_789, cost.CostNanos)
	require.Equal(t, "provider_reported", cost.Pricing.MatchKind)
	require.Equal(t, "priced", cost.Pricing.Status)
	// A cache hit never charges, even with a provider figure attached.
	hit := m.CalculateCost("openrouter", "some/model", u, true, 0)
	require.Zero(t, hit.CostNanos)
}

func TestPriceCandidatesStripFineTuneAndSnapshotDate(t *testing.T) {
	m := newTestMeter()
	for _, model := range []string{"gpt-4o-2024-08-06", "ft:gpt-4o:acme::abc123", "ft:gpt-4o-2024-08-06:acme::abc123"} {
		match := m.ResolvePrice("openai", model)
		require.Equal(t, "openai/gpt-4o", match.Key, model)
	}
	require.Equal(t, "gpt-4o", stripFineTuneAndDate("gpt-4o-2024-08-06"))
	require.Equal(t, "gpt-4o-mini", stripFineTuneAndDate("ft:gpt-4o-mini:org:suffix:id"))
	require.Equal(t, "claude-sonnet-4-5", stripFineTuneAndDate("claude-sonnet-4-5"), "a version suffix is not a date")
}
