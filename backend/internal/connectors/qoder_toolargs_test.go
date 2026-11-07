package connectors

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/transform"
)

func parseQoderLine(t *testing.T, line string) string {
	t.Helper()
	chunks, err := transform.OpenAICodec{}.ParseStreamLine([]byte(line), "qoder")
	require.NoError(t, err)
	chunks = unwrapQoderToolArgs(chunks)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].ToolCall)
	return string(chunks[0].ToolCall.Arguments)
}

func TestQoder_UnwrapsNestedToolArguments(t *testing.T) {
	cases := map[string]string{
		"nested object":          `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Bash","arguments":{"input":{"command":"pwd","description":"show cwd"}}}}]}}]}`,
		"nested string":          `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Bash","arguments":"{\"input\":{\"command\":\"pwd\",\"description\":\"show cwd\"}}"}}]}}]}`,
		"single unknown wrapper": `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Bash","arguments":{"toolCallInput":{"command":"pwd","description":"show cwd"}}}}]}}]}`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			require.JSONEq(t, `{"command":"pwd","description":"show cwd"}`, parseQoderLine(t, line))
		})
	}
}

func TestQoder_LeavesFragmentsAndPlainArgumentsAlone(t *testing.T) {
	fragment := `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":{\"comm"}}]}}]}`
	require.Equal(t, `{"input":{"comm`, parseQoderLine(t, fragment))

	plain := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"Bash","arguments":{"command":"ls","timeout":5}}}]}}]}`
	require.Equal(t, `{"command":"ls","timeout":5}`, parseQoderLine(t, plain))
}
