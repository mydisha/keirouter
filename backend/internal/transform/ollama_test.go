package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestOllama_ParseRequest_Basic(t *testing.T) {
	body := []byte(`{
		"model": "llama3.1",
		"stream": true,
		"messages": [
			{"role": "system", "content": "be helpful"},
			{"role": "user", "content": "hello"}
		],
		"options": {"temperature": 0.5, "num_predict": 128, "top_p": 0.9}
	}`)

	req, err := OllamaCodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Equal(t, "llama3.1", req.Model)
	require.True(t, req.Stream)
	require.Equal(t, "be helpful", req.System)
	require.Len(t, req.Messages, 1)
	require.Equal(t, "hello", req.Messages[0].TextContent())
	require.NotNil(t, req.Temperature)
	require.InDelta(t, 0.5, *req.Temperature, 1e-9)
	require.NotNil(t, req.MaxTokens)
	require.Equal(t, 128, *req.MaxTokens)
	require.NotNil(t, req.TopP)
}

func TestOllama_RenderRequest_OptionsAndToolResult(t *testing.T) {
	maxTok := 64
	temp := 0.2
	req := &core.ChatRequest{
		Model:       "llama3.1",
		System:      "sys",
		MaxTokens:   &maxTok,
		Temperature: &temp,
		Messages: []core.Message{
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "c1", Content: "sunny"}},
			}},
		},
	}
	body, err := OllamaCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed ollamaRequest
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Equal(t, "llama3.1", parsed.Model)
	require.NotNil(t, parsed.Options)
	require.Equal(t, 64, *parsed.Options.NumPredict)

	// system + assistant(tool_call) + tool(result)
	require.Len(t, parsed.Messages, 3)
	require.Equal(t, "system", parsed.Messages[0].Role)
	require.Equal(t, "assistant", parsed.Messages[1].Role)
	require.Len(t, parsed.Messages[1].ToolCalls, 1)
	require.Equal(t, "get_weather", parsed.Messages[1].ToolCalls[0].Function.Name)

	// Tool result carries the resolved tool_name from the id->name map.
	require.Equal(t, "tool", parsed.Messages[2].Role)
	require.Equal(t, "get_weather", parsed.Messages[2].ToolName)
	require.Equal(t, "sunny", parsed.Messages[2].Content)
}

func TestOllama_ParseResponse_Unary(t *testing.T) {
	body := []byte(`{
		"model": "llama3.1",
		"message": {"role": "assistant", "content": "the answer"},
		"done": true,
		"done_reason": "stop",
		"prompt_eval_count": 10,
		"eval_count": 5
	}`)
	resp, err := OllamaCodec{}.ParseResponse(body, "llama3.1")
	require.NoError(t, err)
	require.Equal(t, "the answer", resp.Message.TextContent())
	require.Equal(t, core.FinishStop, resp.FinishReason)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestOllama_ParseStreamLine_NDJSON(t *testing.T) {
	// Content fragment (no data: prefix — bare NDJSON).
	line := []byte(`{"model":"llama3.1","message":{"role":"assistant","content":"hel"},"done":false}`)
	chunks, err := OllamaCodec{}.ParseStreamLine(line, "llama3.1")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkText, chunks[0].Type)
	require.Equal(t, "hel", chunks[0].Delta)

	// Final fragment emits finish + usage.
	final := []byte(`{"model":"llama3.1","done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":3}`)
	chunks, err = OllamaCodec{}.ParseStreamLine(final, "llama3.1")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 2)
	require.Equal(t, core.ChunkFinish, chunks[0].Type)
	require.Equal(t, core.ChunkUsage, chunks[1].Type)
	require.Equal(t, 10, chunks[1].Usage.TotalTokens)
}

func TestOllama_RenderStream_NDJSONShape(t *testing.T) {
	state := &StreamState{Model: "llama3.1"}
	out, err := OllamaCodec{}.RenderStreamChunk(core.StreamChunk{Type: core.ChunkText, Delta: "hi"}, state)
	require.NoError(t, err)
	require.Len(t, out, 1)
	// NDJSON line: no "data:" prefix, ends with a single newline.
	s := string(out[0])
	require.False(t, strings.HasPrefix(s, "data:"))
	require.True(t, strings.HasSuffix(s, "\n"))
	require.Contains(t, s, `"content":"hi"`)
	require.Contains(t, s, `"done":false`)

	// Usage chunk stashes counts, RenderStreamDone emits the terminal line.
	_, err = OllamaCodec{}.RenderStreamChunk(core.StreamChunk{
		Type: core.ChunkUsage, Usage: &core.Usage{PromptTokens: 4, CompletionTokens: 2},
	}, state)
	require.NoError(t, err)
	done := OllamaCodec{}.RenderStreamDone(state)
	require.Len(t, done, 1)
	require.Contains(t, string(done[0]), `"done":true`)
	require.Contains(t, string(done[0]), `"prompt_eval_count":4`)
}

func TestCrossDialect_OpenAIToOllamaRoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "llama3.1",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "ping"}
		],
		"max_tokens": 32
	}`)
	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	ollamaBody, err := OllamaCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	back, err := OllamaCodec{}.ParseRequest(ollamaBody)
	require.NoError(t, err)
	require.Equal(t, "be terse", back.System)
	require.Len(t, back.Messages, 1)
	require.Equal(t, "ping", back.Messages[0].TextContent())
}
