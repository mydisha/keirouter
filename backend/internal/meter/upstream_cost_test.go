package meter

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// surplusTrustedEvent mimics a surplus-trusted step: cap rates are the
// fallback, the reported upstream cost is the source of truth.
func surplusTrustedEvent(u core.Usage) Event {
	return Event{
		MarketSlug: "surplus-trusted:gpt-5.6-luna",
		// Cap $0.012/M in, $0.072/M out; ×1.1 markup for the user.
		MarketRateIn: 0.0132, MarketRateOut: 0.0792,
		MarketCacheReadRate: 0.00132, MarketCacheWriteRate: 0.0165,
		UpstreamRateIn: 0.012, UpstreamRateOut: 0.072,
		UpstreamCacheReadRate: 0.0012, UpstreamCacheWriteRate: 0.015,
		BillUpstreamCost: true, UpstreamCostMult: 1.1,
		Provider: "custom-openai-surplus", Model: "gpt-5.6-luna",
		Usage: u,
	}
}

func TestReportedUpstreamCostDrivesUserAndOperatorCost(t *testing.T) {
	ev := surplusTrustedEvent(core.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
		UpstreamCostMicro: 40, UpstreamCostReported: true})
	m := &Meter{}
	user := m.costForEvent(ev)
	up := upstreamCostForEvent(ev)
	if up.CostNanos != 40_000 || up.CostMicros != 40 {
		t.Fatalf("upstream = %d nanos / %d micros, want exactly the reported 40 micros", up.CostNanos, up.CostMicros)
	}
	if user.CostNanos != 44_000 || user.CostMicros != 44 {
		t.Fatalf("user = %d nanos / %d micros, want 40×1.1 = 44 micros", user.CostNanos, user.CostMicros)
	}
	if user.Pricing.Source != "upstream_cost" {
		t.Fatalf("pricing source = %q, want upstream_cost", user.Pricing.Source)
	}
	parts := user.InputCostNanos + user.CachedCostNanos + user.CacheWriteCostNanos + user.OutputCostNanos + user.ReasoningCostNanos
	if parts != user.CostNanos {
		t.Fatalf("breakdown sums to %d, want %d", parts, user.CostNanos)
	}
	if user.CostNanos < up.CostNanos {
		t.Fatal("user must never be billed below upstream cost")
	}
}

func TestUnreportedUpstreamCostFallsBackToCapRate(t *testing.T) {
	ev := surplusTrustedEvent(core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000})
	m := &Meter{}
	user := m.costForEvent(ev)
	up := upstreamCostForEvent(ev)
	// 1M in × 0.012 + 1M out × 0.072 = $0.084 = 84_000 micros (operator cap).
	if up.CostMicros != 84_000 {
		t.Fatalf("upstream fallback = %d micros, want 84000", up.CostMicros)
	}
	if user.CostMicros != 92_400 {
		t.Fatalf("user fallback = %d micros, want 92400 (cap × 1.1)", user.CostMicros)
	}
}

func TestReportedZeroUpstreamCostIsFree(t *testing.T) {
	ev := surplusTrustedEvent(core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		UpstreamCostReported: true})
	user := (&Meter{}).costForEvent(ev)
	if user.CostNanos != 0 {
		t.Fatalf("user = %d, want 0 for a reported-free request", user.CostNanos)
	}
}

func TestUpstreamCostIgnoredWhenNotBilledFromUpstream(t *testing.T) {
	ev := surplusTrustedEvent(core.Usage{PromptTokens: 1_000_000, CompletionTokens: 0, TotalTokens: 1_000_000,
		UpstreamCostMicro: 1, UpstreamCostReported: true})
	ev.BillUpstreamCost = false
	user := (&Meter{}).costForEvent(ev)
	if user.CostMicros != 13_200 {
		t.Fatalf("user = %d micros, want rate pricing 13200", user.CostMicros)
	}
}
