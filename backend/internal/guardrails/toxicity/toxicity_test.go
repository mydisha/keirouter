package toxicity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

func TestNativeEngine_DetectsProfanityID(t *testing.T) {
	det := New(Config{})
	dec, err := det.Inbound(context.Background(), inbound("dasar bangsat lo, anjing!"), enabledPolicy(60))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if !findingCategory(dec, "profanity") {
		t.Errorf("expected profanity finding, got %+v", dec.Findings)
	}
}

func TestNativeEngine_DetectsHateEN(t *testing.T) {
	det := New(Config{})
	dec, err := det.Inbound(context.Background(), inbound("kill all of them, they are subhuman"), enabledPolicy(60))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if !findingCategory(dec, "hate") {
		t.Errorf("expected hate finding, got %+v", dec.Findings)
	}
}

func TestNativeEngine_DetectsHarassment(t *testing.T) {
	det := New(Config{})
	dec, err := det.Inbound(context.Background(), inbound("kill yourself, you piece of shit"), enabledPolicy(60))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision, got nil")
	}
	if !findingCategory(dec, "harassment") {
		t.Errorf("expected harassment finding, got %+v", dec.Findings)
	}
}

func TestNativeEngine_Benign(t *testing.T) {
	det := New(Config{})
	cases := []string{
		"Hello, how are you today?",
		"Please write a story about a friendly robot.",
		"Saya butuh bantuan untuk membuat proposal proyek.",
		"What's the weather in Jakarta?",
	}
	for _, c := range cases {
		dec, err := det.Inbound(context.Background(), inbound(c), enabledPolicy(70))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if dec != nil {
			t.Errorf("expected no decision for benign %q, got %+v", c, dec.Findings)
		}
	}
}

func TestNativeEngine_RespectsCategoryFilter(t *testing.T) {
	det := New(Config{})
	policy := guardrails.Policy{
		Toxicity: &guardrails.ToxicityConfig{
			Enabled:    true,
			Categories: []string{"violence"}, // hate-only text should NOT trigger
			Threshold:  60,
		},
	}
	dec, err := det.Inbound(context.Background(), inbound("kill all of them, they are subhuman"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != nil {
		t.Errorf("expected nil decision when policy restricts to violence-only, got %+v", dec.Findings)
	}
}

func TestNativeEngine_RespectsThreshold(t *testing.T) {
	det := New(Config{})
	// "anjing" alone (also just "dog") weighs 55; threshold 60 must drop it.
	dec, err := det.Inbound(context.Background(), inbound("anjing."), enabledPolicy(60))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != nil {
		t.Errorf("expected nil decision below threshold, got %+v", dec.Findings)
	}
}

func TestNativeEngine_NoActionDefaultsToWarn(t *testing.T) {
	det := New(Config{})
	policy := enabledPolicy(60)
	policy.Toxicity.Action = ""
	dec, err := det.Inbound(context.Background(), inbound("kill all subhuman"), policy)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil || dec.Action != guardrails.ActionWarn {
		t.Errorf("expected default ActionWarn, got %+v", dec)
	}
}

func TestOpenAIEngine_ParsesScores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"categories": map[string]bool{
						"hate": true, "harassment": false,
					},
					"category_scores": map[string]float64{
						"hate":             0.92,
						"hate/threatening": 0.81, // folds into hate; max wins
						"harassment":       0.10,
						"violence":         0.05,
					},
				},
			},
		})
	}))
	defer srv.Close()

	det := New(Config{OpenAI: &OpenAIConfig{APIKey: "test", BaseURL: srv.URL}})
	dec, err := det.Inbound(context.Background(), inbound("..."), enabledOpenAIPolicy(60))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision")
	}
	if !findingCategory(dec, "hate") {
		t.Errorf("expected hate finding, got %+v", dec.Findings)
	}
	for _, f := range dec.Findings {
		if f.Entity == "TOXICITY:hate" && f.Score < 0.91 {
			t.Errorf("expected hate score >= 0.91 (max of 0.92 and 0.81), got %v", f.Score)
		}
	}
}

func TestOpenAIEngine_MissingKeyFallsBackSoft(t *testing.T) {
	det := New(Config{OpenAI: &OpenAIConfig{APIKey: ""}})
	dec, err := det.Inbound(context.Background(), inbound("..."), enabledOpenAIPolicy(60))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec != nil {
		t.Errorf("expected nil decision with empty OpenAI key, got %+v", dec)
	}
}

func TestEngineRouting_UnknownDefaultsToNative(t *testing.T) {
	det := New(Config{})
	policy := enabledPolicy(60)
	policy.Toxicity.Engine = "nonexistent"
	dec, _ := det.Inbound(context.Background(), inbound("anjing bangsat babi"), policy)
	if dec == nil {
		t.Fatal("expected native fallback to fire")
	}
}

// --- helpers ---

func inbound(text string) *guardrails.InboundRequest {
	req := &core.ChatRequest{Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: text}}}}}
	return &guardrails.InboundRequest{Source: req, FlatText: text}
}

func enabledPolicy(threshold int) guardrails.Policy {
	return guardrails.Policy{
		Toxicity: &guardrails.ToxicityConfig{
			Enabled:   true,
			Threshold: threshold,
			Action:    guardrails.ActionBlock,
		},
	}
}

func enabledOpenAIPolicy(threshold int) guardrails.Policy {
	p := enabledPolicy(threshold)
	p.Toxicity.Engine = "openai"
	return p
}

func findingCategory(dec *guardrails.Decision, cat string) bool {
	for _, f := range dec.Findings {
		if f.Entity == "TOXICITY:"+cat {
			return true
		}
	}
	return false
}
