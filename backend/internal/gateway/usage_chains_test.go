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
		{ChainID: "c1", Provider: "codex", Model: "gpt-5", Requests: 3, Successes: 2, FellBack: 3},
		{ChainID: "c1", Provider: "deepseek", Model: "chat", Requests: 0},
		{ChainID: "c1", Provider: "old", Model: "removed-step", Requests: 1, Successes: 1},
	}
	out := chainUsageRow(chain, rows)

	if out["requests"].(int64) != 10 || out["fallback_requests"].(int64) != 3 {
		t.Fatalf("totals: requests=%v fallbacks=%v", out["requests"], out["fallback_requests"])
	}
	steps := out["steps"].([]map[string]any)
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3 (two steps + last-resort fallback)", len(steps))
	}
	if steps[0]["provider"] != "anthropic" || steps[0]["requests"].(int64) != 6 {
		t.Fatalf("first step = %v", steps[0])
	}
	if steps[1]["provider"] != "codex" || steps[1]["share"].(float64) != 0.3 {
		t.Fatalf("second step = %v", steps[1])
	}
	if steps[2]["is_fallback"] != true {
		t.Fatalf("last-resort model should be flagged: %v", steps[2])
	}
	other := out["other_targets"].([]map[string]any)
	if len(other) != 1 || other[0]["model"] != "removed-step" {
		t.Fatalf("other targets = %v", other)
	}
}
