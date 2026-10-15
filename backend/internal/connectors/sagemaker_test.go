package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func sageMakerTestCreds(base string) core.Credentials {
	return core.Credentials{
		BaseURL: base,
		Extra: map[string]string{
			awsFieldAuth:                     awsAuthAccessKey,
			awsFieldRegion:                   "eu-west-1",
			awsFieldAccessKeyID:              "AKIDEXAMPLE",
			awsFieldSecretKey:                "secret",
			sageMakerFieldInferenceComponent: "ic-llama",
		},
	}
}

func sageMakerTestRequest() *core.ChatRequest {
	maxTokens := 64
	temp := 0.2
	return &core.ChatRequest{
		Model:       "my-endpoint",
		System:      "be brief",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Stop:        []string{"END"},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "weather in Paris?"}}},
		},
		Tools: []core.Tool{{Name: "get_weather", Description: "weather lookup",
			Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)}},
		ToolChoice: "auto",
	}
}

func sageMakerDrain(t *testing.T, ch <-chan core.StreamChunk) []core.StreamChunk {
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
			t.Fatal("stream did not finish")
		}
	}
}

func TestSageMakerBaseURL(t *testing.T) {
	c := NewSageMaker("sagemaker", "")
	creds := core.Credentials{Extra: map[string]string{awsFieldRegion: "ap-southeast-1"}}
	if got, want := c.invokeURL(creds, "llama-ep", false), "https://runtime.sagemaker.ap-southeast-1.amazonaws.com/endpoints/llama-ep/invocations"; got != want {
		t.Errorf("invoke url = %q, want %q", got, want)
	}
	if got := c.invokeURL(creds, "llama-ep", true); !strings.HasSuffix(got, "/endpoints/llama-ep/invocations-response-stream") {
		t.Errorf("stream url = %q", got)
	}
	cn := core.Credentials{Extra: map[string]string{awsFieldRegion: "cn-north-1"}}
	if got := c.baseURL(cn); got != "https://runtime.sagemaker.cn-north-1.amazonaws.com.cn" {
		t.Errorf("china base = %q", got)
	}
	override := core.Credentials{BaseURL: "https://vpce-123.runtime.sagemaker.us-east-1.vpce.amazonaws.com/"}
	if got := c.baseURL(override); got != "https://vpce-123.runtime.sagemaker.us-east-1.vpce.amazonaws.com" {
		t.Errorf("override base = %q", got)
	}
}

func TestSageMakerChat(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/endpoints/my-endpoint/invocations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") || !strings.Contains(auth, "/eu-west-1/sagemaker/aws4_request") {
			t.Errorf("authorization = %q", auth)
		}
		if !strings.Contains(auth, "x-amzn-sagemaker-inference-component") {
			t.Errorf("inference component header not signed: %q", auth)
		}
		if r.Header.Get(sageMakerInferenceComponentHdr) != "ic-llama" {
			t.Errorf("inference component = %q", r.Header.Get(sageMakerInferenceComponentHdr))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","model":"/opt/ml/model","choices":[{"index":0,"message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
			"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30}}`)
	}))
	defer srv.Close()

	c := NewSageMaker("sagemaker", "")
	resp, err := c.Chat(context.Background(), sageMakerTestRequest(), sageMakerTestCreds(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["model"] != "my-endpoint" || gotBody["max_tokens"] != float64(64) || gotBody["tool_choice"] != "auto" {
		t.Errorf("body = %v", gotBody)
	}
	if _, ok := gotBody["stream"]; ok {
		t.Errorf("non-stream body carries stream: %v", gotBody)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %v", gotBody["messages"])
	}
	if resp.Model != "my-endpoint" || resp.FinishReason != core.FinishToolCalls {
		t.Errorf("model/finish = %q/%q", resp.Model, resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 21 || resp.Usage.CompletionTokens != 9 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].ToolCall == nil ||
		resp.Message.Content[0].ToolCall.Name != "get_weather" || string(resp.Message.Content[0].ToolCall.Arguments) != `{"city":"Paris"}` {
		t.Errorf("content = %+v", resp.Message.Content)
	}
}

func TestSageMakerChatRejectsNonChatResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"generated_text":"hi"}]`)
	}))
	defer srv.Close()
	_, err := NewSageMaker("sagemaker", "").Chat(context.Background(), sageMakerTestRequest(), sageMakerTestCreds(srv.URL))
	if pe := core.AsProviderError(err); pe == nil || pe.Kind != core.ErrUpstream || !strings.Contains(pe.Message, "Chat Completions") {
		t.Fatalf("err = %v", err)
	}
}

// writeSageMakerStream splits an SSE transcript into PayloadPart frames at
// arbitrary byte offsets, as the runtime does.
func writeSageMakerStream(w http.ResponseWriter, sse string, cuts ...int) {
	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	prev := 0
	for _, cut := range append(cuts, len(sse)) {
		part := sse[prev:cut]
		prev = cut
		_, _ = w.Write(encodeEventStreamFrameHeaders(map[string]string{
			":event-type": "PayloadPart", ":message-type": "event", ":content-type": "application/octet-stream",
		}, []byte(part)))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func TestSageMakerStreamReassemblesPayloadParts(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"content":"lo"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Rome\"}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}` + "\n\n" +
		"data: [DONE]\n\n"
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/endpoints/my-endpoint/invocations-response-stream" {
			t.Errorf("path = %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		// Cut inside "data:", inside a JSON string and right before a newline.
		writeSageMakerStream(w, sse, 3, 40, 81, 200, 201, 330)
	}))
	defer srv.Close()

	ch, err := NewSageMaker("sagemaker", "").Stream(context.Background(), sageMakerTestRequest(), sageMakerTestCreds(srv.URL), core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := sageMakerDrain(t, ch)
	if gotBody["stream"] != true {
		t.Errorf("stream flag missing: %v", gotBody)
	}
	if so, _ := gotBody["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v", gotBody["stream_options"])
	}

	var text, thinking, args string
	var toolName string
	for _, c := range chunks {
		switch c.Type {
		case core.ChunkText:
			text += c.Delta
		case core.ChunkThinking:
			thinking += c.Delta
		case core.ChunkToolCall:
			if c.ToolCall.Name != "" {
				toolName = c.ToolCall.Name
			}
			args += string(c.ToolCall.Arguments)
		case core.ChunkError:
			t.Fatalf("unexpected error chunk: %v", c.Err)
		}
	}
	if text != "Hello" || thinking != "thinking" || toolName != "get_weather" || args != `{"city":"Rome"}` {
		t.Errorf("text=%q thinking=%q tool=%q args=%q", text, thinking, toolName, args)
	}
	n := len(chunks)
	if n < 2 || chunks[n-2].Type != core.ChunkUsage || chunks[n-1].Type != core.ChunkFinish {
		t.Fatalf("stream must end with usage + finish, got %+v", chunks)
	}
	if chunks[n-2].Usage.PromptTokens != 12 || chunks[n-1].FinishReason != core.FinishToolCalls {
		t.Errorf("usage/finish = %+v / %q", chunks[n-2].Usage, chunks[n-1].FinishReason)
	}
}

func TestSageMakerStreamExceptionFrames(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		payload string
		kind    core.ErrorKind
	}{
		{"model timeout", map[string]string{":message-type": "exception", ":exception-type": "ModelStreamError"},
			`{"Message":"took too long","ErrorCode":"ModelInvocationTimeExceeded"}`, core.ErrTimeout},
		{"container context overflow", map[string]string{":message-type": "exception", ":exception-type": "ModelStreamError"},
			`{"Message":"This model's maximum context length is 8192 tokens","ErrorCode":"StreamError"}`, core.ErrContextWindow},
		{"internal", map[string]string{":message-type": "exception", ":exception-type": "InternalStreamFailure"},
			`{"Message":"boom"}`, core.ErrUpstream},
		{"throttled", map[string]string{":message-type": "exception", ":exception-type": "ThrottlingException"},
			`{"message":"Rate exceeded"}`, core.ErrRateLimit},
		{"error event", map[string]string{":message-type": "error", ":error-code": "AccessDeniedException", ":error-message": "nope"},
			``, core.ErrAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSageMakerStream(w, `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}`+"\n\n")
				_, _ = w.Write(encodeEventStreamFrameHeaders(tc.headers, []byte(tc.payload)))
			}))
			defer srv.Close()
			ch, err := NewSageMaker("sagemaker", "").Stream(context.Background(), sageMakerTestRequest(), sageMakerTestCreds(srv.URL), core.StreamConfig{})
			if err != nil {
				t.Fatal(err)
			}
			chunks := sageMakerDrain(t, ch)
			last := chunks[len(chunks)-1]
			if last.Type != core.ChunkError {
				t.Fatalf("last chunk = %+v", last)
			}
			pe := core.AsProviderError(last.Err)
			if pe.Kind != tc.kind || pe.Provider != "sagemaker" || pe.Model != "my-endpoint" {
				t.Errorf("err = %+v, want kind %s", pe, tc.kind)
			}
			for _, c := range chunks {
				if c.Type == core.ChunkFinish {
					t.Error("finish emitted after an error frame")
				}
			}
		})
	}
}

func TestSageMakerStatusErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		errType string
		body    string
		kind    core.ErrorKind
	}{
		{"model error keeps container status", 424, "ModelError",
			`{"ErrorCode":"CLIENT_ERROR_FROM_MODEL","Message":"Received client error (429) from primary","OriginalStatusCode":429,"OriginalMessage":"{\"error\":\"too many requests\"}"}`,
			core.ErrRateLimit},
		{"unknown endpoint", 400, "ValidationError", `{"message":"Endpoint my-endpoint of account 123456789012 not found."}`, core.ErrModelUnavailable},
		{"throttling", 400, "ThrottlingException", `{"message":"Rate exceeded"}`, core.ErrRateLimit},
		{"bad signature", 403, "InvalidSignatureException", `{"message":"The request signature we calculated does not match"}`, core.ErrAuth},
		{"expired token", 400, "ExpiredTokenException", `{"message":"The security token included in the request is expired"}`, core.ErrAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Amzn-ErrorType", tc.errType+":http://internal.amazon.com/coral/com.amazon.sagemaker/")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := NewSageMaker("sagemaker", "").Chat(context.Background(), sageMakerTestRequest(), sageMakerTestCreds(srv.URL))
			pe := core.AsProviderError(err)
			if pe == nil || pe.Kind != tc.kind {
				t.Fatalf("err = %+v, want kind %s", pe, tc.kind)
			}
		})
	}
}

func TestSageMakerRequiresAWSCredentials(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	// An API key must not turn into a bearer token: SageMaker only takes SigV4.
	creds := core.Credentials{BaseURL: srv.URL, APIKey: "bedrock-key", Extra: map[string]string{awsFieldRegion: "us-east-1"}}
	_, err := NewSageMaker("sagemaker", "").Chat(context.Background(), sageMakerTestRequest(), creds)
	if pe := core.AsProviderError(err); pe == nil || pe.Kind != core.ErrAuth {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Error("request sent without AWS credentials")
	}
	if err := NewSageMaker("sagemaker", "").Validate(context.Background(), creds); err == nil {
		t.Error("validate must fail without AWS credentials")
	}
}

func TestSageMakerStreamStopsOnCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSageMakerStream(w, `data: {"choices":[{"index":0,"delta":{"content":"a"}}]}`+"\n\n")
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := NewSageMaker("sagemaker", "").Stream(ctx, sageMakerTestRequest(), sageMakerTestCreds(srv.URL), core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if first := <-ch; first.Type != core.ChunkText {
		t.Fatalf("first chunk = %+v", first)
	}
	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
}

func TestSageMakerPartBytesAcceptsBase64Wrapper(t *testing.T) {
	f := &eventStreamFrame{headers: map[string]string{":content-type": "application/json"},
		payload: []byte(`{"Bytes":"ZGF0YTogeyJhIjoxfQo="}`)}
	if got := string(sageMakerPartBytes(f)); got != "data: {\"a\":1}\n" {
		t.Errorf("decoded = %q", got)
	}
	raw := &eventStreamFrame{headers: map[string]string{}, payload: []byte("data: x\n")}
	if got := string(sageMakerPartBytes(raw)); got != "data: x\n" {
		t.Errorf("raw = %q", got)
	}
}
