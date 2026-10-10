package transform

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/core"
)

// GeminiCodec handles Google's Gemini generateContent wire format. Gemini
// groups turns under "contents" with role "user"/"model", carries system text
// in a separate "systemInstruction", and nests tool calls as functionCall /
// functionResponse parts.
type GeminiCodec struct{}

func (GeminiCodec) Dialect() core.Dialect { return core.DialectGemini }

// ---- wire types -------------------------------------------------------------

type gemRequest struct {
	Contents          []gemContent    `json:"contents"`
	SystemInstruction *gemContent     `json:"systemInstruction,omitempty"`
	Tools             []gemTool       `json:"tools,omitempty"`
	ToolConfig        json.RawMessage `json:"toolConfig,omitempty"`
	GenerationConfig  *gemGenConfig   `json:"generationConfig,omitempty"`
}

type gemGenConfig struct {
	Temperature        *float64           `json:"temperature,omitempty"`
	TopP               *float64           `json:"topP,omitempty"`
	MaxOutputTokens    *int               `json:"maxOutputTokens,omitempty"`
	StopSequences      []string           `json:"stopSequences,omitempty"`
	ThinkingConfig     *gemThinkingConfig `json:"thinkingConfig,omitempty"`
	ResponseMimeType   string             `json:"responseMimeType,omitempty"`
	ResponseJSONSchema json.RawMessage    `json:"responseJsonSchema,omitempty"`
}

// gemThinkingConfig controls Gemini reasoning. Gemini 2.x takes a token
// budget; Gemini 3 takes a level. Setting both is a 400.
type gemThinkingConfig struct {
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []gemPart `json:"parts"`
}

type gemPart struct {
	Text             string             `json:"text,omitempty"`
	FunctionCall     *gemFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *gemFunctionResult `json:"functionResponse,omitempty"`
	InlineData       *gemInlineData     `json:"inlineData,omitempty"`
	FileData         *gemFileData       `json:"fileData,omitempty"`
	ThoughtSignature string             `json:"thoughtSignature,omitempty"`
	Thought          bool               `json:"thought,omitempty"`
}

type gemInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemFileData struct {
	MIMEType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

type gemFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type gemFunctionResult struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
	// Parts carries binary results (screenshots, documents) returned by a
	// tool; Gemini accepts inlineData parts inside functionResponse.
	Parts []gemPart `json:"parts,omitempty"`
}

type gemTool struct {
	FunctionDeclarations []gemFuncDecl `json:"functionDeclarations"`
}

type gemFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ---- request parsing --------------------------------------------------------

func (GeminiCodec) ParseRequest(body []byte) (*core.ChatRequest, error) {
	var raw gemRequest
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("gemini: parse request: %w", err)
	}

	req := &core.ChatRequest{}
	if raw.SystemInstruction != nil {
		for _, p := range raw.SystemInstruction.Parts {
			req.System += p.Text
		}
	}
	if raw.GenerationConfig != nil {
		req.Temperature = raw.GenerationConfig.Temperature
		req.TopP = raw.GenerationConfig.TopP
		req.MaxTokens = raw.GenerationConfig.MaxOutputTokens
		req.Stop = raw.GenerationConfig.StopSequences
	}
	for _, t := range raw.Tools {
		for _, fd := range t.FunctionDeclarations {
			req.Tools = append(req.Tools, core.Tool{Name: fd.Name, Description: fd.Description, Parameters: fd.Parameters})
		}
	}
	for _, c := range raw.Contents {
		req.Messages = append(req.Messages, parseGemContent(c))
	}
	return req, nil
}

func parseGemContent(c gemContent) core.Message {
	msg := core.Message{Role: mapGemRole(c.Role)}
	for _, p := range c.Parts {
		switch {
		case p.FunctionCall != nil:
			// Preserve Gemini's native call id and thought signature when present;
			// otherwise derive a stable id from the function name so downstream
			// dialects can pair the matching result.
			msg.Content = append(msg.Content, core.ContentPart{
				Type:     core.PartToolCall,
				ToolCall: &core.ToolCall{ID: geminiEncodeCallID(p.FunctionCall, p.ThoughtSignature), Name: p.FunctionCall.Name, Arguments: p.FunctionCall.Args},
			})
		case p.FunctionResponse != nil:
			// Pair the result back to the native or name-derived call id.
			msg.Content = append(msg.Content, core.ContentPart{
				Type: core.PartToolResult,
				ToolResult: &core.ToolResult{
					CallID:  geminiEncodeResultID(p.FunctionResponse),
					Content: string(p.FunctionResponse.Response),
				},
			})
		case p.InlineData != nil:
			msg.Content = append(msg.Content, core.ContentPart{
				Type:  core.PartImage,
				Media: &core.MediaPayload{MIMEType: p.InlineData.MIMEType, Data: p.InlineData.Data},
			})
		case p.FileData != nil:
			msg.Content = append(msg.Content, core.ContentPart{
				Type:  core.PartImage,
				Media: &core.MediaPayload{MIMEType: p.FileData.MIMEType, URL: p.FileData.FileURI},
			})
		case p.Text != "":
			if p.Thought {
				msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: p.Text})
			} else {
				msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: p.Text})
			}
		}
	}
	return msg
}

func mapGemRole(role string) core.Role {
	if role == "model" {
		return core.RoleAssistant
	}
	return core.RoleUser
}

// ---- request rendering ------------------------------------------------------

func (GeminiCodec) RenderRequest(req *core.ChatRequest) ([]byte, error) {
	out := gemRequest{}
	if req.System != "" {
		out.SystemInstruction = &gemContent{Parts: []gemPart{{Text: req.System}}}
	}
	if req.Temperature != nil || req.TopP != nil || req.MaxTokens != nil || len(req.Stop) > 0 {
		out.GenerationConfig = &gemGenConfig{
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxOutputTokens: req.MaxTokens,
			StopSequences:   req.Stop,
		}
	}
	if tc := geminiThinkingConfig(req); tc != nil {
		if out.GenerationConfig == nil {
			out.GenerationConfig = &gemGenConfig{}
		}
		out.GenerationConfig.ThinkingConfig = tc
	}
	if mime, schema := geminiResponseFormat(req.ResponseFormat); mime != "" {
		if out.GenerationConfig == nil {
			out.GenerationConfig = &gemGenConfig{}
		}
		out.GenerationConfig.ResponseMimeType = mime
		out.GenerationConfig.ResponseJSONSchema = schema
	}
	// Resolve a sanitized, collision-free name per declared tool. The same
	// mapping is reused when rendering functionCall parts so an assistant tool
	// call always references the exact name its declaration was sent under, and
	// when rendering functionResponse parts so the result name matches the call
	// (Gemini rejects a functionResponse whose name has no matching call).
	callIDToName := buildGeminiCallNameMap(req.Messages)
	if len(req.Tools) > 0 {
		var decls []gemFuncDecl
		for _, t := range req.Tools {
			decls = append(decls, gemFuncDecl{
				Name:        sanitizeGeminiName(t.Name),
				Description: t.Description,
				Parameters:  cleanGeminiToolSchema(t.Parameters),
			})
		}
		out.Tools = []gemTool{{FunctionDeclarations: decls}}

		// Render tool_choice as a functionCallingConfig only when tools are
		// declared; an allowed-name mode with no tools is rejected.
		if tc := openAIToolChoiceToGemini(req.ToolChoice, sanitizeGeminiName); tc != nil {
			if raw, err := json.Marshal(tc); err == nil {
				out.ToolConfig = raw
			}
		}
	}
	for _, m := range req.Messages {
		c := renderGemContent(m, callIDToName)
		// Gemini requires strictly alternating user/model turns, and the
		// function responses for one batch of parallel calls must sit in one
		// user content ("number of function response parts must equal number
		// of function call parts"). Merge adjacent same-role contents.
		if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == c.Role {
			out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, c.Parts...)
			continue
		}
		out.Contents = append(out.Contents, c)
	}
	for i := range out.Contents {
		out.Contents[i].Parts = dropEmptyGemParts(out.Contents[i].Parts)
	}
	return json.Marshal(out)
}

// dropEmptyGemParts removes placeholder parts once a content has real parts;
// a lone empty content keeps a single-space text so it still serializes to a
// valid part ({} is rejected with "oneof data must be set").
func dropEmptyGemParts(parts []gemPart) []gemPart {
	out := parts[:0]
	for _, p := range parts {
		if p.Text == "" && p.FunctionCall == nil && p.FunctionResponse == nil && p.InlineData == nil && p.FileData == nil {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		out = append(out, gemPart{Text: " "})
	}
	return out
}

// geminiThinkingConfig maps the canonical reasoning config to Gemini's
// thinkingConfig, choosing budget (2.x) or level (3+) by model profile.
func geminiThinkingConfig(req *core.ChatRequest) *gemThinkingConfig {
	if req.Reasoning == nil {
		return nil
	}
	effort := strings.ToLower(strings.TrimSpace(req.Reasoning.Effort))
	format := capability.ResolveProfile("gemini", req.Model).ThinkingFormat
	useLevel := format == "gemini-level"
	switch effort {
	case "none", "off", "disabled":
		if useLevel {
			return &gemThinkingConfig{ThinkingLevel: "low"}
		}
		zero := 0
		return &gemThinkingConfig{ThinkingBudget: &zero}
	}
	if req.Reasoning.MaxTokens > 0 && !useLevel {
		budget := req.Reasoning.MaxTokens
		return &gemThinkingConfig{ThinkingBudget: &budget, IncludeThoughts: true}
	}
	if useLevel {
		level := "high"
		switch effort {
		case "minimal", "low":
			level = "low"
		case "medium":
			level = "medium"
		case "", "auto", "adaptive":
			// Leave the model default; only ask for the thoughts.
			return &gemThinkingConfig{IncludeThoughts: true}
		}
		return &gemThinkingConfig{ThinkingLevel: level, IncludeThoughts: true}
	}
	var budget int
	switch effort {
	case "minimal":
		budget = 128
	case "low":
		budget = 1024
	case "medium":
		budget = 2048
	case "high", "xhigh", "max":
		budget = 4096
	default:
		// Dynamic budget: let Gemini decide, but surface the thoughts.
		budget = -1
	}
	return &gemThinkingConfig{ThinkingBudget: &budget, IncludeThoughts: true}
}

// geminiResponseFormat maps OpenAI response_format to Gemini's structured
// output controls: json_object → application/json, json_schema → the schema
// itself (responseJsonSchema accepts standard JSON Schema on Gemini 2+).
func geminiResponseFormat(raw json.RawMessage) (string, json.RawMessage) {
	if len(raw) == 0 {
		return "", nil
	}
	var rf struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if json.Unmarshal(raw, &rf) != nil {
		return "", nil
	}
	switch rf.Type {
	case "json_object":
		return "application/json", nil
	case "json_schema":
		if rf.JSONSchema != nil && len(rf.JSONSchema.Schema) > 0 {
			return "application/json", rf.JSONSchema.Schema
		}
		return "application/json", nil
	}
	return "", nil
}

// buildGeminiCallNameMap maps each tool-call id to its sanitized function name,
// gathered from every assistant tool call in the conversation. functionResponse
// parts use it to recover the name Gemini requires from the tool result's id.
func buildGeminiCallNameMap(messages []core.Message) map[string]string {
	m := map[string]string{}
	for _, msg := range messages {
		for _, p := range msg.Content {
			if p.Type == core.PartToolCall && p.ToolCall != nil && p.ToolCall.ID != "" {
				m[p.ToolCall.ID] = sanitizeGeminiName(p.ToolCall.Name)
			}
		}
	}
	return m
}

// geminiEncodeCallID derives a tool-call id, preserving id and thoughtSignature if present.
//
// Gemini 2.x sends no id. Deriving it from the function name alone made two
// parallel read_file calls share one id, so clients (and Anthropic upstreams)
// saw a duplicate tool_use id and the second call overwrote the first. A
// synthetic nonce makes each call unique; renderGemContent strips it again so
// Gemini never sees an id it did not issue.
func geminiEncodeCallID(call *gemFunctionCall, thoughtSig string) string {
	id := call.ID
	synthetic := false
	if id == "" {
		id = call.Name
		synthetic = true
	}
	if id == "" {
		id = "unknown"
	}
	encoded := id
	encoded = strings.ReplaceAll(encoded, ":", "_")
	if !strings.HasPrefix(encoded, "call_") {
		encoded = "call_" + encoded
	}
	if synthetic {
		encoded += geminiSyntheticIDSep + geminiNonce()
	}
	if thoughtSig != "" && !strings.Contains(encoded, "__sig__") {
		encoded += "__sig__" + base64.RawURLEncoding.EncodeToString([]byte(thoughtSig))
	}
	return encoded
}

// geminiSyntheticIDSep marks the nonce KeiRouter appends to ids it invented.
const geminiSyntheticIDSep = "__n__"

func geminiNonce() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// stripGeminiSyntheticID removes the nonce suffix from an id KeiRouter
// invented, returning the bare name-derived id.
func stripGeminiSyntheticID(id string) (string, bool) {
	if i := strings.Index(id, geminiSyntheticIDSep); i >= 0 {
		return id[:i], true
	}
	return id, false
}

func geminiEncodeResultID(res *gemFunctionResult) string {
	id := res.ID
	if id == "" {
		id = res.Name
	}
	if id == "" {
		id = "unknown"
	}
	if strings.HasPrefix(id, "call_") {
		return id
	}
	return "call_" + id
}

func renderGemContent(m core.Message, callIDToName map[string]string) gemContent {
	role := "user"
	if m.Role == core.RoleAssistant {
		role = "model"
	}
	c := gemContent{Role: role}
	for _, p := range m.Content {
		switch p.Type {
		case core.PartThinking:
			c.Parts = append(c.Parts, gemPart{Text: p.Text, Thought: true})
		case core.PartText:
			c.Parts = append(c.Parts, gemPart{Text: p.Text})
		case core.PartToolCall:
			name := sanitizeGeminiName(p.ToolCall.Name)
			idStr := p.ToolCall.ID
			idStr = strings.TrimPrefix(idStr, "call_")

			var thoughtSig string
			if idx := strings.Index(idStr, "__sig__"); idx >= 0 {
				sigB, _ := base64.RawURLEncoding.DecodeString(idStr[idx+7:])
				thoughtSig = string(sigB)
				idStr = idStr[:idx]
			}

			idToSend := idStr
			if bare, synthetic := stripGeminiSyntheticID(idStr); synthetic {
				idToSend = ""
				idStr = bare
			}
			if idToSend == name || idToSend == strings.ReplaceAll(name, ":", "_") {
				idToSend = ""
			}

			c.Parts = append(c.Parts, gemPart{
				ThoughtSignature: thoughtSig,
				FunctionCall: &gemFunctionCall{
					ID:   idToSend,
					Name: name,
					Args: normalizeGeminiArgs(p.ToolCall.Arguments),
				},
			})
		case core.PartToolResult:
			// Recover the function name from the call id; Gemini requires the
			// response name to match the originating functionCall name.
			name := callIDToName[p.ToolResult.CallID]
			if name == "" {
				name = "tool"
			}

			idStr := p.ToolResult.CallID
			idStr = strings.TrimPrefix(idStr, "call_")
			if idx := strings.Index(idStr, "__sig__"); idx >= 0 {
				idStr = idStr[:idx]
			}
			idToSend := idStr
			if _, synthetic := stripGeminiSyntheticID(idStr); synthetic {
				idToSend = ""
			}
			if idToSend == name || idToSend == strings.ReplaceAll(name, ":", "_") {
				idToSend = ""
			}

			fr := &gemFunctionResult{
				ID:       idToSend,
				Name:     name,
				Response: json.RawMessage(quoteIfNotJSON(p.ToolResult.Content)),
			}
			for _, mp := range p.ToolResult.Parts {
				if mp.Media != nil && mp.Media.Data != "" {
					fr.Parts = append(fr.Parts, gemPart{InlineData: &gemInlineData{MIMEType: mp.Media.MIMEType, Data: mp.Media.Data}})
				}
			}
			c.Parts = append(c.Parts, gemPart{FunctionResponse: fr})
		case core.PartImage, core.PartDocument:
			if p.Media != nil {
				if p.Media.Data != "" {
					c.Parts = append(c.Parts, gemPart{InlineData: &gemInlineData{MIMEType: p.Media.MIMEType, Data: p.Media.Data}})
				} else if p.Media.URL != "" {
					c.Parts = append(c.Parts, gemPart{FileData: &gemFileData{MIMEType: p.Media.MIMEType, FileURI: p.Media.URL}})
				}
			}
		}
	}
	return c
}

// normalizeGeminiArgs guarantees functionCall.args is a JSON object; Gemini
// rejects a null or non-object args value.
func normalizeGeminiArgs(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) || trimmed[0] != '{' {
		return json.RawMessage("{}")
	}
	return trimmed
}

// quoteIfNotJSON wraps a tool-result string as a JSON value if it isn't already
// a valid JSON object, since Gemini's functionResponse.response expects a JSON object.
// quoteIfNotJSON renders a tool result as the JSON object Gemini requires:
// an object passes through, any other JSON value is wrapped as {"result": v},
// and plain text is wrapped as a string. The first byte decides the branch so
// a 2 KB text result costs one marshal rather than two failing decodes.
func quoteIfNotJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) > 0 {
		switch trimmed[0] {
		case '{':
			if json.Valid([]byte(trimmed)) {
				return trimmed
			}
		case '[', '"', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if json.Valid([]byte(trimmed)) {
				return `{"result":` + trimmed + `}`
			}
		}
	}
	b, _ := json.Marshal(struct {
		Result string `json:"result"`
	}{Result: s})
	return string(b)
}

// ---- response parsing -------------------------------------------------------

type gemResponse struct {
	Candidates []struct {
		Content           gemContent      `json:"content"`
		FinishReason      string          `json:"finishReason"`
		GroundingMetadata json.RawMessage `json:"groundingMetadata"`
	} `json:"candidates"`
	UsageMetadata gemUsageMetadata `json:"usageMetadata"`
}

// gemUsageMetadata is Gemini's usage block. promptTokenCount already includes
// cached tokens; thoughtsTokenCount may or may not be inside
// candidatesTokenCount depending on the model generation.
type gemUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	ToolUsePromptTokenCount int `json:"toolUsePromptTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

// gemUsageToCore normalises Gemini usage with these rules:
//   - thoughts are added to candidates only when the total proves they are
//     not already included (prompt + candidates + toolUse != total);
//   - tool-use prompt tokens count as prompt tokens unless the response was
//     search-grounded, where they are billed per query instead;
//   - grounded responses count one web search request.
func gemUsageToCore(m gemUsageMetadata, grounding json.RawMessage) core.Usage {
	grounded := len(grounding) > 0 && !bytes.Equal(bytes.TrimSpace(grounding), []byte("null")) &&
		bytes.Contains(grounding, []byte("webSearchQueries"))
	prompt := m.PromptTokenCount
	if !grounded {
		prompt += m.ToolUsePromptTokenCount
	}
	completion := m.CandidatesTokenCount
	inclusive := m.TotalTokenCount > 0 && m.PromptTokenCount+m.CandidatesTokenCount+m.ToolUsePromptTokenCount == m.TotalTokenCount
	if !inclusive {
		completion += m.ThoughtsTokenCount
	}
	if completion == 0 && m.TotalTokenCount > m.PromptTokenCount {
		completion = m.TotalTokenCount - m.PromptTokenCount
	}
	total := m.TotalTokenCount
	if total == 0 {
		total = prompt + completion
	}
	u := core.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		CachedTokens:     m.CachedContentTokenCount,
		ReasoningTokens:  m.ThoughtsTokenCount,
		Source:           core.UsageSourceProvider,
	}
	if grounded {
		u.WebSearchRequests = 1
	}
	return u
}

func (GeminiCodec) ParseResponse(body []byte, model string) (*core.ChatResponse, error) {
	var raw gemResponse
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("gemini: parse response: %w", err)
	}
	if len(raw.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: response has no candidates")
	}
	cand := raw.Candidates[0]
	msg := parseGemContent(cand.Content)
	msg.Role = core.RoleAssistant

	return &core.ChatResponse{
		Model:        model,
		Message:      msg,
		FinishReason: mapGemCandidateFinish(cand.Content, cand.FinishReason),
		Usage:        gemUsageToCore(raw.UsageMetadata, cand.GroundingMetadata),
	}, nil
}

func (GeminiCodec) RenderResponse(resp *core.ChatResponse) ([]byte, error) {
	content := renderGemContent(resp.Message, nil)
	content.Role = "model"
	out := map[string]any{
		"candidates": []map[string]any{{
			"content":      content,
			"finishReason": renderGemFinish(resp.FinishReason),
			"index":        0,
		}},
		"usageMetadata": renderGemUsage(resp.Usage),
	}
	return json.Marshal(out)
}

// renderGemUsage renders usage in Gemini's usageMetadata shape. Canonical
// CompletionTokens includes thoughts; Gemini reports them separately.
func renderGemUsage(u core.Usage) map[string]int {
	candidates := u.CompletionTokens - u.ReasoningTokens
	if candidates < 0 {
		candidates = u.CompletionTokens
	}
	out := map[string]int{
		"promptTokenCount":     u.PromptTokens,
		"candidatesTokenCount": candidates,
		"totalTokenCount":      u.TotalTokens,
	}
	if u.ReasoningTokens > 0 {
		out["thoughtsTokenCount"] = u.ReasoningTokens
	}
	if u.CachedTokens > 0 {
		out["cachedContentTokenCount"] = u.CachedTokens
	}
	return out
}

// Gemini uses STOP for both a completed text response and a completed
// function-call turn. Canonical/OpenAI-compatible clients need the latter to
// be reported as tool_calls so their agent loop records and resumes the turn
// with the correct protocol state.
func mapGemCandidateFinish(content gemContent, reason string) core.FinishReason {
	finish := mapGemFinish(reason)
	if finish != core.FinishStop {
		return finish
	}
	for _, part := range content.Parts {
		if part.FunctionCall != nil {
			return core.FinishToolCalls
		}
	}
	return finish
}

func mapGemFinish(r string) core.FinishReason {
	switch r {
	case "STOP":
		return core.FinishStop
	case "MAX_TOKENS":
		return core.FinishLength
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY",
		"IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION", "LANGUAGE", "OTHER":
		return core.FinishFilter
	default:
		// MALFORMED_FUNCTION_CALL, UNEXPECTED_TOOL_CALL, MISSING_THOUGHT_SIGNATURE
		// and friends end the turn like a stop.
		return core.FinishStop
	}
}

func renderGemFinish(r core.FinishReason) string {
	switch r {
	case core.FinishLength:
		return "MAX_TOKENS"
	case core.FinishFilter:
		return "SAFETY"
	default:
		return "STOP"
	}
}
