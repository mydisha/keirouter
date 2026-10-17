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

func TestDiscountPercent(t *testing.T) {
	ref := officialPrice{InputPerM: 0.036, OutputPerM: 0.684}
	require.Equal(t, 97, discountPercent(0.000882, 0.0167685, ref))
	require.Zero(t, discountPercent(1, 1, ref), "pricier than official shows no discount")
	require.Zero(t, discountPercent(0, 0, ref), "unpriced shows no discount")
	require.Zero(t, discountPercent(0.01, 0.1, officialPrice{}))
}

func TestChainOfficialPriceFromModelsDev(t *testing.T) {
	s := &Server{}
	c := store.Chain{
		MarketSlugs: []string{"surplus-trusted:deepseek-v4.1-flash"},
		Steps:       []store.ChainStep{{Provider: "custom-openai-surplus", Model: "deepseek-v4.1-flash"}},
	}
	_, ok := s.chainOfficialPrice(c)
	require.False(t, ok, "no models.dev data, no manual price: no official price")
	require.Zero(t, s.chainContextTokens(c))

	refs := market.NewReferenceCache()
	refs.Replace(map[string]market.Reference{"deepseek-v4-1-flash": {InputPerM: 0.15, OutputPerM: 0.6, ContextTokens: 1000000, Provider: "deepseek"}})
	s.references = refs
	got, ok := s.chainOfficialPrice(c)
	require.True(t, ok)
	require.Equal(t, officialPrice{InputPerM: 0.15, OutputPerM: 0.6, Source: "models.dev", Provider: "deepseek"}, got)
	require.Equal(t, 1000000, s.chainContextTokens(c))

	// Vendor-prefixed inferhub slugs resolve to the same model.
	got, ok = s.chainOfficialPrice(store.Chain{MarketSlugs: []string{"cmc/deepseek/deepseek-v4.1-flash"}})
	require.True(t, ok)
	require.Equal(t, "deepseek", got.Provider)

	in, out := 0.5, 9.0
	c.OfficialInputPerM, c.OfficialOutputPerM = &in, &out
	got, ok = s.chainOfficialPrice(c)
	require.True(t, ok)
	require.Equal(t, officialPrice{InputPerM: 0.5, OutputPerM: 9, Source: "manual"}, got, "manual wins over models.dev")
}

func TestPublicModelsReportsDiscount(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	in, out := 1.0, 10.0
	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "c-disc", TenantID: store.DefaultTenantID, Name: "disc", Strategy: "priority",
		InputPerM: 0.2, OutputPerM: 2, OfficialInputPerM: &in, OfficialOutputPerM: &out,
		Steps:     []store.ChainStep{{Provider: "custom-openai-x", Model: "unknown-model-xyz", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1)
	m := payload.Models[0]
	require.Equal(t, 1.0, m["official_input_per_m"])
	require.Equal(t, 10.0, m["official_output_per_m"])
	require.Equal(t, 80.0, m["discount_pct"])
	require.Nil(t, m["context_tokens"], "unknown context stays null")
}

func TestAdminChainOfficialPrice(t *testing.T) {
	s, db, cookie := newMarketPricingTestServer(t)
	s.chains = db.Chains()

	rec := marketRequest(t, s, cookie, http.MethodPost, "/api/chains",
		`{"name":"op","steps":[{"provider":"openai","model":"gpt-4o"}],"official_input_per_m":2.5,"official_output_per_m":10}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	entry := func() map[string]any {
		var list struct {
			Chains []map[string]any `json:"chains"`
		}
		require.NoError(t, json.Unmarshal(marketRequest(t, s, cookie, http.MethodGet, "/api/chains", "").Body.Bytes(), &list))
		return chainEntryByName(t, list.Chains, "op")
	}
	require.Equal(t, 2.5, entry()["official_input_per_m"])
	require.Equal(t, 10.0, entry()["official_output_per_m"])
	require.Nil(t, entry()["official_auto"], "no models.dev data loaded in this test")

	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"strategy":"priority"}`).Code)
	require.Equal(t, 2.5, entry()["official_input_per_m"], "absent fields stay unchanged")

	require.Equal(t, http.StatusBadRequest, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"official_input_per_m":null}`).Code,
		"one side alone is rejected")
	require.Equal(t, http.StatusBadRequest, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"official_input_per_m":0,"official_output_per_m":1}`).Code)

	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"official_input_per_m":null,"official_output_per_m":null}`).Code)
	require.Nil(t, entry()["official_input_per_m"], "null returns the chain to automatic")
}
