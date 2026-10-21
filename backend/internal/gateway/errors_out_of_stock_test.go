package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// The client sees the real condition (503, out of stock, retry in 30s) with no
// upstream provider name, support address or raw upstream text.
func TestWriteProviderError_ModelOutOfStock(t *testing.T) {
	pe := &core.ProviderError{
		Kind:       core.ErrModelOutOfStock,
		Scope:      core.FailureScopeRequest,
		Provider:   "surplus",
		Model:      "gemini-3.8-flash",
		StatusCode: http.StatusServiceUnavailable,
		Message:    `{"error":{"code":"no_healthy_sellers","message":"Contact support@surplusintelligence.ai"}}`,
		RetryAfter: core.ModelOutOfStockRetryAfter,
	}

	for _, dialect := range []core.Dialect{core.DialectOpenAI, core.DialectAnthropic, core.DialectGemini} {
		t.Run(string(dialect), func(t *testing.T) {
			rec := httptest.NewRecorder()
			(&Server{}).writeProviderErrorAs(rec, dialect, pe)

			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.Equal(t, "30", rec.Header().Get("Retry-After"))
			body := rec.Body.String()
			require.True(t, json.Valid([]byte(body)))
			require.Contains(t, body, "temporarily out of stock")
			require.NotContains(t, strings.ToLower(body), "surplus")
			require.NotContains(t, body, "no_healthy_sellers")
		})
	}
}

// An unmet discount floor is a 422 telling the client why, without upstream
// names or the raw Surplus message.
func TestWriteProviderError_MinDiscountNotMet(t *testing.T) {
	pe := &core.ProviderError{
		Kind: core.ErrMinDiscountNotMet, Scope: core.FailureScopeRequest, Provider: "surplus",
		StatusCode: http.StatusNotFound,
		Message:    `{"error":{"code":"minimum_discount_not_met","message":"No marketplace sellers ..."}}`,
	}
	rec := httptest.NewRecorder()
	(&Server{}).writeProviderErrorAs(rec, core.DialectOpenAI, pe)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "minimum discount")
	require.NotContains(t, strings.ToLower(body), "surplus")
	require.NotContains(t, body, "minimum_discount_not_met")
}
