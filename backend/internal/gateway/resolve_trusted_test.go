package gateway

import (
	"context"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestStampSurplusTrustedFlagsStepAndUsesCapAsFallbackRate(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 0.0025, BestOut: 0.015, CapIn: 0.012, CapOut: 0.072, MinDiscountPct: 94,
		Providers: []string{"bedrock", "openai"},
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
	if s.UpstreamProviders != "bedrock,openai" {
		t.Fatalf("allow-list = %v, want [bedrock openai]", s.UpstreamProviders)
	}
	if out[1].BillUpstreamCost || out[1].MinDiscountPct != 0 || out[1].UpstreamProviders != "" {
		t.Fatal("non-trusted step must be untouched")
	}
}

func TestStampSurplusTrustedAdoptsChainSlugByModel(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{Found: true, BestIn: 1, BestOut: 2, CapIn: 3, CapOut: 4, MinDiscountPct: 50, Providers: []string{"zai"}}.Model("glm-5.3")})
	steps := []dispatch.Target{{Provider: "custom-openai-surplus", Model: "glm-5.3"}}
	out := stampSurplusTrusted(steps, []string{"surplus-trusted:glm-5.3"}, cache, 0)
	if out[0].MarketSlug != "surplus-trusted:glm-5.3" || !out[0].BillUpstreamCost || out[0].MinDiscountPct != 50 {
		t.Fatalf("step = %+v, want chain-level trusted slug adopted", out[0])
	}
}

func TestStampSurplusTrustedDropsStepWithoutTrustedQuote(t *testing.T) {
	// No quote means no trusted allow-list: sending the request anyway would let
	// Surplus pick an untrusted seller, so the step is skipped.
	steps := []dispatch.Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "custom-openai-surplus", Model: "x", MarketSlug: "surplus-trusted:x"},
		{Provider: "anthropic", Model: "claude"},
	}
	out := stampSurplusTrusted(steps, nil, market.NewSnapshotCache(), 10)
	if len(out) != 2 || out[0].Model != "gpt-4o" || out[1].Model != "claude" {
		t.Fatalf("out = %+v, want the trusted step dropped and order kept", out)
	}
	if steps[1].Model != "x" {
		t.Fatal("input slice must not be mutated")
	}
	empty := market.NewSnapshotCache()
	empty.Replace([]market.Model{market.TrustedQuote{Found: true, BestIn: 1, BestOut: 2}.Model("x")})
	if out := stampSurplusTrusted(steps[1:2], nil, empty, 10); len(out) != 0 {
		t.Fatalf("quote without providers must drop the step, got %+v", out)
	}
}

func TestChainResultUsesChainMarkupOverride(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{Found: true, BestIn: 1, BestOut: 2, CapIn: 3, CapOut: 4, Providers: []string{"p"}}.Model("m")})
	override := 50.0
	chains := &fakeChains{chains: []store.Chain{
		{ID: "a", Name: "own", MarkupPercent: &override, Steps: []store.ChainStep{{Provider: "p", Model: "m", MarketSlug: "surplus-trusted:m"}}},
		{ID: "b", Name: "global", Steps: []store.ChainStep{{Provider: "p", Model: "m", MarketSlug: "surplus-trusted:m"}}},
	}}
	for name, want := range map[string]float64{"own": 1.5, "global": 1.1} {
		res, err := chainResultWith(context.Background(), chains, nil, cache, 10, store.DefaultTenantID, name)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Targets[0].UpstreamCostMult; got < want-1e-9 || got > want+1e-9 {
			t.Fatalf("%s mult = %v, want %v", name, got, want)
		}
	}
}
