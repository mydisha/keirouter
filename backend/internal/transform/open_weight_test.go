package transform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestAnthropic_ThinkingDisabledStaysOffUpstream(t *testing.T) {
	in := `{"model":"deepseek-v4-flash","max_tokens":100,"thinking":{"type":"disabled"},
	  "messages":[{"role":"user","content":"hi"}]}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)

	for _, provider := range []string{"deepseek", "zai", "moonshot"} {
		body, err := OpenAICodec{}.RenderRequestForProvider(req, provider)
		require.NoError(t, err)
		var got oaiRequest
		require.NoError(t, json.Unmarshal(body, &got))
		require.Empty(t, got.ReasoningEffort, provider)
		if got.Thinking != nil {
			require.Equal(t, "disabled", got.Thinking.Type, provider)
		}
	}

	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.NotContains(t, decodeJSON(t, body), "thinking")
}

func TestMoonshot_ClampsSamplingWithoutThinking(t *testing.T) {
	cases := []struct {
		temp float64
		n    string
		want float64
	}{
		{temp: 1.5, want: 1},
		{temp: 0.7, want: 0.7},
		{temp: 0, n: "3", want: 0.3},
		{temp: 0, n: "1", want: 0},
	}
	for _, tc := range cases {
		req := quirkReq("moonshot-v1-8k", "")
		req.Temperature = &tc.temp
		if tc.n != "" {
			req.Extra = map[string]json.RawMessage{"n": json.RawMessage(tc.n)}
		}
		body, err := OpenAICodec{}.RenderRequestForProvider(req, "moonshot")
		require.NoError(t, err)
		require.EqualValues(t, tc.want, decodeJSON(t, body)["temperature"], "temp=%v n=%s", tc.temp, tc.n)
	}
}

func TestMoonshot_RequiredToolChoiceBecomesPrompt(t *testing.T) {
	req := quirkReq("kimi-k2.6", "")
	req.Tools = []core.Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	req.ToolChoice = "required"
	body, err := OpenAICodec{}.RenderRequestForProvider(req, "moonshot")
	require.NoError(t, err)
	m := decodeJSON(t, body)
	require.NotContains(t, m, "tool_choice")
	msgs := m["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	require.Equal(t, "user", last["role"])
	require.Equal(t, moonshotRequiredToolPrompt, last["content"])

	// Other hosts serving Kimi keep the client's tool_choice.
	body, err = OpenAICodec{}.RenderRequestForProvider(req, "together")
	require.NoError(t, err)
	require.Equal(t, "required", decodeJSON(t, body)["tool_choice"])
}

func TestReasoningEchoScope_NamespacedKimi(t *testing.T) {
	for _, model := range []string{
		"moonshotai/Kimi-K2.6",
		"@cf/moonshotai/kimi-k2.6",
		"accounts/fireworks/models/kimi-k3",
		"moonshot/kimi-k2",
		"kimi-k2-thinking",
	} {
		require.Equal(t, reasoningToolCalls, reasoningEchoScope("together", model), model)
	}
	require.Equal(t, reasoningNone, reasoningEchoScope("together", "meta-llama/llama-kimi"))
}
