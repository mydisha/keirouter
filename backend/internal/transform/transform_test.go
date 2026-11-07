package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestOpenAI_ParseRequest_BasicChat(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "be helpful"},
			{"role": "user", "content": "hello"}
		],
		"stream": true,
		"max_tokens": 256
	}`)

	req, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", req.Model)
	require.Equal(t, "be helpful", req.System)
	require.True(t, req.Stream)
	require.NotNil(t, req.MaxTokens)
	require.Equal(t, 256, *req.MaxTokens)
	require.Len(t, req.Messages, 1)
	require.Equal(t, core.RoleUser, req.Messages[0].Role)
	require.Equal(t, "hello", req.Messages[0].TextContent())
}

func TestOpenAI_ParseRequest_ToolCallAndResult(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "assistant", "content": null, "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"SF\"}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny, 22C"}
		]
	}`)

	req, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)

	tc := req.Messages[0].Content[0]
	require.Equal(t, core.PartToolCall, tc.Type)
	require.Equal(t, "get_weather", tc.ToolCall.Name)

	tr := req.Messages[1].Content[0]
	require.Equal(t, core.PartToolResult, tr.Type)
	require.Equal(t, "call_1", tr.ToolResult.CallID)
	require.Equal(t, "sunny, 22C", tr.ToolResult.Content)
}

// Cross-dialect: an OpenAI request rendered to Anthropic and parsed back must
// preserve system, messages, and tool structure.
func TestCrossDialect_OpenAIToAnthropicRoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4.5",
		"messages": [
			{"role": "system", "content": "you are precise"},
			{"role": "user", "content": "what is 2+2?"},
			{"role": "assistant", "content": "4"},
			{"role": "user", "content": "and 3+3?"}
		],
		"max_tokens": 100
	}`)

	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	antBody, err := AnthropicCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	// Anthropic body must hoist system and carry alternating roles.
	var antReq map[string]any
	require.NoError(t, json.Unmarshal(antBody, &antReq))
	require.Equal(t, "you are precise", antReq["system"])
	require.Equal(t, float64(100), antReq["max_tokens"])

	// Parse the Anthropic body back to canonical and compare essentials.
	back, err := AnthropicCodec{}.ParseRequest(antBody)
	require.NoError(t, err)
	require.Equal(t, "you are precise", back.System)
	require.Len(t, back.Messages, 3)
	require.Equal(t, "what is 2+2?", back.Messages[0].TextContent())
	require.Equal(t, core.RoleAssistant, back.Messages[1].Role)
	require.Equal(t, "and 3+3?", back.Messages[2].TextContent())
}

func TestAnthropic_MergesConsecutiveSameRole(t *testing.T) {
	// Two consecutive user messages (e.g. text + tool result) must merge into
	// one Anthropic message, since the API forbids consecutive same-role turns.
	req := &core.ChatRequest{
		Model: "claude-x",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "first"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "second"}}},
		},
	}
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed struct {
		Messages []json.RawMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Messages, 1, "consecutive user messages must merge")
}

func TestOpenAI_ResponseRoundTrip(t *testing.T) {
	resp := &core.ChatResponse{
		ID:    "resp1",
		Model: "gpt-4o",
		Message: core.Message{
			Role:    core.RoleAssistant,
			Content: []core.ContentPart{{Type: core.PartText, Text: "the answer"}},
		},
		FinishReason: core.FinishStop,
		Usage:        core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	body, err := OpenAICodec{}.RenderResponse(resp)
	require.NoError(t, err)

	back, err := OpenAICodec{}.ParseResponse(body, "gpt-4o")
	require.NoError(t, err)
	require.Equal(t, "the answer", back.Message.TextContent())
	require.Equal(t, core.FinishStop, back.FinishReason)
	require.Equal(t, 15, back.Usage.TotalTokens)
}

func TestOpenAI_ParseStreamLine(t *testing.T) {
	line := []byte(`{"id":"x","model":"gpt-4o","choices":[{"delta":{"content":"hel"},"finish_reason":null}]}`)
	chunks, err := OpenAICodec{}.ParseStreamLine(line, "gpt-4o")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkText, chunks[0].Type)
	require.Equal(t, "hel", chunks[0].Delta)

	done, err := OpenAICodec{}.ParseStreamLine([]byte("[DONE]"), "gpt-4o")
	require.NoError(t, err)
	require.Empty(t, done)
}

func TestOpenAI_ParseStreamLine_ToolArgumentsObject(t *testing.T) {
	line := []byte(`{"id":"x","model":"qoder","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"TaskCreate","arguments":{"subject":"Fix Qoder","description":"Debug tool args"}}}]},"finish_reason":null}]}`)

	chunks, err := OpenAICodec{}.ParseStreamLine(line, "qoder")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkToolCall, chunks[0].Type)
	require.Equal(t, "TaskCreate", chunks[0].ToolCall.Name)
	require.JSONEq(t, `{"subject":"Fix Qoder","description":"Debug tool args"}`, string(chunks[0].ToolCall.Arguments))
}

func TestOpenAI_ParseStreamLine_ToolArgumentsTopLevelInput(t *testing.T) {
	line := []byte(`{"id":"x","model":"qoder","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","name":"TaskCreate","input":{"subject":"Fix Qoder","description":"Debug TaskCreate"}}]},"finish_reason":null}]}`)

	chunks, err := OpenAICodec{}.ParseStreamLine(line, "qoder")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkToolCall, chunks[0].Type)
	require.Equal(t, "TaskCreate", chunks[0].ToolCall.Name)
	require.JSONEq(t, `{"subject":"Fix Qoder","description":"Debug TaskCreate"}`, string(chunks[0].ToolCall.Arguments))
}

func TestOpenAI_ParseStreamLine_ToolArgumentsStringFragment(t *testing.T) {
	line := []byte(`{"id":"x","model":"qoder","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"subject\":\"Fix"}}]},"finish_reason":null}]}`)

	chunks, err := OpenAICodec{}.ParseStreamLine(line, "qoder")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkToolCall, chunks[0].Type)
	require.Equal(t, `{"subject":"Fix`, string(chunks[0].ToolCall.Arguments))
}

func TestAnthropic_ParseStreamEvents(t *testing.T) {
	textDelta := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
	chunks, err := AnthropicCodec{}.ParseStreamLine(textDelta, "claude")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkText, chunks[0].Type)
	require.Equal(t, "hi", chunks[0].Delta)

	stop := []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}`)
	chunks, err = AnthropicCodec{}.ParseStreamLine(stop, "claude")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 1)
	require.Equal(t, core.ChunkFinish, chunks[0].Type)
	require.Equal(t, core.FinishStop, chunks[0].FinishReason)
}

func TestAnthropic_RenderResponse_ToolInputAlwaysObject(t *testing.T) {
	resp := &core.ChatResponse{
		ID:    "msg1",
		Model: "claude-x",
		Message: core.Message{
			Role: core.RoleAssistant,
			Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "toolu_1", Name: "Read", Arguments: nil}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "toolu_2", Name: "Read", Arguments: json.RawMessage(`[]`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "toolu_3", Name: "Read", Arguments: json.RawMessage(`{"file_path":"README.md"}`)}},
			},
		},
		FinishReason: core.FinishToolCalls,
	}

	body, err := AnthropicCodec{}.RenderResponse(resp)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	content := parsed["content"].([]any)

	firstInput := content[0].(map[string]any)["input"]
	require.IsType(t, map[string]any{}, firstInput, "nil tool args must render as object")
	require.Empty(t, firstInput.(map[string]any))

	secondInput := content[1].(map[string]any)["input"]
	require.IsType(t, map[string]any{}, secondInput, "array tool args must render as object")
	require.Empty(t, secondInput.(map[string]any))

	thirdInput := content[2].(map[string]any)["input"].(map[string]any)
	require.Equal(t, "README.md", thirdInput["file_path"])
}

func TestAnthropic_RenderRequest_ToolInputAlwaysObject(t *testing.T) {
	req := &core.ChatRequest{
		Model: "claude-x",
		Messages: []core.Message{{
			Role: core.RoleAssistant,
			Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "toolu_1", Name: "Read", Arguments: json.RawMessage(`null`)}},
			},
		}},
	}

	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	msgs := parsed["messages"].([]any)
	blocks := msgs[0].(map[string]any)["content"].([]any)
	input := blocks[0].(map[string]any)["input"]

	require.IsType(t, map[string]any{}, input)
	require.Empty(t, input.(map[string]any))
}

func TestAnthropic_RenderStreamChunk_ArgumentDeltasPassedThrough(t *testing.T) {
	state := &StreamState{Model: "claude-x", MessageID: "msg1"}

	// First chunk: opens the tool_use block (ID + Name).
	openEvents, err := AnthropicCodec{}.RenderStreamChunk(core.StreamChunk{
		Type:  core.ChunkToolCall,
		Index: 0,
		ToolCall: &core.ToolCall{
			ID:        "toolu_1",
			Name:      "Bash",
			Arguments: json.RawMessage(``),
		},
	}, state)
	require.NoError(t, err)
	require.Len(t, openEvents, 2) // message_start + content_block_start

	// Subsequent chunks: partial JSON fragments must pass through verbatim.
	fragments := []string{`{"com`, `mand":"`, `ls -la`, `"}`}
	for _, frag := range fragments {
		deltaEvents, err := AnthropicCodec{}.RenderStreamChunk(core.StreamChunk{
			Type:  core.ChunkToolCall,
			Index: 0,
			ToolCall: &core.ToolCall{
				Arguments: json.RawMessage(frag),
			},
		}, state)
		require.NoError(t, err)
		require.Len(t, deltaEvents, 1, "partial JSON fragment must produce one event")
		require.Contains(t, string(deltaEvents[0]), `"input_json_delta"`)
		require.Contains(t, string(deltaEvents[0]), `"partial_json"`)
	}
}

func TestAnthropic_RenderStreamChunk_ToolInputAlwaysObject(t *testing.T) {
	state := &StreamState{Model: "claude-x", MessageID: "msg1"}
	events, err := AnthropicCodec{}.RenderStreamChunk(core.StreamChunk{
		Type:  core.ChunkToolCall,
		Index: 0,
		ToolCall: &core.ToolCall{
			ID:        "toolu_1",
			Name:      "Read",
			Arguments: json.RawMessage(`[]`),
		},
	}, state)
	require.NoError(t, err)
	require.Len(t, events, 2)

	var start map[string]any
	data := strings.TrimPrefix(strings.Split(string(events[1]), "\n")[1], "data: ")
	require.NoError(t, json.Unmarshal([]byte(data), &start))

	block := start["content_block"].(map[string]any)
	input := block["input"]
	require.IsType(t, map[string]any{}, input)
	require.Empty(t, input.(map[string]any))
	require.NotContains(t, string(events[1]), `"partial_json":[]`)
}

// Stream re-render: canonical text chunks rendered to OpenAI SSE must start with
// the assistant role then carry content deltas.
func TestOpenAI_RenderStreamChunk_SequencesRole(t *testing.T) {
	state := &StreamState{Model: "gpt-4o", MessageID: "id1"}
	first, err := OpenAICodec{}.RenderStreamChunk(core.StreamChunk{Type: core.ChunkText, Delta: "a"}, state)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Contains(t, string(first[0]), `"role":"assistant"`)
	require.Contains(t, string(first[0]), `"content":"a"`)

	second, err := OpenAICodec{}.RenderStreamChunk(core.StreamChunk{Type: core.ChunkText, Delta: "b"}, state)
	require.NoError(t, err)
	require.NotContains(t, string(second[0]), `"role"`, "role only sent once")

	done := OpenAICodec{}.RenderStreamDone(state)
	require.Contains(t, string(done[0]), "[DONE]")
}

// Cross-dialect: an OpenAI request rendered to Gemini and parsed back must
// preserve system, roles, and message text.
func TestCrossDialect_OpenAIToGeminiRoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "gemini-2.0-flash",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "ping"},
			{"role": "assistant", "content": "pong"},
			{"role": "user", "content": "again"}
		],
		"max_tokens": 64
	}`)

	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	gemBody, err := GeminiCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var gemReq map[string]any
	require.NoError(t, json.Unmarshal(gemBody, &gemReq))
	require.Contains(t, gemReq, "systemInstruction")
	require.Contains(t, gemReq, "contents")

	back, err := GeminiCodec{}.ParseRequest(gemBody)
	require.NoError(t, err)
	require.Equal(t, "be terse", back.System)
	require.Len(t, back.Messages, 3)
	require.Equal(t, "ping", back.Messages[0].TextContent())
	require.Equal(t, core.RoleAssistant, back.Messages[1].Role)
	require.Equal(t, "again", back.Messages[2].TextContent())
}

func TestGemini_ResponseRoundTrip(t *testing.T) {
	resp := &core.ChatResponse{
		Model:        "gemini-2.0-flash",
		Message:      core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		FinishReason: core.FinishStop,
		Usage:        core.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
	}
	body, err := GeminiCodec{}.RenderResponse(resp)
	require.NoError(t, err)

	back, err := GeminiCodec{}.ParseResponse(body, "gemini-2.0-flash")
	require.NoError(t, err)
	require.Equal(t, "hi", back.Message.TextContent())
	require.Equal(t, core.FinishStop, back.FinishReason)
	require.Equal(t, 4, back.Usage.TotalTokens)
}

func TestRegistry_ResolvesCodecs(t *testing.T) {
	reg := DefaultRegistry()

	_, err := reg.Codec(core.DialectOpenAI)
	require.NoError(t, err)

	_, err = reg.StreamCodec(core.DialectAnthropic)
	require.NoError(t, err)

	_, err = reg.Codec(core.DialectGemini)
	require.NoError(t, err, "gemini codec is registered")

	_, err = reg.StreamCodec(core.DialectGemini)
	require.NoError(t, err, "gemini codec supports streaming")

	_, err = reg.Codec(core.DialectOllama)
	require.NoError(t, err, "ollama codec is registered")

	_, err = reg.StreamCodec(core.DialectOllama)
	require.NoError(t, err, "ollama codec supports streaming")

	_, err = reg.Codec(core.DialectCursor)
	require.Error(t, err, "unregistered dialect must error")
}

// ---- Multimodal image tests --------------------------------------------------

// OpenAI image_url (data URI) → parse → canonical → render back to OpenAI.
func TestOpenAI_ImageDataURL_RoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "describe this"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}}
			]}
		]
	}`)

	req, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Len(t, req.Messages, 1)
	require.Len(t, req.Messages[0].Content, 2)

	img := req.Messages[0].Content[1]
	require.Equal(t, core.PartImage, img.Type)
	require.NotNil(t, img.Media)
	require.Equal(t, "image/png", img.Media.MIMEType)
	require.Equal(t, "iVBORw0KGgo=", img.Media.Data)
	require.Empty(t, img.Media.URL, "data URI should decompose into MIMEType+Data")

	// Render back to OpenAI format.
	rendered, err := OpenAICodec{}.RenderRequest(req)
	require.NoError(t, err)

	var oaiReq map[string]any
	require.NoError(t, json.Unmarshal(rendered, &oaiReq))
	msgs := oaiReq["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 2)
	require.Equal(t, "text", content[0].(map[string]any)["type"])
	imgPart := content[1].(map[string]any)
	require.Equal(t, "image_url", imgPart["type"])
	url := imgPart["image_url"].(map[string]any)["url"].(string)
	require.Contains(t, url, "data:image/png;base64,")
}

// OpenAI image_url (remote URL) → parse → canonical preserves URL.
func TestOpenAI_ImageURL_Remote(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "what is this?"},
				{"type": "image_url", "image_url": {"url": "https://example.com/cat.jpg"}}
			]}
		]
	}`)

	req, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)
	img := req.Messages[0].Content[1]
	require.Equal(t, core.PartImage, img.Type)
	require.Equal(t, "https://example.com/cat.jpg", img.Media.URL)
	require.Empty(t, img.Media.Data)
	require.Empty(t, img.Media.MIMEType)
}

// OpenAI image (data URI) → render to Anthropic → base64 image block.
func TestCrossDialect_OpenAIImageToAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "describe this"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}}
			]}
		]
	}`)

	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	antBody, err := AnthropicCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var antReq map[string]any
	require.NoError(t, json.Unmarshal(antBody, &antReq))
	msgs := antReq["messages"].([]any)
	msg := msgs[0].(map[string]any)
	blocks := msg["content"].([]any)
	require.Len(t, blocks, 2)

	imgBlock := blocks[1].(map[string]any)
	require.Equal(t, "image", imgBlock["type"])
	source := imgBlock["source"].(map[string]any)
	require.Equal(t, "base64", source["type"])
	require.Equal(t, "image/png", source["media_type"])
	require.Equal(t, "iVBORw0KGgo=", source["data"])
}

// OpenAI image (remote URL) → render to Anthropic → url image block.
func TestCrossDialect_OpenAIImageURLToAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "what is this?"},
				{"type": "image_url", "image_url": {"url": "https://example.com/cat.jpg"}}
			]}
		]
	}`)

	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	antBody, err := AnthropicCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var antReq map[string]any
	require.NoError(t, json.Unmarshal(antBody, &antReq))
	msgs := antReq["messages"].([]any)
	msg := msgs[0].(map[string]any)
	blocks := msg["content"].([]any)
	require.Len(t, blocks, 2)

	imgBlock := blocks[1].(map[string]any)
	require.Equal(t, "image", imgBlock["type"])
	source := imgBlock["source"].(map[string]any)
	require.Equal(t, "url", source["type"])
	require.Equal(t, "https://example.com/cat.jpg", source["url"])
}

// Anthropic base64 image → parse → render to OpenAI → data URI.
func TestCrossDialect_AnthropicImageToOpenAI(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4.5",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "describe"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "/9j/4AAQ"}}
			]}
		]
	}`)

	canonical, err := AnthropicCodec{}.ParseRequest(body)
	require.NoError(t, err)
	require.Len(t, canonical.Messages, 1)
	require.Len(t, canonical.Messages[0].Content, 2)

	img := canonical.Messages[0].Content[1]
	require.Equal(t, core.PartImage, img.Type)
	require.Equal(t, "image/jpeg", img.Media.MIMEType)
	require.Equal(t, "/9j/4AAQ", img.Media.Data)

	// Render to OpenAI.
	oaiBody, err := OpenAICodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var oaiReq map[string]any
	require.NoError(t, json.Unmarshal(oaiBody, &oaiReq))
	msgs := oaiReq["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 2)
	imgPart := content[1].(map[string]any)
	require.Equal(t, "image_url", imgPart["type"])
	url := imgPart["image_url"].(map[string]any)["url"].(string)
	require.Contains(t, url, "data:image/jpeg;base64,/9j/4AAQ")
}

// Anthropic base64 image → parse → render to Gemini → inlineData.
func TestCrossDialect_AnthropicImageToGemini(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4.5",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "describe"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "abc123"}}
			]}
		]
	}`)

	canonical, err := AnthropicCodec{}.ParseRequest(body)
	require.NoError(t, err)

	gemBody, err := GeminiCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var gemReq map[string]any
	require.NoError(t, json.Unmarshal(gemBody, &gemReq))
	contents := gemReq["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 2)
	inlineData := parts[1].(map[string]any)["inlineData"].(map[string]any)
	require.Equal(t, "image/png", inlineData["mimeType"])
	require.Equal(t, "abc123", inlineData["data"])
}

func TestCrossDialect_AnthropicImageURLToGemini(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4.5",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "describe"},
				{"type": "image", "source": {"type": "url", "url": "https://example.com/photo.jpg"}}
			]}
		]
	}`)

	canonical, err := AnthropicCodec{}.ParseRequest(body)
	require.NoError(t, err)

	gemBody, err := GeminiCodec{}.RenderRequest(canonical)
	require.NoError(t, err)
	var gemReq map[string]any
	require.NoError(t, json.Unmarshal(gemBody, &gemReq))
	contents := gemReq["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	fileData := parts[1].(map[string]any)["fileData"].(map[string]any)
	require.Equal(t, "https://example.com/photo.jpg", fileData["fileUri"])
}

// Anthropic URL image → parse → render to OpenAI → image_url with URL.
func TestCrossDialect_AnthropicImageURLToOpenAI(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4.5",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "what is this?"},
				{"type": "image", "source": {"type": "url", "url": "https://example.com/photo.jpg"}}
			]}
		]
	}`)

	canonical, err := AnthropicCodec{}.ParseRequest(body)
	require.NoError(t, err)
	img := canonical.Messages[0].Content[1]
	require.Equal(t, core.PartImage, img.Type)
	require.Equal(t, "https://example.com/photo.jpg", img.Media.URL)

	oaiBody, err := OpenAICodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var oaiReq map[string]any
	require.NoError(t, json.Unmarshal(oaiBody, &oaiReq))
	msgs := oaiReq["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)
	imgPart := content[1].(map[string]any)
	url := imgPart["image_url"].(map[string]any)["url"].(string)
	require.Equal(t, "https://example.com/photo.jpg", url)
}

// OpenAI image → render to Ollama → images array with base64.
func TestCrossDialect_OpenAIImageToOllama(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "describe"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,abc123"}}
			]}
		]
	}`)

	canonical, err := OpenAICodec{}.ParseRequest(body)
	require.NoError(t, err)

	ollBody, err := OllamaCodec{}.RenderRequest(canonical)
	require.NoError(t, err)

	var ollReq map[string]any
	require.NoError(t, json.Unmarshal(ollBody, &ollReq))
	msgs := ollReq["messages"].([]any)
	msg := msgs[0].(map[string]any)
	images := msg["images"].([]any)
	require.Len(t, images, 1)
	require.Equal(t, "abc123", images[0])
}
