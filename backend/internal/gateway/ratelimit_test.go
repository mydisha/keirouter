package gateway

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/limits"
)

func TestIPLimiterWindowAndLiveLimit(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	l := newIPLimiter(2, time.Minute)
	t.Cleanup(l.Stop)
	l.now = func() time.Time { return now }

	require.True(t, l.Allow("1.2.3.4"))
	require.True(t, l.Allow("1.2.3.4"))
	require.False(t, l.Allow("1.2.3.4"))
	require.True(t, l.Allow("5.6.7.8"), "limits are per IP")

	// Rejected attempts must not extend the window.
	now = now.Add(time.Minute)
	require.True(t, l.Allow("1.2.3.4"))

	l.SetLimit(0)
	for range 10 {
		require.True(t, l.Allow("1.2.3.4"), "zero limit disables the limiter")
	}
}

func TestIPLimiterExceededOnlyCountsRecorded(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	l := newIPLimiter(2, time.Minute)
	t.Cleanup(l.Stop)
	l.now = func() time.Time { return now }

	blocked, _ := l.Exceeded("1.2.3.4")
	require.False(t, blocked, "checking must not count as a failure")
	l.Record("1.2.3.4")
	blocked, _ = l.Exceeded("1.2.3.4")
	require.False(t, blocked)
	l.Record("1.2.3.4")

	now = now.Add(20 * time.Second)
	blocked, retryAfter := l.Exceeded("1.2.3.4")
	require.True(t, blocked)
	require.Equal(t, 40*time.Second, retryAfter)

	now = now.Add(40 * time.Second)
	blocked, _ = l.Exceeded("1.2.3.4")
	require.False(t, blocked, "window elapsed")
}

func TestNilIPLimiterAllows(t *testing.T) {
	var l *ipLimiter
	require.True(t, l.Allow("x"))
	blocked, _ := l.Exceeded("x")
	require.False(t, blocked)
	l.Record("x")
	l.SetLimit(5)
}

func TestE2E_AuthFailuresThrottleIP(t *testing.T) {
	h := newE2E(t, openAIUpstream(), func(o *e2eOptions) {
		o.cfg.Limits.AuthFailuresPerMinute = 2
	})
	body := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	const remote = "203.0.113.7:51000"

	for range 2 {
		resp := h.postFrom(t, remote, "/v1/chat/completions", body, "kr_invalid")
		resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
	resp := h.postFrom(t, remote, "/v1/chat/completions", body, "kr_invalid")
	resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))

	// A valid key from the same (blocked) IP still gets through: clients
	// behind one proxy share an IP, and one stale key must not lock out the rest.
	resp = h.postFrom(t, remote, "/v1/chat/completions", body, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Other IPs are unaffected.
	resp = h.postFrom(t, "198.51.100.9:40000", "/v1/chat/completions", body, "kr_invalid")
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Raising the limit from the dashboard takes effect immediately.
	limit := 10
	h.gateway.applyLimitSettings(EndpointSettings{RateLimitsEnabled: true, AuthFailuresPerMinute: &limit})
	resp = h.postFrom(t, remote, "/v1/chat/completions", body, "kr_invalid")
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestE2E_AuthFailuresIgnoreLoopback(t *testing.T) {
	h := newE2E(t, openAIUpstream(), func(o *e2eOptions) {
		o.cfg.Limits.AuthFailuresPerMinute = 1
	})
	body := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	// Local tools (and a reverse proxy on the same host) all arrive over
	// loopback; a stale key there keeps getting a plain 401, never a block.
	for _, remote := range []string{"127.0.0.1:50000", "[::1]:50000"} {
		for range 3 {
			resp := h.postFrom(t, remote, "/v1/chat/completions", body, "kr_invalid")
			resp.Body.Close()
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode, remote)
		}
	}
}

func TestE2E_SuccessfulRequestsDoNotCountAsAuthFailures(t *testing.T) {
	h := newE2E(t, openAIUpstream(), func(o *e2eOptions) {
		o.cfg.Limits.AuthFailuresPerMinute = 1
	})
	body := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	for range 3 {
		resp := h.post(t, "/v1/chat/completions", body, h.apiKey)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

// limitedE2E wires a per-key TPM limit of 100. The fake upstream reports 8
// total tokens per call while each request reserves its prompt plus
// max_tokens=60, so only reconciliation lets a second request fit.
func limitedE2E(t *testing.T, reconcile bool) *e2eHarness {
	cfg := config.Default()
	cfg.Limits.Enabled = true
	cfg.Limits.DefaultTPM = 100
	cfg.Limits.ReconcileTokens = reconcile
	lim := limits.NewMemory(limits.MemoryConfig{Enabled: true, Window: time.Minute})
	return newE2E(t, openAIUpstream(), func(o *e2eOptions) {
		o.cfg = cfg
		o.limiter = lim
	})
}

func TestE2E_TPMReconcilesActualUsage(t *testing.T) {
	body := `{"model":"openai/gpt-4o","max_tokens":60,"messages":[{"role":"user","content":"hi"}]}`

	h := limitedE2E(t, true)
	for i := range 3 {
		resp := h.post(t, "/v1/chat/completions", body, h.apiKey)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i)
	}

	h = limitedE2E(t, false)
	resp := h.post(t, "/v1/chat/completions", body, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = h.post(t, "/v1/chat/completions", body, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "estimate is kept when reconciliation is off")
}

func TestEffectiveLimitSettingsFallBackToConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.OutputReserveCap = 8192
	s := &Server{cfg: cfg}

	es := s.effectiveLimitSettings(EndpointSettings{})
	require.True(t, *es.TPMReconcileEnabled)
	require.EqualValues(t, 8192, *es.TPMOutputReserveCap)
	require.Equal(t, 1000, *es.ConcurrencyRetryAfterMs)
	require.Equal(t, 20, *es.AuthFailuresPerMinute)
	require.Equal(t, 5, *es.LoginAttemptsPerMinute)

	off := false
	es = s.effectiveLimitSettings(EndpointSettings{TPMReconcileEnabled: &off})
	require.False(t, *es.TPMReconcileEnabled, "dashboard value wins over config")
}

func TestE2E_StreamingSettlesTPMAndConcurrency(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.Enabled = true
	cfg.Limits.DefaultTPM = 100
	cfg.Limits.DefaultConcurrency = 1
	lim := limits.NewMemory(limits.MemoryConfig{Enabled: true, Window: time.Minute})
	h := newE2E(t, streamNoUsageUpstream(), func(o *e2eOptions) {
		o.cfg = cfg
		o.limiter = lim
	})

	body := `{"model":"openai/gpt-4o","stream":true,"max_tokens":60,"messages":[{"role":"user","content":"hi"}]}`
	for i := range 3 {
		resp := h.post(t, "/v1/chat/completions", body, h.apiKey)
		_, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i)
	}
}
