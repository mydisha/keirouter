package transform

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// CommandCodeCodec handles Command Code's /alpha/generate wire format. The
// request is a wrapped envelope {threadId, memory, config, params} where params
// uses an Anthropic-style shape: a top-level system string, messages with
// content blocks (text / tool-call / tool-result), and tools as
// {name, description, input_schema}. The response is an AI SDK v5 NDJSON event
// stream (text-delta, reasoning-delta, tool-input-*, tool-call, finish-step,
// finish, error).
type CommandCodeCodec struct{}

func (CommandCodeCodec) Dialect() core.Dialect { return core.DialectCommandCode }

// ParseRequest is provided for completeness; Command Code is an upstream-only
// dialect (KeiRouter never serves it to clients), so this is a minimal decode.
func (CommandCodeCodec) ParseRequest(body []byte) (*core.ChatRequest, error) {
	var env struct {
		Params struct {
			Model    string          `json:"model"`
			System   string          `json:"system"`
			Stream   bool            `json:"stream"`
			Messages json.RawMessage `json:"messages"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("commandcode: parse request: %w", err)
	}
	return &core.ChatRequest{Model: env.Params.Model, System: env.Params.System, Stream: env.Params.Stream}, nil
}

// RenderRequest builds the Command Code generate envelope from a canonical
// request.
func (CommandCodeCodec) RenderRequest(req *core.ChatRequest) ([]byte, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case core.RoleTool:
			var blocks []map[string]any
			for _, p := range m.Content {
				if p.Type == core.PartToolResult && p.ToolResult != nil {
					blocks = append(blocks, map[string]any{
						"type":       "tool-result",
						"toolCallId": p.ToolResult.CallID,
						"toolName":   "",
						"output":     map[string]any{"type": "text", "value": p.ToolResult.Content},
					})
				}
			}
			if len(blocks) > 0 {
				messages = append(messages, map[string]any{"role": "tool", "content": blocks})
			}
		case core.RoleAssistant:
			var blocks []map[string]any
			for _, p := range m.Content {
				switch p.Type {
				case core.PartText:
					if p.Text != "" {
						blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
					}
				case core.PartToolCall:
					if p.ToolCall != nil {
						blocks = append(blocks, map[string]any{
							"type":       "tool-call",
							"toolCallId": p.ToolCall.ID,
							"toolName":   p.ToolCall.Name,
							"input":      rawOrEmptyObject(p.ToolCall.Arguments),
						})
					}
				}
			}
			if len(blocks) == 0 {
				blocks = []map[string]any{{"type": "text", "text": ""}}
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
		default: // user
			blocks := commandCodeContentBlocks(m)
			messages = append(messages, map[string]any{"role": "user", "content": blocks})
		}
	}

	params := map[string]any{
		"model":       req.Model,
		"messages":    messages,
		"stream":      req.Stream,
		"max_tokens":  derefOr(req.MaxTokens, 64000),
		"temperature": derefFloatOr(req.Temperature, 0.3),
	}
	if req.System != "" {
		params["system"] = req.System
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		params["tools"] = tools
	}
	if req.TopP != nil {
		params["top_p"] = *req.TopP
	}

	envelope := map[string]any{
		"threadId": uuid.NewString(),
		"memory":   "",
		"config": map[string]any{
			"workingDir":    "/",
			"environment":   "linux",
			"structure":     []any{},
			"isGitRepo":     false,
			"currentBranch": "",
			"mainBranch":    "",
			"gitStatus":     "",
			"recentCommits": []any{},
			"date":          time.Now().Format("2006-01-02"),
		},
		"params": params,
	}
	return json.Marshal(envelope)
}

func commandCodeContentBlocks(m core.Message) []map[string]any {
	var blocks []map[string]any
	for _, p := range m.Content {
		switch p.Type {
		case core.PartText:
			blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
		case core.PartImage:
			blocks = append(blocks, map[string]any{"type": "text", "text": "[image omitted]"})
		}
	}
	if len(blocks) == 0 {
		blocks = []map[string]any{{"type": "text", "text": ""}}
	}
	return blocks
}

// ParseResponse parses a non-streaming Command Code response. The generate API
// is stream-first; for a unary call we accumulate the same event types.
func (CommandCodeCodec) ParseResponse(body []byte, model string) (*core.ChatResponse, error) {
	// Command Code returns NDJSON even for non-stream; fold all lines through
	// the streaming parser and assemble a single response.
	msg := core.Message{Role: core.RoleAssistant}
	var textBuf strings.Builder
	finish := core.FinishStop
	var usage core.Usage

	for _, line := range bytes.Split(body, []byte("\n")) {
		chunks, err := CommandCodeCodec{}.ParseStreamLine(line, model)
		if err != nil {
			continue
		}
		for _, ch := range chunks {
			switch ch.Type {
			case core.ChunkText:
				textBuf.WriteString(ch.Delta)
			case core.ChunkToolCall:
				if ch.ToolCall != nil {
					msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: ch.ToolCall})
					finish = core.FinishToolCalls
				}
			case core.ChunkFinish:
				if ch.FinishReason != "" {
					finish = ch.FinishReason
				}
			case core.ChunkUsage:
				if ch.Usage != nil {
					usage = *ch.Usage
				}
			}
		}
	}
	if textBuf.Len() > 0 {
		msg.Content = append([]core.ContentPart{{Type: core.PartText, Text: textBuf.String()}}, msg.Content...)
	}
	return &core.ChatResponse{Model: model, Message: msg, FinishReason: finish, Usage: usage}, nil
}

// RenderResponse is not used (Command Code is upstream-only); provided to
// satisfy the Codec interface.
func (CommandCodeCodec) RenderResponse(resp *core.ChatResponse) ([]byte, error) {
	return json.Marshal(map[string]any{"type": "finish", "model": resp.Model})
}

// ccStreamEvent is one AI SDK v5 NDJSON event from Command Code.
type ccStreamEvent struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Delta        string          `json:"delta"`
	ID           string          `json:"id"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	Input        json.RawMessage `json:"input"`
	FinishReason string          `json:"finishReason"`
	Usage        *ccUsage        `json:"usage"`
	TotalUsage   *ccUsage        `json:"totalUsage"`
}

type ccUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// ParseStreamLine converts one Command Code NDJSON event into canonical chunks.
func (CommandCodeCodec) ParseStreamLine(line []byte, _ string) ([]core.StreamChunk, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	// Tolerate optional "data:" framing.
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(line[5:])
	}
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
		return nil, nil
	}

	var ev ccStreamEvent
	if err := json.UnmarshalNoCopy(line, &ev); err != nil {
		return nil, fmt.Errorf("commandcode: parse stream event: %w", err)
	}

	switch ev.Type {
	case "text-delta":
		text := ev.Text
		if text == "" {
			text = ev.Delta
		}
		if text == "" {
			return nil, nil
		}
		return []core.StreamChunk{{Type: core.ChunkText, Delta: text}}, nil

	case "reasoning-delta":
		if ev.Text == "" {
			return nil, nil
		}
		return []core.StreamChunk{{Type: core.ChunkThinking, Delta: ev.Text}}, nil

	case "tool-input-start":
		id := firstNonEmpty(ev.ID, ev.ToolCallID)
		return []core.StreamChunk{{
			Type:     core.ChunkToolCall,
			ToolCall: &core.ToolCall{ID: id, Name: ev.ToolName, Arguments: json.RawMessage("")},
		}}, nil

	case "tool-input-delta":
		delta := ev.Delta
		if delta == "" {
			return nil, nil
		}
		return []core.StreamChunk{{
			Type:     core.ChunkToolCall,
			ToolCall: &core.ToolCall{Arguments: json.RawMessage(delta)},
		}}, nil

	case "tool-call":
		args := rawOrEmptyObject(ev.Input)
		return []core.StreamChunk{{
			Type:     core.ChunkToolCall,
			ToolCall: &core.ToolCall{ID: ev.ToolCallID, Name: ev.ToolName, Arguments: args},
		}}, nil

	case "finish-step":
		var chunks []core.StreamChunk
		if ev.FinishReason != "" {
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkFinish, FinishReason: mapCCFinish(ev.FinishReason)})
		}
		if ev.Usage != nil {
			chunks = append(chunks, ccUsageChunk(ev.Usage))
		}
		return chunks, nil

	case "finish":
		var chunks []core.StreamChunk
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkFinish, FinishReason: mapCCFinish(ev.FinishReason)})
		if u := ev.TotalUsage; u != nil {
			chunks = append(chunks, ccUsageChunk(u))
		} else if ev.Usage != nil {
			chunks = append(chunks, ccUsageChunk(ev.Usage))
		}
		return chunks, nil

	case "error":
		return []core.StreamChunk{{
			Type: core.ChunkError,
			Err:  &core.ProviderError{Kind: core.ErrUpstream, Provider: "commandcode", Message: ev.Text},
		}}, nil

	default:
		// start, start-step, reasoning-start/end, text-start/end, metadata: ignore.
		return nil, nil
	}
}

// RenderStreamChunk / RenderStreamDone are not used (Command Code is
// upstream-only) but satisfy StreamCodec so the dialect can be a stream codec.
func (CommandCodeCodec) RenderStreamChunk(_ core.StreamChunk, _ *StreamState) ([][]byte, error) {
	return nil, nil
}
func (CommandCodeCodec) RenderStreamDone(_ *StreamState) [][]byte { return nil }

func ccUsageChunk(u *ccUsage) core.StreamChunk {
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return core.StreamChunk{
		Type: core.ChunkUsage,
		Usage: &core.Usage{
			PromptTokens:     u.InputTokens,
			CompletionTokens: u.OutputTokens,
			TotalTokens:      total,
			Source:           core.UsageSourceProvider,
		},
	}
}

func mapCCFinish(reason string) core.FinishReason {
	switch reason {
	case "stop":
		return core.FinishStop
	case "length":
		return core.FinishLength
	case "tool-calls", "tool_use":
		return core.FinishToolCalls
	case "content-filter":
		return core.FinishFilter
	default:
		return core.FinishStop
	}
}

func rawOrEmptyObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

func derefOr(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

func derefFloatOr(p *float64, def float64) float64 {
	if p != nil {
		return *p
	}
	return def
}
