package market

import (
	"math"
	"testing"
)

func TestComputeChainRateUsesCheapestSlugPair(t *testing.T) {
	models := []Model{
		{Slug: "a", MinAskIn: 1, MinAskOut: 5}, // sum 6
		{Slug: "b", MinAskIn: 2, MinAskOut: 3}, // sum 5 cheapest
	}
	rate, ok := ComputeChainRate([]string{"a", "b"}, models, 0, 0.1, 1.25)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.InputPerM != 2 || rate.OutputPerM != 3 {
		t.Fatalf("rate = (%v,%v), want slug b's (2,3)", rate.InputPerM, rate.OutputPerM)
	}
	if rate.CachedInputPerM != 0.2 || rate.CacheWritePerM != 2.5 {
		t.Fatalf("cache = (%v,%v), want (0.2,2.5)", rate.CachedInputPerM, rate.CacheWritePerM)
	}
}

func TestComputeChainRateMarkup(t *testing.T) {
	models := []Model{{Slug: "a", MinAskIn: 1, MinAskOut: 2}}
	rate, ok := ComputeChainRate([]string{"a"}, models, 20, 0.1, 1.25)
	if !ok || rate.InputPerM != 1.2 || rate.OutputPerM != 2.4 {
		t.Fatalf("got %+v ok=%v, want in=1.2 out=2.4", rate, ok)
	}
}

func TestComputeChainRateMissingSlugs(t *testing.T) {
	models := []Model{{Slug: "a", MinAskIn: 1, MinAskOut: 1}}
	if _, ok := ComputeChainRate([]string{"x"}, models, 0, 0.1, 1.25); ok {
		t.Fatal("unknown slug must return false")
	}
	if _, ok := ComputeChainRate(nil, models, 0, 0.1, 1.25); ok {
		t.Fatal("empty slugs must return false")
	}
}

func TestComputeChainRateRejectsNonFinite(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Model
	}{
		{"nan input", Model{Slug: "a", MinAskIn: math.NaN(), MinAskOut: 1}},
		{"inf input", Model{Slug: "a", MinAskIn: math.Inf(1), MinAskOut: 1}},
		{"nan output", Model{Slug: "a", MinAskIn: 1, MinAskOut: math.NaN()}},
		{"inf output", Model{Slug: "a", MinAskIn: 1, MinAskOut: math.Inf(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ComputeChainRate([]string{"a"}, []Model{tc.m}, 0, 0.1, 1.25); ok {
				t.Fatal("non-finite ask must return false")
			}
		})
	}
}

func TestComputeChainRateSkipsZeroOrNegativeAsk(t *testing.T) {
	// Only slug has a zero ask: must not price the chain at 0.
	zero := []Model{{Slug: "a", MinAskIn: 0, MinAskOut: 5}}
	if _, ok := ComputeChainRate([]string{"a"}, zero, 0, 0.1, 1.25); ok {
		t.Fatal("zero MinAskIn must be skipped, leaving chain unpriced")
	}
	zeroOut := []Model{{Slug: "a", MinAskIn: 5, MinAskOut: 0}}
	if _, ok := ComputeChainRate([]string{"a"}, zeroOut, 0, 0.1, 1.25); ok {
		t.Fatal("zero MinAskOut must be skipped, leaving chain unpriced")
	}
	negative := []Model{{Slug: "a", MinAskIn: -1, MinAskOut: 5}}
	if _, ok := ComputeChainRate([]string{"a"}, negative, 0, 0.1, 1.25); ok {
		t.Fatal("negative ask must be skipped, leaving chain unpriced")
	}

	// A zeroed slug alongside a valid one is simply ignored.
	mixed := []Model{
		{Slug: "a", MinAskIn: 0, MinAskOut: 0},
		{Slug: "b", MinAskIn: 2, MinAskOut: 3},
	}
	rate, ok := ComputeChainRate([]string{"a", "b"}, mixed, 0, 0.1, 1.25)
	if !ok {
		t.Fatal("expected valid slug to price the chain")
	}
	if rate.InputPerM != 2 || rate.OutputPerM != 3 {
		t.Fatalf("price = (%v,%v), want (2,3)", rate.InputPerM, rate.OutputPerM)
	}
}

func TestComputeChainRateUsesPublishedCacheRates(t *testing.T) {
	models := []Model{{Slug: "surplus:m", MinAskIn: 0.12, MinAskOut: 0.48, CacheRead: 0.02, CacheWrite: 0.15}}
	rate, ok := ComputeChainRate([]string{"surplus:m"}, models, 0, 0.1, 1.25)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.CachedInputPerM != 0.02 || rate.CacheWritePerM != 0.15 {
		t.Fatalf("cache = (%v,%v), want published (0.02,0.15)", rate.CachedInputPerM, rate.CacheWritePerM)
	}
}

func TestComputeChainRateDerivesCacheWhenUnpublished(t *testing.T) {
	models := []Model{{Slug: "a", MinAskIn: 1, MinAskOut: 2}}
	rate, ok := ComputeChainRate([]string{"a"}, models, 0, 0.1, 1.25)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.CachedInputPerM != 0.1 || rate.CacheWritePerM != 1.25 {
		t.Fatalf("cache = (%v,%v), want derived (0.1,1.25)", rate.CachedInputPerM, rate.CacheWritePerM)
	}
}

func TestComputeChainRateMixedSurplusAndInferhub(t *testing.T) {
	models := []Model{
		{Slug: "cbcn/x", MinAskIn: 0.20, MinAskOut: 0.50},
		{Slug: "surplus:m", MinAskIn: 0.12, MinAskOut: 0.48, CacheRead: 0.02, CacheWrite: 0.15},
	}
	rate, ok := ComputeChainRate([]string{"cbcn/x", "surplus:m"}, models, 0, 0.1, 1.25)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.InputPerM != 0.12 || rate.OutputPerM != 0.48 {
		t.Fatalf("rates = (%v,%v), want cheapest (0.12,0.48)", rate.InputPerM, rate.OutputPerM)
	}
}

func TestComputeChainRateSafetyFloorsToFailover(t *testing.T) {
	// Cheapest ask $0.10 but fallback offer at $0.30: with a 10% safety floor,
	// the billed rate must rise to 0.30*1.1 = 0.33, not stay at 0.10.
	models := []Model{{Slug: "surplus:m", MinAskIn: 0.10, MinAskOut: 0.40, FailoverIn: 0.30, FailoverOut: 0.60}}
	rate, ok := ComputeChainRateWithSafety([]string{"surplus:m"}, models, 0, 0.1, 1.25, true, 10)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.InputPerM != 0.33 || rate.OutputPerM != 0.66 {
		t.Fatalf("rates = (%v,%v), want (0.33,0.66)", rate.InputPerM, rate.OutputPerM)
	}
}

func TestComputeChainRateSafetyDisabledIgnoresFailover(t *testing.T) {
	models := []Model{{Slug: "surplus:m", MinAskIn: 0.10, MinAskOut: 0.40, FailoverIn: 0.30, FailoverOut: 0.60}}
	rate, ok := ComputeChainRateWithSafety([]string{"surplus:m"}, models, 0, 0.1, 1.25, false, 10)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.InputPerM != 0.10 || rate.OutputPerM != 0.40 {
		t.Fatalf("rates = (%v,%v), want best-ask (0.10,0.40)", rate.InputPerM, rate.OutputPerM)
	}
}

func TestComputeChainRateSafetyKeepsHigherMarkup(t *testing.T) {
	// Markup produces 0.10*5 = 0.50 > failover floor 0.30*1.1 = 0.33: keep 0.50.
	models := []Model{{Slug: "surplus:m", MinAskIn: 0.10, MinAskOut: 0.40, FailoverIn: 0.30, FailoverOut: 0.60}}
	rate, ok := ComputeChainRateWithSafety([]string{"surplus:m"}, models, 400, 0.1, 1.25, true, 10)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.InputPerM != 0.50 || rate.OutputPerM != 2.00 {
		t.Fatalf("rates = (%v,%v), want markup (0.50,2.00)", rate.InputPerM, rate.OutputPerM)
	}
}

func TestComputeChainRateSafetyInertWhenFailoverUnknown(t *testing.T) {
	models := []Model{{Slug: "surplus:m", MinAskIn: 0.10, MinAskOut: 0.40}}
	rate, ok := ComputeChainRateWithSafety([]string{"surplus:m"}, models, 0, 0.1, 1.25, true, 10)
	if !ok {
		t.Fatal("expected match")
	}
	if rate.InputPerM != 0.10 || rate.OutputPerM != 0.40 {
		t.Fatalf("rates = (%v,%v), want best-ask (0.10,0.40)", rate.InputPerM, rate.OutputPerM)
	}
}

// The chain rate comes from one real slug (the cheapest by input+output), never
// a mix of the cheapest input of one slug and the cheapest output of another.
func TestComputeChainRatePricesFromSingleCheapestSlug(t *testing.T) {
	models := []Model{
		{Slug: "p", MinAskIn: 0.001, MinAskOut: 0.010}, // sum 0.011
		{Slug: "q", MinAskIn: 0.005, MinAskOut: 0.002}, // sum 0.007 cheapest
	}
	r, ok := ComputeChainRate([]string{"p", "q"}, models, 0, CacheReadMult, CacheWriteMult)
	if !ok {
		t.Fatal("expected a rate")
	}
	if r.InputPerM != 0.005 || r.OutputPerM != 0.002 {
		t.Fatalf("rate = %v/%v, want q's 0.005/0.002", r.InputPerM, r.OutputPerM)
	}
}
