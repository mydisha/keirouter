package connectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// OCI Generative AI chat wire format. One endpoint (/actions/chat) serves two
// request shapes selected by chatRequest.apiFormat:
//
//   - GENERIC: OpenAI-like role/content messages with TEXT and IMAGE parts,
//     FUNCTION tools and tool calls (Meta, xAI, OpenAI, Google, ... models).
//   - COHERE: Cohere's chat API — the latest user turn travels in "message",
//     earlier turns in "chatHistory", the system prompt in "preambleOverride"
//     and tools declare flat parameterDefinitions instead of JSON Schema.

const (
	ociFormatGeneric = "GENERIC"
	ociFormatCohere  = "COHERE"

	// ociDefaultMaxTokens is sent when the client sets no limit: the service
	// default is tiny and silently truncates answers mid-sentence.
	ociDefaultMaxTokens = 4096
)

// ociAPIFormat picks the request shape for a model name. Cohere models are
// named "cohere.<model>"; everything else uses the generic format.
func ociAPIFormat(model string) string {
	if vendor, _, ok := strings.Cut(strings.ToLower(model), "."); ok && vendor == "cohere" {
		return ociFormatCohere
	}
	return ociFormatGeneric
}

// ociUsesMaxCompletionTokens reports whether a model rejects maxTokens and
// needs maxCompletionTokens instead. OpenAI-hosted models (except the
// gpt-oss open weights, served by OCI's own stack) and Gemini reasoning
// models only accept the latter.
func ociUsesMaxCompletionTokens(model string) bool {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "openai."):
		return !strings.HasPrefix(m, "openai.gpt-oss")
	case strings.HasPrefix(m, "google.gemini-"):
		return !strings.Contains(m, "lite")
	}
	return false
}

// ---- request shapes ---------------------------------------------------------

type ociChatDetails struct {
	CompartmentID string         `json:"compartmentId"`
	ServingMode   ociServingMode `json:"servingMode"`
	ChatRequest   any            `json:"chatRequest"`
}

type ociServingMode struct {
	ServingType string `json:"servingType"`
	ModelID     string `json:"modelId,omitempty"`
	EndpointID  string `json:"endpointId,omitempty"`
}

type ociStreamOptions struct {
	IsIncludeUsage bool `json:"isIncludeUsage"`
}

type ociGenericRequest struct {
	APIFormat           string            `json:"apiFormat"`
	Messages            []ociMessage      `json:"messages"`
	Tools               []ociTool         `json:"tools,omitempty"`
	ToolChoice          map[string]string `json:"toolChoice,omitempty"`
	IsStream            bool              `json:"isStream,omitempty"`
	StreamOptions       *ociStreamOptions `json:"streamOptions,omitempty"`
	MaxTokens           *int              `json:"maxTokens,omitempty"`
	MaxCompletionTokens *int              `json:"maxCompletionTokens,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	TopP                *float64          `json:"topP,omitempty"`
	TopK                *int              `json:"topK,omitempty"`
	Stop                []string          `json:"stop,omitempty"`
	Seed                *int              `json:"seed,omitempty"`
	FrequencyPenalty    *float64          `json:"frequencyPenalty,omitempty"`
	PresencePenalty     *float64          `json:"presencePenalty,omitempty"`
	ReasoningEffort     string            `json:"reasoningEffort,omitempty"`
	ResponseFormat      map[string]any    `json:"responseFormat,omitempty"`
}

type ociMessage struct {
	Role       string           `json:"role"`
	Content    []ociContentPart `json:"content,omitempty"`
	ToolCalls  []ociToolCall    `json:"toolCalls,omitempty"`
	ToolCallID string           `json:"toolCallId,omitempty"`
}

type ociContentPart struct {
	Type     string       `json:"type"`
	Text     *string      `json:"text,omitempty"`
	ImageURL *ociImageURL `json:"imageUrl,omitempty"`
}

type ociImageURL struct {
	URL string `json:"url"`
}

type ociToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ociTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ociCohereRequest struct {
	APIFormat        string             `json:"apiFormat"`
	Message          string             `json:"message"`
	ChatHistory      []ociCohereMessage `json:"chatHistory,omitempty"`
	PreambleOverride string             `json:"preambleOverride,omitempty"`
	Tools            []ociCohereTool    `json:"tools,omitempty"`
	IsStream         bool               `json:"isStream,omitempty"`
	StreamOptions    *ociStreamOptions  `json:"streamOptions,omitempty"`
	MaxTokens        *int               `json:"maxTokens,omitempty"`
	Temperature      *float64           `json:"temperature,omitempty"`
	TopP             *float64           `json:"topP,omitempty"`
	TopK             *int               `json:"topK,omitempty"`
	StopSequences    []string           `json:"stopSequences,omitempty"`
	Seed             *int               `json:"seed,omitempty"`
	FrequencyPenalty *float64           `json:"frequencyPenalty,omitempty"`
	PresencePenalty  *float64           `json:"presencePenalty,omitempty"`
	ResponseFormat   map[string]any     `json:"responseFormat,omitempty"`
}

type ociCohereMessage struct {
	Role        string              `json:"role"`
	Message     *string             `json:"message,omitempty"`
	ToolCalls   []ociCohereToolCall `json:"toolCalls,omitempty"`
	ToolResults []ociCohereResult   `json:"toolResults,omitempty"`
}

type ociCohereToolCall struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
}

type ociCohereResult struct {
	Call    ociCohereToolCall `json:"call"`
	Outputs []map[string]any  `json:"outputs"`
}

type ociCohereTool struct {
	Name                 string                       `json:"name"`
	Description          string                       `json:"description"`
	ParameterDefinitions map[string]ociCohereParamDef `json:"parameterDefinitions"`
}

type ociCohereParamDef struct {
	Description string `json:"description"`
	Type        string `json:"type"`
	IsRequired  bool   `json:"isRequired"`
}

// ---- request rendering ------------------------------------------------------

// renderOCIChat builds the /actions/chat body for a canonical request.
func renderOCIChat(req *core.ChatRequest, compartment string, serving ociServingMode, format string, stream bool) ([]byte, error) {
	var chat any
	if format == ociFormatCohere {
		cr, err := ociCohereChatRequest(req, stream)
		if err != nil {
			return nil, err
		}
		chat = cr
	} else {
		chat = ociGenericChatRequest(req, stream)
	}
	return json.Marshal(ociChatDetails{CompartmentID: compartment, ServingMode: serving, ChatRequest: chat})
}

func ociGenericChatRequest(req *core.ChatRequest, stream bool) *ociGenericRequest {
	out := &ociGenericRequest{
		APIFormat:        ociFormatGeneric,
		Messages:         ociGenericMessages(req),
		IsStream:         stream,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		TopK:             ociExtraInt(req, "top_k"),
		Stop:             req.Stop,
		Seed:             ociExtraInt(req, "seed"),
		FrequencyPenalty: ociExtraFloat(req, "frequency_penalty"),
		PresencePenalty:  ociExtraFloat(req, "presence_penalty"),
		ResponseFormat:   ociResponseFormat(req.ResponseFormat, ociFormatGeneric),
	}
	if stream {
		out.StreamOptions = &ociStreamOptions{IsIncludeUsage: true}
	}
	maxTokens := ociDefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	if ociUsesMaxCompletionTokens(req.Model) {
		out.MaxCompletionTokens = &maxTokens
	} else {
		out.MaxTokens = &maxTokens
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		effort := strings.ToUpper(req.Reasoning.Effort)
		if effort == "DISABLE" || effort == "DISABLED" {
			effort = "NONE"
		}
		out.ReasoningEffort = effort
	}
	for _, t := range req.Tools {
		if t.Name == "" || len(t.Raw) > 0 && len(t.Parameters) == 0 {
			continue // provider-native server tools have no function equivalent
		}
		out.Tools = append(out.Tools, ociTool{Type: "FUNCTION", Name: t.Name, Description: t.Description,
			Parameters: ociCleanSchema(t.Parameters)})
	}
	if len(out.Tools) > 0 {
		out.ToolChoice = ociToolChoice(req.ToolChoice)
	}
	return out
}

// ociToolChoice maps the canonical (OpenAI-form) tool_choice to OCI's flat
// {"type": AUTO|NONE|REQUIRED} or {"type": "FUNCTION", "name": ...} form.
func ociToolChoice(v any) map[string]string {
	switch t := v.(type) {
	case string:
		switch strings.ToLower(t) {
		case "auto":
			return map[string]string{"type": "AUTO"}
		case "none":
			return map[string]string{"type": "NONE"}
		case "required", "any":
			return map[string]string{"type": "REQUIRED"}
		}
	case map[string]any:
		if fn, ok := t["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return map[string]string{"type": "FUNCTION", "name": name}
			}
		}
		typ, _ := t["type"].(string)
		switch strings.ToUpper(typ) {
		case "AUTO", "NONE", "REQUIRED":
			return map[string]string{"type": strings.ToUpper(typ)}
		}
	}
	return nil
}

func ociText(s string) *string { return &s }

// ociGenericMessages converts the canonical conversation to GENERIC messages.
// Tool results become TOOL messages keyed by toolCallId; thinking, audio and
// document parts have no GENERIC equivalent and are dropped.
func ociGenericMessages(req *core.ChatRequest) []ociMessage {
	var out []ociMessage
	if sys := strings.TrimSpace(req.System); sys != "" {
		out = append(out, ociMessage{Role: "SYSTEM", Content: []ociContentPart{{Type: "TEXT", Text: ociText(req.System)}}})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case core.RoleSystem, core.RoleDeveloper:
			if txt := m.TextContent(); txt != "" {
				out = append(out, ociMessage{Role: "SYSTEM", Content: []ociContentPart{{Type: "TEXT", Text: ociText(txt)}}})
			}
		case core.RoleAssistant:
			msg := ociMessage{Role: "ASSISTANT"}
			if txt := m.TextContent(); txt != "" {
				msg.Content = []ociContentPart{{Type: "TEXT", Text: ociText(txt)}}
			}
			for _, p := range m.Content {
				if p.Type == core.PartToolCall && p.ToolCall != nil {
					msg.ToolCalls = append(msg.ToolCalls, ociToolCall{ID: p.ToolCall.ID, Type: "FUNCTION",
						Name: p.ToolCall.Name, Arguments: ociArgs(p.ToolCall.Arguments)})
				}
			}
			if len(msg.Content) > 0 || len(msg.ToolCalls) > 0 {
				out = append(out, msg)
			}
		default: // user and tool turns
			var parts []ociContentPart
			for _, p := range m.Content {
				switch p.Type {
				case core.PartToolResult:
					if p.ToolResult != nil {
						out = append(out, ociMessage{Role: "TOOL", ToolCallID: p.ToolResult.CallID,
							Content: []ociContentPart{{Type: "TEXT", Text: ociText(p.ToolResult.Content)}}})
					}
				case core.PartText:
					if p.Text != "" {
						parts = append(parts, ociContentPart{Type: "TEXT", Text: ociText(p.Text)})
					}
				case core.PartImage:
					if u := ociImageRef(p.Media); u != "" {
						parts = append(parts, ociContentPart{Type: "IMAGE", ImageURL: &ociImageURL{URL: u}})
					}
				}
			}
			if len(parts) == 0 {
				continue
			}
			role := "USER"
			if m.Role == core.RoleTool {
				role = "TOOL"
			}
			out = append(out, ociMessage{Role: role, Content: parts})
		}
	}
	return out
}

// ociImageRef renders an image as a URL or base64 data URI.
func ociImageRef(m *core.MediaPayload) string {
	if m == nil {
		return ""
	}
	if m.URL != "" {
		return m.URL
	}
	if m.Data == "" {
		return ""
	}
	mime := m.MIMEType
	if mime == "" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + m.Data
}

func ociArgs(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "{}"
	}
	return string(raw)
}

// ociFlatTurn is a normalized conversation turn used to build the Cohere
// message/chatHistory split.
type ociFlatTurn struct {
	role    core.Role // user, assistant or tool
	text    string
	calls   []*core.ToolCall
	results []*core.ToolResult
}

func ociCohereChatRequest(req *core.ChatRequest, stream bool) (*ociCohereRequest, error) {
	var preamble []string
	if s := strings.TrimSpace(req.System); s != "" {
		preamble = append(preamble, req.System)
	}
	var turns []ociFlatTurn
	for _, m := range req.Messages {
		switch m.Role {
		case core.RoleSystem, core.RoleDeveloper:
			if txt := m.TextContent(); txt != "" {
				preamble = append(preamble, txt)
			}
		case core.RoleAssistant:
			t := ociFlatTurn{role: core.RoleAssistant, text: m.TextContent()}
			for _, p := range m.Content {
				if p.Type == core.PartToolCall && p.ToolCall != nil {
					t.calls = append(t.calls, p.ToolCall)
				}
			}
			turns = append(turns, t)
		default:
			var results []*core.ToolResult
			for _, p := range m.Content {
				if p.Type == core.PartToolResult && p.ToolResult != nil {
					results = append(results, p.ToolResult)
				}
			}
			if len(results) > 0 {
				turns = append(turns, ociFlatTurn{role: core.RoleTool, results: results})
			}
			if txt := m.TextContent(); txt != "" {
				role := core.RoleUser
				if m.Role == core.RoleTool {
					role = core.RoleTool
					txt = ""
				}
				if txt != "" {
					turns = append(turns, ociFlatTurn{role: role, text: txt})
				}
			}
		}
	}

	last := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].role == core.RoleUser {
			last = i
			break
		}
	}
	if last < 0 {
		return nil, &core.ProviderError{Kind: core.ErrBadRequest, Scope: core.FailureScopeRequest,
			Message: "Cohere models on OCI require at least one user message"}
	}

	// Resolve tool results to the call that produced them: the Cohere API
	// identifies a call by name + parameters, not by id.
	callByID := map[string]ociCohereToolCall{}
	for _, t := range turns {
		for _, c := range t.calls {
			callByID[c.ID] = ociCohereToolCall{Name: c.Name, Parameters: ociCohereParams(c.Arguments)}
		}
	}

	var history []ociCohereMessage
	for i, t := range turns {
		if i == last {
			continue
		}
		switch t.role {
		case core.RoleUser:
			history = append(history, ociCohereMessage{Role: "USER", Message: ociText(t.text)})
		case core.RoleAssistant:
			msg := ociCohereMessage{Role: "CHATBOT", Message: ociText(t.text)}
			for _, c := range t.calls {
				msg.ToolCalls = append(msg.ToolCalls, ociCohereToolCall{Name: c.Name, Parameters: ociCohereParams(c.Arguments)})
			}
			history = append(history, msg)
		case core.RoleTool:
			var results []ociCohereResult
			for _, r := range t.results {
				call, ok := callByID[r.CallID]
				if !ok {
					call = ociCohereToolCall{Parameters: json.RawMessage("{}")}
				}
				results = append(results, ociCohereResult{Call: call, Outputs: []map[string]any{{"output": r.Content}}})
			}
			// Parallel results of one assistant turn share one TOOL entry.
			if n := len(history); n > 0 && history[n-1].Role == "TOOL" {
				history[n-1].ToolResults = append(history[n-1].ToolResults, results...)
			} else if len(results) > 0 {
				history = append(history, ociCohereMessage{Role: "TOOL", ToolResults: results})
			}
		}
	}

	maxTokens := ociDefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	out := &ociCohereRequest{
		APIFormat:        ociFormatCohere,
		Message:          turns[last].text,
		ChatHistory:      history,
		PreambleOverride: strings.Join(preamble, "\n"),
		IsStream:         stream,
		MaxTokens:        &maxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		TopK:             ociExtraInt(req, "top_k"),
		StopSequences:    req.Stop,
		Seed:             ociExtraInt(req, "seed"),
		FrequencyPenalty: ociExtraFloat(req, "frequency_penalty"),
		PresencePenalty:  ociExtraFloat(req, "presence_penalty"),
		ResponseFormat:   ociResponseFormat(req.ResponseFormat, ociFormatCohere),
	}
	if stream {
		out.StreamOptions = &ociStreamOptions{IsIncludeUsage: true}
	}
	// The Cohere chat endpoint has no tool_choice; "none" is honoured by not
	// advertising the tools at all.
	if tc, _ := req.ToolChoice.(string); tc != "none" {
		for _, t := range req.Tools {
			if t.Name == "" || len(t.Raw) > 0 && len(t.Parameters) == 0 {
				continue
			}
			out.Tools = append(out.Tools, ociCohereToolDef(t))
		}
	}
	return out, nil
}

// ociCohereParams returns tool-call arguments as a JSON object, falling back
// to {} when the model produced something that is not an object.
func ociCohereParams(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

// ociPythonTypes maps JSON Schema types to the Python type names the Cohere
// parameterDefinitions expect.
var ociPythonTypes = map[string]string{
	"string": "str", "number": "float", "boolean": "bool", "integer": "int",
	"array": "List", "object": "Dict", "any": "any",
}

func ociCohereToolDef(t core.Tool) ociCohereTool {
	out := ociCohereTool{Name: t.Name, Description: t.Description, ParameterDefinitions: map[string]ociCohereParamDef{}}
	schema, _ := ociCleanSchema(t.Parameters).(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	required := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	for name, raw := range props {
		p, _ := raw.(map[string]any)
		typ, _ := p["type"].(string)
		if typ == "" {
			typ = "string"
		}
		if py, ok := ociPythonTypes[typ]; ok {
			typ = py
		}
		desc, _ := p["description"].(string)
		out.ParameterDefinitions[name] = ociCohereParamDef{Description: ociEnrichDescription(desc, p), Type: typ, IsRequired: required[name]}
	}
	return out
}

// ociEnrichDescription folds schema constraints the flat Cohere parameter
// definition cannot express (enum, format, range, pattern) into the
// description so the model still sees them.
func ociEnrichDescription(desc string, p map[string]any) string {
	var parts []string
	if desc != "" {
		parts = append(parts, desc)
	}
	if v, ok := p["enum"]; ok {
		b, _ := json.Marshal(v)
		parts = append(parts, "Allowed values: "+string(b))
	}
	if v, ok := p["format"]; ok {
		parts = append(parts, fmt.Sprintf("Format: %v", v))
	}
	var rng []string
	if v, ok := p["minimum"]; ok {
		rng = append(rng, fmt.Sprintf("min=%v", v))
	}
	if v, ok := p["maximum"]; ok {
		rng = append(rng, fmt.Sprintf("max=%v", v))
	}
	if len(rng) > 0 {
		parts = append(parts, "Range: "+strings.Join(rng, ", "))
	}
	if v, ok := p["pattern"]; ok {
		parts = append(parts, fmt.Sprintf("Pattern: %v", v))
	}
	return strings.Join(parts, ". ")
}

// ociResponseFormat converts an OpenAI response_format to OCI's shape. The
// Cohere format has no JSON_SCHEMA type, so a schema rides on JSON_OBJECT.
func ociResponseFormat(raw json.RawMessage, format string) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var rf struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Schema      json.RawMessage `json:"schema"`
			Strict      *bool           `json:"strict"`
		} `json:"json_schema"`
	}
	if json.Unmarshal(raw, &rf) != nil || rf.Type == "" {
		return nil
	}
	typ := strings.ToLower(rf.Type)
	switch {
	case typ == "text":
		return map[string]any{"type": "TEXT"}
	case format == ociFormatCohere:
		out := map[string]any{"type": "JSON_OBJECT"}
		if rf.JSONSchema != nil && len(rf.JSONSchema.Schema) > 0 {
			out["schema"] = rf.JSONSchema.Schema
		}
		return out
	case typ == "json_schema":
		if rf.JSONSchema == nil {
			return map[string]any{"type": "JSON_OBJECT"}
		}
		js := map[string]any{"name": rf.JSONSchema.Name}
		if rf.JSONSchema.Name == "" {
			js["name"] = "response"
		}
		if rf.JSONSchema.Description != "" {
			js["description"] = rf.JSONSchema.Description
		}
		if len(rf.JSONSchema.Schema) > 0 {
			js["schema"] = rf.JSONSchema.Schema
		}
		if rf.JSONSchema.Strict != nil {
			js["isStrict"] = *rf.JSONSchema.Strict
		}
		return map[string]any{"type": "JSON_SCHEMA", "jsonSchema": js}
	case typ == "json_object", typ == "json":
		return map[string]any{"type": "JSON_OBJECT"}
	}
	return nil
}

func ociExtraFloat(req *core.ChatRequest, key string) *float64 {
	raw, ok := req.Extra[key]
	if !ok {
		return nil
	}
	var v *float64
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func ociExtraInt(req *core.ChatRequest, key string) *int {
	f := ociExtraFloat(req, key)
	if f == nil {
		return nil
	}
	v := int(*f)
	return &v
}

// ---- tool schema cleanup ----------------------------------------------------

// ociCleanSchema rewrites a JSON Schema into the subset OCI accepts: $ref /
// $defs are inlined, Optional-style anyOf collapses to its non-null branch,
// titles and null defaults are stripped, type lists are narrowed and arrays
// always carry items.
func ociCleanSchema(raw json.RawMessage) any {
	var v any
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &v) != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if _, ok := v.(map[string]any); !ok {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return ociSanitizeSchema(ociResolveAnyOf(ociResolveRefs(v)))
}

func ociResolveRefs(schema any) any {
	root, _ := schema.(map[string]any)
	defs := map[string]any{}
	for _, key := range []string{"$defs", "definitions"} {
		if d, ok := root[key].(map[string]any); ok {
			for k, v := range d {
				defs[k] = v
			}
		}
	}
	stack := map[string]bool{}
	var resolve func(v any) any
	resolve = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			if ref, ok := t["$ref"].(string); ok {
				name := ""
				for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
					if strings.HasPrefix(ref, prefix) {
						name = strings.TrimPrefix(ref, prefix)
					}
				}
				def, found := defs[name]
				if name == "" || !found {
					return t
				}
				if stack[name] {
					return map[string]any{"type": "object"} // break cycles
				}
				stack[name] = true
				out := resolve(def)
				delete(stack, name)
				return out
			}
			out := make(map[string]any, len(t))
			for k, val := range t {
				if k == "$defs" || k == "definitions" {
					continue
				}
				out[k] = resolve(val)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, val := range t {
				out[i] = resolve(val)
			}
			return out
		}
		return v
	}
	return resolve(schema)
}

func ociResolveAnyOf(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if branches, ok := t["anyOf"].([]any); ok {
			if _, typed := t["type"]; !typed {
				for _, b := range branches {
					bm, isMap := b.(map[string]any)
					if isMap && bm["type"] == "null" {
						continue
					}
					merged := make(map[string]any, len(t)+len(bm))
					for k, val := range t {
						if k != "anyOf" {
							merged[k] = val
						}
					}
					for k, val := range bm {
						merged[k] = val
					}
					return ociResolveAnyOf(merged)
				}
			}
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = ociResolveAnyOf(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = ociResolveAnyOf(val)
		}
		return out
	}
	return v
}

func ociSanitizeSchema(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = ociSanitizeSchema(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			switch {
			case k == "title":
				continue
			case k == "default" && val == nil:
				continue
			case k == "type":
				if s, ok := val.(string); ok && s == "any" {
					out[k] = "object"
					continue
				}
				if list, ok := val.([]any); ok {
					out[k] = "string"
					for _, item := range list {
						if s, ok := item.(string); ok && s != "null" {
							out[k] = s
							break
						}
					}
					continue
				}
			}
			out[k] = ociSanitizeSchema(val)
		}
		if out["type"] == "array" {
			if _, ok := out["items"]; !ok {
				out["items"] = map[string]any{"type": "object"}
			}
		}
		if req, ok := out["required"]; ok {
			props, _ := out["properties"].(map[string]any)
			list, isList := req.([]any)
			kept := []any{}
			if isList && props != nil {
				for _, r := range list {
					if s, ok := r.(string); ok {
						if _, exists := props[s]; exists {
							kept = append(kept, s)
						}
					}
				}
			}
			out["required"] = kept
		}
		return out
	}
	return v
}
