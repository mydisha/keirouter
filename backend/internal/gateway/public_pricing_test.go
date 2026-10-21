package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func adminPublicPricing(t *testing.T, gw *Server, method, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/public-pricing", strings.NewReader(body))
	rec := httptest.NewRecorder()
	if method == http.MethodPut {
		gw.adminUpdatePublicPricing(rec, req)
	} else {
		gw.adminGetPublicPricing(rec, req)
	}
	var out map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec, out
}

func getPublicPricing(t *testing.T, h http.Handler, ip, query string) (*httptest.ResponseRecorder, pricingResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/pricing"+query, nil)
	req.RemoteAddr = ip + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out pricingResp
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec, out
}

// Nothing is public until an admin turns it on and picks chains; then only
// the picked Surplus chains are shown, with no API key.
func TestPublicPricingOnlyPublishedChains(t *testing.T) {
	gw, _, _ := pricingFixture(t)
	h := gw.routes()

	rec, _ := getPublicPricing(t, h, "203.0.113.1", "")
	require.Equal(t, http.StatusNotFound, rec.Code, "off by default")

	// The admin tab lists only Surplus-priced chains, none published yet.
	rec, cfg := adminPublicPricing(t, gw, http.MethodGet, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, false, cfg["enabled"])
	models := cfg["models"].([]any)
	require.Len(t, models, 2, "fixed is not Surplus-priced")
	require.Equal(t, "glm-trusted", models[0].(map[string]any)["model"])
	require.Equal(t, false, models[0].(map[string]any)["published"])

	// Only Surplus-priced chains can be published.
	rec, _ = adminPublicPricing(t, gw, http.MethodPut, `{"enabled":true,"chain_ids":["fixed"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec, cfg = adminPublicPricing(t, gw, http.MethodPut, `{"enabled":true,"chain_ids":["glm","glm"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []any{"glm"}, cfg["chain_ids"])

	rec, out := getPublicPricing(t, h, "203.0.113.1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "surplus", out.Source)
	require.Len(t, out.Models, 1)
	require.Equal(t, "glm-trusted", out.Models[0]["model"])
	require.InDelta(t, 0.25, out.Models[0]["input_per_m"], 1e-9)

	// Unpublished chains are unknown, whatever the caller asks for.
	rec, out = getPublicPricing(t, h, "203.0.113.1", "?model=glm-trusted,luna-trusted,fixed")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"luna-trusted", "fixed"}, out.Unknown)
	rec, _ = getPublicPricing(t, h, "203.0.113.1", "?model=luna-trusted")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Turning it off hides everything again.
	rec, _ = adminPublicPricing(t, gw, http.MethodPut, `{"enabled":false,"chain_ids":["glm"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = getPublicPricing(t, h, "203.0.113.1", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// Each client IP gets 30 requests a minute; another IP is unaffected.
func TestPublicPricingLimitsPerIP(t *testing.T) {
	gw, _, _ := pricingFixture(t)
	rec, _ := adminPublicPricing(t, gw, http.MethodPut, `{"enabled":true,"chain_ids":["glm"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	h := gw.routes()

	for i := 0; i < pricingRequestsPerMinute; i++ {
		rec, _ = getPublicPricing(t, h, "203.0.113.5", "")
		require.Equal(t, http.StatusOK, rec.Code, "request %d", i+1)
	}
	rec, _ = getPublicPricing(t, h, "203.0.113.5", "")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	rec, _ = getPublicPricing(t, h, "203.0.113.6", "")
	require.Equal(t, http.StatusOK, rec.Code)
}
