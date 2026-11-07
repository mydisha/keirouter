package transform

import (
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"
)

// Tool-choice translation. The canonical request stores tool_choice in the
// OpenAI form (the string "auto"/"none"/"required" or an object
// {type:"function",function:{name}}). Inbound non-OpenAI dialects normalize
// their own shape into this form at parse time, and each upstream codec renders
// it back into the shape that provider expects. Keeping a single canonical form
// means a forced/!auto tool choice survives any cross-dialect hop instead of
// being silently dropped.

// claudeToolChoiceToOpenAI converts an Anthropic tool_choice value into the
// canonical OpenAI form. Returns nil when absent or unrecognized.
func claudeToolChoiceToOpenAI(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		if name, _ := m["name"].(string); name != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}
		}
	}
	return nil
}

// openAIToolChoiceToClaude converts the canonical OpenAI tool_choice into the
// Anthropic form. Returns nil to omit the field (Anthropic then defaults to
// "auto"), so an unrecognized value never leaks upstream as a 400.
func openAIToolChoiceToClaude(v any) any {
	switch t := v.(type) {
	case string:
		switch t {
		case "required":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		case "auto":
			return map[string]any{"type": "auto"}
		}
	case map[string]any:
		// OpenAI forced tool: {type:"function", function:{name}}. Checked before
		// the native pass-through because this shape also carries a `type`
		// ("function") that Anthropic rejects.
		if fn, ok := t["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return map[string]any{"type": "tool", "name": name}
			}
		}
		// Already Anthropic-native — only pass through the types it accepts.
		switch typ, _ := t["type"].(string); typ {
		case "auto", "any", "tool", "none":
			return t
		}
	}
	return nil
}

// openAIToolChoiceToGemini converts the canonical OpenAI tool_choice into a
// Gemini toolConfig.functionCallingConfig. sanitize brings a forced tool name
// into Gemini's allowed character set. Returns nil to omit the field (Gemini
// then defaults to AUTO).
func openAIToolChoiceToGemini(v any, sanitize func(string) string) map[string]any {
	mode := func(m string) map[string]any {
		return map[string]any{"functionCallingConfig": map[string]any{"mode": m}}
	}
	switch t := v.(type) {
	case string:
		switch t {
		case "none":
			return mode("NONE")
		case "required":
			return mode("ANY")
		case "auto":
			return mode("AUTO")
		}
	case map[string]any:
		if fn, ok := t["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return map[string]any{
					"functionCallingConfig": map[string]any{
						"mode":                 "ANY",
						"allowedFunctionNames": []string{sanitize(name)},
					},
				}
			}
		}
	}
	return nil
}

// geminiToolChoiceToOpenAI reads Gemini's toolConfig.functionCallingConfig.
func geminiToolChoiceToOpenAI(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var tc struct {
		Config struct {
			Mode    string   `json:"mode"`
			Allowed []string `json:"allowedFunctionNames"`
		} `json:"functionCallingConfig"`
	}
	if json.Unmarshal(raw, &tc) != nil {
		return nil
	}
	switch strings.ToUpper(tc.Config.Mode) {
	case "AUTO":
		return "auto"
	case "NONE":
		return "none"
	case "ANY":
		if len(tc.Config.Allowed) == 1 {
			return map[string]any{"type": "function", "function": map[string]any{"name": tc.Config.Allowed[0]}}
		}
		return "required"
	}
	return nil
}

// responsesToolChoiceToOpenAI reads a Responses tool_choice
// ("auto" | {"type":"function","name":...}) into the chat form.
func responsesToolChoiceToOpenAI(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &m) == nil && (m.Type == "function" || m.Type == "custom") && m.Name != "" {
		return map[string]any{"type": "function", "function": map[string]any{"name": m.Name}}
	}
	return nil
}

// openAIToolChoiceToResponses renders the chat form as a Responses tool_choice.
func openAIToolChoiceToResponses(v any) any {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if fn, ok := t["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return struct {
					Type string `json:"type"`
					Name string `json:"name"`
				}{"function", name}
			}
		}
	}
	return nil
}
