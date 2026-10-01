package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// newPortalTestServer wires a Server with the store, identity, and auth
// services the portal handlers need.
func newPortalTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	authSvc := auth.New(db.Settings(), "", config.Default().Security.SessionTTL)
	_, err = authSvc.EnsureDefaults(ctx)
	require.NoError(t, err)

	return &Server{
		db:       db,
		identity: identity.New(db.APIKeys()),
		auth:     authSvc,
		budgets:  db.Budgets(),
		usage:    db.Usage(),
		settings: db.Settings(),
		log:      slog.Default(),
		cfg:      config.Default(),
	}
}

// withChiParam builds a request whose chi URLParam "sub" is set.
func withChiParam(r *http.Request, sub string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sub", sub)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestPortalClaimRejectsBadKey(t *testing.T) {
	srv := newPortalTestServer(t)
	tok, err := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/portal/auth/claim",
		strings.NewReader(`{"api_key":"not-a-real-key"}`))
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalClaim(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPortalClaimBindsKeyAndRejectsSecondUser(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	issued, err := srv.identity.Create(ctx, store.DefaultTenantID, "", "portal-key")
	require.NoError(t, err)

	claim := func(sub string) *httptest.ResponseRecorder {
		tok, err := srv.auth.IssuePortalSession("portal:"+sub, sub+"@example.com")
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/portal/auth/claim",
			strings.NewReader(`{"api_key":"`+issued.Plaintext+`"}`))
		req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
		rec := httptest.NewRecorder()
		srv.handlePortalClaim(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, claim("sub-1").Code)

	// The same key cannot be claimed by a different Google subject.
	require.Equal(t, http.StatusConflict, claim("sub-2").Code)

	// And a second key from the first user would also conflict via sub upsert:
	// the binding stays on the original key.
	u, err := srv.db.PortalUsers().GetBySub(ctx, "sub-1")
	require.NoError(t, err)
	require.Equal(t, issued.Record.ID, u.KeyID)
}

func TestPortalSessionRejectedByAdminMiddleware(t *testing.T) {
	srv := newPortalTestServer(t)
	tok, err := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.sessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPortalUsageRequiresClaim(t *testing.T) {
	srv := newPortalTestServer(t)
	tok, err := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/usage", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalUsage(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestPortalStatusUnauthenticated(t *testing.T) {
	srv := newPortalTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/portal/auth/status", nil)
	rec := httptest.NewRecorder()
	srv.handlePortalStatus(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, false, body["authenticated"])
}

func TestPortalLoginStartRequiresConfiguredSSO(t *testing.T) {
	srv := newPortalTestServer(t) // portalSSO is nil
	req := httptest.NewRequest(http.MethodGet, "/portal/auth/google/start", nil)
	rec := httptest.NewRecorder()
	srv.handlePortalLoginStart(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// seedPlan creates a plan for portal provisioning tests.
func seedPlan(t *testing.T, srv *Server, id string, limitMicros int64, models string) {
	t.Helper()
	require.NoError(t, srv.db.Plans().Create(context.Background(), store.Plan{
		ID: id, TenantID: store.DefaultTenantID, Name: "Plan " + id,
		LimitMicros: limitMicros, Period: "monthly", AlertPct: 80, HardCutoff: true,
		AllowedModels: models, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
}

func TestPortalCreateKeyDisabledWithoutDefaultPlan(t *testing.T) {
	srv := newPortalTestServer(t)
	tok, err := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalCreateKey(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestPortalCreateKeyProvisionsWithPlanAndBudget(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	seedPlan(t, srv, "free", 5_000_000, "gpt-4o")
	require.NoError(t, srv.settings.Set(ctx, portalDefaultPlanKey, "free"))

	tok, err := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalCreateKey(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body["key"])
	keyID, _ := body["key_id"].(string)
	require.NotEmpty(t, keyID)

	u, err := srv.db.PortalUsers().GetBySub(ctx, "sub-1")
	require.NoError(t, err)
	require.Equal(t, keyID, u.KeyID)
	require.Equal(t, "free", u.PlanID)

	budgets, err := srv.budgets.ListByScope(ctx, store.ScopeAPIKey, keyID)
	require.NoError(t, err)
	require.Len(t, budgets, 1)
	require.EqualValues(t, 5_000_000, budgets[0].LimitMicros)

	// Second call is rejected (idempotent).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/portal/key", nil)
	req2.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	srv.handlePortalCreateKey(rec2, req2)
	require.Equal(t, http.StatusConflict, rec2.Code)
}

func TestPortalStatusReportsHasKeyAndProvisioning(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	seedPlan(t, srv, "free", 0, "")
	require.NoError(t, srv.settings.Set(ctx, portalDefaultPlanKey, "free"))

	tok, err := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/auth/status", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalStatus(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, true, body["authenticated"])
	require.Equal(t, false, body["has_key"])
	require.Equal(t, true, body["provisioning_enabled"])
}

func TestAdminPortalListAndDeleteUser(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	seedPlan(t, srv, "free", 1_000_000, "")
	require.NoError(t, srv.settings.Set(ctx, portalDefaultPlanKey, "free"))

	tok, _ := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	req := httptest.NewRequest(http.MethodPost, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalCreateKey(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	listRec := httptest.NewRecorder()
	srv.adminListPortalUsers(listRec, httptest.NewRequest(http.MethodGet, "/api/portal-users", nil))
	require.Equal(t, http.StatusOK, listRec.Code)
	var listBody struct {
		Users         []map[string]any `json:"users"`
		DefaultPlanID string           `json:"default_plan_id"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listBody))
	require.Len(t, listBody.Users, 1)
	require.Equal(t, "free", listBody.DefaultPlanID)

	delRec := httptest.NewRecorder()
	srv.adminDeletePortalUser(delRec, withChiParam(httptest.NewRequest(http.MethodDelete, "/api/portal-users/sub-1", nil), "sub-1"))
	require.Equal(t, http.StatusNoContent, delRec.Code)
	_, err := srv.db.PortalUsers().GetBySub(ctx, "sub-1")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestPortalKeyRequiresSession(t *testing.T) {
	srv := newPortalTestServer(t)
	rec := httptest.NewRecorder()
	srv.handlePortalKey(rec, httptest.NewRequest(http.MethodGet, "/portal/key", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPortalKeyNotFoundWithoutBinding(t *testing.T) {
	srv := newPortalTestServer(t)
	tok, err := srv.auth.IssuePortalSession("portal:sub-key-1", "k@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalKey(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalKeyReturnsMaskedPreview(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	issued, err := srv.identity.Create(ctx, store.DefaultTenantID, "", "portal-key")
	require.NoError(t, err)
	require.NoError(t, srv.db.PortalUsers().Upsert(ctx, store.PortalUser{
		GoogleSub: "sub-key-2", Email: "k2@example.com", KeyID: issued.Record.ID, PlanID: "free",
	}))

	tok, err := srv.auth.IssuePortalSession("portal:sub-key-2", "k2@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalKey(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, issued.Record.ID, body["key_id"])
	require.Equal(t, issued.Record.Display, body["display"])
	require.NotContains(t, rec.Body.String(), issued.Plaintext, "plaintext must never be returned")
}

func TestAdminSetPortalUserPlanResyncsBudget(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	seedPlan(t, srv, "free", 1_000_000, "gpt-4o-mini")
	seedPlan(t, srv, "pro", 9_000_000, "gpt-4o")
	require.NoError(t, srv.settings.Set(ctx, portalDefaultPlanKey, "free"))

	tok, _ := srv.auth.IssuePortalSession("portal:sub-1", "a@example.com")
	req := httptest.NewRequest(http.MethodPost, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	srv.handlePortalCreateKey(httptest.NewRecorder(), req)

	u, err := srv.db.PortalUsers().GetBySub(ctx, "sub-1")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req = withChiParam(httptest.NewRequest(http.MethodPatch, "/api/portal-users/sub-1/plan", strings.NewReader(`{"plan_id":"pro"}`)), "sub-1")
	srv.adminSetPortalUserPlan(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	budgets, err := srv.budgets.ListByScope(ctx, store.ScopeAPIKey, u.KeyID)
	require.NoError(t, err)
	require.Len(t, budgets, 1)
	require.EqualValues(t, 9_000_000, budgets[0].LimitMicros)
	require.Equal(t, u.KeyID, budgets[0].ScopeID)

	u, _ = srv.db.PortalUsers().GetBySub(ctx, "sub-1")
	require.Equal(t, "pro", u.PlanID)
}
