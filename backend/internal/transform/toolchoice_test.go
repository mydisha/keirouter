package transform

import (
	"testing"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// TestAnthropicToolChoiceRender verifies the canonical OpenAI tool_choice is
// translated into the Anthropic shape (and only when tools are present).
func TestAnthropicToolChoiceRender(t *testing.T) {
	cases := []struct {
		name     string
		choice   any
		wantType string
		wantName string
	}{
		{"required maps to any", "required", "any", ""},
		{"auto maps to auto", "auto", "auto", ""},
		{"none maps to none", "none", "none", ""},
		{"forced tool maps to tool", map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}, "tool", "lookup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &core.ChatRequest{
				Model:      "claude-sonnet-4.5",
				Tools:      []core.Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}},
				ToolChoice: tc.choice,
				Messages:   []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
			}
			body, err := AnthropicCodec{}.RenderRequest(req)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var out struct {
				ToolChoice map[string]any `json:"tool_choice"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if out.ToolChoice["type"] != tc.wantType {
				t.Errorf("tool_choice.type = %v, want %q (%s)", out.ToolChoice["type"], tc.wantType, body)
			}
			if tc.wantName != "" && out.ToolChoice["name"] != tc.wantName {
				t.Errorf("tool_choice.name = %v, want %q", out.ToolChoice["name"], tc.wantName)
			}
		})
	}
}

// TestAnthropicToolChoiceOmittedWithoutTools verifies tool_choice is not emitted
// when no tools are declared (Anthropic rejects that pairing).
func TestAnthropicToolChoiceOmittedWithoutTools(t *testing.T) {
	req := &core.ChatRequest{
		Model:      "claude-sonnet-4.5",
		ToolChoice: "required",
		Messages:   []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var probe map[string]any
	_ = json.Unmarshal(body, &probe)
	if _, ok := probe["tool_choice"]; ok {
		t.Errorf("tool_choice should be omitted without tools: %s", body)
	}
}

// TestAnthropicDropsTemperatureForOpus4 verifies claude-opus-4 strips temperature.
func TestAnthropicDropsTemperatureForOpus4(t *testing.T) {
	temp := 0.7
	req := &core.ChatRequest{
		Model:       "claude-opus-4-20250514",
		Temperature: &temp,
		Messages:    []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var probe map[string]any
	_ = json.Unmarshal(body, &probe)
	if _, ok := probe["temperature"]; ok {
		t.Errorf("temperature should be dropped for claude-opus-4: %s", body)
	}
}

// TestAnthropicDropsSamplingForClaude5 verifies Claude 5.x strips both
// temperature and top_p, while Sonnet 4.6 keeps them.
func TestAnthropicDropsSamplingForClaude5(t *testing.T) {
	temp, topP := 0.7, 0.9
	for model, wantDropped := range map[string]bool{
		"claude-opus-5-5":   true,
		"claude-sonnet-5":   true,
		"claude-haiku-5-5":  true,
		"claude-fable-5-1":  true,
		"claude-mythos-5":   true,
		"claude-sonnet-4-6": false,
	} {
		req := &core.ChatRequest{
			Model:       model,
			Temperature: &temp,
			TopP:        &topP,
			Messages:    []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
		}
		body, err := AnthropicCodec{}.RenderRequest(req)
		if err != nil {
			t.Fatalf("render %s: %v", model, err)
		}
		var probe map[string]any
		_ = json.Unmarshal(body, &probe)
		_, hasTemp := probe["temperature"]
		_, hasTopP := probe["top_p"]
		if hasTemp == wantDropped || hasTopP == wantDropped {
			t.Errorf("%s: temperature present=%v top_p present=%v, want dropped=%v: %s", model, hasTemp, hasTopP, wantDropped, body)
		}
	}
}

// TestGeminiToolConfigRender verifies tool_choice maps to a Gemini toolConfig.
func TestGeminiToolConfigRender(t *testing.T) {
	req := &core.ChatRequest{
		Model:      "gemini-2.5-pro",
		Tools:      []core.Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}},
		Messages:   []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := GeminiCodec{}.RenderRequest(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var out struct {
		ToolConfig struct {
			FunctionCallingConfig struct {
				Mode                 string   `json:"mode"`
				AllowedFunctionNames []string `json:"allowedFunctionNames"`
			} `json:"functionCallingConfig"`
		} `json:"toolConfig"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Errorf("mode = %q, want ANY (%s)", out.ToolConfig.FunctionCallingConfig.Mode, body)
	}
	if len(out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
		out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0] != "lookup" {
		t.Errorf("allowedFunctionNames = %v, want [lookup]", out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames)
	}
}

// TestClaudeToolChoiceParse verifies inbound Anthropic tool_choice normalizes to
// the canonical OpenAI form.
func TestClaudeToolChoiceParse(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4.5","max_tokens":100,"tool_choice":{"type":"any"},"messages":[{"role":"user","content":"hi"}]}`)
	req, err := AnthropicCodec{}.ParseRequest(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if req.ToolChoice != "required" {
		t.Errorf("tool_choice = %v, want \"required\"", req.ToolChoice)
	}
}
