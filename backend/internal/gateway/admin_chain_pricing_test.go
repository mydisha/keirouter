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
)

func newChainPricingTestServer(t *testing.T) *Server {
	t.Helper()
	s, db := newCustomProviderTestServer(t)
	s.chains = db.Chains()
	return s
}

func postChain(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/chains", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.adminCreateChain(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec.Code, out
}

func patchChain(t *testing.T, s *Server, id, body string) int {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	req := httptest.NewRequest(http.MethodPatch, "/chains/"+id, strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.adminUpdateChain(rec, req)
	return rec.Code
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

func TestAdminChainPricing_CreateRejectsNegativeInput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _ := postChain(t, s, `{"name":"priced","steps":[{"provider":"openai","model":"gpt-4o","input_per_m":-1}]}`)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestAdminChainPricing_CreateRejectsNegativeOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _ := postChain(t, s, `{"name":"priced","steps":[{"provider":"openai","model":"gpt-4o","output_per_m":-1}]}`)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestAdminChainPricing_CreateAndListRoundTrip(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body := postChain(t, s, `{"name":"priced","steps":[{"provider":"openai","model":"gpt-4o","input_per_m":2.5,"output_per_m":10,"cache_write_per_m":3.125,"cache_read_per_m":0.25}]}`)
	require.Equal(t, http.StatusCreated, code, body)

	chains := listChains(t, s)
	require.Len(t, chains, 1)
	rawSteps, ok := chains[0]["steps"].([]any)
	require.True(t, ok, "steps must be a list")
	require.Len(t, rawSteps, 1)
	st, ok := rawSteps[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 2.5, st["input_per_m"])
	require.Equal(t, 10.0, st["output_per_m"])
	require.Equal(t, 3.125, st["cache_write_per_m"])
	require.Equal(t, 0.25, st["cache_read_per_m"])
}

func TestAdminChainPricing_UpdateRejectsNegative(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body := postChain(t, s, `{"name":"priced","steps":[{"provider":"openai","model":"gpt-4o","input_per_m":1}]}`)
	require.Equal(t, http.StatusCreated, code, body)
	id, ok := body["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, id)

	code = patchChain(t, s, id, `{"steps":[{"provider":"openai","model":"gpt-4o","output_per_m":-1}]}`)
	require.Equal(t, http.StatusBadRequest, code)
}
