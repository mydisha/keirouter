package transform

import (
	"crypto/sha256"
	"strings"

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
// forest of per-provider conditionals.

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

// thinkingFormatFor resolves the reasoning wire format to render for a
// provider. Vendor switches (enable_thinking, reasoning_split, ...) are only
// understood by the vendor's own API, so they are keyed on the provider id;
// an aggregator serving the same model (Groq, OpenRouter, Cloudflare, a vLLM
// box) gets the generic rendering regardless of the model's native format.
func thinkingFormatFor(providerID string) string {
	switch providerID {
	case "volcengine-ark", "byteplus", "doubao":
		return "doubao"
	case "alicode", "alicode-intl", "dashscope", "qwen":
		return "qwen"
	case "hunyuan":
		return "hunyuan"
	case "minimax":
		return "minimax"
	case "moonshot", "kimi":
		return "kimi"
	case "glm", "glm-cn", "zai":
		return "zai"
	}
	return ""
}

// applyThinkingByFormat renders the canonical reasoning config in the wire
// format the target model understands. Formats without a dedicated switch
// keep the generic thinking/reasoning_effort rendering for providers that
// echo reasoning (scope != none) and leave other requests untouched.
func applyThinkingByFormat(out *oaiRequest, req *core.ChatRequest, providerID string, profile capability.Profile, scope reasoningScope) {
	if req.Reasoning == nil {
		return
	}
	effort := req.Reasoning.Effort
	off := thinkingDisabled(effort)
	if off && profile.Reasoning && !profile.ThinkingCanDisable {
		// The model cannot stop reasoning; asking for "disabled" is a 400.
		// Leave the request as-is so the upstream default applies.
		out.Thinking = nil
		out.ReasoningEffort = ""
		return
	}

	switch thinkingFormatFor(providerID) {
	case "qwen", "hunyuan":
		// DashScope and Hunyuan switch reasoning with enable_thinking and
		// bound it with thinking_budget; they reject the thinking object.
		enabled := !off
		out.EnableThinking = &enabled
		out.Thinking = nil
		out.ReasoningEffort = ""
		if !enabled {
			return
		}
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
	case "doubao":
		// Volcengine Ark / BytePlus: {"thinking":{"type":"enabled"|"disabled"|"auto"}}.
		typ := "enabled"
		switch {
		case off:
			typ = "disabled"
		case strings.EqualFold(strings.TrimSpace(effort), "auto"), strings.EqualFold(strings.TrimSpace(effort), "adaptive"):
			typ = "auto"
		}
		out.Thinking = &oaiThinking{Type: typ}
		out.ReasoningEffort = ""
	case "minimax":
		// MiniMax returns reasoning in reasoning_details when asked to split
		// it out of content; thinking.type / reasoning_effort are ignored.
		split := true
		out.ReasoningSplit = &split
		applyGenericReasoningConfig(out, req)
	case "kimi":
		applyGenericReasoningConfig(out, req)
		if !off {
			// Moonshot thinking models accept only the default sampling.
			out.Temperature = nil
			out.TopP = nil
		}
	default:
		if scope != reasoningNone {
			applyGenericReasoningConfig(out, req)
		}
	}
}

// applyProviderParamRules enforces per-provider parameter constraints that
// would otherwise come back as 400s.
func applyProviderParamRules(out *oaiRequest, req *core.ChatRequest, providerID string, profile capability.Profile) {
	model := strings.ToLower(req.Model)
	switch providerID {
	case "mistral":
		// tool_choice "required" and the object form are spelled "any".
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
		// seed is random_seed; echoed reasoning history is extra_forbidden.
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
		// Groq only separates reasoning from content when asked.
		if req.Reasoning != nil && !thinkingDisabled(req.Reasoning.Effort) && profile.Reasoning {
			out.ReasoningFormat = "parsed"
		}
		out.Thinking = nil
	case "xai":
		if strings.HasPrefix(model, "grok-4") || strings.HasPrefix(model, "grok-code") {
			out.Stop = nil
			delete(out.Extra, "frequency_penalty")
			delete(out.Extra, "presence_penalty")
		}
		out.Thinking = nil
	case "cerebras", "sambanova":
		for _, k := range []string{"frequency_penalty", "presence_penalty", "logit_bias", "service_tier"} {
			delete(out.Extra, k)
		}
		out.ParallelToolCalls = nil
		out.Thinking = nil
	case "volcengine-ark", "byteplus", "doubao":
		out.ParallelToolCalls = nil
	}

	// Locked reasoning models (deepseek-reasoner, r1) ignore or reject
	// sampling overrides; drop them rather than risk a 400.
	if profile.Reasoning && !profile.ThinkingCanDisable && profile.ThinkingFormat == "deepseek" {
		out.Temperature = nil
		out.TopP = nil
		delete(out.Extra, "logprobs")
		delete(out.Extra, "top_logprobs")
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

// mistralToolID returns id unchanged when it already fits, otherwise a
// deterministic nine-character digest so repeated ids map consistently.
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
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// oaiReasoningDetail is one entry of reasoning_details (MiniMax reasoning_split,
// OpenRouter). Only text entries carry visible reasoning.
type oaiReasoningDetail struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// reasoningDetailsText folds reasoning_details into one thinking string.
func reasoningDetailsText(details []oaiReasoningDetail) string {
	switch len(details) {
	case 0:
		return ""
	case 1:
		return details[0].Text
	}
	var b strings.Builder
	for _, d := range details {
		b.WriteString(d.Text)
	}
	return b.String()
}
