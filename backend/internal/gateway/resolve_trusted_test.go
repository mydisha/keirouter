package gateway

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/market"
)

func TestStampSurplusTrustedFlagsStepAndUsesCapAsFallbackRate(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 0.0025, BestOut: 0.015, CapIn: 0.012, CapOut: 0.072, MinDiscountPct: 94,
	}.Model("gpt-5.6-luna")})
	steps := []dispatch.Target{
		{Provider: "custom-openai-surplus", Model: "gpt-5.6-luna", MarketSlug: "surplus-trusted:gpt-5.6-luna"},
		{Provider: "openai", Model: "gpt-4o"},
	}
	out := stampSurplusTrusted(steps, nil, cache, 10)
	s := out[0]
	if !s.BillUpstreamCost || s.MinDiscountPct != 94 {
		t.Fatalf("step = %+v, want billed from upstream cost with ceiling 94", s)
	}
	if s.UpstreamCostMult < 1.0999 || s.UpstreamCostMult > 1.1001 {
		t.Fatalf("mult = %v, want 1.1", s.UpstreamCostMult)
	}
	// Without a reported cost the bill falls back to the ceiling, never the
	// cheapest ask, so a Surplus failover cannot undercut it.
	if s.UpstreamRateIn != 0.012 || s.UpstreamRateOut != 0.072 {
		t.Fatalf("upstream fallback = (%v,%v), want cap", s.UpstreamRateIn, s.UpstreamRateOut)
	}
	if s.MarketRateIn < 0.01319 || s.MarketRateIn > 0.01321 {
		t.Fatalf("market fallback in = %v, want cap×1.1", s.MarketRateIn)
	}
	if out[1].BillUpstreamCost || out[1].MinDiscountPct != 0 {
		t.Fatal("non-trusted step must be untouched")
	}
}

func TestStampSurplusTrustedAdoptsChainSlugByModel(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{Found: true, BestIn: 1, BestOut: 2, CapIn: 3, CapOut: 4, MinDiscountPct: 50}.Model("glm-5.3")})
	steps := []dispatch.Target{{Provider: "custom-openai-surplus", Model: "glm-5.3"}}
	out := stampSurplusTrusted(steps, []string{"surplus-trusted:glm-5.3"}, cache, 0)
	if out[0].MarketSlug != "surplus-trusted:glm-5.3" || !out[0].BillUpstreamCost || out[0].MinDiscountPct != 50 {
		t.Fatalf("step = %+v, want chain-level trusted slug adopted", out[0])
	}
}

func TestStampSurplusTrustedMissingQuoteStillBillsUpstreamCost(t *testing.T) {
	steps := []dispatch.Target{{Provider: "custom-openai-surplus", Model: "x", MarketSlug: "surplus-trusted:x"}}
	out := stampSurplusTrusted(steps, nil, market.NewSnapshotCache(), 10)
	if !out[0].BillUpstreamCost || out[0].MinDiscountPct != 0 {
		t.Fatalf("step = %+v, want upstream-cost billing without a ceiling", out[0])
	}
}
