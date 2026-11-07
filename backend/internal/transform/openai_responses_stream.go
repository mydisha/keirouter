package transform

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// The Responses API streams a rich, typed event sequence rather than uniform
// chunks:
//
//	response.created → response.in_progress →
//	response.output_item.added (message|reasoning|function_call) →
//	response.output_text.delta* / response.function_call_arguments.delta* /
//	response.reasoning_summary_text.delta* →
//	response.output_item.done → response.completed
//
// ParseStreamLine maps each event payload to canonical chunks (for routing a
// Responses provider like Codex back to a client). RenderStreamChunk produces
// the corresponding event sequence for a client that speaks the Responses
// dialect.

// respStreamEvent is one Responses SSE data payload.
type respStreamEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemID string `json:"item_id"`
	// OutputIndex orders parallel tool calls; chat clients accumulate
	// arguments per index, so it must be carried through.
	OutputIndex int             `json:"output_index"`
	Item        *respStreamItem `json:"item"`
	Response    *struct {
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type respStreamItem struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	EncryptedContent string `json:"encrypted_content"`
}

// ParseStreamLine converts one Responses SSE event payload into canonical chunks.
func (OpenAIResponsesCodec) ParseStreamLine(line []byte, _ string) ([]core.StreamChunk, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
		return nil, nil
	}

	var ev respStreamEvent
	if err := json.UnmarshalNoCopy(line, &ev); err != nil {
		return nil, fmt.Errorf("openai-responses: parse stream event: %w", err)
	}

	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta == "" {
			return nil, nil
		}
		return []core.StreamChunk{{Type: core.ChunkText, Delta: ev.Delta}}, nil

	case "response.reasoning_summary_text.delta":
		if ev.Delta == "" {
			return nil, nil
		}
		return []core.StreamChunk{{Type: core.ChunkThinking, Delta: ev.Delta}}, nil

	case "response.output_item.added":
		if ev.Item != nil && (ev.Item.Type == "function_call" || ev.Item.Type == "custom_tool_call") {
			tc := &core.ToolCall{ID: ev.Item.CallID, Name: ev.Item.Name, Arguments: json.RawMessage("{}"), Custom: ev.Item.Type == "custom_tool_call"}
			if tc.Custom {
				tc.Arguments = nil
			}
			return []core.StreamChunk{{Type: core.ChunkToolCall, Index: ev.OutputIndex, ToolCall: tc}}, nil
		}
		return nil, nil

	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		if ev.Delta == "" {
			return nil, nil
		}
		return []core.StreamChunk{{
			Type:     core.ChunkToolCall,
			Index:    ev.OutputIndex,
			ToolCall: &core.ToolCall{Arguments: json.RawMessage(ev.Delta)},
		}}, nil

	case "response.completed":
		var chunks []core.StreamChunk
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop})
		if ev.Response != nil && ev.Response.Usage != nil {
			u := ev.Response.Usage
			chunks = append(chunks, core.StreamChunk{
				Type: core.ChunkUsage,
				Usage: &core.Usage{
					PromptTokens:     u.InputTokens,
					CompletionTokens: u.OutputTokens,
					TotalTokens:      u.InputTokens + u.OutputTokens,
					CachedTokens:     u.InputTokensDetails.CachedTokens,
					ReasoningTokens:  u.OutputTokensDetails.ReasoningTokens,
					Source:           core.UsageSourceProvider,
				},
			})
		}
		return chunks, nil

	case "error", "response.failed":
		msg := ""
		if ev.Error != nil {
			msg = ev.Error.Message
		} else if ev.Response != nil && ev.Response.Error != nil {
			msg = ev.Response.Error.Message
		}
		return []core.StreamChunk{{
			Type: core.ChunkError,
			Err:  classifyRespStreamError(msg),
		}}, nil

	case "response.output_item.done":
		// The summary already streamed as deltas; only the signature is new.
		if ev.Item != nil && ev.Item.Type == "reasoning" && ev.Item.EncryptedContent != "" {
			return []core.StreamChunk{{Type: core.ChunkThinking, Signature: ev.Item.EncryptedContent}}, nil
		}
		return nil, nil

	default:
		// response.created, in_progress, content_part.*,
		// output_text.done, reasoning_summary_*: nothing canonical to emit.
		return nil, nil
	}
}

// classifyRespStreamError maps an in-stream error event (HTTP 200 with an
// error in the SSE body, so no status code) onto the right error kind/scope.
// Codex reports request-level problems this way — e.g. "Your input exceeds
// the context window of this model." — and the previous blanket ErrUpstream
// classification (provider scope) put the account on cooldown and fed the
// provider circuit breaker on every client retry, eventually locking out all
// accounts with "all matching candidates are temporarily unavailable".
func classifyRespStreamError(msg string) *core.ProviderError {
	m := strings.ToLower(msg)
	switch {
	// Codex can report model capacity and service overload inside an otherwise
	// successful HTTP 200 stream. This is not a bad credential: let the
	// pipeline rotate attempts without cooling the account or provider.
	case strings.Contains(m, "selected model is at capacity"),
		strings.Contains(m, "model_at_capacity"),
		strings.Contains(m, "server_is_overloaded"),
		strings.Contains(m, "service_unavailable_error"):
		return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Message: msg}
	}
	return errclass.ClassifyErrorDetail("", 0, "", msg)
}

// respStreamState tracks per-stream rendering bookkeeping for the Responses
// event sequence, stashed in StreamState.Custom.
type respStreamState struct {
	seq          int
	started      bool
	responseID   string
	msgAdded     bool
	msgPartAdded bool
	msgText      string
	toolAdded    map[int]bool
	toolCallID   map[int]string
	toolName     map[int]string
	toolCustom   map[int]bool
	toolArgs     map[int]string
	usage        core.Usage
	completed    bool
	// One reasoning item per upstream thinking block: a signature only
	// verifies against its own block's text.
	rsOpen  bool
	rsCount int
	rsText  string
}

func respState(state *StreamState) *respStreamState {
	if state.Custom == nil {
		state.Custom = map[string]any{}
	}
	if s, ok := state.Custom["resp"].(*respStreamState); ok {
		return s
	}
	s := &respStreamState{
		responseID: firstNonEmpty(state.MessageID, "resp_stream"),
		toolAdded:  map[int]bool{},
		toolCallID: map[int]string{},
		toolName:   map[int]string{},
		toolCustom: map[int]bool{},
		toolArgs:   map[int]string{},
	}
	state.Custom["resp"] = s
	return s
}

// RenderStreamChunk emits Responses API event(s) for a canonical chunk.
func (OpenAIResponsesCodec) RenderStreamChunk(chunk core.StreamChunk, state *StreamState) ([][]byte, error) {
	s := respState(state)
	var events [][]byte
	emit := func(eventType string, data map[string]any) {
		s.seq++
		data["sequence_number"] = s.seq
		data["type"] = eventType
		events = append(events, respEvent(eventType, data))
	}

	ensureStarted := func() {
		if s.started {
			return
		}
		s.started = true
		emit("response.created", map[string]any{
			"response": map[string]any{
				"id": s.responseID, "object": "response", "status": "in_progress", "output": []any{},
			},
		})
		emit("response.in_progress", map[string]any{
			"response": map[string]any{"id": s.responseID, "object": "response", "status": "in_progress"},
		})
	}

	rsID := func() string { return "rs_" + s.responseID + "_" + strconv.Itoa(s.rsCount) }
	closeReasoning := func(signature string) {
		if !s.rsOpen {
			return
		}
		summary := []any{}
		if s.rsText != "" {
			summary = append(summary, map[string]any{"type": "summary_text", "text": s.rsText})
		}
		item := map[string]any{"id": rsID(), "type": "reasoning", "summary": summary}
		if signature != "" {
			item["encrypted_content"] = core.TagSignature(signature, state.ReasoningOrigin, "openai")
		}
		emit("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		s.rsOpen, s.rsText = false, ""
		s.rsCount++
	}

	switch chunk.Type {
	case core.ChunkText:
		ensureStarted()
		closeReasoning("")
		msgID := "msg_" + s.responseID + "_0"
		if !s.msgAdded {
			s.msgAdded = true
			emit("response.output_item.added", map[string]any{
				"output_index": 0,
				"item":         map[string]any{"id": msgID, "type": "message", "role": "assistant", "content": []any{}},
			})
		}
		if !s.msgPartAdded {
			s.msgPartAdded = true
			emit("response.content_part.added", map[string]any{
				"item_id": msgID, "output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
			})
		}
		s.msgText += chunk.Delta
		emit("response.output_text.delta", map[string]any{
			"item_id": msgID, "output_index": 0, "content_index": 0, "delta": chunk.Delta,
		})

	case core.ChunkThinking:
		ensureStarted()
		if !s.rsOpen {
			s.rsOpen = true
			emit("response.output_item.added", map[string]any{
				"output_index": 0,
				"item":         map[string]any{"id": rsID(), "type": "reasoning", "summary": []any{}},
			})
		}
		if chunk.Delta != "" {
			s.rsText += chunk.Delta
			emit("response.reasoning_summary_text.delta", map[string]any{
				"item_id": rsID(), "output_index": 0, "summary_index": 0, "delta": chunk.Delta,
			})
		}
		if chunk.Signature != "" {
			closeReasoning(chunk.Signature)
		}

	case core.ChunkToolCall:
		if chunk.ToolCall == nil {
			return nil, nil
		}
		ensureStarted()
		closeReasoning("")
		idx := chunk.Index
		if chunk.ToolCall.Name != "" {
			s.toolName[idx] = chunk.ToolCall.Name
			s.toolCustom[idx] = chunk.ToolCall.Custom
		}
		if chunk.ToolCall.ID != "" && !s.toolAdded[idx] {
			s.toolAdded[idx] = true
			s.toolCallID[idx] = chunk.ToolCall.ID
			emit("response.output_item.added", map[string]any{
				"output_index": idx,
				"item": map[string]any{
					"id": "fc_" + chunk.ToolCall.ID, "type": s.toolItemType(idx),
					"call_id": chunk.ToolCall.ID, "name": s.toolName[idx], "arguments": "",
				},
			})
		}
		if args := string(chunk.ToolCall.Arguments); args != "" && args != "{}" {
			callID := s.toolCallID[idx]
			s.toolArgs[idx] += args
			emit(s.toolDeltaEvent(idx), map[string]any{
				"item_id": "fc_" + callID, "output_index": idx, "delta": args,
			})
		}

	case core.ChunkFinish:
		// Close any open message/tool items, then complete.
		closeReasoning("")
		if s.msgAdded {
			msgID := "msg_" + s.responseID + "_0"
			emit("response.output_text.done", map[string]any{
				"item_id": msgID, "output_index": 0, "content_index": 0, "text": s.msgText,
			})
			emit("response.output_item.done", map[string]any{
				"output_index": 0,
				"item": map[string]any{
					"id": msgID, "type": "message", "role": "assistant",
					"content": []map[string]any{{"type": "output_text", "text": s.msgText, "annotations": []any{}}},
				},
			})
		}
		for _, idx := range slices.Sorted(maps.Keys(s.toolCallID)) {
			callID := s.toolCallID[idx]
			args := s.toolArgs[idx]
			if args == "" {
				args = "{}"
			}
			if s.toolCustom[idx] {
				emit("response.custom_tool_call_input.done", map[string]any{
					"item_id": "fc_" + callID, "output_index": idx, "input": args,
				})
				emit("response.output_item.done", map[string]any{
					"output_index": idx,
					"item": map[string]any{
						"id": "fc_" + callID, "type": "custom_tool_call",
						"call_id": callID, "name": s.toolName[idx], "input": args,
					},
				})
				continue
			}
			emit("response.function_call_arguments.done", map[string]any{
				"item_id": "fc_" + callID, "output_index": idx, "arguments": args,
			})
			emit("response.output_item.done", map[string]any{
				"output_index": idx,
				"item": map[string]any{
					"id": "fc_" + callID, "type": "function_call",
					"call_id": callID, "name": s.toolName[idx], "arguments": args,
				},
			})
		}

	case core.ChunkUsage:
		// Anthropic reports input tokens at message_start and Gemini on every
		// frame, so usage is merged and sent once with response.completed.
		if chunk.Usage != nil {
			mergeStreamUsage(&s.usage, *chunk.Usage)
		}
		return nil, nil

	default:
		return nil, nil
	}
	return events, nil
}

// RenderStreamDone ends the response with response.completed, opening it
// first when the upstream produced no output at all.
func (OpenAIResponsesCodec) RenderStreamDone(state *StreamState) [][]byte {
	s := respState(state)
	if s.completed {
		return nil
	}
	s.completed = true
	var events [][]byte
	if !s.started {
		s.started = true
		events = append(events, s.event("response.created", map[string]any{
			"response": map[string]any{"id": s.responseID, "object": "response", "status": "in_progress", "output": []any{}},
		}))
	}
	resp := map[string]any{"id": s.responseID, "object": "response", "status": "completed"}
	if s.usage != (core.Usage{}) {
		resp["usage"] = map[string]int{
			"input_tokens":  s.usage.PromptTokens,
			"output_tokens": s.usage.CompletionTokens,
			"total_tokens":  s.usage.PromptTokens + s.usage.CompletionTokens,
		}
	}
	return append(events, s.event("response.completed", map[string]any{"response": resp}))
}

// RenderStreamError ends the response with response.failed. A nil state (the
// zero-copy path) renders it without a response id or sequence number.
func (OpenAIResponsesCodec) RenderStreamError(state *StreamState, message string) [][]byte {
	failed := map[string]any{"code": "server_error", "message": message}
	if state == nil {
		return [][]byte{respEvent("response.failed", map[string]any{
			"type":     "response.failed",
			"response": map[string]any{"object": "response", "status": "failed", "error": failed},
		})}
	}
	s := respState(state)
	if s.completed {
		return nil
	}
	s.completed = true
	return [][]byte{s.event("response.failed", map[string]any{
		"response": map[string]any{"id": s.responseID, "object": "response", "status": "failed", "error": failed},
	})}
}

func (s *respStreamState) event(name string, data map[string]any) []byte {
	s.seq++
	data["sequence_number"] = s.seq
	data["type"] = name
	return respEvent(name, data)
}

// mergeStreamUsage folds a usage report into the running total. Providers
// report usage incrementally or cumulatively, so each non-zero field wins.
func mergeStreamUsage(total *core.Usage, u core.Usage) {
	if u.PromptTokens > 0 {
		total.PromptTokens = u.PromptTokens
	}
	if u.CompletionTokens > 0 {
		total.CompletionTokens = u.CompletionTokens
	}
	if u.CachedTokens > 0 {
		total.CachedTokens = u.CachedTokens
	}
	if u.ReasoningTokens > 0 {
		total.ReasoningTokens = u.ReasoningTokens
	}
}

// respEvent formats a Responses API SSE event: "event: <name>\ndata: <json>\n\n".
func respEvent(name string, payload map[string]any) []byte {
	b, _ := json.Marshal(payload)
	out := make([]byte, 0, len(name)+len(b)+20)
	out = append(out, "event: "...)
	out = append(out, name...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, b...)
	out = append(out, '\n', '\n')
	return out
}

// toolItemType is the Responses item type for the tool call at idx.
func (s *respStreamState) toolItemType(idx int) string {
	if s.toolCustom[idx] {
		return "custom_tool_call"
	}
	return "function_call"
}

// toolDeltaEvent is the argument/input delta event name for the call at idx.
func (s *respStreamState) toolDeltaEvent(idx int) string {
	if s.toolCustom[idx] {
		return "response.custom_tool_call_input.delta"
	}
	return "response.function_call_arguments.delta"
}
