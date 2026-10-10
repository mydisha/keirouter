package transform

import (
	"bytes"
	"fmt"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/core"
)

// AnthropicCodec handles the Anthropic Messages wire format (/v1/messages).
type AnthropicCodec struct{}

func (AnthropicCodec) Dialect() core.Dialect { return core.DialectAnthropic }

// ---- wire types -------------------------------------------------------------

type antRequest struct {
	Model      string            `json:"model"`
	System     json.RawMessage   `json:"system,omitempty"`
	Messages   []antMessage      `json:"messages"`
	Tools      []json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage   `json:"tool_choice,omitempty"`
	MaxTokens  int               `json:"max_tokens"`
	Stream     bool              `json:"stream,omitempty"`
	Temp       *float64          `json:"temperature,omitempty"`
	TopP       *float64          `json:"top_p,omitempty"`
	Stop       []string          `json:"stop_sequences,omitempty"`
	// Thinking carries the extended-thinking configuration that clients like
	// Claude Code send. It must be forwarded to Anthropic-compatible upstreams
	// (e.g. GLM, Zhipu) or the model will not emit reasoning blocks, confusing
	// clients that expect them.
	Thinking json.RawMessage `json:"thinking,omitempty"`
	// OutputConfig carries effort for adaptive-thinking models.
	OutputConfig json.RawMessage `json:"output_config,omitempty"`
	// Metadata carries user_id for abuse tracking.
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type antMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type antBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Thinking holds the reasoning content for thinking blocks. Anthropic uses
	// "thinking" as the JSON key (not "text") for this block type.
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *antImageSource `json:"source,omitempty"`
	// Signature is the cryptographic proof tag for thinking blocks that must be
	// echoed back to the upstream on the next turn. Only the originating provider's
	// signatures are valid; foreign ones (from combo-mixed models) are rejected.
	Signature string `json:"signature,omitempty"`
	// Data is the opaque payload of a redacted_thinking block.
	Data string `json:"data,omitempty"`
	// CacheControl is the prompt-caching breakpoint on this block.
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

type antImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type antTool struct {
	Type         string          `json:"type,omitempty"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// antEmptyInputSchema is the schema Anthropic requires for a parameter-less
// tool; omitting input_schema entirely is a 400.
var antEmptyInputSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// isAntServerTool reports whether a tool definition is a provider-native
// (server) tool rather than a custom function: web_search_*, bash_*,
// text_editor_*, computer_*, code_execution_*, web_fetch_*, memory_*.
func isAntServerTool(typ string) bool {
	return typ != "" && typ != "custom"
}

// ---- request parsing --------------------------------------------------------

func (AnthropicCodec) ParseRequest(body []byte) (*core.ChatRequest, error) {
	var raw antRequest
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("anthropic: parse request: %w", err)
	}

	maxTokens := raw.MaxTokens
	systemText, systemCache := decodeAntSystemBlocks(raw.System)
	req := &core.ChatRequest{
		Model:              raw.Model,
		System:             systemText,
		SystemCacheControl: systemCache,
		Temperature:        raw.Temp,
		TopP:               raw.TopP,
		Stop:               raw.Stop,
		Stream:             raw.Stream,
	}
	if maxTokens > 0 {
		req.MaxTokens = &maxTokens
	}

	for _, rawTool := range raw.Tools {
		var t antTool
		if err := json.Unmarshal(rawTool, &t); err != nil {
			continue
		}
		if isAntServerTool(t.Type) {
			// Server tools have no function schema; keep the definition
			// verbatim for same-dialect upstreams.
			req.Tools = append(req.Tools, core.Tool{Name: t.Name, Raw: rawTool})
			continue
		}
		req.Tools = append(req.Tools, core.Tool{
			Name:         t.Name,
			Description:  t.Description,
			Parameters:   t.InputSchema,
			CacheControl: t.CacheControl,
		})
	}
	req.ToolChoice = claudeToolChoiceToOpenAI(raw.ToolChoice)
	if len(raw.ToolChoice) > 0 {
		var tc struct {
			DisableParallel *bool `json:"disable_parallel_tool_use"`
		}
		if json.Unmarshal(raw.ToolChoice, &tc) == nil && tc.DisableParallel != nil {
			parallel := !*tc.DisableParallel
			req.ParallelToolCalls = &parallel
		}
	}

	for _, m := range raw.Messages {
		req.Messages = append(req.Messages, parseAntMessage(m))
	}

	// Parse thinking configuration from the raw body (before unmarshaling)
	req.Reasoning = parseAntThinkingFromBytes(body)

	return req, nil
}

// parseAntThinkingFromBytes extracts thinking configuration from raw JSON bytes.
// This is called before the antRequest struct is unmarshaled to capture the
// thinking field which is not part of the antRequest struct.
func parseAntThinkingFromBytes(body []byte) *core.ReasoningConfig {
	var thinkingWrapper struct {
		Thinking *struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens,omitempty"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(body, &thinkingWrapper); err == nil && thinkingWrapper.Thinking != nil {
		cfg := &core.ReasoningConfig{}
		switch thinkingWrapper.Thinking.Type {
		case "enabled":
			cfg.Effort = "high"
			if thinkingWrapper.Thinking.BudgetTokens > 0 {
				cfg.MaxTokens = thinkingWrapper.Thinking.BudgetTokens
			}
		case "adaptive":
			cfg.Effort = "adaptive"
		}
		return cfg
	}
	return nil
}

func decodeAntSystem(raw json.RawMessage) string {
	text, _ := decodeAntSystemBlocks(raw)
	return text
}

// decodeAntSystemBlocks flattens the system prompt (string or block array)
// to text and returns the cache_control of the last block that had one. A
// breakpoint on any system block caches the prefix up to that block; after
// flattening, one breakpoint on the whole system text preserves that.
func decodeAntSystemBlocks(raw json.RawMessage) (string, json.RawMessage) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []antBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil
	}
	var out strings.Builder
	var cache json.RawMessage
	for _, b := range blocks {
		if b.Type == "text" {
			out.WriteString(b.Text)
		}
		if len(b.CacheControl) > 0 {
			cache = b.CacheControl
		}
	}
	return out.String(), cache
}

func parseAntMessage(m antMessage) core.Message {
	msg := core.Message{Role: mapAntRole(m.Role)}

	// Content may be a plain string or an array of blocks.
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: s})
		return msg
	}

	var blocks []antBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return msg
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: b.Text, CacheControl: b.CacheControl})
		case "document":
			if b.Source != nil {
				msg.Content = append(msg.Content, core.ContentPart{
					Type:         core.PartDocument,
					Media:        &core.MediaPayload{MIMEType: b.Source.MediaType, Data: b.Source.Data, URL: b.Source.URL},
					CacheControl: b.CacheControl,
				})
			}
		case "thinking":
			// Anthropic thinking blocks carry content in the "thinking" field
			// (not "text"). Signature must be preserved for echoing back on
			// follow-up turns — the upstream validates it.
			msg.Content = append(msg.Content, core.ContentPart{
				Type:      core.PartThinking,
				Text:      b.Thinking,
				Signature: b.Signature,
			})
		case "redacted_thinking":
			msg.Content = append(msg.Content, core.ContentPart{Type: core.PartRedactedThinking, Text: b.Data})
		case "tool_use":
			msg.Content = append(msg.Content, core.ContentPart{
				Type:         core.PartToolCall,
				ToolCall:     &core.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input},
				CacheControl: b.CacheControl,
			})
		case "tool_result":
			text, parts := decodeAntToolResultContent(b.Content)
			msg.Content = append(msg.Content, core.ContentPart{
				Type: core.PartToolResult,
				ToolResult: &core.ToolResult{
					CallID:  b.ToolUseID,
					Content: text,
					IsError: b.IsError,
					Parts:   parts,
				},
				CacheControl: b.CacheControl,
			})
		case "image":
			if b.Source != nil {
				part := core.ContentPart{Type: core.PartImage, CacheControl: b.CacheControl}
				if b.Source.Type == "url" && b.Source.URL != "" {
					part.Media = &core.MediaPayload{URL: b.Source.URL}
				} else {
					part.Media = &core.MediaPayload{MIMEType: b.Source.MediaType, Data: b.Source.Data}
				}
				msg.Content = append(msg.Content, part)
			}
		}
	}
	return msg
}

// decodeAntToolResultContent splits tool_result content into its text and any
// media blocks (screenshots, documents) so neither is lost in translation.
func decodeAntToolResultContent(raw json.RawMessage) (string, []core.ContentPart) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []antBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return string(raw), nil
	}
	var out strings.Builder
	var parts []core.ContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out.WriteString(b.Text)
		case "image", "document":
			if b.Source == nil {
				continue
			}
			typ := core.PartImage
			if b.Type == "document" {
				typ = core.PartDocument
			}
			parts = append(parts, core.ContentPart{
				Type:  typ,
				Media: &core.MediaPayload{MIMEType: b.Source.MediaType, Data: b.Source.Data, URL: b.Source.URL},
			})
		}
	}
	return out.String(), parts
}

func mapAntRole(role string) core.Role {
	switch role {
	case "assistant":
		return core.RoleAssistant
	default:
		return core.RoleUser
	}
}

// ---- request rendering ------------------------------------------------------

// antDefaultMaxOutput is the conservative output ceiling for Claude models.
const antDefaultMaxOutput = 64000

func (AnthropicCodec) RenderRequest(req *core.ChatRequest) ([]byte, error) {
	maxTokens := 4096
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	thinkingBudget := 0

	// Reconcile max_tokens against thinking budget. Anthropic requires
	// max_tokens strictly greater than budget_tokens (else 400). Prefer raising
	// max_tokens to preserve the requested thinking depth; if the budget alone
	// meets/exceeds the ceiling, cap output and shrink the budget so some tokens
	// remain for the answer.
	ceiling := antDefaultMaxOutput
	adaptive := anthropicUsesAdaptiveThinking(req.Model)
	explicitMaxTokens := req.MaxTokens != nil && *req.MaxTokens > 0
	if req.Reasoning != nil && req.Reasoning.MaxTokens > 0 {
		thinkingBudget = req.Reasoning.MaxTokens
	} else if req.Reasoning != nil && !adaptive {
		// OpenAI-style reasoning_effort carries no budget; Anthropic's
		// budget-style thinking needs one or it answers 400. Map effort to
		// LiteLLM's budgets and give the answer room above the budget.
		if budget := anthropicEffortBudget(req.Reasoning.Effort); budget > 0 {
			thinkingBudget = budget
			if !explicitMaxTokens {
				maxTokens = min(budget+antThinkingAnswerReserve, ceiling)
			}
		}
	}
	if thinkingBudget > 0 {
		if thinkingBudget >= maxTokens {
			// Raise max_tokens to preserve thinking depth (up to ceiling)
			maxTokens = min(thinkingBudget+1024, ceiling)
			if thinkingBudget >= maxTokens {
				// Budget exceeds ceiling; shrink budget so 1024 tokens remain for answer
				// Note: We don't mutate req.Reasoning, the reconciliation happens at render time
				thinkingBudget = max(1024, maxTokens-1024)
			}
		}
	}

	out := antRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Stream:    req.Stream,
		Temp:      req.Temperature,
		TopP:      req.TopP,
		Stop:      req.Stop,
	}
	// claude-opus-4 deprecated temperature and returns a 400 when it is present.
	if modelRejectsTemperature(req.Model) {
		out.Temp = nil
	}

	// Forward extended-thinking configuration to Anthropic-compatible upstreams.
	// Claude Code sends thinking: {type: "enabled", budget_tokens: N} and expects
	// reasoning blocks in the response. Dropping this field causes the upstream
	// (GLM, Zhipu, etc.) to skip reasoning, which confuses clients and may
	// trigger retries.
	thinkingEnabled := false
	if req.Reasoning != nil {
		effort := strings.ToLower(strings.TrimSpace(req.Reasoning.Effort))
		var thinking map[string]any
		switch effort {
		case "adaptive", "auto":
			thinking = map[string]any{"type": "adaptive"}
		case "", "none", "off", "disabled":
			if thinkingBudget > 0 {
				thinking = map[string]any{"type": "enabled"}
			}
		default:
			if adaptive && thinkingBudget == 0 {
				// Claude 4.6+ picks its own budget; effort steers it via
				// output_config instead of a fixed token budget.
				thinking = map[string]any{"type": "adaptive"}
				if level := anthropicOutputEffort(effort); level != "" {
					out.OutputConfig, _ = json.Marshal(map[string]any{"effort": level})
				}
			} else {
				thinking = map[string]any{"type": "enabled"}
			}
		}
		if thinking != nil && thinking["type"] == "enabled" {
			if thinkingBudget <= 0 {
				thinkingBudget = antMinThinkingBudget
				if thinkingBudget >= maxTokens {
					maxTokens = min(thinkingBudget+antThinkingAnswerReserve, ceiling)
				}
				out.MaxTokens = maxTokens
			}
			thinking["budget_tokens"] = thinkingBudget
		}
		if thinking != nil {
			thinkingEnabled = true
			if raw, err := json.Marshal(thinking); err == nil {
				out.Thinking = raw
			}
		}
	}
	if thinkingEnabled {
		// Anthropic rejects sampling overrides while thinking is enabled
		// (only the default of 1 is accepted). Drop them rather than fail.
		if out.Temp != nil && *out.Temp != 1 {
			out.Temp = nil
		}
		if out.TopP != nil && *out.TopP != 1 {
			out.TopP = nil
		}
	}

	if req.System != "" {
		if len(req.SystemCacheControl) > 0 {
			out.System, _ = json.Marshal([]antBlock{{Type: "text", Text: req.System, CacheControl: req.SystemCacheControl}})
		} else {
			out.System, _ = json.Marshal(req.System)
		}
	}
	if user := extraString(req, "user"); user != "" {
		// OpenAI's user id maps to Anthropic's abuse-tracking metadata.
		out.Metadata, _ = json.Marshal(map[string]string{"user_id": user})
	}

	for _, t := range req.Tools {
		if len(t.Raw) > 0 {
			out.Tools = append(out.Tools, t.Raw)
			continue
		}
		schema := t.Parameters
		if len(bytes.TrimSpace(schema)) == 0 || bytes.Equal(bytes.TrimSpace(schema), []byte("null")) {
			schema = antEmptyInputSchema
		}
		encoded, err := json.Marshal(antTool{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  schema,
			CacheControl: t.CacheControl,
		})
		if err != nil {
			continue
		}
		out.Tools = append(out.Tools, encoded)
	}

	// Render tool_choice only when tools are declared; Anthropic rejects a
	// tool_choice with an empty tools array.
	if len(out.Tools) > 0 {
		tc := openAIToolChoiceToClaude(req.ToolChoice)
		tcMap, _ := tc.(map[string]any)
		if thinkingEnabled && tcMap != nil {
			// Forced tool use is incompatible with extended thinking (400);
			// LiteLLM downgrades it to auto, so do we.
			if typ, _ := tcMap["type"].(string); typ == "any" || typ == "tool" {
				tcMap = map[string]any{"type": "auto"}
				tc = tcMap
			}
		}
		if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
			if tcMap == nil {
				tcMap = map[string]any{"type": "auto"}
				tc = tcMap
			}
			if typ, _ := tcMap["type"].(string); typ != "none" {
				tcMap["disable_parallel_tool_use"] = true
			}
		}
		if tc != nil {
			if raw, err := json.Marshal(tc); err == nil {
				out.ToolChoice = raw
			}
		}
	}

	// Anthropic requires alternating user/assistant roles and groups tool
	// results into user messages. We render each canonical message to a block
	// array; consecutive same-role messages are merged.
	type pending struct {
		role   string
		blocks []antBlock
	}
	var merged []pending
	for _, m := range req.Messages {
		blocks := renderAntBlocks(m)
		role := "user"
		if m.Role == core.RoleAssistant {
			role = "assistant"
		}
		// Anthropic forbids consecutive messages with the same role: merge
		// their blocks before encoding so each message is marshalled once.
		if n := len(merged); n > 0 && merged[n-1].role == role {
			merged[n-1].blocks = append(merged[n-1].blocks, blocks...)
			continue
		}
		merged = append(merged, pending{role: role, blocks: blocks})
	}
	out.Messages = make([]antMessage, 0, len(merged))
	for _, pm := range merged {
		raw, err := json.Marshal(pm.blocks)
		if err != nil {
			return nil, fmt.Errorf("anthropic: encode message: %w", err)
		}
		out.Messages = append(out.Messages, antMessage{Role: pm.role, Content: raw})
	}

	return json.Marshal(out)
}

func renderAntBlocks(m core.Message) []antBlock {
	var blocks []antBlock
	for _, p := range m.Content {
		switch p.Type {
		case core.PartText:
			if p.Text == "" {
				continue
			}
			blocks = append(blocks, antBlock{Type: "text", Text: p.Text, CacheControl: p.CacheControl})
		case core.PartDocument:
			if src := antMediaSource(p.Media); src != nil {
				blocks = append(blocks, antBlock{Type: "document", Source: src, CacheControl: p.CacheControl})
			}
		case core.PartThinking:
			// Render thinking with the correct "thinking" JSON key and echo back
			// the signature. A thinking block without a signature (reasoning
			// synthesized by a non-Anthropic upstream, or a fallback across
			// providers) is rejected by Anthropic with 400, so it is dropped —
			// the assistant turn's text and tool calls carry the state that
			// matters.
			if p.Signature == "" || p.Text == "" {
				continue
			}
			blocks = append(blocks, antBlock{Type: "thinking", Thinking: p.Text, Signature: p.Signature})
		case core.PartRedactedThinking:
			if p.Text != "" {
				blocks = append(blocks, antBlock{Type: "redacted_thinking", Data: p.Text})
			}
		case core.PartToolCall:
			blocks = append(blocks, antBlock{
				Type:         "tool_use",
				ID:           p.ToolCall.ID,
				Name:         p.ToolCall.Name,
				Input:        normalizeAntToolInputRaw(p.ToolCall.Arguments),
				CacheControl: p.CacheControl,
			})
		case core.PartToolResult:
			blocks = append(blocks, antBlock{
				Type:         "tool_result",
				ToolUseID:    p.ToolResult.CallID,
				Content:      renderAntToolResultContent(p.ToolResult),
				IsError:      p.ToolResult.IsError,
				CacheControl: p.CacheControl,
			})
		case core.PartImage:
			if src := antMediaSource(p.Media); src != nil {
				blocks = append(blocks, antBlock{Type: "image", Source: src, CacheControl: p.CacheControl})
			}
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, antBlock{Type: "text", Text: " "})
	}
	return blocks
}

// antMediaSource renders a media payload as an Anthropic source object.
func antMediaSource(m *core.MediaPayload) *antImageSource {
	if m == nil {
		return nil
	}
	if m.Data != "" {
		return &antImageSource{Type: "base64", MediaType: m.MIMEType, Data: m.Data}
	}
	if m.URL != "" {
		return &antImageSource{Type: "url", URL: m.URL}
	}
	return nil
}

// renderAntToolResultContent renders a tool result as a plain string when it
// is text-only, or as a block array when it carries screenshots/documents.
func renderAntToolResultContent(r *core.ToolResult) json.RawMessage {
	if len(r.Parts) == 0 {
		content, _ := json.Marshal(r.Content)
		return content
	}
	var blocks []antBlock
	if r.Content != "" {
		blocks = append(blocks, antBlock{Type: "text", Text: r.Content})
	}
	for _, p := range r.Parts {
		src := antMediaSource(p.Media)
		if src == nil {
			continue
		}
		typ := "image"
		if p.Type == core.PartDocument {
			typ = "document"
		}
		blocks = append(blocks, antBlock{Type: typ, Source: src})
	}
	if len(blocks) == 0 {
		return json.RawMessage(`""`)
	}
	content, _ := json.Marshal(blocks)
	return content
}

// extraString returns a string-valued passthrough parameter from req.Extra.
func extraString(req *core.ChatRequest, key string) string {
	raw, ok := req.Extra[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func normalizeAntToolInputRaw(raw json.RawMessage) json.RawMessage {
	if antToolInputIsObject(raw) {
		return raw
	}
	return json.RawMessage(`{}`)
}

func normalizeAntToolInputValue(raw json.RawMessage) any {
	raw = normalizeAntToolInputRaw(raw)
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return map[string]any{}
	}
	return input
}

func antToolInputIsObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	return raw[0] == '{'
}

// ---- response parsing -------------------------------------------------------

type antResponse struct {
	ID         string     `json:"id"`
	Model      string     `json:"model"`
	Content    []antBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func (AnthropicCodec) ParseResponse(body []byte, model string) (*core.ChatResponse, error) {
	var raw antResponse
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("anthropic: parse response: %w", err)
	}

	msg := core.Message{Role: core.RoleAssistant}
	for _, b := range raw.Content {
		switch b.Type {
		case "text":
			msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: b.Text})
		case "thinking":
			msg.Content = append(msg.Content, core.ContentPart{
				Type: core.PartThinking, Text: firstNonEmpty(b.Thinking, b.Text), Signature: b.Signature,
			})
		case "redacted_thinking":
			msg.Content = append(msg.Content, core.ContentPart{Type: core.PartRedactedThinking, Text: b.Data})
		case "tool_use":
			msg.Content = append(msg.Content, core.ContentPart{
				Type:     core.PartToolCall,
				ToolCall: &core.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input},
			})
		}
	}

	return &core.ChatResponse{
		ID:           raw.ID,
		Model:        firstNonEmpty(raw.Model, model),
		Message:      msg,
		FinishReason: mapAntStop(raw.StopReason),
		Usage: core.Usage{
			PromptTokens:     raw.Usage.InputTokens + raw.Usage.CacheReadInputTokens + raw.Usage.CacheCreationInputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens: raw.Usage.InputTokens + raw.Usage.CacheReadInputTokens +
				raw.Usage.CacheCreationInputTokens + raw.Usage.OutputTokens,
			CachedTokens:     raw.Usage.CacheReadInputTokens,
			CacheWriteTokens: raw.Usage.CacheCreationInputTokens,
			Source:           core.UsageSourceProvider,
		},
	}, nil
}

func (AnthropicCodec) RenderResponse(resp *core.ChatResponse) ([]byte, error) {
	var content []map[string]any
	for _, p := range resp.Message.Content {
		switch p.Type {
		case core.PartText:
			content = append(content, map[string]any{"type": "text", "text": p.Text})
		case core.PartThinking:
			block := map[string]any{"type": "thinking", "thinking": p.Text}
			if p.Signature != "" {
				block["signature"] = p.Signature
			}
			content = append(content, block)
		case core.PartRedactedThinking:
			content = append(content, map[string]any{"type": "redacted_thinking", "data": p.Text})
		case core.PartToolCall:
			content = append(content, map[string]any{
				"type": "tool_use", "id": p.ToolCall.ID, "name": p.ToolCall.Name, "input": normalizeAntToolInputValue(p.ToolCall.Arguments),
			})
		}
	}
	out := map[string]any{
		"id":          firstNonEmpty(resp.ID, "msg_"+resp.Model),
		"type":        "message",
		"role":        "assistant",
		"model":       resp.Model,
		"content":     content,
		"stop_reason": renderAntStop(resp.FinishReason),
		"usage":       renderAntUsage(resp.Usage),
	}
	return json.Marshal(out)
}

// renderAntUsage renders canonical usage in Anthropic's shape, where
// input_tokens excludes cache reads/writes and the cache counts are reported
// separately. Canonical PromptTokens is the all-inclusive figure.
func renderAntUsage(u core.Usage) map[string]int {
	input := u.PromptTokens - u.CachedTokens - u.CacheWriteTokens
	if input < 0 {
		input = u.PromptTokens
	}
	out := map[string]int{
		"input_tokens":  input,
		"output_tokens": u.CompletionTokens,
	}
	if u.CachedTokens > 0 {
		out["cache_read_input_tokens"] = u.CachedTokens
	}
	if u.CacheWriteTokens > 0 {
		out["cache_creation_input_tokens"] = u.CacheWriteTokens
	}
	return out
}

// Anthropic thinking helpers.
const (
	// antMinThinkingBudget is the smallest budget Anthropic accepts.
	antMinThinkingBudget = 1024
	// antThinkingAnswerReserve is added above the thinking budget when the
	// client did not pin max_tokens (LiteLLM: budget + 4096).
	antThinkingAnswerReserve = 4096
)

// anthropicEffortBudget maps an OpenAI-style reasoning_effort to Anthropic's
// budget_tokens (LiteLLM's table). Zero means "no mapping" (none/off/auto).
func anthropicEffortBudget(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal", "low":
		return 1024
	case "medium":
		return 2048
	case "high":
		return 4096
	case "xhigh":
		return 8192
	case "max":
		return 16384
	}
	return 0
}

// anthropicOutputEffort maps reasoning_effort to output_config.effort for
// adaptive-thinking models.
func anthropicOutputEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	case "max":
		return "max"
	}
	return ""
}

// anthropicUsesAdaptiveThinking reports whether the model (Claude 4.6+) uses
// adaptive thinking, where a fixed budget is unnecessary.
func anthropicUsesAdaptiveThinking(model string) bool {
	return capability.ResolveProfile("anthropic", model).ThinkingFormat == "claude-adaptive"
}

func mapAntStop(r string) core.FinishReason {
	switch r {
	case "end_turn", "stop_sequence", "pause_turn":
		return core.FinishStop
	case "max_tokens", "model_context_window_exceeded", "compaction":
		return core.FinishLength
	case "tool_use":
		return core.FinishToolCalls
	case "refusal":
		return core.FinishFilter
	default:
		return core.FinishStop
	}
}

func renderAntStop(r core.FinishReason) string {
	switch r {
	case core.FinishLength:
		return "max_tokens"
	case core.FinishToolCalls:
		return "tool_use"
	case core.FinishFilter:
		return "refusal"
	default:
		return "end_turn"
	}
}
