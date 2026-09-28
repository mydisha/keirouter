package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newChainPricingTestServer(t *testing.T) *Server {
	t.Helper()
	s, db := newCustomProviderTestServer(t)
	s.chains = db.Chains()
	return s
}

func postChain(t *testing.T, s *Server, body string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/chains", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.adminCreateChain(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec.Code, out, rec.Body.String()
}

func patchChain(t *testing.T, s *Server, id, body string) (int, string) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	req := httptest.NewRequest(http.MethodPatch, "/chains/"+id, strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.adminUpdateChain(rec, req)
	return rec.Code, rec.Body.String()
}

func listChains(t *testing.T, s *Server) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/chains", nil)
	rec := httptest.NewRecorder()
	s.adminListChains(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Chains []map[string]any `json:"chains"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Chains
}

func TestAdminChainPricing_ExportImportRoundTrip(t *testing.T) {
	s := newChainPricingTestServer(t)
	db := s.db
	s.identity = identity.New(db.APIKeys())
	s.budgets = db.Budgets()
	s.pools = db.ProxyPools()
	s.aliases = db.Aliases()
	s.settings = db.Settings()

	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "c1", TenantID: adminTenant, Name: "priced", Strategy: "priority",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		Steps: []store.ChainStep{{ID: "step-1", Provider: "openai", Model: "gpt-4o", Position: 0}},
	}))

	// Export.
	expRec := httptest.NewRecorder()
	s.adminExportDatabase(expRec, httptest.NewRequest(http.MethodGet, "/settings/database", nil))
	require.Equal(t, http.StatusOK, expRec.Code, expRec.Body.String())
	var export map[string]any
	require.NoError(t, json.Unmarshal(expRec.Body.Bytes(), &export))
	rawChains := export["chains"].([]any)
	require.Len(t, rawChains, 1)
	entry := rawChains[0].(map[string]any)
	require.Equal(t, 2.5, entry["input_per_m"])
	require.Equal(t, 10.0, entry["output_per_m"])
	require.Equal(t, 3.125, entry["cache_write_per_m"])
	require.Equal(t, 0.25, entry["cache_read_per_m"])

	// Import into a fresh store and confirm rates survive.
	s2, db2 := newCustomProviderTestServer(t)
	s2.chains = db2.Chains()
	s2.settings = db2.Settings()
	impRec := httptest.NewRecorder()
	s2.adminImportDatabase(impRec, httptest.NewRequest(http.MethodPost, "/settings/database", strings.NewReader(expRec.Body.String())))
	require.Equal(t, http.StatusOK, impRec.Code, impRec.Body.String())

	imported, err := db2.Chains().ListByTenant(context.Background(), adminTenant)
	require.NoError(t, err)
	require.Len(t, imported, 1)
	require.Equal(t, 2.5, imported[0].InputPerM)
	require.Equal(t, 10.0, imported[0].OutputPerM)
	require.Equal(t, 3.125, imported[0].CacheWritePerM)
	require.Equal(t, 0.25, imported[0].CacheReadPerM)
}

func TestAdminChainPricing_CreateRejectsNegativeInput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":-1,"output_per_m":1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "finite and non-negative")
}

func TestAdminChainPricing_CreateRejectsNegativeOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":1,"output_per_m":-1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "finite and non-negative")
}

func TestAdminChainPricing_CreateRejectsZeroInput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":0,"output_per_m":1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "greater than 0")
}

func TestAdminChainPricing_CreateRejectsZeroOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "greater than 0")
}

func TestAdminChainPricing_CreateRejectsPerStepRate(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","steps":[{"provider":"openai","model":"gpt-4o","input_per_m":2.5}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "unknown field")
}

func TestAdminChainPricing_CreateUnpricedChainAllowed(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"free","steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)
}

func TestAdminChainPricing_CreateAndListRoundTrip(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"priced","input_per_m":2.5,"output_per_m":10,"cache_write_per_m":3.125,"cache_read_per_m":0.25,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)

	chains := listChains(t, s)
	require.Len(t, chains, 1)
	require.Equal(t, 2.5, chains[0]["input_per_m"])
	require.Equal(t, 10.0, chains[0]["output_per_m"])
	require.Equal(t, 3.125, chains[0]["cache_write_per_m"])
	require.Equal(t, 0.25, chains[0]["cache_read_per_m"])
}

func TestAdminChainPricing_UpdateRejectsNegative(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"priced","input_per_m":1,"output_per_m":2,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)
	id := body["id"].(string)

	code, respBody := patchChain(t, s, id, `{"input_per_m":1,"output_per_m":-1}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, respBody, "finite and non-negative")
}

func TestAdminChainPricing_UpdateRejectsZeroOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"priced","input_per_m":1,"output_per_m":2,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)
	id := body["id"].(string)

	code, respBody := patchChain(t, s, id, `{"input_per_m":1,"output_per_m":0}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, respBody, "greater than 0")
}
