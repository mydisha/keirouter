package gateway

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestEstimateInputTokens(t *testing.T) {
	req := &core.ChatRequest{
		System: "be brief",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartText, Text: "hi there"},
			}},
		},
	}
	// Two short messages plus framing: a handful of tokens, never zero.
	got := estimateInputTokens(req)
	if got < 8 || got > 20 {
		t.Errorf("estimateInputTokens = %d, want a small positive count", got)
	}
}

func TestEstimateInputTokens_ToolsAndResults(t *testing.T) {
	base := &core.ChatRequest{
		Messages: []core.Message{
			{Role: core.RoleTool, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{Content: "file body"}},
			}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{Arguments: []byte(`{"a":1}`)}},
			}},
		},
	}
	withoutTools := estimateInputTokens(base)
	base.Tools = []core.Tool{{Name: "Read", Description: "read file", Parameters: []byte(`{"x":1}`)}}
	withTools := estimateInputTokens(base)
	if withoutTools == 0 || withTools <= withoutTools {
		t.Errorf("tool definitions must add tokens: without=%d with=%d", withoutTools, withTools)
	}
}

func TestEstimateInputTokens_NilAndEmpty(t *testing.T) {
	if got := estimateInputTokens(nil); got != 0 {
		t.Errorf("nil request = %d, want 0", got)
	}
	if got := estimateInputTokens(&core.ChatRequest{}); got != 0 {
		t.Errorf("empty request = %d, want 0", got)
	}
}
