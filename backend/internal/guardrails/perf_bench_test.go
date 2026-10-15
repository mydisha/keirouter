package guardrails_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/guardrails/bias"
	"github.com/mydisha/keirouter/backend/internal/guardrails/injection"
	"github.com/mydisha/keirouter/backend/internal/guardrails/pii"
	"github.com/mydisha/keirouter/backend/internal/guardrails/topics"
	"github.com/mydisha/keirouter/backend/internal/guardrails/toxicity"
)

func benchEngine(p guardrails.Policy) *guardrails.Engine {
	return guardrails.NewEngine(guardrails.EngineConfig{
		Resolver: guardrails.NewResolver(&fakeResolver{policy: p}, 0),
		Detectors: []guardrails.Detector{
			pii.New(), injection.New(), topics.New(topics.Config{}), toxicity.New(toxicity.Config{}), bias.New(),
		},
	})
}

func allOn() guardrails.Policy {
	return guardrails.Policy{
		PII:       &guardrails.PIIConfig{Enabled: true, ScanOutput: true},
		Injection: &guardrails.InjectionConfig{Enabled: true, Action: guardrails.ActionLogOnly},
		Topics:    &guardrails.TopicsConfig{Enabled: true, Topics: []string{"politics", "gambling", "crypto trading"}},
		Toxicity:  &guardrails.ToxicityConfig{Enabled: true, Action: guardrails.ActionLogOnly},
		Bias:      &guardrails.BiasConfig{Enabled: true},
	}
}

var benchPrompt = strings.Repeat("Tolong bantu saya refactor fungsi ini supaya lebih cepat. "+
	"Here is the stack trace from production, the handler at line 42 panics when the map is nil. ", 40)

// BenchmarkInbound_AllDetectors runs every detector over a ~6 KB prompt.
func BenchmarkInbound_AllDetectors(b *testing.B) {
	e := benchEngine(allOn())
	b.ReportAllocs()
	for b.Loop() {
		req := &core.ChatRequest{
			System:   "You are a helpful coding assistant.",
			Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: benchPrompt}}}},
		}
		e.Inbound(context.Background(), req)
	}
}

// BenchmarkStreamGuard pushes a ~6 KB response through the guard in
// token-sized deltas.
func BenchmarkStreamGuard(b *testing.B) {
	e := benchEngine(allOn())
	b.ReportAllocs()
	for b.Loop() {
		g := e.NewStreamGuard(context.Background(), &core.ChatRequest{})
		for i := 0; i < len(benchPrompt); i += 6 {
			g.Push(benchPrompt[i:min(i+6, len(benchPrompt))])
		}
		g.Flush()
	}
}
