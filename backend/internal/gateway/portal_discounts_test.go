package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestPortalDiscountSettings(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, c := range []store.Chain{
		{ID: "c1", TenantID: store.DefaultTenantID, Name: "gemini-trusted", Strategy: "priority",
			Steps: []store.ChainStep{{ID: "s1", Provider: "custom-openai-surplus", Model: "gemini-3.8-flash", MarketSlug: "surplus-trusted:gemini-3.8-flash"}}},
		{ID: "c2", TenantID: store.DefaultTenantID, Name: "ds-flash-ordered", Strategy: "priority",
			Steps: []store.ChainStep{{ID: "s2", Provider: "custom-openai-inferhub", Model: "ali/deepseek-v4.1-flash", MarketSlug: "ali/deepseek-v4.1-flash"}}},
		{ID: "c3", TenantID: store.DefaultTenantID, Name: "glm-trusted", Strategy: "priority",
			MarketSlugs: []string{"surplus-trusted:glm-5.3-flash"},
			Steps:       []store.ChainStep{{ID: "s3", Provider: "custom-openai-surplus", Model: "glm-5.3-flash"}}},
	} {
		c.CreatedAt, c.UpdatedAt = now, now
		require.NoError(t, srv.db.Chains().Create(ctx, c))
	}

	issued, err := srv.identity.Create(ctx, store.DefaultTenantID, "", "portal-key")
	require.NoError(t, err)
	tok, err := srv.auth.IssuePortalSession("portal:sub-discounts", "d@example.com")
	require.NoError(t, err)
	claimReq := httptest.NewRequest(http.MethodPost, "/portal/auth/claim",
		strings.NewReader(`{"api_key":"`+issued.Plaintext+`"}`))
	claimReq.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	claimRec := httptest.NewRecorder()
	srv.handlePortalClaim(claimRec, claimReq)
	require.Equal(t, http.StatusOK, claimRec.Code)
	require.NoError(t, srv.db.APIKeys().SetMinDiscountPct(ctx, issued.Record.ID, 30))

	call := func(method, model, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/portal/api/discounts/models/"+model, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("model", model)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		switch method {
		case http.MethodPut:
			srv.handlePortalSetModelDiscount(rec, req)
		case http.MethodDelete:
			srv.handlePortalDeleteModelDiscount(rec, req)
		}
		return rec
	}
	type row struct {
		Model        string `json:"model"`
		Supported    bool   `json:"supported"`
		OverridePct  *int   `json:"override_pct"`
		EffectivePct int    `json:"effective_pct"`
	}
	list := func() (int, []row) {
		req := httptest.NewRequest(http.MethodGet, "/portal/api/discounts", nil)
		req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
		rec := httptest.NewRecorder()
		srv.handlePortalDiscounts(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			DefaultPct int   `json:"default_pct"`
			Models     []row `json:"models"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out.DefaultPct, out.Models
	}

	def, rows := list()
	require.Equal(t, 30, def)
	require.Equal(t, []row{
		{Model: "ds-flash-ordered", Supported: false, EffectivePct: 30},
		{Model: "gemini-trusted", Supported: true, EffectivePct: 30},
		{Model: "glm-trusted", Supported: true, EffectivePct: 30},
	}, rows, "sorted by name; trusted steps (per step or chain-level slug) are supported")

	// Warm the auth cache, then override: the next request sees it at once.
	_, err = srv.identity.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
	rec := call(http.MethodPut, "gemini-trusted", `{"min_discount_pct":50}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = call(http.MethodPut, "ds-flash-ordered", `{"min_discount_pct":0}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	authed, err := srv.identity.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
	require.Equal(t, 50, authed.EffectiveMinDiscount("gemini-trusted"))
	require.Equal(t, 0, authed.EffectiveMinDiscount("ds-flash-ordered"))
	require.Equal(t, 30, authed.EffectiveMinDiscount("glm-trusted"))

	_, rows = list()
	require.Equal(t, 50, *rows[1].OverridePct)
	require.Equal(t, 50, rows[1].EffectivePct)
	require.Equal(t, 0, *rows[0].OverridePct)
	require.Nil(t, rows[2].OverridePct)

	// Reset returns the model to the default.
	rec = call(http.MethodDelete, "gemini-trusted", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, rows = list()
	require.Nil(t, rows[1].OverridePct)
	require.Equal(t, 30, rows[1].EffectivePct)

	// Unknown models and bad values are refused without writing anything.
	require.Equal(t, http.StatusNotFound, call(http.MethodPut, "no-such-chain", `{"min_discount_pct":10}`).Code)
	for _, bad := range []string{`{"min_discount_pct":96}`, `{"min_discount_pct":-1}`, `{"min_discount_pct":2.5}`, `nope`} {
		require.Equal(t, http.StatusBadRequest, call(http.MethodPut, "glm-trusted", bad).Code, bad)
	}
	overrides, err := srv.identity.Keys().ModelDiscounts(ctx, issued.Record.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"ds-flash-ordered": 0}, overrides)
}

func TestPortalDiscountSettingsRequiresSession(t *testing.T) {
	srv := newPortalTestServer(t)
	rec := httptest.NewRecorder()
	srv.handlePortalDiscounts(rec, httptest.NewRequest(http.MethodGet, "/portal/api/discounts", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
