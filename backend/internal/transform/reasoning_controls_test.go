package transform

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestReasoningControls(t *testing.T) {
	cases := []struct {
		name               string
		dialect            core.Dialect
		provider, model    string
		adjustable, canOff bool
	}{
		// A model without reasoning offers no control at all.
		{"non-reasoning model", core.DialectOpenAI, "openai", "gpt-4o", false, false},
		// OpenAI first party: its reasoning families take an effort but never "none".
		{"openai reasoning family", core.DialectOpenAI, "openai", "gpt-5", capability.ResolveProfile("openai", "gpt-5").Reasoning, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adj, off := ReasoningControls(c.dialect, c.provider, c.model)
			if adj != c.adjustable || off != c.canOff {
				t.Fatalf("ReasoningControls(%s, %s, %s) = (%v, %v), want (%v, %v)", c.dialect, c.provider, c.model, adj, off, c.adjustable, c.canOff)
			}
		})
	}
}

// Off is only ever offered for models whose effort is adjustable, and never
// when the profile says thinking cannot be disabled.
func TestReasoningControls_OffImpliesAdjustable(t *testing.T) {
	targets := []struct {
		dialect         core.Dialect
		provider, model string
	}{
		{core.DialectAnthropic, "anthropic", "claude-sonnet-4-5"},
		{core.DialectOpenAI, "deepseek", "deepseek-reasoner"},
		{core.DialectOpenAI, "qwen", "qwen3-max"},
		{core.DialectOpenAI, "kimi", "kimi-k2-thinking"},
		{core.DialectOpenAI, "groq", "llama-3.3-70b-versatile"},
		{core.DialectOpenAI, "openrouter", "deepseek/deepseek-r1"},
	}
	for _, tc := range targets {
		adj, off := ReasoningControls(tc.dialect, tc.provider, tc.model)
		p := capability.ResolveProfile(tc.provider, tc.model)
		if off && !adj {
			t.Errorf("%s/%s: off without adjustable", tc.provider, tc.model)
		}
		if off && !p.ThinkingCanDisable {
			t.Errorf("%s/%s: off offered though thinking cannot be disabled", tc.provider, tc.model)
		}
		if !p.Reasoning && (adj || off) {
			t.Errorf("%s/%s: controls offered for a model without reasoning", tc.provider, tc.model)
		}
		// A non-OpenAI dialect forwards the effort natively for any reasoning model.
		if tc.dialect != core.DialectOpenAI && p.Reasoning && !adj {
			t.Errorf("%s/%s: reasoning model on %s dialect should be adjustable", tc.provider, tc.model, tc.dialect)
		}
	}
}
