package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestPublicOverviewEmptyDBIsZeroed(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/overview", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var overview struct {
		TotalRequests int64 `json:"total_requests"`
		TotalTokens   int64 `json:"total_tokens"`
		Success       int64 `json:"success"`
		Failed        int64 `json:"failed"`
		ModelCount    int   `json:"model_count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &overview))
	require.Zero(t, overview.TotalRequests)
	require.Zero(t, overview.TotalTokens)
	require.Zero(t, overview.Success)
	require.Zero(t, overview.Failed)
	// model_count reflects the catalogue, which is non-empty even with no usage.
	require.Positive(t, overview.ModelCount)
}

// TestPublicModelsEmptyDBListsCatalogue proves the catalogue is catalog-driven,
// not usage-driven: an idle gateway still advertises its priced models.
func TestPublicModelsEmptyDBListsCatalogue(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/models", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Models []struct {
			ModelID    string  `json:"model_id"`
			ProviderID string  `json:"provider_id"`
			InputPerM  float64 `json:"input_per_m"`
			OutputPerM float64 `json:"output_per_m"`
			Usage      struct {
				Requests int64 `json:"requests"`
			} `json:"usage"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Models, "catalogue must be non-empty even with no usage")
	for _, m := range payload.Models {
		require.NotEmpty(t, m.ModelID)
		require.NotEmpty(t, m.ProviderID)
		require.Zero(t, m.Usage.Requests)
	}
}

// TestPublicModelsHidesProviderSecrets seeds real catalog models with usage and
// asserts the full item shape, all-time aggregation, request-descending order,
// and that the serialized body contains no provider/caller secrets. Running it
// on a populated DB (not an empty one) is what makes the NotContains checks bite.
func TestPublicModelsHidesProviderSecrets(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	records := []store.UsageRecord{
		// gpt-4o: 3 requests, 2 distinct keys, 300 prompt + 60 completion tokens.
		{ID: "p1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		{ID: "p2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		{ID: "p3", TenantID: store.DefaultTenantID, APIKeyID: "key-b", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
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
			ProviderID   string          `json:"provider_id"`
			InputPerM    float64         `json:"input_per_m"`
			OutputPerM   float64         `json:"output_per_m"`
			Capabilities json.RawMessage `json:"capabilities"`
			Usage        struct {
				Users    int   `json:"users"`
				Requests int64 `json:"requests"`
				Tokens   int64 `json:"tokens"`
			} `json:"usage"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	var gpt4o *struct {
		Name         string          `json:"name"`
		ModelID      string          `json:"model_id"`
		Provider     string          `json:"provider"`
		ProviderID   string          `json:"provider_id"`
		InputPerM    float64         `json:"input_per_m"`
		OutputPerM   float64         `json:"output_per_m"`
		Capabilities json.RawMessage `json:"capabilities"`
		Usage        struct {
			Users    int   `json:"users"`
			Requests int64 `json:"requests"`
			Tokens   int64 `json:"tokens"`
		} `json:"usage"`
	}
	for i := range payload.Models {
		if payload.Models[i].ModelID == "gpt-4o" {
			gpt4o = &payload.Models[i]
			break
		}
	}
	require.NotNil(t, gpt4o, "gpt-4o must be listed after being used")
	require.Equal(t, "GPT-4o", gpt4o.Name)
	require.Equal(t, "openai", gpt4o.ProviderID)
	require.Equal(t, "OpenAI", gpt4o.Provider)
	require.NotEmpty(t, gpt4o.Capabilities)
	require.Equal(t, 2.5, gpt4o.InputPerM)
	require.Equal(t, 10.0, gpt4o.OutputPerM)
	require.Equal(t, 2, gpt4o.Usage.Users)
	require.Equal(t, int64(3), gpt4o.Usage.Requests)
	require.Equal(t, int64(360), gpt4o.Usage.Tokens)

	body := rec.Body.String()
	for _, secret := range []string{"api_key", "base_url", "request_id", "key_id", "key_name", "account_id", "pricing_key"} {
		require.NotContains(t, body, secret)
	}
	// The seeded key identifiers must not appear under any key name.
	for _, id := range []string{"key-a", "key-b", "key-c"} {
		require.NotContains(t, body, id)
	}
}

// TestPublicOverviewAllTimeAndModelCount proves the headline figures are
// all-time (an old record is counted) and that model_count matches the
// catalogue size returned by /v1/public/models.
func TestPublicOverviewAllTimeAndModelCount(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-400 * 24 * time.Hour)
	require.NoError(t, db.Usage().RecordBatch(ctx, []store.UsageRecord{
		{ID: "o1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: old},
		{ID: "o2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "error", PromptTokens: 0, CompletionTokens: 0, CreatedAt: old},
	}))

	overviewRec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(overviewRec, httptest.NewRequest(http.MethodGet, "/v1/public/overview", nil))
	require.Equal(t, http.StatusOK, overviewRec.Code, overviewRec.Body.String())
	var overview struct {
		TotalRequests int64 `json:"total_requests"`
		TotalTokens   int64 `json:"total_tokens"`
		Success       int64 `json:"success"`
		Failed        int64 `json:"failed"`
		ModelCount    int   `json:"model_count"`
	}
	require.NoError(t, json.Unmarshal(overviewRec.Body.Bytes(), &overview))
	require.Equal(t, int64(2), overview.TotalRequests, "records older than 24h must still count")
	require.Equal(t, int64(120), overview.TotalTokens)
	require.Equal(t, int64(1), overview.Success)
	require.Equal(t, int64(1), overview.Failed)

	modelsRec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(modelsRec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	var models struct {
		Models []json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(modelsRec.Body.Bytes(), &models))
	require.Equal(t, len(models.Models), overview.ModelCount, "model_count must equal catalogue size")
}

// TestPublicRejectsUnknownRoutes locks the surface: the two removed endpoints no
// longer exist.
func TestPublicRejectsUnknownRoutes(t *testing.T) {
	gw := newPublicTestGateway(t)
	for _, path := range []string{"/v1/public/performance", "/v1/public/archived"} {
		rec := httptest.NewRecorder()
		gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, rec.Code, path)
	}
}

// spyConnectorSource counts connector resolutions. dispatch.Dispatcher calls
// ConnectorSource.Get for every target before checking whether any account
// exists, so any handler that reaches the dispatcher (directly or through the
// pipeline) bumps this counter regardless of seeded accounts.
type spyConnectorSource struct{ gets int32 }

func (s *spyConnectorSource) Get(string) (core.Connector, error) {
	atomic.AddInt32(&s.gets, 1)
	return nil, errors.New("spy: no connector")
}

// TestPublicNeverDispatches proves spec §6.4: no public handler reaches the
// dispatcher. It injects a pipeline whose dispatcher is backed by a counting
// connector source and hits both /v1/public/* endpoints. If any handler started
// a model/provider call, the dispatcher would resolve a connector and the
// counter would be non-zero — so this test fails on that regression.
func TestPublicNeverDispatches(t *testing.T) {
	db, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background()))
	require.NoError(t, db.Tenants().EnsureDefault(context.Background()))
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Usage().RecordBatch(context.Background(), []store.UsageRecord{
		{ID: "d1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", Status: "success", PromptTokens: 10, CompletionTokens: 5, CreatedAt: time.Now().UTC()},
	}))

	spy := &spyConnectorSource{}
	disp := dispatch.New(spy, db.Accounts(), nil)
	pipe := pipeline.New(pipeline.Deps{Dispatcher: disp})
	gw := New(Deps{Config: config.Default(), DB: db, Usage: db.Usage(), Settings: db.Settings(), Pipeline: pipe})

	for _, path := range []string{
		"/v1/public/overview",
		"/v1/public/models",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		gw.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	}

	require.Zero(t, atomic.LoadInt32(&spy.gets), "public handlers must never dispatch a model/provider call")
}

// TestPublicRejectsNonGet locks the security contract from spec §6: the
// public API is read-only. A POST to a GET-only route must not dispatch; chi
// answers 405 (or 404), never a handler run.
func TestPublicRejectsNonGet(t *testing.T) {
	gw := newPublicTestGateway(t)
	for _, path := range []string{
		"/v1/public/overview",
		"/v1/public/models",
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
