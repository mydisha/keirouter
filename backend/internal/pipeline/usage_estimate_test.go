package pipeline

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// TestEstimateStreamUsage verifies the fallback usage estimate combines a
// prompt estimate from the request with a completion estimate from the streamed
// output length.
func TestEstimateStreamUsage(t *testing.T) {
	req := &core.ChatRequest{
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "Summarize the following document for me."}}},
		},
	}
	got := estimateStreamUsage(req, 400)
	wantPrompt := core.EstimatePromptTokens(req)
	if got.PromptTokens != wantPrompt || wantPrompt == 0 {
		t.Errorf("PromptTokens = %d, want %d", got.PromptTokens, wantPrompt)
	}
	// ~3.5 characters per streamed token: 400 chars → 115 tokens.
	if got.CompletionTokens < 100 || got.CompletionTokens > 135 {
		t.Errorf("CompletionTokens = %d, want ≈115", got.CompletionTokens)
	}
	if got.TotalTokens != got.PromptTokens+got.CompletionTokens {
		t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, got.PromptTokens+got.CompletionTokens)
	}
	if got.Source != core.UsageSourceEstimated {
		t.Errorf("Source = %q, want estimated", got.Source)
	}
}

// TestEstimateStreamUsage_NoOutput verifies a request with no streamed output
// still reports the prompt estimate and a zero completion.
func TestEstimateStreamUsage_NoOutput(t *testing.T) {
	req := &core.ChatRequest{
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "ping"}}},
		},
	}
	got := estimateStreamUsage(req, 0)
	if got.PromptTokens == 0 {
		t.Error("PromptTokens must be estimated from the request")
	}
	if got.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", got.CompletionTokens)
	}
	if got.TotalTokens != got.PromptTokens {
		t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, got.PromptTokens)
	}
}

// TestPartialStreamUsageReplacesPlaceholderOutput verifies that an Anthropic
// message_start placeholder (output_tokens: 1) is replaced by an estimate when
// the stream dies before message_delta reports the real count.
func TestPartialStreamUsageReplacesPlaceholderOutput(t *testing.T) {
	req := &core.ChatRequest{Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	got := partialStreamUsage(req, core.Usage{PromptTokens: 500, CompletionTokens: 1, Source: core.UsageSourceProvider}, 2000)
	if got.PromptTokens != 500 {
		t.Fatalf("prompt tokens must be kept: %d", got.PromptTokens)
	}
	if got.CompletionTokens < 400 {
		t.Fatalf("completion must be estimated from streamed text, got %d", got.CompletionTokens)
	}
	if got.Source != core.UsageSourceEstimated {
		t.Fatalf("source = %q, want estimated", got.Source)
	}
	// A real count is never overridden.
	real := partialStreamUsage(req, core.Usage{PromptTokens: 500, CompletionTokens: 42}, 2000)
	if real.CompletionTokens != 42 {
		t.Fatalf("real completion count overridden: %d", real.CompletionTokens)
	}
}
