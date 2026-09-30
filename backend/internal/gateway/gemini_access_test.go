package gateway

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// TestGeminiEdgeEnforcesModelAllowlist is the regression guard for the
// pre-existing bypass: the Gemini generateContent edge must apply the same
// per-key model allowlist as the OpenAI/Anthropic edges.
func TestGeminiEdgeEnforcesModelAllowlist(t *testing.T) {
	h := newE2E(t, openAIUpstream())

	// A chain the request can route through (bare name, no slash).
	require.NoError(t, h.gateway.chains.Create(context.Background(), store.Chain{
		ID:        "chain-gem",
		TenantID:  store.DefaultTenantID,
		Name:      "gem",
		Strategy:  "fallback",
		Steps:     []store.ChainStep{{Position: 0, Provider: "openai", Model: "gpt-4o"}},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}))

	rec, err := h.gateway.identity.Authenticate(context.Background(), h.apiKey)
	require.NoError(t, err)

	// Allowlist excludes the chain -> 403.
	require.NoError(t, h.gateway.identity.Keys().SetAllowedModels(context.Background(), rec.ID, []string{"something-else"}))
	resp := h.post(t, "/v1beta/models/gem:generateContent", `{"contents":[{"parts":[{"text":"hi"}]}]}`, h.apiKey)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Allowlist includes the chain -> not 403.
	require.NoError(t, h.gateway.identity.Keys().SetAllowedModels(context.Background(), rec.ID, []string{"gem"}))
	resp2 := h.post(t, "/v1beta/models/gem:generateContent", `{"contents":[{"parts":[{"text":"hi"}]}]}`, h.apiKey)
	body := readAllString(t, resp2)
	require.NotEqual(t, http.StatusForbidden, resp2.StatusCode, body)
}

func readAllString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
