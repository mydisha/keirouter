package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		log:      slog.Default(),
		cfg:      config.Default(),
	}
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
