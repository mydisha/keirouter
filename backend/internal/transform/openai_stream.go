package transform

import (
	"bytes"
	"fmt"
	"time"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// oaiStreamChunk is one SSE "data:" payload from an OpenAI streaming response.
type oaiStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			// ReasoningContent carries thinking/reasoning text from models
			// that expose it as a structured field (DeepSeek, some MiMo
			// versions). The JSON field name varies by provider.
			ReasoningContent string `json:"reasoning_content"`
			// Reasoning is the field name OpenRouter, Groq and vLLM use.
			Reasoning string `json:"reasoning"`
			// ReasoningDetails is MiniMax's split reasoning (reasoning_split).
			ReasoningDetails []oaiReasoningDetail `json:"reasoning_details"`
			ToolCalls        []oaiStreamToolCall  `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage"`
}

type oaiStreamToolCall struct {
	Index     int             `json:"index"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	Params    json.RawMessage `json:"parameters"`
	Args      json.RawMessage `json:"args"`
	Function  struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
		Params    json.RawMessage `json:"parameters"`
		Args      json.RawMessage `json:"args"`
	} `json:"function"`
}

// ParseStreamLine decodes one upstream SSE data payload into canonical chunks.
// The caller strips the "data: " prefix before calling; the special "[DONE]"
// sentinel is handled here and yields no chunks.
func (OpenAICodec) ParseStreamLine(line []byte, model string) ([]core.StreamChunk, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
		return nil, nil
	}

	// {"error":{...}} inside an HTTP 200 stream (OpenAI-compatible gateways,
	// OpenRouter, vLLM) carries no choices and would otherwise be dropped as
	// an empty frame, turning a failed completion into a silent success.
	if pe, ok := errclass.StreamErrorFrame(line); ok {
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}

	var raw oaiStreamChunk
	if err := json.UnmarshalNoCopy(line, &raw); err != nil {
		return nil, fmt.Errorf("openai: parse stream chunk: %w", err)
	}

	var chunks []core.StreamChunk
	if len(raw.Choices) > 0 {
		c := raw.Choices[0]
		// Structured reasoning_content field (DeepSeek, some MiMo).
		if reasoning := firstNonEmpty(c.Delta.ReasoningContent, c.Delta.Reasoning, reasoningDetailsText(c.Delta.ReasoningDetails)); reasoning != "" {
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkThinking, Delta: reasoning})
		}
		if c.Delta.Content != "" {
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkText, Delta: c.Delta.Content})
		}
		for _, tc := range c.Delta.ToolCalls {
			chunks = append(chunks, core.StreamChunk{
				Type:  core.ChunkToolCall,
				Index: tc.Index,
				ToolCall: &core.ToolCall{
					ID:        tc.ID,
					Name:      firstNonEmpty(tc.Function.Name, tc.Name),
					Arguments: extractOpenAIStreamToolArguments(tc),
				},
			})
		}
		if c.FinishReason != nil {
			chunks = append(chunks, core.StreamChunk{
				Type:         core.ChunkFinish,
				FinishReason: mapOAIFinish(*c.FinishReason),
			})
		}
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
		chunks = append(chunks, core.StreamChunk{
			Type: core.ChunkUsage,
			Usage: &core.Usage{
				PromptTokens:      raw.Usage.PromptTokens,
				CompletionTokens:  raw.Usage.CompletionTokens,
				TotalTokens:       raw.Usage.TotalTokens,
				CachedTokens:      cached,
				ReasoningTokens:   reasoning,
				ProviderCostNanos: usdToNanos(raw.Usage.Cost),
				Source:            core.UsageSourceProvider,
			},
		})
	}
	return chunks, nil
}

func extractOpenAIStreamToolArguments(tc oaiStreamToolCall) json.RawMessage {
	for _, raw := range []json.RawMessage{
		tc.Function.Arguments,
		tc.Function.Input,
		tc.Function.Params,
		tc.Function.Args,
		tc.Arguments,
		tc.Input,
		tc.Params,
		tc.Args,
	} {
		if normalized := normalizeOpenAIToolArguments(raw); len(normalized) > 0 {
			return normalized
		}
	}
	return nil
}

// normalizeOpenAIToolArguments decodes a JSON-string argument (often a
// fragment) and passes anything else through; arguments are never reshaped.
func normalizeOpenAIToolArguments(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil
		}
		return json.RawMessage(s)
	}
	if json.Valid(raw) {
		return raw
	}
	return nil
}

// Typed wire shapes for streamed chat.completion.chunk events.
type oaiChunkEvent struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []oaiChunkChoice `json:"choices"`
	Usage   *oaiUsageOut     `json:"usage,omitempty"`
}

type oaiChunkChoice struct {
	Index        int           `json:"index"`
	Delta        oaiChunkDelta `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

type oaiChunkDelta struct {
	Role             string             `json:"role,omitempty"`
	Content          *string            `json:"content,omitempty"`
	ReasoningContent *string            `json:"reasoning_content,omitempty"`
	ToolCalls        []oaiChunkToolCall `json:"tool_calls,omitempty"`
}

type oaiChunkToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type"`
	Function oaiChunkFunction `json:"function"`
}

type oaiChunkFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// oaiUsageOut is OpenAI's usage object with the optional detail blocks.
type oaiUsageOut struct {
	PromptTokens            int                `json:"prompt_tokens"`
	CompletionTokens        int                `json:"completion_tokens"`
	TotalTokens             int                `json:"total_tokens"`
	PromptTokensDetails     *oaiPromptDetails  `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *oaiCompletDetails `json:"completion_tokens_details,omitempty"`
}

type oaiPromptDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type oaiCompletDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

func oaiUsageFromCore(u core.Usage) *oaiUsageOut {
	out := &oaiUsageOut{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	if u.CachedTokens > 0 {
		out.PromptTokensDetails = &oaiPromptDetails{CachedTokens: u.CachedTokens}
	}
	if u.ReasoningTokens > 0 {
		out.CompletionTokensDetails = &oaiCompletDetails{ReasoningTokens: u.ReasoningTokens}
	}
	return out
}

// RenderStreamChunk encodes a canonical chunk as an OpenAI SSE event. The first
// emitted event carries the assistant role per the OpenAI contract.
func (OpenAICodec) RenderStreamChunk(chunk core.StreamChunk, state *StreamState) ([][]byte, error) {
	var delta oaiChunkDelta
	role := func() {
		if !state.SentRole {
			delta.Role = "assistant"
			state.SentRole = true
		}
	}
	switch chunk.Type {
	case core.ChunkThinking:
		// Echo structured reasoning back to the client as reasoning_content so
		// clients that replay it on follow-up turns (Cursor, Cline, etc.) keep
		// the real reasoning. DeepSeek/MiniMax thinking mode requires this
		// field on subsequent turns or it returns a 400.
		if chunk.Delta == "" {
			return nil, nil
		}
		role()
		delta.ReasoningContent = &chunk.Delta
	case core.ChunkText:
		role()
		delta.Content = &chunk.Delta

	case core.ChunkToolCall:
		role()
		args := string(chunk.ToolCall.Arguments)
		if args == "" {
			args = "{}"
		}
		state.oaiToolSeen = true
		delta.ToolCalls = []oaiChunkToolCall{{
			Index:    openAIToolIndex(state, chunk),
			ID:       chunk.ToolCall.ID,
			Type:     "function",
			Function: oaiChunkFunction{Name: chunk.ToolCall.Name, Arguments: args},
		}}
	case core.ChunkFinish:
		// One finish per choice: upstreams that repeat finish_reason (vLLM
		// behind Cloudflare sends it on trailing frames too) would otherwise
		// let a late "stop" overwrite the "tool_calls" clients act on.
		if state.oaiFinishSent {
			return nil, nil
		}
		state.oaiFinishSent = true
		reason := chunk.FinishReason
		if reason == core.FinishStop && state.oaiToolSeen {
			reason = core.FinishToolCalls
		}
		finish := string(reason)
		return [][]byte{encodeOAIEvent(state, oaiChunkDelta{}, &finish)}, nil
	case core.ChunkUsage:
		// Held until RenderStreamDone: OpenAI sends usage once, as the final
		// empty-choices event, even when the upstream reports it per chunk.
		if chunk.Usage != nil {
			mergeStreamUsage(&state.oaiUsage, *chunk.Usage)
			state.oaiHasUsage = true
		}
		return nil, nil
	default:
		return nil, nil
	}
	return [][]byte{encodeOAIEvent(state, delta, nil)}, nil
}

// RenderStreamDone emits the accumulated usage event, then the OpenAI terminal
// sentinel.
func (OpenAICodec) RenderStreamDone(state *StreamState) [][]byte {
	done := []byte("data: [DONE]\n\n")
	if state == nil || !state.oaiHasUsage {
		return [][]byte{done}
	}
	state.oaiHasUsage = false
	usage := state.oaiUsage
	if sum := usage.PromptTokens + usage.CompletionTokens; usage.TotalTokens < sum {
		usage.TotalTokens = sum
	}
	return [][]byte{encodeOAIUsageEvent(state, &usage), done}
}

// oaiCreatedAt returns the stream's creation time, fixing it on first use.
func oaiCreatedAt(state *StreamState) int64 {
	if state.oaiCreated == 0 {
		state.oaiCreated = time.Now().Unix()
	}
	return state.oaiCreated
}

func encodeOAIEvent(state *StreamState, delta oaiChunkDelta, finish *string) []byte {
	return sseData(&oaiChunkEvent{
		ID:      firstNonEmpty(state.MessageID, "chatcmpl-stream"),
		Object:  "chat.completion.chunk",
		Created: oaiCreatedAt(state),
		Model:   state.Model,
		Choices: []oaiChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	})
}

func encodeOAIUsageEvent(state *StreamState, usage *core.Usage) []byte {
	if usage == nil {
		return nil
	}
	return sseData(&oaiChunkEvent{
		ID:      firstNonEmpty(state.MessageID, "chatcmpl-stream"),
		Object:  "chat.completion.chunk",
		Created: oaiCreatedAt(state),
		Model:   state.Model,
		Choices: []oaiChunkChoice{},
		Usage:   oaiUsageFromCore(*usage),
	})
}

func ptr[T any](v T) *T { return &v }

// openAIToolIndex numbers tool calls from 0 in opening order; the upstream
// index also counts text and thinking blocks (Anthropic, Responses).
func openAIToolIndex(state *StreamState, chunk core.StreamChunk) int {
	if chunk.ToolCall.ID == "" {
		for i := len(state.oaiToolUpstream) - 1; i >= 0; i-- {
			if state.oaiToolUpstream[i] == chunk.Index {
				return i
			}
		}
	}
	if state.oaiToolUpstream == nil {
		state.oaiToolUpstream = state.oaiToolBuf[:0]
	}
	state.oaiToolUpstream = append(state.oaiToolUpstream, chunk.Index)
	return len(state.oaiToolUpstream) - 1
}
