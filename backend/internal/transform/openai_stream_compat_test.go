package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

type oaiTestEvent struct {
	Created int64 `json:"created"`
	Choices []struct {
		Delta struct {
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// renderOAI feeds chunks through the codec the way the gateway does and
// returns every decoded event plus whether [DONE] terminated the stream.
func renderOAI(t *testing.T, chunks ...core.StreamChunk) ([]oaiTestEvent, bool) {
	t.Helper()
	state := &StreamState{Model: "m"}
	ResetStreamState(state)
	var frames [][]byte
	for _, c := range chunks {
		evs, err := OpenAICodec{}.RenderStreamChunk(c, state)
		require.NoError(t, err)
		frames = append(frames, evs...)
	}
	frames = append(frames, OpenAICodec{}.RenderStreamDone(state)...)
	var out []oaiTestEvent
	done := false
	for _, f := range frames {
		line := strings.TrimSpace(string(f))
		require.True(t, strings.HasPrefix(line, "data: "), line)
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			done = true
			continue
		}
		require.False(t, done, "event after [DONE]")
		var ev oaiTestEvent
		require.NoError(t, json.Unmarshal([]byte(payload), &ev))
		out = append(out, ev)
	}
	return out, done
}

func usageChunk(prompt, completion, cached int) core.StreamChunk {
	return core.StreamChunk{Type: core.ChunkUsage, Usage: &core.Usage{PromptTokens: prompt, CompletionTokens: completion, CachedTokens: cached}}
}

func TestOpenAIStream_PerChunkUsageCoalescedIntoOneFinalEvent(t *testing.T) {
	events, done := renderOAI(t,
		core.StreamChunk{Type: core.ChunkText, Delta: "he"},
		usageChunk(0, 7, 0),
		core.StreamChunk{Type: core.ChunkText, Delta: "llo"},
		usageChunk(0, 0, 0),
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
		usageChunk(10824, 32, 10752),
	)
	require.True(t, done)

	var usageEvents []oaiTestEvent
	for _, ev := range events {
		if ev.Usage != nil {
			usageEvents = append(usageEvents, ev)
		}
	}
	require.Len(t, usageEvents, 1, "upstream usage on every chunk must reach the client once")
	last := events[len(events)-1]
	require.NotNil(t, last.Usage, "the usage event must be the last one before [DONE]")
	require.Empty(t, last.Choices)
	require.Equal(t, 10824, last.Usage.PromptTokens)
	require.Equal(t, 32, last.Usage.CompletionTokens)
	require.Equal(t, 10856, last.Usage.TotalTokens)
	require.NotNil(t, last.Usage.PromptTokensDetails)
	require.Equal(t, 10752, last.Usage.PromptTokensDetails.CachedTokens)
}

func TestOpenAIStream_InterimZeroUsageDoesNotEraseSettledValues(t *testing.T) {
	events, _ := renderOAI(t,
		core.StreamChunk{Type: core.ChunkText, Delta: "x"},
		usageChunk(500, 3, 400),
		usageChunk(0, 9, 0),
	)
	last := events[len(events)-1]
	require.NotNil(t, last.Usage)
	require.Equal(t, 500, last.Usage.PromptTokens)
	require.Equal(t, 9, last.Usage.CompletionTokens)
	require.Equal(t, 400, last.Usage.PromptTokensDetails.CachedTokens)
}

func TestOpenAIStream_NoUsageMeansNoUsageEvent(t *testing.T) {
	events, done := renderOAI(t,
		core.StreamChunk{Type: core.ChunkText, Delta: "x"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	)
	require.True(t, done)
	for _, ev := range events {
		require.Nil(t, ev.Usage)
	}
}

func finishReasons(events []oaiTestEvent) []string {
	var out []string
	for _, ev := range events {
		for _, c := range ev.Choices {
			if c.FinishReason != nil {
				out = append(out, *c.FinishReason)
			}
		}
	}
	return out
}

func TestOpenAIStream_RepeatedFinishAfterToolCallStaysToolCalls(t *testing.T) {
	// Recorded from Cloudflare DeepSeek with a named tool_choice: one tool call,
	// then finish_reason "stop" on three trailing frames.
	events, _ := renderOAI(t,
		core.StreamChunk{Type: core.ChunkThinking, Delta: "call the tool"},
		core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: "chatcmpl-tool-1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)}},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	)
	require.Equal(t, []string{"tool_calls"}, finishReasons(events))
}

func TestOpenAIStream_FirstFinishWinsWithoutTools(t *testing.T) {
	events, _ := renderOAI(t,
		core.StreamChunk{Type: core.ChunkText, Delta: "x"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishLength},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	)
	require.Equal(t, []string{"length"}, finishReasons(events))
}

func TestOpenAIStream_LengthAfterToolCallIsKept(t *testing.T) {
	events, _ := renderOAI(t,
		core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "f", Arguments: json.RawMessage(`{"a":`)}},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishLength},
	)
	require.Equal(t, []string{"length"}, finishReasons(events), "a truncated tool call must still read as truncated")
}

func TestOpenAIStream_EveryEventCarriesTheSameCreated(t *testing.T) {
	events, _ := renderOAI(t,
		core.StreamChunk{Type: core.ChunkText, Delta: "a"},
		core.StreamChunk{Type: core.ChunkText, Delta: "b"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
		usageChunk(5, 2, 0),
	)
	require.NotEmpty(t, events)
	created := events[0].Created
	require.Positive(t, created)
	for _, ev := range events {
		require.Equal(t, created, ev.Created)
	}
}

func TestResetStreamState_ClearsOpenAIBookkeeping(t *testing.T) {
	state := &StreamState{}
	ResetStreamState(state)
	_, _ = OpenAICodec{}.RenderStreamChunk(core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: "c", Name: "f"}}, state)
	_, _ = OpenAICodec{}.RenderStreamChunk(core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop}, state)
	_, _ = OpenAICodec{}.RenderStreamChunk(usageChunk(1, 1, 0), state)

	ResetStreamState(state)

	require.Zero(t, state.oaiCreated)
	require.False(t, state.oaiFinishSent)
	require.False(t, state.oaiToolSeen)
	require.False(t, state.oaiHasUsage)
	require.Equal(t, core.Usage{}, state.oaiUsage)
}

func TestOpenAIRenderResponse_HasCreated(t *testing.T) {
	out, err := OpenAICodec{}.RenderResponse(&core.ChatResponse{ID: "x", Model: "m", FinishReason: core.FinishStop,
		Message: core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}})
	require.NoError(t, err)
	var got struct {
		Created int64 `json:"created"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	require.Positive(t, got.Created)
}
