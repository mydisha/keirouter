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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

const gigaTestKey = "Y2xpZW50OnNlY3JldA==" // base64("client:secret")

// gigaFake is the GigaChat OAuth endpoint and REST API in one server.
type gigaFake struct {
	srv         *httptest.Server
	tokenCalls  atomic.Int32
	tokenSeq    atomic.Int32
	chatCalls   atomic.Int32
	fileCalls   atomic.Int32
	lastBody    atomic.Value // []byte
	lastHeaders atomic.Value // http.Header
	chat        func(w http.ResponseWriter, r *http.Request)
}

func newGigaFake(t *testing.T) *gigaFake {
	f := &gigaFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth":
			f.tokenCalls.Add(1)
			require.NoError(t, r.ParseForm())
			if r.Header.Get("Authorization") != "Basic "+gigaTestKey {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"code":6,"message":"credentials doesn't match db data"}`)
				return
			}
			require.NotEmpty(t, r.Header.Get("RqUID"))
			require.Equal(t, "GIGACHAT_API_B2B", r.PostForm.Get("scope"))
			fmt.Fprintf(w, `{"access_token":"tok-%d","expires_at":%d}`, f.tokenSeq.Add(1), time.Now().Add(30*time.Minute).UnixMilli())
		case "/api/v1/chat/completions":
			f.chatCalls.Add(1)
			body, _ := io.ReadAll(r.Body)
			f.lastBody.Store(body)
			f.lastHeaders.Store(r.Header.Clone())
			f.chat(w, r)
		case "/api/v1/files":
			f.fileCalls.Add(1)
			require.NoError(t, r.ParseMultipartForm(1<<20))
			require.Equal(t, "general", r.FormValue("purpose"))
			file, hdr, err := r.FormFile("file")
			require.NoError(t, err)
			defer file.Close()
			require.Equal(t, "image/png", hdr.Header.Get("Content-Type"))
			require.True(t, strings.HasSuffix(hdr.Filename, ".png"))
			data, _ := io.ReadAll(file)
			require.Equal(t, "PNGDATA", string(data))
			fmt.Fprint(w, `{"id":"file-1","object":"file","purpose":"general"}`)
		case "/api/v1/models":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"object":"list","data":[{"id":"GigaChat-2-Max","object":"model","owned_by":"salutedevices","type":"chat"},{"id":"EmbeddingsGigaR","object":"model","type":"embedder"}]}`)
		case "/api/v1/embeddings":
			fmt.Fprint(w, `{"object":"list","model":"Embeddings","data":[{"object":"embedding","embedding":[0.5,0.25],"index":1,"usage":{"prompt_tokens":3}},{"object":"embedding","embedding":[1,2],"index":0,"usage":{"prompt_tokens":4}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *gigaFake) creds() core.Credentials {
	return core.Credentials{APIKey: gigaTestKey, BaseURL: f.srv.URL + "/api/v1", Extra: map[string]string{
		"gigachat_auth_url": f.srv.URL + "/oauth",
		"gigachat_scope":    "GIGACHAT_API_B2B",
	}}
}

func (f *gigaFake) body(t *testing.T) map[string]any {
	var out map[string]any
	require.NoError(t, json.Unmarshal(f.lastBody.Load().([]byte), &out))
	return out
}

func gigaUser(text string) core.Message {
	return core.Message{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: text}}}
}

func TestGigaChatRenderRequest(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"index":0,"finish_reason":"stop"}],"created":1,"model":"GigaChat-2-Max:2.0","object":"chat.completion","usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
	}
	temp, maxTok := 0.0, 100
	req := &core.ChatRequest{
		Model:       "GigaChat-2-Max",
		System:      "You are helpful.",
		Temperature: &temp,
		MaxTokens:   &maxTok,
		Stop:        []string{"END"},
		Tools:       []core.Tool{{Name: "weather", Description: "Weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)}},
		ToolChoice:  map[string]any{"type": "function", "function": map[string]any{"name": "weather"}},
		Extra:       map[string]json.RawMessage{"repetition_penalty": json.RawMessage(`1.1`)},
		Messages: []core.Message{
			{Role: core.RoleSystem, Content: []core.ContentPart{{Type: core.PartText, Text: "Answer in Russian."}}},
			gigaUser("Weather in Moscow and Kazan?"),
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartText, Text: "Let me check."},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_a", Name: "weather", Arguments: json.RawMessage(`{"city":"Moscow"}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "call_b", Name: "weather", Arguments: json.RawMessage(`"{\"city\":\"Kazan\"}"`)}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_a", Content: `{"temp":-5}`}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "call_b", Content: "cold"}}}},
			{Role: core.RoleSystem, Content: []core.ContentPart{{Type: core.PartText, Text: "Be brief."}}},
		},
	}
	resp, err := NewGigaChat("gigachat", "").Chat(context.Background(), req, f.creds())
	require.NoError(t, err)
	require.Equal(t, "done", resp.Message.TextContent())
	require.NotEmpty(t, resp.ID)

	h := f.lastHeaders.Load().(http.Header)
	require.Equal(t, "Bearer tok-1", h.Get("Authorization"))

	body := f.body(t)
	require.Equal(t, "GigaChat-2-Max", body["model"])
	require.NotContains(t, body, "temperature")
	require.EqualValues(t, 0, body["top_p"])
	require.EqualValues(t, 100, body["max_tokens"])
	require.EqualValues(t, 1.1, body["repetition_penalty"])
	require.NotContains(t, body, "stop")
	require.Equal(t, map[string]any{"name": "weather"}, body["function_call"])
	require.Equal(t, "weather", dig(body, "functions", 0, "name"))
	require.Equal(t, "object", dig(body, "functions", 0, "parameters", "type"))

	msgs := body["messages"].([]any)
	roles := make([]string, len(msgs))
	for i, mm := range msgs {
		roles[i] = dig(mm, "role").(string)
	}
	require.Equal(t, []string{"system", "user", "assistant", "function", "assistant", "function", "user"}, roles)
	require.Equal(t, "You are helpful.\n\nAnswer in Russian.", dig(msgs, 0, "content"))
	require.Equal(t, "Let me check.", dig(msgs, 2, "content"))
	require.Equal(t, map[string]any{"city": "Moscow"}, dig(msgs, 2, "function_call", "arguments"))
	require.Equal(t, map[string]any{"role": "function", "name": "weather", "content": `{"temp":-5}`}, msgs[3])
	require.Equal(t, "", dig(msgs, 4, "content"))
	require.Equal(t, map[string]any{"city": "Kazan"}, dig(msgs, 4, "function_call", "arguments"))
	require.Equal(t, `"cold"`, dig(msgs, 5, "content"))
	require.Equal(t, "Be brief.", dig(msgs, 6, "content"))
}

func TestGigaChatFunctionCallResponse(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","function_call":{"name":"weather","arguments":{"city":"Omsk"}},"functions_state_id":"abc"},"index":0,"finish_reason":"function_call"}],
			"created":1700000000,"model":"GigaChat-2","object":"chat.completion","usage":{"prompt_tokens":40,"completion_tokens":9,"total_tokens":49,"precached_prompt_tokens":100}}`)
	}
	req := &core.ChatRequest{Model: "GigaChat-2", Messages: []core.Message{gigaUser("weather?")},
		Tools: []core.Tool{{Name: "weather", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}}
	c := NewGigaChat("gigachat", "")
	resp, err := c.Chat(context.Background(), req, f.creds())
	require.NoError(t, err)
	require.Equal(t, core.FinishToolCalls, resp.FinishReason)
	require.Len(t, resp.Message.Content, 1)
	tc := resp.Message.Content[0].ToolCall
	require.Equal(t, "weather", tc.Name)
	require.JSONEq(t, `{"city":"Omsk"}`, string(tc.Arguments))
	require.True(t, strings.HasPrefix(tc.ID, "call_"))
	require.NotContains(t, f.body(t), "function_call", "no tool_choice leaves the model default")
	require.Equal(t, core.Usage{PromptTokens: 140, CompletionTokens: 9, TotalTokens: 149, CachedTokens: 100, Source: core.UsageSourceProvider}, resp.Usage)

	// The id is deterministic for the same answer.
	again, err := c.Chat(context.Background(), req, f.creds())
	require.NoError(t, err)
	require.Equal(t, tc.ID, again.Message.Content[0].ToolCall.ID)
	require.EqualValues(t, 1, f.tokenCalls.Load(), "token is cached across calls")
}

func TestGigaChatStructuredOutput(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","function_call":{"name":"person","arguments":{"name":"Ivan"}}},"index":0,"finish_reason":"function_call"}]}`)
	}
	req := &core.ChatRequest{Model: "GigaChat-2", Messages: []core.Message{gigaUser("who?")},
		ResponseFormat: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"person","schema":{"type":"object","properties":{"name":{"type":"string"}}}}}`)}
	resp, err := NewGigaChat("gigachat", "").Chat(context.Background(), req, f.creds())
	require.NoError(t, err)
	require.Equal(t, core.FinishStop, resp.FinishReason)
	require.JSONEq(t, `{"name":"Ivan"}`, resp.Message.TextContent())
	body := f.body(t)
	require.Equal(t, map[string]any{"name": "person"}, body["function_call"])
	require.Equal(t, "person", dig(body, "functions", 0, "name"))
}

func TestGigaChatStream(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checking\",\"role\":\"assistant\"},\"index\":0}],\"created\":5,\"model\":\"GigaChat-2\",\"object\":\"chat.completion\"}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\",\"role\":\"assistant\",\"function_call\":{\"name\":\"weather\",\"arguments\":{\"city\":\"Tver\"}}},\"index\":0,\"finish_reason\":\"function_call\"}],\"created\":5,\"model\":\"GigaChat-2\",\"object\":\"chat.completion\"}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"created\":5,\"model\":\"GigaChat-2\",\"object\":\"chat.completion\",\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"total_tokens\":25,\"precached_prompt_tokens\":0}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
	req := &core.ChatRequest{Model: "GigaChat-2", Messages: []core.Message{gigaUser("weather?")},
		Tools:    []core.Tool{{Name: "weather"}},
		Metadata: core.RequestMetadata{ContextAffinityKey: "sess-1"}}
	ch, err := NewGigaChat("gigachat", "").Stream(context.Background(), req, f.creds(), core.StreamConfig{})
	require.NoError(t, err)
	var got []core.StreamChunk
	for c := range ch {
		require.NotEqual(t, core.ChunkError, c.Type, c.Err)
		got = append(got, c)
	}
	require.Equal(t, true, f.body(t)["stream"])
	require.Equal(t, "sess-1", f.lastHeaders.Load().(http.Header).Get("X-Session-ID"))
	require.Len(t, got, 4)
	require.Equal(t, core.ChunkText, got[0].Type)
	require.Equal(t, "Checking", got[0].Delta)
	require.Equal(t, core.ChunkToolCall, got[1].Type)
	require.Equal(t, "weather", got[1].ToolCall.Name)
	require.NotEmpty(t, got[1].ToolCall.ID)
	require.JSONEq(t, `{"city":"Tver"}`, string(got[1].ToolCall.Arguments))
	require.Equal(t, core.ChunkUsage, got[2].Type)
	require.Equal(t, 25, got[2].Usage.TotalTokens)
	require.Equal(t, core.ChunkFinish, got[3].Type)
	require.Equal(t, core.FinishToolCalls, got[3].FinishReason)
}

func TestGigaChatStreamErrorFrame(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"index\":0}]}\n\n")
		fmt.Fprint(w, "data: {\"status\":429,\"message\":\"Too many requests\"}\n\n")
	}
	ch, err := NewGigaChat("gigachat", "").Stream(context.Background(), &core.ChatRequest{Model: "GigaChat-2", Messages: []core.Message{gigaUser("hi")}}, f.creds(), core.StreamConfig{})
	require.NoError(t, err)
	var last core.StreamChunk
	for c := range ch {
		last = c
	}
	require.Equal(t, core.ChunkError, last.Type)
	pe := core.AsProviderError(last.Err)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, "gigachat", pe.Provider)
}

func TestGigaChatRetriesOnceAfter401(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"status":401,"message":"Token has expired"}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"index":0,"finish_reason":"stop"}]}`)
	}
	c := NewGigaChat("gigachat", "")
	creds := f.creds()
	_, _, err := c.token(context.Background(), creds)
	require.NoError(t, err)
	resp, err := c.Chat(context.Background(), &core.ChatRequest{Model: "GigaChat-2", Messages: []core.Message{gigaUser("hi")}}, creds)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Message.TextContent())
	require.EqualValues(t, 2, f.tokenCalls.Load())
	require.EqualValues(t, 2, f.chatCalls.Load())

	// A fresh token that is rejected is not retried.
	f.chat = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"status":401,"message":"Unauthorized"}`)
	}
	sharedTokenCache.invalidate(c.tokenKey(creds))
	_, err = c.Chat(context.Background(), &core.ChatRequest{Model: "GigaChat-2", Messages: []core.Message{gigaUser("hi")}}, creds)
	require.Equal(t, core.ErrAuth, core.AsProviderError(err).Kind)
	require.EqualValues(t, 3, f.chatCalls.Load())
}

func TestGigaChatImages(t *testing.T) {
	f := newGigaFake(t)
	f.chat = func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"a cat"},"index":0,"finish_reason":"stop"}]}`)
	}
	req := &core.ChatRequest{Model: "GigaChat-2-Max", Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{
		{Type: core.PartText, Text: "What is it?"},
		{Type: core.PartImage, Media: &core.MediaPayload{MIMEType: "image/png", Data: "UE5HREFUQQ=="}},
		{Type: core.PartImage, Media: &core.MediaPayload{MIMEType: "image/png", URL: "https://example.com/cat.png"}},
	}}}}
	c := NewGigaChat("gigachat", "")
	for i := 0; i < 2; i++ {
		_, err := c.Chat(context.Background(), req, f.creds())
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, f.fileCalls.Load(), "uploaded once, then cached")
	msg := dig(f.body(t), "messages", 0)
	require.Equal(t, []any{"file-1"}, dig(msg, "attachments"))
	require.Contains(t, dig(msg, "content"), "What is it?")
	require.Contains(t, dig(msg, "content"), "image omitted")
}

func TestGigaChatValidateAndModels(t *testing.T) {
	f := newGigaFake(t)
	c := NewGigaChat("gigachat", "")
	require.NoError(t, c.Validate(context.Background(), f.creds()))

	bad := f.creds()
	bad.APIKey = "d3Jvbmc6a2V5"
	err := c.Validate(context.Background(), bad)
	require.ErrorContains(t, err, "authorization key or scope was rejected")
	require.Equal(t, core.ErrAuth, core.AsProviderError(err).Kind)

	unreachable := f.creds()
	unreachable.Extra["gigachat_auth_url"] = "http://127.0.0.1:1/oauth"
	err = c.Validate(context.Background(), unreachable)
	require.ErrorContains(t, err, "could not reach the GigaChat OAuth endpoint")

	err = c.Validate(context.Background(), core.Credentials{})
	require.ErrorContains(t, err, "authorization key is missing")

	models, err := c.ListModels(context.Background(), f.creds())
	require.NoError(t, err)
	require.Equal(t, []ModelSpec{{ID: "GigaChat-2-Max", Name: "GigaChat-2-Max", Kind: core.ServiceLLM},
		{ID: "EmbeddingsGigaR", Name: "EmbeddingsGigaR", Kind: core.ServiceEmbedding}}, models)
	var _ LiveModelSource = c
}

func TestGigaChatEmbeddings(t *testing.T) {
	f := newGigaFake(t)
	var c core.MediaConnector = NewGigaChat("gigachat", "")
	resp, err := c.Embeddings(context.Background(), &core.EmbeddingRequest{Model: "Embeddings", Input: []string{"a", "b"}}, f.creds())
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 2}, {0.5, 0.25}}, resp.Vectors)
	require.Equal(t, 7, resp.Usage.PromptTokens)
}

func TestGigaChatTokenParsing(t *testing.T) {
	tok, err := gigaParseToken("gigachat", []byte(`{"tok":"short","exp":1893456000000}`))
	require.NoError(t, err)
	require.Equal(t, "short", tok.Value)
	require.Equal(t, int64(1893456000000), tok.ExpiresAt.UnixMilli())

	tok, err = gigaParseToken("gigachat", []byte(`{"access_token":"long","expires_at":1893456000000}`))
	require.NoError(t, err)
	require.Equal(t, "long", tok.Value)
	require.Equal(t, int64(1893456000000), tok.ExpiresAt.UnixMilli())

	require.Equal(t, "Y2xpZW50OnNlY3JldA==", gigaAuthKey(core.Credentials{APIKey: "client:secret"}))
	require.Equal(t, "abc", gigaAuthKey(core.Credentials{APIKey: "Basic abc"}))
}

func TestGigaChatTLSFailureIsExplained(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	creds := core.Credentials{APIKey: gigaTestKey, Extra: map[string]string{"gigachat_auth_url": srv.URL + "/oauth"}}
	_, _, err := NewGigaChat("gigachat", "").token(context.Background(), creds)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Russian Trusted Root CA")
}
