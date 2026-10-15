package capability

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// TestOf verifies the capability set projected from a resolved profile across
// the four-step fallback chain.
func TestOf(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected core.CapabilitySet
	}{
		{
			name:  "mimo v2.5 has vision and 1M context",
			model: "mimo-v2.5",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapVision,
				core.CapLongContext,
			),
		},
		{
			name:  "mimo omni adds audio input",
			model: "mimo-v2-omni",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapVision,
				core.CapAudioInput,
				core.CapLongContext,
			),
		},
		{
			name:  "exact id overrides generic pattern",
			model: "glm-4.6v",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapVision,
				core.CapReasoning,
				// 128k context -> not long context
			),
		},
		{
			name:  "generic glm family is text reasoning, long context",
			model: "glm-5",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapReasoning,
				core.CapLongContext,
			),
		},
		{
			name:  "gemini 2.5 is fully multimodal with search",
			model: "gemini-2.5-flash",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapVision,
				core.CapDocumentInput,
				core.CapStructuredOutput,
				core.CapAudioInput,
				core.CapVideoInput,
				core.CapReasoning,
				core.CapWebSearch,
				core.CapLongContext,
			),
		},
		{
			name:  "gpt-5 keeps structured output and web search",
			model: "gpt-5",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapVision,
				core.CapDocumentInput,
				core.CapReasoning,
				core.CapWebSearch,
				core.CapStructuredOutput,
				core.CapLongContext,
			),
		},
		{
			name:  "small context model is not long context",
			model: "gpt-3.5-turbo",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
			),
		},
		{
			name:  "image-only model drops tool calling",
			model: "gpt-image-1",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapImageOutput,
				core.CapLongContext,
			),
		},
		{
			name:  "unknown model falls back to the floor",
			model: "totally-unknown-xyz",
			expected: core.NewCapabilitySet(
				core.CapStreaming,
				core.CapToolCalling,
				core.CapLongContext,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Of(tt.model)
			if !equalSets(got, tt.expected) {
				t.Errorf("Of(%q) = %v, want %v", tt.model, got, tt.expected)
			}
		})
	}
}

// TestOfProviderOverride verifies that a provider-scoped override takes
// precedence over exact-id and pattern matches.
func TestOfProviderOverride(t *testing.T) {
	// Without provider context, minimax-m2.7 falls through the text-only
	// MiniMax M2.7 pattern (no vision).
	bare := Of("minimax-m2.7")
	if bare.Has(core.CapVision) {
		t.Fatalf("expected bare minimax-m2.7 to lack vision, got %v", bare)
	}

	// With the codebuddy provider, the override grants vision.
	scoped := OfProvider("codebuddy", "minimax-m2.7")
	if !scoped.Has(core.CapVision) {
		t.Errorf("expected codebuddy/minimax-m2.7 to have vision, got %v", scoped)
	}
	if !scoped.Has(core.CapReasoning) {
		t.Errorf("expected codebuddy/minimax-m2.7 to have reasoning, got %v", scoped)
	}
}

// TestLatestFamilyProfiles pins the capability profile of current model
// families, including dashed/dated ids and always-on thinking.
func TestLatestFamilyProfiles(t *testing.T) {
	cases := []struct {
		model    string
		format   string
		locked   bool
		ctx      int
		vision   bool
		pdf      bool
		noTools  bool
		thinking bool
	}{
		{"claude-opus-5-5", "claude-adaptive", true, 1000000, true, true, false, true},
		{"anthropic/claude-sonnet-5.5", "claude-adaptive", true, 1000000, true, true, false, true},
		{"claude-sonnet-5", "claude-adaptive", false, 1000000, true, true, false, true},
		{"claude-haiku-5-5", "claude-adaptive", false, 1000000, true, true, false, true},
		{"claude-fable-5-1", "claude-adaptive", true, 1000000, true, true, false, true},
		{"claude-mythos-5", "claude-adaptive", true, 1000000, true, true, false, true},
		{"claude-opus-4-7-20260416", "claude-adaptive", false, 1000000, true, true, false, true},
		{"claude-opus-4-6-thinking", "claude-adaptive", false, 1000000, true, true, false, true},
		{"claude-opus-4-5-20251101", "claude-budget", false, 200000, true, true, false, true},
		{"gpt-6-sol", "openai", false, 922000, true, true, false, true},
		{"gpt-5.6-terra", "openai", false, 922000, true, true, false, true},
		{"gpt-5.5", "openai", false, 1050000, true, true, false, true},
		{"gemini-3.8-flash", "gemini-level", true, 1048576, true, true, false, true},
		{"grok-4.20-non-reasoning", "", false, 1000000, true, false, false, false},
		{"grok-4.5", "openai", false, 500000, true, false, false, true},
		{"kimi-k3", "kimi", false, 1048576, true, false, false, true},
		{"glm-5.3", "zai", false, 1000000, false, false, false, true},
		{"qwen3.7-max", "qwen", false, 991808, false, false, false, true},
		{"deepseek-v4-flash", "deepseek", false, 1000000, true, false, false, true},
		{"deepseek-v4-pro", "deepseek", false, 1000000, false, false, false, true},
		{"mistral-large-4", "", false, 1048576, true, false, false, true},
		{"gemini-omni-flash-preview", "", false, 1048576, true, false, true, true},
	}
	for _, tc := range cases {
		p := ResolveProfile("", tc.model)
		if p.ThinkingFormat != tc.format || p.ThinkingCanDisable == tc.locked || p.ContextWindow != tc.ctx ||
			p.Vision != tc.vision || p.PDF != tc.pdf || p.Tools == tc.noTools || p.Reasoning != tc.thinking {
			t.Errorf("%s = %+v", tc.model, p)
		}
	}
}

// TestResolveProfileChain spot-checks profile fields across each resolution
// step: exact id, provider override, and pattern fallback.
func TestResolveProfileChain(t *testing.T) {
	// Exact id: claude-opus-4.6 carries adaptive thinking and a 1M context.
	if p := ResolveProfile("", "claude-opus-4.6"); p.ThinkingFormat != "claude-adaptive" || p.ContextWindow != 1000000 {
		t.Errorf("claude-opus-4.6 = %+v, want claude-adaptive / 1000000", p)
	}

	// Vendor-prefixed id resolves the same as the bare id.
	if p := ResolveProfile("", "anthropic/claude-opus-4.6"); p.ThinkingFormat != "claude-adaptive" {
		t.Errorf("anthropic/claude-opus-4.6 thinking = %q, want claude-adaptive", p.ThinkingFormat)
	}

	// Provider override: locked thinking on a codebuddy model.
	if p := ResolveProfile("codebuddy", "deepseek-v3-2-volc"); p.ThinkingCanDisable {
		t.Errorf("codebuddy/deepseek-v3-2-volc should not allow disabling thinking")
	}

	// Pattern fallback: a thinking-only model cannot disable thinking.
	if p := ResolveProfile("", "deepseek-r1"); !p.Reasoning || p.ThinkingCanDisable {
		t.Errorf("deepseek-r1 = %+v, want reasoning with locked thinking", p)
	}

	// Floor: an unknown model keeps tools and the default window.
	if p := ResolveProfile("", "totally-unknown-xyz"); !p.Tools || p.ContextWindow != 200000 {
		t.Errorf("unknown model = %+v, want tools / 200000 floor", p)
	}
}

// TestRequired verifies request-shape inference. Structured output and
// reasoning are adapted downstream rather than gated, so they are intentionally
// absent from the required set even when the request carries them.
func TestRequired(t *testing.T) {
	req := &core.ChatRequest{
		Tools:          []core.Tool{{Name: "lookup"}},
		Stream:         true,
		ResponseFormat: []byte(`{"type":"json_schema"}`),
		Reasoning:      &core.ReasoningConfig{Effort: "high"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartImage},
				{Type: core.PartAudio},
			}},
		},
	}
	got := Required(req)
	want := core.NewCapabilitySet(
		core.CapToolCalling,
		core.CapStreaming,
		core.CapVision,
		core.CapAudioInput,
	)
	if !equalSets(got, want) {
		t.Errorf("Required() = %v, want %v", got, want)
	}
}

// equalSets reports whether two capability sets contain exactly the same
// capabilities.
func equalSets(a, b core.CapabilitySet) bool {
	return a.Satisfies(b) && b.Satisfies(a)
}
