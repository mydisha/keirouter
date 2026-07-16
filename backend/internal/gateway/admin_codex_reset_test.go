package gateway

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestNewCodexResetRequest(t *testing.T) {
	req, err := newCodexResetRequest(
		context.Background(),
		"access-token",
		map[string]string{"accountId": "workspace-123"},
		"redeem-123",
		"credit-456",
	)
	if err != nil {
		t.Fatalf("newCodexResetRequest() error = %v", err)
	}

	if got := req.Header.Get("ChatGPT-Account-ID"); got != "workspace-123" {
		t.Fatalf("ChatGPT-Account-ID = %q, want workspace-123", got)
	}
	if got := req.Header.Get("OpenAI-Beta"); got != "codex-1" {
		t.Fatalf("OpenAI-Beta = %q, want codex-1", got)
	}
	if got := req.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q, want codex_cli_rs", got)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if payload["redeem_request_id"] != "redeem-123" {
		t.Fatalf("redeem_request_id = %q", payload["redeem_request_id"])
	}
	if payload["credit_id"] != "credit-456" {
		t.Fatalf("credit_id = %q", payload["credit_id"])
	}
}

func TestParseCodexConsumeResponse(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantOK   bool
		wantNone bool
	}{
		{name: "current reset", body: `{"outcome":"reset"}`, wantOK: true},
		{name: "idempotent success", body: `{"outcome":"alreadyRedeemed"}`, wantOK: true},
		{name: "current no credit", body: `{"outcome":"noCredit"}`, wantNone: true},
		{name: "legacy reset", body: `{"code":"reset","windows_reset":2}`, wantOK: true},
		{name: "nothing to reset", body: `{"outcome":"nothingToReset"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCodexConsumeResponse(200, []byte(tt.body))
			if got := result["ok"].(bool); got != tt.wantOK {
				t.Fatalf("ok = %v, want %v", got, tt.wantOK)
			}
			if got := result["no_credit"].(bool); got != tt.wantNone {
				t.Fatalf("no_credit = %v, want %v", got, tt.wantNone)
			}
		})
	}
}

func TestCodexTimeString(t *testing.T) {
	want := time.Unix(1_780_000_000, 0).UTC().Format(time.RFC3339)
	for _, value := range []any{float64(1_780_000_000), float64(1_780_000_000_000)} {
		if got := codexTimeString(value); got != want {
			t.Fatalf("codexTimeString(%v) = %q, want %q", value, got, want)
		}
	}
	if got := codexTimeString("2026-08-13T00:00:00Z"); got != "2026-08-13T00:00:00Z" {
		t.Fatalf("string timestamp changed to %q", got)
	}
}
