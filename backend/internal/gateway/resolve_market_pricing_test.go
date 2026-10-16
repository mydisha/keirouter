package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// A chain that does not reorder by market must still bill each step at its own
// slug's rate. Before the fix the steps carried only the chain aggregate (the
// cheapest slug), so a request served by the dearer step was billed cheap.
func TestUnorderedChainBillsServingStepSlug(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{
		{Slug: "cheap/slug", MinAskIn: 0.10, MinAskOut: 0.20},
		{Slug: "dear/slug", MinAskIn: 1.00, MinAskOut: 2.00},
	})
	chains := &fakeChains{chains: []store.Chain{{
		ID: "c1", Name: "flash",
		// Chain aggregate = cheapest slug + 10%.
		InputPerM: 0.11, OutputPerM: 0.22,
		MarketSlugs: []string{"cheap/slug", "dear/slug"},
		Steps: []store.ChainStep{
			{Position: 0, Provider: "p-cheap", Model: "m", MarketSlug: "cheap/slug"},
			{Position: 1, Provider: "p-dear", Model: "m", MarketSlug: "dear/slug"},
		},
	}}}

	res, err := resolveTargetsWith(context.Background(), chains, &fakeAliases{}, nil, cache, 10, "t1", "chain:flash")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := providers(res.Targets); len(got) != 2 || got[0] != "p-cheap" || got[1] != "p-dear" {
		t.Fatalf("declared order must be kept without ReorderByMarket, got %v", got)
	}
	dear := res.Targets[1]
	if dear.MarketRateIn < 1.0999 || dear.MarketRateIn > 1.1001 || dear.MarketRateOut < 2.1999 || dear.MarketRateOut > 2.2001 {
		t.Fatalf("dear step rates = %v/%v, want ~1.1/2.2", dear.MarketRateIn, dear.MarketRateOut)
	}
	if dear.UpstreamRateIn != 1.00 || dear.UpstreamRateOut != 2.00 {
		t.Fatalf("dear step upstream rates = %v/%v, want 1/2", dear.UpstreamRateIn, dear.UpstreamRateOut)
	}

	// The fallback step served the request: the meter must bill dear/slug.
	capt := &slugCaptureStore{}
	mtr := meter.New(capt, nil, nil)
	if _, err := mtr.Record(context.Background(), meter.Event{
		TenantID: store.DefaultTenantID, Provider: dear.Provider, Model: dear.Model,
		InputPerM: dear.InputPerM, OutputPerM: dear.OutputPerM,
		MarketSlug: dear.MarketSlug, MarketRateIn: dear.MarketRateIn, MarketRateOut: dear.MarketRateOut,
		Status: "success",
		Usage:  core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, Source: core.UsageSourceProvider},
	}); err != nil {
		t.Fatalf("meter.Record: %v", err)
	}
	if capt.last.PricingKey != "dear/slug" {
		t.Fatalf("PricingKey = %q, want dear/slug", capt.last.PricingKey)
	}
	// 1.1 + 2.2 = 3.3 USD.
	if capt.last.CostNanos != 3_300_000_000 {
		t.Fatalf("CostNanos = %d, want 3300000000", capt.last.CostNanos)
	}
}

// A step whose slug has no live quote is skipped; routing continues with the
// steps that can be priced.
func TestStepWithUnavailableSlugIsSkipped(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{
		{Slug: "live/slug", MinAskIn: 0.5, MinAskOut: 1.0},
		{Slug: "zero/slug", MinAskIn: 0, MinAskOut: 0},
	})
	chains := &fakeChains{chains: []store.Chain{{
		ID: "c1", Name: "flash",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "p-gone", Model: "m", MarketSlug: "gone/slug"},
			{Position: 1, Provider: "p-zero", Model: "m", MarketSlug: "zero/slug"},
			{Position: 2, Provider: "p-live", Model: "m", MarketSlug: "live/slug"},
			{Position: 3, Provider: "p-plain", Model: "m"},
		},
	}}}

	res, err := resolveTargetsWith(context.Background(), chains, &fakeAliases{}, nil, cache, 0, "t1", "chain:flash")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := providers(res.Targets); len(got) != 2 || got[0] != "p-live" || got[1] != "p-plain" {
		t.Fatalf("targets = %v, want [p-live p-plain]", got)
	}
}

// When every step is bound to a slug and none is quoted, the chain is sold out
// rather than routed at an unknown price.
func TestChainSoldOutWhenNoSlugIsQuoted(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace(nil)
	chains := &fakeChains{chains: []store.Chain{{
		ID: "c1", Name: "flash",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "p1", Model: "m", MarketSlug: "a/slug"},
			{Position: 1, Provider: "p2", Model: "m", MarketSlug: "b/slug"},
		},
	}}}

	_, err := resolveTargetsWith(context.Background(), chains, &fakeAliases{}, nil, cache, 0, "t1", "chain:flash")
	bad, ok := err.(badModelError)
	if !ok || bad.HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want 503 sold out", err)
	}
}

// An inferhub-slugged step bills inferhub's reported cost × markup, bids up to
// the slug's maxAsk (so inferhub picks any available seller instead of
// refusing when the cheapest asks are busy), and falls back to that cap rate
// and the per-request minimum when no cost is reported.
func TestInferhubStepBillsReportedCostWithMaxAskBid(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{
		{Slug: "cb/x", MinAskIn: 0.00015, MinAskOut: 0.0006, MaxAskIn: 0.075, MaxAskOut: 0.3},
		{Slug: "surplus:x", MinAskIn: 0.1, MinAskOut: 0.2},
	})
	chains := &fakeChains{chains: []store.Chain{{
		ID: "c1", Name: "flash",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "inferhub", Model: "cb/x", MarketSlug: "cb/x"},
			{Position: 1, Provider: "surplus", Model: "x", MarketSlug: "surplus:x"},
		},
	}}}
	res, err := resolveTargetsWith(context.Background(), chains, &fakeAliases{}, nil, cache, 10, "t1", "chain:flash")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ih := res.Targets[0]
	if !ih.BillUpstreamCost || ih.UpstreamCostMult < 1.0999 || ih.UpstreamCostMult > 1.1001 {
		t.Fatalf("inferhub step: BillUpstreamCost=%v mult=%v, want true/1.1", ih.BillUpstreamCost, ih.UpstreamCostMult)
	}
	if ih.BidCapIn != 0.075 || ih.BidCapOut != 0.3 {
		t.Fatalf("bid cap = %v/%v, want maxAsk 0.075/0.3", ih.BidCapIn, ih.BidCapOut)
	}
	if ih.UpstreamRateIn != 0.075 || ih.UpstreamRateOut != 0.3 {
		t.Fatalf("fallback upstream rates = %v/%v, want the cap", ih.UpstreamRateIn, ih.UpstreamRateOut)
	}
	if ih.MinChargeMicro != market.InferhubMinChargeMicro {
		t.Fatalf("min charge = %v, want %v", ih.MinChargeMicro, market.InferhubMinChargeMicro)
	}
	if s := res.Targets[1]; s.BillUpstreamCost || s.BidCapIn != 0 || s.MinChargeMicro != 0 {
		t.Fatalf("surplus step must keep rate billing: %+v", s)
	}
}

// A direct provider/model call whose model is a live inferhub slug is priced
// like a chain step on that slug: reported cost × markup, maxAsk bid cap.
// Without this it bypassed the markup (non-stream) or billed nothing (stream).
func TestDirectCallOnInferhubSlugIsMarketPriced(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{
		{Slug: "cb/x", MinAskIn: 0.00015, MinAskOut: 0.0006, MaxAskIn: 0.075, MaxAskOut: 0.3},
		{Slug: "surplus:y", MinAskIn: 0.1, MinAskOut: 0.2},
	})
	res, err := resolveTargetsWith(context.Background(), &fakeChains{}, &fakeAliases{}, nil, cache, 10, "t1", "custom-openai-inferhub/cb/x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := res.Targets[0]
	if got.Provider != "custom-openai-inferhub" || got.Model != "cb/x" || got.MarketSlug != "cb/x" {
		t.Fatalf("target = %+v, want inferhub cb/x bound to slug cb/x", got)
	}
	if !got.BillUpstreamCost || got.BidCapIn != 0.075 || got.UpstreamCostMult < 1.0999 || got.UpstreamCostMult > 1.1001 {
		t.Fatalf("direct inferhub target not market priced: %+v", got)
	}

	for _, model := range []string{"openai/gpt-4o", "custom-openai-surplus/surplus:y", "custom-openai-inferhub/unlisted/x"} {
		res, err := resolveTargetsWith(context.Background(), &fakeChains{}, &fakeAliases{}, nil, cache, 10, "t1", model)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if tg := res.Targets[0]; tg.MarketSlug != "" || tg.BillUpstreamCost {
			t.Fatalf("%s must stay unpriced by the market: %+v", model, tg)
		}
	}
}
