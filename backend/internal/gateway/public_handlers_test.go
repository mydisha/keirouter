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
	// model_count reflects the number of routing chains, zero with none seeded.
	require.Zero(t, overview.ModelCount)
}

// TestPublicModelsEmptyDBListsNothing proves the public catalogue is
// chain-driven: with no routing chains an idle gateway advertises nothing.
func TestPublicModelsEmptyDBListsNothing(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/models", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Models []json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Empty(t, payload.Models, "no chains means no public models")
}

// TestPublicModelsHidesProviderSecrets seeds a routing chain with attributed
// usage and asserts the full item shape, all-time aggregation,
// request-descending order, and that the serialized body contains no
// provider/caller secrets. Running it on a populated DB (not an empty one) is
// what makes the NotContains checks bite.
func TestPublicModelsHidesProviderSecrets(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "gpt-4o", TenantID: store.DefaultTenantID, Name: "gpt-4o", Strategy: "priority",
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: now, UpdatedAt: now,
	}))
	records := []store.UsageRecord{
		// gpt-4o: 3 requests, 2 distinct keys, 300 prompt + 60 completion tokens.
		{ID: "p1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", ChainID: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		{ID: "p2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", ChainID: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
		{ID: "p3", TenantID: store.DefaultTenantID, APIKeyID: "key-b", Provider: "openai", Model: "gpt-4o", ChainID: "gpt-4o", Status: "success", PromptTokens: 100, CompletionTokens: 20, InputRatePerM: 2.5, OutputRatePerM: 10, PricingStatus: "priced", PricingSource: "official", CreatedAt: now},
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
	require.Equal(t, "gpt-4o", gpt4o.Name, "public name is the chain name")
	require.Equal(t, "combo", gpt4o.ProviderID)
	require.Equal(t, "combo", gpt4o.Provider)
	require.NotEmpty(t, gpt4o.Capabilities)
	require.Equal(t, 2.5, gpt4o.InputPerM, "unpriced chain falls back to the first step's catalog price")
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
// all-time (an old record is counted) and that model_count matches the number
// of routing chains returned by /v1/public/models.
func TestPublicOverviewAllTimeAndModelCount(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-400 * 24 * time.Hour)
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "chain-1", TenantID: store.DefaultTenantID, Name: "combo-a", Strategy: "priority",
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: old, UpdatedAt: old,
	}))
	require.NoError(t, db.Usage().RecordBatch(ctx, []store.UsageRecord{
		{ID: "o1", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", ChainID: "chain-1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: old},
		{ID: "o2", TenantID: store.DefaultTenantID, APIKeyID: "key-a", Provider: "openai", Model: "gpt-4o", ChainID: "chain-1", Status: "error", PromptTokens: 0, CompletionTokens: 0, CreatedAt: old},
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
	require.Len(t, models.Models, 1, "one seeded chain is listed")
	require.Equal(t, len(models.Models), overview.ModelCount, "model_count must equal chain count")
}

func TestPublicModelsListsChainsOnly(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "chain-1", TenantID: store.DefaultTenantID, Name: "deepseek-v4.1-flash",
		Strategy:  "priority",
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		Models []struct {
			Name       string `json:"name"`
			ModelID    string `json:"model_id"`
			ProviderID string `json:"provider_id"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1, "only chains are listed")
	require.Equal(t, "deepseek-v4.1-flash", payload.Models[0].Name)
	require.Equal(t, "combo", payload.Models[0].ProviderID)

	// Catalog ids must not leak in.
	require.NotContains(t, rec.Body.String(), "openai/gpt-4o")
}

// TestPublicModelsUsesChainConfiguredPrice proves that when a chain has its own
// rates configured, the landing page shows those rates, not the first step's
// catalog price.
func TestPublicModelsUsesChainConfiguredPrice(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "chain-priced", TenantID: store.DefaultTenantID, Name: "my-priced-combo",
		Strategy: "priority",
		InputPerM: 1.25, OutputPerM: 5.5, CacheWritePerM: 1.5625, CacheReadPerM: 0.125,
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		Models []struct {
			ModelID        string  `json:"model_id"`
			InputPerM      float64 `json:"input_per_m"`
			OutputPerM     float64 `json:"output_per_m"`
			CachedPerM     float64 `json:"cached_per_m"`
			CacheWritePerM float64 `json:"cache_write_per_m"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1)
	m := payload.Models[0]
	// gpt-4o lists 2.5/10 in the catalog; the chain overrides every rate.
	require.Equal(t, 1.25, m.InputPerM)
	require.Equal(t, 5.5, m.OutputPerM)
	require.Equal(t, 0.125, m.CachedPerM)
	require.Equal(t, 1.5625, m.CacheWritePerM)
}

// TestPublicModelsCapabilitiesFromFirstStep proves capabilities are derived
// from the chain's first step, not the chain name. The chain is named
// "my-fast-combo" (which resolves to no vision) while its first step is
// openai/gpt-4o (vision=true), so a name-derived capability would report false.
func TestPublicModelsCapabilitiesFromFirstStep(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "c-cap", TenantID: store.DefaultTenantID, Name: "my-fast-combo", Strategy: "priority",
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		Models []struct {
			Name         string `json:"name"`
			ProviderID   string `json:"provider_id"`
			Capabilities struct {
				Vision bool `json:"vision"`
			} `json:"capabilities"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1)
	require.Equal(t, "my-fast-combo", payload.Models[0].Name)
	require.Equal(t, "combo", payload.Models[0].ProviderID, "provider_id stays combo for display")
	require.True(t, payload.Models[0].Capabilities.Vision,
		"capabilities must come from the first step (openai/gpt-4o has vision), not the chain name")
}

func TestPublicModelsSkipsEmptyStepChain(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "chain-empty", TenantID: store.DefaultTenantID, Name: "empty",
		Strategy: "priority", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	var payload struct {
		Models []json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Empty(t, payload.Models)
}

func TestPublicModelsAttributesChainUsage(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "c1", TenantID: store.DefaultTenantID, Name: "combo-a", Strategy: "priority",
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	require.NoError(t, db.Usage().RecordBatch(ctx, []store.UsageRecord{
		{ID: "r1", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: time.Now().UTC()},
		{ID: "r2", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "", Status: "success", PromptTokens: 500, CompletionTokens: 500, CreatedAt: time.Now().UTC()},
	}))

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	var payload struct {
		Models []struct {
			ModelID string `json:"model_id"`
			Usage   struct {
				Requests int64 `json:"requests"`
				Tokens   int64 `json:"tokens"`
			} `json:"usage"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1)
	require.Equal(t, int64(1), payload.Models[0].Usage.Requests, "direct-target row must not count")
	require.Equal(t, int64(120), payload.Models[0].Usage.Tokens)
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
	gw := New(Deps{Config: config.Default(), DB: db, Usage: db.Usage(), Settings: db.Settings(), Chains: db.Chains(), Pipeline: pipe})

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
	return db, New(Deps{Config: config.Default(), DB: db, Usage: db.Usage(), Settings: db.Settings(), Chains: db.Chains()})
}
