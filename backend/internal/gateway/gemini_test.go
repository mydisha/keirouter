package gateway

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/limits"
)

const geminiBody = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

func TestGemini_ProviderModelWithSlashIsRouted(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	resp := h.post(t, "/v1beta/models/openai/gpt-4o:generateContent", geminiBody, h.apiKey)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	require.Contains(t, string(body), "hello from upstream")
}

func TestGemini_KeyModelRestrictionApplies(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	require.NoError(t, h.gateway.identity.Keys().SetAllowedModels(context.Background(), h.keyID, []string{"gpt-4o-mini"}))

	resp := h.post(t, "/v1beta/models/openai/gpt-4o:generateContent", geminiBody, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestGemini_KeyRateLimitApplies(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.Enabled = true
	cfg.Limits.DefaultRPM = 1
	lim := limits.NewMemory(limits.MemoryConfig{Enabled: true, Window: time.Minute})
	h := newE2E(t, openAIUpstream(), func(o *e2eOptions) {
		o.cfg = cfg
		o.limiter = lim
	})

	resp := h.post(t, "/v1beta/models/openai/gpt-4o:generateContent", geminiBody, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = h.post(t, "/v1beta/models/openai/gpt-4o:generateContent", geminiBody, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}
