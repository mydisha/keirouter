package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestResponses_ParseRequest_InputItems(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5-codex",
		"stream": true,
		"instructions": "be precise",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "sunny"}
		],
		"tools": [
			{"type": "function", "name": "get_weather", "description": "weather", "parameters": {"type": "object", "properties": {}}}
		]
	}`)

	req, err := OpenAIResponsesCodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5-codex", req.Model)
	require.True(t, req.Stream)
	require.Equal(t, "be precise", req.System)
	require.Len(t, req.Tools, 1)
	require.Equal(t, "get_weather", req.Tools[0].Name)

	// user message + assistant(tool_call) + tool(result)
	require.Len(t, req.Messages, 3)
	require.Equal(t, "hello", req.Messages[0].TextContent())
	require.Equal(t, core.RoleAssistant, req.Messages[1].Role)
	require.Equal(t, core.PartToolCall, req.Messages[1].Content[0].Type)
	require.Equal(t, "get_weather", req.Messages[1].Content[0].ToolCall.Name)
	require.Equal(t, core.RoleTool, req.Messages[2].Role)
	require.Equal(t, "sunny", req.Messages[2].Content[0].ToolResult.Content)
}

func TestResponses_ParseRequest_StringInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"just a prompt"}`)
	req, err := OpenAIResponsesCodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Len(t, req.Messages, 1)
	require.Equal(t, "just a prompt", req.Messages[0].TextContent())
}

func TestResponses_ReasoningWithEncryptedContent_RoundTrip(t *testing.T) {
	// Simulate a multi-turn Codex conversation where the client echoes back
	// a reasoning item with encrypted_content (as required by the Codex API
	// when include: ["reasoning.encrypted_content"] is set). This MUST survive
	// the parse→render round-trip without data loss.
	body := []byte(`{
		"model": "gpt-5-codex",
		"stream": true,
		"instructions": "be precise",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "what is 2+2?"}]},
			{"type": "reasoning", "encrypted_content": "encrypted_abc123", "summary": [{"type": "summary_text", "text": "thinking about math"}]},
			{"type": "function_call", "call_id": "call_1", "name": "calculator", "arguments": "{\"expr\":\"2+2\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "4"},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "The answer is 4."}]}
		]
	}`)

	req, err := OpenAIResponsesCodec{}.ParseRequest(body)
	require.NoError(t, err)

	// Verify reasoning item was parsed with both text and encrypted content.
	// The reasoning is attached to the function_call message (next assistant turn).
	var hasReasoning bool
	for _, m := range req.Messages {
		for _, p := range m.Content {
			if p.Type == core.PartThinking {
				hasReasoning = true
				require.Equal(t, "thinking about math", p.Text)
				require.Equal(t, "encrypted_abc123", p.Signature)
			}
		}
	}
	require.True(t, hasReasoning, "reasoning item with encrypted_content must be preserved")

	// Now render back to Responses API format.
	rendered, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	// Verify the rendered output contains the reasoning item with encrypted_content.
	var parsed struct {
		Input []struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
			Summary          []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
			CallID string `json:"call_id"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(rendered, &parsed))

	// Find the reasoning item in the rendered output.
	var foundReasoning bool
	var foundFunctionCall bool
	var foundFunctionCallOutput bool
	for _, item := range parsed.Input {
		switch item.Type {
		case "reasoning":
			foundReasoning = true
			require.Equal(t, "encrypted_abc123", item.EncryptedContent, "encrypted_content must survive round-trip")
			require.Len(t, item.Summary, 1)
			require.Equal(t, "thinking about math", item.Summary[0].Text)
		case "function_call":
			foundFunctionCall = true
			require.Equal(t, "call_1", item.CallID)
		case "function_call_output":
			foundFunctionCallOutput = true
			require.Equal(t, "call_1", item.CallID)
		}
	}
	require.True(t, foundReasoning, "rendered output must include reasoning item")
	require.True(t, foundFunctionCall, "rendered output must include function_call")
	require.True(t, foundFunctionCallOutput, "rendered output must include function_call_output")
}

func TestResponses_EncryptedContentOnly(t *testing.T) {
	// Codex may send reasoning items with ONLY encrypted_content and no summary.
	// These must still be preserved.
	body := []byte(`{
		"model": "gpt-5-codex",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"type": "reasoning", "encrypted_content": "enc_xyz"},
			{"type": "function_call", "call_id": "call_2", "name": "search", "arguments": "{\"q\":\"test\"}"},
			{"type": "function_call_output", "call_id": "call_2", "output": "results"}
		]
	}`)

	req, err := OpenAIResponsesCodec{}.ParseRequest(body)
	require.NoError(t, err)

	rendered, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed struct {
		Input []struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(rendered, &parsed))

	var found bool
	for _, item := range parsed.Input {
		if item.Type == "reasoning" {
			found = true
			require.Equal(t, "enc_xyz", item.EncryptedContent)
		}
	}
	require.True(t, found, "reasoning item with only encrypted_content must be preserved")
}

func TestResponses_RenderRequest_Shape(t *testing.T) {
	req := &core.ChatRequest{
		Model:  "gpt-5-codex",
		System: "sys",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "f", Arguments: json.RawMessage(`{}`)}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "c1", Content: "out"}},
			}},
		},
	}
	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed struct {
		Instructions string `json:"instructions"`
		Input        []struct {
			Type   string `json:"type"`
			Role   string `json:"role"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Equal(t, "sys", parsed.Instructions)
	require.Len(t, parsed.Input, 3)
	require.Equal(t, "message", parsed.Input[0].Type)
	require.Equal(t, "function_call", parsed.Input[1].Type)
	require.Equal(t, "function_call_output", parsed.Input[2].Type)
	require.Equal(t, "out", parsed.Input[2].Output)
}

func TestResponses_RenderRequest_AnthropicUserToolResult(t *testing.T) {
	req := &core.ChatRequest{
		Model: "gpt-5-codex",
		Messages: []core.Message{
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_Sdv5EdtiwN15K2yYb9q8pbXD", Name: "Read", Arguments: json.RawMessage(`{"file_path":"a.go"}`)}},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_Sdv5EdtiwN15K2yYb9q8pbXD", Content: "file body"}},
			}},
		},
	}

	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Input, 2)
	require.Equal(t, "function_call", parsed.Input[0].Type)
	require.Equal(t, "function_call_output", parsed.Input[1].Type)
	require.Equal(t, "call_Sdv5EdtiwN15K2yYb9q8pbXD", parsed.Input[1].CallID)
	require.Equal(t, "file body", parsed.Input[1].Output)
}

func TestResponses_ParseResponse_Unary(t *testing.T) {
	body := []byte(`{
		"id": "resp_123",
		"output": [
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "the answer"}]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "input_tokens_details": {"cached_tokens": 3}}
	}`)
	resp, err := OpenAIResponsesCodec{}.ParseResponse(body, "gpt-5-codex")
	require.NoError(t, err)
	require.Equal(t, "the answer", resp.Message.TextContent())
	require.Equal(t, 15, resp.Usage.TotalTokens)
	require.Equal(t, 3, resp.Usage.CachedTokens)
}

func TestResponses_ParseStreamLine_Events(t *testing.T) {
	codec := OpenAIResponsesCodec{}

	text := []byte(`{"type":"response.output_text.delta","delta":"hel"}`)
	chunks, err := codec.ParseStreamLine(text, "gpt-5")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkText, chunks[0].Type)
	require.Equal(t, "hel", chunks[0].Delta)

	fcAdded := []byte(`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"c1","name":"f"}}`)
	chunks, err = codec.ParseStreamLine(fcAdded, "gpt-5")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkToolCall, chunks[0].Type)
	require.Equal(t, "f", chunks[0].ToolCall.Name)

	completed := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`)
	chunks, err = codec.ParseStreamLine(completed, "gpt-5")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 2)
	require.Equal(t, core.ChunkFinish, chunks[0].Type)
	require.Equal(t, core.ChunkUsage, chunks[1].Type)
	require.Equal(t, 15, chunks[1].Usage.TotalTokens)
}

func TestResponses_RenderStream_EventSequence(t *testing.T) {
	codec := OpenAIResponsesCodec{}
	state := &StreamState{Model: "gpt-5", MessageID: "abc"}

	first, err := codec.RenderStreamChunk(core.StreamChunk{Type: core.ChunkText, Delta: "hi"}, state)
	require.NoError(t, err)
	joined := strings.Join(toStrings(first), "")
	// First text chunk must open the response and the message item.
	require.Contains(t, joined, "response.created")
	require.Contains(t, joined, "response.output_item.added")
	require.Contains(t, joined, "response.output_text.delta")

	usage, err := codec.RenderStreamChunk(core.StreamChunk{
		Type: core.ChunkUsage, Usage: &core.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	}, state)
	require.NoError(t, err)
	require.Contains(t, strings.Join(toStrings(usage), ""), "response.completed")
}

func TestCrossDialect_OpenAIToResponsesRoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5-codex",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "ping"}
		]
	}`)
	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	respBody, err := OpenAIResponsesCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	back, err := OpenAIResponsesCodec{}.ParseRequest(respBody)
	require.NoError(t, err)
	require.Equal(t, "be terse", back.System)
	require.Len(t, back.Messages, 1)
	require.Equal(t, "ping", back.Messages[0].TextContent())
}

func TestResponses_RenderRequest_StripsUnsupportedParams(t *testing.T) {
	// Simulate a request that arrives with Chat Completions parameters
	// (max_tokens, temperature, top_p) — these must NOT appear in the
	// rendered Responses API body.
	maxTokens := 4096
	temp := 0.7
	topP := 0.9
	req := &core.ChatRequest{
		Model:       "gpt-5-codex",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		TopP:        &topP,
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
		},
	}

	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))

	// These Chat Completions parameters must NOT be present.
	require.NotContains(t, parsed, "max_tokens", "max_tokens must not appear in Responses API body")
	require.NotContains(t, parsed, "temperature", "temperature must not appear in Responses API body")
	require.NotContains(t, parsed, "top_p", "top_p must not appear in Responses API body")

	// These Responses API fields MUST be present.
	require.Contains(t, parsed, "model")
	require.Contains(t, parsed, "input")
	require.Contains(t, parsed, "instructions")
	require.Contains(t, parsed, "stream")
	require.Contains(t, parsed, "store")
}

func TestResponses_RenderRequest_AllowlistStripsUnknownFields(t *testing.T) {
	// Verify the allowlist catches any field that isn't in the Responses API spec.
	// This is a defense-in-depth test — even if future code accidentally adds
	// a field to the output map, the allowlist strips it.
	req := &core.ChatRequest{
		Model: "gpt-5-codex",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		},
	}

	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))

	// Every key in the output must be in the allowlist.
	for k := range parsed {
		require.True(t, responsesAPIAllowlist[k], "unexpected field %q in Responses API body — add to responsesAPIAllowlist if valid", k)
	}
}

func TestResponses_RenderRequest_WithTools(t *testing.T) {
	req := &core.ChatRequest{
		Model: "gpt-5-codex",
		Tools: []core.Tool{
			{Name: "get_weather", Description: "Get weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)},
		},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "what's the weather?"}}},
		},
	}

	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed struct {
		Tools []struct {
			Type       string          `json:"type"`
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Tools, 1)
	require.Equal(t, "function", parsed.Tools[0].Type)
	require.Equal(t, "get_weather", parsed.Tools[0].Name)
}

func TestResponses_RenderRequest_EffortSuffixModel(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		reasoning     *core.ReasoningConfig
		wantModel     string
		wantEffort    string
		wantReasoning bool
	}{
		{
			name:          "grok high suffix",
			model:         "grok-4.5-high",
			wantModel:     "grok-4.5",
			wantEffort:    "high",
			wantReasoning: true,
		},
		{
			name:          "grok medium suffix",
			model:         "grok-4.5-medium",
			wantModel:     "grok-4.5",
			wantEffort:    "medium",
			wantReasoning: true,
		},
		{
			name:          "grok low suffix",
			model:         "grok-4.5-low",
			wantModel:     "grok-4.5",
			wantEffort:    "low",
			wantReasoning: true,
		},
		{
			name:          "base model no reasoning",
			model:         "grok-4.5",
			wantModel:     "grok-4.5",
			wantReasoning: false,
		},
		{
			name:          "explicit effort wins",
			model:         "grok-4.5",
			reasoning:     &core.ReasoningConfig{Effort: "high"},
			wantModel:     "grok-4.5",
			wantEffort:    "high",
			wantReasoning: true,
		},
		{
			name:          "explicit effort wins over suffix",
			model:         "grok-4.5-low",
			reasoning:     &core.ReasoningConfig{Effort: "high"},
			wantModel:     "grok-4.5",
			wantEffort:    "high",
			wantReasoning: true,
		},
		{
			name:          "codex high suffix",
			model:         "gpt-5.3-codex-high",
			wantModel:     "gpt-5.3-codex",
			wantEffort:    "high",
			wantReasoning: true,
		},
		{
			name:          "none suffix strips without reasoning",
			model:         "grok-4.5-none",
			wantModel:     "grok-4.5",
			wantReasoning: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &core.ChatRequest{
				Model:     tc.model,
				Reasoning: tc.reasoning,
				Messages: []core.Message{
					{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
				},
			}
			body, err := OpenAIResponsesCodec{}.RenderRequest(req)
			require.NoError(t, err)

			var parsed struct {
				Model     string `json:"model"`
				Reasoning *struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
			}
			require.NoError(t, json.Unmarshal(body, &parsed))
			require.Equal(t, tc.wantModel, parsed.Model)
			if tc.wantReasoning {
				require.NotNil(t, parsed.Reasoning)
				require.Equal(t, tc.wantEffort, parsed.Reasoning.Effort)
			} else {
				require.Nil(t, parsed.Reasoning)
			}
		})
	}
}

func toStrings(b [][]byte) []string {
	out := make([]string, len(b))
	for i, x := range b {
		out[i] = string(x)
	}
	return out
}

// TestClassifyRespStreamError verifies that in-stream error events (HTTP 200
// with an error in the SSE body) are scoped correctly: request problems must
// not cool down accounts or trip the provider circuit breaker.
func TestClassifyRespStreamError(t *testing.T) {
	// The reported failure: Codex context overflow arrives in-stream. It is
	// model-scoped (a larger-context target in the chain may still serve it)
	// and must never cool down the account or trip the provider circuit.
	pe := classifyRespStreamError("Your input exceeds the context window of this model. Please adjust your input and try again.")
	require.Equal(t, core.ErrContextWindow, pe.Kind)
	require.Equal(t, core.FailureScopeModel, pe.EffectiveScope())
	require.True(t, pe.Fallbackable())

	pe = classifyRespStreamError("The 'gpt-5-6' model is not supported when using Codex with a ChatGPT account.")
	require.Equal(t, core.ErrModelUnavailable, pe.Kind)
	require.Equal(t, core.FailureScopeModel, pe.EffectiveScope())

	// A TPM rate limit mentions token counts too; it must classify as a rate
	// limit (account cooldown) and never as request-scoped context overflow.
	pe = classifyRespStreamError("Rate limit reached for gpt-5-codex: too many tokens per min. Limit 30000, Requested 31000.")
	require.Equal(t, core.ErrRateLimit, pe.Kind)

	for _, message := range []string{
		"server_is_overloaded",
		"service_unavailable_error",
		"Selected model is at capacity. Please try a different model.",
		"model_at_capacity",
	} {
		pe = classifyRespStreamError(message)
		require.Equal(t, core.ErrUpstream, pe.Kind)
		require.Equal(t, core.FailureScopeRequest, pe.EffectiveScope())
		require.True(t, pe.Fallbackable())
	}
}
