package transform

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The breakpoint must stay after the static block: moving it to the end of a
// prompt whose last block changes per request misses the cache every time.
func TestAnthropic_SystemBreakpointStaysOnItsBlock(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","max_tokens":10,"system":[
	  {"type":"text","text":"header"},
	  {"type":"text","text":"static rules","cache_control":{"type":"ephemeral"}},
	  {"type":"text","text":"today is monday"}],
	  "messages":[{"role":"user","content":"hi"}]}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)

	sys := decodeJSON(t, body)["system"].([]any)
	require.Len(t, sys, 3)
	require.Equal(t, "header", sys[0].(map[string]any)["text"])
	require.Nil(t, sys[0].(map[string]any)["cache_control"])
	require.Equal(t, "static rules", sys[1].(map[string]any)["text"])
	require.Equal(t, map[string]any{"type": "ephemeral"}, sys[1].(map[string]any)["cache_control"])
	require.Equal(t, "today is monday", sys[2].(map[string]any)["text"])
	require.Nil(t, sys[2].(map[string]any)["cache_control"])
}

func TestOpenAI_CacheControlReachesAnthropic(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","messages":[
	  {"role":"system","content":[{"type":"text","text":"rules","cache_control":{"type":"ephemeral"}}]},
	  {"role":"system","content":"env: linux"},
	  {"role":"user","content":[{"type":"text","text":"read it","cache_control":{"type":"ephemeral","ttl":"1h"}},
	    {"type":"text","text":"no cache","cache_control":null}]},
	  {"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},
	  {"role":"tool","tool_call_id":"c1","content":[{"type":"text","text":"file body","cache_control":{"type":"ephemeral"}}]}]}`
	req, err := OpenAICodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	body, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	m := decodeJSON(t, body)

	sys := m["system"].([]any)
	require.Len(t, sys, 2)
	require.Equal(t, "rules", sys[0].(map[string]any)["text"])
	require.Equal(t, map[string]any{"type": "ephemeral"}, sys[0].(map[string]any)["cache_control"])
	require.Equal(t, "env: linux", sys[1].(map[string]any)["text"])
	require.Nil(t, sys[1].(map[string]any)["cache_control"])

	msgs := m["messages"].([]any)
	user := msgs[0].(map[string]any)["content"].([]any)
	require.Equal(t, map[string]any{"type": "ephemeral", "ttl": "1h"}, user[0].(map[string]any)["cache_control"])
	_, hasNull := user[1].(map[string]any)["cache_control"]
	require.False(t, hasNull)

	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	require.Equal(t, "tool_result", result["type"])
	require.Equal(t, map[string]any{"type": "ephemeral"}, result["cache_control"])
}
