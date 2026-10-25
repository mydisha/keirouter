package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestAdminChainDisableToggle(t *testing.T) {
	s, db, cookie := newMarketPricingTestServer(t)
	s.chains = db.Chains()

	rec := marketRequest(t, s, cookie, http.MethodPost, "/api/chains",
		`{"name":"dis","steps":[{"provider":"openai","model":"gpt-4o"}]}`)
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
		return chainEntryByName(t, list.Chains, "dis")
	}
	require.Equal(t, false, entry()["disabled"])
	require.Equal(t, false, entry()["sold_out"])

	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"disabled":true}`).Code)
	require.Equal(t, true, entry()["disabled"])
	require.Equal(t, true, entry()["sold_out"])

	// An unrelated edit keeps the chain disabled and its steps intact.
	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"strategy":"priority"}`).Code)
	require.Equal(t, true, entry()["disabled"])
	require.Len(t, entry()["steps"], 1)

	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"disabled":false}`).Code)
	require.Equal(t, false, entry()["disabled"])
	require.Equal(t, false, entry()["sold_out"])
}

func TestDisabledChainIsSoldOutEverywhere(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "c-off", TenantID: store.DefaultTenantID, Name: "off-model",
		Strategy: "priority", InputPerM: 1, OutputPerM: 2, Disabled: true,
		Steps:     []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	models := getPublicModels(t, gw)
	require.Len(t, models, 1)
	require.True(t, models[0].SoldOut)
	require.Zero(t, models[0].InputPerM, "a disabled chain shows no price")

	for _, name := range []string{"off-model", "chain:off-model"} {
		_, err := resolveTargetsWith(ctx, db.Chains(), nil, nil, nil, 0, store.DefaultTenantID, name)
		var bad badModelError
		require.ErrorAs(t, err, &bad, name)
		require.Equal(t, http.StatusServiceUnavailable, bad.HTTPStatus())
		require.Contains(t, bad.Error(), "sold out")
	}
}

func TestChainUpdateRefreshesPublicCatalog(t *testing.T) {
	s, db, cookie := newMarketPricingTestServer(t)
	s.chains = db.Chains()
	s.usage = db.Usage()
	rec := marketRequest(t, s, cookie, http.MethodPost, "/api/chains",
		`{"name":"fresh","steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	require.False(t, getPublicModels(t, s)[0].SoldOut) // primes the cache
	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"disabled":true}`).Code)
	require.True(t, getPublicModels(t, s)[0].SoldOut, "catalog must not serve the cached pre-disable body")
}
