package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/mydisha/keirouter/backend/internal/consolelog"
	"github.com/mydisha/keirouter/backend/internal/core"
)

func finals(buf *consolelog.Buffer) []consolelog.Entry {
	var out []consolelog.Entry
	for _, e := range buf.Entries() {
		if e.Final {
			out = append(out, e)
		}
	}
	return out
}

func TestTraceMissingKeyWritesOneFinalLine(t *testing.T) {
	buf := consolelog.New()
	s := &Server{consoleLog: buf}
	h := chimiddleware.RequestID(s.authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run without a key")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	got := finals(buf)
	if len(got) != 1 {
		t.Fatalf("final lines = %d: %+v", len(got), buf.Entries())
	}
	e := got[0]
	if e.Status != 401 || e.Level != "WARN" || e.ReqID == "" || e.Endpoint != "POST /v1/chat/completions" || e.Hint == "" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestTraceSettleQuotesErrorBody(t *testing.T) {
	buf := consolelog.New()
	tr := newReqTrace(buf, httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil))
	tr.settle(http.StatusBadRequest, []byte(`{"error":{"message":"model is required","type":"invalid_request_error"}}`))
	tr.settle(http.StatusBadRequest, nil) // second call is a no-op

	got := finals(buf)
	if len(got) != 1 || got[0].Message != "Rejected · /v1/embeddings · model is required" || got[0].Status != 400 {
		t.Fatalf("finals = %+v", got)
	}
}

func TestTraceCompleteExplainsProviderFailure(t *testing.T) {
	buf := consolelog.New()
	tr := newReqTrace(buf, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	tr.setRequest("sonnet", true)
	tr.AttemptFailed("anthropic", "claude-sonnet-4", &core.ProviderError{Kind: core.ErrUpstream, StatusCode: 529, Provider: "anthropic"})
	tr.complete(outcome{fallbacks: 1, err: &core.ProviderError{
		Kind: core.ErrRateLimit, Provider: "openrouter", Model: "x", StatusCode: 429, RetryAfter: 3 * time.Second, Message: "slow down",
	}})
	tr.settle(http.StatusTooManyRequests, nil) // already finished

	all := buf.Entries()
	if len(all) != 2 {
		t.Fatalf("entries = %+v", all)
	}
	if all[0].Level != "WARN" || !strings.Contains(all[0].Message, "anthropic/claude-sonnet-4 failed") {
		t.Fatalf("attempt line = %+v", all[0])
	}
	e := all[1]
	if !e.Final || e.Level != "ERROR" || e.Status != 429 || e.Model != "sonnet" || !e.Stream {
		t.Fatalf("final = %+v", e)
	}
	if !strings.Contains(e.Message, "rate-limited by openrouter (HTTP 429)") || !strings.Contains(e.Message, "2 attempts") {
		t.Fatalf("message = %q", e.Message)
	}
	if !strings.Contains(e.Hint, "Retry-After: 3.0s") || !strings.Contains(e.Detail, "slow down") {
		t.Fatalf("hint/detail = %q / %q", e.Hint, e.Detail)
	}
}

func TestTraceClientDisconnectIsWarning(t *testing.T) {
	buf := consolelog.New()
	tr := newReqTrace(buf, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	tr.setRequest("gpt-4o", true)
	tr.complete(outcome{provider: "openai", model: "gpt-4o", chunks: 12, err: &streamWriteError{err: errBrokenPipe{}}})

	e := finals(buf)[0]
	if e.Level != "WARN" || e.Status != 499 || !strings.HasPrefix(e.Message, "Client disconnected · gpt-4o") {
		t.Fatalf("entry = %+v", e)
	}
}

func TestTraceSuccessSummary(t *testing.T) {
	buf := consolelog.New()
	tr := newReqTrace(buf, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	tr.setRequest("fast", false)
	tr.complete(outcome{provider: "groq", model: "llama-3.3-70b", tokens: 1234, costMicros: 3100})

	e := finals(buf)[0]
	if e.Level != "INFO" || e.Status != 200 || e.Target != "groq/llama-3.3-70b" {
		t.Fatalf("entry = %+v", e)
	}
	if want := "Completed · fast → groq/llama-3.3-70b · 1,234 tokens · $0.0031 · "; !strings.HasPrefix(e.Message, want) {
		t.Fatalf("message = %q, want prefix %q", e.Message, want)
	}
}

type errBrokenPipe struct{}

func (errBrokenPipe) Error() string { return "write: broken pipe" }
