package transform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

const personSchema = `{"type":"object","properties":{"person_name":{"type":"string"}},"required":["person_name"]}`

// structuredClientBodies are the same structured-output request in each
// client dialect.
var structuredClientBodies = map[core.Dialect]string{
	core.DialectOpenAI: `{"model":"m","messages":[{"role":"user","content":"who?"}],
		"response_format":{"type":"json_schema","json_schema":{"name":"person","strict":true,"schema":` + personSchema + `}}}`,
	core.DialectAnthropic: `{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"who?"}],
		"output_config":{"format":{"type":"json_schema","schema":` + personSchema + `}}}`,
	core.DialectOpenAIResponses: `{"model":"m","input":"who?",
		"text":{"format":{"type":"json_schema","name":"person","strict":true,"schema":` + personSchema + `}}}`,
	core.DialectGemini: `{"contents":[{"role":"user","parts":[{"text":"who?"}]}],
		"generationConfig":{"responseMimeType":"application/json","responseJsonSchema":` + personSchema + `}}`,
}

func parseStructured(t *testing.T, d core.Dialect) *core.ChatRequest {
	t.Helper()
	codec, err := DefaultRegistry().Codec(d)
	require.NoError(t, err)
	req, err := codec.ParseRequest([]byte(structuredClientBodies[d]))
	require.NoError(t, err)
	require.NotEmpty(t, req.ResponseFormat, "%s client format must reach the canonical request", d)
	req.Metadata.SourceDialect = d
	return req
}

func TestStructuredOutput_ReachesEveryProviderFamily(t *testing.T) {
	for d := range structuredClientBodies {
		t.Run(string(d), func(t *testing.T) {
			req := parseStructured(t, d)

			openai, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
			require.NoError(t, err)
			require.Contains(t, string(openai), `"response_format":{"type":"json_schema"`)
			require.Contains(t, string(openai), `person_name`)

			compat, err := OpenAICodec{}.RenderRequestForProvider(req, "deepseek")
			require.NoError(t, err)
			require.Contains(t, string(compat), `"response_format":{"type":"json_object"}`)
			require.Contains(t, string(compat), `person_name`, "the schema travels in the prompt")

			gemini, err := GeminiCodec{}.RenderRequest(req)
			require.NoError(t, err)
			require.Contains(t, string(gemini), `"responseMimeType":"application/json"`)
			require.Contains(t, string(gemini), `"responseJsonSchema"`)

			responses, err := OpenAIResponsesCodec{}.RenderRequest(req)
			require.NoError(t, err)
			require.Contains(t, string(responses), `"text":{"format":{"type":"json_schema"`)

			anthropic, err := AnthropicCodec{}.RenderRequest(req)
			require.NoError(t, err)
			if d == core.DialectAnthropic {
				require.Contains(t, string(anthropic), `"output_config":{"format":{"type":"json_schema","schema":`)
				require.NotContains(t, string(anthropic), StructuredOutputTool)
			} else {
				require.Contains(t, string(anthropic), `"tool_choice":{"type":"tool","name":"`+StructuredOutputTool+`"}`)
			}
		})
	}
}

func TestStructuredOutput_AnthropicUsesThePromptWhenThinkingOrToolsAreOn(t *testing.T) {
	req := parseStructured(t, core.DialectOpenAI)
	req.Reasoning = &core.ReasoningConfig{Effort: "high"}
	out, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.NotContains(t, string(out), StructuredOutputTool)
	require.Contains(t, string(out), `person_name`)

	req = parseStructured(t, core.DialectOpenAI)
	req.Tools = []core.Tool{{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}
	out, err = AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.NotContains(t, string(out), StructuredOutputTool)
	require.Contains(t, string(out), `"name":"lookup"`)
}

func TestStructuredOutput_GeminiOpenAPISchemaBecomesJSONSchema(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"x"}]}],"generationConfig":{"responseMimeType":"application/json",
		"responseSchema":{"type":"OBJECT","properties":{"n":{"type":"INTEGER"}}}}}`
	req, err := GeminiCodec{}.ParseRequest([]byte(body))
	require.NoError(t, err)
	require.Contains(t, string(req.ResponseFormat), `"type":"object"`)
	require.Contains(t, string(req.ResponseFormat), `"type":"integer"`)
}

func TestStructuredOutput_ForcedToolComesBackAsText(t *testing.T) {
	resp := &core.ChatResponse{
		Message: core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{
			Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "t", Name: StructuredOutputTool, Arguments: []byte(`{"person_name":"Ada"}`)},
		}}},
		FinishReason: core.FinishToolCalls,
	}
	UnwrapStructuredOutput(resp)
	require.Equal(t, core.PartText, resp.Message.Content[0].Type)
	require.Equal(t, `{"person_name":"Ada"}`, resp.Message.Content[0].Text)
	require.Equal(t, core.FinishStop, resp.FinishReason)
}

func TestStructuredOutput_ForcedToolStreamsAsText(t *testing.T) {
	lines := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"` + StructuredOutputTool + `","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"person_name\":"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Ada\"}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
	}
	var s StructuredOutputStream
	var text strings.Builder
	var finish core.FinishReason
	for _, l := range lines {
		chunks, err := AnthropicCodec{}.ParseStreamLine([]byte(l), "m")
		require.NoError(t, err)
		for _, c := range chunks {
			c, keep := s.Process(c)
			if !keep {
				continue
			}
			require.NotEqual(t, core.ChunkToolCall, c.Type)
			if c.Type == core.ChunkText {
				text.WriteString(c.Delta)
			}
			if c.Type == core.ChunkFinish {
				finish = c.FinishReason
			}
		}
	}
	require.Equal(t, `{"person_name":"Ada"}`, text.String())
	require.Equal(t, core.FinishStop, finish)
}

func TestEstimatePrompt_CountsTheResponseFormat(t *testing.T) {
	req := parseStructured(t, core.DialectOpenAI)
	with := core.EstimatePromptTokens(req)
	req.ResponseFormat = nil
	require.Greater(t, with, core.EstimatePromptTokens(req))
}
