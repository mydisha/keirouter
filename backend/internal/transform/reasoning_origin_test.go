package transform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func renderStream(t *testing.T, codec StreamCodec, state *StreamState, chunks ...core.StreamChunk) string {
	t.Helper()
	var b strings.Builder
	for _, c := range chunks {
		frames, err := codec.RenderStreamChunk(c, state)
		require.NoError(t, err)
		for _, f := range frames {
			b.Write(f)
		}
	}
	return b.String()
}

// A signature from another provider family goes to the client tagged, and
// comes back with its origin, so a fallback target can tell it cannot verify it.
func TestAnthropic_ForeignSignatureIsTaggedAndComesBackWithItsOrigin(t *testing.T) {
	resp := &core.ChatResponse{Message: core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{
		{Type: core.PartThinking, Text: "plan", Signature: "gAAAAB", ReasoningOrigin: "openai"},
		{Type: core.PartText, Text: "done"},
	}}}
	body, err := AnthropicCodec{}.RenderResponse(resp)
	require.NoError(t, err)
	require.Contains(t, string(body), `"signature":"krsig1.openai.gAAAAB"`)

	in := `{"model":"claude-sonnet-4-5","max_tokens":10,"messages":[
	  {"role":"user","content":"hi"},
	  {"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"krsig1.openai.gAAAAB"},
	    {"type":"thinking","thinking":"own","signature":"EqQB"},{"type":"text","text":"done"}]},
	  {"role":"user","content":"next"}]}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	parts := req.Messages[1].Content
	require.Equal(t, "gAAAAB", parts[0].Signature)
	require.Equal(t, "openai", parts[0].ReasoningOrigin)
	require.Equal(t, "EqQB", parts[1].Signature)
	require.Equal(t, "anthropic", parts[1].ReasoningOrigin)
}

func TestAnthropicStream_TagsSignaturesFromAnotherFamily(t *testing.T) {
	state := &StreamState{MessageID: "msg_1", ReasoningOrigin: "glm"}
	out := renderStream(t, AnthropicCodec{}, state,
		core.StreamChunk{Type: core.ChunkThinking, Delta: "hmm"},
		core.StreamChunk{Type: core.ChunkThinking, Signature: "zsig"})
	require.Contains(t, out, `"signature":"krsig1.glm.zsig"`)

	state = &StreamState{MessageID: "msg_2", ReasoningOrigin: "anthropic"}
	out = renderStream(t, AnthropicCodec{}, state, core.StreamChunk{Type: core.ChunkThinking, Signature: "EqQB"})
	require.Contains(t, out, `"signature":"EqQB"`)
}

func TestResponses_EachReasoningItemKeepsItsOwnSignature(t *testing.T) {
	in := `{"model":"gpt-5","input":[
	  {"type":"message","role":"user","content":"go"},
	  {"type":"reasoning","summary":[{"type":"summary_text","text":"first"}],"encrypted_content":"enc1"},
	  {"type":"reasoning","summary":[{"type":"summary_text","text":"second"}],"encrypted_content":"krsig1.anthropic.EqQB"},
	  {"type":"function_call","call_id":"c1","name":"ls","arguments":"{}"},
	  {"type":"function_call_output","call_id":"c1","output":"a.txt"}]}`
	req, err := OpenAIResponsesCodec{}.ParseRequest([]byte(in))
	require.NoError(t, err)
	parts := req.Messages[1].Content
	require.Len(t, parts, 3)
	require.Equal(t, core.ContentPart{Type: core.PartThinking, Text: "first", Signature: "enc1", ReasoningOrigin: "openai"}, parts[0])
	require.Equal(t, core.ContentPart{Type: core.PartThinking, Text: "second", Signature: "EqQB", ReasoningOrigin: "anthropic"}, parts[1])
	require.Equal(t, core.PartToolCall, parts[2].Type)
}

func TestResponsesStream_ForwardsEncryptedReasoning(t *testing.T) {
	codec := OpenAIResponsesCodec{}
	var chunks []core.StreamChunk
	for _, line := range []string{
		`{"type":"response.reasoning_summary_text.delta","delta":"think"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"gAAAAB"}}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
	} {
		got, err := codec.ParseStreamLine([]byte(line), "gpt-5")
		require.NoError(t, err)
		chunks = append(chunks, got...)
	}
	require.Equal(t, core.StreamChunk{Type: core.ChunkThinking, Signature: "gAAAAB"}, chunks[1])

	out := renderStream(t, codec, &StreamState{MessageID: "resp_1", ReasoningOrigin: "openai"}, chunks...)
	require.Contains(t, out, `"encrypted_content":"gAAAAB"`)
	require.Contains(t, out, `"text":"think"`)
}

// Claude streams interleaved thinking as separate signed blocks; each must
// reach a Responses client as its own reasoning item.
func TestResponsesStream_OneReasoningItemPerSignedBlock(t *testing.T) {
	out := renderStream(t, OpenAIResponsesCodec{}, &StreamState{MessageID: "resp_2", ReasoningOrigin: "anthropic"},
		core.StreamChunk{Type: core.ChunkThinking, Delta: "a"},
		core.StreamChunk{Type: core.ChunkThinking, Signature: "s1"},
		core.StreamChunk{Type: core.ChunkThinking, Delta: "b"},
		core.StreamChunk{Type: core.ChunkThinking, Signature: "s2"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop})
	require.Contains(t, out, `"encrypted_content":"krsig1.anthropic.s1"`)
	require.Contains(t, out, `"encrypted_content":"krsig1.anthropic.s2"`)
	require.Contains(t, out, `"id":"rs_resp_2_0"`)
	require.Contains(t, out, `"id":"rs_resp_2_1"`)
	require.Equal(t, 2, strings.Count(out, `"type":"response.output_item.done"`))
}

func toolTurn(thinking core.ContentPart) *core.ChatRequest {
	return &core.ChatRequest{
		Model:     "claude-sonnet-4-5",
		Reasoning: &core.ReasoningConfig{MaxTokens: 2048},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "list files"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{thinking,
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "ls", Arguments: []byte(`{}`)}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "c1", Content: "a.txt"}}}},
		},
	}
}

func TestAnthropic_BudgetThinkingOffWhenTheToolTurnHasNoVerifiableThinking(t *testing.T) {
	body, err := AnthropicCodec{}.RenderRequest(toolTurn(core.ContentPart{Type: core.PartThinking, Text: "from a chat client"}))
	require.NoError(t, err)
	m := decodeJSON(t, body)
	require.Nil(t, m["thinking"])
	require.NotContains(t, string(body), `"type":"thinking"`)

	body, err = AnthropicCodec{}.RenderRequest(toolTurn(core.ContentPart{Type: core.PartThinking, Text: "own", Signature: "EqQB"}))
	require.NoError(t, err)
	require.NotNil(t, decodeJSON(t, body)["thinking"])

	// Other vendors behind the Anthropic API accept a tool turn without thinking.
	req := toolTurn(core.ContentPart{Type: core.PartThinking, Text: "x"})
	req.Model = "glm-4.6"
	body, err = AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.NotNil(t, decodeJSON(t, body)["thinking"])

	// Adaptive thinking may skip a turn, so a turn without thinking is valid.
	req = toolTurn(core.ContentPart{Type: core.PartThinking, Text: "x"})
	req.Model = "claude-opus-4-6"
	req.Reasoning = &core.ReasoningConfig{Adaptive: true}
	body, err = AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.NotNil(t, decodeJSON(t, body)["thinking"])
}
