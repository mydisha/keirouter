package gateway

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestChainUsageRowMapsServedTargetsOntoSteps(t *testing.T) {
	chain := store.Chain{
		ID: "c1", Name: "coding", Strategy: "fallback",
		// Positions deliberately out of slice order: steps must sort by position.
		Steps: []store.ChainStep{
			{Provider: "codex", Model: "gpt-5", Position: 1},
			{Provider: "anthropic", Model: "sonnet", Position: 0},
		},
		FallbackProvider: "deepseek", FallbackModel: "chat",
	}
	rows := []store.ChainServedUsage{
		{ChainID: "c1", Provider: "anthropic", Model: "sonnet", Requests: 6, Successes: 6},
		{ChainID: "c1", Provider: "codex", Model: "gpt-5", Requests: 4, Successes: 2, Failures: 1, FellBack: 3},
		{ChainID: "c1", Provider: "deepseek", Model: "chat", Requests: 0},
		{ChainID: "c1", Provider: "old", Model: "removed-step", Requests: 1, Successes: 1},
	}
	out := chainUsageRow(chain, rows)

	if out["requests"].(int64) != 11 || out["fallback_requests"].(int64) != 3 {
		t.Fatalf("totals: requests=%v fallbacks=%v", out["requests"], out["fallback_requests"])
	}
	steps := out["steps"].([]map[string]any)
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3 (two steps + last-resort fallback)", len(steps))
	}
	if steps[0]["provider"] != "anthropic" || steps[0]["requests"].(int64) != 6 {
		t.Fatalf("first step = %v", steps[0])
	}
	// codex served 4 requests: 2 succeeded, 1 failed, 1 was a guardrail
	// block that counts toward neither side of the success rate.
	if steps[1]["provider"] != "codex" || steps[1]["requests"].(int64) != 4 ||
		steps[1]["success_rate"].(float64) != 2.0/3.0 {
		t.Fatalf("second step = %v", steps[1])
	}
	if out["success_rate"].(float64) != 9.0/10.0 {
		t.Fatalf("chain success rate = %v, want 0.9", out["success_rate"])
	}
	if steps[2]["success_rate"].(float64) != 1 {
		t.Fatalf("idle fallback step should report 1, got %v", steps[2]["success_rate"])
	}
	if steps[2]["is_fallback"] != true {
		t.Fatalf("last-resort model should be flagged: %v", steps[2])
	}
	other := out["other_targets"].([]map[string]any)
	if len(other) != 1 || other[0]["model"] != "removed-step" {
		t.Fatalf("other targets = %v", other)
	}
}
