package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func withHeartbeat(d time.Duration) func(*e2eOptions) {
	return func(o *e2eOptions) { o.cfg.Server.StreamHeartbeatInterval = d }
}

// slowStreamUpstream waits before sending any byte, like a marketplace that
// holds the request until a seller is free, then streams a short answer.
func slowStreamUpstream(wait time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(wait)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"hel"}}]}`,
			`data: {"choices":[{"delta":{"content":"lo"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			fmt.Fprintf(w, "%s\n\n", l)
		}
	}
}

func outOfStockUpstream(wait time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(wait)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"code":"no_healthy_sellers","message":"no sellers"}}`)
	}
}

const streamBody = `{"model":"openai/gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`

// A slow first response gets headers and keep-alive frames before the
// answer, then the full answer on the same 200 stream.
func TestEarlyCommit_SlowUpstreamStillStreamsAnswer(t *testing.T) {
	h := newE2E(t, slowStreamUpstream(400*time.Millisecond), withHeartbeat(50*time.Millisecond))

	resp := h.post(t, "/v1/chat/completions", streamBody, h.apiKey)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(raw)
	require.True(t, strings.HasPrefix(out, ": keep-alive\n\n"), "stream must open with a heartbeat, got %q", out)
	require.Contains(t, out, `"content":"hel"`)
	require.Contains(t, out, `"content":"lo"`)
	require.Contains(t, out, "[DONE]")
}

// Out of stock after headers went out arrives as an in-band error event with
// a code the client can match on.
func TestEarlyCommit_SlowOutOfStockIsInBandError(t *testing.T) {
	h := newE2E(t, outOfStockUpstream(300*time.Millisecond), withHeartbeat(50*time.Millisecond))

	resp := h.post(t, "/v1/chat/completions", streamBody, h.apiKey)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(raw)
	require.Contains(t, out, ": keep-alive")
	require.Contains(t, out, `"code":"model_out_of_stock"`)
	require.Contains(t, out, "temporarily out of stock")
	require.NotContains(t, strings.ToLower(out), "no_healthy_sellers")
}

// A failure before the heartbeat interval keeps today's behavior: a real 503
// with Retry-After, so SDK retries keep working.
func TestEarlyCommit_FastOutOfStockKeepsStatus(t *testing.T) {
	h := newE2E(t, outOfStockUpstream(0), withHeartbeat(2*time.Second))

	resp := h.post(t, "/v1/chat/completions", streamBody, h.apiKey)
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Equal(t, "15", resp.Header.Get("Retry-After"))
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "keep-alive")
}

// A fast answer keeps the provider headers and has no heartbeat prefix.
func TestEarlyCommit_FastAnswerUnchanged(t *testing.T) {
	h := newE2E(t, slowStreamUpstream(0), withHeartbeat(2*time.Second))

	resp := h.post(t, "/v1/chat/completions", streamBody, h.apiKey)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "openai", resp.Header.Get("X-KeiRouter-Provider"))
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(raw), "data: "), "got %q", raw)
}

func TestEarlyCommit_FinishBeforeDelayWritesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	ec := startEarlyCommit(rec, func() {}, time.Hour, time.Hour)
	require.False(t, ec.finish())
	require.False(t, rec.Flushed)
	require.Empty(t, rec.Body.String())
	require.Empty(t, rec.Header().Get("Content-Type"))
}

func TestEarlyCommit_DisabledWhenIntervalZero(t *testing.T) {
	require.Nil(t, startEarlyCommit(httptest.NewRecorder(), func() {}, 0, 0))
	var ec *earlyCommit
	require.False(t, ec.finish())
}

func TestCommittedStreamErrorEvent(t *testing.T) {
	pe := &core.ProviderError{
		Kind: core.ErrModelOutOfStock, Provider: "surplus",
		Message:    `{"error":{"code":"no_healthy_sellers"}}`,
		RetryAfter: core.ModelOutOfStockRetryAfter,
	}

	payload := func(t *testing.T, ev []byte) map[string]any {
		t.Helper()
		s := string(ev)
		require.True(t, strings.HasSuffix(s, "\n\n"))
		i := strings.Index(s, "data: ")
		require.GreaterOrEqual(t, i, 0)
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(s[i+len("data: "):])), &m))
		require.NotContains(t, strings.ToLower(s), "surplus")
		return m
	}

	t.Run("openai", func(t *testing.T) {
		m := payload(t, committedStreamErrorEvent(core.DialectOpenAI, pe))
		require.Equal(t, "model_out_of_stock", m["error"].(map[string]any)["code"])
	})
	t.Run("anthropic", func(t *testing.T) {
		ev := committedStreamErrorEvent(core.DialectAnthropic, pe)
		require.True(t, strings.HasPrefix(string(ev), "event: error\n"))
		m := payload(t, ev)
		require.Equal(t, "overloaded_error", m["error"].(map[string]any)["type"])
	})
	t.Run("gemini", func(t *testing.T) {
		m := payload(t, committedStreamErrorEvent(core.DialectGemini, pe))
		require.Equal(t, "UNAVAILABLE", m["error"].(map[string]any)["status"])
	})
	t.Run("min discount", func(t *testing.T) {
		floor := &core.ProviderError{Kind: core.ErrMinDiscountNotMet, RetryAfter: core.ModelOutOfStockRetryAfter}
		m := payload(t, committedStreamErrorEvent(core.DialectOpenAI, floor))
		require.Equal(t, "min_discount_not_met", m["error"].(map[string]any)["code"])
	})
}
