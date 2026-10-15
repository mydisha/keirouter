package bias

import (
	"context"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

func TestBias_DetectsGenderEN(t *testing.T) {
	det := New()
	dec, err := det.Outbound(context.Background(), outbound("Women are naturally better at caregiving and men cannot do it well."), enabledPolicy(40))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision on gender-biased response")
	}
	if !findingCategory(dec, "gender") {
		t.Errorf("expected gender finding, got %+v", dec.Findings)
	}
}

func TestBias_DetectsEthnicID(t *testing.T) {
	det := New()
	dec, err := det.Outbound(context.Background(), outbound("Semua orang Jawa pasti suka makanan manis dan ras itu memang berbeda."), enabledPolicy(40))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision on ethnic-biased response")
	}
	if !findingCategory(dec, "ethnic") {
		t.Errorf("expected ethnic finding, got %+v", dec.Findings)
	}
}

func TestBias_DetectsReligious(t *testing.T) {
	det := New()
	dec, err := det.Outbound(context.Background(), outbound("All Muslims are very devout and that religion is strict."), enabledPolicy(40))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if dec == nil {
		t.Fatal("expected decision on religious-biased response")
	}
	if !findingCategory(dec, "religious") {
		t.Errorf("expected religious finding, got %+v", dec.Findings)
	}
}

func TestBias_BenignText(t *testing.T) {
	det := New()
	cases := []string{
		"Here is a recipe for chocolate cake. Mix flour and sugar...",
		"Software engineering involves writing, testing, and maintaining code.",
		"Cuaca Jakarta hari ini cerah dengan suhu 30 derajat.",
	}
	for _, c := range cases {
		dec, err := det.Outbound(context.Background(), outbound(c), enabledPolicy(60))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if dec != nil {
			t.Errorf("expected nil decision for benign %q, got %+v", c, dec.Findings)
		}
	}
}

func TestBias_RespectsCategoryFilter(t *testing.T) {
	det := New()
	policy := guardrails.Policy{
		Bias: &guardrails.BiasConfig{
			Enabled:    true,
			Categories: []string{"political"}, // gender hit should NOT trigger
			Threshold:  40,
		},
	}
	dec, _ := det.Outbound(context.Background(), outbound("Women are naturally better at caregiving."), policy)
	if dec != nil {
		t.Errorf("expected nil decision when category filtered out, got %+v", dec.Findings)
	}
}

func TestBias_RespectsThreshold(t *testing.T) {
	det := New()
	// Single gender phrase => score 50; threshold 80 must drop it.
	dec, _ := det.Outbound(context.Background(), outbound("Women cannot do that job."), enabledPolicy(80))
	if dec != nil {
		t.Errorf("expected nil decision below threshold, got %+v", dec.Findings)
	}
}

func TestBias_InboundIsNoop(t *testing.T) {
	det := New()
	req := &core.ChatRequest{Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "Women are naturally better at caregiving."}}}}}
	in := &guardrails.InboundRequest{Source: req, FlatText: "Women are naturally better at caregiving."}
	dec, _ := det.Inbound(context.Background(), in, enabledPolicy(40))
	if dec != nil {
		t.Errorf("bias inbound must be no-op, got %+v", dec)
	}
}

func TestBias_DefaultActionIsLogOnly(t *testing.T) {
	det := New()
	policy := enabledPolicy(40)
	policy.Bias.Action = ""
	dec, _ := det.Outbound(context.Background(), outbound("Women are naturally better at caregiving."), policy)
	if dec == nil || dec.Action != guardrails.ActionLogOnly {
		t.Errorf("expected default ActionLogOnly, got %+v", dec)
	}
}

// --- helpers ---

func outbound(text string) *guardrails.OutboundResponse {
	resp := &core.ChatResponse{Message: core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: text}}}}
	return &guardrails.OutboundResponse{Source: resp, Text: text}
}

func enabledPolicy(threshold int) guardrails.Policy {
	return guardrails.Policy{
		Bias: &guardrails.BiasConfig{
			Enabled:   true,
			Threshold: threshold,
			Action:    guardrails.ActionBlock,
		},
	}
}

func findingCategory(dec *guardrails.Decision, cat string) bool {
	for _, f := range dec.Findings {
		if f.Entity == "BIAS:"+cat {
			return true
		}
	}
	return false
}
