package meter

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestCostMicrosUsesExactModelPriceBeforeProviderFallback(t *testing.T) {
	m := New(nil,
		map[string]Price{
			"anthropic": {InputPerM: 1, OutputPerM: 2},
		},
		map[string]Price{
			"anthropic/claude-opus-4-7": {InputPerM: 15, OutputPerM: 75},
		},
	)

	cost := m.CostMicros("anthropic", "claude-opus-4-7", core.Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
	}, false)

	if cost != 90_000_000 {
		t.Fatalf("CostMicros() = %d, want 90000000", cost)
	}
}

func TestCostMicrosFallsBackToProviderPrice(t *testing.T) {
	m := New(nil,
		map[string]Price{
			"anthropic": {InputPerM: 1, OutputPerM: 2},
		},
		nil,
	)

	cost := m.CostMicros("anthropic", "unknown-model", core.Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
	}, false)

	if cost != 3_000_000 {
		t.Fatalf("CostMicros() = %d, want 3000000", cost)
	}
}

func TestCostMicrosAppliesCacheAndReasoningRates(t *testing.T) {
	m := New(nil, nil, map[string]Price{
		"openai/o4-mini": {
			InputPerM:       2,
			OutputPerM:      8,
			CachedInputPerM: 0.5,
			CacheWritePerM:  2.5,
			ReasoningPerM:   8,
		},
	})

	cost := m.CostMicros("openai", "o4-mini", core.Usage{
		PromptTokens:     1_600,
		CompletionTokens: 200,
		CachedTokens:     500,
		CacheWriteTokens: 100,
		ReasoningTokens:  50,
	}, false)

	// Reasoning tokens are already included in completion tokens, so only the
	// non-reasoning remainder is charged at the regular output rate.
	if cost != 4_100 {
		t.Fatalf("CostMicros() = %d, want 4100", cost)
	}
}

func TestCostMicrosCacheHitIsFree(t *testing.T) {
	m := New(nil, map[string]Price{
		"openai": {InputPerM: 100, OutputPerM: 100},
	}, nil)

	cost := m.CostMicros("openai", "gpt-5", core.Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
	}, true)

	if cost != 0 {
		t.Fatalf("CostMicros() = %d, want 0", cost)
	}
}

func TestRecordPrefersMarketSlugOverChainRate(t *testing.T) {
	// Chain aggregate is cheap (1/1); winning step's market slug is pricier.
	m := New(nil, nil, map[string]Price{})
	ev := Event{
		Provider: "commandcode", Model: "deepseek/deepseek-v4-pro",
		InputPerM: 1, OutputPerM: 1, // chain aggregate
		MarketSlug:   "cmc/deepseek/deepseek-v4-pro",
		MarketRateIn: 0.15, MarketRateOut: 0.45, // already post-markup
		Usage: core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
	}
	bd := m.costForEvent(ev)
	if bd.Pricing.Source != "market_slug" {
		t.Fatalf("source = %q, want market_slug", bd.Pricing.Source)
	}
	if bd.Pricing.Key != "cmc/deepseek/deepseek-v4-pro" {
		t.Fatalf("key = %q", bd.Pricing.Key)
	}
	if bd.Pricing.MatchKind != "market_slug" {
		t.Fatalf("match kind = %q, want market_slug", bd.Pricing.MatchKind)
	}
	// cost = 0.15 + 0.45 = 0.60 USD = 600_000_000 nanos
	if bd.CostNanos != 600_000_000 {
		t.Fatalf("CostNanos = %d, want 600000000", bd.CostNanos)
	}
}

func TestUpstreamCostUsesPreMarkupRate(t *testing.T) {
	ev := Event{
		Provider: "commandcode", Model: "deepseek/deepseek-v4.1-flash",
		MarketSlug:     "cmc/deepseek/deepseek-v4.1-flash",
		MarketRateIn:   0.14, MarketRateOut: 0.42,
		UpstreamRateIn: 0.10, UpstreamRateOut: 0.30,
		Usage:          core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
	}
	if up := upstreamCostForEvent(ev); up.CostNanos != 400_000_000 {
		t.Fatalf("upstream CostNanos = %d, want 400000000", up.CostNanos)
	}
}

func TestMarketReasoningRateBillsReasoningTokens(t *testing.T) {
	m := New(nil, nil, map[string]Price{})
	ev := Event{
		Provider: "custom-openai-surplus", Model: "deepseek-v4.1-flash",
		MarketSlug:   "surplus:deepseek-v4.1-flash",
		MarketRateIn: 0.10, MarketRateOut: 0.40, MarketReasoningRate: 1.00,
		UpstreamRateIn: 0.05, UpstreamRateOut: 0.20, UpstreamReasoningRate: 0.50,
		Usage: core.Usage{PromptTokens: 1_000_000, CompletionTokens: 2_000_000, ReasoningTokens: 1_000_000},
	}
	bd := m.costForEvent(ev)
	if bd.ReasoningRatePerM != 1.00 || bd.OutputRatePerM != 0.40 {
		t.Fatalf("rates = (out %v, reasoning %v), want (0.40, 1.00)", bd.OutputRatePerM, bd.ReasoningRatePerM)
	}
	// 0.10 input + 0.40 regular output + 1.00 reasoning = 1.50 USD
	if bd.CostNanos != 1_500_000_000 {
		t.Fatalf("CostNanos = %d, want 1500000000", bd.CostNanos)
	}
	// 0.05 + 0.20 + 0.50 = 0.75 USD
	if up := upstreamCostForEvent(ev); up.CostNanos != 750_000_000 {
		t.Fatalf("upstream CostNanos = %d, want 750000000", up.CostNanos)
	}

	// Unpublished reasoning rate falls back to the output rate.
	ev.MarketReasoningRate, ev.UpstreamReasoningRate = 0, 0
	bd = m.costForEvent(ev)
	if bd.ReasoningRatePerM != 0.40 || bd.CostNanos != 900_000_000 {
		t.Fatalf("fallback = (reasoning %v, cost %d), want (0.40, 900000000)", bd.ReasoningRatePerM, bd.CostNanos)
	}
}

func TestUpstreamCostZeroWithoutSlug(t *testing.T) {
	ev := Event{
		Provider: "openai", Model: "gpt-4o",
		InputPerM: 1, OutputPerM: 1,
		Usage: core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
	}
	if up := upstreamCostForEvent(ev); up.CostNanos != 0 {
		t.Fatalf("upstream CostNanos = %d, want 0 (unknown upstream)", up.CostNanos)
	}
}

func TestCostForEventFlagsFallbackWhenSlugCostsMore(t *testing.T) {
	m := New(nil, nil, map[string]Price{})
	// Cheapest aggregate is 0.11; winning slug bills 0.30 -> fallback.
	ev := Event{
		Provider: "commandcode", Model: "deepseek-v4.1-flash",
		InputPerM: 0.11, OutputPerM: 0.11,
		MarketSlug: "cmc/deepseek/deepseek-v4.1-flash", MarketRateIn: 0.30, MarketRateOut: 0.30,
		CheapestRateIn: 0.11,
		Usage:          core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
	}
	bd := m.costForEvent(ev)
	if bd.FallbackRatePerM != 0.11 {
		t.Fatalf("FallbackRatePerM = %v, want 0.11", bd.FallbackRatePerM)
	}
	// Same slug but priced at the cheapest rate -> not a fallback.
	ev.MarketRateIn, ev.MarketRateOut = 0.11, 0.11
	if bd := m.costForEvent(ev); bd.FallbackRatePerM != 0 {
		t.Fatalf("FallbackRatePerM = %v, want 0 (at cheapest)", bd.FallbackRatePerM)
	}
}

func TestRecordFallsBackToChainWhenNoSlug(t *testing.T) {
	m := New(nil, nil, map[string]Price{})
	ev := Event{Provider: "openai", Model: "gpt-4o", InputPerM: 2, OutputPerM: 4,
		Usage: core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}}
	bd := m.costForEvent(ev)
	if bd.Pricing.Source != "chain" {
		t.Fatalf("source = %q, want chain", bd.Pricing.Source)
	}
}

func TestMarketPriceUnsetWhenSlugEmptyOrRatesZero(t *testing.T) {
	if _, ok := marketPrice(Event{MarketRateIn: 1, MarketRateOut: 1}); ok {
		t.Fatalf("empty slug should not be billable")
	}
	if _, ok := marketPrice(Event{MarketSlug: "x/y"}); ok {
		t.Fatalf("zero rates should not be billable")
	}
	if _, ok := marketPrice(Event{MarketSlug: "x/y", MarketRateIn: 0.1}); !ok {
		t.Fatalf("positive input rate should be billable")
	}
}
