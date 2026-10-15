package pii

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

func entities(ms []Match) map[Entity]string {
	out := map[Entity]string{}
	for _, m := range ms {
		out[m.Entity] = m.Text
	}
	return out
}

func TestRecognize_NewEntities(t *testing.T) {
	cases := []struct {
		text string
		want Entity
		val  string
	}{
		{"my key is sk-proj-AbCdEf1234567890XyZaBcDeF", EntityAPIKey, "sk-proj-AbCdEf1234567890XyZaBcDeF"},
		{"aws AKIAIOSFODNN7EXAMPLE here", EntityAPIKey, "AKIAIOSFODNN7EXAMPLE"},
		{"token: ghp_0123456789abcdefghijABCDEFGHIJ012345", EntityAPIKey, "ghp_0123456789abcdefghijABCDEFGHIJ012345"},
		{"api_key = \"a1b2c3d4e5f6g7h8i9j0\"", EntityAPIKey, "a1b2c3d4e5f6g7h8i9j0"},
		{"password: Rahasia123!", EntityPassword, "Rahasia123!"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----", EntityPrivateKey, ""},
		{"SSN 123-45-6789", EntityUSSSN, "123-45-6789"},
		{"no KK 3201202001900001", EntityIDKK, "3201202001900001"},
		{"BPJS 0001234567890", EntityIDBPJS, "0001234567890"},
		{"IBAN GB82 WEST 1234 5698 7654 32", EntityIBAN, "GB82 WEST 1234 5698 7654 32"},
		{"host fe80::1ff:fe23:4567:890a", EntityIP, "fe80::1ff:fe23:4567:890a"},
		{"call (021) 5555 1234", EntityPhone, "(021) 5555 1234"},
		{"+1 (415) 555-2671", EntityPhone, "+1 (415) 555-2671"},
		{"628123456789", EntityPhone, "628123456789"},
		{"mail budi [at] mail [dot] com", EntityEmail, "budi [at] mail [dot] com"},
		{"see https://example.com/x.", EntityURL, "https://example.com/x"},
	}
	for _, c := range cases {
		got := entities(Recognize(c.text, nil, 0.5))
		v, ok := got[c.want]
		if !ok {
			t.Errorf("%q: want %s, got %v", c.text, c.want, got)
			continue
		}
		if c.val != "" && v != c.val {
			t.Errorf("%q: %s = %q, want %q", c.text, c.want, v, c.val)
		}
	}
}

func TestRecognize_ObfuscatedDigits(t *testing.T) {
	cases := []string{
		"NIK ３２０１２０２００１９００００１",    // fullwidth digits
		"NIK 3201​2020​0190​0001", // zero-width splits
	}
	for _, c := range cases {
		ms := Recognize(c, nil, 0.5)
		if len(ms) == 0 || ms[0].Entity != EntityIDNIK {
			t.Fatalf("%q: want ID_NIK, got %+v", c, ms)
		}
		masked, _ := applyStrategy(c, ms, guardrails.PIIStrategyReplace)
		if masked != "NIK <ID_NIK>" {
			t.Errorf("%q masked to %q", c, masked)
		}
	}
}

// A credit card must be Luhn-valid and carry a real issuer prefix, and is
// found without a context word at the default min score.
func TestRecognize_CardWithoutContext(t *testing.T) {
	if e := entities(Recognize("4111 1111 1111 1111", nil, 0.5)); e[EntityCreditCard] == "" {
		t.Errorf("visa test card not detected: %v", e)
	}
	// Luhn-valid but no issuer uses prefix 1.
	if e := entities(Recognize("1000 0000 0000 0008", nil, 0.5)); e[EntityCreditCard] != "" {
		t.Errorf("non-issuer number flagged as card: %v", e)
	}
}

func TestRecognize_SecretsNeedEntropy(t *testing.T) {
	for _, c := range []string{"password is required", "token = getTokenFromEnvironment"} {
		if ms := Recognize(c, nil, 0.5); len(ms) > 0 {
			t.Errorf("%q: unexpected %+v", c, ms)
		}
	}
}

func chat(msgs ...core.Message) *core.ChatRequest { return &core.ChatRequest{Messages: msgs} }

func text(role core.Role, s string) core.Message {
	return core.Message{Role: role, Content: []core.ContentPart{{Type: core.PartText, Text: s}}}
}

func TestInbound_MasksWholeConversation(t *testing.T) {
	req := chat(
		text(core.RoleUser, "my email is a@example.com"),
		core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartToolCall, ToolCall: &core.ToolCall{
			ID: "1", Name: "send", Arguments: json.RawMessage(`{"to":"b@example.com","n":1}`),
		}}}},
		core.Message{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{
			CallID: "1", Content: "sent to c@example.com",
		}}}},
		text(core.RoleUser, "thanks"),
	)
	req.System = "support: d@example.com"
	in := &guardrails.InboundRequest{Source: req}
	p := guardrails.Policy{PII: &guardrails.PIIConfig{Enabled: true, Strategy: guardrails.PIIStrategyReplace}}
	dec, err := New().Inbound(context.Background(), in, p)
	if err != nil || dec == nil || dec.Action != guardrails.ActionMask {
		t.Fatalf("want mask, got %+v err=%v", dec, err)
	}
	raw, _ := json.Marshal(req)
	if strings.Contains(string(raw), "example.com") {
		t.Errorf("PII left in request: %s", raw)
	}
	if !json.Valid(req.Messages[1].Content[0].ToolCall.Arguments) {
		t.Errorf("tool args no longer valid JSON: %s", req.Messages[1].Content[0].ToolCall.Arguments)
	}
}

func TestInbound_ScopeLastUser(t *testing.T) {
	req := chat(text(core.RoleUser, "old a@example.com"), text(core.RoleUser, "new b@example.com"))
	in := &guardrails.InboundRequest{Source: req}
	p := guardrails.Policy{PII: &guardrails.PIIConfig{Enabled: true, Scope: guardrails.PIIScopeLastUser}}
	if _, err := New().Inbound(context.Background(), in, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Messages[0].TextContent(), "a@example.com") {
		t.Errorf("last_user scope rewrote an earlier message")
	}
	if strings.Contains(req.Messages[1].TextContent(), "b@example.com") {
		t.Errorf("last user message not masked")
	}
}

func TestInbound_BlockTypesAndAllowList(t *testing.T) {
	p := guardrails.Policy{PII: &guardrails.PIIConfig{
		Enabled:    true,
		BlockTypes: []string{"API_KEY"},
		AllowList:  []string{"help@keirouter.dev"},
	}}
	req := chat(text(core.RoleUser, "write to help@keirouter.dev"))
	dec, _ := New().Inbound(context.Background(), &guardrails.InboundRequest{Source: req}, p)
	if dec != nil {
		t.Errorf("allow-listed value fired: %+v", dec)
	}
	req = chat(text(core.RoleUser, "email x@example.com, key sk-ant-api03-abcdefghijklmnop1234567890"))
	dec, _ = New().Inbound(context.Background(), &guardrails.InboundRequest{Source: req}, p)
	if dec == nil || dec.Action != guardrails.ActionBlock {
		t.Fatalf("want block on API_KEY, got %+v", dec)
	}
	if strings.Contains(dec.Reason, "EMAIL") {
		t.Errorf("reason should name the blocking entity only: %q", dec.Reason)
	}
}
