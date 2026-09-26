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
		"success_24h":0,"failed_24h":0,"top_models":[],"recent":[]}`, rec.Body.String())
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
// asserts the per-model scalars plus a 24-entry hourly series whose content is
// correct (requests/tokens land in the expected bucket), and that the
// serialized body leaks no provider/caller secrets.
func TestPublicPerformanceReturnsKnownModel(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	// The handler buckets with slotSecs = int(span.Seconds())/24 over the last
	// 24h. Seed at bucket midpoints so the expected index is stable across the
	// few ms between here and the request.
	now := time.Now().UTC()
	slotSecs := int64((24 * time.Hour).Seconds()) / 24 // 3600
	since := now.Add(-24 * time.Hour)
	bucketOf := func(t time.Time) int { return int(t.Sub(since).Seconds()) / int(slotSecs) }
	seedA := now.Add(-2*time.Hour - 30*time.Minute) // midpoint of bucket 21
	seedB := now.Add(-5*time.Hour - 30*time.Minute) // midpoint of bucket 18
	bucketA, bucketB := bucketOf(seedA), bucketOf(seedB)
	require.NotEqual(t, bucketA, bucketB)

	records := []store.UsageRecord{
		{ID: "q1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, EndToEndLatencyMS: 1000, TTFTMS: 200, CreatedAt: seedA},
		{ID: "q2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, EndToEndLatencyMS: 2000, TTFTMS: 400, CreatedAt: seedA},
		{ID: "q3", TenantID: store.DefaultTenantID, APIKeyID: "key-b", Provider: "openai", Model: "gpt-4o", Status: "error", PromptTokens: 50, CompletionTokens: 0, EndToEndLatencyMS: 3000, TTFTMS: 600, CreatedAt: seedB},
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

	// The series must reflect the seeded records in their expected buckets:
	// seedA (2 records) -> 2 requests, 240 tokens; seedB (1 record) -> 1
	// request, 50 tokens. A dropped/zeroed/mis-bucketed series fails here.
	byBucket := make(map[int]struct{ requests, tokens int64 })
	for _, b := range payload.Series {
		byBucket[b.Bucket] = struct{ requests, tokens int64 }{b.Requests, b.Tokens}
	}
	require.Equal(t, int64(2), byBucket[bucketA].requests)
	require.Equal(t, int64(240), byBucket[bucketA].tokens)
	require.Equal(t, int64(1), byBucket[bucketB].requests)
	require.Equal(t, int64(50), byBucket[bucketB].tokens)

	body := rec.Body.String()
	for _, secret := range []string{"api_key", "base_url", "request_id", "key_id", "key_name", "account_id", "pricing_key", "error_kind"} {
		require.NotContains(t, body, secret)
	}
}

func TestPublicArchivedEmptyDBIsEmptyArrays(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/archived", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"podium":[],"history":[]}`, rec.Body.String())
}

// TestPublicArchivedPodiumAndHistory seeds three models with descending token
// totals and asserts the all-time podium (top two by tokens) plus a full
// per-model history, and that the serialized body leaks no secrets.
func TestPublicArchivedPodiumAndHistory(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []store.UsageRecord{
		{ID: "a1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "big", Status: "success", PromptTokens: 900, CompletionTokens: 100, CreatedAt: now},
		{ID: "a2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "mid", Status: "success", PromptTokens: 400, CompletionTokens: 100, CreatedAt: now},
		{ID: "a3", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "anthropic", Model: "small", Status: "success", PromptTokens: 50, CompletionTokens: 50, CreatedAt: now},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/archived", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	type item struct {
		Model    string `json:"model"`
		Tokens   int64  `json:"tokens"`
		Requests int64  `json:"requests"`
		Status   string `json:"status"`
	}
	var payload struct {
		Podium  []item `json:"podium"`
		History []item `json:"history"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Podium, 2)
	require.Equal(t, "big", payload.Podium[0].Model)
	require.Equal(t, int64(1000), payload.Podium[0].Tokens)
	require.Equal(t, int64(1), payload.Podium[0].Requests)
	require.Equal(t, "mid", payload.Podium[1].Model)

	require.Len(t, payload.History, 3)
	for _, h := range payload.History {
		require.Equal(t, "arsip", h.Status)
	}
	require.Equal(t, "big", payload.History[0].Model)
	require.Equal(t, "small", payload.History[2].Model)

	body := rec.Body.String()
	for _, secret := range []string{"api_key", "base_url", "request_id", "key_id", "key_name", "account_id", "pricing_key", "error_kind"} {
		require.NotContains(t, body, secret)
	}
}

// TestPublicRecentHasNoIdentifiers seeds a record and asserts publicOverview's
// recent array carries the redacted fields only — provider/model/status and
// latencies, never request_id or any key/account identifier.
func TestPublicRecentHasNoIdentifiers(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []store.UsageRecord{
		{ID: "r1", RequestID: "req-secret-1", TenantID: store.DefaultTenantID, APIKeyID: "key-secret", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, LatencyMS: 1234, TTFTMS: 210, CreatedAt: now},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/overview", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Recent []struct {
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			Status    string `json:"status"`
			LatencyMS int    `json:"latency_ms"`
			TTFTMS    int    `json:"ttft_ms"`
		} `json:"recent"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Recent, 1)
	require.Equal(t, "openai", payload.Recent[0].Provider)
	require.Equal(t, "gpt-4o", payload.Recent[0].Model)
	require.Equal(t, "success", payload.Recent[0].Status)
	require.Equal(t, 1234, payload.Recent[0].LatencyMS)
	require.Equal(t, 210, payload.Recent[0].TTFTMS)

	body := rec.Body.String()
	for _, secret := range []string{"api_key", "base_url", "request_id", "key_id", "key_name", "account_id", "pricing_key", "error_kind"} {
		require.NotContains(t, body, secret)
	}
}

// TestPublicRejectsNonGet locks the security contract from spec §6: the
// public API is read-only. A POST to a GET-only route must not dispatch; chi
// answers 405 (or 404), never a handler run.
func TestPublicRejectsNonGet(t *testing.T) {
	gw := newPublicTestGateway(t)
	for _, path := range []string{
		"/v1/public/overview",
		"/v1/public/models",
		"/v1/public/performance",
		"/v1/public/archived",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		gw.Handler().ServeHTTP(rec, req)
		require.Contains(t, []int{http.StatusMethodNotAllowed, http.StatusNotFound}, rec.Code, path)
	}
}

// TestPublicRateLimitReturns429 locks the per-IP budget: requests within budget
// are served (200), and once the budget is spent the limiter short-circuits
// with 429. Looping to 100 makes the test independent of the exact budget.
func TestPublicRateLimitReturns429(t *testing.T) {
	gw := newPublicTestGateway(t)
	first, last := 0, 0
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/public/overview", nil)
		gw.Handler().ServeHTTP(rec, req)
		last = rec.Code
		if i == 0 {
			first = rec.Code
		}
		require.Contains(t, []int{http.StatusOK, http.StatusTooManyRequests}, rec.Code, "request %d", i)
		if last == http.StatusTooManyRequests {
			break
		}
	}
	require.Equal(t, http.StatusOK, first, "first request within budget must be allowed")
	require.Equal(t, http.StatusTooManyRequests, last, "must exceed the per-IP budget within 100 requests")
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
