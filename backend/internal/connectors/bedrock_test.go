package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
)

// ---- helpers ----------------------------------------------------------------

func bedrockTestCreds(base string) core.Credentials {
	return core.Credentials{
		APIKey:  "ABSK-test-key",
		BaseURL: base,
		Extra:   map[string]string{awsFieldAuth: awsAuthAPIKey, awsFieldRegion: "us-west-2"},
	}
}

// bedrockRecorder is a test upstream that records each request and answers
// with the handler's response.
type bedrockRecorder struct {
	mu   sync.Mutex
	reqs []bedrockRecorded
}

type bedrockRecorded struct {
	Method string
	URI    string
	Header http.Header
	Body   []byte
}

func (r *bedrockRecorder) last(t *testing.T) bedrockRecorded {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.reqs)
	return r.reqs[len(r.reqs)-1]
}

func newBedrockServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte)) (*httptest.Server, *bedrockRecorder) {
	t.Helper()
	rec := &bedrockRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, bedrockRecorded{Method: r.Method, URI: r.RequestURI, Header: r.Header.Clone(), Body: body})
		rec.mu.Unlock()
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func bedrockEvent(eventType, payload string) []byte {
	return encodeEventStreamFrameHeaders(map[string]string{
		":event-type": eventType, ":message-type": "event", ":content-type": "application/json",
	}, []byte(payload))
}

func bedrockExceptionFrame(excType, msg string) []byte {
	return encodeEventStreamFrameHeaders(map[string]string{
		":exception-type": excType, ":message-type": "exception", ":content-type": "application/json",
	}, []byte(`{"message":"`+msg+`"}`))
}

func writeBedrockFrames(w http.ResponseWriter, frames ...[]byte) {
	w.Header().Set("Content-Type", bedrockEventStream)
	for _, f := range frames {
		_, _ = w.Write(f)
	}
}

func collectBedrockChunks(t *testing.T, ch <-chan core.StreamChunk) []core.StreamChunk {
	t.Helper()
	var out []core.StreamChunk
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, c)
		case <-timeout:
			t.Fatal("stream did not close")
		}
	}
}

func bedrockUserText(text string) core.Message {
	return core.Message{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: text}}}
}

func bedrockIntPtr(v int) *int           { return &v }
func bedrockFloatPtr(v float64) *float64 { return &v }

const bedrockPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// ---- converse rendering & parsing ------------------------------------------

func TestBedrockConverseChatRendersAndParses(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output":{"message":{"role":"assistant","content":[
				{"reasoningContent":{"reasoningText":{"text":"think","signature":"sig-1"}}},
				{"text":"Hello"},
				{"toolUse":{"toolUseId":"tu_1","name":"get_weather","input":{"city":"Paris"}}}
			]}},
			"stopReason":"tool_use",
			"usage":{"inputTokens":10,"outputTokens":5,"totalTokens":40,"cacheReadInputTokens":20,"cacheWriteInputTokens":5}
		}`))
	})
	c := NewBedrock("bedrock", "https://bedrock-runtime.{aws_region}.amazonaws.com")
	model := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	req := &core.ChatRequest{
		Model:              model,
		System:             "be brief",
		SystemCacheControl: json.RawMessage(`{"type":"ephemeral"}`),
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartText, Text: "what is this?", CacheControl: json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)},
				{Type: core.PartImage, Media: &core.MediaPayload{MIMEType: "image/png", Data: bedrockPNG}},
				{Type: core.PartDocument, Media: &core.MediaPayload{MIMEType: "application/pdf", Data: "JVBERi0xLjQK"}},
			}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "prior", Signature: "sig-0"},
				{Type: core.PartThinking, Text: "unsigned from elsewhere"},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "tu_0", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Rome"}`)}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "tu_0", Content: "boom", IsError: true}},
			}},
			bedrockUserText("and now?"),
		},
		Tools: []core.Tool{
			{Name: "get_weather", Description: "weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)},
			{Name: "web_search", Raw: json.RawMessage(`{"type":"web_search_20250305","name":"web_search"}`)},
		},
		ToolChoice:  "required",
		Stop:        []string{"END"},
		Temperature: bedrockFloatPtr(0.3),
		MaxTokens:   bedrockIntPtr(512),
		Extra:       map[string]json.RawMessage{"top_k": json.RawMessage(`40`)},
	}

	resp, err := c.Chat(context.Background(), req, bedrockTestCreds(srv.URL))
	require.NoError(t, err)

	got := rec.last(t)
	require.Equal(t, "/model/us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse", got.URI)
	require.Equal(t, "Bearer ABSK-test-key", got.Header.Get("Authorization"))
	require.Equal(t, "application/json", got.Header.Get("Accept"))

	var body converseRequest
	require.NoError(t, json.Unmarshal(got.Body, &body))
	require.Len(t, body.System, 2)
	require.Equal(t, "be brief", body.System[0].Text)
	require.Equal(t, "default", body.System[1].CachePoint.Type)

	require.Len(t, body.Messages, 3, "the tool result and the next user text share one user turn")
	user := body.Messages[0].Content
	require.Equal(t, "what is this?", user[0].Text)
	require.Equal(t, "1h", user[1].CachePoint.TTL)
	require.Equal(t, "png", user[2].Image.Format)
	require.Equal(t, bedrockPNG, user[2].Image.Source.Bytes)
	require.Equal(t, "pdf", user[3].Document.Format)
	require.Equal(t, "document-1", user[3].Document.Name)

	asst := body.Messages[1].Content
	require.Len(t, asst, 2, "unsigned thinking is dropped")
	require.Equal(t, "sig-0", asst[0].ReasoningContent.ReasoningText.Signature)
	require.Equal(t, "get_weather", asst[1].ToolUse.Name)
	require.JSONEq(t, `{"city":"Rome"}`, string(asst[1].ToolUse.Input))

	toolTurn := body.Messages[2]
	require.Equal(t, "user", toolTurn.Role)
	require.Equal(t, "tu_0", toolTurn.Content[0].ToolResult.ToolUseID)
	require.Equal(t, "error", toolTurn.Content[0].ToolResult.Status)
	require.Equal(t, "boom", toolTurn.Content[0].ToolResult.Content[0].Text)
	require.Equal(t, "and now?", toolTurn.Content[1].Text)

	require.Len(t, body.ToolConfig.Tools, 1, "provider-native tools are dropped")
	require.Equal(t, "get_weather", body.ToolConfig.Tools[0].ToolSpec.Name)
	require.NotNil(t, body.ToolConfig.ToolChoice.Any)

	require.Equal(t, 512, *body.InferenceConfig.MaxTokens)
	require.InDelta(t, 0.3, *body.InferenceConfig.Temperature, 1e-9)
	require.Equal(t, []string{"END"}, body.InferenceConfig.StopSequences)
	require.EqualValues(t, 40, body.AdditionalModelRequestFields["top_k"])

	require.Equal(t, core.FinishToolCalls, resp.FinishReason)
	require.Len(t, resp.Message.Content, 3)
	require.Equal(t, core.PartThinking, resp.Message.Content[0].Type)
	require.Equal(t, "sig-1", resp.Message.Content[0].Signature)
	require.Equal(t, "Hello", resp.Message.Content[1].Text)
	tc := resp.Message.Content[2].ToolCall
	require.Equal(t, "tu_1", tc.ID)
	require.JSONEq(t, `{"city":"Paris"}`, string(tc.Arguments))
	require.Equal(t, core.Usage{PromptTokens: 35, CompletionTokens: 5, TotalTokens: 40, CachedTokens: 20, CacheWriteTokens: 5, Source: core.UsageSourceProvider}, resp.Usage)
}

func TestBedrockConverseThinkingBudgetAndForcedToolDowngrade(t *testing.T) {
	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{
		Model:       "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		Messages:    []core.Message{bedrockUserText("hi")},
		Tools:       []core.Tool{{Name: "f"}},
		ToolChoice:  map[string]any{"type": "function", "function": map[string]any{"name": "f"}},
		Temperature: bedrockFloatPtr(0.2),
		Reasoning:   &core.ReasoningConfig{Effort: "high"},
	}
	r, err := c.renderConverse(context.Background(), req, req.Model, bedrockTestCreds("http://x"))
	require.NoError(t, err)
	var body converseRequest
	require.NoError(t, json.Unmarshal(r.body, &body))

	thinking, _ := json.Marshal(body.AdditionalModelRequestFields["thinking"])
	require.JSONEq(t, `{"type":"enabled","budget_tokens":4096}`, string(thinking))
	require.Greater(t, *body.InferenceConfig.MaxTokens, 4096)
	require.Nil(t, body.InferenceConfig.Temperature, "Claude rejects sampling overrides while thinking")
	require.Nil(t, body.ToolConfig.ToolChoice, "forced tool use is downgraded to auto under thinking")
	require.JSONEq(t, `{"type":"object","properties":{}}`, string(body.ToolConfig.Tools[0].ToolSpec.InputSchema.JSON))
}

func TestBedrockConverseReasoningForOtherFamilies(t *testing.T) {
	c := NewBedrock("bedrock", "")
	cases := []struct {
		model string
		want  string
	}{
		{"us.amazon.nova-2-lite-v1:0", `{"reasoningConfig":{"type":"enabled","maxReasoningEffort":"high"}}`},
		{"openai.gpt-oss-120b-1:0", `{"reasoning_effort":"high"}`},
		{"us.openai.gpt-5.5", `{"reasoning":{"effort":"high"}}`},
		{"us.deepseek.r1-v1:0", `null`},
	}
	for _, tc := range cases {
		req := &core.ChatRequest{Model: tc.model, Messages: []core.Message{bedrockUserText("hi")}, Reasoning: &core.ReasoningConfig{Effort: "xhigh"}}
		r, err := c.renderConverse(context.Background(), req, tc.model, bedrockTestCreds("http://x"))
		require.NoError(t, err)
		var body converseRequest
		require.NoError(t, json.Unmarshal(r.body, &body))
		got, _ := json.Marshal(body.AdditionalModelRequestFields)
		require.JSONEq(t, tc.want, string(got), tc.model)
	}

	req := &core.ChatRequest{Model: "us.amazon.nova-pro-v1:0", Messages: []core.Message{bedrockUserText("hi")},
		Extra: map[string]json.RawMessage{"top_k": json.RawMessage(`5`)}}
	r, err := c.renderConverse(context.Background(), req, req.Model, bedrockTestCreds("http://x"))
	require.NoError(t, err)
	require.Contains(t, string(r.body), `"additionalModelRequestFields":{"inferenceConfig":{"topK":5}}`)
}

func TestBedrockConverseToolNamesAndForcedChoice(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[
			{"toolUse":{"toolUseId":"t1","name":"files_read","input":{}}}]}},"stopReason":"tool_use",
			"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`))
	})
	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{
		Model:      "mistral.mistral-large-2407-v1:0",
		Messages:   []core.Message{bedrockUserText("read")},
		Tools:      []core.Tool{{Name: "files.read", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "files.read"}},
	}
	resp, err := c.Chat(context.Background(), req, bedrockTestCreds(srv.URL))
	require.NoError(t, err)

	var body converseRequest
	require.NoError(t, json.Unmarshal(rec.last(t).Body, &body))
	require.Equal(t, "files_read", body.ToolConfig.Tools[0].ToolSpec.Name)
	require.Equal(t, "files_read", body.ToolConfig.ToolChoice.Tool.Name)
	require.Equal(t, "files.read", resp.Message.Content[0].ToolCall.Name)
}

func TestBedrockConverseToolChoiceNoneNeutralizesHistory(t *testing.T) {
	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{
		Model: "us.meta.llama3-3-70b-instruct-v1:0",
		Messages: []core.Message{
			bedrockUserText("q"),
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "c1", Name: "f", Arguments: json.RawMessage(`{"a":1}`)}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "c1", Content: "42"}}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "The answer is  \n"}}},
		},
		Tools:      []core.Tool{{Name: "f"}},
		ToolChoice: "none",
	}
	r, err := c.renderConverse(context.Background(), req, req.Model, bedrockTestCreds("http://x"))
	require.NoError(t, err)
	var body converseRequest
	require.NoError(t, json.Unmarshal(r.body, &body))
	require.Nil(t, body.ToolConfig)
	require.Len(t, body.Messages, 4)
	require.Contains(t, body.Messages[1].Content[0].Text, "[Called tool f")
	require.Contains(t, body.Messages[2].Content[0].Text, "42")
	require.Nil(t, body.Messages[2].Content[0].ToolResult)
	require.Equal(t, "The answer is", body.Messages[3].Content[0].Text, "assistant prefill is right-trimmed")
}

func TestBedrockConverseStructuredOutputViaTool(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[
			{"toolUse":{"toolUseId":"t1","name":"json_tool_call","input":{"answer":4}}}]}},"stopReason":"tool_use",
			"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`))
	})
	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{
		Model:          "us.amazon.nova-pro-v1:0",
		Messages:       []core.Message{bedrockUserText("2+2")},
		ResponseFormat: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"a","schema":{"type":"object","properties":{"answer":{"type":"integer"}}}}}`),
	}
	resp, err := c.Chat(context.Background(), req, bedrockTestCreds(srv.URL))
	require.NoError(t, err)
	var body converseRequest
	require.NoError(t, json.Unmarshal(rec.last(t).Body, &body))
	require.Equal(t, bedrockJSONTool, body.ToolConfig.ToolChoice.Tool.Name)
	require.Equal(t, core.FinishStop, resp.FinishReason)
	require.JSONEq(t, `{"answer":4}`, resp.Message.Content[0].Text)
}

func TestBedrockConverseRemoteImageIsInlined(t *testing.T) {
	httputil.SetAllowPrivateBaseURL(true)
	t.Cleanup(func() { httputil.SetAllowPrivateBaseURL(false) })
	png, _ := base64.StdEncoding.DecodeString(bedrockPNG)
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	t.Cleanup(img.Close)

	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{Model: "us.amazon.nova-lite-v1:0", Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{
		{Type: core.PartImage, Media: &core.MediaPayload{URL: img.URL + "/cat"}},
		{Type: core.PartImage, Media: &core.MediaPayload{URL: "s3://bucket/photo.jpg"}},
	}}}}
	r, err := c.renderConverse(context.Background(), req, req.Model, bedrockTestCreds("http://x"))
	require.NoError(t, err)
	var body converseRequest
	require.NoError(t, json.Unmarshal(r.body, &body))
	blocks := body.Messages[0].Content
	require.Equal(t, "png", blocks[0].Image.Format)
	require.Equal(t, bedrockPNG, blocks[0].Image.Source.Bytes)
	require.Equal(t, "jpeg", blocks[1].Image.Format)
	require.Equal(t, "s3://bucket/photo.jpg", blocks[1].Image.Source.S3Location.URI)

	// Without the private-network allowance the loopback URL is refused.
	httputil.SetAllowPrivateBaseURL(false)
	_, err = c.renderConverse(context.Background(), req, req.Model, bedrockTestCreds("http://x"))
	require.Equal(t, core.ErrBadRequest, core.AsProviderError(err).Kind)
}

func TestBedrockGuardrailConfig(t *testing.T) {
	c := NewBedrock("bedrock", "")
	creds := bedrockTestCreds("http://x")
	creds.Extra[bedrockFieldGuardrailID] = "gr-123"
	req := &core.ChatRequest{Model: "us.amazon.nova-pro-v1:0", Messages: []core.Message{bedrockUserText("hi")}}
	r, err := c.renderConverse(context.Background(), req, req.Model, creds)
	require.NoError(t, err)
	require.Contains(t, string(r.body), `"guardrailConfig":{"guardrailIdentifier":"gr-123","guardrailVersion":"DRAFT"}`)
}

// ---- endpoints & auth -------------------------------------------------------

func TestBedrockEndpoints(t *testing.T) {
	c := NewBedrock("bedrock", "https://bedrock-runtime.{aws_region}.amazonaws.com")
	creds := core.Credentials{Extra: map[string]string{awsFieldRegion: "eu-central-1"}}
	u, err := c.modelURL(creds, "arn:aws:bedrock:eu-central-1:123:inference-profile/eu.anthropic.claude-sonnet-4-6", "converse")
	require.NoError(t, err)
	require.Equal(t, "https://bedrock-runtime.eu-central-1.amazonaws.com/model/arn%3Aaws%3Abedrock%3Aeu-central-1%3A123%3Ainference-profile%2Feu.anthropic.claude-sonnet-4-6/converse", u)

	ctl, err := c.controlBase(creds)
	require.NoError(t, err)
	require.Equal(t, "https://bedrock.eu-central-1.amazonaws.com", ctl)

	// A runtime VPC endpoint keeps the public control plane.
	creds.BaseURL = "https://vpce-1.bedrock-runtime.eu-central-1.vpce.amazonaws.com"
	ctl, err = c.controlBase(creds)
	require.NoError(t, err)
	require.Equal(t, "https://bedrock.eu-central-1.amazonaws.com", ctl)

	creds.Extra[awsFieldRegion] = "us-east-1.evil.com/x"
	_, err = c.runtimeBase(creds)
	require.Error(t, err)
}

func TestBedrockSigV4SignsSentHeaders(t *testing.T) {
	creds := core.Credentials{Extra: map[string]string{
		awsFieldAuth: awsAuthAccessKey, awsFieldRegion: "us-east-1",
		awsFieldAccessKeyID: "AKIDEXAMPLE", awsFieldSecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}}
	var verifyErr error
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		ts, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		if err != nil {
			verifyErr = err
		} else {
			want, err := signAWSRequest(r.Method, "http://"+r.Host+r.RequestURI, body,
				map[string]string{"Content-Type": r.Header.Get("Content-Type"), "Accept": r.Header.Get("Accept")},
				awsCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
				"us-east-1", "bedrock", ts)
			if err != nil {
				verifyErr = err
			} else if want["Authorization"] != r.Header.Get("Authorization") {
				verifyErr = io.ErrUnexpectedEOF
			}
		}
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1}}`))
	})
	creds.BaseURL = srv.URL
	c := NewBedrock("bedrock", "")
	resp, err := c.Chat(context.Background(), &core.ChatRequest{Model: "amazon.nova-micro-v1:0", Messages: []core.Message{bedrockUserText("hi")}}, creds)
	require.NoError(t, err)
	require.NoError(t, verifyErr, "server-side signature recomputation must match")
	require.Equal(t, "ok", resp.Message.TextContent())
	auth := rec.last(t).Header.Get("Authorization")
	require.True(t, strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/"))
	require.Contains(t, auth, "/us-east-1/bedrock/aws4_request")
	require.Contains(t, auth, "SignedHeaders=accept;content-type;host;x-amz-date")
	require.Equal(t, "/model/amazon.nova-micro-v1%3A0/converse", rec.last(t).URI)
}

func TestBedrockHTTPErrorClassification(t *testing.T) {
	cases := []struct {
		status  int
		errType string
		body    string
		kind    core.ErrorKind
	}{
		{403, "AccessDeniedException", `{"message":"You don't have access to the model with the specified model ID."}`, core.ErrModelUnavailable},
		{403, "UnrecognizedClientException", `{"message":"The security token included in the request is invalid."}`, core.ErrAuth},
		{429, "ThrottlingException", `{"message":"Too many requests, please wait before trying again."}`, core.ErrRateLimit},
		{400, "ServiceQuotaExceededException", `{"message":"Too many tokens per day"}`, core.ErrRateLimit},
		{400, "ValidationException", `{"message":"The provided model identifier is invalid."}`, core.ErrModelUnavailable},
		{400, "ValidationException", `{"message":"Invocation of model ID x with on-demand throughput isn't supported."}`, core.ErrModelUnavailable},
		{400, "ValidationException", `{"message":"messages.0.content.0: text field is blank"}`, core.ErrBadRequest},
		{424, "ModelErrorException", `{"message":"model failed"}`, core.ErrUpstream},
		{408, "ModelTimeoutException", `{"message":"slow"}`, core.ErrTimeout},
	}
	for _, tc := range cases {
		srv, _ := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("X-Amzn-ErrorType", tc.errType+":http://internal.amazon.com/coral/com.amazon.bedrock/")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		c := NewBedrock("bedrock", "")
		_, err := c.Chat(context.Background(), &core.ChatRequest{Model: "amazon.nova-micro-v1:0", Messages: []core.Message{bedrockUserText("hi")}}, bedrockTestCreds(srv.URL))
		pe := core.AsProviderError(err)
		require.Equal(t, tc.kind, pe.Kind, tc.errType+" "+tc.body)
		require.Equal(t, tc.status, pe.StatusCode)
		require.NotContains(t, pe.Error(), "ABSK-test-key")
	}
}

func TestBedrockValidate(t *testing.T) {
	c := NewBedrock("bedrock", "")
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("X-Amzn-ErrorType", "ValidationException")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"The provided model identifier is invalid."}`))
	})
	require.NoError(t, c.Validate(context.Background(), bedrockTestCreds(srv.URL)))
	require.Equal(t, "Bearer ABSK-test-key", rec.last(t).Header.Get("Authorization"))

	bad, _ := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Authentication failed: Please make sure your API Key is valid."}`))
	})
	err := c.Validate(context.Background(), bedrockTestCreds(bad.URL))
	require.Error(t, err)
	require.Equal(t, core.ErrAuth, core.AsProviderError(err).Kind)

	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	require.Error(t, c.Validate(context.Background(), bedrockTestCreds(down.URL)))

	// Missing IAM keys fail before any request.
	err = c.Validate(context.Background(), core.Credentials{Extra: map[string]string{awsFieldAuth: awsAuthAccessKey}})
	require.Equal(t, core.ErrAuth, core.AsProviderError(err).Kind)
}

// ---- streaming --------------------------------------------------------------

func TestBedrockConverseStream(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		writeBedrockFrames(w,
			bedrockEvent("messageStart", `{"role":"assistant"}`),
			bedrockEvent("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"hmm"}}}`),
			bedrockEvent("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"sig"}}}`),
			bedrockEvent("contentBlockStop", `{"contentBlockIndex":0}`),
			bedrockEvent("contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"Hi"}}`),
			bedrockEvent("contentBlockStart", `{"contentBlockIndex":2,"start":{"toolUse":{"toolUseId":"tu_9","name":"lookup"}}}`),
			bedrockEvent("contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"{\"q\":"}}}`),
			bedrockEvent("contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"\"x\"}"}}}`),
			bedrockEvent("contentBlockStop", `{"contentBlockIndex":2}`),
			bedrockEvent("messageStop", `{"stopReason":"tool_use"}`),
			bedrockEvent("metadata", `{"usage":{"inputTokens":7,"outputTokens":3,"totalTokens":12,"cacheReadInputTokens":2},"metrics":{"latencyMs":5}}`),
		)
	})
	c := NewBedrock("bedrock", "")
	var ttft time.Duration
	ch, err := c.Stream(context.Background(), &core.ChatRequest{Model: "us.anthropic.claude-haiku-4-5-20251001-v1:0", Messages: []core.Message{bedrockUserText("hi")},
		Tools: []core.Tool{{Name: "lookup"}}}, bedrockTestCreds(srv.URL), core.StreamConfig{OnFirstChunk: func(d time.Duration) { ttft = d + 1 }})
	require.NoError(t, err)
	chunks := collectBedrockChunks(t, ch)

	got := rec.last(t)
	require.Equal(t, "/model/us.anthropic.claude-haiku-4-5-20251001-v1%3A0/converse-stream", got.URI)
	require.Equal(t, bedrockEventStream, got.Header.Get("Accept"))
	require.NotZero(t, ttft)

	require.Len(t, chunks, 8)
	require.Equal(t, core.StreamChunk{Type: core.ChunkThinking, Delta: "hmm"}, chunks[0])
	require.Equal(t, core.StreamChunk{Type: core.ChunkThinking, Signature: "sig"}, chunks[1])
	require.Equal(t, core.StreamChunk{Type: core.ChunkText, Delta: "Hi"}, chunks[2])
	require.Equal(t, core.ChunkToolCall, chunks[3].Type)
	require.Equal(t, 0, chunks[3].Index)
	require.Equal(t, "tu_9", chunks[3].ToolCall.ID)
	require.Equal(t, "lookup", chunks[3].ToolCall.Name)
	require.Equal(t, `{"q":`, string(chunks[4].ToolCall.Arguments))
	require.Equal(t, `"x"}`, string(chunks[5].ToolCall.Arguments))
	require.Equal(t, core.ChunkUsage, chunks[6].Type)
	require.Equal(t, 9, chunks[6].Usage.PromptTokens)
	require.Equal(t, 2, chunks[6].Usage.CachedTokens)
	require.Equal(t, core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishToolCalls}, chunks[7])
}

func TestBedrockStreamExceptionFrames(t *testing.T) {
	cases := []struct {
		excType string
		msg     string
		kind    core.ErrorKind
		status  int
	}{
		{"throttlingException", "Too many requests", core.ErrRateLimit, 429},
		{"validationException", "Input is too long for requested model.", core.ErrContextWindow, 400},
		{"validationException", "bad field", core.ErrBadRequest, 400},
		{"accessDeniedException", "not authorized", core.ErrAuth, 403},
		{"modelStreamErrorException", "model crashed", core.ErrUpstream, 424},
		{"serviceUnavailableException", "try later", core.ErrUpstream, 503},
		{"internalServerException", "oops", core.ErrUpstream, 500},
	}
	for _, tc := range cases {
		srv, _ := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
			writeBedrockFrames(w,
				bedrockEvent("messageStart", `{"role":"assistant"}`),
				bedrockEvent("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"partial"}}`),
				bedrockExceptionFrame(tc.excType, tc.msg),
			)
		})
		c := NewBedrock("bedrock", "")
		ch, err := c.Stream(context.Background(), &core.ChatRequest{Model: "amazon.nova-micro-v1:0", Messages: []core.Message{bedrockUserText("hi")}}, bedrockTestCreds(srv.URL), core.StreamConfig{})
		require.NoError(t, err)
		chunks := collectBedrockChunks(t, ch)
		require.Len(t, chunks, 2, tc.excType)
		require.Equal(t, core.ChunkText, chunks[0].Type)
		require.Equal(t, core.ChunkError, chunks[1].Type)
		pe := core.AsProviderError(chunks[1].Err)
		require.Equal(t, tc.kind, pe.Kind, tc.excType)
		require.Equal(t, tc.status, pe.StatusCode, tc.excType)
		require.Equal(t, "bedrock", pe.Provider)
		require.Equal(t, "amazon.nova-micro-v1:0", pe.Model)
		require.Contains(t, pe.Message, tc.msg)
	}
}

func TestBedrockStreamTruncatedAndFinishWithoutMetadata(t *testing.T) {
	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{Model: "amazon.nova-micro-v1:0", Messages: []core.Message{bedrockUserText("hi")}}

	truncated, _ := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		writeBedrockFrames(w, bedrockEvent("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"par"}}`))
	})
	ch, err := c.Stream(context.Background(), req, bedrockTestCreds(truncated.URL), core.StreamConfig{})
	require.NoError(t, err)
	chunks := collectBedrockChunks(t, ch)
	require.Equal(t, core.ChunkError, chunks[len(chunks)-1].Type)
	require.Equal(t, core.ErrUpstream, core.AsProviderError(chunks[len(chunks)-1].Err).Kind)

	noMeta, _ := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		writeBedrockFrames(w,
			bedrockEvent("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"done"}}`),
			bedrockEvent("messageStop", `{"stopReason":"max_tokens"}`))
	})
	ch, err = c.Stream(context.Background(), req, bedrockTestCreds(noMeta.URL), core.StreamConfig{})
	require.NoError(t, err)
	chunks = collectBedrockChunks(t, ch)
	require.Equal(t, core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishLength}, chunks[len(chunks)-1])
}

func TestBedrockStreamStopsOnCancel(t *testing.T) {
	release := make(chan struct{})
	srv, _ := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		writeBedrockFrames(w, bedrockEvent("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"a"}}`))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	c := NewBedrock("bedrock", "")
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.Stream(ctx, &core.ChatRequest{Model: "amazon.nova-micro-v1:0", Messages: []core.Message{bedrockUserText("hi")}}, bedrockTestCreds(srv.URL), core.StreamConfig{})
	require.NoError(t, err)
	first := <-ch
	require.Equal(t, "a", first.Delta)
	cancel()
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
}

// ---- InvokeModel ------------------------------------------------------------

func TestBedrockInvokeAnthropicChat(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"yo"}],
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2,"cache_read_input_tokens":1}}`))
	})
	c := NewBedrock("bedrock", "")
	req := &core.ChatRequest{Model: "invoke/us.anthropic.claude-sonnet-4-5-20250929-v1:0", Messages: []core.Message{bedrockUserText("hi")}, MaxTokens: bedrockIntPtr(100)}
	req.Metadata.ClientBetas = []string{"context-1m-2025-08-07", "files-api-2025-04-14"}
	resp, err := c.Chat(context.Background(), req, bedrockTestCreds(srv.URL))
	require.NoError(t, err)
	require.Equal(t, "yo", resp.Message.TextContent())
	require.Equal(t, req.Model, resp.Model)
	require.Equal(t, 5, resp.Usage.PromptTokens)

	got := rec.last(t)
	require.Equal(t, "/model/us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/invoke", got.URI)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got.Body, &body))
	require.JSONEq(t, `"bedrock-2023-05-31"`, string(body["anthropic_version"]))
	require.JSONEq(t, `["context-1m-2025-08-07"]`, string(body["anthropic_beta"]))
	require.NotContains(t, body, "model")
	require.NotContains(t, body, "stream")
	require.JSONEq(t, `100`, string(body["max_tokens"]))
}

func TestBedrockInvokeAnthropicStream(t *testing.T) {
	chunk := func(ev string) []byte {
		payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(ev))})
		return bedrockEvent("chunk", string(payload))
	}
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		writeBedrockFrames(w,
			chunk(`{"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}`),
			chunk(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			chunk(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`),
			chunk(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`),
			chunk(`{"type":"content_block_stop","index":0}`),
			chunk(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
			chunk(`{"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":3,"outputTokenCount":2}}`),
		)
	})
	c := NewBedrock("bedrock", "")
	ch, err := c.Stream(context.Background(), &core.ChatRequest{Model: "invoke/anthropic.claude-3-7-sonnet-20250219-v1:0", Messages: []core.Message{bedrockUserText("hi")}},
		bedrockTestCreds(srv.URL), core.StreamConfig{})
	require.NoError(t, err)
	chunks := collectBedrockChunks(t, ch)
	require.Equal(t, "/model/anthropic.claude-3-7-sonnet-20250219-v1%3A0/invoke-with-response-stream", rec.last(t).URI)

	var text strings.Builder
	for _, c := range chunks {
		require.NotEqual(t, core.ChunkError, c.Type)
		if c.Type == core.ChunkText {
			text.WriteString(c.Delta)
		}
	}
	require.Equal(t, "Hello", text.String())
	last := chunks[len(chunks)-1]
	require.Equal(t, core.ChunkFinish, last.Type, "finish is emitted after the usage chunks")
	require.Equal(t, core.ChunkUsage, chunks[len(chunks)-2].Type)
}

func TestBedrockInvokeRejectsNonAnthropic(t *testing.T) {
	c := NewBedrock("bedrock", "")
	_, err := c.Chat(context.Background(), &core.ChatRequest{Model: "invoke/meta.llama3-3-70b-instruct-v1:0", Messages: []core.Message{bedrockUserText("hi")}}, bedrockTestCreds("http://127.0.0.1:1"))
	require.Equal(t, core.ErrBadRequest, core.AsProviderError(err).Kind)
}

// ---- embeddings & discovery ------------------------------------------------

func TestBedrockEmbeddings(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch {
		case strings.Contains(r.RequestURI, "titan"):
			var in struct {
				InputText string `json:"inputText"`
			}
			_ = json.Unmarshal(body, &in)
			v := float32(len(in.InputText))
			_, _ = w.Write([]byte(`{"embedding":[` + bedrockJSONFloat(v) + `,0.5],"inputTextTokenCount":2}`))
		default:
			w.Header().Set("X-Amzn-Bedrock-Input-Token-Count", "9")
			_, _ = w.Write([]byte(`{"embeddings":{"float":[[1,2],[3,4]]}}`))
		}
	})
	c := NewBedrock("bedrock", "")

	resp, err := c.Embeddings(context.Background(), &core.EmbeddingRequest{Model: "amazon.titan-embed-text-v2:0", Input: []string{"a", "bbb", "cc"}, Dimensions: 256}, bedrockTestCreds(srv.URL))
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 0.5}, {3, 0.5}, {2, 0.5}}, resp.Vectors, "input order is preserved")
	require.Equal(t, 6, resp.Usage.PromptTokens)
	require.Contains(t, string(rec.last(t).Body), `"dimensions":256`)
	require.Contains(t, string(rec.last(t).Body), `"normalize":true`)

	resp, err = c.Embeddings(context.Background(), &core.EmbeddingRequest{Model: "us.cohere.embed-v4:0", Input: []string{"x", "y"}}, bedrockTestCreds(srv.URL))
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 2}, {3, 4}}, resp.Vectors)
	require.Equal(t, 9, resp.Usage.PromptTokens)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.last(t).Body, &body))
	require.Equal(t, "search_document", body["input_type"])
	require.Equal(t, []any{"float"}, body["embedding_types"])

	_, err = c.Embeddings(context.Background(), &core.EmbeddingRequest{Model: "amazon.nova-pro-v1:0", Input: []string{"x"}}, bedrockTestCreds(srv.URL))
	require.Equal(t, core.ErrCapability, core.AsProviderError(err).Kind)
}

func bedrockJSONFloat(v float32) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestBedrockListModels(t *testing.T) {
	srv, rec := newBedrockServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/foundation-models"):
			_, _ = w.Write([]byte(`{"modelSummaries":[
				{"modelId":"amazon.nova-pro-v1:0","modelName":"Nova Pro","providerName":"Amazon","outputModalities":["TEXT"],"inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"ACTIVE"}},
				{"modelId":"anthropic.claude-sonnet-4-6","modelName":"Claude Sonnet 4.6","providerName":"Anthropic","outputModalities":["TEXT"],"inferenceTypesSupported":["INFERENCE_PROFILE"]},
				{"modelId":"amazon.titan-embed-text-v2:0","modelName":"Titan Embeddings V2","providerName":"Amazon","outputModalities":["EMBEDDING"],"inferenceTypesSupported":["ON_DEMAND"]},
				{"modelId":"amazon.nova-canvas-v1:0","modelName":"Nova Canvas","providerName":"Amazon","outputModalities":["IMAGE"],"inferenceTypesSupported":["ON_DEMAND"]},
				{"modelId":"anthropic.claude-v2","modelName":"Claude 2","providerName":"Anthropic","outputModalities":["TEXT"],"inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"LEGACY"}}
			]}`))
		case r.URL.Query().Get("typeEquals") == "SYSTEM_DEFINED" && r.URL.Query().Get("nextToken") == "":
			_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[
				{"inferenceProfileId":"us.anthropic.claude-sonnet-4-6","inferenceProfileName":"US Claude Sonnet 4.6","status":"ACTIVE","type":"SYSTEM_DEFINED",
				 "models":[{"modelArn":"arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-4-6"}]}
			],"nextToken":"page2"}`))
		case r.URL.Query().Get("typeEquals") == "SYSTEM_DEFINED":
			_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[
				{"inferenceProfileId":"us.amazon.nova-canvas-v1:0","inferenceProfileName":"US Nova Canvas","status":"ACTIVE","type":"SYSTEM_DEFINED",
				 "models":[{"modelArn":"arn:aws:bedrock:us-east-1::foundation-model/amazon.nova-canvas-v1:0"}]}
			]}`))
		default:
			_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[
				{"inferenceProfileId":"abc123","inferenceProfileName":"team-claude","inferenceProfileArn":"arn:aws:bedrock:us-east-1:111:application-inference-profile/abc123","status":"ACTIVE","type":"APPLICATION"}
			]}`))
		}
	})
	c := NewBedrock("bedrock", "")
	models, err := c.ListModels(context.Background(), bedrockTestCreds(srv.URL))
	require.NoError(t, err)
	ids := map[string]core.ServiceKind{}
	for _, m := range models {
		ids[m.ID] = m.Kind
	}
	require.Equal(t, map[string]core.ServiceKind{
		"amazon.nova-pro-v1:0":                                               core.ServiceLLM,
		"amazon.titan-embed-text-v2:0":                                       core.ServiceEmbedding,
		"us.anthropic.claude-sonnet-4-6":                                     core.ServiceLLM,
		"arn:aws:bedrock:us-east-1:111:application-inference-profile/abc123": core.ServiceLLM,
	}, ids)
	require.Equal(t, "Bearer ABSK-test-key", rec.last(t).Header.Get("Authorization"))
}

func TestBedrockStaticModelsRegistered(t *testing.T) {
	models := ModelsForProvider("bedrock")
	require.NotEmpty(t, models)
	var sawClaude, sawEmb bool
	for _, m := range models {
		sawClaude = sawClaude || m.ID == "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
		sawEmb = sawEmb || (m.ID == "amazon.titan-embed-text-v2:0" && m.Kind == core.ServiceEmbedding)
	}
	require.True(t, sawClaude)
	require.True(t, sawEmb)
	require.True(t, DrivableDialect(core.DialectBedrock))
}
