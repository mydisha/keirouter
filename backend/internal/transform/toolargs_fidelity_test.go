package transform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Arguments chosen to break any decode/re-encode: a wrapper-looking key, an
// empty object, an integer above 2^53, HTML characters and unsorted keys.
const fidelityArgs = `{"table":"users","data":{"name":"a","opts":{}},"n":12345678901234567890,"html":"<b>&</b>","a":1}`

// decoderChunks returns the chunks each upstream decoder produces for one tool
// call carrying args, in that decoder's own shape.
func decoderChunks(t *testing.T, name, args string) []core.StreamChunk {
	t.Helper()
	var lines []string
	var codec StreamCodec
	switch name {
	case "openai":
		codec = OpenAICodec{}
		lines = append(lines, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"save","arguments":""}}]}}]}`)
		for _, frag := range splitEvery(args, 7) {
			lines = append(lines, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":`+jsonString(frag)+`}}]}}]}`)
		}
		lines = append(lines, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	case "anthropic":
		codec = AnthropicCodec{}
		lines = append(lines, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"save","input":{}}}`)
		for _, frag := range splitEvery(args, 7) {
			lines = append(lines, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+jsonString(frag)+`}}`)
		}
		lines = append(lines, `{"type":"content_block_stop","index":1}`)
		lines = append(lines, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
	case "gemini":
		codec = GeminiCodec{}
		lines = append(lines, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"save","args":`+args+`}}]},"finishReason":"STOP"}]}`)
	}
	var out []core.StreamChunk
	for _, l := range lines {
		chunks, err := codec.ParseStreamLine([]byte(l), "m")
		require.NoError(t, err)
		out = append(out, chunks...)
	}
	return out
}

func splitEvery(s string, n int) []string {
	var parts []string
	for len(s) > n {
		parts = append(parts, s[:n])
		s = s[n:]
	}
	return append(parts, s)
}

func jsonString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

func sanitizedToolCalls(chunks []core.StreamChunk, repairs bool) []core.ToolCall {
	s := NewToolArgSanitizer()
	s.ClaudeCodeRepairs = repairs
	var calls []core.ToolCall
	emit := func(c core.StreamChunk) {
		if c.Type == core.ChunkToolCall {
			calls = append(calls, *c.ToolCall)
		}
	}
	for _, c := range chunks {
		s.Process(c, emit)
	}
	s.Flush(emit)
	return calls
}

var decoderShapes = []string{"openai", "anthropic", "gemini"}

func TestToolArguments_ReachTheClientUnchangedForEveryDecoder(t *testing.T) {
	for _, shape := range decoderShapes {
		for _, repairs := range []bool{false, true} {
			calls := sanitizedToolCalls(decoderChunks(t, shape, fidelityArgs), repairs)
			require.Len(t, calls, 1, shape)
			if shape == "gemini" {
				// Gemini sends a complete object, which its decoder may reformat.
				require.JSONEq(t, fidelityArgs, string(calls[0].Arguments), shape)
				continue
			}
			require.Equal(t, fidelityArgs, string(calls[0].Arguments), "%s repairs=%v", shape, repairs)
		}
	}
}

func TestToolArgSanitizer_EmptyValuesInsideArgumentsSurvive(t *testing.T) {
	args := `{"opts":{},"list":[],"s":""}`
	for _, shape := range decoderShapes {
		calls := sanitizedToolCalls(decoderChunks(t, shape, args), false)
		require.Len(t, calls, 1, shape)
		require.JSONEq(t, args, string(calls[0].Arguments), shape)
	}
}

func TestToolArgSanitizer_ArgumentlessCallIsOneEmptyObject(t *testing.T) {
	for _, shape := range decoderShapes {
		calls := sanitizedToolCalls(decoderChunks(t, shape, `{}`), false)
		require.Len(t, calls, 1, shape)
		require.Equal(t, `{}`, string(calls[0].Arguments), shape)
	}
}

func TestToolArgSanitizer_EmptyObjectFragmentInsideArgumentsIsKept(t *testing.T) {
	s := NewToolArgSanitizer()
	var got string
	emit := func(c core.StreamChunk) {
		if c.Type == core.ChunkToolCall {
			got = string(c.ToolCall.Arguments)
		}
	}
	for _, frag := range []string{`{}`, `{"opts":`, `{}`, `}`} {
		s.Process(core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: "t1", Name: "x", Arguments: []byte(frag)}}, emit)
	}
	s.Flush(emit)
	require.Equal(t, `{"opts":{}}`, got)
}

func TestToolArgSanitizer_ClaudeCodeRepairOnlyWhenEnabled(t *testing.T) {
	args := `{"file_path":"/a.txt","limit":"100"}`
	chunk := core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: "t1", Name: "Read", Arguments: []byte(args)}}

	off := sanitizedToolCalls([]core.StreamChunk{chunk}, false)
	require.Equal(t, args, string(off[0].Arguments))

	on := sanitizedToolCalls([]core.StreamChunk{chunk}, true)
	require.JSONEq(t, `{"file_path":"/a.txt","limit":100}`, string(on[0].Arguments))
}

func TestToolArgSanitizer_RepairLeavesValidArgumentsByteIdentical(t *testing.T) {
	args := `{"limit":10,"file_path":"/a.txt"}`
	require.Equal(t, args, sanitizeToolArgs("Read", args))
	write := `{"file_path":"/a","content":{"not":"a string"}}`
	require.Equal(t, write, sanitizeToolArgs("Write", write), "a wrong type is left for the tool to reject")
}

func TestToolArgSanitizer_KeepsCustomFlag(t *testing.T) {
	chunks := []core.StreamChunk{
		{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "apply_patch", Custom: true, Arguments: []byte("*** Begin Patch")}},
		{Type: core.ChunkFinish, FinishReason: core.FinishToolCalls},
	}
	calls := sanitizedToolCalls(chunks, false)
	require.Len(t, calls, 1)
	require.True(t, calls[0].Custom)

	state := &StreamState{}
	var out strings.Builder
	s := NewToolArgSanitizer()
	for _, c := range chunks {
		s.Process(c, func(c core.StreamChunk) {
			events, err := OpenAIResponsesCodec{}.RenderStreamChunk(c, state)
			require.NoError(t, err)
			for _, e := range events {
				out.Write(e)
			}
		})
	}
	require.Contains(t, out.String(), `"custom_tool_call"`)
	require.NotContains(t, out.String(), `"function_call"`)
}

func TestToolArgSanitizer_FlushesInIndexOrder(t *testing.T) {
	var chunks []core.StreamChunk
	for i := 9; i >= 0; i-- {
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkToolCall, Index: i, ToolCall: &core.ToolCall{ID: "c" + string(rune('0'+i)), Name: "x", Arguments: []byte(`{}`)}})
	}
	calls := sanitizedToolCalls(chunks, false)
	require.Len(t, calls, 10)
	for i, c := range calls {
		require.Equal(t, "c"+string(rune('0'+i)), c.ID)
	}
}

func TestOpenAI_AssistantTextStaysBeforeToolCalls(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"Let me check.","tool_calls":[{"id":"c1","type":"function","function":{"name":"a","arguments":"{}"}},{"id":"c2","type":"function","function":{"name":"b","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"x"},{"role":"tool","tool_call_id":"c2","content":"y"}]}`
	req, err := OpenAICodec{}.ParseRequest([]byte(body))
	require.NoError(t, err)

	out, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	text := strings.Index(string(out), `"Let me check."`)
	tool := strings.Index(string(out), `"tool_use"`)
	require.True(t, text >= 0 && tool > text, "text must precede tool_use: %s", out)
}

func TestToolArguments_NeverReshapedInHistoryOrUnaryResponses(t *testing.T) {
	args := `{"table":"users","data":{"name":"a"}}`
	history := `{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"insert","arguments":` + jsonString(args) + `}}]}]}`
	req, err := OpenAICodec{}.ParseRequest([]byte(history))
	require.NoError(t, err)
	require.Equal(t, args, string(req.Messages[0].Content[0].ToolCall.Arguments))

	single := `{"options":{"depth":2}}`
	resp := `{"id":"r","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"scan","arguments":` + jsonString(single) + `}}]},"finish_reason":"tool_calls"}]}`
	parsed, err := OpenAICodec{}.ParseResponse([]byte(resp), "m")
	require.NoError(t, err)
	require.Equal(t, single, string(parsed.Message.Content[0].ToolCall.Arguments))
}

func TestStream_OpenAIToolIndexesCountFromZero(t *testing.T) {
	state := &StreamState{}
	var out strings.Builder
	for _, c := range []core.StreamChunk{
		{Type: core.ChunkText, Delta: "hi"},
		{Type: core.ChunkToolCall, Index: 2, ToolCall: &core.ToolCall{ID: "a", Name: "x", Arguments: []byte(`{}`)}},
		{Type: core.ChunkToolCall, Index: 3, ToolCall: &core.ToolCall{ID: "b", Name: "y", Arguments: []byte(`{}`)}},
	} {
		events, err := OpenAICodec{}.RenderStreamChunk(c, state)
		require.NoError(t, err)
		for _, e := range events {
			out.Write(e)
		}
	}
	require.Contains(t, out.String(), `"index":0,"id":"a"`)
	require.Contains(t, out.String(), `"index":1,"id":"b"`)
}
