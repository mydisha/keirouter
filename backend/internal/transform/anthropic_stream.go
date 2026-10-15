package transform

import (
	"bytes"
	"fmt"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// Anthropic streams a sequence of typed SSE events rather than uniform chunks:
//
//	message_start → content_block_start → content_block_delta* →
//	content_block_stop → message_delta → message_stop
//
// ParseStreamLine maps the data payload of each event to canonical chunks, and
// RenderStreamChunk produces the corresponding event sequence for a client that
// speaks Anthropic.

type antStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
		Signature   string `json:"signature"`
	} `json:"delta"`
	ContentBlock struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
	} `json:"content_block"`
	Usage   *antUsage `json:"usage"`
	Message *struct {
		Usage antUsage `json:"usage"`
	} `json:"message"`
}

// ParseStreamLine converts one Anthropic SSE data payload into canonical chunks.
func (AnthropicCodec) ParseStreamLine(line []byte, _ string) ([]core.StreamChunk, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}

	// {"type":"error","error":{"type":"overloaded_error",...}} arrives inside
	// an HTTP 200 stream. Surface it so the pipeline records a failure and
	// the client sees an error instead of a clean, truncated message_stop.
	if pe, ok := errclass.StreamErrorFrame(line); ok {
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}

	var ev antStreamEvent
	if err := json.UnmarshalNoCopy(line, &ev); err != nil {
		return nil, fmt.Errorf("anthropic: parse stream event: %w", err)
	}

	switch ev.Type {
	case "content_block_start":
		switch ev.ContentBlock.Type {
		case "tool_use", "server_tool_use":
			return []core.StreamChunk{{
				Type:  core.ChunkToolCall,
				Index: ev.Index,
				ToolCall: &core.ToolCall{
					ID:        ev.ContentBlock.ID,
					Name:      ev.ContentBlock.Name,
					Arguments: json.RawMessage("{}"),
				},
			}}, nil
		case "redacted_thinking":
			// Opaque encrypted reasoning: the client must echo it verbatim on
			// the next turn, so it has to reach the client as its own block.
			return []core.StreamChunk{{Type: core.ChunkRedactedThinking, Delta: ev.ContentBlock.Data}}, nil
		case "thinking":
			// Non-streamed thinking (rare): content arrives on the start event.
			if ev.ContentBlock.Thinking != "" || ev.ContentBlock.Signature != "" {
				return []core.StreamChunk{{Type: core.ChunkThinking, Delta: ev.ContentBlock.Thinking, Signature: ev.ContentBlock.Signature}}, nil
			}
		}
		return nil, nil

	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			return []core.StreamChunk{{Type: core.ChunkText, Delta: ev.Delta.Text}}, nil
		case "thinking_delta":
			return []core.StreamChunk{{Type: core.ChunkThinking, Delta: ev.Delta.Thinking}}, nil
		case "signature_delta":
			// The signature proves the thinking block came from this model;
			// Claude Code must send it back with the block on the next turn
			// or Anthropic rejects the request.
			return []core.StreamChunk{{Type: core.ChunkThinking, Signature: ev.Delta.Signature}}, nil
		case "input_json_delta":
			return []core.StreamChunk{{
				Type:     core.ChunkToolCall,
				Index:    ev.Index,
				ToolCall: &core.ToolCall{Arguments: json.RawMessage(ev.Delta.PartialJSON)},
			}}, nil
		}
		return nil, nil

	case "message_delta":
		var chunks []core.StreamChunk
		if ev.Delta.StopReason != "" {
			chunks = append(chunks, core.StreamChunk{
				Type:         core.ChunkFinish,
				FinishReason: mapAntStop(ev.Delta.StopReason),
			})
		}
		if ev.Usage != nil {
			u := antUsageToCore(*ev.Usage)
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkUsage, Usage: &u})
		}
		return chunks, nil

	case "message_start":
		if ev.Message != nil {
			u := antUsageToCore(ev.Message.Usage)
			return []core.StreamChunk{{Type: core.ChunkUsage, Usage: &u}}, nil
		}
		return nil, nil

	default:
		// message_stop, content_block_stop, ping: nothing canonical to emit.
		return nil, nil
	}
}

// Typed wire shapes for Anthropic stream events.
type antEventOut struct {
	Type         string       `json:"type"`
	Index        *int         `json:"index,omitempty"`
	Message      *antMsgStart `json:"message,omitempty"`
	ContentBlock *antBlockOut `json:"content_block,omitempty"`
	Delta        *antDeltaOut `json:"delta,omitempty"`
	Usage        *antUsageOut `json:"usage,omitempty"`
}

type antMsgStart struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Role       string      `json:"role"`
	Model      string      `json:"model"`
	Content    []any       `json:"content"`
	StopReason *string     `json:"stop_reason"`
	Usage      antUsageOut `json:"usage"`
}

type antBlockOut struct {
	Type     string          `json:"type"`
	Text     *string         `json:"text,omitempty"`
	Thinking *string         `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Data     string          `json:"data,omitempty"`
}

type antDeltaOut struct {
	Type        string  `json:"type,omitempty"`
	Text        *string `json:"text,omitempty"`
	Thinking    *string `json:"thinking,omitempty"`
	PartialJSON *string `json:"partial_json,omitempty"`
	Signature   string  `json:"signature,omitempty"`
	StopReason  string  `json:"stop_reason,omitempty"`
}

type antUsageOut struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

func antUsageFromCore(u core.Usage) antUsageOut {
	input := u.PromptTokens - u.CachedTokens - u.CacheWriteTokens
	if input < 0 {
		input = u.PromptTokens
	}
	return antUsageOut{
		InputTokens:              input,
		OutputTokens:             u.CompletionTokens,
		CacheReadInputTokens:     u.CachedTokens,
		CacheCreationInputTokens: u.CacheWriteTokens,
	}
}

func antBlockStop(idx int) []byte {
	return sseNamed("content_block_stop", &antEventOut{Type: "content_block_stop", Index: &idx})
}

func antBlockStart(idx int, block antBlockOut) []byte {
	return sseNamed("content_block_start", &antEventOut{Type: "content_block_start", Index: &idx, ContentBlock: &block})
}

func antBlockDelta(idx int, delta antDeltaOut) []byte {
	return sseNamed("content_block_delta", &antEventOut{Type: "content_block_delta", Index: &idx, Delta: &delta})
}

var emptyString = ""

// RenderStreamChunk emits Anthropic event(s) for a canonical chunk. It lazily
// opens the message and a text content block on first text delta.
func (AnthropicCodec) RenderStreamChunk(chunk core.StreamChunk, state *StreamState) ([][]byte, error) {
	var events [][]byte
	if state.Custom == nil {
		state.Custom = make(map[string]any)
	}

	ensureOpen := func() {
		if state.SentRole {
			return
		}
		state.SentRole = true
		var startUsage antUsageOut
		// An Anthropic upstream reports input tokens in its own message_start,
		// which arrives before any content: pass them through so the client
		// sees real prompt accounting instead of zeros.
		if u, ok := state.Custom["usage"].(core.Usage); ok {
			startUsage = antUsageFromCore(u)
			startUsage.OutputTokens = 0
		}
		events = append(events, sseNamed("message_start", &antEventOut{
			Type: "message_start",
			Message: &antMsgStart{
				ID: firstNonEmpty(state.MessageID, "msg_stream"), Type: "message",
				Role: "assistant", Model: state.Model, Content: []any{},
				StopReason: nil, Usage: startUsage,
			},
		}))
	}
	closeThinking := func() {
		if thinkOpen, _ := state.Custom["thinking_open"].(bool); thinkOpen {
			thinkIdx, _ := state.Custom["thinking_index"].(int)
			events = append(events, antBlockStop(thinkIdx))
			state.Custom["thinking_open"] = false
		}
	}
	closeText := func() {
		if state.OpenedBlock {
			textIdx, _ := state.Custom["text_index"].(int)
			events = append(events, antBlockStop(textIdx))
			state.OpenedBlock = false
		}
	}
	closeTool := func() {
		if toolOpen, _ := state.Custom["tool_open"].(bool); toolOpen {
			events = append(events, antBlockStop(state.ToolIndex))
			state.Custom["tool_open"] = false
		}
	}

	switch chunk.Type {
	case core.ChunkText:
		ensureOpen()
		// Close any open thinking block before starting/continuing text.
		// Thinking and text are separate content blocks with distinct indices;
		// leaving a thinking block open while emitting text_delta corrupts the
		// block index sequence and makes some clients (Claude Code) render the
		// thinking as a second message segment.
		closeThinking()
		// Close any open tool block before starting/continuing text.
		closeTool()
		if !state.OpenedBlock {
			state.OpenedBlock = true
			// The text block opens at the next index after any thinking block
			// that already opened (e.g. MiMo streams reasoning_content first).
			state.Custom["text_index"] = nextContentIndex(state)
			textIdx, _ := state.Custom["text_index"].(int)
			events = append(events, antBlockStart(textIdx, antBlockOut{Type: "text", Text: &emptyString}))
		}
		textIdx, _ := state.Custom["text_index"].(int)
		events = append(events, antBlockDelta(textIdx, antDeltaOut{Type: "text_delta", Text: &chunk.Delta}))

	case core.ChunkThinking:
		ensureOpen()
		// Anthropic streams reasoning as its own typed content block:
		// content_block_start(type=thinking) → thinking_delta* →
		// content_block_stop. Emitting thinking_delta without opening a
		// thinking block (and reusing the text block's index) produces a
		// non-compliant stream that some clients (Claude Code) mis-parse as a
		// second message segment/turn.
		if thinkOpen, _ := state.Custom["thinking_open"].(bool); !thinkOpen {
			// A text block may already be open (text arrived before thinking);
			// close it first so the thinking block gets its own index.
			closeText()
			state.Custom["thinking_open"] = true
			state.Custom["thinking_index"] = nextContentIndex(state)
			thinkIdx, _ := state.Custom["thinking_index"].(int)
			events = append(events, antBlockStart(thinkIdx, antBlockOut{Type: "thinking", Thinking: &emptyString}))
		}
		thinkIdx, _ := state.Custom["thinking_index"].(int)
		if chunk.Delta != "" {
			events = append(events, antBlockDelta(thinkIdx, antDeltaOut{Type: "thinking_delta", Thinking: &chunk.Delta}))
		}
		if chunk.Signature != "" {
			events = append(events, antBlockDelta(thinkIdx, antDeltaOut{Type: "signature_delta", Signature: chunk.Signature}))
		}

	case core.ChunkRedactedThinking:
		ensureOpen()
		closeThinking()
		closeTool()
		closeText()
		idx := nextContentIndex(state)
		// Reserve the index by recording it as a (closed) thinking block.
		state.Custom["thinking_index"] = idx
		events = append(events, antBlockStart(idx, antBlockOut{Type: "redacted_thinking", Data: chunk.Delta}))
		events = append(events, antBlockStop(idx))

	case core.ChunkUsage:
		if chunk.Usage == nil {
			return nil, nil
		}
		// Merge rather than replace: an Anthropic upstream splits usage across
		// message_start (input) and message_delta (output).
		merged := *chunk.Usage
		if prev, ok := state.Custom["usage"].(core.Usage); ok {
			if merged.PromptTokens == 0 {
				merged.PromptTokens, merged.CachedTokens, merged.CacheWriteTokens = prev.PromptTokens, prev.CachedTokens, prev.CacheWriteTokens
			}
			if merged.CompletionTokens == 0 {
				merged.CompletionTokens = prev.CompletionTokens
			}
		}
		state.Custom["usage"] = merged

	case core.ChunkToolCall:
		ensureOpen()
		if chunk.ToolCall == nil {
			break
		}
		closeThinking()
		closeText()

		// First chunk for a tool call (carries ID and Name) — open a new
		// tool_use content block. Close any previously open tool block first.
		// Open a new tool_use block only when the ID actually changes. Some
		// upstreams (e.g. Kiro) repeat the same tool ID on the arguments
		// continuation chunk; treating a repeated ID as a new block would
		// close the real block with empty input and open a nameless duplicate.
		if chunk.ToolCall.ID != "" {
			openID, _ := state.Custom["tool_id"].(string)
			if openID != chunk.ToolCall.ID {
				closeTool()
				state.ToolIndex = nextContentIndex(state)
				state.Custom["tool_seen"] = true
				state.Custom["tool_open"] = true
				state.Custom["tool_id"] = chunk.ToolCall.ID
				events = append(events, antBlockStart(state.ToolIndex, antBlockOut{
					Type: "tool_use", ID: chunk.ToolCall.ID, Name: chunk.ToolCall.Name, Input: json.RawMessage("{}"),
				}))
			}
		}

		// Emit argument deltas (skip empty ones). Partial JSON fragments
		// from upstream streaming are not individually valid objects, so
		// they must not be normalized — the client reassembles them.
		if toolOpen, _ := state.Custom["tool_open"].(bool); toolOpen {
			args := string(chunk.ToolCall.Arguments)
			if args != "" && args != "{}" && args != "[]" {
				events = append(events, antBlockDelta(state.ToolIndex, antDeltaOut{Type: "input_json_delta", PartialJSON: &args}))
			}
		}

	case core.ChunkFinish:
		if sent, _ := state.Custom["finish_sent"].(bool); sent {
			return nil, nil
		}
		state.Custom["finish_sent"] = true
		ensureOpen()
		// Close any open thinking block, then any open tool block, then any
		// open text block — each with its own index — so every content_block_stop
		// matches a content_block_start.
		closeThinking()
		closeTool()
		closeText()
		// The terminal message_delta is emitted by RenderStreamDone so a usage
		// chunk that trails the finish (OpenAI upstreams send it last) still
		// lands in the single message_delta Anthropic clients expect; a second
		// one reads as a new turn to Claude Code.
		state.Custom["finish_reason"] = chunk.FinishReason

	default:
		return nil, nil
	}
	return events, nil
}

// nextContentIndex returns the next free content-block index, accounting for
// any thinking, text, or tool block already opened. Anthropic requires each
// content block to have a unique monotonically increasing index; thinking and
// text must not share an index or clients mis-parse the stream as two messages.
func nextContentIndex(state *StreamState) int {
	max := -1
	if idx, ok := state.Custom["thinking_index"].(int); ok && idx > max {
		max = idx
	}
	if idx, ok := state.Custom["text_index"].(int); ok && idx > max {
		max = idx
	}
	if seen, _ := state.Custom["tool_seen"].(bool); seen && state.ToolIndex > max {
		max = state.ToolIndex
	}
	return max + 1
}

// RenderStreamDone emits the terminal message_delta (once) and message_stop.
func (AnthropicCodec) RenderStreamDone(state *StreamState) [][]byte {
	var events [][]byte
	if state != nil && state.SentRole {
		// Exactly one message_delta per message, carrying the final stop
		// reason and whatever usage arrived (before or after the finish).
		if sent, _ := state.Custom["delta_sent"].(bool); !sent {
			state.Custom["delta_sent"] = true
			finish, _ := state.Custom["finish_reason"].(core.FinishReason)
			if finish == "" {
				finish = core.FinishStop
			}
			usage := antUsageOut{}
			if u, ok := state.Custom["usage"].(core.Usage); ok {
				usage = antUsageFromCore(u)
			}
			events = append(events, sseNamed("message_delta", &antEventOut{
				Type:  "message_delta",
				Delta: &antDeltaOut{StopReason: renderAntStop(finish)},
				Usage: &usage,
			}))
		}
	}
	return append(events, sseNamed("message_stop", &antEventOut{Type: "message_stop"}))
}
