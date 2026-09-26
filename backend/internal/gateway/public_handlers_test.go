package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestPublicOverviewEmptyDBIsZeroed(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/overview", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"total_requests":0,"total_tokens":0,"rps_10s":0,
		"success_24h":0,"failed_24h":0,"top_models":[]}`, rec.Body.String())
}

func TestPublicModelsEmptyDBIsEmptyArray(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/models", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"models":[]}`, rec.Body.String())
}

// TestPublicModelsHidesProviderSecrets seeds real catalog models with usage and
// asserts the full item shape, aggregation, request-descending order, and that
// the serialized body contains no provider/caller secrets. Running it on a
// populated DB (not an empty one) is what makes the NotContains checks bite.
func TestPublicModelsHidesProviderSecrets(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []store.UsageRecord{
		// gpt-4o: 3 requests, 2 distinct keys, 300 prompt + 60 completion tokens.
		{ID: "p1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		{ID: "p2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		{ID: "p3", TenantID: store.DefaultTenantID, APIKeyID: "key-b", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		// gpt-4o-mini: 1 request (fewer, so it must sort after gpt-4o), 1 key.
		{ID: "p4", TenantID: store.DefaultTenantID, APIKeyID: "key-c", Provider: "openai", Model: "gpt-4o-mini", Status: "success", PromptTokens: 10, CompletionTokens: 5, InputRatePerM: 0.15, OutputRatePerM: 0.6, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/models", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Models []struct {
			Name         string          `json:"name"`
			ModelID      string          `json:"model_id"`
			Provider     string          `json:"provider"`
			InputPerM    float64         `json:"input_per_m"`
			OutputPerM   float64         `json:"output_per_m"`
			Capabilities json.RawMessage `json:"capabilities"`
			Usage24h     struct {
				Users    int   `json:"users"`
				Requests int64 `json:"requests"`
				Tokens   int64 `json:"tokens"`
			} `json:"usage_24h"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 2)

	first := payload.Models[0]
	require.Equal(t, "gpt-4o", first.ModelID)
	require.Equal(t, "GPT-4o", first.Name)
	require.Equal(t, "OpenAI", first.Provider)
	require.NotEmpty(t, first.Capabilities)
	require.Equal(t, 2.5, first.InputPerM)
	require.Equal(t, 10.0, first.OutputPerM)
	require.Equal(t, 2, first.Usage24h.Users)
	require.Equal(t, int64(3), first.Usage24h.Requests)
	require.Equal(t, int64(360), first.Usage24h.Tokens)

	second := payload.Models[1]
	require.Equal(t, "gpt-4o-mini", second.ModelID)
	require.Equal(t, 1, second.Usage24h.Users)
	require.Equal(t, int64(1), second.Usage24h.Requests)

	body := rec.Body.String()
	for _, secret := range []string{"api_key", "base_url", "request_id", "key_id", "key_name", "account_id", "pricing_key"} {
		require.NotContains(t, body, secret)
	}
}

func TestPublicPerformanceRejectsUnknownModel(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/performance?model=../../etc/passwd", nil)
	gw.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "unknown model")
}

func TestPublicPerformanceRejectsEmptyModel(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/performance", nil)
	gw.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "unknown model")
}

// TestPublicPerformanceReturnsKnownModel seeds usage for a catalog model and
// asserts the per-model scalars plus a 24-entry hourly series, and that the
// serialized body leaks no provider/caller secrets.
func TestPublicPerformanceReturnsKnownModel(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []store.UsageRecord{
		{ID: "q1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, EndToEndLatencyMS: 1000, TTFTMS: 200, CreatedAt: now},
		{ID: "q2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, EndToEndLatencyMS: 2000, TTFTMS: 400, CreatedAt: now},
		{ID: "q3", TenantID: store.DefaultTenantID, APIKeyID: "key-b", Provider: "openai", Model: "gpt-4o", Status: "error", PromptTokens: 50, CompletionTokens: 0, EndToEndLatencyMS: 3000, TTFTMS: 600, CreatedAt: now},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/performance?model=gpt-4o", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Model        string  `json:"model"`
		AvgLatencyMS int64   `json:"avg_latency_ms"`
		AvgTTFTMS    int64   `json:"avg_ttft_ms"`
		SuccessRate  float64 `json:"success_rate"`
		Series       []struct {
			Bucket   int   `json:"bucket"`
			Requests int64 `json:"requests"`
			Tokens   int64 `json:"tokens"`
		} `json:"series"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "gpt-4o", payload.Model)
	require.Equal(t, int64(2000), payload.AvgLatencyMS)
	require.Equal(t, int64(400), payload.AvgTTFTMS)
	require.InDelta(t, 2.0/3.0, payload.SuccessRate, 1e-9)
	require.Len(t, payload.Series, 24)

	body := rec.Body.String()
	for _, secret := range []string{"api_key", "base_url", "request_id", "key_id", "key_name", "account_id", "pricing_key", "error_kind"} {
		require.NotContains(t, body, secret)
	}
}

func newPublicTestGateway(t *testing.T) *Server {
	t.Helper()
	_, gw := newPublicTestGatewayWithDB(t)
	return gw
}

// newPublicTestGatewayWithDB returns both the migrated store and the gateway so
// a test can seed usage records before exercising the handlers.
func newPublicTestGatewayWithDB(t *testing.T) (*store.DB, *Server) {
	t.Helper()
	db, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background()))
	require.NoError(t, db.Tenants().EnsureDefault(context.Background()))
	t.Cleanup(func() { _ = db.Close() })
	return db, New(Deps{Config: config.Default(), DB: db, Usage: db.Usage(), Settings: db.Settings()})
}
