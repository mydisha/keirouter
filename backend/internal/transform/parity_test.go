package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// ---- Anthropic ----------------------------------------------------------

func TestAnthropicReasoningEffortGetsBudget(t *testing.T) {
	req := &core.ChatRequest{
		Model:     "claude-sonnet-4-5",
		Reasoning: &core.ReasoningConfig{Effort: "high"},
		Messages:  []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)
	thinking := m["thinking"].(map[string]any)
	require.Equal(t, "enabled", thinking["type"])
	require.EqualValues(t, 4096, thinking["budget_tokens"])
	require.Greater(t, m["max_tokens"].(float64), float64(4096), "max_tokens must exceed the budget")
}

func TestAnthropicAdaptiveModelUsesOutputConfig(t *testing.T) {
	req := &core.ChatRequest{
		Model:     "claude-sonnet-4-6",
		Reasoning: &core.ReasoningConfig{Effort: "medium"},
		Messages:  []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)
	require.Equal(t, "adaptive", m["thinking"].(map[string]any)["type"])
	require.Equal(t, "medium", m["output_config"].(map[string]any)["effort"])
}

func TestAnthropicThinkingDropsForcedToolChoiceAndSampling(t *testing.T) {
	temp := 0.2
	req := &core.ChatRequest{
		Model:       "claude-sonnet-4-5",
		Temperature: &temp,
		Reasoning:   &core.ReasoningConfig{Effort: "low"},
		Tools:       []core.Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}},
		ToolChoice:  "required",
		Messages:    []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)
	require.Equal(t, "auto", m["tool_choice"].(map[string]any)["type"], "forced tool use is incompatible with thinking")
	_, hasTemp := m["temperature"]
	require.False(t, hasTemp)
}

func TestAnthropicUnsignedThinkingIsDropped(t *testing.T) {
	req := &core.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "q"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "reasoning from deepseek"},
				{Type: core.PartThinking, Text: "signed", Signature: "sig123"},
				{Type: core.PartRedactedThinking, Text: "opaque"},
				{Type: core.PartText, Text: "answer"},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "next"}}},
		},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	s := string(body)
	require.NotContains(t, s, "reasoning from deepseek")
	require.Contains(t, s, `"signature":"sig123"`)
	require.Contains(t, s, `"redacted_thinking"`)
	require.Contains(t, s, `"data":"opaque"`)
}

func TestAnthropicServerToolPassthroughAndEmptySchema(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","max_tokens":10,"messages":[{"role":"user","content":"hi"}],
	  "tools":[{"type":"web_search_20250305","name":"web_search","max_uses":3},{"name":"noop","description":"no params"}],
	  "tool_choice":{"type":"auto","disable_parallel_tool_use":true}}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	require.Len(t, req.Tools, 2)
	require.NotEmpty(t, req.Tools[0].Raw)
	require.NotNil(t, req.ParallelToolCalls)
	require.False(t, *req.ParallelToolCalls)

	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)
	tools := m["tools"].([]any)
	require.Equal(t, "web_search_20250305", tools[0].(map[string]any)["type"])
	require.EqualValues(t, 3, tools[0].(map[string]any)["max_uses"])
	schema := tools[1].(map[string]any)["input_schema"].(map[string]any)
	require.Equal(t, "object", schema["type"])
	require.Equal(t, true, m["tool_choice"].(map[string]any)["disable_parallel_tool_use"])
}

func TestAnthropicStreamSignatureRoundTrip(t *testing.T) {
	codec := AnthropicCodec{}
	var chunks []core.StreamChunk
	for _, line := range []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc=="}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"ENC"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
	} {
		got, err := codec.ParseStreamLine([]byte(line), "m")
		require.NoError(t, err)
		chunks = append(chunks, got...)
	}
	state := &StreamState{Model: "m"}
	ResetStreamState(state)
	var out strings.Builder
	for _, ch := range chunks {
		events, err := codec.RenderStreamChunk(ch, state)
		require.NoError(t, err)
		for _, ev := range events {
			out.Write(ev)
		}
	}
	for _, ev := range codec.RenderStreamDone(state) {
		out.Write(ev)
	}
	rendered := out.String()
	require.Contains(t, rendered, `"signature_delta"`)
	require.Contains(t, rendered, `"signature":"abc=="`)
	require.Contains(t, rendered, `"redacted_thinking"`)
	require.Contains(t, rendered, `"data":"ENC"`)
	require.Contains(t, rendered, `"output_tokens":7`)
	require.Equal(t, 1, strings.Count(rendered, "event: message_delta"), "one terminal message_delta carrying usage")
	sigPos := strings.Index(rendered, "signature_delta")
	stopPos := strings.Index(rendered[sigPos:], "content_block_stop")
	require.Greater(t, stopPos, 0, "signature must land before the thinking block closes")
}

func TestAnthropicUnaryThinkingParseAndUsageRender(t *testing.T) {
	body := `{"id":"msg_1","model":"claude","content":[{"type":"thinking","thinking":"deep","signature":"s"},{"type":"redacted_thinking","data":"x"},{"type":"text","text":"ok"}],
	  "stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":3,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}`
	resp, err := AnthropicCodec{}.ParseResponse([]byte(body), "claude")
	require.NoError(t, err)
	require.Equal(t, core.PartThinking, resp.Message.Content[0].Type)
	require.Equal(t, "deep", resp.Message.Content[0].Text)
	require.Equal(t, "s", resp.Message.Content[0].Signature)
	require.Equal(t, core.PartRedactedThinking, resp.Message.Content[1].Type)
	require.Equal(t, core.FinishFilter, resp.FinishReason)
	require.Equal(t, 17, resp.Usage.PromptTokens)

	out, err := AnthropicCodec{}.RenderResponse(resp)
	require.NoError(t, err)
	m := decodeJSON(t, out)
	usage := m["usage"].(map[string]any)
	require.EqualValues(t, 10, usage["input_tokens"])
	require.EqualValues(t, 5, usage["cache_read_input_tokens"])
	require.EqualValues(t, 2, usage["cache_creation_input_tokens"])
	require.Equal(t, "refusal", m["stop_reason"])
	require.Contains(t, string(out), `"signature":"s"`)
}

// ---- OpenAI ---------------------------------------------------------------

func TestOpenAIParseStopStringAndMaxCompletionTokens(t *testing.T) {
	req, err := OpenAICodec{}.ParseRequest([]byte(`{"model":"gpt-5","stop":"\n","max_completion_tokens":77,"parallel_tool_calls":false,"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"\n"}, req.Stop)
	require.Equal(t, 77, *req.MaxTokens)
	require.False(t, *req.ParallelToolCalls)

	req, err = OpenAICodec{}.ParseRequest([]byte(`{"model":"gpt-4o","stop":["a","b"],"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, req.Stop)
}

func TestOpenAIFirstPartyReasoningModelRequest(t *testing.T) {
	mt := 500
	temp := 0.3
	req := &core.ChatRequest{
		Model: "gpt-5", MaxTokens: &mt, Temperature: &temp, Stream: true,
		Reasoning: &core.ReasoningConfig{Effort: "high"},
		Messages:  []core.Message{{Role: core.RoleDeveloper, Content: []core.ContentPart{{Type: core.PartText, Text: "be terse"}}}, {Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)
	m := decodeJSON(t, body)
	_, hasMax := m["max_tokens"]
	require.False(t, hasMax)
	require.EqualValues(t, 500, m["max_completion_tokens"])
	require.Equal(t, "high", m["reasoning_effort"])
	_, hasTemp := m["temperature"]
	require.False(t, hasTemp)
	require.Equal(t, true, m["stream_options"].(map[string]any)["include_usage"])
	require.Equal(t, "developer", m["messages"].([]any)[0].(map[string]any)["role"])

	// A generic OpenAI-compatible provider keeps max_tokens and gets a
	// system role instead of developer.
	body, err = OpenAICodec{}.RenderRequestForProvider(req, "groq")
	require.NoError(t, err)
	m = decodeJSON(t, body)
	require.EqualValues(t, 500, m["max_tokens"])
	require.Equal(t, "system", m["messages"].([]any)[0].(map[string]any)["role"])
}

func TestOpenAIUsageDetailsRendered(t *testing.T) {
	resp := &core.ChatResponse{Model: "m", Message: core.Message{Role: core.RoleAssistant},
		Usage: core.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, CachedTokens: 40, ReasoningTokens: 20}}
	out, err := OpenAICodec{}.RenderResponse(resp)
	require.NoError(t, err)
	usage := decodeJSON(t, out)["usage"].(map[string]any)
	require.EqualValues(t, 40, usage["prompt_tokens_details"].(map[string]any)["cached_tokens"])
	require.EqualValues(t, 20, usage["completion_tokens_details"].(map[string]any)["reasoning_tokens"])
}

func TestOpenAIStreamReasoningAliasField(t *testing.T) {
	chunks, err := OpenAICodec{}.ParseStreamLine([]byte(`{"choices":[{"delta":{"reasoning":"thinking..."}}]}`), "m")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkThinking, chunks[0].Type)
}

// ---- Gemini ---------------------------------------------------------------

func TestGeminiMergesParallelToolResultsIntoOneTurn(t *testing.T) {
	req := &core.ChatRequest{
		Model: "gemini-2.5-pro",
		Tools: []core.Tool{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`)}},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "read both"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"p":"a"}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_2", Name: "read_file", Arguments: json.RawMessage(`{"p":"b"}`)}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_1", Content: "A"}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_2", Content: "B"}}}},
		},
	}
	body, err := GeminiCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)
	contents := m["contents"].([]any)
	require.Len(t, contents, 3, "user, model, user")
	last := contents[2].(map[string]any)
	require.Equal(t, "user", last["role"])
	require.Len(t, last["parts"].([]any), 2, "both function responses in one content")
}

func TestGeminiParallelCallsGetUniqueIDs(t *testing.T) {
	line := `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{"p":"a"}}},{"functionCall":{"name":"read_file","args":{"p":"b"}}}]}}]}`
	chunks, err := GeminiCodec{}.ParseStreamLine([]byte(line), "gemini-2.5-pro")
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.NotEqual(t, chunks[0].ToolCall.ID, chunks[1].ToolCall.ID)
	require.True(t, strings.HasPrefix(chunks[0].ToolCall.ID, "call_read_file"))

	// Rendering the invented id back to Gemini must not leak the nonce.
	events, err := GeminiCodec{}.RenderStreamChunk(chunks[0], &StreamState{})
	require.NoError(t, err)
	require.NotContains(t, string(events[0]), "__n__")
	require.NotContains(t, string(events[0]), `"id"`)
}

func TestGeminiThinkingConfigAndResponseFormat(t *testing.T) {
	req := &core.ChatRequest{
		Model:          "gemini-2.5-flash",
		Reasoning:      &core.ReasoningConfig{Effort: "medium"},
		ResponseFormat: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object","properties":{"a":{"type":"string"}}}}}`),
		Messages:       []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
	}
	body, err := GeminiCodec{}.RenderRequest(req)
	require.NoError(t, err)
	gc := decodeJSON(t, body)["generationConfig"].(map[string]any)
	tc := gc["thinkingConfig"].(map[string]any)
	require.EqualValues(t, 2048, tc["thinkingBudget"])
	require.Equal(t, true, tc["includeThoughts"])
	require.Equal(t, "application/json", gc["responseMimeType"])
	require.NotNil(t, gc["responseJsonSchema"])
}

func TestGeminiSchemaInlinesDefsAndFillsItems(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"files":{"type":"array"},"opts":{"$ref":"#/$defs/Opts"}},
	  "$defs":{"Opts":{"type":"object","properties":{"deep":{"type":"boolean"}}}}}`)
	cleaned := decodeJSON(t, cleanGeminiToolSchema(raw))
	props := cleaned["properties"].(map[string]any)
	files := props["files"].(map[string]any)
	require.Equal(t, "object", files["items"].(map[string]any)["type"])
	opts := props["opts"].(map[string]any)
	require.Equal(t, "object", opts["type"])
	require.Contains(t, opts["properties"], "deep")
	_, hasDefs := cleaned["$defs"]
	require.False(t, hasDefs)
}

func TestGeminiUsageAndFinishRender(t *testing.T) {
	resp := &core.ChatResponse{Model: "g", Message: core.Message{Role: core.RoleAssistant}, FinishReason: core.FinishFilter,
		Usage: core.Usage{PromptTokens: 10, CompletionTokens: 30, ReasoningTokens: 12, CachedTokens: 4, TotalTokens: 40}}
	out, err := GeminiCodec{}.RenderResponse(resp)
	require.NoError(t, err)
	um := decodeJSON(t, out)["usageMetadata"].(map[string]any)
	require.EqualValues(t, 18, um["candidatesTokenCount"])
	require.EqualValues(t, 12, um["thoughtsTokenCount"])
	require.EqualValues(t, 4, um["cachedContentTokenCount"])
	require.Equal(t, core.FinishFilter, mapGemFinish("PROHIBITED_CONTENT"))
	require.Equal(t, core.FinishStop, mapGemFinish("MALFORMED_FUNCTION_CALL"))
}

// ---- Responses ------------------------------------------------------------

func TestResponsesParallelCallsFormOneAssistantTurn(t *testing.T) {
	in := `{"model":"gpt-5","input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]},
	  {"type":"function_call","call_id":"c1","name":"a","arguments":"{}"},
	  {"type":"function_call","call_id":"c2","name":"b","arguments":"{}"},
	  {"type":"function_call_output","call_id":"c1","output":"1"},
	  {"type":"function_call_output","call_id":"c2","output":"2"}]}`
	req, err := OpenAIResponsesCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	require.Len(t, req.Messages, 4, "user, assistant(2 calls), tool, tool")
	require.Equal(t, core.RoleAssistant, req.Messages[1].Role)
	require.Len(t, req.Messages[1].Content, 2)
}

func TestResponsesStreamCarriesOutputIndex(t *testing.T) {
	chunks, err := OpenAIResponsesCodec{}.ParseStreamLine([]byte(`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"a\""}`), "m")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, 2, chunks[0].Index)
}

// ---- cache_control / media / custom tools --------------------------------

func TestAnthropicCacheControlAndMediaRoundTrip(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","max_tokens":10,
	  "system":[{"type":"text","text":"sys A"},{"type":"text","text":" sys B","cache_control":{"type":"ephemeral"}}],
	  "tools":[{"name":"t","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],
	  "messages":[
	    {"role":"user","content":[{"type":"text","text":"look","cache_control":{"type":"ephemeral","ttl":"1h"}},
	      {"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"UERG"}}]},
	    {"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"t","input":{}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":[{"type":"text","text":"shot"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBOR"}}]}]}
	  ]}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	require.Equal(t, "sys A sys B", req.System)
	require.JSONEq(t, `{"type":"ephemeral"}`, string(req.SystemCacheControl))
	require.JSONEq(t, `{"type":"ephemeral"}`, string(req.Tools[0].CacheControl))
	require.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, string(req.Messages[0].Content[0].CacheControl))
	require.Equal(t, core.PartDocument, req.Messages[0].Content[1].Type)
	tr := req.Messages[2].Content[0].ToolResult
	require.Equal(t, "shot", tr.Content)
	require.Len(t, tr.Parts, 1)

	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	s := string(body)
	require.Contains(t, s, `"ttl":"1h"`)
	require.Contains(t, s, `"type":"document"`)
	require.Contains(t, s, `"media_type":"image/png"`)
	m := decodeJSON(t, body)
	sys := m["system"].([]any)[0].(map[string]any)
	require.NotNil(t, sys["cache_control"])
	tool := m["tools"].([]any)[0].(map[string]any)
	require.NotNil(t, tool["cache_control"])
}

func TestOpenAIToolMessageMediaAndPassthroughParams(t *testing.T) {
	in := `{"model":"gpt-4o","seed":7,"presence_penalty":0.5,"service_tier":"flex","store":true,"user":"u1",
	  "messages":[{"role":"tool","tool_call_id":"c1","content":[{"type":"text","text":"ok"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAA"}}]}]}`
	req, err := OpenAICodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	tr := req.Messages[0].Content[0].ToolResult
	require.Equal(t, "ok", tr.Content)
	require.Len(t, tr.Parts, 1)
	require.Equal(t, "image/png", tr.Parts[0].Media.MIMEType)
	require.Len(t, req.Extra, 5)

	body, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)
	m := decodeJSON(t, body)
	require.EqualValues(t, 7, m["seed"])
	require.Equal(t, "flex", m["service_tier"])
	require.Equal(t, true, m["store"])

	body, err = OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)
	m = decodeJSON(t, body)
	require.EqualValues(t, 7, m["seed"], "widely supported params pass through")
	_, hasTier := m["service_tier"]
	require.False(t, hasTier, "first-party-only params are dropped for third parties")

	// user -> Anthropic metadata.user_id
	body, err = AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(body), `"user_id":"u1"`)
}

func TestResponsesCustomToolRoundTrip(t *testing.T) {
	in := `{"model":"gpt-5-codex","previous_response_id":"resp_prev","max_output_tokens":123,
	  "tools":[{"type":"custom","name":"apply_patch","description":"patch","format":{"type":"grammar","syntax":"lark","definition":"x"}}],
	  "input":[{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch"},
	           {"type":"custom_tool_call_output","call_id":"c1","output":"Done"}]}`
	req, err := OpenAIResponsesCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	require.Equal(t, "resp_prev", req.PreviousResponseID)
	require.Equal(t, 123, *req.MaxTokens)
	require.NotEmpty(t, req.Tools[0].Raw)
	require.True(t, req.Messages[0].Content[0].ToolCall.Custom)
	require.Equal(t, "*** Begin Patch", string(req.Messages[0].Content[0].ToolCall.Arguments))

	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)
	require.Equal(t, "resp_prev", m["previous_response_id"])
	tools := m["tools"].([]any)
	require.Equal(t, "custom", tools[0].(map[string]any)["type"])
	require.NotNil(t, tools[0].(map[string]any)["format"])
	items := m["input"].([]any)
	require.Equal(t, "custom_tool_call", items[0].(map[string]any)["type"])
	require.Equal(t, "*** Begin Patch", items[0].(map[string]any)["input"])
	require.Equal(t, "custom_tool_call_output", items[1].(map[string]any)["type"])

	// Streaming: custom input deltas render as custom_tool_call_input events.
	codec := OpenAIResponsesCodec{}
	state := &StreamState{Model: "m"}
	ResetStreamState(state)
	var out strings.Builder
	for _, line := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"c9","name":"apply_patch"}}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"*** Begin"}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
	} {
		chunks, err := codec.ParseStreamLine([]byte(line), "m")
		require.NoError(t, err)
		for _, ch := range chunks {
			evs, err := codec.RenderStreamChunk(ch, state)
			require.NoError(t, err)
			for _, ev := range evs {
				out.Write(ev)
			}
		}
	}
	require.Contains(t, out.String(), "response.custom_tool_call_input.delta")
	require.Contains(t, out.String(), `"type":"custom_tool_call"`)
}

func TestToolNameAliasesForGemini(t *testing.T) {
	require.Nil(t, ToolNameAliases("read_file"))
	aliases := ToolNameAliases("mcp__server__very/odd name")
	require.Len(t, aliases, 1)
	require.NotEqual(t, "mcp__server__very/odd name", aliases[0])
}

func TestOpenAIRenderCanonicalisesCloudflareModel(t *testing.T) {
	req := &core.ChatRequest{Model: "meta/llama-3.3-70b-instruct-fp8-fast",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "cloudflare-ai")
	require.NoError(t, err)
	require.Equal(t, "@cf/meta/llama-3.3-70b-instruct-fp8-fast", decodeJSON(t, body)["model"])
	body, err = OpenAICodec{}.RenderRequestForProvider(req, "groq")
	require.NoError(t, err)
	require.Equal(t, "meta/llama-3.3-70b-instruct-fp8-fast", decodeJSON(t, body)["model"])
}

// ---- usage normalisation ---------------------------------------------------

func TestAnthropicUsageNormalisationWithCacheTTLAndSearch(t *testing.T) {
	body := `{"id":"m","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
	  "usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":500,"cache_creation_input_tokens":300,
	    "cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200},
	    "server_tool_use":{"web_search_requests":2}}}`
	resp, err := AnthropicCodec{}.ParseResponse([]byte(body), "claude")
	require.NoError(t, err)
	u := resp.Usage
	require.Equal(t, 900, u.PromptTokens, "prompt includes cache reads and writes")
	require.Equal(t, 300, u.CacheWriteTokens)
	require.Equal(t, 200, u.CacheWrite1hTokens)
	require.Equal(t, 2, u.WebSearchRequests)

	// Iterations (compaction / server tool loops) are summed.
	iter := `{"id":"m","model":"claude","content":[],"stop_reason":"end_turn",
	  "usage":{"input_tokens":1,"output_tokens":1,"iterations":[
	    {"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":0},
	    {"input_tokens":30,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":40,"server_tool_use":{"web_search_requests":1}}]}}`
	resp, err = AnthropicCodec{}.ParseResponse([]byte(iter), "claude")
	require.NoError(t, err)
	require.Equal(t, 100, resp.Usage.PromptTokens)
	require.Equal(t, 12, resp.Usage.CompletionTokens)
	require.Equal(t, 1, resp.Usage.WebSearchRequests)

	// Streaming message_start carries the same shape.
	chunks, err := AnthropicCodec{}.ParseStreamLine([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":90,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_1h_input_tokens":50}}}}`), "claude")
	require.NoError(t, err)
	require.Equal(t, 150, chunks[0].Usage.PromptTokens)
	require.Equal(t, 50, chunks[0].Usage.CacheWrite1hTokens)
}

func TestGeminiUsageInclusiveThoughtsAndGrounding(t *testing.T) {
	// Totals prove thoughts are already inside candidates: do not add them.
	inclusive := gemUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 60, ThoughtsTokenCount: 20, TotalTokenCount: 160}
	u := gemUsageToCore(inclusive, nil)
	require.Equal(t, 60, u.CompletionTokens)
	require.Equal(t, 20, u.ReasoningTokens)

	// Totals show thoughts are separate: add them.
	separate := gemUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 60, ThoughtsTokenCount: 20, TotalTokenCount: 180}
	u = gemUsageToCore(separate, nil)
	require.Equal(t, 80, u.CompletionTokens)

	// Tool-use prompt tokens are prompt tokens unless search grounding ran.
	withTool := gemUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 10, ToolUsePromptTokenCount: 30, TotalTokenCount: 140}
	require.Equal(t, 130, gemUsageToCore(withTool, nil).PromptTokens)
	grounded := gemUsageToCore(withTool, json.RawMessage(`{"webSearchQueries":["x"],"groundingChunks":[]}`))
	require.Equal(t, 100, grounded.PromptTokens)
	require.Equal(t, 1, grounded.WebSearchRequests)
}

func TestOpenRouterUsageAccountingAndCost(t *testing.T) {
	req := &core.ChatRequest{Model: "some/model", Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "openrouter")
	require.NoError(t, err)
	require.Equal(t, true, decodeJSON(t, body)["usage"].(map[string]any)["include"])
	body, err = OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)
	_, has := decodeJSON(t, body)["usage"]
	require.False(t, has)

	resp, err := OpenAICodec{}.ParseResponse([]byte(`{"id":"r","model":"m","choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.00123}}`), "m")
	require.NoError(t, err)
	require.EqualValues(t, 1_230_000, resp.Usage.ProviderCostNanos)

	chunks, err := OpenAICodec{}.ParseStreamLine([]byte(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.5}}`), "m")
	require.NoError(t, err)
	require.EqualValues(t, 500_000_000, chunks[0].Usage.ProviderCostNanos)
}

func TestDeepSeekPromptCacheHitTokensCountAsCached(t *testing.T) {
	resp, err := OpenAICodec{}.ParseResponse([]byte(`{"id":"r","model":"deepseek-chat","choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":1000,"completion_tokens":5,"total_tokens":1005,"prompt_cache_hit_tokens":768,"prompt_cache_miss_tokens":232}}`), "deepseek-chat")
	require.NoError(t, err)
	require.Equal(t, 768, resp.Usage.CachedTokens)
	chunks, err := OpenAICodec{}.ParseStreamLine([]byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":1,"total_tokens":101,"prompt_cache_hit_tokens":64}}`), "deepseek-chat")
	require.NoError(t, err)
	require.Equal(t, 64, chunks[0].Usage.CachedTokens)
}
