package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type pricingResp struct {
	AsOf   *string          `json:"as_of"`
	Models []map[string]any `json:"models"`
}

// pricingFixture is a gateway with three chains, a live Surplus snapshot and
// a key that may call only glm-trusted and fixed.
func pricingFixture(t *testing.T) (*Server, *market.SnapshotCache, string) {
	t.Helper()
	db, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background()))
	require.NoError(t, db.Tenants().EnsureDefault(context.Background()))
	t.Cleanup(func() { _ = db.Close() })
	ids := identity.New(db.APIKeys())
	gw := New(Deps{Config: config.Default(), DB: db, Usage: db.Usage(), Settings: db.Settings(), Chains: db.Chains(), Identity: ids})

	ctx := context.Background()
	now := time.Now().UTC()
	markup := 25.0
	offIn, offOut := 1.0, 4.0
	for _, c := range []store.Chain{
		{ID: "glm", Name: "glm-trusted", MarkupPercent: &markup, OfficialInputPerM: &offIn, OfficialOutputPerM: &offOut,
			MarketSlugs: []string{"surplus-trusted:glm-5.3"},
			Steps:       []store.ChainStep{{ID: "s1", Provider: "custom-openai-surplus", Model: "glm-5.3", MarketSlug: "surplus-trusted:glm-5.3"}}},
		{ID: "luna", Name: "luna-trusted", MarketSlugs: []string{"surplus-trusted:luna"},
			Steps: []store.ChainStep{{ID: "s2", Provider: "custom-openai-surplus", Model: "luna"}}},
		{ID: "fixed", Name: "fixed", InputPerM: 2, OutputPerM: 8,
			Steps: []store.ChainStep{{ID: "s3", Provider: "openai", Model: "gpt-4o"}}},
	} {
		c.TenantID, c.Strategy, c.CreatedAt, c.UpdatedAt = store.DefaultTenantID, "priority", now, now
		require.NoError(t, db.Chains().Create(ctx, c))
	}
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{
		market.TrustedQuote{Found: true, BestIn: 0.2, BestOut: 0.8, CapIn: 0.4, CapOut: 1.6, MinDiscountPct: 50, Providers: []string{"zai"}}.Model("glm-5.3"),
		market.TrustedQuote{Found: true, BestIn: 1, BestOut: 1, CapIn: 1, CapOut: 1, Providers: []string{"x"}}.Model("luna"),
	})
	gw.marketCache = cache

	issued, err := ids.Create(ctx, store.DefaultTenantID, "", "pricing-key")
	require.NoError(t, err)
	require.NoError(t, db.APIKeys().SetAllowedModels(ctx, issued.Record.ID, []string{"glm-trusted", "chain:fixed"}))
	return gw, cache, issued.Plaintext
}

func getPricing(t *testing.T, gw http.Handler, key, query string) (*httptest.ResponseRecorder, pricingResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing"+query, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	var out pricingResp
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec, out
}

// A key sees live Surplus prices (markup included) only for chains it may
// call; chains priced elsewhere and chains it may not call are left out.
func TestPricingSurplusOnlyAndKeyScoped(t *testing.T) {
	gw, cache, key := pricingFixture(t)
	h := gw.routes()

	rec, out := getPricing(t, h, key, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.NotContains(t, rec.Body.String(), `"source"`)
	require.NotContains(t, rec.Body.String(), `"unknown"`)
	require.NotNil(t, out.AsOf)
	require.Len(t, out.Models, 1, "fixed is not Surplus-priced; luna is not allowed for this key")

	glm := out.Models[0]
	require.Equal(t, "glm-trusted", glm["model"])
	require.Equal(t, true, glm["available"])
	require.InDelta(t, 0.25, glm["input_per_m"], 1e-9)
	require.InDelta(t, 1.0, glm["output_per_m"], 1e-9)
	require.InDelta(t, 0.5, glm["max_input_per_m"], 1e-9)
	require.InDelta(t, 2.0, glm["max_output_per_m"], 1e-9)
	require.InDelta(t, 75, glm["discount_pct"], 1e-9)
	for _, hidden := range []string{"markup_percent", "providers", "price_source"} {
		require.NotContains(t, glm, hidden)
	}

	// Models outside the key's Surplus chains are skipped.
	rec, out = getPricing(t, h, key, "?model=chain:GLM-trusted,luna-trusted,fixed")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, out.Models, 1)
	require.Equal(t, "glm-trusted", out.Models[0]["model"], "unlisted names are skipped")
	rec, _ = getPricing(t, h, key, "?model=luna-trusted")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// The live snapshot moves and the price follows once the 2s cache lapses.
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 0.1, BestOut: 0.4, CapIn: 0.4, CapOut: 1.6, MinDiscountPct: 50, Providers: []string{"zai"},
	}.Model("glm-5.3")})
	gw.pricing.mu.Lock()
	gw.pricing.builtAt = time.Time{}
	gw.pricing.mu.Unlock()
	_, out = getPricing(t, h, key, "?model=glm-trusted")
	require.InDelta(t, 0.125, out.Models[0]["input_per_m"], 1e-9)

	// Sold out: no live quote, so no price.
	cache.Replace(nil)
	gw.pricing.mu.Lock()
	gw.pricing.builtAt = time.Time{}
	gw.pricing.mu.Unlock()
	_, out = getPricing(t, h, key, "?model=glm-trusted")
	require.Equal(t, false, out.Models[0]["available"])
	require.Nil(t, out.Models[0]["input_per_m"])
}

func TestPricingRequiresKeyAndLimitsPerKey(t *testing.T) {
	gw, _, key := pricingFixture(t)
	h := gw.routes() // one router, so one limiter across the calls

	rec, _ := getPricing(t, h, "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	q := "?model=" + strings.Repeat("m,", 51)
	rec, _ = getPricing(t, h, key, q)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	for i := 1; i < pricingRequestsPerMinute; i++ {
		rec, _ = getPricing(t, h, key, "")
		require.Equal(t, http.StatusOK, rec.Code, "request %d", i+1)
	}
	rec, _ = getPricing(t, h, key, "")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}
