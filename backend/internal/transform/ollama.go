package transform

import (
	"bytes"
	"fmt"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// OllamaCodec handles Ollama's native /api/chat wire format. Ollama groups
// turns under "messages" with string content (plus an optional images[] of raw
// base64), carries generation knobs under "options" (temperature, num_predict,
// top_p), accepts OpenAI-style tool definitions, and streams newline-delimited
// JSON (NDJSON) rather than SSE.
type OllamaCodec struct{}

func (OllamaCodec) Dialect() core.Dialect { return core.DialectOllama }

// ---- wire types -------------------------------------------------------------

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	Format   json.RawMessage `json:"format,omitempty"`
}

type ollamaOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumPredict  *int     `json:"num_predict,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Thinking  string           `json:"thinking,omitempty"`
	Images    []string         `json:"images,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Type     string `json:"type,omitempty"`
	ID       string `json:"id,omitempty"`
	Function struct {
		Index     int             `json:"index,omitempty"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Thinking  string           `json:"thinking"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// ---- request parsing (inbound Ollama client) --------------------------------

func (OllamaCodec) ParseRequest(body []byte) (*core.ChatRequest, error) {
	var raw ollamaRequest
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("ollama: parse request: %w", err)
	}

	req := &core.ChatRequest{Model: raw.Model, Stream: raw.Stream}
	if raw.Options != nil {
		req.Temperature = raw.Options.Temperature
		req.TopP = raw.Options.TopP
		req.MaxTokens = raw.Options.NumPredict
		req.Stop = raw.Options.Stop
	}
	if len(raw.Tools) > 0 {
		req.Tools = parseOpenAIToolDefs(raw.Tools)
	}
	for _, m := range raw.Messages {
		if m.Role == "system" {
			if req.System != "" {
				req.System += "\n"
			}
			req.System += m.Content
			continue
		}
		req.Messages = append(req.Messages, parseOllamaMessage(m))
	}
	return req, nil
}

func parseOllamaMessage(m ollamaMessage) core.Message {
	msg := core.Message{Role: mapOllamaRole(m.Role)}
	if m.Role == "tool" {
		msg.Content = append(msg.Content, core.ContentPart{
			Type:       core.PartToolResult,
			ToolResult: &core.ToolResult{CallID: m.ToolName, Content: m.Content},
		})
		return msg
	}
	if m.Content != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: m.Content})
	}
	for _, img := range m.Images {
		msg.Content = append(msg.Content, core.ContentPart{
			Type:  core.PartImage,
			Media: &core.MediaPayload{MIMEType: "image/png", Data: img},
		})
	}
	for _, tc := range m.ToolCalls {
		args := tc.Function.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		msg.Content = append(msg.Content, core.ContentPart{
			Type:     core.PartToolCall,
			ToolCall: &core.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args},
		})
	}
	return msg
}

func mapOllamaRole(role string) core.Role {
	switch role {
	case "assistant":
		return core.RoleAssistant
	case "tool":
		return core.RoleTool
	case "system":
		return core.RoleSystem
	default:
		return core.RoleUser
	}
}

// ---- request rendering (outbound to Ollama provider) ------------------------

func (OllamaCodec) RenderRequest(req *core.ChatRequest) ([]byte, error) {
	out := ollamaRequest{Model: req.Model, Stream: req.Stream}

	if req.Temperature != nil || req.TopP != nil || req.MaxTokens != nil || len(req.Stop) > 0 {
		out.Options = &ollamaOptions{
			Temperature: req.Temperature,
			TopP:        req.TopP,
			NumPredict:  req.MaxTokens,
			Stop:        req.Stop,
		}
	}
	if len(req.Tools) > 0 {
		out.Tools = renderOpenAIToolDefs(req.Tools)
	}

	// Map tool-call ids to their tool names so tool results can carry the
	// human-readable tool_name Ollama expects.
	idToName := map[string]string{}
	for _, m := range req.Messages {
		for _, p := range m.Content {
			if p.Type == core.PartToolCall && p.ToolCall != nil && p.ToolCall.ID != "" {
				idToName[p.ToolCall.ID] = p.ToolCall.Name
			}
		}
	}

	if req.System != "" {
		out.Messages = append(out.Messages, ollamaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, renderOllamaMessage(m, idToName)...)
	}
	return json.Marshal(out)
}

func renderOllamaMessage(m core.Message, idToName map[string]string) []ollamaMessage {
	// Tool results become standalone "tool" messages in Ollama.
	if m.Role == core.RoleTool {
		var msgs []ollamaMessage
		for _, p := range m.Content {
			if p.Type == core.PartToolResult && p.ToolResult != nil {
				name := idToName[p.ToolResult.CallID]
				if name == "" {
					name = p.ToolResult.CallID
				}
				if name == "" {
					name = "unknown_tool"
				}
				msgs = append(msgs, ollamaMessage{Role: "tool", ToolName: name, Content: p.ToolResult.Content})
			}
		}
		return msgs
	}

	out := ollamaMessage{Role: string(m.Role)}
	var text strings.Builder
	for _, p := range m.Content {
		switch p.Type {
		case core.PartText:
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(p.Text)
		case core.PartImage:
			if p.Media != nil && p.Media.Data != "" {
				out.Images = append(out.Images, p.Media.Data)
			}
		case core.PartToolCall:
			if p.ToolCall != nil {
				args := p.ToolCall.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				tc := ollamaToolCall{Type: "function", ID: p.ToolCall.ID}
				tc.Function.Name = p.ToolCall.Name
				tc.Function.Arguments = args
				out.ToolCalls = append(out.ToolCalls, tc)
			}
		}
	}
	out.Content = text.String()

	// Skip wholly-empty non-assistant messages.
	if out.Content == "" && len(out.Images) == 0 && len(out.ToolCalls) == 0 && m.Role != core.RoleAssistant {
		return nil
	}
	return []ollamaMessage{out}
}

// ---- response parsing (from Ollama provider) --------------------------------

func (OllamaCodec) ParseResponse(body []byte, model string) (*core.ChatResponse, error) {
	var raw ollamaResponse
	if err := json.UnmarshalNoCopy(body, &raw); err != nil {
		return nil, fmt.Errorf("ollama: parse response: %w", err)
	}

	msg := core.Message{Role: core.RoleAssistant}
	if raw.Message.Thinking != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: raw.Message.Thinking})
	}
	if raw.Message.Content != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: raw.Message.Content})
	}
	finish := core.FinishStop
	for i, tc := range raw.Message.ToolCalls {
		args := tc.Function.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		msg.Content = append(msg.Content, core.ContentPart{
			Type:     core.PartToolCall,
			ToolCall: &core.ToolCall{ID: id, Name: tc.Function.Name, Arguments: args},
		})
		finish = core.FinishToolCalls
	}
	if raw.DoneReason == "tool_calls" {
		finish = core.FinishToolCalls
	}

	modelOut := raw.Model
	if modelOut == "" {
		modelOut = model
	}
	return &core.ChatResponse{
		Model:        modelOut,
		Message:      msg,
		FinishReason: finish,
		Usage: core.Usage{
			PromptTokens:     raw.PromptEvalCount,
			CompletionTokens: raw.EvalCount,
			TotalTokens:      raw.PromptEvalCount + raw.EvalCount,
			Source:           core.UsageSourceProvider,
		},
	}, nil
}

// RenderResponse encodes a canonical response as an Ollama /api/chat result for
// a client that speaks Ollama.
func (OllamaCodec) RenderResponse(resp *core.ChatResponse) ([]byte, error) {
	msg := map[string]any{"role": "assistant"}
	var content, thinking strings.Builder
	var toolCalls []map[string]any
	for _, p := range resp.Message.Content {
		switch p.Type {
		case core.PartText:
			content.WriteString(p.Text)
		case core.PartThinking:
			thinking.WriteString(p.Text)
		case core.PartToolCall:
			if p.ToolCall != nil {
				args := p.ToolCall.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				fn := map[string]any{"name": p.ToolCall.Name, "arguments": args}
				toolCalls = append(toolCalls, map[string]any{"type": "function", "function": fn})
			}
		}
	}
	msg["content"] = content.String()
	if thinking.Len() > 0 {
		msg["thinking"] = thinking.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	doneReason := "stop"
	if resp.FinishReason == core.FinishToolCalls {
		doneReason = "tool_calls"
	}
	out := map[string]any{
		"model":             resp.Model,
		"message":           msg,
		"done":              true,
		"done_reason":       doneReason,
		"prompt_eval_count": resp.Usage.PromptTokens,
		"eval_count":        resp.Usage.CompletionTokens,
	}
	return json.Marshal(out)
}

// ---- streaming (NDJSON, not SSE) --------------------------------------------

// ParseStreamLine decodes one Ollama NDJSON line into canonical chunks. Unlike
// SSE dialects, Ollama lines are bare JSON objects with no "data:" prefix; the
// connector passes each raw line here.
func (OllamaCodec) ParseStreamLine(line []byte, model string) ([]core.StreamChunk, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var raw ollamaResponse
	if err := json.UnmarshalNoCopy(line, &raw); err != nil {
		return nil, fmt.Errorf("ollama: parse stream line: %w", err)
	}

	var chunks []core.StreamChunk
	if raw.Message.Thinking != "" {
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkThinking, Delta: raw.Message.Thinking})
	}
	if raw.Message.Content != "" {
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkText, Delta: raw.Message.Content})
	}
	hadTool := false
	for i, tc := range raw.Message.ToolCalls {
		args := tc.Function.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		chunks = append(chunks, core.StreamChunk{
			Type:     core.ChunkToolCall,
			Index:    tc.Function.Index,
			ToolCall: &core.ToolCall{ID: id, Name: tc.Function.Name, Arguments: args},
		})
		hadTool = true
	}

	if raw.Done {
		finish := core.FinishStop
		if raw.DoneReason == "tool_calls" || hadTool {
			finish = core.FinishToolCalls
		}
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkFinish, FinishReason: finish})
		chunks = append(chunks, core.StreamChunk{
			Type: core.ChunkUsage,
			Usage: &core.Usage{
				PromptTokens:     raw.PromptEvalCount,
				CompletionTokens: raw.EvalCount,
				TotalTokens:      raw.PromptEvalCount + raw.EvalCount,
				Source:           core.UsageSourceProvider,
			},
		})
	}
	return chunks, nil
}

// RenderStreamChunk encodes a canonical chunk as an Ollama NDJSON line for a
// client that speaks Ollama.
func (OllamaCodec) RenderStreamChunk(chunk core.StreamChunk, state *StreamState) ([][]byte, error) {
	switch chunk.Type {
	case core.ChunkText:
		return [][]byte{ollamaLine(map[string]any{
			"model":   state.Model,
			"message": map[string]any{"role": "assistant", "content": chunk.Delta},
			"done":    false,
		})}, nil
	case core.ChunkThinking:
		return [][]byte{ollamaLine(map[string]any{
			"model":   state.Model,
			"message": map[string]any{"role": "assistant", "content": "", "thinking": chunk.Delta},
			"done":    false,
		})}, nil
	case core.ChunkToolCall:
		if chunk.ToolCall == nil {
			return nil, nil
		}
		args := chunk.ToolCall.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		fn := map[string]any{"name": chunk.ToolCall.Name, "arguments": args}
		return [][]byte{ollamaLine(map[string]any{
			"model": state.Model,
			"message": map[string]any{
				"role":       "assistant",
				"content":    "",
				"tool_calls": []map[string]any{{"type": "function", "function": fn}},
			},
			"done": false,
		})}, nil
	case core.ChunkUsage:
		if chunk.Usage == nil {
			return nil, nil
		}
		if state.Custom == nil {
			state.Custom = map[string]any{}
		}
		state.Custom["prompt_tokens"] = chunk.Usage.PromptTokens
		state.Custom["completion_tokens"] = chunk.Usage.CompletionTokens
		return nil, nil
	default:
		return nil, nil
	}
}

// RenderStreamDone emits the terminal done=true NDJSON line, carrying any usage
// captured from the final usage chunk.
func (OllamaCodec) RenderStreamDone(state *StreamState) [][]byte {
	final := map[string]any{
		"model":   state.Model,
		"message": map[string]any{"role": "assistant", "content": ""},
		"done":    true,
	}
	if state.Custom != nil {
		if v, ok := state.Custom["prompt_tokens"].(int); ok {
			final["prompt_eval_count"] = v
		}
		if v, ok := state.Custom["completion_tokens"].(int); ok {
			final["eval_count"] = v
		}
	}
	return [][]byte{ollamaLine(final)}
}

// ollamaLine formats one NDJSON line (compact JSON + newline).
func ollamaLine(payload map[string]any) []byte {
	b, _ := json.Marshal(payload)
	return append(b, '\n')
}

// ---- OpenAI-style tool definition helpers -----------------------------------

// parseOpenAIToolDefs converts an OpenAI-style tools array into canonical Tools.
func parseOpenAIToolDefs(raw json.RawMessage) []core.Tool {
	var defs []struct {
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &defs); err != nil {
		return nil
	}
	var tools []core.Tool
	for _, d := range defs {
		if d.Function.Name == "" {
			continue
		}
		tools = append(tools, core.Tool{
			Name:        d.Function.Name,
			Description: d.Function.Description,
			Parameters:  d.Function.Parameters,
		})
	}
	return tools
}

// renderOpenAIToolDefs converts canonical Tools into an OpenAI-style tools array
// (the format Ollama accepts).
func renderOpenAIToolDefs(tools []core.Tool) json.RawMessage {
	arr := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if len(t.Parameters) > 0 {
			fn["parameters"] = t.Parameters
		}
		arr = append(arr, map[string]any{"type": "function", "function": fn})
	}
	b, _ := json.Marshal(arr)
	return b
}
