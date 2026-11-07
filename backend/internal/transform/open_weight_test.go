package transform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
