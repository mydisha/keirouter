package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// sapFake is an XSUAA tenant, AI API and orchestration deployment in one.
type sapFake struct {
	srv          *httptest.Server
	tokenCalls   atomic.Int32
	tokenCounter atomic.Int32
	listCalls    atomic.Int32
	completions  atomic.Int32
	lastBody     atomic.Value // []byte
	lastHeaders  atomic.Value // http.Header
	deployments  string
	completion   func(w http.ResponseWriter, r *http.Request, n int32)
}

func newSAPFake(t *testing.T) *sapFake {
	f := &sapFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/token":
			f.tokenCalls.Add(1)
			require.NoError(t, r.ParseForm())
			if r.PostForm.Get("client_secret") != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":"unauthorized","error_description":"Bad credentials"}`)
				return
			}
			require.Equal(t, "client_credentials", r.PostForm.Get("grant_type"))
			require.Equal(t, "client-1", r.PostForm.Get("client_id"))
			fmt.Fprintf(w, `{"access_token":"tok-%d","expires_in":3600}`, f.tokenCounter.Add(1))
		case r.URL.Path == "/v2/lm/deployments":
			f.listCalls.Add(1)
			require.Equal(t, "orchestration", r.URL.Query().Get("scenarioId"))
			require.Equal(t, "RUNNING", r.URL.Query().Get("status"))
			require.Equal(t, "rg-1", r.Header.Get("AI-Resource-Group"))
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, f.deployments)
		case strings.HasSuffix(r.URL.Path, "/v2/completion"):
			n := f.completions.Add(1)
			body, _ := io.ReadAll(r.Body)
			f.lastBody.Store(body)
			f.lastHeaders.Store(r.Header.Clone())
			require.Equal(t, "/v2/inference/deployments/new/v2/completion", r.URL.Path)
			f.completion(w, r, n)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	f.deployments = fmt.Sprintf(`{"count":2,"resources":[
		{"id":"old","deploymentUrl":"%[1]s/v2/inference/deployments/old","scenarioId":"orchestration","status":"RUNNING","createdAt":"2025-01-01T00:00:00Z"},
		{"id":"new","deploymentUrl":"%[1]s/v2/inference/deployments/new","scenarioId":"orchestration","status":"RUNNING","createdAt":"2025-06-01T00:00:00Z"}]}`, f.srv.URL)
	return f
}

func (f *sapFake) creds() core.Credentials {
	key := fmt.Sprintf(`{"credentials":{"clientid":"client-1","clientsecret":"secret","url":%q,"serviceurls":{"AI_API_URL":%q}}}`,
		f.srv.URL, f.srv.URL+"/v2")
	return core.Credentials{AccountID: "acc-" + f.srv.URL, Extra: map[string]string{
		"sap_service_key":    key,
		"sap_resource_group": "rg-1",
	}}
}

func (f *sapFake) body(t *testing.T) map[string]any {
	var out map[string]any
	require.NoError(t, json.Unmarshal(f.lastBody.Load().([]byte), &out))
	return out
}

func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			v = v.(map[string]any)[k]
		case int:
			v = v.([]any)[k]
		}
	}
	return v
}

func TestSAPResolveConfig(t *testing.T) {
	c := NewSAP("sap", "https://default.example")

	cfg, err := c.resolveConfig(core.Credentials{Extra: map[string]string{
		"sap_service_key": `{"clientid":"id","clientsecret":"sec","url":"https://sub.authentication.eu10.hana.ondemand.com/","serviceurls":{"AI_API_URL":"https://api.ai.example/v2/"}}`,
		"sap_client_id":   "override-id",
	}})
	require.NoError(t, err)
	require.Equal(t, "override-id", cfg.clientID)
	require.Equal(t, "sec", cfg.clientSecret)
	require.Equal(t, "https://sub.authentication.eu10.hana.ondemand.com/oauth/token", cfg.tokenURL)
	require.Equal(t, "https://api.ai.example", cfg.apiURL)
	require.Equal(t, "default", cfg.resourceGroup)

	cfg, err = c.resolveConfig(core.Credentials{Extra: map[string]string{
		"sap_client_id": "a", "sap_client_secret": "b", "sap_auth_url": "https://auth.example/oauth/token",
		"sap_api_url": "https://api.example", "sap_deployment_url": "https://api.example/v2/inference/deployments/d1/",
	}})
	require.NoError(t, err)
	require.Equal(t, "https://auth.example/oauth/token", cfg.tokenURL)
	require.Equal(t, "https://api.example/v2/inference/deployments/d1", cfg.deploymentURL)

	_, err = c.resolveConfig(core.Credentials{Extra: map[string]string{"sap_client_id": "a"}})
	require.Equal(t, core.ErrAuth, core.AsProviderError(err).Kind)
	require.ErrorContains(t, err, "client secret")
	require.ErrorContains(t, err, "auth URL")

	_, err = c.resolveConfig(core.Credentials{Extra: map[string]string{"sap_service_key": "{not json"}})
	require.ErrorContains(t, err, "not valid JSON")

	_, err = c.resolveConfig(core.Credentials{Extra: map[string]string{
		"sap_service_key": `{"clientid":"id","certurl":"https://x","certificate":"-----BEGIN","url":"https://x"}`}})
	require.ErrorContains(t, err, "x509")
}

func TestSAPChatRendersOrchestrationRequest(t *testing.T) {
	f := newSAPFake(t)
	f.completion = func(w http.ResponseWriter, _ *http.Request, _ int32) {
		fmt.Fprint(w, `{"request_id":"req-1","intermediate_results":{"templating":[]},"final_result":{
			"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-2024-08-06",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Checking.","tool_calls":[
				{"id":"call_9","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Berlin\"}"}}]},
				"finish_reason":"tool_calls"}],
			"usage":{"completion_tokens":7,"prompt_tokens":30,"total_tokens":37,"prompt_tokens_details":{"cached_tokens":10}}}}`)
	}
	temp, maxTok := 0.2, 256
	req := &core.ChatRequest{
		Model:       "gpt-4o",
		System:      "Be terse.",
		Temperature: &temp,
		MaxTokens:   &maxTok,
		Stop:        []string{"END"},
		Tools: []core.Tool{{Name: "get_weather", Description: "Weather", Parameters: json.RawMessage(`{"properties":{"city":{"type":"string"}}}`)},
			{Name: "noop"}},
		ToolChoice:     "required",
		ResponseFormat: json.RawMessage(`{"type":"json_object"}`),
		Extra:          map[string]json.RawMessage{"seed": json.RawMessage(`7`), "model_version": json.RawMessage(`"2024-08-06"`)},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartText, Text: "What is this?"},
				{Type: core.PartImage, Media: &core.MediaPayload{MIMEType: "image/png", Data: "iVBOR"}},
			}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "hmm"},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`)}},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_1", Content: "sunny"}},
				{Type: core.PartText, Text: "And Berlin?", CacheControl: json.RawMessage(`{"type":"ephemeral"}`)},
			}},
		},
	}

	resp, err := NewSAP("sap", "").Chat(context.Background(), req, f.creds())
	require.NoError(t, err)

	h := f.lastHeaders.Load().(http.Header)
	require.Equal(t, "Bearer tok-1", h.Get("Authorization"))
	require.Equal(t, "rg-1", h.Get("AI-Resource-Group"))

	body := f.body(t)
	require.NotContains(t, body["config"].(map[string]any), "stream")
	pt := dig(body, "config", "modules", "prompt_templating")
	require.Equal(t, "gpt-4o", dig(pt, "model", "name"))
	require.Equal(t, "2024-08-06", dig(pt, "model", "version"))
	params := dig(pt, "model", "params").(map[string]any)
	require.Equal(t, 0.2, params["temperature"])
	require.EqualValues(t, 256, params["max_tokens"])
	require.EqualValues(t, 7, params["seed"])
	require.Equal(t, []any{"END"}, params["stop"])
	require.NotContains(t, params, "tool_choice")

	prompt := dig(pt, "prompt").(map[string]any)
	require.Equal(t, map[string]any{"type": "json_object"}, prompt["response_format"])
	tools := prompt["tools"].([]any)
	require.Len(t, tools, 2)
	require.Equal(t, "object", dig(tools[0], "function", "parameters", "type"))
	require.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, dig(tools[1], "function", "parameters"))
	require.Equal(t, false, dig(tools[0], "function", "strict"))

	tpl := prompt["template"].([]any)
	require.Len(t, tpl, 5)
	require.Equal(t, map[string]any{"role": "system", "content": "Be terse."}, tpl[0])
	require.Equal(t, "user", dig(tpl[1], "role"))
	require.Equal(t, "What is this?", dig(tpl[1], "content", 0, "text"))
	require.Equal(t, "data:image/png;base64,iVBOR", dig(tpl[1], "content", 1, "image_url", "url"))
	require.Equal(t, "auto", dig(tpl[1], "content", 1, "image_url", "detail"))
	require.Equal(t, "", dig(tpl[2], "content"))
	require.Equal(t, `{"city":"Paris"}`, dig(tpl[2], "tool_calls", 0, "function", "arguments"))
	require.Equal(t, "call_1", dig(tpl[2], "tool_calls", 0, "id"))
	require.Equal(t, map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "sunny"}, tpl[3])
	require.Equal(t, "ephemeral", dig(tpl[4], "content", 0, "cache_control", "type"))

	require.Equal(t, "chatcmpl-1", resp.ID)
	require.Equal(t, core.FinishToolCalls, resp.FinishReason)
	require.Equal(t, "Checking.", resp.Message.TextContent())
	var call *core.ToolCall
	for _, p := range resp.Message.Content {
		if p.Type == core.PartToolCall {
			call = p.ToolCall
		}
	}
	require.NotNil(t, call)
	require.Equal(t, "call_9", call.ID)
	require.JSONEq(t, `{"city":"Berlin"}`, string(call.Arguments))
	require.Equal(t, 30, resp.Usage.PromptTokens)
	require.Equal(t, 10, resp.Usage.CachedTokens)
	require.Equal(t, 37, resp.Usage.TotalTokens)
}

func TestSAPDeploymentAndTokenCached(t *testing.T) {
	f := newSAPFake(t)
	f.completion = func(w http.ResponseWriter, _ *http.Request, _ int32) {
		fmt.Fprint(w, `{"final_result":{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}}`)
	}
	c := NewSAP("sap", "")
	for i := 0; i < 3; i++ {
		resp, err := c.Chat(context.Background(), &core.ChatRequest{Model: "gpt-4o",
			Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}, f.creds())
		require.NoError(t, err)
		require.Equal(t, "hi", resp.Message.TextContent())
	}
	require.EqualValues(t, 1, f.tokenCalls.Load())
	require.EqualValues(t, 1, f.listCalls.Load())
	require.EqualValues(t, 3, f.completions.Load())
}

func TestSAPRetriesOnceWithFreshTokenAfter401(t *testing.T) {
	f := newSAPFake(t)
	f.completion = func(w http.ResponseWriter, r *http.Request, n int32) {
		if r.Header.Get("Authorization") == "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"token expired"}}`)
			return
		}
		fmt.Fprint(w, `{"final_result":{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}}`)
	}
	c := NewSAP("sap", "")
	creds := f.creds()
	// Warm the cache so the rejected token is a cached one.
	_, _, err := c.token(context.Background(), mustSAPConfig(t, c, creds))
	require.NoError(t, err)

	resp, err := c.Chat(context.Background(), &core.ChatRequest{Model: "gpt-4o",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}, creds)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Message.TextContent())
	require.EqualValues(t, 2, f.tokenCalls.Load())
	require.EqualValues(t, 2, f.completions.Load())
}

func mustSAPConfig(t *testing.T, c *SAP, creds core.Credentials) sapConfig {
	cfg, err := c.resolveConfig(creds)
	require.NoError(t, err)
	return cfg
}

func TestSAPErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   core.ErrorKind
	}{
		{http.StatusBadRequest, `{"error":{"request_id":"r","code":400,"message":"temperature must be between 0 and 2","location":"LLM Module"}}`, core.ErrBadRequest},
		{http.StatusTooManyRequests, `{"error":{"code":429,"message":"Too many requests"}}`, core.ErrRateLimit},
		{http.StatusForbidden, `{"error":{"code":403,"message":"forbidden"}}`, core.ErrAuth},
	}
	for _, tc := range cases {
		f := newSAPFake(t)
		f.completion = func(w http.ResponseWriter, _ *http.Request, _ int32) {
			w.WriteHeader(tc.status)
			fmt.Fprint(w, tc.body)
		}
		_, err := NewSAP("sap", "").Chat(context.Background(), &core.ChatRequest{Model: "gpt-4o",
			Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}, f.creds())
		require.Error(t, err)
		require.Equal(t, tc.kind, core.AsProviderError(err).Kind, tc.body)
	}

	// An error envelope inside a 200 response.
	pe := (&sapEnvelope{Code: json.RawMessage(`500`), Message: "boom", Location: "Templating"}).providerError("sap", "m")
	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, "Templating: boom", pe.Message)
}

func TestSAPNoRunningDeployment(t *testing.T) {
	f := newSAPFake(t)
	f.deployments = `{"count":1,"resources":[{"id":"x","deploymentUrl":"","scenarioId":"orchestration","status":"RUNNING"}]}`
	f.completion = func(http.ResponseWriter, *http.Request, int32) { t.Fatal("no completion expected") }
	_, err := NewSAP("sap", "").Chat(context.Background(), &core.ChatRequest{Model: "gpt-4o",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}, f.creds())
	require.ErrorContains(t, err, "no running orchestration deployment")
	require.Equal(t, core.ErrModelUnavailable, core.AsProviderError(err).Kind)
}

func TestSAPStream(t *testing.T) {
	f := newSAPFake(t)
	f.completion = func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"request_id":"r","intermediate_results":{"templating":[{"content":"Hi","role":"user"}]},"final_result":{"id":"","object":"","created":0,"model":"","choices":[{"index":0,"delta":{"content":""}}]}}`,
			`{"request_id":"r","final_result":{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "}}]}}`,
			`{"request_id":"r","orchestration_result":{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"from SAP!"}}]}}`,
			`{"request_id":"r","final_result":{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}}`,
			`{"request_id":"r","final_result":{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}}`,
			`{"request_id":"r","final_result":{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":7,"total_tokens":10}}}`,
			`[DONE]`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}
	ch, err := NewSAP("sap", "").Stream(context.Background(), &core.ChatRequest{Model: "gpt-4o",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "Hi"}}}}}, f.creds(), core.StreamConfig{})
	require.NoError(t, err)
	var text, args strings.Builder
	var types []core.ChunkType
	var usage *core.Usage
	var finish core.FinishReason
	for c := range ch {
		types = append(types, c.Type)
		switch c.Type {
		case core.ChunkText:
			text.WriteString(c.Delta)
		case core.ChunkToolCall:
			args.Write(c.ToolCall.Arguments)
		case core.ChunkUsage:
			usage = c.Usage
		case core.ChunkFinish:
			finish = c.FinishReason
		case core.ChunkError:
			t.Fatal(c.Err)
		}
	}
	require.Equal(t, true, dig(f.body(t), "config", "stream", "enabled"))
	require.Equal(t, "Hello from SAP!", text.String())
	require.Contains(t, args.String(), `"x"`)
	require.Equal(t, core.FinishToolCalls, finish)
	require.Equal(t, 10, usage.TotalTokens)
	require.Equal(t, []core.ChunkType{core.ChunkUsage, core.ChunkFinish}, types[len(types)-2:])
}

func TestSAPStreamInBandError(t *testing.T) {
	f := newSAPFake(t)
	f.completion = func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"final_result\":{\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"par\"}}]}}\n\n")
		fmt.Fprint(w, "data: {\"error\":{\"request_id\":\"r\",\"code\":429,\"message\":\"Rate limit reached\",\"location\":\"LLM Module\"}}\n\n")
	}
	ch, err := NewSAP("sap", "").Stream(context.Background(), &core.ChatRequest{Model: "gpt-4o",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "Hi"}}}}}, f.creds(), core.StreamConfig{})
	require.NoError(t, err)
	var last core.StreamChunk
	for c := range ch {
		last = c
	}
	require.Equal(t, core.ChunkError, last.Type)
	pe := core.AsProviderError(last.Err)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, "sap", pe.Provider)
	require.Contains(t, pe.Message, "LLM Module")
}

func TestSAPValidate(t *testing.T) {
	f := newSAPFake(t)
	c := NewSAP("sap", "")
	require.NoError(t, c.Validate(context.Background(), f.creds()))

	bad := f.creds()
	bad.Extra["sap_client_secret"] = "wrong"
	err := c.Validate(context.Background(), bad)
	require.ErrorContains(t, err, "client credentials were rejected")

	// Validate always lists, so a cached deployment does not mask an empty group.
	f.deployments = `{"count":0,"resources":[]}`
	err = c.Validate(context.Background(), f.creds())
	require.ErrorContains(t, err, "no running orchestration deployment")
	require.NotContains(t, err.Error(), "rejected")

	pinned := f.creds()
	pinned.Extra["sap_deployment_url"] = f.srv.URL + "/v2/inference/deployments/new"
	require.NoError(t, c.Validate(context.Background(), pinned))

	missing := core.Credentials{Extra: map[string]string{"sap_client_id": "x"}}
	err = c.Validate(context.Background(), missing)
	require.ErrorContains(t, err, "incomplete")

	unreachable := f.creds()
	unreachable.Extra["sap_auth_url"] = "http://127.0.0.1:1"
	err = c.Validate(context.Background(), unreachable)
	require.ErrorContains(t, err, "could not reach the SAP auth URL")
}
