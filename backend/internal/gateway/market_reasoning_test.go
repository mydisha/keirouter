package gateway

import (
	"math"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/market"
)

func TestPriceMarketStepsCarriesReasoningRate(t *testing.T) {
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{{Slug: "surplus:m", MinAskIn: 0.1, MinAskOut: 0.4, Reasoning: 1.0}})

	steps := priceMarketSteps([]dispatch.Target{{Provider: "p", Model: "m", MarketSlug: "surplus:m"}}, cache, 50)

	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if got := steps[0].MarketReasoningRate; math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("billable reasoning = %v, want 1.5", got)
	}
	if got := steps[0].UpstreamReasoningRate; got != 1.0 {
		t.Fatalf("upstream reasoning = %v, want 1.0", got)
	}
}
