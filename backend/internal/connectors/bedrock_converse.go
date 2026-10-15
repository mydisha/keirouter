package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Converse API rendering and parsing. Converse is Bedrock's model-neutral chat
// shape: system blocks, alternating user/assistant messages of typed content
// blocks, a toolConfig, an inferenceConfig for the common sampling knobs, and
// additionalModelRequestFields for anything model-specific (Claude thinking,
// top_k, Nova/gpt-oss reasoning).

// ---- wire types -------------------------------------------------------------

type converseRequest struct {
	Messages                     []converseMessage   `json:"messages"`
	System                       []converseBlock     `json:"system,omitempty"`
	InferenceConfig              *converseInference  `json:"inferenceConfig,omitempty"`
	ToolConfig                   *converseToolConfig `json:"toolConfig,omitempty"`
	AdditionalModelRequestFields map[string]any      `json:"additionalModelRequestFields,omitempty"`
	GuardrailConfig              *converseGuardrail  `json:"guardrailConfig,omitempty"`
}

type converseMessage struct {
	Role    string          `json:"role"`
	Content []converseBlock `json:"content"`
}

// converseBlock is a ContentBlock / SystemContentBlock union: exactly one
// member is set.
type converseBlock struct {
	Text             string              `json:"text,omitempty"`
	Image            *converseMedia      `json:"image,omitempty"`
	Document         *converseMedia      `json:"document,omitempty"`
	Video            *converseMedia      `json:"video,omitempty"`
	ToolUse          *converseToolUse    `json:"toolUse,omitempty"`
	ToolResult       *converseToolResult `json:"toolResult,omitempty"`
	ReasoningContent *converseReasoning  `json:"reasoningContent,omitempty"`
	CachePoint       *converseCachePoint `json:"cachePoint,omitempty"`
	JSON             json.RawMessage     `json:"json,omitempty"`
}

type converseMedia struct {
	Format string              `json:"format"`
	Name   string              `json:"name,omitempty"`
	Source converseMediaSource `json:"source"`
}

type converseMediaSource struct {
	Bytes      string      `json:"bytes,omitempty"`
	S3Location *converseS3 `json:"s3Location,omitempty"`
}

type converseS3 struct {
	URI string `json:"uri"`
}

type converseToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type converseToolResult struct {
	ToolUseID string          `json:"toolUseId"`
	Content   []converseBlock `json:"content"`
	Status    string          `json:"status,omitempty"`
}

type converseReasoning struct {
	ReasoningText   *converseReasoningText `json:"reasoningText,omitempty"`
	RedactedContent string                 `json:"redactedContent,omitempty"`
}

type converseReasoningText struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

type converseCachePoint struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type converseInference struct {
	MaxTokens     *int     `json:"maxTokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

func (i *converseInference) empty() bool {
	return i.MaxTokens == nil && i.Temperature == nil && i.TopP == nil && len(i.StopSequences) == 0
}

type converseToolConfig struct {
	Tools      []converseTool      `json:"tools"`
	ToolChoice *converseToolChoice `json:"toolChoice,omitempty"`
}

type converseTool struct {
	ToolSpec   *converseToolSpec   `json:"toolSpec,omitempty"`
	CachePoint *converseCachePoint `json:"cachePoint,omitempty"`
}

type converseToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema struct {
		JSON json.RawMessage `json:"json"`
	} `json:"inputSchema"`
}

type converseToolChoice struct {
	Auto *struct{}          `json:"auto,omitempty"`
	Any  *struct{}          `json:"any,omitempty"`
	Tool *converseNamedTool `json:"tool,omitempty"`
}

type converseNamedTool struct {
	Name string `json:"name"`
}

type converseGuardrail struct {
	Identifier string `json:"guardrailIdentifier"`
	Version    string `json:"guardrailVersion"`
}

type converseUsage struct {
	InputTokens           int `json:"inputTokens"`
	OutputTokens          int `json:"outputTokens"`
	TotalTokens           int `json:"totalTokens"`
	CacheReadInputTokens  int `json:"cacheReadInputTokens"`
	CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
	CacheDetails          []struct {
		InputTokens int    `json:"inputTokens"`
		TTL         string `json:"ttl"`
	} `json:"cacheDetails"`
}

// core converts Converse usage: inputTokens excludes cache reads and writes,
// canonical prompt tokens include them.
func (u converseUsage) core() core.Usage {
	prompt := u.InputTokens + u.CacheReadInputTokens + u.CacheWriteInputTokens
	out := core.Usage{
		PromptTokens:     prompt,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      prompt + u.OutputTokens,
		CachedTokens:     u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheWriteInputTokens,
		Source:           core.UsageSourceProvider,
	}
	// The 1h share is only trusted when the per-TTL split accounts for every
	// written token; a partial split would misprice the remainder.
	var short, long int
	for _, d := range u.CacheDetails {
		switch d.TTL {
		case "5m":
			short += d.InputTokens
		case "1h":
			long += d.InputTokens
		}
	}
	if long > 0 && short+long == u.CacheWriteInputTokens {
		out.CacheWrite1hTokens = long
	}
	return out
}

// ---- model families ---------------------------------------------------------

// bedrockFamily captures the per-model-family differences the Converse
// renderer has to respect.
type bedrockFamily struct {
	anthropic bool
	nova      bool
	nova2     bool
	gptOSS    bool
	openaiGPT bool
	deepseek  bool
	r1        bool
	claude3   bool
}

// bedrockBaseModel strips an ARN down to its resource id and lowercases it.
func bedrockBaseModel(id string) string {
	id = strings.ToLower(id)
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	return id
}

func bedrockFamilyOf(modelID string) bedrockFamily {
	b := bedrockBaseModel(modelID)
	f := bedrockFamily{
		anthropic: strings.Contains(b, "anthropic.") || strings.Contains(b, "claude"),
		nova:      strings.Contains(b, "amazon.nova") || strings.HasPrefix(b, "nova-"),
		nova2:     strings.Contains(b, "nova-2-"),
		gptOSS:    strings.Contains(b, "gpt-oss"),
		deepseek:  strings.Contains(b, "deepseek."),
		r1:        strings.Contains(b, "deepseek.r1"),
	}
	f.openaiGPT = strings.Contains(b, "openai.gpt-") && !f.gptOSS
	f.claude3 = f.anthropic && strings.Contains(b, "claude-3")
	return f
}

// cachePoints reports whether the family accepts cachePoint blocks.
func (f bedrockFamily) cachePoints() bool { return f.anthropic || f.nova }

// bedrockClaudeName maps a Bedrock Claude id ("us.anthropic.claude-opus-4-6-v1",
// an inference-profile ARN) to the bare Claude name the Anthropic codec keys
// its per-model rules on ("claude-opus-4-6-v1").
func bedrockClaudeName(modelID string) string {
	b := bedrockBaseModel(modelID)
	if i := strings.Index(b, "anthropic."); i >= 0 {
		b = b[i+len("anthropic."):]
	}
	return b
}

// ---- tool names -------------------------------------------------------------

// bedrockJSONTool is the synthetic tool that carries structured output when
// the client asked for a json_schema response without declaring tools.
const bedrockJSONTool = "json_tool_call"

// bedrockToolNames maps client tool names to Converse-safe names
// ([a-zA-Z0-9_-]{1,64}) and back.
type bedrockToolNames struct {
	toWire   map[string]string
	fromWire map[string]string
	// jsonTool is set when the structured-output tool was injected.
	jsonTool string
}

func newBedrockToolNames() *bedrockToolNames {
	return &bedrockToolNames{toWire: map[string]string{}, fromWire: map[string]string{}}
}

func (n *bedrockToolNames) wire(name string) string {
	if w, ok := n.toWire[name]; ok {
		return w
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	w := b.String()
	if w == "" {
		w = "tool"
	}
	if len(w) > 64 {
		w = w[:64]
	}
	base := w
	for i := 2; ; i++ {
		if prev, taken := n.fromWire[w]; !taken || prev == name {
			break
		}
		suffix := "_" + strconv.Itoa(i)
		w = base
		if len(w)+len(suffix) > 64 {
			w = w[:64-len(suffix)]
		}
		w += suffix
	}
	n.toWire[name] = w
	n.fromWire[w] = name
	return w
}

func (n *bedrockToolNames) original(wire string) string {
	if n != nil {
		if o, ok := n.fromWire[wire]; ok {
			return o
		}
	}
	return wire
}

// ---- request rendering ------------------------------------------------------

type converseRendered struct {
	body  []byte
	names *bedrockToolNames
}

type converseRenderer struct {
	ctx         context.Context
	provider    string
	model       string
	fam         bedrockFamily
	names       *bedrockToolNames
	nativeTools bool
	docs        int
}

// renderConverse builds the Converse body for req against modelID.
func (c *Bedrock) renderConverse(ctx context.Context, req *core.ChatRequest, modelID string, creds core.Credentials) (*converseRendered, error) {
	r := &converseRenderer{ctx: ctx, provider: c.id, model: modelID, fam: bedrockFamilyOf(modelID), names: newBedrockToolNames()}
	var out converseRequest
	inf := &converseInference{MaxTokens: req.MaxTokens, Temperature: req.Temperature, TopP: req.TopP, StopSequences: req.Stop}
	extra := map[string]any{}
	thinking := false

	if r.fam.anthropic {
		p, err := c.anthropicParams(req, modelID)
		if err != nil {
			return nil, err
		}
		inf.Temperature, inf.TopP = p.Temperature, p.TopP
		if len(p.Thinking) > 0 {
			thinking = true
			extra["thinking"] = p.Thinking
			maxTokens := p.MaxTokens
			inf.MaxTokens = &maxTokens
		}
		if len(p.OutputConfig) > 0 {
			extra["output_config"] = p.OutputConfig
		}
		if betas := bedrockAnthropicBetas(req.Metadata.ClientBetas); len(betas) > 0 {
			extra["anthropic_beta"] = betas
		}
	} else {
		applyBedrockReasoning(r.fam, req.Reasoning, extra)
	}
	if topK, ok := bedrockTopK(req); ok {
		if r.fam.nova {
			extra["inferenceConfig"] = map[string]any{"topK": topK}
		} else {
			extra["top_k"] = topK
		}
	}

	out.System = r.system(req)
	toolCfg, err := r.tools(req, thinking)
	if err != nil {
		return nil, err
	}
	out.ToolConfig = toolCfg
	r.nativeTools = toolCfg != nil
	if out.Messages, err = r.messages(req.Messages); err != nil {
		return nil, err
	}
	if !inf.empty() {
		out.InferenceConfig = inf
	}
	if len(extra) > 0 {
		out.AdditionalModelRequestFields = extra
	}
	if gid := extraValue(creds, bedrockFieldGuardrailID); gid != "" {
		out.GuardrailConfig = &converseGuardrail{Identifier: gid, Version: extraOr(creds, bedrockFieldGuardrailVersion, "DRAFT")}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: modelID, Message: err.Error(), Cause: err}
	}
	return &converseRendered{body: body, names: r.names}, nil
}

// bedrockAnthropicParams are the Claude request knobs as the Anthropic codec
// reconciles them (thinking budget vs max tokens, sampling overrides the
// model rejects, adaptive-thinking effort).
type bedrockAnthropicParams struct {
	MaxTokens    int             `json:"max_tokens"`
	Temperature  *float64        `json:"temperature"`
	TopP         *float64        `json:"top_p"`
	Thinking     json.RawMessage `json:"thinking"`
	OutputConfig json.RawMessage `json:"output_config"`
}

// anthropicParams runs the sampling and thinking settings through the
// Anthropic codec so Claude on Bedrock gets exactly the rules Claude on the
// first-party API gets.
func (c *Bedrock) anthropicParams(req *core.ChatRequest, modelID string) (bedrockAnthropicParams, error) {
	probe := &core.ChatRequest{
		Model:       bedrockClaudeName(modelID),
		Messages:    []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "."}}}},
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Reasoning:   req.Reasoning,
	}
	var p bedrockAnthropicParams
	raw, err := c.codec.RenderRequest(probe)
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	if err != nil {
		return p, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: modelID, Message: err.Error(), Cause: err}
	}
	return p, nil
}

// bedrockSafeBetas are the anthropic-beta flags Bedrock accepts; any other
// flag is rejected with a 400, so client betas are filtered by prefix.
var bedrockSafeBetas = []string{
	"context-1m-", "interleaved-thinking-", "output-128k-", "token-efficient-tools-",
	"fine-grained-tool-streaming-", "computer-use-", "context-management-",
}

func bedrockAnthropicBetas(client []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range client {
		b = strings.TrimSpace(b)
		if b == "" || seen[b] {
			continue
		}
		for _, prefix := range bedrockSafeBetas {
			if strings.HasPrefix(b, prefix) {
				seen[b] = true
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// applyBedrockReasoning maps reasoning effort onto the non-Claude families
// that take it through additionalModelRequestFields.
func applyBedrockReasoning(f bedrockFamily, rc *core.ReasoningConfig, extra map[string]any) {
	if rc == nil || f.r1 {
		return // DeepSeek R1 always reasons and rejects any reasoning field.
	}
	effort := strings.ToLower(strings.TrimSpace(rc.Effort))
	switch effort {
	case "", "none", "off", "disabled", "auto", "adaptive":
		return
	case "minimal":
		effort = "low"
	case "xhigh", "max":
		effort = "high"
	}
	switch {
	case f.nova2:
		extra["reasoningConfig"] = map[string]any{"type": "enabled", "maxReasoningEffort": effort}
	case f.gptOSS, f.deepseek:
		extra["reasoning_effort"] = effort
	case f.openaiGPT:
		extra["reasoning"] = map[string]any{"effort": effort}
	}
}

// bedrockTopK reads a top_k passthrough parameter.
func bedrockTopK(req *core.ChatRequest) (int, bool) {
	raw, ok := req.Extra["top_k"]
	if !ok {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil || f <= 0 {
		return 0, false
	}
	return int(f), true
}

// cachePoint renders a cache breakpoint, or nil when the model has no prompt
// caching or the part carries none.
func (r *converseRenderer) cachePoint(cc json.RawMessage) *converseCachePoint {
	if len(cc) == 0 || !r.fam.cachePoints() {
		return nil
	}
	cp := &converseCachePoint{Type: "default"}
	var parsed struct {
		TTL string `json:"ttl"`
	}
	if json.Unmarshal(cc, &parsed) == nil && r.fam.anthropic && !r.fam.claude3 && (parsed.TTL == "5m" || parsed.TTL == "1h") {
		cp.TTL = parsed.TTL
	}
	return cp
}

func (r *converseRenderer) system(req *core.ChatRequest) []converseBlock {
	var blocks []converseBlock
	if strings.TrimSpace(req.System) != "" {
		blocks = append(blocks, converseBlock{Text: req.System})
		if cp := r.cachePoint(req.SystemCacheControl); cp != nil {
			blocks = append(blocks, converseBlock{CachePoint: cp})
		}
	}
	for _, m := range req.Messages {
		if m.Role != core.RoleSystem && m.Role != core.RoleDeveloper {
			continue
		}
		for _, p := range m.Content {
			if p.Type != core.PartText || strings.TrimSpace(p.Text) == "" {
				continue
			}
			blocks = append(blocks, converseBlock{Text: p.Text})
			if cp := r.cachePoint(p.CacheControl); cp != nil {
				blocks = append(blocks, converseBlock{CachePoint: cp})
			}
		}
	}
	return blocks
}

// bedrockToolChoice normalises the canonical tool_choice into a mode
// ("", auto, none, any, tool) and the forced tool name.
func bedrockToolChoice(v any) (string, string) {
	switch t := v.(type) {
	case nil:
		return "", ""
	case string:
		switch t {
		case "required", "any":
			return "any", ""
		case "none", "auto":
			return t, ""
		}
		return "", ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", ""
	}
	var obj struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return "", ""
	}
	if name := bedrockFirstNonEmpty(obj.Function.Name, obj.Name); name != "" {
		return "tool", name
	}
	switch obj.Type {
	case "required", "any":
		return "any", ""
	case "none", "auto":
		return obj.Type, ""
	}
	return "", ""
}

var bedrockEmptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

func bedrockToolSchema(schema json.RawMessage) json.RawMessage {
	s := bytes.TrimSpace(schema)
	if len(s) == 0 || bytes.Equal(s, []byte("null")) || s[0] != '{' {
		return bedrockEmptySchema
	}
	return s
}

// bedrockResponseSchema extracts a json_schema response format's schema.
func bedrockResponseSchema(rf json.RawMessage) json.RawMessage {
	if len(rf) == 0 {
		return nil
	}
	var parsed struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if json.Unmarshal(rf, &parsed) != nil || parsed.Type != "json_schema" {
		return nil
	}
	s := bytes.TrimSpace(parsed.JSONSchema.Schema)
	if len(s) == 0 || s[0] != '{' {
		return nil
	}
	return s
}

// tools renders toolConfig. Provider-native tool definitions have no Converse
// form and are dropped; tool_choice "none" omits tools entirely, since
// Converse has no way to declare tools the model may not call. A json_schema
// response format without client tools becomes a forced synthetic tool.
func (r *converseRenderer) tools(req *core.ChatRequest, thinking bool) (*converseToolConfig, error) {
	mode, forced := bedrockToolChoice(req.ToolChoice)
	if mode == "none" {
		return nil, nil
	}
	var tools []converseTool
	for _, t := range req.Tools {
		if len(t.Raw) > 0 || t.Name == "" {
			continue
		}
		spec := &converseToolSpec{Name: r.names.wire(t.Name), Description: t.Description}
		spec.InputSchema.JSON = bedrockToolSchema(t.Parameters)
		tools = append(tools, converseTool{ToolSpec: spec})
		if cp := r.cachePoint(t.CacheControl); cp != nil {
			tools = append(tools, converseTool{CachePoint: cp})
		}
	}
	if len(tools) == 0 {
		schema := bedrockResponseSchema(req.ResponseFormat)
		if schema == nil {
			return nil, nil
		}
		r.names.jsonTool = bedrockJSONTool
		spec := &converseToolSpec{Name: bedrockJSONTool, Description: "Return the response as JSON that matches this schema."}
		spec.InputSchema.JSON = schema
		tools = append(tools, converseTool{ToolSpec: spec})
		mode, forced = "tool", bedrockJSONTool
	}
	cfg := &converseToolConfig{Tools: tools}
	// Forced tool use is incompatible with extended thinking.
	if thinking && (mode == "any" || mode == "tool") {
		mode = "auto"
	}
	// Auto is the default and is omitted: several families reject any
	// toolChoice at all.
	switch mode {
	case "any":
		cfg.ToolChoice = &converseToolChoice{Any: &struct{}{}}
	case "tool":
		name := forced
		if name != bedrockJSONTool {
			name = r.names.wire(forced)
		}
		cfg.ToolChoice = &converseToolChoice{Tool: &converseNamedTool{Name: name}}
	}
	return cfg, nil
}

// messages renders the conversation: tool results ride in user turns,
// consecutive same-role turns merge, and the sequence starts with a user turn.
func (r *converseRenderer) messages(msgs []core.Message) ([]converseMessage, error) {
	var out []converseMessage
	for _, m := range msgs {
		if m.Role == core.RoleSystem || m.Role == core.RoleDeveloper {
			continue
		}
		role := "user"
		if m.Role == core.RoleAssistant {
			role = "assistant"
		}
		blocks, err := r.blocks(m, role == "assistant")
		if err != nil {
			return nil, err
		}
		if !bedrockHasContent(blocks) {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			continue
		}
		out = append(out, converseMessage{Role: role, Content: blocks})
	}
	if len(out) == 0 {
		return nil, bedrockBadRequest(r.provider, r.model, "request has no messages")
	}
	if out[0].Role != "user" {
		out = append([]converseMessage{{Role: "user", Content: []converseBlock{{Text: "."}}}}, out...)
	}
	// An assistant prefill must not end in whitespace.
	if last := &out[len(out)-1]; last.Role == "assistant" {
		for i := len(last.Content) - 1; i >= 0; i-- {
			if last.Content[i].CachePoint != nil {
				continue
			}
			if last.Content[i].Text != "" {
				last.Content[i].Text = strings.TrimRight(last.Content[i].Text, " \t\r\n")
				if last.Content[i].Text == "" {
					last.Content = append(last.Content[:i], last.Content[i+1:]...)
				}
			}
			break
		}
		if !bedrockHasContent(last.Content) {
			out = out[:len(out)-1]
		}
	}
	return out, nil
}

func bedrockHasContent(blocks []converseBlock) bool {
	for _, b := range blocks {
		if b.CachePoint == nil {
			return true
		}
	}
	return false
}

func (r *converseRenderer) blocks(m core.Message, assistant bool) ([]converseBlock, error) {
	var out []converseBlock
	addCache := func(cc json.RawMessage) {
		if cp := r.cachePoint(cc); cp != nil && len(out) > 0 {
			out = append(out, converseBlock{CachePoint: cp})
		}
	}
	for _, p := range m.Content {
		switch p.Type {
		case core.PartText:
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			out = append(out, converseBlock{Text: p.Text})
			addCache(p.CacheControl)
		case core.PartImage, core.PartDocument:
			fallback := "image"
			if p.Type == core.PartDocument {
				fallback = "document"
			}
			b, err := r.media(p.Media, fallback)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
			addCache(p.CacheControl)
		case core.PartAudio:
			return nil, &core.ProviderError{Kind: core.ErrCapability, Scope: core.FailureScopeModel, Provider: r.provider, Model: r.model,
				Message: "audio input is not supported by the Bedrock Converse API"}
		case core.PartToolCall:
			if p.ToolCall == nil {
				continue
			}
			if !r.nativeTools {
				out = append(out, converseBlock{Text: fmt.Sprintf("[Called tool %s with arguments %s]", p.ToolCall.Name, bedrockArgsText(p.ToolCall.Arguments))})
				continue
			}
			out = append(out, converseBlock{ToolUse: &converseToolUse{
				ToolUseID: bedrockToolUseID(p.ToolCall.ID), Name: r.names.wire(p.ToolCall.Name), Input: bedrockToolInput(p.ToolCall),
			}})
		case core.PartToolResult:
			if p.ToolResult == nil {
				continue
			}
			if !r.nativeTools {
				out = append(out, converseBlock{Text: fmt.Sprintf("[Result of tool call %s]\n%s", p.ToolResult.CallID, p.ToolResult.Content)})
				continue
			}
			b, err := r.toolResult(p.ToolResult)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
			addCache(p.CacheControl)
		case core.PartThinking:
			// Only signed Claude reasoning can be replayed; unsigned text
			// from another model would be rejected.
			if assistant && r.fam.anthropic && p.Signature != "" {
				out = append(out, converseBlock{ReasoningContent: &converseReasoning{
					ReasoningText: &converseReasoningText{Text: p.Text, Signature: p.Signature},
				}})
			}
		case core.PartRedactedThinking:
			if assistant && r.fam.anthropic && p.Text != "" {
				out = append(out, converseBlock{ReasoningContent: &converseReasoning{RedactedContent: p.Text}})
			}
		}
	}
	return out, nil
}

// bedrockToolUseID brings a client tool-call id into Converse's
// [a-zA-Z0-9_-]{1,64} alphabet. The mapping is deterministic, so a call and
// its result still pair up; ids Bedrock issued are already valid.
func bedrockToolUseID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "call"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func bedrockArgsText(args json.RawMessage) string {
	if s := strings.TrimSpace(string(args)); s != "" {
		return s
	}
	return "{}"
}

// bedrockToolInput returns tool-call arguments as the JSON object Converse
// requires.
func bedrockToolInput(tc *core.ToolCall) json.RawMessage {
	raw := bytes.TrimSpace(tc.Arguments)
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	if !tc.Custom && raw[0] == '{' && json.Valid(raw) {
		return raw
	}
	// A JSON string that itself holds an object (double-encoded arguments).
	var s string
	if !tc.Custom && json.Unmarshal(raw, &s) == nil {
		if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") && json.Valid([]byte(t)) {
			return json.RawMessage(t)
		}
	}
	wrapped, _ := json.Marshal(map[string]string{"input": string(raw)})
	return wrapped
}

func (r *converseRenderer) toolResult(res *core.ToolResult) (converseBlock, error) {
	var content []converseBlock
	if strings.TrimSpace(res.Content) != "" {
		content = append(content, converseBlock{Text: res.Content})
	}
	for _, p := range res.Parts {
		switch p.Type {
		case core.PartText:
			if strings.TrimSpace(p.Text) != "" {
				content = append(content, converseBlock{Text: p.Text})
			}
		case core.PartImage, core.PartDocument:
			fallback := "image"
			if p.Type == core.PartDocument {
				fallback = "document"
			}
			b, err := r.media(p.Media, fallback)
			if err != nil {
				return converseBlock{}, err
			}
			content = append(content, b)
		}
	}
	if len(content) == 0 {
		content = []converseBlock{{Text: "(no output)"}}
	}
	tr := &converseToolResult{ToolUseID: bedrockToolUseID(res.CallID), Content: content}
	// The error status is a Claude-only field.
	if res.IsError && r.fam.anthropic {
		tr.Status = "error"
	}
	return converseBlock{ToolResult: tr}, nil
}

// media renders an image, document or video block.
func (r *converseRenderer) media(m *core.MediaPayload, fallback string) (converseBlock, error) {
	bm, err := resolveBedrockMedia(r.ctx, r.provider, r.model, m)
	if err != nil {
		return converseBlock{}, err
	}
	kind := bm.kind(fallback)
	if bm.MIME == "" {
		// Unlabelled inline data: let the magic bytes decide.
		if sniffed := bm.sniffMIME(); sniffed != "" {
			bm.MIME = sniffed
			kind = bm.kind(fallback)
		}
	}
	src := converseMediaSource{Bytes: bm.Data}
	if bm.S3URI != "" {
		src = converseMediaSource{S3Location: &converseS3{URI: bm.S3URI}}
	}
	switch kind {
	case "image":
		if f := bm.format(bedrockImageFormats); f != "" {
			return converseBlock{Image: &converseMedia{Format: f, Source: src}}, nil
		}
		return converseBlock{}, bedrockBadRequest(r.provider, r.model, "unsupported image type "+strconv.Quote(bm.MIME)+" (Bedrock accepts png, jpeg, gif, webp)")
	case "video":
		if f := bm.format(bedrockVideoFormats); f != "" {
			return converseBlock{Video: &converseMedia{Format: f, Source: src}}, nil
		}
		return converseBlock{}, bedrockBadRequest(r.provider, r.model, "unsupported video type "+strconv.Quote(bm.MIME))
	}
	f := bm.format(bedrockDocumentFormats)
	if f == "" {
		return converseBlock{}, bedrockBadRequest(r.provider, r.model, "unsupported document type "+strconv.Quote(bm.MIME))
	}
	r.docs++
	return converseBlock{Document: &converseMedia{Format: f, Name: "document-" + strconv.Itoa(r.docs), Source: src}}, nil
}

// ---- response parsing -------------------------------------------------------

type converseResponse struct {
	Output struct {
		Message *struct {
			Content []converseBlock `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string        `json:"stopReason"`
	Usage      converseUsage `json:"usage"`
}

func parseConverseResponse(body []byte, model string, names *bedrockToolNames) (*core.ChatResponse, error) {
	var raw converseResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("bedrock: parse converse response: %w", err)
	}
	msg := core.Message{Role: core.RoleAssistant}
	sawTool := false
	if raw.Output.Message != nil {
		for _, b := range raw.Output.Message.Content {
			switch {
			case b.ReasoningContent != nil:
				if rt := b.ReasoningContent.ReasoningText; rt != nil {
					msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: rt.Text, Signature: rt.Signature})
				} else if b.ReasoningContent.RedactedContent != "" {
					msg.Content = append(msg.Content, core.ContentPart{Type: core.PartRedactedThinking, Text: b.ReasoningContent.RedactedContent})
				}
			case b.ToolUse != nil:
				input := b.ToolUse.Input
				if len(bytes.TrimSpace(input)) == 0 {
					input = json.RawMessage("{}")
				}
				if names != nil && names.jsonTool != "" && b.ToolUse.Name == names.jsonTool {
					msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: string(input)})
					continue
				}
				sawTool = true
				msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: &core.ToolCall{
					ID: b.ToolUse.ToolUseID, Name: names.original(b.ToolUse.Name), Arguments: input,
				}})
			case b.Text != "":
				msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: b.Text})
			}
		}
	}
	finish := mapConverseStop(raw.StopReason)
	if finish == core.FinishToolCalls && !sawTool {
		finish = core.FinishStop
	}
	return &core.ChatResponse{Model: model, Message: msg, FinishReason: finish, Usage: raw.Usage.core()}, nil
}

func mapConverseStop(reason string) core.FinishReason {
	switch reason {
	case "tool_use":
		return core.FinishToolCalls
	case "max_tokens", "model_context_window_exceeded":
		return core.FinishLength
	case "guardrail_intervened", "content_filtered":
		return core.FinishFilter
	default:
		return core.FinishStop
	}
}

// ---- streaming --------------------------------------------------------------

// converseStreamState maps ConverseStream events to canonical chunks. The
// terminal finish waits for the metadata event so usage precedes it.
type converseStreamState struct {
	names *bedrockToolNames
	// tools maps a content block index to its dense tool-call ordinal; -1
	// marks the structured-output tool, whose input streams as text.
	tools      map[int]int
	nextTool   int
	stopReason string
	stopped    bool
	finished   bool
}

func newConverseStreamState(names *bedrockToolNames) *converseStreamState {
	return &converseStreamState{names: names, tools: map[int]int{}}
}

type converseStreamEvent struct {
	ContentBlockIndex int `json:"contentBlockIndex"`
	Start             *struct {
		ToolUse *struct {
			ToolUseID string `json:"toolUseId"`
			Name      string `json:"name"`
		} `json:"toolUse"`
	} `json:"start"`
	Delta *struct {
		Text    *string `json:"text"`
		ToolUse *struct {
			Input string `json:"input"`
		} `json:"toolUse"`
		ReasoningContent *struct {
			Text            *string `json:"text"`
			Signature       string  `json:"signature"`
			RedactedContent string  `json:"redactedContent"`
		} `json:"reasoningContent"`
	} `json:"delta"`
	StopReason string         `json:"stopReason"`
	Usage      *converseUsage `json:"usage"`
}

func (s *converseStreamState) frame(f *eventStreamFrame) ([]core.StreamChunk, error) {
	eventType := f.headers[":event-type"]
	switch eventType {
	case "contentBlockStart", "contentBlockDelta", "messageStop", "metadata":
	default:
		return nil, nil // messageStart, contentBlockStop
	}
	var ev converseStreamEvent
	if err := json.Unmarshal(f.payload, &ev); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
			Message: "bedrock: malformed " + eventType + " event: " + err.Error(), Cause: err}
	}
	switch eventType {
	case "contentBlockStart":
		if ev.Start == nil || ev.Start.ToolUse == nil {
			return nil, nil
		}
		tu := ev.Start.ToolUse
		if s.names != nil && s.names.jsonTool != "" && tu.Name == s.names.jsonTool {
			s.tools[ev.ContentBlockIndex] = -1
			return nil, nil
		}
		ordinal := s.nextTool
		s.nextTool++
		s.tools[ev.ContentBlockIndex] = ordinal
		return []core.StreamChunk{{Type: core.ChunkToolCall, Index: ordinal, ToolCall: &core.ToolCall{
			ID: tu.ToolUseID, Name: s.names.original(tu.Name),
		}}}, nil

	case "contentBlockDelta":
		d := ev.Delta
		if d == nil {
			return nil, nil
		}
		switch {
		case d.Text != nil:
			if *d.Text == "" {
				return nil, nil
			}
			return []core.StreamChunk{{Type: core.ChunkText, Delta: *d.Text}}, nil
		case d.ToolUse != nil:
			if d.ToolUse.Input == "" {
				return nil, nil
			}
			ordinal, ok := s.tools[ev.ContentBlockIndex]
			if ok && ordinal < 0 {
				return []core.StreamChunk{{Type: core.ChunkText, Delta: d.ToolUse.Input}}, nil
			}
			if !ok {
				// A delta without its start event: allocate the ordinal now.
				ordinal = s.nextTool
				s.nextTool++
				s.tools[ev.ContentBlockIndex] = ordinal
			}
			return []core.StreamChunk{{Type: core.ChunkToolCall, Index: ordinal, ToolCall: &core.ToolCall{
				Arguments: json.RawMessage(d.ToolUse.Input),
			}}}, nil
		case d.ReasoningContent != nil:
			rc := d.ReasoningContent
			switch {
			case rc.RedactedContent != "":
				return []core.StreamChunk{{Type: core.ChunkRedactedThinking, Delta: rc.RedactedContent}}, nil
			case rc.Signature != "":
				return []core.StreamChunk{{Type: core.ChunkThinking, Signature: rc.Signature}}, nil
			case rc.Text != nil && *rc.Text != "":
				return []core.StreamChunk{{Type: core.ChunkThinking, Delta: *rc.Text}}, nil
			}
		}
		return nil, nil

	case "messageStop":
		s.stopped = true
		s.stopReason = ev.StopReason
		return nil, nil

	case "metadata":
		var chunks []core.StreamChunk
		if ev.Usage != nil {
			u := ev.Usage.core()
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkUsage, Usage: &u})
		}
		if s.stopped && !s.finished {
			s.finished = true
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkFinish, FinishReason: s.finishReason()})
		}
		return chunks, nil
	}
	return nil, nil
}

func (s *converseStreamState) finishReason() core.FinishReason {
	r := mapConverseStop(s.stopReason)
	if r == core.FinishToolCalls && s.nextTool == 0 {
		return core.FinishStop // only the structured-output tool was called
	}
	return r
}

func (s *converseStreamState) finish() ([]core.StreamChunk, error) {
	if s.finished {
		return nil, nil
	}
	if !s.stopped {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
			Message: "bedrock stream ended before messageStop"}
	}
	s.finished = true
	return []core.StreamChunk{{Type: core.ChunkFinish, FinishReason: s.finishReason()}}, nil
}
