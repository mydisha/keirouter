package transform

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// OpenAICodec handles the OpenAI Chat Completions wire format, the de-facto
// standard most CLI tools speak.
type OpenAICodec struct{}

func (OpenAICodec) Dialect() core.Dialect { return core.DialectOpenAI }

// ---- wire types -------------------------------------------------------------

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	ToolChoice  any          `json:"tool_choice,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	MaxTokens   *int         `json:"max_tokens,omitempty"`
	// MaxCompletionTokens is the field OpenAI reasoning models (gpt-5,
	// o-series) require instead of max_tokens.
	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"`
	// Stop accepts either a string or an array of strings on input.
	Stop              json.RawMessage     `json:"stop,omitempty"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls,omitempty"`
	Stream            bool                `json:"stream,omitempty"`
	StreamOpts        *oaiStreamOpt       `json:"stream_options,omitempty"`
	ResponseFormat    json.RawMessage     `json:"response_format,omitempty"`
	ReasoningEffort   string              `json:"reasoning_effort,omitempty"`
	Thinking          *oaiThinking        `json:"thinking,omitempty"`
	UsageAccounting   *oaiUsageAccounting `json:"usage,omitempty"`
	ExtraBody         map[string]any      `json:"extra_body,omitempty"`
	// Extra carries passthrough parameters (seed, penalties, logprobs, user,
	// service_tier, ...) that the canonical model does not represent. They
	// are spliced into the rendered JSON by MarshalJSON.
	Extra map[string]json.RawMessage `json:"-"`
}

// MarshalJSON renders the request and splices passthrough parameters in at
// the top level. Named fields always win over Extra.
func (r oaiRequest) MarshalJSON() ([]byte, error) {
	type plain oaiRequest
	base, err := json.Marshal(plain(r))
	if err != nil || len(r.Extra) == 0 {
		return base, err
	}
	return spliceExtra(base, r.Extra)
}

// spliceExtra appends extra top-level members to an already-encoded JSON
// object, skipping keys the object already has.
func spliceExtra(obj []byte, extra map[string]json.RawMessage) ([]byte, error) {
	var present map[string]json.RawMessage
	if err := json.Unmarshal(obj, &present); err != nil {
		return obj, nil
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if _, dup := present[k]; !dup {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return obj, nil
	}
	sort.Strings(keys)
	buf := make([]byte, 0, len(obj)+64*len(keys))
	buf = append(buf, obj[:len(obj)-1]...) // drop closing brace
	for _, k := range keys {
		if len(buf) > 1 {
			buf = append(buf, ',')
		}
		kb, _ := json.Marshal(k)
		buf = append(buf, kb...)
		buf = append(buf, ':')
		buf = append(buf, extra[k]...)
	}
	return append(buf, '}'), nil
}

// oaiPassthroughParams are OpenAI chat parameters the canonical request does
// not model. They are kept verbatim and rendered back to OpenAI-dialect
// upstreams. Tier "any" is understood by essentially every OpenAI-compatible
// server; tier "first-party" only by api.openai.com / Azure and is dropped
// elsewhere to avoid "unsupported parameter" 400s.
var oaiPassthroughParams = map[string]string{
	"seed": "any", "frequency_penalty": "any", "presence_penalty": "any",
	"logit_bias": "any", "logprobs": "any", "top_logprobs": "any", "n": "any",
	"user":         "any",
	"service_tier": "first-party", "store": "first-party", "metadata": "first-party",
	"prediction": "first-party", "modalities": "first-party", "audio": "first-party",
	"web_search_options": "first-party", "prompt_cache_key": "first-party",
	"safety_identifier": "first-party", "verbosity": "first-party",
}

// parseOAIExtraParams collects passthrough parameters from the raw body.
func parseOAIExtraParams(body []byte) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(body, &all) != nil {
		return nil
	}
	var extra map[string]json.RawMessage
	for k, raw := range all {
		if _, ok := oaiPassthroughParams[k]; !ok || string(raw) == "null" {
			continue
		}
		if extra == nil {
			extra = make(map[string]json.RawMessage, 4)
		}
		extra[k] = raw
	}
	return extra
}

// filterExtraForProvider drops first-party-only passthrough params for
// third-party OpenAI-compatible upstreams.
func filterExtraForProvider(extra map[string]json.RawMessage, providerID string) map[string]json.RawMessage {
	if len(extra) == 0 || isOpenAIFirstParty(providerID) {
		return extra
	}
	out := make(map[string]json.RawMessage, len(extra))
	for k, v := range extra {
		if oaiPassthroughParams[k] == "any" {
			out[k] = v
		}
	}
	return out
}

type oaiThinking struct {
	Type string `json:"type,omitempty"`
}

// oaiUsageAccounting is OpenRouter's {"usage":{"include":true}} request flag.
type oaiUsageAccounting struct {
	Include bool `json:"include"`
}

type oaiStreamOpt struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type oaiMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []oaiToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	// ReasoningContent carries thinking/reasoning text that must be echoed
	// back on follow-up turns for DeepSeek, MiniMax, and similar providers.
	// Omitted when empty to avoid 400 errors on providers that don't support it.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// ---- request parsing --------------------------------------------------------

func (OpenAICodec) ParseRequest(body []byte) (*core.ChatRequest, error) {
	var raw oaiRequest
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("openai: parse request: %w", err)
	}

	req := &core.ChatRequest{
		Model:             raw.Model,
		Temperature:       raw.Temperature,
		TopP:              raw.TopP,
		MaxTokens:         raw.MaxTokens,
		Stop:              decodeOAIStop(raw.Stop),
		Stream:            raw.Stream,
		ToolChoice:        raw.ToolChoice,
		ParallelToolCalls: raw.ParallelToolCalls,
	}
	if req.MaxTokens == nil && raw.MaxCompletionTokens != nil {
		req.MaxTokens = raw.MaxCompletionTokens
	}
	req.Extra = parseOAIExtraParams(body)
	// stream_options.include_usage: the client opts in to a usage event on the
	// streaming response. We don't forward this upstream (many OpenAI-compatible
	// providers 400 on it — see renderOAIRequest), but the pipeline honors it by
	// guaranteeing a final usage event, synthesizing an estimate if the provider
	// omits one.
	if raw.Stream && raw.StreamOpts != nil && raw.StreamOpts.IncludeUsage {
		req.IncludeUsage = true
	}
	if raw.ReasoningEffort != "" {
		req.Reasoning = &core.ReasoningConfig{Effort: raw.ReasoningEffort}
	} else if raw.Thinking != nil && raw.Thinking.Type != "" {
		switch strings.ToLower(raw.Thinking.Type) {
		case "disabled":
			req.Reasoning = &core.ReasoningConfig{Effort: "none"}
		case "enabled":
			req.Reasoning = &core.ReasoningConfig{Effort: "auto"}
		}
	}

	for _, t := range raw.Tools {
		if t.Type != "function" && t.Type != "" {
			continue
		}
		req.Tools = append(req.Tools, core.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	for _, m := range raw.Messages {
		msg, isSystem, sysText := parseOAIMessage(m)
		if isSystem {
			// Hoist system content to the top-level field; concatenate multiples.
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += sysText
			continue
		}
		req.Messages = append(req.Messages, msg)
	}
	return req, nil
}

// decodeOAIStop accepts OpenAI's stop parameter as either a single string or
// an array of strings.
func decodeOAIStop(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	return nil
}

// parseOAIMessage converts one OpenAI message to canonical form. System and
// developer roles are reported separately so the caller can hoist them.
func parseOAIMessage(m oaiMessage) (msg core.Message, isSystem bool, sysText string) {
	role := m.Role
	// OpenAI's "developer" role is a system-equivalent for newer models.
	if role == "system" || role == "developer" {
		return core.Message{}, true, decodeOAIContentText(m.Content)
	}

	msg.Role = mapOAIRole(role)
	msg.Name = m.Name

	// Preserve reasoning_content as a thinking part so it gets echoed back
	// on follow-up turns (required by DeepSeek, MiniMax, etc.).
	if m.ReasoningContent != "" && role == "assistant" {
		msg.Content = append(msg.Content, core.ContentPart{
			Type: core.PartThinking,
			Text: m.ReasoningContent,
		})
	}

	// Assistant tool calls.
	for _, tc := range m.ToolCalls {
		msg.Content = append(msg.Content, core.ContentPart{
			Type: core.PartToolCall,
			ToolCall: &core.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: normalizeOpenAIToolArguments(tc.Function.Arguments),
			},
		})
	}

	// Tool result message. Text becomes Content; images/files (screenshot
	// tools) are kept as Parts so Anthropic/Gemini upstreams still see them.
	if role == "tool" {
		result := &core.ToolResult{CallID: m.ToolCallID}
		var text strings.Builder
		for _, p := range decodeOAIContentParts(m.Content) {
			if p.Type == core.PartText {
				text.WriteString(p.Text)
				continue
			}
			result.Parts = append(result.Parts, p)
		}
		result.Content = text.String()
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolResult, ToolResult: result})
		return msg, false, ""
	}

	// Text / multimodal content parts.
	msg.Content = append(msg.Content, decodeOAIContentParts(m.Content)...)
	return msg, false, ""
}

func mapOAIRole(role string) core.Role {
	switch role {
	case "user":
		return core.RoleUser
	case "assistant":
		return core.RoleAssistant
	case "tool":
		return core.RoleTool
	default:
		return core.RoleUser
	}
}

// decodeOAIContentText extracts plain text from an OpenAI content field, which
// may be a string or an array of typed parts.
func decodeOAIContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var b strings.Builder
	for _, p := range decodeOAIContentParts(raw) {
		if p.Type == core.PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// decodeOAIContentParts extracts canonical content parts from an OpenAI content
// field (string or array of {type,text|image_url}).
func decodeOAIContentParts(raw json.RawMessage) []core.ContentPart {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []core.ContentPart{{Type: core.PartText, Text: s}}
	}

	var arr []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
		File struct {
			FileData string `json:"file_data"`
			Filename string `json:"filename"`
			FileID   string `json:"file_id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}

	var parts []core.ContentPart
	for _, p := range arr {
		switch p.Type {
		case "text":
			parts = append(parts, core.ContentPart{Type: core.PartText, Text: p.Text})
		case "image_url":
			media := parseOAIImageURL(p.ImageURL.URL)
			parts = append(parts, core.ContentPart{Type: core.PartImage, Media: media})
		case "file":
			// {"type":"file","file":{"filename":"x.pdf","file_data":"data:application/pdf;base64,..."}}
			if p.File.FileData != "" {
				media := parseOAIImageURL(p.File.FileData)
				if media.MIMEType == "" {
					media.MIMEType = "application/pdf"
				}
				parts = append(parts, core.ContentPart{Type: core.PartDocument, Media: media})
			}
		}
	}
	return parts
}

// parseOAIImageURL decomposes an OpenAI image_url value into a MediaPayload.
// If the URL is a data URI (data:<mime>;base64,<data>), MIMEType and Data are
// populated; otherwise the URL is kept as-is for cross-dialect forwarding.
func parseOAIImageURL(rawURL string) *core.MediaPayload {
	if strings.HasPrefix(rawURL, "data:") {
		// Format: data:<mediatype>;base64,<data>
		rest, ok := strings.CutPrefix(rawURL, "data:")
		if ok {
			if idx := strings.Index(rest, ";base64,"); idx > 0 {
				return &core.MediaPayload{
					MIMEType: rest[:idx],
					Data:     rest[idx+8:],
				}
			}
		}
	}
	return &core.MediaPayload{URL: rawURL}
}

// mediaToDataURL converts a MediaPayload into a URL suitable for the OpenAI
// image_url format. If the media has inline base64 data, it is wrapped in a
// data URI. If it already has a URL, that URL is returned as-is.
func mediaToDataURL(m *core.MediaPayload) string {
	if m.Data != "" {
		mime := m.MIMEType
		if mime == "" {
			mime = "image/png"
		}
		return "data:" + mime + ";base64," + m.Data
	}
	return m.URL
}

// ---- request rendering ------------------------------------------------------

// needsJSONSchemaFallback checks if response_format uses json_schema and the
// provider doesn't support native structured output. Providers that support
// json_schema natively (like OpenAI, Azure) are excluded.
func needsJSONSchemaFallback(providerID string, responseFormat json.RawMessage) bool {
	if len(responseFormat) == 0 {
		return false
	}
	var rf struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(responseFormat, &rf); err != nil || rf.Type != "json_schema" {
		return false
	}
	// Providers that support native json_schema.
	switch providerID {
	case "openai", "azure":
		return false
	}
	return true
}

// fallbackResponseFormat converts json_schema to json_object for providers
// that don't support native structured output.
func fallbackResponseFormat(responseFormat json.RawMessage) json.RawMessage {
	if len(responseFormat) == 0 {
		return responseFormat
	}
	var rf struct {
		Type       string `json:"type"`
		JSONSchema any    `json:"json_schema"`
	}
	if err := json.Unmarshal(responseFormat, &rf); err != nil {
		return responseFormat
	}
	if rf.Type != "json_schema" {
		return responseFormat
	}
	// Fall back to plain json_object mode.
	out, _ := json.Marshal(map[string]string{"type": "json_object"})
	return out
}

// reasoningPlaceholder is injected as reasoning_content on assistant messages
// that lack real reasoning when the target requires it. A single space is the
// minimal non-empty value that satisfies upstream "must be passed back"
// validation without polluting the conversation.
const reasoningPlaceholder = " "

// reasoningScope controls how aggressively reasoning_content is injected on
// assistant messages that lack it. DeepSeek requires it on ALL assistant
// turns, while Kimi only requires it on turns that carry tool_calls.
type reasoningScope int

const (
	reasoningNone      reasoningScope = iota
	reasoningAll                      // inject on all assistant messages (DeepSeek)
	reasoningToolCalls                // inject only on assistant messages with tool_calls (Kimi)
)

// reasoningEchoScope classifies a provider/model into the reasoning_content
// injection scope. DeepSeek's thinking-capable models (including those served
// via OpenAI-compatible aggregators like OpenRouter, SiliconFlow, Fireworks)
// require it on every assistant turn. Kimi models require it only on turns
// that carry tool_calls. GLM/Zhipu and MiniMax models also emit structured
// reasoning_content and need it echoed back to maintain thinking context.
// Other providers don't need it at all.
func reasoningEchoScope(providerID, model string) reasoningScope {
	m := strings.ToLower(model)
	if providerID == "deepseek" || strings.Contains(m, "deepseek") {
		return reasoningAll
	}
	// Kimi models (e.g. kimi-k2, kimi-latest) require reasoning_content only
	// on assistant turns with tool_calls.
	if strings.HasPrefix(m, "kimi-") || strings.HasPrefix(m, "moonshot/kimi-") {
		return reasoningToolCalls
	}
	// GLM/Zhipu models (glm-5.2, glm-4.7, zai-org/glm-*, etc.) emit structured
	// reasoning_content and expect it echoed back on follow-up turns, similar
	// to DeepSeek. Without this, the model may skip reasoning or reject the
	// request, causing clients to retry and potentially duplicate responses.
	if providerID == "glm" || providerID == "glm-cn" || providerID == "zai" ||
		strings.Contains(m, "glm-") || strings.Contains(m, "zai-org/glm") {
		return reasoningAll
	}
	// MiniMax models (minimax-m1, abab6.5-chat, etc.) also use reasoning_content.
	if strings.Contains(m, "minimax") || strings.Contains(m, "abab") {
		return reasoningAll
	}
	return reasoningNone
}

// requiresReasoningEcho reports whether the given provider/model echoes
// reasoning_content in thinking mode and rejects (400) follow-up turns that
// omit it. This is kept for backward compatibility with tests and for gating
// DeepSeek-specific request fixes.
func requiresReasoningEcho(providerID, model string) bool {
	return reasoningEchoScope(providerID, model) != reasoningNone
}

// isDeepSeekTarget reports whether the target is a DeepSeek model. Used to gate
// DeepSeek-specific request fixes (thinking mode, tool ID sanitization, missing
// tool response fills) that should not run for Kimi or other providers.
func isDeepSeekTarget(providerID, model string) bool {
	if providerID == "deepseek" {
		return true
	}
	return strings.Contains(strings.ToLower(model), "deepseek")
}

// shouldInjectReasoning reports whether a placeholder reasoning_content should
// be injected on the given rendered message. The scope controls the breadth:
//   - reasoningAll: all assistant messages without real reasoning (DeepSeek)
//   - reasoningToolCalls: only assistant messages that carry tool_calls (Kimi)
//
// Messages that already have genuine reasoning_content are never overwritten so
// the real chain-of-thought is preserved.
func shouldInjectReasoning(scope reasoningScope, msg oaiMessage) bool {
	if scope == reasoningNone || msg.Role != string(core.RoleAssistant) {
		return false
	}
	if msg.ReasoningContent != "" {
		return false // genuine reasoning — never overwrite
	}
	if scope == reasoningToolCalls {
		return len(msg.ToolCalls) > 0
	}
	return true // reasoningAll
}

// RenderRequestForProvider renders an OpenAI request for a specific provider,
// applying provider-specific fallbacks (e.g. json_schema → json_object) and,
// for reasoning providers, ensuring assistant messages carry reasoning_content.
func (c OpenAICodec) RenderRequestForProvider(req *core.ChatRequest, providerID string) ([]byte, error) {
	scope := reasoningEchoScope(providerID, req.Model)
	if needsJSONSchemaFallback(providerID, req.ResponseFormat) {
		clone := *req
		clone.ResponseFormat = fallbackResponseFormat(req.ResponseFormat)
		return renderOAIRequestForProvider(&clone, providerID, scope)
	}
	return renderOAIRequestForProvider(req, providerID, scope)
}

func (OpenAICodec) RenderRequest(req *core.ChatRequest) ([]byte, error) {
	return renderOAIRequest(req, reasoningNone)
}

func renderOAIRequestForProvider(req *core.ChatRequest, providerID string, scope reasoningScope) ([]byte, error) {
	out, err := buildOAIRequest(req, scope)
	if err != nil {
		return nil, err
	}
	out.Extra = filterExtraForProvider(req.Extra, providerID)
	if providerID == "openrouter" {
		// Ask OpenRouter to report the exact charge with the usage block.
		out.UsageAccounting = &oaiUsageAccounting{Include: true}
	}
	if providerID == "cloudflare-ai" {
		// Workers AI model ids are namespaced ("@cf/meta/..."); a bare
		// "meta/..." answers 400. Accept the short form clients tend to type.
		out.Model = canonicalCloudflareModel(out.Model)
	}
	if isDeepSeekTarget(providerID, req.Model) {
		applyDeepSeekRequestFixes(out, req, providerID)
	} else if isOpenAIFirstParty(providerID) {
		applyOpenAIFirstPartyFixes(out, req)
	} else if scope != reasoningNone {
		// Non-DeepSeek reasoning providers (GLM, Kimi, MiniMax, etc.) still
		// need thinking type + reasoning_effort forwarded. DeepSeek has its
		// own fixes above; this path handles the rest without duplicating
		// provider-specific hacks.
		applyGenericReasoningConfig(out, req)
	}
	if providerID == "codebuddy" {
		applyCodebuddyRequestFixes(out, req)
	}
	if stripReasoningFor(providerID) {
		stripReasoningContent(out)
	}
	if !isOpenAIFirstParty(providerID) {
		// "developer" is an OpenAI-only role; strict OpenAI-compatible
		// servers reject it. It is a system message everywhere else.
		for i := range out.Messages {
			if out.Messages[i].Role == string(core.RoleDeveloper) {
				out.Messages[i].Role = string(core.RoleSystem)
			}
		}
	}
	return json.Marshal(out)
}

// canonicalCloudflareModel gives bare Workers AI model names the "@cf/"
// namespace; names already namespaced (@cf/, @hf/) are untouched.
func canonicalCloudflareModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.HasPrefix(model, "@") {
		return model
	}
	return "@cf/" + model
}

// usdToNanos converts a provider-reported USD amount to nanodollars.
func usdToNanos(usd float64) int64 {
	if usd <= 0 {
		return 0
	}
	return int64(usd*1e9 + 0.5)
}

// isOpenAIFirstParty reports whether the provider is api.openai.com or Azure
// OpenAI, where the full parameter surface (max_completion_tokens,
// reasoning_effort, stream_options) is supported.
func isOpenAIFirstParty(providerID string) bool {
	return providerID == "openai" || providerID == "azure" || providerID == "azure_openai"
}

// isOpenAIReasoningModel reports whether the model is one of OpenAI's
// reasoning families, which reject max_tokens and accept reasoning_effort.
func isOpenAIReasoningModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "codex")
}

// applyOpenAIFirstPartyFixes adapts a request for api.openai.com / Azure:
// reasoning models take max_completion_tokens and reasoning_effort (never
// the vendor-specific "thinking" object), and streams ask for the final usage
// chunk so metering never has to estimate.
func applyOpenAIFirstPartyFixes(out *oaiRequest, req *core.ChatRequest) {
	if isOpenAIReasoningModel(req.Model) {
		if out.MaxTokens != nil {
			out.MaxCompletionTokens = out.MaxTokens
			out.MaxTokens = nil
		}
		if req.Reasoning != nil {
			switch effort := strings.ToLower(strings.TrimSpace(req.Reasoning.Effort)); effort {
			case "minimal", "low", "medium", "high", "xhigh":
				out.ReasoningEffort = effort
			case "max":
				out.ReasoningEffort = "xhigh"
			}
		}
		// Reasoning models only accept the default sampling values.
		out.Temperature = nil
		out.TopP = nil
	}
	if out.Stream {
		out.StreamOpts = &oaiStreamOpt{IncludeUsage: true}
	}
}

// renderOAIRequest renders a canonical request to the OpenAI wire format. When
// scope is non-none, assistant messages that carry no real reasoning get a
// placeholder reasoning_content so reasoning-mode providers (DeepSeek, Kimi,
// etc.) don't reject the follow-up turn with a 400. Messages with genuine
// reasoning are left untouched so the real chain-of-thought is preserved.
func renderOAIRequest(req *core.ChatRequest, scope reasoningScope) ([]byte, error) {
	out, err := buildOAIRequest(req, scope)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func buildOAIRequest(req *core.ChatRequest, scope reasoningScope) (*oaiRequest, error) {
	out := oaiRequest{
		Model:          req.Model,
		Temperature:    req.Temperature,
		TopP:           req.TopP,
		MaxTokens:      req.MaxTokens,
		Stream:         req.Stream,
		ResponseFormat: req.ResponseFormat,
	}
	if len(req.Stop) > 0 {
		out.Stop, _ = json.Marshal(req.Stop)
	}
	out.Extra = req.Extra
	// Only carry tool_choice when tools are present; providers such as Qwen
	// reject a tool_choice paired with an empty/absent tools array.
	if len(req.Tools) > 0 {
		out.ToolChoice = req.ToolChoice
		out.ParallelToolCalls = req.ParallelToolCalls
	}
	// Note: stream_options with include_usage is intentionally omitted. Many
	// OpenAI-compatible providers (MiMo, Volcengine, etc.) reject this field
	// with 400. Usage is captured from the final stream chunk instead.

	if req.System != "" {
		sysContent, _ := json.Marshal(req.System)
		out.Messages = append(out.Messages, oaiMessage{Role: "system", Content: sysContent})
	}

	for _, m := range req.Messages {
		for _, msg := range renderOAIMessages(m) {
			if shouldInjectReasoning(scope, msg) {
				msg.ReasoningContent = reasoningPlaceholder
			}
			out.Messages = append(out.Messages, msg)
		}
	}

	for _, t := range req.Tools {
		var tool oaiTool
		tool.Type = "function"
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = t.Parameters
		out.Tools = append(out.Tools, tool)
	}

	return &out, nil
}

func applyDeepSeekRequestFixes(out *oaiRequest, req *core.ChatRequest, providerID string) {
	applyDeepSeekThinking(out, req, providerID)
	normalizeDeepSeekToolMessages(out.Messages)
	out.Messages = fillMissingDeepSeekToolResponses(out.Messages)
}

// applyCodebuddyRequestFixes handles CodeBuddy-specific reasoning_effort
// semantics. CodeBuddy only surfaces model reasoning when the request carries
// OpenAI-style reasoning_effort + reasoning_summary:"auto". When reasoning is
// "none"/"off" the fields are omitted entirely.
func applyCodebuddyRequestFixes(out *oaiRequest, req *core.ChatRequest) {
	if req.Reasoning == nil {
		return
	}
	effort := strings.ToLower(req.Reasoning.Effort)
	if effort == "none" || effort == "off" {
		out.ReasoningEffort = ""
		return
	}
	if out.ReasoningEffort != "" {
		if out.ExtraBody == nil {
			out.ExtraBody = map[string]any{}
		}
		out.ExtraBody["reasoning_summary"] = "auto"
	}
}

// applyGenericReasoningConfig forwards reasoning_effort and thinking type to
// any provider that supports reasoning-mode requests. This was previously
// DeepSeek-only, but GLM, Kimi, MiniMax, and other reasoning providers also
// need these fields to emit structured reasoning_content.
func applyGenericReasoningConfig(out *oaiRequest, req *core.ChatRequest) {
	if req.Reasoning == nil {
		return
	}
	effort := strings.ToLower(req.Reasoning.Effort)
	switch effort {
	case "none", "off", "disabled":
		out.Thinking = &oaiThinking{Type: "disabled"}
		out.ReasoningEffort = ""
	case "", "auto", "adaptive":
		out.Thinking = &oaiThinking{Type: "enabled"}
		out.ReasoningEffort = ""
	case "xhigh":
		out.Thinking = &oaiThinking{Type: "enabled"}
		out.ReasoningEffort = "max"
	default:
		out.Thinking = &oaiThinking{Type: "enabled"}
		out.ReasoningEffort = effort
	}
}

func applyDeepSeekThinking(out *oaiRequest, req *core.ChatRequest, providerID string) {
	applyGenericReasoningConfig(out, req)
	if req.Reasoning != nil {
		switch strings.ToLower(req.Reasoning.Effort) {
		case "none", "off", "disabled":
			out.ReasoningEffort = ""
		case "max", "xhigh":
			out.ReasoningEffort = "max"
		default:
			out.ReasoningEffort = "high"
		}
	}

	if providerID != "deepseek" {
		return
	}
	switch strings.ToLower(req.Model) {
	case "deepseek-v4-pro-max":
		out.Model = "deepseek-v4-pro"
		out.Thinking = nil
		out.ReasoningEffort = "max"
		setDeepSeekExtraThinking(out, "enabled")
	case "deepseek-v4-pro-none":
		out.Model = "deepseek-v4-pro"
		out.Thinking = nil
		out.ReasoningEffort = ""
		setDeepSeekExtraThinking(out, "disabled")
	}
}

func setDeepSeekExtraThinking(out *oaiRequest, typ string) {
	if out.ExtraBody == nil {
		out.ExtraBody = map[string]any{}
	}
	out.ExtraBody["thinking"] = map[string]any{"type": typ}
}

func normalizeDeepSeekToolMessages(messages []oaiMessage) {
	for i := range messages {
		msg := &messages[i]
		if msg.Role == "tool" && msg.ToolCallID != "" {
			msg.ToolCallID = sanitizeDeepSeekToolID(msg.ToolCallID, i, 0, "")
		}
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			name := firstNonEmpty(tc.Function.Name, "tool")
			tc.ID = sanitizeDeepSeekToolID(tc.ID, i, j, name)
			if tc.Type == "" {
				tc.Type = "function"
			}
			tc.Function.Arguments = ensureToolArgumentsJSONString(tc.Function.Arguments)
		}
	}
}

func sanitizeDeepSeekToolID(id string, msgIndex, tcIndex int, toolName string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	name := sanitizeDeepSeekToolID(toolName, msgIndex, tcIndex, "")
	if name != "" {
		return fmt.Sprintf("call_msg%d_tc%d_%s", msgIndex, tcIndex, name)
	}
	return fmt.Sprintf("call_msg%d_tc%d", msgIndex, tcIndex)
}

func ensureToolArgumentsJSONString(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		if s == "" {
			s = "{}"
		}
		out, _ := json.Marshal(s)
		return out
	}
	out, _ := json.Marshal(string(trimmed))
	return out
}

func fillMissingDeepSeekToolResponses(messages []oaiMessage) []oaiMessage {
	var out []oaiMessage
	for i, msg := range messages {
		out = append(out, msg)
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		// Scan all subsequent consecutive tool messages to collect which
		// tool_call_ids already have a response. This correctly handles
		// multiple tool calls followed by multiple tool messages.
		responded := make(map[string]bool)
		for j := i + 1; j < len(messages) && messages[j].Role == "tool"; j++ {
			if messages[j].ToolCallID != "" {
				responded[messages[j].ToolCallID] = true
			}
		}
		for _, id := range deepSeekToolCallIDs(msg) {
			if responded[id] {
				continue
			}
			content, _ := json.Marshal("")
			out = append(out, oaiMessage{Role: "tool", ToolCallID: id, Content: content})
		}
	}
	return out
}

func deepSeekToolCallIDs(msg oaiMessage) []string {
	ids := make([]string, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		if tc.ID != "" {
			ids = append(ids, tc.ID)
		}
	}
	return ids
}

// reasoningPlaceholderMaxLen is the threshold below which reasoning_content is
// treated as a placeholder and left intact. The injected placeholder is a single
// space (" "); stripping it would re-trigger upstream complaints about missing
// reasoning_content on the next turn. Real chain-of-thought blocks exceed this
// length and are stripped to bound multi-turn input token growth.
const reasoningPlaceholderMaxLen = 8

// stripReasoningFor reports whether the target provider rejects echoed
// reasoning_content from prior assistant turns. Non-reasoning providers such
// as groq answer 400 with "property 'reasoning_content' is unsupported" when
// history carries it, so their requests are stripped before dispatch.
func stripReasoningFor(providerID string) bool {
	switch providerID {
	case "kimchi", "groq":
		return true
	}
	return false
}

// stripReasoningContent removes echoed reasoning_content from assistant messages
// in the outgoing request. Clients replay the full reasoning block from prior
// turns, which inflates input tokens on multi-turn conversations. Only real
// reasoning (length > reasoningPlaceholderMaxLen) is stripped; the minimal
// placeholder injected for upstream validation is preserved.
func stripReasoningContent(out *oaiRequest) {
	for i := range out.Messages {
		msg := &out.Messages[i]
		if msg.Role != string(core.RoleAssistant) {
			continue
		}
		if len(msg.ReasoningContent) > reasoningPlaceholderMaxLen {
			msg.ReasoningContent = ""
		}
	}
}

func hasDeepSeekToolResult(msg oaiMessage, id string) bool {
	return msg.Role == "tool" && msg.ToolCallID == id
}

// renderOAIMessages splits a canonical message into one or more OpenAI wire
// messages. In the OpenAI format, each tool result must be its own message with
// role "tool" and a tool_call_id. Other content (text, images, tool calls) is
// rendered into a single message via renderOAIMessage. Tool result messages
// are emitted first, followed by any remaining content.
//
// This is critical for cross-dialect translation: Anthropic groups multiple
// tool_result blocks into a single user message, but OpenAI (and DeepSeek)
// require each tool result as a separate "tool" role message. Without this
// splitting, assistant messages with N tool_calls would be followed by only 1
// tool message, triggering "insufficient tool messages" 400 errors.
func renderOAIMessages(m core.Message) []oaiMessage {
	// Fast path: no tool results → delegate to the single-message renderer.
	hasToolResult := false
	for _, p := range m.Content {
		if p.Type == core.PartToolResult {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		return []oaiMessage{renderOAIMessage(m)}
	}

	var out []oaiMessage
	// Emit each tool result as its own OpenAI tool message.
	for _, p := range m.Content {
		if p.Type != core.PartToolResult {
			continue
		}
		content, _ := json.Marshal(p.ToolResult.Content)
		out = append(out, oaiMessage{
			Role:       "tool",
			ToolCallID: p.ToolResult.CallID,
			Content:    content,
		})
	}

	// If there's remaining content (text, images, tool calls), render it
	// into a separate message with the original role.
	var remaining []core.ContentPart
	for _, p := range m.Content {
		if p.Type != core.PartToolResult {
			remaining = append(remaining, p)
		}
	}
	if len(remaining) > 0 {
		trimmed := core.Message{Role: m.Role, Name: m.Name, Content: remaining}
		out = append(out, renderOAIMessage(trimmed))
	}
	return out
}

func renderOAIMessage(m core.Message) oaiMessage {
	out := oaiMessage{Role: string(m.Role), Name: m.Name}

	var textParts []string
	var thinkingParts []string
	var hasMedia bool
	var contentParts []map[string]any

	for _, p := range m.Content {
		switch p.Type {
		case core.PartThinking:
			// Collect thinking/reasoning content for echoing back as
			// reasoning_content on DeepSeek, MiniMax, and similar providers.
			thinkingParts = append(thinkingParts, p.Text)
		case core.PartText:
			textParts = append(textParts, p.Text)
		case core.PartImage:
			hasMedia = true
			if p.Media != nil {
				url := mediaToDataURL(p.Media)
				contentParts = append(contentParts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": url},
				})
			}
		case core.PartDocument:
			hasMedia = true
			if p.Media != nil {
				contentParts = append(contentParts, map[string]any{
					"type": "file",
					"file": map[string]any{"filename": "document.pdf", "file_data": mediaToDataURL(p.Media)},
				})
			}
		case core.PartToolCall:
			var tc oaiToolCall
			tc.ID = p.ToolCall.ID
			tc.Type = "function"
			tc.Function.Name = p.ToolCall.Name
			tc.Function.Arguments = ensureToolArgumentsJSONString(p.ToolCall.Arguments)
			out.ToolCalls = append(out.ToolCalls, tc)
		case core.PartToolResult:
			// Tool results are handled by renderOAIMessages; if we reach
			// here via direct renderOAIMessage call, handle the first one
			// for backward compatibility.
			out.Role = "tool"
			out.ToolCallID = p.ToolResult.CallID
			content, _ := json.Marshal(p.ToolResult.Content)
			out.Content = content
			return out
		}
	}

	// Echo reasoning_content on assistant messages. DeepSeek, MiniMax, and
	// similar providers require this field on follow-up/tool-call turns to
	// avoid 400 errors ("reasoning_content must be passed back").
	if m.Role == core.RoleAssistant && len(thinkingParts) > 0 {
		out.ReasoningContent = strings.Join(thinkingParts, "")
	}

	// When images are present, use the array-of-parts content format (OpenAI
	// requires this for multimodal messages). Text-only messages use a plain
	// string for backward compatibility.
	if hasMedia {
		// Prepend any accumulated text as a text part.
		if len(textParts) > 0 {
			contentParts = append([]map[string]any{{
				"type": "text",
				"text": strings.Join(textParts, ""),
			}}, contentParts...)
		}
		b, _ := json.Marshal(contentParts)
		out.Content = b
	} else if len(textParts) > 0 {
		content, _ := json.Marshal(strings.Join(textParts, ""))
		out.Content = content
	}
	return out
}

// ---- response parsing -------------------------------------------------------

type oaiResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			// ReasoningContent carries thinking/reasoning text from models
			// that expose it as a structured field (DeepSeek, some MiMo).
			ReasoningContent string        `json:"reasoning_content"`
			ToolCalls        []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// Cost is the USD charge OpenRouter reports when usage accounting is
	// requested; it is exact and preferred over catalogue pricing.
	Cost float64 `json:"cost"`
	// PromptCacheHitTokens is DeepSeek's cache-read count (reported before
	// the OpenAI-style prompt_tokens_details existed; still emitted today).
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

func (c OpenAICodec) ParseResponse(body []byte, model string) (*core.ChatResponse, error) {
	var raw oaiResponse
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("openai: parse response: %w", err)
	}
	return c.buildResponse(raw, model)
}

// ParseResponseFrom implements StreamingResponseCodec. It decodes directly from
// an io.Reader, avoiding the intermediate []byte allocation.
func (c OpenAICodec) ParseResponseFrom(r io.Reader, model string) (*core.ChatResponse, error) {
	var raw oaiResponse
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("openai: parse response: %w", err)
	}
	return c.buildResponse(raw, model)
}

// buildResponse converts a parsed oaiResponse into a canonical ChatResponse.
func (OpenAICodec) buildResponse(raw oaiResponse, model string) (*core.ChatResponse, error) {
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("openai: response has no choices")
	}

	choice := raw.Choices[0]
	msg := core.Message{Role: core.RoleAssistant}

	// Extract thinking content: prefer structured reasoning_content field,
	// fall back to <think> tag extraction from content.
	thinkingText := choice.Message.ReasoningContent
	contentText := choice.Message.Content
	if thinkingText == "" && contentText != "" {
		thinkingChunks, clean := StripThinkTags(contentText)
		if len(thinkingChunks) > 0 {
			// Store thinking as the first content part.
			for _, tc := range thinkingChunks {
				msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: tc.Delta})
			}
			contentText = clean
		}
	} else if thinkingText != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: thinkingText})
	}

	if contentText != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: contentText})
	}
	for _, tc := range choice.Message.ToolCalls {
		msg.Content = append(msg.Content, core.ContentPart{
			Type: core.PartToolCall,
			ToolCall: &core.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: normalizeOpenAIToolArguments(tc.Function.Arguments),
			},
		})
	}

	resp := &core.ChatResponse{
		ID:           raw.ID,
		Model:        firstNonEmpty(raw.Model, model),
		Message:      msg,
		FinishReason: mapOAIFinish(choice.FinishReason),
	}
	if raw.Usage != nil {
		var cached, reasoning int
		if raw.Usage.PromptTokensDetails != nil {
			cached = raw.Usage.PromptTokensDetails.CachedTokens
		}
		if cached == 0 {
			cached = raw.Usage.PromptCacheHitTokens
		}
		if raw.Usage.CompletionTokensDetails != nil {
			reasoning = raw.Usage.CompletionTokensDetails.ReasoningTokens
		}
		resp.Usage = core.Usage{
			PromptTokens:      raw.Usage.PromptTokens,
			CompletionTokens:  raw.Usage.CompletionTokens,
			TotalTokens:       raw.Usage.TotalTokens,
			CachedTokens:      cached,
			ReasoningTokens:   reasoning,
			ProviderCostNanos: usdToNanos(raw.Usage.Cost),
			Source:            core.UsageSourceProvider,
		}
	}
	return resp, nil
}

func (OpenAICodec) RenderResponse(resp *core.ChatResponse) ([]byte, error) {
	out := map[string]any{
		"id":      firstNonEmpty(resp.ID, "chatcmpl-"+resp.Model),
		"object":  "chat.completion",
		"model":   resp.Model,
		"choices": []map[string]any{renderOAIChoice(resp)},
		"usage":   renderOAIUsage(resp.Usage),
	}
	return json.Marshal(out)
}

// renderOAIUsage renders usage in OpenAI's shape including the cached and
// reasoning token details clients use for cost display and context tracking.
func renderOAIUsage(u core.Usage) map[string]any {
	out := map[string]any{
		"prompt_tokens":     u.PromptTokens,
		"completion_tokens": u.CompletionTokens,
		"total_tokens":      u.TotalTokens,
	}
	if u.CachedTokens > 0 {
		out["prompt_tokens_details"] = map[string]int{"cached_tokens": u.CachedTokens}
	}
	if u.ReasoningTokens > 0 {
		out["completion_tokens_details"] = map[string]int{"reasoning_tokens": u.ReasoningTokens}
	}
	return out
}

func renderOAIChoice(resp *core.ChatResponse) map[string]any {
	message := map[string]any{"role": "assistant"}
	var text strings.Builder
	var thinking strings.Builder
	var toolCalls []map[string]any
	for _, p := range resp.Message.Content {
		switch p.Type {
		case core.PartThinking:
			thinking.WriteString(p.Text)
		case core.PartText:
			text.WriteString(p.Text)
		case core.PartToolCall:
			toolCalls = append(toolCalls, map[string]any{
				"id":   p.ToolCall.ID,
				"type": "function",
				"function": map[string]string{
					"name":      p.ToolCall.Name,
					"arguments": string(p.ToolCall.Arguments),
				},
			})
		}
	}
	if text.Len() > 0 {
		message["content"] = text.String()
	} else {
		message["content"] = nil
	}
	// Surface structured reasoning so clients can replay it on follow-up turns
	// (DeepSeek/MiniMax thinking mode require reasoning_content to be echoed
	// back or the next request returns a 400).
	if thinking.Len() > 0 {
		message["reasoning_content"] = thinking.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	return map[string]any{
		"index":         0,
		"message":       message,
		"finish_reason": string(resp.FinishReason),
	}
}

func mapOAIFinish(r string) core.FinishReason {
	switch r {
	case "stop":
		return core.FinishStop
	case "length":
		return core.FinishLength
	case "tool_calls", "function_call":
		return core.FinishToolCalls
	case "content_filter":
		return core.FinishFilter
	default:
		return core.FinishStop
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
