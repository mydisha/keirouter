package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// trustedSurplusServer serves the best-ask list at / and a per-model book at
// /<model>; it records which books were requested.
func trustedSurplusServer(t *testing.T, books map[string]string, hits *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			_, _ = w.Write([]byte(`{"markets":[]}`))
			return
		}
		*hits = append(*hits, name)
		body, ok := books[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const lunaBook = `{"offers":[
 {"provider_id":"openai","price_input_per_1m":2000,"price_output_per_1m":12000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":false,"healthy":true,"trusted":true},
 {"provider_id":"bedrock","price_input_per_1m":2495,"price_output_per_1m":14969,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
 {"provider_id":"openai","price_input_per_1m":9000,"price_output_per_1m":54000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
 {"provider_id":"openai","price_input_per_1m":12000,"price_output_per_1m":72000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true}
]}`

func TestSyncFetchesTrustedBooksForBoundSlugsOnly(t *testing.T) {
	db := newSyncTestDB(t)
	ctx := context.Background()
	var hits []string
	srv := trustedSurplusServer(t, map[string]string{"gpt-5.6-luna": lunaBook}, &hits)

	c := store.Chain{
		ID: "c1", TenantID: store.DefaultTenantID, Name: "luna-trusted", Strategy: "priority",
		MarketSlugs: []string{"surplus-trusted:gpt-5.6-luna"},
		Steps: []store.ChainStep{{ID: "s1", ChainID: "c1", Position: 0, Provider: "custom-openai-surplus",
			Model: "gpt-5.6-luna", MarketSlug: "surplus-trusted:gpt-5.6-luna"}},
	}
	if err := db.Chains().Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := market.SaveSettings(ctx, db.Settings().Set, market.Settings{
		RefreshIntervalSeconds: 2, MarkupPercent: 10, SurplusFallbackDepth: 3,
	}); err != nil {
		t.Fatal(err)
	}

	a := newSyncTestApp(t, db)
	a.surplusURL = srv.URL
	if _, err := a.syncChainMarketPrices(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(hits) != 1 || hits[0] != "gpt-5.6-luna" {
		t.Fatalf("book hits = %v, want exactly [gpt-5.6-luna]", hits)
	}
	m, ok := a.MarketCache().Lookup("surplus-trusted:gpt-5.6-luna")
	if !ok {
		t.Fatal("trusted slug missing from cache")
	}
	if m.MinAskIn != 0.002495 || m.MinAskOut != 0.014969 {
		t.Fatalf("best = (%v,%v), want the cheapest available trusted offer", m.MinAskIn, m.MinAskOut)
	}
	if m.MinDiscountPct != 94 {
		t.Fatalf("min discount = %d, want 94 (third level)", m.MinDiscountPct)
	}
	if strings.Join(m.Providers, ",") != "bedrock,openai" {
		t.Fatalf("providers = %v, want the available trusted ones", m.Providers)
	}
	got, _ := db.Chains().Get(ctx, "c1")
	// Displayed chain rate = cheapest trusted × 1.10 markup.
	if got.InputPerM < 0.0027444 || got.InputPerM > 0.0027446 {
		t.Fatalf("chain input = %v, want 0.0027445", got.InputPerM)
	}
}

func TestSyncAppliesChainMarkupOverride(t *testing.T) {
	db := newSyncTestDB(t)
	ctx := context.Background()
	var hits []string
	srv := trustedSurplusServer(t, map[string]string{"gpt-5.6-luna": lunaBook}, &hits)
	override, zero := 50.0, 0.0
	for _, c := range []store.Chain{
		{ID: "own", Name: "own", MarkupPercent: &override},
		{ID: "none", Name: "none", MarkupPercent: &zero},
		{ID: "global", Name: "global"},
	} {
		c.TenantID, c.Strategy = store.DefaultTenantID, "priority"
		c.MarketSlugs = []string{"surplus-trusted:gpt-5.6-luna"}
		c.Steps = []store.ChainStep{{ID: c.ID + "-s", ChainID: c.ID, Provider: "custom-openai-surplus", Model: "gpt-5.6-luna"}}
		if err := db.Chains().Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := market.SaveSettings(ctx, db.Settings().Set, market.Settings{RefreshIntervalSeconds: 2, MarkupPercent: 10, SurplusFallbackDepth: 3}); err != nil {
		t.Fatal(err)
	}
	a := newSyncTestApp(t, db)
	a.surplusURL = srv.URL
	if _, err := a.syncChainMarketPrices(ctx); err != nil {
		t.Fatal(err)
	}
	// Cheapest trusted input = 0.002495 $/M.
	for id, want := range map[string]float64{"own": 0.0037425, "none": 0.002495, "global": 0.0027445} {
		c, _ := db.Chains().Get(ctx, id)
		if c.InputPerM < want-1e-12 || c.InputPerM > want+1e-12 {
			t.Fatalf("%s input = %v, want %v", id, c.InputPerM, want)
		}
	}
}

// floorBook: list $1/M in, $4/M out. inferhub is untrusted; bedrock is only 20%
// off (fails min30); google is 40% off; openai is exactly 30% off.
const floorBook = `{"offers":[
 {"provider_id":"inferhub","price_input_per_1m":100000,"price_output_per_1m":400000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":false},
 {"provider_id":"bedrock","price_input_per_1m":800000,"price_output_per_1m":3200000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":true},
 {"provider_id":"google","price_input_per_1m":600000,"price_output_per_1m":2400000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":true},
 {"provider_id":"openai","price_input_per_1m":700000,"price_output_per_1m":2800000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":true}
]}`

func TestSyncTrustedQuoteHonoursProviderMinDiscountFloor(t *testing.T) {
	for _, tc := range []struct {
		baseURL   string
		providers string
		best      float64
	}{
		// No floor: bedrock (20% off) is still a valid trusted ask, but the
		// cheapest is google.
		{"https://api.surplusintelligence.ai/v1", "bedrock,google,openai", 0.6},
		// min30: bedrock is dropped from the allow-list; untrusted inferhub
		// never appears in either case.
		{"https://api.surplusintelligence.ai/min30/v1", "google,openai", 0.6},
	} {
		db := newSyncTestDB(t)
		ctx := context.Background()
		var hits []string
		srv := trustedSurplusServer(t, map[string]string{"gemini-3.8-flash": floorBook}, &hits)
		if err := db.CustomProviders().CreateProvider(ctx, store.CustomProvider{
			ID: "custom-openai-surplus", TenantID: store.DefaultTenantID, DisplayName: "Surplus",
			Alias: "surplus", Dialect: "openai", BaseURL: tc.baseURL,
		}); err != nil {
			t.Fatal(err)
		}
		c := store.Chain{
			ID: "g", TenantID: store.DefaultTenantID, Name: "gemini-trusted", Strategy: "priority",
			Steps: []store.ChainStep{{ID: "g-s", ChainID: "g", Provider: "custom-openai-surplus",
				Model: "gemini-3.8-flash", MarketSlug: "surplus-trusted:gemini-3.8-flash"}},
		}
		if err := db.Chains().Create(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := market.SaveSettings(ctx, db.Settings().Set, market.Settings{RefreshIntervalSeconds: 2, MarkupPercent: 10, SurplusFallbackDepth: 3}); err != nil {
			t.Fatal(err)
		}
		a := newSyncTestApp(t, db)
		a.surplusURL = srv.URL
		if _, err := a.syncChainMarketPrices(ctx); err != nil {
			t.Fatal(err)
		}
		m, ok := a.MarketCache().Lookup("surplus-trusted:gemini-3.8-flash")
		if !ok {
			t.Fatalf("%s: trusted slug missing", tc.baseURL)
		}
		if got := strings.Join(m.Providers, ","); got != tc.providers {
			t.Fatalf("%s: providers = %q, want %q", tc.baseURL, got, tc.providers)
		}
		if m.MinAskIn < tc.best-1e-12 || m.MinAskIn > tc.best+1e-12 {
			t.Fatalf("%s: best in = %v, want %v", tc.baseURL, m.MinAskIn, tc.best)
		}
		if strings.Contains(tc.baseURL, "min30") {
			// The deepest allowed level (openai, exactly 30% off) sets the
			// ceiling: Surplus can never bill more than list × 0.70.
			if m.MinDiscountPct != 30 || m.FailoverIn > 0.7+1e-12 || m.FailoverOut > 2.8+1e-12 {
				t.Fatalf("min30 ceiling = %d cap=(%v,%v), want 30 and ≤ (0.7,2.8)", m.MinDiscountPct, m.FailoverIn, m.FailoverOut)
			}
		}
	}
}
