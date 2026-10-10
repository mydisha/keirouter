package connectors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestResponseHeaderTimeoutAppliesPerRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	ctx := core.WithResponseHeaderTimeout(context.Background(), 50*time.Millisecond)
	_, err := openStream(ctx, "openai", "gpt-4o", srv.URL, []byte(`{}`), nil)
	pe := core.AsProviderError(err)
	require.Equal(t, core.ErrTimeout, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.Scope, "a self-imposed header budget must not cool the account down")
	require.True(t, errors.Is(pe.Cause, context.DeadlineExceeded))
	require.NoError(t, ctx.Err(), "the caller's context must stay usable for the next attempt")

	// A generous budget lets the same slow server through.
	ctx = core.WithResponseHeaderTimeout(context.Background(), 5*time.Second)
	resp, err := openStream(ctx, "openai", "gpt-4o", srv.URL, []byte(`{}`), nil)
	require.NoError(t, err)
	resp.Body.Close()
}

func TestUnaryWithoutDeadlineGetsSafetyNetBudget(t *testing.T) {
	require.Equal(t, defaultUnaryHeaderTimeout, headerBudget(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	require.Equal(t, time.Duration(0), headerBudget(ctx), "a request deadline bounds unary calls on its own")
	require.Equal(t, 7*time.Second, headerBudget(core.WithResponseHeaderTimeout(ctx, 7*time.Second)))
}

func TestFreshConnectionReplayOnDialError(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	err := transportError(context.Background(), "openai", "gpt-4o", dialErr)
	require.True(t, shouldRetryFreshConnection(context.Background(), err))

	timeout := &net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr{}}
	err = transportError(context.Background(), "openai", "gpt-4o", timeout)
	require.Equal(t, core.ErrTimeout, core.AsProviderError(err).Kind)
	require.False(t, shouldRetryFreshConnection(context.Background(), err), "timeouts are not replayed blindly")
}

func TestUnaryRequestSurvivesServerClosingPooledConnection(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	body, err := doJSON(ctx, "openai", "gpt-4o", srv.URL, []byte(`{}`), nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(body))

	// The server drops the idle pooled connection (NAT timeout, restart).
	// Whether the transport notices first or the write lands on the dead
	// socket, the next call must succeed without surfacing an error.
	srv.CloseClientConnections()
	body, err = doJSON(ctx, "openai", "gpt-4o", srv.URL, []byte(`{}`), nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(body))
	require.Equal(t, 2, hits)
}

func TestHTTPStatusErrorBodyClassification(t *testing.T) {
	mk := func(status int, body string, headers map[string]string) *core.ProviderError {
		resp := &http.Response{StatusCode: status, Header: http.Header{}}
		for k, v := range headers {
			resp.Header.Set(k, v)
		}
		return core.AsProviderError(httpStatusError("openai", "gpt-4o", resp, []byte(body)))
	}

	pe := mk(408, `{"error":"request timeout"}`, nil)
	require.Equal(t, core.ErrTimeout, pe.Kind)
	require.True(t, pe.Fallbackable())

	pe = mk(400, `{"error":{"message":"This model's maximum context length is 128000 tokens","code":"context_length_exceeded"}}`, nil)
	require.Equal(t, core.ErrContextWindow, pe.Kind)
	require.Equal(t, core.FailureScopeModel, pe.EffectiveScope())
	require.True(t, pe.Fallbackable())

	pe = mk(400, `{"error":{"message":"Your request was rejected as a result of our safety system.","code":"content_policy_violation"}}`, nil)
	require.Equal(t, core.ErrContentFilter, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.EffectiveScope())

	pe = mk(503, `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`, nil)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, core.FailureScopeAccount, pe.EffectiveScope())

	pe = mk(503, `{"error":{"message":"overloaded"}}`, map[string]string{"Retry-After": "2"})
	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, 2*time.Second, pe.RetryAfter)

	pe = mk(500, `{"error":{"message":"boom"}}`, map[string]string{"Retry-After": "86400"})
	require.Equal(t, time.Hour, pe.RetryAfter, "transient hints are capped at an hour")

	pe = mk(400, `{"error":{"message":"Request too large for gpt-4o in organization org-x on tokens per min (TPM): Limit 30000"}}`, nil)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
}

func TestMergeBetaFlags(t *testing.T) {
	require.Equal(t, "a-2025-01-01,b-2025-02-02", mergeBetaFlags("a-2025-01-01", []string{"b-2025-02-02", "a-2025-01-01"}))
	require.Equal(t, "x-2025-01-01", mergeBetaFlags("", []string{"x-2025-01-01"}))
	require.Equal(t, "x-2025-01-01", mergeBetaFlags("x-2025-01-01", nil))
}

func TestStreamRequiredErrorRecognisesDashScopeThinking(t *testing.T) {
	err := &core.ProviderError{Kind: core.ErrBadRequest, StatusCode: 400,
		Message: "parameter.enable_thinking must be set to false for non-streaming calls"}
	if !isStreamRequiredError(err) {
		t.Fatal("DashScope enable_thinking/stream error must trigger the stream retry")
	}
	other := &core.ProviderError{Kind: core.ErrBadRequest, StatusCode: 400, Message: "enable_thinking is not supported"}
	if isStreamRequiredError(other) {
		t.Fatal("unrelated enable_thinking error must not trigger the stream retry")
	}
}
