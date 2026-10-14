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
