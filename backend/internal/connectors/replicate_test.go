package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func replicateTestConnector() *Replicate {
	c := NewReplicate("replicate", "")
	c.pollMin, c.pollMax = time.Millisecond, 2*time.Millisecond
	return c
}

func replicateUserReq(model, text string) *core.ChatRequest {
	return &core.ChatRequest{Model: model,
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: text}}}}}
}

func replicateCollect(t *testing.T, ch <-chan core.StreamChunk) []core.StreamChunk {
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

func TestReplicateWaitSeconds(t *testing.T) {
	if got := replicateWaitSeconds(context.Background()); got != 58 {
		t.Fatalf("no deadline: %d", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if got := replicateWaitSeconds(ctx); got < 16 || got > 18 {
		t.Fatalf("20s deadline: %d", got)
	}
	short, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if got := replicateWaitSeconds(short); got != 0 {
		t.Fatalf("1s deadline: %d", got)
	}
}

func TestReplicateTargetFor(t *testing.T) {
	ver := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		model, path, version string
		bad                  bool
	}{
		{model: "meta/meta-llama-3-70b-instruct", path: "models/meta/meta-llama-3-70b-instruct/predictions"},
		{model: "meta/llama-2-70b-chat:" + ver, path: "predictions", version: ver},
		{model: ver, path: "predictions", version: ver},
		{model: "deployments/acme/my-llm", path: "deployments/acme/my-llm/predictions"},
		{model: "llama", bad: true},
		{model: "a/b/c", bad: true},
		{model: "a/b:", bad: true},
	} {
		got, err := replicateTargetFor(tc.model)
		if tc.bad {
			if err == nil {
				t.Fatalf("%s: expected error", tc.model)
			}
			continue
		}
		if err != nil || got.path != tc.path || got.version != tc.version {
			t.Fatalf("%s: got %+v, %v", tc.model, got, err)
		}
	}
}

func TestReplicatePromptAndInput(t *testing.T) {
	req := &core.ChatRequest{
		Model:     "meta/meta-llama-3-70b-instruct",
		System:    "be nice",
		MaxTokens: ociIntP(64), Temperature: ociFloatP(0.3), TopP: ociFloatP(0.8), Stop: []string{"<end>", "##"},
		Extra: map[string]json.RawMessage{"top_k": json.RawMessage(`20`), "logit_bias": json.RawMessage(`{}`)},
		Messages: []core.Message{
			{Role: core.RoleSystem, Content: []core.ContentPart{{Type: core.PartText, Text: "short answers"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "1", Name: "clock", Arguments: json.RawMessage(`{}`)}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "1", Content: "12:00"}}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "time?"}}},
		},
	}
	in := replicateInput(req)
	wantPrompt := "User: hi\n\nAssistant: hello\n[called tool clock with {}]\n\nTool result: 12:00\n\nUser: time?\n\nAssistant:"
	if in["prompt"] != wantPrompt {
		t.Fatalf("prompt:\n%q\nwant:\n%q", in["prompt"], wantPrompt)
	}
	if in["system_prompt"] != "be nice\n\nshort answers" || in["max_tokens"] != 64 || in["max_new_tokens"] != 64 ||
		in["temperature"] != 0.3 || in["top_p"] != 0.8 || in["stop_sequences"] != "<end>,##" {
		t.Fatalf("input = %v", in)
	}
	if string(in["top_k"].(json.RawMessage)) != "20" || in["logit_bias"] != nil {
		t.Fatalf("passthrough = %v", in)
	}
	single := replicateInput(replicateUserReq("a/b", "just this"))
	if single["prompt"] != "just this" || single["system_prompt"] != nil || single["max_tokens"] != nil {
		t.Fatalf("single-turn input = %v", single)
	}
}

func TestReplicateChatWaitSucceeded(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/models/meta/meta-llama-3-70b-instruct/predictions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer r8_test" || r.Header.Get("Prefer") != "wait=58" {
			t.Errorf("headers = %v", r.Header)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write([]byte(`{"id":"p1","status":"succeeded","output":["Hel","lo", " world"],
			"metrics":{"input_token_count":9,"output_token_count":3}}`))
	}))
	defer srv.Close()
	resp, err := replicateTestConnector().Chat(context.Background(), replicateUserReq("meta/meta-llama-3-70b-instruct", "hi"),
		core.Credentials{APIKey: "r8_test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if body["stream"] != false || body["version"] != nil || body["input"].(map[string]any)["prompt"] != "hi" {
		t.Fatalf("create body = %v", body)
	}
	if resp.ID != "p1" || resp.Message.TextContent() != "Hello world" || resp.FinishReason != core.FinishStop {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Usage.PromptTokens != 9 || resp.Usage.CompletionTokens != 3 || resp.Usage.TotalTokens != 12 ||
		resp.Usage.Source != core.UsageSourceProvider {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestReplicateChatPollsVersionedPrediction(t *testing.T) {
	ver := strings.Repeat("0f", 32)
	var polls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/predictions":
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			if body["version"] != ver {
				t.Errorf("version = %v", body["version"])
			}
			_, _ = fmt.Fprintf(w, `{"id":"p2","status":"starting","urls":{"get":"%s/predictions/p2"}}`, srv.URL)
		case r.Method == http.MethodGet && r.URL.Path == "/predictions/p2":
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"id":"p2","status":"processing","output":["par"]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"p2","status":"succeeded","output":"partial done"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	resp, err := replicateTestConnector().Chat(context.Background(), replicateUserReq("meta/llama-2-70b-chat:"+ver, "hi"),
		core.Credentials{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.TextContent() != "partial done" || polls.Load() != 3 {
		t.Fatalf("text=%q polls=%d", resp.Message.TextContent(), polls.Load())
	}
}

func TestReplicateChatFailures(t *testing.T) {
	for _, tc := range []struct {
		pred string
		kind core.ErrorKind
	}{
		{`{"id":"p","status":"failed","error":"CUDA out of memory"}`, core.ErrUpstream},
		{`{"id":"p","status":"failed","error":"Prompt is too long for this model"}`, core.ErrContextWindow},
		{`{"id":"p","status":"canceled"}`, core.ErrUpstream},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(tc.pred))
		}))
		_, err := replicateTestConnector().Chat(context.Background(), replicateUserReq("a/b", "hi"), core.Credentials{APIKey: "k", BaseURL: srv.URL})
		srv.Close()
		if pe := core.AsProviderError(err); pe.Kind != tc.kind || pe.Provider != "replicate" || pe.Model != "a/b" {
			t.Fatalf("%s: err = %v", tc.pred, err)
		}
	}
	// HTTP-level errors map through the shared classifier.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail":"Request was throttled."}`))
	}))
	defer srv.Close()
	_, err := replicateTestConnector().Chat(context.Background(), replicateUserReq("a/b", "hi"), core.Credentials{APIKey: "k", BaseURL: srv.URL})
	if pe := core.AsProviderError(err); pe.Kind != core.ErrRateLimit {
		t.Fatalf("429: err = %v", err)
	}
	_, err = replicateTestConnector().Chat(context.Background(), replicateUserReq("not-a-model", "hi"), core.Credentials{APIKey: "k", BaseURL: srv.URL})
	if pe := core.AsProviderError(err); pe.Kind != core.ErrBadRequest {
		t.Fatalf("bad model: err = %v", err)
	}
}

func TestReplicateChatCancelStopsPrediction(t *testing.T) {
	var canceled atomic.Bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/predictions/p3/cancel":
			canceled.Store(true)
			_, _ = w.Write([]byte(`{"id":"p3","status":"canceled"}`))
		case r.Method == http.MethodPost:
			_, _ = fmt.Fprintf(w, `{"id":"p3","status":"processing","urls":{"get":"%[1]s/predictions/p3","cancel":"%[1]s/predictions/p3/cancel"}}`, srv.URL)
		default:
			_, _ = w.Write([]byte(`{"id":"p3","status":"processing"}`))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := replicateTestConnector().Chat(ctx, replicateUserReq("a/b", "hi"), core.Credentials{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !canceled.Load() {
		t.Fatalf("err=%v canceled=%v", err, canceled.Load())
	}
	// The overall wait is capped as well.
	c := replicateTestConnector()
	c.maxWait = 10 * time.Millisecond
	canceled.Store(false)
	_, err = c.Chat(context.Background(), replicateUserReq("a/b", "hi"), core.Credentials{APIKey: "k", BaseURL: srv.URL})
	if pe := core.AsProviderError(err); pe.Kind != core.ErrTimeout || !canceled.Load() {
		t.Fatalf("max wait: err=%v canceled=%v", err, canceled.Load())
	}
}

func TestReplicateChatIgnoresForeignPollURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"p4","status":"starting","urls":{"get":"https://attacker.example/steal"}}`))
			return
		}
		if r.URL.Path != "/predictions/p4" {
			t.Errorf("polled %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"p4","status":"succeeded","output":["ok"]}`))
	}))
	defer srv.Close()
	resp, err := replicateTestConnector().Chat(context.Background(), replicateUserReq("a/b", "hi"), core.Credentials{APIKey: "k", BaseURL: srv.URL})
	if err != nil || resp.Message.TextContent() != "ok" {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
}

// replicateStreamServer serves create + SSE stream + final prediction.
func replicateStreamServer(t *testing.T, events string, final string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			if body["stream"] != true || r.Header.Get("Prefer") != "" {
				t.Errorf("stream create body=%v prefer=%q", body, r.Header.Get("Prefer"))
			}
			_, _ = fmt.Fprintf(w, `{"id":"s1","status":"starting","urls":{"get":"%[1]s/predictions/s1","stream":"%[1]s/streams/s1"}}`, srv.URL)
		case r.URL.Path == "/streams/s1":
			if r.Header.Get("Accept") != "text/event-stream" {
				t.Errorf("accept = %q", r.Header.Get("Accept"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, events)
		case r.URL.Path == "/predictions/s1":
			_, _ = io.WriteString(w, final)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReplicateStream(t *testing.T) {
	events := ": keep-alive\n\n" +
		"event: output\nid: 1\ndata: Hello\n\n" +
		"event: output\ndata:  world\n\n" +
		"event: output\ndata: line one\ndata: line two\n\n" +
		"event: done\ndata: {}\n\n"
	srv := replicateStreamServer(t, events, `{"id":"s1","status":"succeeded","metrics":{"input_token_count":4,"output_token_count":6}}`)
	ch, err := replicateTestConnector().Stream(context.Background(), replicateUserReq("a/b", "hi"),
		core.Credentials{APIKey: "k", BaseURL: srv.URL}, core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := replicateCollect(t, ch)
	var text string
	pings := 0
	for _, c := range chunks {
		switch c.Type {
		case core.ChunkText:
			text += c.Delta
		case core.ChunkPing:
			pings++
		case core.ChunkError:
			t.Fatalf("unexpected error %v", c.Err)
		}
	}
	if text != "Hello worldline one\nline two" || pings != 1 {
		t.Fatalf("text=%q pings=%d", text, pings)
	}
	n := len(chunks)
	if chunks[n-2].Type != core.ChunkUsage || chunks[n-2].Usage.TotalTokens != 10 ||
		chunks[n-1].Type != core.ChunkFinish || chunks[n-1].FinishReason != core.FinishStop {
		t.Fatalf("tail = %+v %+v", chunks[n-2], chunks[n-1])
	}
}

func TestReplicateStreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name, events, final string
		kind                core.ErrorKind
	}{
		{"error event", "event: output\ndata: a\n\nevent: error\ndata: {\"detail\":\"rate limit exceeded\"}\n\n", `{}`, core.ErrRateLimit},
		{"failed done", "event: done\ndata: {\"reason\":\"error\"}\n\n", `{"id":"s1","status":"failed","error":"maximum context length exceeded"}`, core.ErrContextWindow},
		{"canceled", "event: done\ndata: {\"reason\":\"canceled\"}\n\n", `{}`, core.ErrUpstream},
		{"truncated", "event: output\ndata: a\n\n", `{}`, core.ErrUpstream},
	} {
		srv := replicateStreamServer(t, tc.events, tc.final)
		ch, err := replicateTestConnector().Stream(context.Background(), replicateUserReq("a/b", "hi"),
			core.Credentials{APIKey: "k", BaseURL: srv.URL}, core.StreamConfig{})
		if err != nil {
			t.Fatal(err)
		}
		chunks := replicateCollect(t, ch)
		last := chunks[len(chunks)-1]
		if pe := core.AsProviderError(last.Err); last.Type != core.ChunkError || pe.Kind != tc.kind || pe.Provider != "replicate" {
			t.Fatalf("%s: last = %+v (%v)", tc.name, last, last.Err)
		}
	}
}

func TestReplicateStreamPollingFallback(t *testing.T) {
	var mu sync.Mutex
	outputs := []string{`["Hel"]`, `["Hel","lo"]`, `["Hel","lo","!"]`}
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"q1","status":"starting"}`))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		status := "processing"
		if step == len(outputs)-1 {
			status = "succeeded"
		}
		_, _ = fmt.Fprintf(w, `{"id":"q1","status":"%s","output":%s,"metrics":{"input_token_count":1,"output_token_count":2}}`, status, outputs[step])
		if step < len(outputs)-1 {
			step++
		}
	}))
	defer srv.Close()
	ch, err := replicateTestConnector().Stream(context.Background(), replicateUserReq("a/b", "hi"),
		core.Credentials{APIKey: "k", BaseURL: srv.URL}, core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := replicateCollect(t, ch)
	var deltas []string
	for _, c := range chunks {
		if c.Type == core.ChunkText {
			deltas = append(deltas, c.Delta)
		}
	}
	last := chunks[len(chunks)-1]
	if strings.Join(deltas, "|") != "Hel|lo|!" || last.Type != core.ChunkFinish {
		t.Fatalf("deltas=%v last=%+v", deltas, last)
	}
}

func TestReplicateValidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/account" || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"title":"Unauthenticated","detail":"You did not pass a valid authentication token","status":401}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"user","username":"me"}`))
	}))
	defer srv.Close()
	c := replicateTestConnector()
	if err := c.Validate(context.Background(), core.Credentials{APIKey: "good", BaseURL: srv.URL}); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	err := c.Validate(context.Background(), core.Credentials{APIKey: "bad", BaseURL: srv.URL})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.ErrAuth || strings.Contains(err.Error(), "bad\"") {
		t.Fatalf("bad token: %v", err)
	}
	err = c.Validate(context.Background(), core.Credentials{APIKey: "good", BaseURL: "http://127.0.0.1:1"})
	if !errors.As(err, &pe) || pe.Kind == core.ErrAuth || pe.StatusCode != 0 {
		t.Fatalf("unreachable: %v", err)
	}
	if err := c.Validate(context.Background(), core.Credentials{BaseURL: srv.URL}); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestReplicateRegistered(t *testing.T) {
	f, ok := dialectFactories[core.DialectReplicate]
	if !ok {
		t.Fatal("Replicate dialect not registered")
	}
	if _, ok := f("replicate", "").(core.Validator); !ok {
		t.Fatal("Replicate connector should validate")
	}
	if len(providerModels["replicate"]) == 0 {
		t.Fatal("no static Replicate models")
	}
}
