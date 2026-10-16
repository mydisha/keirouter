package pipeline

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
)

func TestWithTargetHeadersAddsMinDiscountWithoutMutatingShared(t *testing.T) {
	shared := map[string]string{"X-Keep": "1"}
	a := dispatch.Attempt{Target: dispatch.Target{MinDiscountPct: 94}, Creds: core.Credentials{Headers: shared}}
	got := withTargetHeaders(a)
	if got.Headers["X-Min-Discount"] != "94" || got.Headers["X-Keep"] != "1" {
		t.Fatalf("headers = %v", got.Headers)
	}
	if _, leaked := shared["X-Min-Discount"]; leaked {
		t.Fatal("shared prepared-credential headers were mutated")
	}
	plain := withTargetHeaders(dispatch.Attempt{Creds: core.Credentials{Headers: shared}})
	if _, ok := plain.Headers["X-Min-Discount"]; ok {
		t.Fatal("no ceiling must send no header")
	}
}

func TestMergeUsageKeepsReportedUpstreamCost(t *testing.T) {
	u := mergeUsage(core.Usage{PromptTokens: 5}, core.Usage{CompletionTokens: 3, UpstreamCostMicro: 12, UpstreamCostReported: true})
	if !u.UpstreamCostReported || u.UpstreamCostMicro != 12 {
		t.Fatalf("usage = %+v", u)
	}
	u = mergeUsage(u, core.Usage{CompletionTokens: 4})
	if !u.UpstreamCostReported || u.UpstreamCostMicro != 12 {
		t.Fatal("a later frame without cost must not erase the reported cost")
	}
}

func TestAddAttemptUsageRequiresEveryAttemptReported(t *testing.T) {
	a := core.Usage{PromptTokens: 10, CompletionTokens: 1, UpstreamCostMicro: 5, UpstreamCostReported: true}
	b := core.Usage{PromptTokens: 10, CompletionTokens: 2, UpstreamCostMicro: 7, UpstreamCostReported: true}
	sum := addAttemptUsage(addAttemptUsage(core.Usage{}, a), b)
	if !sum.UpstreamCostReported || sum.UpstreamCostMicro != 12 {
		t.Fatalf("sum = %+v, want reported 12", sum)
	}
	unreported := core.Usage{PromptTokens: 10, CompletionTokens: 2}
	mixed := addAttemptUsage(addAttemptUsage(core.Usage{}, a), unreported)
	if mixed.UpstreamCostReported {
		t.Fatal("an attempt without a reported cost must fall back to rate billing")
	}
}

// An inferhub-slugged step sends its bid cap as the price limit headers.
func TestWithTargetHeadersSendsInferhubBidCap(t *testing.T) {
	shared := map[string]string{"X-Keep": "1"}
	a := dispatch.Attempt{
		Target: dispatch.Target{MarketSlug: "alicn/deepseek-v4.1-flash", BidCapIn: 0.075, BidCapOut: 0.3, UpstreamRateIn: 9, UpstreamRateOut: 9},
		Creds:  core.Credentials{Headers: shared},
	}
	got := withTargetHeaders(a)
	if got.Headers["x-max-input-price"] != "0.075" || got.Headers["x-max-output-price"] != "0.3" {
		t.Fatalf("headers = %v", got.Headers)
	}
	if got.Headers["X-Keep"] != "1" {
		t.Fatal("existing headers must be kept")
	}
	if _, leaked := shared["x-max-input-price"]; leaked {
		t.Fatal("shared prepared-credential headers were mutated")
	}
}

func TestWithTargetHeadersNoCapWithoutInferhubQuote(t *testing.T) {
	for name, tgt := range map[string]dispatch.Target{
		"no slug":  {BidCapIn: 1, BidCapOut: 2},
		"no cap":   {MarketSlug: "ali/x", UpstreamRateIn: 1, UpstreamRateOut: 2},
		"half cap": {MarketSlug: "ali/x", BidCapIn: 1},
	} {
		got := withTargetHeaders(dispatch.Attempt{Target: tgt})
		if _, ok := got.Headers["x-max-input-price"]; ok {
			t.Fatalf("%s: must not send a price cap, headers = %v", name, got.Headers)
		}
	}
}

// inferhub reports its charge as usage.cost (ProviderCostNanos), which must
// count as charged just like Surplus's buyer_cost_micro.
func TestUpstreamChargedCountsProviderReportedCost(t *testing.T) {
	charged := []core.StreamChunk{{Type: core.ChunkUsage, Usage: &core.Usage{ProviderCostNanos: 10_000}}}
	if !upstreamCharged(charged) {
		t.Fatal("a usage frame with a provider-reported cost must count as charged")
	}
	free := []core.StreamChunk{{Type: core.ChunkUsage, Usage: &core.Usage{CompletionTokens: 3}}}
	if upstreamCharged(free) {
		t.Fatal("a usage frame without any cost must not count as charged")
	}
}

// The raw stream path must keep the cost the upstream reports in usage.cost
// (inferhub, OpenRouter); dropping it billed direct inferhub streams at zero.
func TestExtractUsageFromStreamKeepsReportedCost(t *testing.T) {
	raw := []byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":27,\"completion_tokens\":21,\"total_tokens\":48}}\n\n" +
		"data: {\"choices\":[],\"object\":\"chat.completion.chunk\",\"usage\":{\"completion_tokens\":21,\"cost\":0.00001,\"prompt_tokens\":27,\"total_tokens\":107}}\n\n" +
		"data: [DONE]\n\n")
	u := extractUsageFromStream(raw)
	if u.ProviderCostNanos != 10_000 {
		t.Fatalf("ProviderCostNanos = %d, want 10000", u.ProviderCostNanos)
	}
	if u.PromptTokens != 27 || u.CompletionTokens != 21 {
		t.Fatalf("tokens = %d/%d, want 27/21", u.PromptTokens, u.CompletionTokens)
	}
}
