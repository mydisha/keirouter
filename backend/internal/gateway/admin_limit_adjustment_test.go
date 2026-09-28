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
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// newAdjustTestServer reuses the top-up test harness (store-backed Server).
func newAdjustTestServer(t *testing.T) *Server { return newTopupTestServer(t) }

func adjustReq(keyID, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/keys/"+keyID+"/limit", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", keyID)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestAdminAdjustKeyLimit_ReducesLimitAndRecords(t *testing.T) {
	s := newAdjustTestServer(t)
	ctx := context.Background()
	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "adjust-key")
	require.NoError(t, err)
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: "b1", TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: key.Record.ID,
		LimitMicros: 6_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	w := httptest.NewRecorder()
	s.adminAdjustKeyLimit(w, adjustReq(key.Record.ID, `{"limit_usd":1,"reason":"undo wrong top up","idempotency_key":"adj-1"}`))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Adjustment map[string]any `json:"adjustment"`
		Budget     map[string]any `json:"budget"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, -5.0, resp.Adjustment["delta_usd"])
	require.Equal(t, 6.0, resp.Adjustment["limit_before_usd"])
	require.Equal(t, 1.0, resp.Adjustment["limit_after_usd"])
	require.EqualValues(t, 1_000_000, resp.Budget["limit_micros"])

	b, err := s.budgets.Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000), b.LimitMicros)

	// Replay the same idempotency key: no second adjustment.
	w2 := httptest.NewRecorder()
	s.adminAdjustKeyLimit(w2, adjustReq(key.Record.ID, `{"limit_usd":1,"reason":"undo wrong top up","idempotency_key":"adj-1"}`))
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	adjs, err := s.db.LimitAdjustments().ListByKey(ctx, key.Record.ID)
	require.NoError(t, err)
	require.Len(t, adjs, 1)
}

func TestAdminAdjustKeyLimit_RejectsInvalidInput(t *testing.T) {
	s := newAdjustTestServer(t)
	ctx := context.Background()
	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "adjust-bad")
	require.NoError(t, err)
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: "b1", TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: key.Record.ID,
		LimitMicros: 1_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	for _, body := range []string{
		`{"limit_usd":-1,"reason":"negative"}`,        // negative
		`{"limit_usd":1,"reason":""}`,                 // missing reason
		`{"limit_usd":1.0000001,"reason":"too precise"}`,
		`{"limit_usd":2000000,"reason":"too large"}`,
	} {
		w := httptest.NewRecorder()
		s.adminAdjustKeyLimit(w, adjustReq(key.Record.ID, body))
		require.Equal(t, http.StatusBadRequest, w.Code, body)
	}

	// Unchanged limit is rejected, not recorded.
	w := httptest.NewRecorder()
	s.adminAdjustKeyLimit(w, adjustReq(key.Record.ID, `{"limit_usd":1,"reason":"no-op","idempotency_key":"noop"}`))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	adjs, err := s.db.LimitAdjustments().ListByKey(ctx, key.Record.ID)
	require.NoError(t, err)
	require.Len(t, adjs, 0)

	// Unknown key -> 404.
	w = httptest.NewRecorder()
	s.adminAdjustKeyLimit(w, adjustReq("does-not-exist", `{"limit_usd":1,"reason":"x"}`))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminAdjustKeyLimit_CreatesBudgetWhenMissing(t *testing.T) {
	s := newAdjustTestServer(t)
	ctx := context.Background()
	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "adjust-nobudget")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	s.adminAdjustKeyLimit(w, adjustReq(key.Record.ID, `{"limit_usd":3,"reason":"initial grant","idempotency_key":"adj-new"}`))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	bs, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.Record.ID)
	require.NoError(t, err)
	require.Len(t, bs, 1)
	require.Equal(t, int64(3_000_000), bs[0].LimitMicros)

	adjs, err := s.db.LimitAdjustments().ListByKey(ctx, key.Record.ID)
	require.NoError(t, err)
	require.Len(t, adjs, 1)
	require.Equal(t, int64(3_000_000), adjs[0].DeltaMicros) // 0 -> 3
	require.Equal(t, int64(0), adjs[0].LimitBeforeMicros)
}

func TestAdminListKeyLimitAdjustments(t *testing.T) {
	s := newAdjustTestServer(t)
	ctx := context.Background()
	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "adjust-list")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/keys/"+key.Record.ID+"/limit-adjustments", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", key.Record.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	s.adminListKeyLimitAdjustments(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Adjustments []map[string]any `json:"adjustments"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Adjustments, 0)
}

func TestUSDLimitToMicros(t *testing.T) {
	cases := map[string]int64{"0": 0, "0.000001": 1, "1": 1_000_000, "2.5": 2_500_000, "1000000": 1_000_000_000_000}
	for in, want := range cases {
		got, err := usdLimitToMicros(json.Number(in))
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"-1", "1.0000001", "abc", "2000000"} {
		_, err := usdLimitToMicros(json.Number(bad))
		require.Error(t, err, bad)
	}
}
