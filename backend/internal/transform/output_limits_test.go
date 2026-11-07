package transform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func antMaxTokens(t *testing.T, req *core.ChatRequest) int {
	t.Helper()
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	return int(decodeJSON(t, body)["max_tokens"].(float64))
}

func userTurn(model, text string) *core.ChatRequest {
	return &core.ChatRequest{Model: model, Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: text}}},
	}}
}

// A client that sends no max_tokens must not have long answers or large tool
// calls cut at 4096 tokens.
func TestAnthropic_DefaultMaxTokensFollowsTheModel(t *testing.T) {
	require.Equal(t, 32000, antMaxTokens(t, userTurn("claude-sonnet-4-5", "hi")))
	require.Equal(t, 32000, antMaxTokens(t, userTurn("claude-opus-4-1-20250805", "hi")))
	require.Equal(t, 4096, antMaxTokens(t, userTurn("claude-3-5-haiku-20241022", "hi")))
	require.Equal(t, 4096, antMaxTokens(t, userTurn("glm-4.6", "hi")))

	// The prompt and the output must fit a 200K window together.
	long := userTurn("claude-sonnet-4-5", strings.Repeat("lorem ipsum dolor sit amet ", 36000))
	n := antMaxTokens(t, long)
	require.Less(t, n, 32000)
	require.GreaterOrEqual(t, n, 4096)
	require.LessOrEqual(t, n+core.EstimatePromptTokens(long), 200000)

	explicit := userTurn("claude-sonnet-4-5", "hi")
	limit := 300
	explicit.MaxTokens = &limit
	require.Equal(t, 300, antMaxTokens(t, explicit))
}

func TestResponses_RendersMaxOutputTokens(t *testing.T) {
	limit := 500
	req := userTurn("gpt-5", "hi")
	req.MaxTokens = &limit

	body, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Equal(t, float64(500), decodeJSON(t, body)["max_output_tokens"])

	body, err = OpenAIResponsesCodec{Codex: true}.RenderRequest(req)
	require.NoError(t, err)
	require.NotContains(t, decodeJSON(t, body), "max_output_tokens")

	tiny := 1
	req.MaxTokens = &tiny
	body, err = OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Equal(t, float64(16), decodeJSON(t, body)["max_output_tokens"])
}

func TestAnthropic_ContextManagementNeedsOptIn(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","max_tokens":100,
	  "context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]},
	  "messages":[{"role":"user","content":"hi"}]}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)

	body, err := AnthropicCodec{ContextManagement: true}.RenderRequest(req)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"edits": []any{map[string]any{"type": "clear_tool_uses_20250919"}}}, decodeJSON(t, body)["context_management"])

	// A compatible host serving a claude-named model still rejects the field.
	body, err = AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.NotContains(t, decodeJSON(t, body), "context_management")
}
