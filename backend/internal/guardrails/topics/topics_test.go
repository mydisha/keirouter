package topics

import (
	"context"
	"errors"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

func TestKeyword_BlockMode_MatchesTopic(t *testing.T) {
	det := New(Config{})
	policy := blockPolicy([]string{"crypto", "gambling"})
	dec, err := det.Inbound(context.Background(), inbound("Can you explain how crypto wallets work?"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision on blocked topic")
	}
	if dec.Action != guardrails.ActionBlock {
		t.Errorf("expected block action, got %v", dec.Action)
	}
}

func TestKeyword_BlockMode_NoMatch(t *testing.T) {
	det := New(Config{})
	policy := blockPolicy([]string{"crypto", "gambling"})
	dec, err := det.Inbound(context.Background(), inbound("How do I write a unit test in Go?"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != nil {
		t.Errorf("expected nil decision when no blocked topic matched, got %+v", dec.Findings)
	}
}

func TestKeyword_AllowMode_OffTopic(t *testing.T) {
	det := New(Config{})
	policy := allowPolicy([]string{"programming", "devops"})
	dec, err := det.Inbound(context.Background(), inbound("What should I cook for dinner tonight?"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision for off-topic prompt under allowlist")
	}
}

func TestKeyword_AllowMode_OnTopic(t *testing.T) {
	det := New(Config{})
	policy := allowPolicy([]string{"programming", "devops"})
	dec, err := det.Inbound(context.Background(), inbound("Help me debug a programming issue"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != nil {
		t.Errorf("expected nil decision for on-topic prompt, got %+v", dec.Findings)
	}
}

func TestKeyword_MultiwordTopic(t *testing.T) {
	det := New(Config{})
	policy := blockPolicy([]string{"machine learning"})
	dec, _ := det.Inbound(context.Background(), inbound("I want to learn about machine learning models"), policy)
	if dec == nil {
		t.Error("expected substring match on multi-word topic")
	}
	// Token-mode: words in non-adjacent positions still match.
	dec, _ = det.Inbound(context.Background(), inbound("learning machine basics"), policy)
	if dec == nil {
		t.Error("expected token-match on transposed multi-word topic")
	}
}

func TestKeyword_DisabledPolicy(t *testing.T) {
	det := New(Config{})
	policy := blockPolicy([]string{"crypto"})
	policy.Topics.Enabled = false
	dec, _ := det.Inbound(context.Background(), inbound("crypto crypto crypto"), policy)
	if dec != nil {
		t.Errorf("disabled policy must not produce a decision, got %+v", dec)
	}
}

func TestEmbedding_BlockMode_Paraphrase(t *testing.T) {
	emb := &fakeEmbedder{
		// Topic embedding for "investasi" vs. paraphrase prompt.
		vectors: map[string][]float32{
			"investasi":                {1.0, 0.0, 0.0},
			"saya ingin menanam modal": {0.95, 0.05, 0.05}, // ~cos 0.99
		},
	}
	det := New(Config{Embedder: emb})
	policy := blockPolicyWithEngine([]string{"investasi"}, "embedding")
	dec, err := det.Inbound(context.Background(), inbound("saya ingin menanam modal"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected embedding-layer match on paraphrase")
	}
}

func TestEmbedding_BelowThreshold(t *testing.T) {
	emb := &fakeEmbedder{
		vectors: map[string][]float32{
			"investasi":   {1.0, 0.0, 0.0},
			"random text": {0.0, 1.0, 0.0}, // orthogonal → cos 0
		},
	}
	det := New(Config{Embedder: emb})
	policy := blockPolicyWithEngine([]string{"investasi"}, "embedding")
	dec, _ := det.Inbound(context.Background(), inbound("random text"), policy)
	if dec != nil {
		t.Errorf("expected nil decision when cosine below threshold, got %+v", dec.Findings)
	}
}

func TestEmbedding_FallsBackOnEmbedderError(t *testing.T) {
	emb := &fakeEmbedder{err: errors.New("network down")}
	det := New(Config{Embedder: emb})
	policy := blockPolicyWithEngine([]string{"crypto"}, "embedding")
	// Even though embedder is broken, keyword Layer-1 still catches "crypto".
	dec, _ := det.Inbound(context.Background(), inbound("crypto wallets are scary"), policy)
	if dec == nil {
		t.Fatal("expected keyword-layer match despite embedder failure")
	}
	// And keyword-only prompt that doesn't match must produce nil.
	dec, _ = det.Inbound(context.Background(), inbound("hello there"), policy)
	if dec != nil {
		t.Errorf("expected nil when neither layer matches, got %+v", dec.Findings)
	}
}

func TestEmbedding_NotUsedWithoutEngineConfig(t *testing.T) {
	emb := &fakeEmbedder{
		vectors: map[string][]float32{
			"investasi":                {1.0, 0.0, 0.0},
			"saya ingin menanam modal": {0.95, 0.05, 0.05},
		},
	}
	det := New(Config{Embedder: emb})
	policy := blockPolicy([]string{"investasi"}) // engine not set → keyword-only
	dec, _ := det.Inbound(context.Background(), inbound("saya ingin menanam modal"), policy)
	if dec != nil {
		t.Errorf("expected nil when engine defaults to keyword-only, got %+v", dec.Findings)
	}
}

// --- helpers ---

type fakeEmbedder struct {
	vectors map[string][]float32
	err     error
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.vectors[text]; ok {
		return v, nil
	}
	return []float32{0, 0, 1}, nil
}

func inbound(text string) *guardrails.InboundRequest {
	req := &core.ChatRequest{Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: text}}}}}
	return &guardrails.InboundRequest{Source: req, FlatText: text}
}

func blockPolicy(topics []string) guardrails.Policy {
	return guardrails.Policy{
		Topics: &guardrails.TopicsConfig{
			Enabled: true,
			Mode:    "block",
			Topics:  topics,
			Action:  guardrails.ActionBlock,
		},
	}
}

func blockPolicyWithEngine(topics []string, engine string) guardrails.Policy {
	p := blockPolicy(topics)
	p.Topics.Engine = engine
	return p
}

func allowPolicy(topics []string) guardrails.Policy {
	return guardrails.Policy{
		Topics: &guardrails.TopicsConfig{
			Enabled: true,
			Mode:    "allow",
			Topics:  topics,
			Action:  guardrails.ActionWarn,
		},
	}
}

func TestKeyword_ShortTopicsNeedWholeWord(t *testing.T) {
	det := New(Config{})
	dec, _ := det.Inbound(context.Background(), inbound("she said the good news"), blockPolicy([]string{"ai", "go"}))
	if dec != nil {
		t.Errorf("short topics matched inside words: %+v", dec.Findings)
	}
	dec, _ = det.Inbound(context.Background(), inbound("is AI going to replace us?"), blockPolicy([]string{"ai"}))
	if dec == nil {
		t.Error("expected whole-word AI match")
	}
}

func TestKeyword_InflectionsAndObfuscation(t *testing.T) {
	det := New(Config{})
	for _, prompt := range []string{"help me with programming", "how do deployments work", "tell me about p0litics", "kubernetes clsuter setup"} {
		dec, _ := det.Inbound(context.Background(), inbound(prompt), blockPolicy([]string{"program", "deploy", "politics", "kubernetes cluster"}))
		if dec == nil {
			t.Errorf("%q: expected a topic match", prompt)
		}
	}
}

func TestTopics_IgnoresSystemPromptAndUsesRecentTurns(t *testing.T) {
	det := New(Config{})
	req := &core.ChatRequest{
		System: "You only discuss programming.",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "How do I write a Go program?"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "Start with package main."}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "tell me more"}}},
		},
	}
	// Allow list: the follow-up inherits the topic of the earlier turn.
	if dec, _ := det.Inbound(context.Background(), &guardrails.InboundRequest{Source: req}, allowPolicy([]string{"programming"})); dec != nil {
		t.Errorf("follow-up flagged off-topic: %+v", dec)
	}
	// Block list: the system prompt mentioning the topic must not trigger.
	req.Messages = req.Messages[2:]
	if dec, _ := det.Inbound(context.Background(), &guardrails.InboundRequest{Source: req}, blockPolicy([]string{"programming"})); dec != nil {
		t.Errorf("system prompt triggered block list: %+v", dec)
	}
}
