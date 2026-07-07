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
	// Without provider context, glm-5v-turbo matches no specific entry and
	// falls through the glm pattern (no vision).
	bare := Of("glm-5v-turbo")
	if bare.Has(core.CapVision) {
		t.Fatalf("expected bare glm-5v-turbo to lack vision, got %v", bare)
	}

	// With the codebuddy provider, the override grants vision.
	scoped := OfProvider("codebuddy", "glm-5v-turbo")
	if !scoped.Has(core.CapVision) {
		t.Errorf("expected codebuddy/glm-5v-turbo to have vision, got %v", scoped)
	}
	if !scoped.Has(core.CapReasoning) {
		t.Errorf("expected codebuddy/glm-5v-turbo to have reasoning, got %v", scoped)
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
