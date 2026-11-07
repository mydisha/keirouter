package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

// TestOpenAI_RenderStreamChunk_Thinking verifies structured reasoning is
// emitted to the client as reasoning_content (issue #17: DeepSeek thinking
// mode requires it on follow-up turns).
func TestOpenAI_RenderStreamChunk_Thinking(t *testing.T) {
	state := &StreamState{Model: "deepseek-reasoner", MessageID: "id1"}
	events, err := OpenAICodec{}.RenderStreamChunk(
		core.StreamChunk{Type: core.ChunkThinking, Delta: "let me think"}, state)
	require.NoError(t, err)
	require.Len(t, events, 1)

	payload := strings.TrimPrefix(string(events[0]), "data: ")
	var got struct {
		Choices []struct {
			Delta struct {
				Role             string `json:"role"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(payload)), &got))
	require.Len(t, got.Choices, 1)
	require.Equal(t, "assistant", got.Choices[0].Delta.Role)
	require.Equal(t, "let me think", got.Choices[0].Delta.ReasoningContent)
}

// TestOpenAI_StreamReasoning_RoundTrip verifies a reasoning_content delta from
// upstream is parsed and re-rendered back to the client without loss.
func TestOpenAI_StreamReasoning_RoundTrip(t *testing.T) {
	line := []byte(`{"id":"c1","model":"deepseek-reasoner","choices":[{"delta":{"reasoning_content":"step one"}}]}`)
	chunks, err := OpenAICodec{}.ParseStreamLine(line, "deepseek-reasoner")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkThinking, chunks[0].Type)
	require.Equal(t, "step one", chunks[0].Delta)

	state := &StreamState{Model: "deepseek-reasoner"}
	events, err := OpenAICodec{}.RenderStreamChunk(chunks[0], state)
	require.NoError(t, err)
	require.Contains(t, string(events[0]), "reasoning_content")
	require.Contains(t, string(events[0]), "step one")
}

// TestOpenAI_RenderResponse_Reasoning verifies non-streaming responses surface
// reasoning_content for clients that replay it.
func TestOpenAI_RenderResponse_Reasoning(t *testing.T) {
	resp := &core.ChatResponse{
		Model: "deepseek-reasoner",
		Message: core.Message{
			Role: core.RoleAssistant,
			Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "internal reasoning"},
				{Type: core.PartText, Text: "the answer"},
			},
		},
		FinishReason: core.FinishStop,
	}
	body, err := OpenAICodec{}.RenderResponse(resp)
	require.NoError(t, err)

	var got struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Choices, 1)
	require.Equal(t, "the answer", got.Choices[0].Message.Content)
	require.Equal(t, "internal reasoning", got.Choices[0].Message.ReasoningContent)
}

// TestOpenAI_RenderRequest_InjectReasoningPlaceholder verifies the safety net:
// for DeepSeek targets, assistant messages without reasoning get a placeholder
// reasoning_content so the upstream doesn't reject the turn with a 400.
func TestOpenAI_RenderRequest_InjectReasoningPlaceholder(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-chat",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "continue"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	// Assistant message (index 1) should carry the placeholder.
	require.Equal(t, "assistant", got.Messages[1].Role)
	require.Equal(t, reasoningPlaceholder, got.Messages[1].ReasoningContent)
}

// TestOpenAI_RenderRequest_PreservesRealReasoning verifies genuine reasoning is
// kept intact (not overwritten by the placeholder).
func TestOpenAI_RenderRequest_PreservesRealReasoning(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-chat",
		Messages: []core.Message{
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "real chain of thought"},
				{Type: core.PartText, Text: "hello"},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "real chain of thought", got.Messages[0].ReasoningContent)
}

// TestOpenAI_RenderRequest_NoInjectForNonDeepSeek verifies non-DeepSeek targets
// are untouched (avoid sending reasoning_content to providers that reject it).
func TestOpenAI_RenderRequest_NoInjectForNonDeepSeek(t *testing.T) {
	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Empty(t, got.Messages[0].ReasoningContent)
}

// TestRequiresReasoningEcho covers provider- and model-based detection.
func TestRequiresReasoningEcho(t *testing.T) {
	require.True(t, requiresReasoningEcho("deepseek", "deepseek-chat"))
	require.True(t, requiresReasoningEcho("openrouter", "deepseek/deepseek-chat"))
	require.True(t, requiresReasoningEcho("siliconflow", "deepseek-ai/DeepSeek-V3.2"))
	require.False(t, requiresReasoningEcho("openai", "gpt-4o"))
	require.False(t, requiresReasoningEcho("groq", "llama-3.3-70b"))
}

func TestOpenAI_RenderRequest_DeepSeekToolCallFixes(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{Name: "get_weather", Arguments: json.RawMessage(`{"city":"Jakarta"}`)}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Messages, 3)
	assistant := got.Messages[1]
	require.Equal(t, reasoningPlaceholder, assistant.ReasoningContent)
	require.Len(t, assistant.ToolCalls, 1)
	require.Equal(t, "call_msg1_tc0_get_weather", assistant.ToolCalls[0].ID)
	require.Equal(t, "function", assistant.ToolCalls[0].Type)

	var args string
	require.NoError(t, json.Unmarshal(assistant.ToolCalls[0].Function.Arguments, &args))
	require.JSONEq(t, `{"city":"Jakarta"}`, args)
	require.Equal(t, "tool", got.Messages[2].Role)
	require.Equal(t, assistant.ToolCalls[0].ID, got.Messages[2].ToolCallID)
}

func TestOpenAI_RenderRequest_NonDeepSeekToolCallsUntouched(t *testing.T) {
	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{Name: "get_weather", Arguments: json.RawMessage(`{"city":"Jakarta"}`)}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Messages, 1)
	require.Empty(t, got.Messages[0].ReasoningContent)
	require.Empty(t, got.Messages[0].ToolCalls[0].ID)
}

func TestOpenAI_RenderRequest_DeepSeekV4ProAliases(t *testing.T) {
	cases := []struct {
		model        string
		wantEffort   string
		wantThinking string
	}{
		{model: "deepseek-v4-pro-max", wantEffort: "max", wantThinking: "enabled"},
		{model: "deepseek-v4-pro-none", wantEffort: "", wantThinking: "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			req := &core.ChatRequest{Model: tc.model, Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
			body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
			require.NoError(t, err)

			var got oaiRequest
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, "deepseek-v4-pro", got.Model)
			require.Equal(t, tc.wantEffort, got.ReasoningEffort)
			// The API reads thinking at the top level; an "extra_body" key is
			// only a Python SDK convention and would be ignored.
			require.NotNil(t, got.Thinking)
			require.Equal(t, tc.wantThinking, got.Thinking.Type)
			require.NotContains(t, string(body), "extra_body")
		})
	}
}

// TestOpenAI_RenderRequest_MultipleToolResultsSplit verifies that a canonical
// message containing multiple PartToolResult parts (as produced by Anthropic
// clients like Claude Code) is correctly split into separate OpenAI tool
// messages. This is the core fix for the "insufficient tool messages following
// tool_calls message" 400 error from DeepSeek.
func TestOpenAI_RenderRequest_MultipleToolResultsSplit(t *testing.T) {
	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "check both"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_2", Name: "read_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)}},
			}},
			// Anthropic groups both tool results into one user message.
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_1", Content: "contents of a"}},
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_2", Content: "contents of b"}},
				{Type: core.PartText, Text: "now summarize"},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequest(req)
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	// Expect: user, assistant(tool_calls), tool(call_1), tool(call_2), user(text)
	require.Len(t, got.Messages, 5, "expected 5 messages: user, assistant, tool, tool, user")

	// Verify both tool messages are present with correct IDs.
	toolMsgs := 0
	for _, m := range got.Messages {
		if m.Role != "tool" {
			continue
		}
		toolMsgs++
		switch m.ToolCallID {
		case "call_1":
			require.Contains(t, string(m.Content), "contents of a")
		case "call_2":
			require.Contains(t, string(m.Content), "contents of b")
		default:
			t.Errorf("unexpected tool_call_id: %s", m.ToolCallID)
		}
	}
	require.Equal(t, 2, toolMsgs, "expected exactly 2 tool messages")

	// Verify the trailing user text is preserved.
	lastMsg := got.Messages[4]
	require.Equal(t, "user", lastMsg.Role)
	require.Contains(t, string(lastMsg.Content), "now summarize")
}

// TestOpenAI_RenderRequest_DeepSeekMultipleToolCallsWithResults verifies the
// full DeepSeek path: multiple tool calls with all results present. The
// fillMissingDeepSeekToolResponses safety net should NOT insert any synthetic
// messages when all results exist.
func TestOpenAI_RenderRequest_DeepSeekMultipleToolCallsWithResults(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "check both"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a"}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_2", Name: "read_file", Arguments: json.RawMessage(`{"path":"b"}`)}},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_1", Content: "a-content"}},
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_2", Content: "b-content"}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	// Expect: user, assistant, tool(call_1), tool(call_2) — no synthetic fills.
	require.Len(t, got.Messages, 4, "should not insert synthetic messages when all results present")

	// Verify both tool results are present and match sanitized IDs.
	assistant := got.Messages[1]
	require.Len(t, assistant.ToolCalls, 2)
	id1 := assistant.ToolCalls[0].ID
	id2 := assistant.ToolCalls[1].ID

	responded := map[string]bool{}
	for _, m := range got.Messages {
		if m.Role == "tool" {
			responded[m.ToolCallID] = true
		}
	}
	require.True(t, responded[id1], "tool call 1 result missing")
	require.True(t, responded[id2], "tool call 2 result missing")
}

// TestOpenAI_RenderRequest_DeepSeekMultipleToolCallsPartialResults verifies
// that when an assistant makes multiple tool calls but only some have results,
// synthetic empty responses are inserted for the missing ones.
func TestOpenAI_RenderRequest_DeepSeekMultipleToolCallsPartialResults(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "check both"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_2", Name: "read_file", Arguments: json.RawMessage(`{}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_3", Name: "read_file", Arguments: json.RawMessage(`{}`)}},
			}},
			// Only call_2 has a result; call_1 and call_3 are missing.
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_2", Content: "found it"}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))

	assistant := got.Messages[1]
	require.Len(t, assistant.ToolCalls, 3)

	// Collect all tool messages and their IDs.
	toolResponses := map[string]string{}
	for _, m := range got.Messages {
		if m.Role == "tool" {
			toolResponses[m.ToolCallID] = string(m.Content)
		}
	}

	// All 3 tool calls should have responses (1 real + 2 synthetic).
	for _, tc := range assistant.ToolCalls {
		content, ok := toolResponses[tc.ID]
		require.True(t, ok, "missing tool response for ID %s", tc.ID)
		if tc.ID == assistant.ToolCalls[1].ID {
			require.Contains(t, content, "found it")
		}
	}
	require.Len(t, toolResponses, 3, "expected 3 total tool responses")
}

// TestOpenAI_RenderRequest_DeepSeekMultipleToolMessagesNotDuplicated verifies
// that when multiple tool messages follow an assistant message, the
// fillMissingDeepSeekToolResponses function correctly recognizes all of them
// and does not insert duplicate synthetic messages.
func TestOpenAI_RenderRequest_DeepSeekMultipleToolMessagesNotDuplicated(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "go"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_a", Name: "search", Arguments: json.RawMessage(`{}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_b", Name: "search", Arguments: json.RawMessage(`{}`)}},
			}},
			// Two separate tool messages (already in OpenAI format via canonical).
			{Role: core.RoleTool, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_a", Content: "result a"}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_b", Content: "result b"}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	// Expect: user, assistant, tool, tool — no duplicates.
	require.Len(t, got.Messages, 4, "should not duplicate existing tool messages")

	toolCount := 0
	for _, m := range got.Messages {
		if m.Role == "tool" {
			toolCount++
		}
	}
	require.Equal(t, 2, toolCount, "expected exactly 2 tool messages, no duplicates")
}

func TestOpenAI_RenderRequest_DeepSeekReasoningEffortMapping(t *testing.T) {
	cases := []struct {
		effort string
		want   string
	}{
		{effort: "low", want: "high"},
		{effort: "medium", want: "high"},
		{effort: "high", want: "high"},
		{effort: "xhigh", want: "max"},
		{effort: "max", want: "max"},
	}
	for _, tc := range cases {
		t.Run(tc.effort, func(t *testing.T) {
			req := &core.ChatRequest{Model: "deepseek-v4-flash", Reasoning: &core.ReasoningConfig{Effort: tc.effort}}
			body, err := OpenAICodec{}.RenderRequestForProvider(req, "custom-openai")
			require.NoError(t, err)

			var got oaiRequest
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, tc.want, got.ReasoningEffort)
			require.NotNil(t, got.Thinking)
			require.Equal(t, "enabled", got.Thinking.Type)
		})
	}
}

// ---- Kimi scope tests ----

// TestRequiresReasoningEcho_Kimi verifies Kimi models are detected (toolCalls scope).
func TestRequiresReasoningEcho_Kimi(t *testing.T) {
	require.True(t, requiresReasoningEcho("moonshot", "kimi-k2"))
	require.True(t, requiresReasoningEcho("openrouter", "moonshot/kimi-k2"))
	require.True(t, requiresReasoningEcho("custom", "kimi-latest"))
	require.False(t, requiresReasoningEcho("openai", "gpt-4o"))
}

// TestReasoningEchoScope_Kimi verifies scope classification: DeepSeek=all, Kimi=toolCalls.
func TestReasoningEchoScope_Kimi(t *testing.T) {
	require.Equal(t, reasoningAll, reasoningEchoScope("deepseek", "deepseek-chat"))
	require.Equal(t, reasoningAll, reasoningEchoScope("openrouter", "deepseek/deepseek-chat"))
	require.Equal(t, reasoningToolCalls, reasoningEchoScope("moonshot", "kimi-k2"))
	require.Equal(t, reasoningToolCalls, reasoningEchoScope("custom", "kimi-latest"))
	require.Equal(t, reasoningNone, reasoningEchoScope("openai", "gpt-4o"))
}

// TestOpenAI_RenderRequest_KimiInjectOnlyOnToolCalls verifies Kimi scope:
// reasoning_content is injected ONLY on assistant messages with tool_calls.
func TestOpenAI_RenderRequest_KimiInjectOnlyOnToolCalls(t *testing.T) {
	req := &core.ChatRequest{
		Model: "kimi-k2",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "call the tool"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{"q":"test"}`)}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "moonshot")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))

	var assistantMsgs []oaiMessage
	for _, m := range got.Messages {
		if m.Role == "assistant" {
			assistantMsgs = append(assistantMsgs, m)
		}
	}
	require.Len(t, assistantMsgs, 2)
	require.Empty(t, assistantMsgs[0].ReasoningContent, "Kimi: plain text assistant should not get reasoning")
	require.Equal(t, reasoningPlaceholder, assistantMsgs[1].ReasoningContent, "Kimi: tool_calls assistant should get reasoning")
}

// TestOpenAI_RenderRequest_KimiPreservesRealReasoning verifies genuine reasoning
// on Kimi messages is preserved (not overwritten by placeholder).
func TestOpenAI_RenderRequest_KimiPreservesRealReasoning(t *testing.T) {
	req := &core.ChatRequest{
		Model: "kimi-k2",
		Messages: []core.Message{
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "real reasoning"},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "test", Arguments: json.RawMessage(`{}`)}},
			}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "moonshot")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	for _, m := range got.Messages {
		if m.Role == "assistant" {
			require.Equal(t, "real reasoning", m.ReasoningContent)
		}
	}
}

// ---- Issue #17 scenario tests: chain + streaming + multi-turn reasoning ----

// TestOpenAI_StreamMultiTurnReasoning simulates a multi-turn streaming
// conversation with DeepSeek thinking mode (issue #17 scenario): turn 1
// produces reasoning, turn 2 sends it back. Verifies the full round-trip.
func TestOpenAI_StreamMultiTurnReasoning(t *testing.T) {
	codec := OpenAICodec{}

	// Turn 1: upstream streams reasoning + text
	streamLines := [][]byte{
		[]byte(`{"id":"c1","model":"deepseek-chat","choices":[{"delta":{"reasoning_content":"Let me think about this"}}]}`),
		[]byte(`{"id":"c1","model":"deepseek-chat","choices":[{"delta":{"content":"The answer is 42"}}]}`),
		[]byte(`{"id":"c1","model":"deepseek-chat","choices":[{"finish_reason":"stop"}]}`),
	}

	var allChunks []core.StreamChunk
	for _, line := range streamLines {
		chunks, err := codec.ParseStreamLine(line, "deepseek-chat")
		require.NoError(t, err)
		allChunks = append(allChunks, chunks...)
	}

	require.NotEmpty(t, allChunks)
	hasThinking, hasText := false, false
	for _, c := range allChunks {
		if c.Type == core.ChunkThinking {
			hasThinking = true
			require.Equal(t, "Let me think about this", c.Delta)
		}
		if c.Type == core.ChunkText {
			hasText = true
			require.Equal(t, "The answer is 42", c.Delta)
		}
	}
	require.True(t, hasThinking, "should capture reasoning_content from stream")
	require.True(t, hasText, "should capture text from stream")

	// Turn 2: client sends back reasoning_content on the assistant message
	turn2Req := &core.ChatRequest{
		Model: "deepseek-chat",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "what is the answer"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "Let me think about this"},
				{Type: core.PartText, Text: "The answer is 42"},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "explain why"}}},
		},
	}
	body, err := codec.RenderRequestForProvider(turn2Req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))

	for _, m := range got.Messages {
		if m.Role == "assistant" {
			require.Equal(t, "Let me think about this", m.ReasoningContent,
				"real reasoning should be preserved, not overwritten with placeholder")
		}
	}
}

// TestOpenAI_ChainFallbackReasoningInjection simulates a chain where fallback
// goes to DeepSeek. The request must have reasoning_content injected for the
// DeepSeek target even though the client originally sent it for a different
// provider. Mirrors the pipeline's cloneForAttempt + RenderRequestForProvider.
func TestOpenAI_ChainFallbackReasoningInjection(t *testing.T) {
	req := &core.ChatRequest{
		Model: "chain:coding",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "continue"}}},
		},
	}

	// Simulate cloneForAttempt with the DeepSeek fallback model
	fallbackReq := *req
	fallbackReq.Model = "deepseek-chat"
	body, err := OpenAICodec{}.RenderRequestForProvider(&fallbackReq, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))

	for _, m := range got.Messages {
		if m.Role == "assistant" {
			require.Equal(t, reasoningPlaceholder, m.ReasoningContent,
				"chain fallback to DeepSeek must inject reasoning_content")
		}
	}
}

// TestOpenAI_CrossDialectReasoningRoundTrip verifies reasoning survives a
// cross-dialect round-trip: PartThinking (from Anthropic client) →
// reasoning_content (for DeepSeek upstream). Covers the "Anthropic client →
// DeepSeek upstream" path via the canonical intermediate.
func TestOpenAI_CrossDialectReasoningRoundTrip(t *testing.T) {
	req := &core.ChatRequest{
		Model: "deepseek-chat",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "forecast"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "I should consider the weather"},
				{Type: core.PartText, Text: "It might rain today"},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "elaborate"}}},
		},
	}

	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))

	for _, m := range got.Messages {
		if m.Role == "assistant" {
			require.Equal(t, "I should consider the weather", m.ReasoningContent,
				"PartThinking must become reasoning_content for DeepSeek")
		}
	}
}

// TestOpenAI_RenderRequest_StreamRawPathReasoning verifies the zero-copy
// streaming path (StreamRaw → RenderRequestForProvider) also injects
// reasoning_content. Explicitly tested to prevent regressions in the fast path.
func TestOpenAI_RenderRequest_StreamRawPathReasoning(t *testing.T) {
	req := &core.ChatRequest{
		Model:  "deepseek-chat",
		Stream: true,
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "again"}}},
		},
	}
	// This is exactly what StreamRaw does internally
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.True(t, got.Stream, "stream flag should be set")

	for _, m := range got.Messages {
		if m.Role == "assistant" {
			require.Equal(t, reasoningPlaceholder, m.ReasoningContent,
				"StreamRaw path must inject reasoning for DeepSeek")
		}
	}
}

// ---- GLM/Zhipu scope tests ----

// TestReasoningEchoScope_GLM verifies GLM/Zhipu models are detected (all scope).
func TestReasoningEchoScope_GLM(t *testing.T) {
	require.Equal(t, reasoningAll, reasoningEchoScope("glm", "glm-5.2"))
	require.Equal(t, reasoningAll, reasoningEchoScope("glm-cn", "glm-4.7"))
	require.Equal(t, reasoningAll, reasoningEchoScope("zai", "zai-org/glm-5.2"))
	require.Equal(t, reasoningAll, reasoningEchoScope("cloudflare-ai", "@cf/zai-org/glm-5.2"))
	require.Equal(t, reasoningNone, reasoningEchoScope("openai", "gpt-4o"))
}

// TestReasoningEchoScope_MiniMax verifies MiniMax models are detected (all scope).
func TestReasoningEchoScope_MiniMax(t *testing.T) {
	require.Equal(t, reasoningAll, reasoningEchoScope("minimax", "minimax-m1"))
	require.Equal(t, reasoningAll, reasoningEchoScope("custom", "abab6.5-chat"))
	require.Equal(t, reasoningNone, reasoningEchoScope("openai", "gpt-4o"))
}

// TestOpenAI_RenderRequest_GLMThinkingConfig verifies GLM targets get
// thinking type + reasoning_effort forwarded (not just DeepSeek).
func TestOpenAI_RenderRequest_GLMThinkingConfig(t *testing.T) {
	maxTok := 4096
	req := &core.ChatRequest{
		Model:     "glm-5.2",
		MaxTokens: &maxTok,
		Reasoning: &core.ReasoningConfig{Effort: "high"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "glm")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotNil(t, got.Thinking, "GLM should get thinking config")
	require.Equal(t, "enabled", got.Thinking.Type)
	require.Equal(t, "high", got.ReasoningEffort)
}

// TestOpenAI_RenderRequest_GLMThinkingConfig_MaxEffort verifies max effort
// maps to reasoning_effort "max" for GLM.
func TestOpenAI_RenderRequest_GLMThinkingConfig_MaxEffort(t *testing.T) {
	req := &core.ChatRequest{
		Model:     "glm-5.2",
		Reasoning: &core.ReasoningConfig{Effort: "max"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "glm")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "max", got.ReasoningEffort)
}

// TestOpenAI_RenderRequest_GLMThinkingDisabled verifies "none" effort maps
// to thinking disabled for GLM.
func TestOpenAI_RenderRequest_GLMThinkingDisabled(t *testing.T) {
	req := &core.ChatRequest{
		Model:     "glm-5.2",
		Reasoning: &core.ReasoningConfig{Effort: "none"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "glm")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotNil(t, got.Thinking)
	require.Equal(t, "disabled", got.Thinking.Type)
	require.Empty(t, got.ReasoningEffort)
}

// TestOpenAI_RenderRequest_GLMReasoningInjection verifies GLM targets get
// reasoning_content injected on assistant turns (like DeepSeek).
func TestOpenAI_RenderRequest_GLMReasoningInjection(t *testing.T) {
	req := &core.ChatRequest{
		Model: "glm-5.2",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "continue"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "glm")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))

	for _, m := range got.Messages {
		if m.Role == "assistant" {
			require.Equal(t, reasoningPlaceholder, m.ReasoningContent,
				"GLM must inject reasoning_content on assistant turns")
		}
	}
}

// TestOpenAI_RenderRequest_NonReasoningProviderNoThinking verifies providers
// without reasoning scope (OpenAI, Groq, etc.) do NOT get thinking config.
func TestOpenAI_RenderRequest_NonReasoningProviderNoThinking(t *testing.T) {
	req := &core.ChatRequest{
		Model:     "gpt-4o",
		Reasoning: &core.ReasoningConfig{Effort: "high"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		},
	}
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)

	var got oaiRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Nil(t, got.Thinking, "OpenAI should not get thinking config")
	require.Empty(t, got.ReasoningEffort)
}
