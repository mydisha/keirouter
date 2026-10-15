package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// watsonxIAMStub serves the IAM token endpoint, minting tok-1, tok-2, ...
func watsonxIAMStub(t *testing.T, wantKey string, calls *int32) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ibm:params:oauth:grant-type:apikey" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("apikey") != wantKey {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"errorCode":"BXNIM0415E","errorMessage":"Provided API key could not be found."}`)
			return
		}
		n := atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + string(rune('0'+n)), "token_type": "Bearer",
			"expires_in": 3600, "expiration": time.Now().Add(time.Hour).Unix(),
		})
	}))
	t.Cleanup(srv.Close)
	prev := watsonxIAMTokenURL
	watsonxIAMTokenURL = srv.URL
	t.Cleanup(func() { watsonxIAMTokenURL = prev })
}

func watsonxTestCreds(base, key string) core.Credentials {
	return core.Credentials{BaseURL: base, APIKey: key, Extra: map[string]string{
		watsonxFieldProjectID: "proj-1",
	}}
}

func watsonxTestRequest() *core.ChatRequest {
	maxTokens := 50
	return &core.ChatRequest{
		Model:     "ibm/granite-3-3-8b-instruct",
		System:    "be brief",
		MaxTokens: &maxTokens,
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
		},
		Tools: []core.Tool{{Name: "lookup",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"strict":true,"properties":{"q":{"type":"object","additionalProperties":false}}}`)}},
		ToolChoice: "required",
	}
}

func TestWatsonxBaseURL(t *testing.T) {
	c := NewWatsonx("watsonx", "https://us-south.ml.cloud.ibm.com")
	cases := map[string]string{
		"":           "https://us-south.ml.cloud.ibm.com",
		"eu-de":      "https://eu-de.ml.cloud.ibm.com",
		"jp-tok":     "https://jp-tok.ml.cloud.ibm.com",
		"ap-south-1": "https://ap-south-1.aws.wxai.ibm.com",
	}
	for region, want := range cases {
		got, err := c.baseURL(core.Credentials{Extra: map[string]string{watsonxFieldRegion: region}})
		if err != nil || got != want {
			t.Errorf("region %q: got %q, %v; want %q", region, got, err, want)
		}
	}
	got, err := c.baseURL(core.Credentials{Extra: map[string]string{watsonxFieldRegion: "custom", watsonxFieldURL: "https://cpd.example.com/"}})
	if err != nil || got != "https://cpd.example.com" {
		t.Errorf("custom: %q, %v", got, err)
	}
	if _, err := c.baseURL(core.Credentials{Extra: map[string]string{watsonxFieldRegion: "custom"}}); err == nil {
		t.Error("custom region without URL must fail")
	}
	if _, err := c.baseURL(core.Credentials{Extra: map[string]string{watsonxFieldRegion: "evil.com/x"}}); err == nil {
		t.Error("malformed region must fail")
	}
	got, _ = c.baseURL(core.Credentials{BaseURL: "https://proxy.example.com/", Extra: map[string]string{watsonxFieldRegion: "eu-de"}})
	if got != "https://proxy.example.com" {
		t.Errorf("base override = %q", got)
	}
}

func TestWatsonxChatIAM(t *testing.T) {
	var iamCalls int32
	watsonxIAMStub(t, "key-chat", &iamCalls)

	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ml/v1/text/chat" || r.URL.Query().Get("version") != "2024-03-13" {
			t.Errorf("url = %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chat-1","model_id":"ibm/granite-3-3-8b-instruct","model":"ibm/granite-3-3-8b-instruct",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Hello there"},"finish_reason":"max_tokens"}],
			"usage":{"prompt_tokens":11,"completion_tokens":50,"total_tokens":61}}`)
	}))
	defer srv.Close()

	c := NewWatsonx("watsonx", "")
	creds := watsonxTestCreds(srv.URL, "key-chat")
	for i := 0; i < 2; i++ {
		resp, err := c.Chat(context.Background(), watsonxTestRequest(), creds)
		if err != nil {
			t.Fatal(err)
		}
		if resp.FinishReason != core.FinishLength || resp.Usage.CompletionTokens != 50 {
			t.Errorf("finish/usage = %q/%+v", resp.FinishReason, resp.Usage)
		}
		if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "Hello there" {
			t.Errorf("content = %+v", resp.Message.Content)
		}
	}
	if iamCalls != 1 {
		t.Errorf("IAM exchanges = %d, want 1 (token must be cached)", iamCalls)
	}

	b := bodies[0]
	if b["model_id"] != "ibm/granite-3-3-8b-instruct" || b["project_id"] != "proj-1" {
		t.Errorf("model/project = %v", b)
	}
	for _, k := range []string{"model", "stream", "tool_choice", "space_id"} {
		if _, ok := b[k]; ok {
			t.Errorf("unexpected member %q in %v", k, b)
		}
	}
	if b["tool_choice_option"] != "required" || b["max_tokens"] != float64(50) {
		t.Errorf("tool_choice_option/max_tokens = %v/%v", b["tool_choice_option"], b["max_tokens"])
	}
	tools, _ := json.Marshal(b["tools"])
	if strings.Contains(string(tools), "additionalProperties") || strings.Contains(string(tools), "strict") {
		t.Errorf("tool schema not sanitized: %s", tools)
	}
	msgs, _ := b["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %v", b["messages"])
	}
}

func TestWatsonxRefreshesTokenOn401(t *testing.T) {
	var iamCalls int32
	watsonxIAMStub(t, "key-refresh", &iamCalls)

	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatCalls, 1)
		if r.Header.Get("Authorization") == "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"errors":[{"code":"authentication_token_expired","message":"token expired"}],"status_code":401}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	resp, err := NewWatsonx("watsonx", "").Chat(context.Background(), watsonxTestRequest(), watsonxTestCreds(srv.URL, "key-refresh"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content[0].Text != "ok" || iamCalls != 2 || chatCalls != 2 {
		t.Errorf("text=%q iam=%d chat=%d", resp.Message.Content[0].Text, iamCalls, chatCalls)
	}
}

func TestWatsonxCP4DAuth(t *testing.T) {
	var authCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/icp4d-api/v1/authorize":
			atomic.AddInt32(&authCalls, 1)
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["username"] != "admin" || b["api_key"] != "cpd-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"_messageCode_":"200","message":"Success","token":"cpd-token"}`)
		case "/ml/v1/text/chat":
			if r.Header.Get("Authorization") != "Bearer cpd-token" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"cpd"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	creds := core.Credentials{APIKey: "cpd-key", Extra: map[string]string{
		watsonxFieldRegion: "custom", watsonxFieldURL: srv.URL, watsonxFieldAuth: "cp4d",
		watsonxFieldUsername: "admin", watsonxFieldSpaceID: "space-9", watsonxFieldAPIVersion: "2025-02-01",
	}}
	resp, err := NewWatsonx("watsonx", "").Chat(context.Background(), watsonxTestRequest(), creds)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content[0].Text != "cpd" || authCalls != 1 {
		t.Errorf("text=%q auth calls=%d", resp.Message.Content[0].Text, authCalls)
	}

	bad := creds
	bad.APIKey = "wrong"
	_, err = NewWatsonx("watsonx", "").Chat(context.Background(), watsonxTestRequest(), bad)
	if pe := core.AsProviderError(err); pe == nil || pe.Kind != core.ErrAuth {
		t.Errorf("bad cp4d key err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Error("error leaks the API key")
	}
}

func TestWatsonxDeploymentStream(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ml/v1/deployments/dep-1/text/chat_stream" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer static-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"id":"c","model_id":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`{"id":"c","choices":[{"index":0,"delta":{"content":"Hi "}}]}`,
			`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"chatcmpl-tool-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]}}]}`,
			`{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		}
		for i, e := range events {
			_, _ = io.WriteString(w, "id: "+string(rune('1'+i))+"\nevent: message\ndata: "+e+"\n\n")
		}
		_, _ = io.WriteString(w, "id: 5\nevent: close\n\n")
	}))
	defer srv.Close()

	req := watsonxTestRequest()
	req.Model = "deployment/dep-1"
	req.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}
	creds := core.Credentials{BaseURL: srv.URL, APIKey: "static-token", Extra: map[string]string{watsonxFieldAuth: "bearer"}}
	ch, err := NewWatsonx("watsonx", "").Stream(context.Background(), req, creds, core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []core.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}
	for _, k := range []string{"model_id", "project_id", "space_id", "tool_choice_option", "stream"} {
		if _, ok := body[k]; ok {
			t.Errorf("deployment body carries %q: %v", k, body)
		}
	}
	if tc, _ := body["tool_choice"].(map[string]any); tc["type"] != "function" {
		t.Errorf("named tool_choice = %v", body["tool_choice"])
	}

	var text string
	var tool *core.ToolCall
	for _, c := range chunks {
		switch c.Type {
		case core.ChunkText:
			text += c.Delta
		case core.ChunkToolCall:
			tool = c.ToolCall
		case core.ChunkError:
			t.Fatalf("error chunk: %v", c.Err)
		}
	}
	if text != "Hi " || tool == nil || tool.Name != "lookup" || string(tool.Arguments) != `{"q":1}` {
		t.Errorf("text=%q tool=%+v", text, tool)
	}
	n := len(chunks)
	if n < 2 || chunks[n-2].Type != core.ChunkUsage || chunks[n-1].Type != core.ChunkFinish || chunks[n-1].FinishReason != core.FinishToolCalls {
		t.Fatalf("stream must end with usage + finish: %+v", chunks)
	}
}

func TestWatsonxStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: 1\nevent: message\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
		_, _ = io.WriteString(w, "id: 2\nevent: error\ndata: {\"errors\":[{\"code\":\"rate_limit_exceeded\",\"message\":\"Too many requests\"}],\"trace\":\"abc\",\"status_code\":429}\n\n")
	}))
	defer srv.Close()
	creds := watsonxTestCreds(srv.URL, "tok")
	creds.Extra[watsonxFieldAuth] = "bearer"
	ch, err := NewWatsonx("watsonx", "").Stream(context.Background(), watsonxTestRequest(), creds, core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var last core.StreamChunk
	for c := range ch {
		last = c
	}
	pe := core.AsProviderError(last.Err)
	if last.Type != core.ChunkError || pe.Kind != core.ErrRateLimit || pe.StatusCode != 429 || pe.Provider != "watsonx" {
		t.Fatalf("last = %+v err = %+v", last, pe)
	}
}

func TestWatsonxHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"errors":[{"code":"model_not_supported","message":"Model 'x' is not supported"}],"status_code":404}`)
	}))
	defer srv.Close()
	creds := watsonxTestCreds(srv.URL, "tok")
	creds.Extra[watsonxFieldAuth] = "bearer"
	_, err := NewWatsonx("watsonx", "").Chat(context.Background(), watsonxTestRequest(), creds)
	if pe := core.AsProviderError(err); pe == nil || pe.Kind != core.ErrModelUnavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestWatsonxRequiresProjectOrSpace(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	creds := core.Credentials{BaseURL: srv.URL, APIKey: "tok", Extra: map[string]string{watsonxFieldAuth: "bearer"}}
	c := NewWatsonx("watsonx", "")
	_, err := c.Chat(context.Background(), watsonxTestRequest(), creds)
	if err == nil || !strings.Contains(err.Error(), "project ID or deployment space ID") {
		t.Fatalf("chat err = %v", err)
	}
	if err := c.Validate(context.Background(), creds); err == nil || !strings.Contains(err.Error(), "project ID") {
		t.Fatalf("validate err = %v", err)
	}
	if called {
		t.Error("request sent without a project or space")
	}
}

func TestWatsonxValidate(t *testing.T) {
	var iamCalls int32
	watsonxIAMStub(t, "key-validate", &iamCalls)

	var reply atomic.Value
	reply.Store(`{"errors":[{"code":"model_not_supported","message":"Model 'ibm/granite-4-h-small' is not supported in this region"}],"status_code":404}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["max_tokens"] != float64(1) {
			t.Errorf("probe max_tokens = %v", b["max_tokens"])
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, reply.Load().(string))
	}))
	defer srv.Close()

	c := NewWatsonx("watsonx", "")
	if err := c.Validate(context.Background(), watsonxTestCreds(srv.URL, "key-validate")); err != nil {
		t.Errorf("unsupported probe model must still validate: %v", err)
	}
	reply.Store(`{"errors":[{"code":"project_not_found","message":"Project with ID 'proj-1' was not found"}],"status_code":404}`)
	if err := c.Validate(context.Background(), watsonxTestCreds(srv.URL, "key-validate")); err == nil {
		t.Error("unknown project must fail validation")
	}
	err := c.Validate(context.Background(), watsonxTestCreds(srv.URL, "bad-key"))
	if err == nil || core.AsProviderError(err).Kind != core.ErrAuth {
		t.Errorf("bad IAM key err = %v", err)
	}
}

func TestWatsonxListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/ml/v1/foundation_model_specs" || q.Get("filters") != "function_text_chat" ||
			q.Get("limit") != "200" || q.Get("version") != "2024-03-13" {
			t.Errorf("url = %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"total_count":3,"resources":[
			{"model_id":"ibm/granite-3-3-8b-instruct","label":"granite-3-3-8b-instruct","lifecycle":[{"id":"available","start_date":"2025-04-16"}]},
			{"model_id":"meta-llama/llama-2-13b-chat","label":"llama-2-13b-chat","lifecycle":[{"id":"available","start_date":"2023-11-09"},{"id":"withdrawn","start_date":"2024-06-01"}]},
			{"model_id":"openai/gpt-oss-120b","lifecycle":[{"id":"available","start_date":"2025-08-01"},{"id":"withdrawn","start_date":"2999-01-01"}]}]}`)
	}))
	defer srv.Close()
	creds := watsonxTestCreds(srv.URL, "tok")
	creds.Extra[watsonxFieldAuth] = "bearer"
	models, err := NewWatsonx("watsonx", "").ListModels(context.Background(), creds)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "ibm/granite-3-3-8b-instruct" || models[0].Name != "granite-3-3-8b-instruct" ||
		models[1].ID != "openai/gpt-oss-120b" || models[1].Name != "openai/gpt-oss-120b" {
		t.Errorf("models = %+v", models)
	}
}

func TestWatsonxRenderStripsReasoningAndMapsEffort(t *testing.T) {
	c := NewWatsonx("watsonx", "")
	req := &core.ChatRequest{
		Model:     "openai/gpt-oss-120b",
		Reasoning: &core.ReasoningConfig{Effort: "xhigh"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "q"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartThinking, Text: "hmm"}, {Type: core.PartText, Text: "a"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "q2"}}},
		},
		Extra: map[string]json.RawMessage{"seed": json.RawMessage(`7`), "user": json.RawMessage(`"u"`)},
	}
	body, err := c.render(req, core.Credentials{Extra: map[string]string{watsonxFieldSpaceID: "sp"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	_ = json.Unmarshal(body, &b)
	if b["reasoning_effort"] != "high" || b["space_id"] != "sp" || b["seed"] != float64(7) {
		t.Errorf("body = %s", body)
	}
	if _, ok := b["user"]; ok || strings.Contains(string(body), "reasoning_content") {
		t.Errorf("unsupported members kept: %s", body)
	}
}
