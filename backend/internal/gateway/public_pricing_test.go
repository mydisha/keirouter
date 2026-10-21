package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type pricingResp struct {
	Currency string           `json:"currency"`
	AsOf     *string          `json:"as_of"`
	Models   []map[string]any `json:"models"`
	Unknown  []string         `json:"unknown"`
}

func getPricing(t *testing.T, gw *Server, query string) (*httptest.ResponseRecorder, pricingResp) {
	t.Helper()
	rec := httptest.NewRecorder()
	gw.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/pricing"+query, nil))
	var out pricingResp
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec, out
}

// The public price is the live snapshot's cheapest trusted ask times the
// chain markup, with the ceiling cap as the max; upstream cost and markup are
// never shown.
func TestPublicPricingFromLiveSnapshot(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	markup := 25.0
	offIn, offOut := 1.0, 4.0
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "glm", TenantID: store.DefaultTenantID, Name: "glm-trusted", Strategy: "priority", MarkupPercent: &markup,
		MarketSlugs:       []string{"surplus-trusted:glm-5.3"},
		OfficialInputPerM: &offIn, OfficialOutputPerM: &offOut,
		Steps:     []store.ChainStep{{ID: "s1", Provider: "custom-openai-surplus", Model: "glm-5.3", MarketSlug: "surplus-trusted:glm-5.3"}},
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "gone", TenantID: store.DefaultTenantID, Name: "gone-trusted", Strategy: "priority",
		MarketSlugs: []string{"surplus-trusted:gone"},
		Steps:       []store.ChainStep{{ID: "s2", Provider: "custom-openai-surplus", Model: "gone"}},
		CreatedAt:   now, UpdatedAt: now,
	}))
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "fixed", TenantID: store.DefaultTenantID, Name: "fixed", Strategy: "priority", InputPerM: 2, OutputPerM: 8,
		Steps:     []store.ChainStep{{ID: "s3", Provider: "openai", Model: "gpt-4o"}},
		CreatedAt: now, UpdatedAt: now,
	}))
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 0.2, BestOut: 0.8, CapIn: 0.4, CapOut: 1.6, MinDiscountPct: 50, Providers: []string{"zai"},
	}.Model("glm-5.3")})
	gw.marketCache = cache

	rec, out := getPricing(t, gw, "?model=glm-trusted,chain:fixed,gone-trusted,nope")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, "USD", out.Currency)
	require.NotNil(t, out.AsOf)
	require.Equal(t, []string{"nope"}, out.Unknown)
	require.Len(t, out.Models, 3)

	fixed, glm, gone := out.Models[0], out.Models[1], out.Models[2]
	require.Equal(t, "glm-trusted", glm["model"])
	require.Equal(t, true, glm["available"])
	require.Equal(t, "market", glm["price_source"])
	require.InDelta(t, 0.25, glm["input_per_m"], 1e-9)
	require.InDelta(t, 1.0, glm["output_per_m"], 1e-9)
	require.InDelta(t, 0.5, glm["max_input_per_m"], 1e-9)
	require.InDelta(t, 2.0, glm["max_output_per_m"], 1e-9)
	require.InDelta(t, 75, glm["discount_pct"], 1e-9)
	for _, hidden := range []string{"markup_percent", "upstream_input_per_m", "providers"} {
		require.NotContains(t, glm, hidden)
	}

	require.Equal(t, "gone-trusted", gone["model"])
	require.Equal(t, false, gone["available"])
	require.Nil(t, gone["input_per_m"])

	require.Equal(t, "fixed", fixed["model"])
	require.Equal(t, "fixed", fixed["price_source"])
	require.InDelta(t, 2.0, fixed["input_per_m"], 1e-9)
	require.Nil(t, fixed["max_input_per_m"])

	// The snapshot moves: the next call reflects it with no extra caching.
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 0.1, BestOut: 0.4, CapIn: 0.4, CapOut: 1.6, MinDiscountPct: 50, Providers: []string{"zai"},
	}.Model("glm-5.3")})
	_, out = getPricing(t, gw, "?model=glm-trusted")
	require.InDelta(t, 0.125, out.Models[0]["input_per_m"], 1e-9)

	// No filter lists every chain.
	_, out = getPricing(t, gw, "")
	require.Len(t, out.Models, 3)
	require.Empty(t, out.Unknown)
}

func TestPublicPricingRejectsUnknownAndOversizedFilters(t *testing.T) {
	_, gw := newPublicTestGatewayWithDB(t)
	rec, _ := getPricing(t, gw, "?model=nope")
	require.Equal(t, http.StatusNotFound, rec.Code)

	q := "?model="
	for i := 0; i < 51; i++ {
		q += "m,"
	}
	rec, _ = getPricing(t, gw, q)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
