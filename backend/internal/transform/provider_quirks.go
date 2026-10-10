package transform

import (
	"crypto/sha256"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/core"
)

// Provider quirks for OpenAI-compatible upstreams.
//
// Every open-weight lab speaks "OpenAI-compatible" with its own dialect for
// the parts OpenAI never standardised: how reasoning is switched on, which
// sampling knobs a reasoning model tolerates, what tool_choice values exist,
// and what a tool-call id may look like. The capability table records each
// model's reasoning wire format (ThinkingFormat); this file turns that plus
// the provider id into concrete request edits so the codec does not grow a
// forest of per-provider ifs.

// effortBudget maps an OpenAI-style effort to a thinking token budget for
// budget-driven APIs (Qwen thinking_budget).
func effortBudget(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal":
		return 512
	case "low":
		return 1024
	case "medium":
		return 4096
	case "high":
		return 16384
	case "xhigh", "max":
		return 32768
	}
	return 0
}

// thinkingDisabled reports whether the client asked for reasoning off.
func thinkingDisabled(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	}
	return false
}

// standardEffort returns the effort if it is a value OpenAI-style APIs accept.
func standardEffort(effort string) string {
	switch e := strings.ToLower(strings.TrimSpace(effort)); e {
	case "minimal", "low", "medium", "high", "xhigh":
		return e
	case "max":
		return "xhigh"
	}
	return ""
}

// applyThinkingByFormat renders the canonical reasoning config in the wire
// format the target model understands. Formats come from the capability
// table; providers that use a known format for every model are mapped below.
func applyThinkingByFormat(out *oaiRequest, req *core.ChatRequest, providerID string, profile capability.Profile) {
	if req.Reasoning == nil {
		return
	}
	format := profile.ThinkingFormat
	switch providerID {
	case "volcengine-ark", "byteplus":
		format = "doubao"
	case "alicode", "alicode-intl", "qwen":
		if format == "" {
			format = "qwen"
		}
	case "glm-cn", "zai":
		if format == "" {
			format = "zai"
		}
	}
	effort := req.Reasoning.Effort
	off := thinkingDisabled(effort)
	if off && !profile.ThinkingCanDisable && profile.Reasoning {
		// The model cannot stop reasoning; sending "disabled" is a 400.
		return
	}

	switch format {
	case "qwen", "hunyuan":
		// DashScope / Hunyuan: enable_thinking + thinking_budget.
		enabled := !off
		out.EnableThinking = &enabled
		if enabled {
			budget := req.Reasoning.MaxTokens
			if budget <= 0 {
				budget = effortBudget(effort)
			}
			if r := profile.ThinkingRange; r != nil && budget > 0 {
				if r.Min > 0 && budget < r.Min {
					budget = r.Min
				}
				if r.Max > 0 && budget > r.Max {
					budget = r.Max
				}
			}
			if budget > 0 {
				out.ThinkingBudget = &budget
			}
		}
		out.Thinking = nil
		out.ReasoningEffort = ""
	case "zai", "doubao":
		// GLM and Doubao: {"thinking":{"type":"enabled"|"disabled"}} only;
		// their parameter whitelists reject reasoning_effort.
		typ := "enabled"
		if off {
			typ = "disabled"
		}
		out.Thinking = &oaiThinking{Type: typ}
		out.ReasoningEffort = ""
	case "kimi":
		// Moonshot: reasoning_effort string; no thinking object.
		out.Thinking = nil
		out.ReasoningEffort = ""
		if !off {
			if e := standardEffort(effort); e != "" {
				out.ReasoningEffort = e
			}
		}
	case "minimax":
		// MiniMax: reasoning is split out of content when reasoning_split is
		// set; thinking cannot be turned off on M2.x and M3 is adaptive.
		split := true
		out.ReasoningSplit = &split
		out.Thinking = nil
		out.ReasoningEffort = ""
	case "deepseek":
		applyGenericReasoningConfig(out, req)
	case "openai", "":
		// OpenAI-style reasoning_effort is the most widely accepted knob;
		// a vendor-specific thinking object is a 400 on strict servers.
		out.Thinking = nil
		out.ReasoningEffort = ""
		if !off {
			out.ReasoningEffort = standardEffort(effort)
		}
	default:
		applyGenericReasoningConfig(out, req)
	}
}

// applyProviderParamRules enforces per-provider parameter constraints that
// would otherwise come back as 400s.
func applyProviderParamRules(out *oaiRequest, req *core.ChatRequest, providerID string, profile capability.Profile) {
	model := strings.ToLower(req.Model)
	switch providerID {
	case "mistral":
		// tool_choice "required" and object forms are spelled "any".
		if len(out.Tools) > 0 && out.ToolChoice != nil {
			switch tc := out.ToolChoice.(type) {
			case string:
				if tc == "required" {
					out.ToolChoice = "any"
				}
			default:
				out.ToolChoice = "any"
			}
		}
		// seed is random_seed; reasoning history is rejected (extra_forbidden).
		if seed, ok := out.Extra["seed"]; ok {
			delete(out.Extra, "seed")
			out.Extra["random_seed"] = seed
		}
		stripReasoningContent(out)
		for i := range out.Messages {
			if out.Messages[i].Role != "tool" {
				out.Messages[i].Name = ""
			}
		}
		applyMistralToolIDs(out)
	case "groq":
		// Groq returns reasoning in a separate field only when asked.
		if req.Reasoning != nil && !thinkingDisabled(req.Reasoning.Effort) && profile.Reasoning {
			out.ReasoningFormat = "parsed"
		}
		out.Thinking = nil
	case "xai":
		if strings.HasPrefix(model, "grok-4") || strings.HasPrefix(model, "grok-code") || strings.HasPrefix(model, "grok-3-mini") {
			out.Stop = nil
		}
		if strings.HasPrefix(model, "grok-4") || strings.HasPrefix(model, "grok-code") {
			delete(out.Extra, "frequency_penalty")
			delete(out.Extra, "presence_penalty")
		}
		out.Thinking = nil
	case "cerebras", "sambanova":
		for _, k := range []string{"frequency_penalty", "presence_penalty", "n", "logprobs", "top_logprobs", "logit_bias"} {
			delete(out.Extra, k)
		}
		if providerID == "sambanova" {
			out.ParallelToolCalls = nil
		}
		out.Thinking = nil
	case "volcengine-ark", "byteplus":
		out.ParallelToolCalls = nil
	}

	// Reasoning-only models reject sampling overrides.
	if profile.Reasoning && !profile.ThinkingCanDisable {
		if profile.ThinkingFormat == "deepseek" || profile.ThinkingFormat == "kimi" {
			out.Temperature = nil
			out.TopP = nil
			delete(out.Extra, "logprobs")
			delete(out.Extra, "top_logprobs")
		}
	}
	// Moonshot reasoning models accept only the default temperature.
	if profile.ThinkingFormat == "kimi" && profile.Reasoning {
		out.Temperature = nil
		out.TopP = nil
		if tc, ok := out.ToolChoice.(string); ok && tc == "required" {
			// Not supported by Moonshot; auto is the closest safe value.
			out.ToolChoice = "auto"
		}
	}
}

// applyMistralToolIDs rewrites tool-call ids to Mistral's required shape
// (exactly nine alphanumeric characters), consistently across the assistant
// tool_calls and the tool results that answer them.
func applyMistralToolIDs(out *oaiRequest) {
	ids := map[string]string{}
	for i := range out.Messages {
		for j := range out.Messages[i].ToolCalls {
			tc := &out.Messages[i].ToolCalls[j]
			tc.ID = mistralToolID(tc.ID, ids)
		}
		if out.Messages[i].Role == "tool" && out.Messages[i].ToolCallID != "" {
			out.Messages[i].ToolCallID = mistralToolID(out.Messages[i].ToolCallID, ids)
		}
	}
}

const mistralIDAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func mistralToolID(id string, seen map[string]string) string {
	if mapped, ok := seen[id]; ok {
		return mapped
	}
	if len(id) == 9 && isAlnum(id) {
		seen[id] = id
		return id
	}
	sum := sha256.Sum256([]byte(id))
	var b [9]byte
	for i := range b {
		b[i] = mistralIDAlphabet[int(sum[i])%len(mistralIDAlphabet)]
	}
	seen[id] = string(b[:])
	return seen[id]
}

func isAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// reasoningDetailsText folds MiniMax-style reasoning_details into one text.
func reasoningDetailsText(details []oaiReasoningDetail) string {
	if len(details) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range details {
		if d.Text != "" {
			b.WriteString(d.Text)
		}
	}
	return b.String()
}

// oaiReasoningDetail is one entry of reasoning_details (MiniMax, OpenRouter).
type oaiReasoningDetail struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

var _ = json.Marshal
