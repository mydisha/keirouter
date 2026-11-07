package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const responsesWithDeveloper = `{"model":"m","instructions":"base rules","input":[
	{"type":"message","role":"developer","content":[{"type":"input_text","text":"sandbox: read-only"}]},
	{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]}]}`

func TestResponses_DeveloperItemsAreKept(t *testing.T) {
	req, err := OpenAIResponsesCodec{}.ParseRequest([]byte(responsesWithDeveloper))
	require.NoError(t, err)

	out, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(out), `"role":"developer"`)
	require.Contains(t, string(out), "sandbox: read-only")
}

func TestLeadingDeveloperMessages_JoinTheSystemPrompt(t *testing.T) {
	req, err := OpenAIResponsesCodec{}.ParseRequest([]byte(responsesWithDeveloper))
	require.NoError(t, err)

	ant, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(ant), `"system":"base rules\n\nsandbox: read-only"`)
	require.Equal(t, 1, strings.Count(string(ant), `"role":"user"`))

	gem, err := GeminiCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(gem), "sandbox: read-only")
	require.NotContains(t, string(gem), `"role":"developer"`)
}

func TestAnthropic_AdaptiveEffortSurvivesTheRoundTrip(t *testing.T) {
	body := `{"model":"claude-x","max_tokens":1000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},
		"messages":[{"role":"user","content":"hi"}]}`
	req, err := AnthropicCodec{}.ParseRequest([]byte(body))
	require.NoError(t, err)
	require.Equal(t, "high", req.Reasoning.Effort)

	out, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(out), `"thinking":{"type":"adaptive"}`)
	require.Contains(t, string(out), `"output_config":{"effort":"high"}`)

	req.Model = "gpt-5"
	oai, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)
	require.Contains(t, string(oai), `"reasoning_effort":"high"`)
}

func TestGemini_ThinkingConfigReachesOtherProviders(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"x"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":3000,"includeThoughts":true}}}`
	req, err := GeminiCodec{}.ParseRequest([]byte(body))
	require.NoError(t, err)
	require.Equal(t, 3000, req.Reasoning.MaxTokens)

	req.Model = "claude-sonnet-4-5"
	out, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(out), `"budget_tokens":3000`)

	off, err := GeminiCodec{}.ParseRequest([]byte(`{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`))
	require.NoError(t, err)
	require.Equal(t, "none", off.Reasoning.Effort)
}

func TestToolChoice_CrossesResponsesAndGemini(t *testing.T) {
	resp := `{"model":"m","input":"x","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"lookup"},"parallel_tool_calls":false}`
	req, err := OpenAIResponsesCodec{}.ParseRequest([]byte(resp))
	require.NoError(t, err)
	ant, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(ant), `"name":"lookup"`)
	require.Contains(t, string(ant), `"type":"tool"`)
	require.Contains(t, string(ant), `"disable_parallel_tool_use":true`)

	back, err := OpenAIResponsesCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(back), `"tool_choice":{"type":"function","name":"lookup"}`)

	gem := `{"contents":[{"role":"user","parts":[{"text":"x"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup"]}}}`
	req, err = GeminiCodec{}.ParseRequest([]byte(gem))
	require.NoError(t, err)
	oai, err := OpenAICodec{}.RenderRequestForProvider(req, "openai")
	require.NoError(t, err)
	var body struct {
		ToolChoice struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_choice"`
	}
	require.NoError(t, json.Unmarshal(oai, &body))
	require.Equal(t, "function", body.ToolChoice.Type)
	require.Equal(t, "lookup", body.ToolChoice.Function.Name)
}
