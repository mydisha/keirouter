package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// newTopupTestServer wires a Server with the store-backed repos the top-up
// handlers use, following the &Server{...} pattern in admin_bulk_test.go.
func newTopupTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	return &Server{
		db:       db,
		identity: identity.New(db.APIKeys()),
		budgets:  db.Budgets(),
		usage:    db.Usage(),
		log:      slog.Default(),
	}
}

func TestAdminTopupKey_RaisesLimitAndRecordsHistory(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()

	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "topup-key")
	require.NoError(t, err)
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: "b1", TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: key.Record.ID,
		LimitMicros: 1_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	r := httptest.NewRequest(http.MethodPost, "/keys/"+key.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":2.5,"reason":"invoice #1","idempotency_key":"idem-1"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", key.Record.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Topup  map[string]any `json:"topup"`
		Budget map[string]any `json:"budget"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 2.5, resp.Topup["amount_usd"])
	require.Equal(t, 1.0, resp.Topup["limit_before_usd"])
	require.Equal(t, 3.5, resp.Topup["limit_after_usd"])
	require.Equal(t, "b1", resp.Budget["id"])
	require.EqualValues(t, 3_500_000, resp.Budget["limit_micros"])
	require.Equal(t, "total", resp.Budget["period"])
	require.Equal(t, true, resp.Budget["hard_cutoff"])

	b, err := s.budgets.Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(3_500_000), b.LimitMicros)

	// Replay with the same idempotency key must not credit again.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/keys/"+key.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":2.5,"idempotency_key":"idem-1"}`))
	r2 = r2.WithContext(context.WithValue(r2.Context(), chi.RouteCtxKey, rctx))
	s.adminTopupKey(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	var resp2 struct {
		Budget map[string]any `json:"budget"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.EqualValues(t, 3_500_000, resp2.Budget["limit_micros"])
	b2, err := s.budgets.Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(3_500_000), b2.LimitMicros)
}

func TestAdminTopupKey_RejectsInvalidAmounts(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "bad-amount")
	require.NoError(t, err)

	for _, body := range []string{
		`{"amount_usd":0}`,
		`{"amount_usd":-5}`,
		`{"amount_usd":1.0000001}`,
		`{"amount_usd":2000000}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/keys/"+issued.Record.ID+"/topup", strings.NewReader(body))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", issued.Record.ID)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		s.adminTopupKey(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
	}
}

func TestAdminTopupKey_CreatesBudgetWhenMissing(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "no-budget")
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, "/keys/"+issued.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":7,"idempotency_key":"idem-x"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", issued.Record.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	bs, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, issued.Record.ID)
	require.NoError(t, err)
	require.Len(t, bs, 1)
	require.Equal(t, int64(7_000_000), bs[0].LimitMicros)
	require.Equal(t, "total", bs[0].Period)
	require.True(t, bs[0].HardCutoff)
}

func TestAdminTopupKey_RejectsOverflow(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()
	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "overflow-key")
	require.NoError(t, err)
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: "bof", TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: key.Record.ID,
		LimitMicros: math.MaxInt64 - 1, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	r := httptest.NewRequest(http.MethodPost, "/keys/"+key.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":2.5,"idempotency_key":"idem-of"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", key.Record.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	b, err := s.budgets.Get(ctx, "bof")
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64-1), b.LimitMicros)
}

func TestUSDToMicros(t *testing.T) {
	cases := map[string]int64{"0.000001": 1, "1": 1_000_000, "2.5": 2_500_000, "1000000": 1_000_000_000_000}
	for in, want := range cases {
		got, err := usdToMicros(json.Number(in))
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"0", "-1", "1.0000001", "abc", "2000000"} {
		_, err := usdToMicros(json.Number(bad))
		require.Error(t, err, bad)
	}
}
