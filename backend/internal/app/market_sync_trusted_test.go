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
 {"price_input_per_1m":2000,"price_output_per_1m":12000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":false,"healthy":true,"trusted":true},
 {"price_input_per_1m":2495,"price_output_per_1m":14969,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
 {"price_input_per_1m":9000,"price_output_per_1m":54000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
 {"price_input_per_1m":12000,"price_output_per_1m":72000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true}
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
	got, _ := db.Chains().Get(ctx, "c1")
	// Displayed chain rate = cheapest trusted × 1.10 markup.
	if got.InputPerM < 0.0027444 || got.InputPerM > 0.0027446 {
		t.Fatalf("chain input = %v, want 0.0027445", got.InputPerM)
	}
}
