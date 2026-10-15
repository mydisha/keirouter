package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newAuditDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func auditRows(t *testing.T, db *store.DB, rec *audit.Recorder) []store.AuditEntry {
	t.Helper()
	rec.Flush(context.Background())
	rows, err := db.Audit().List(context.Background(), store.DefaultTenantID, store.AuditFilter{})
	require.NoError(t, err)
	return rows
}

func TestAuditAdminMiddleware(t *testing.T) {
	db := newAuditDB(t)
	rec := audit.New(db.Audit(), nil, audit.Config{DefaultTenantID: store.DefaultTenantID})
	t.Cleanup(func() { rec.Stop(time.Second) })
	s := &Server{audit: rec}

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(s.auditAdmin)
		r.Post("/keys", func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			require.JSONEq(t, `{"name":"ci","secret":"sk-live-123"}`, string(body), "handler still sees the body")
			writeJSON(w, http.StatusCreated, map[string]any{"key": map[string]any{"id": "key_new"}, "secret": "sk-x"})
		})
		r.Delete("/keys/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		r.Get("/keys", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		r.Get("/settings/sqlite/backup", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("db")) })
	})

	do := func(method, path, body string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "tok"})
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	do(http.MethodPost, "/api/keys", `{"name":"ci","secret":"sk-live-123"}`)
	do(http.MethodDelete, "/api/keys/key_9", "")
	do(http.MethodGet, "/api/keys", "")
	do(http.MethodGet, "/api/settings/sqlite/backup", "")

	rows := auditRows(t, db, rec)
	require.Len(t, rows, 3, "plain reads are not audited")
	byAction := map[string]store.AuditEntry{}
	for _, e := range rows {
		byAction[e.Action] = e
	}

	create := byAction["keys.create"]
	require.Equal(t, "key_new", create.Target, "id read from the response")
	require.Equal(t, "keys", create.ResourceType)
	require.Equal(t, audit.OutcomeSuccess, create.Outcome)
	require.Equal(t, 201, create.StatusCode)
	require.Equal(t, "/api/keys", create.HTTPRoute)
	require.Equal(t, sessionActor("tok"), create.Actor)
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", create.TraceID)
	require.JSONEq(t, `{"fields":"name,secret"}`, create.Detail)
	require.NotContains(t, create.Detail, "sk-live", "values never reach the log")

	del := byAction["keys.delete"]
	require.Equal(t, "key_9", del.Target)
	require.Equal(t, "/api/keys/{id}", del.HTTPRoute)
	require.Equal(t, audit.OutcomeFailure, del.Outcome)
	require.Equal(t, audit.SeverityWarn, del.Severity)

	require.Contains(t, byAction, "settings.sqlite.backup.read")
}

func TestAuditLoginAndAPI(t *testing.T) {
	db := newAuditDB(t)
	ctx := context.Background()
	cfg := config.Default()
	authSvc := auth.New(db.Settings(), cfg.Security.JWTSecret, cfg.Security.SessionTTL)
	_, err := authSvc.EnsureDefaults(ctx)
	require.NoError(t, err)
	rec := audit.New(db.Audit(), nil, audit.Config{DefaultTenantID: store.DefaultTenantID, Key: []byte("k")})
	t.Cleanup(func() { rec.Stop(time.Second) })
	gw := New(Deps{Config: cfg, Auth: authSvc, DB: db, Settings: db.Settings(), Audit: rec})
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	// A stale cookie from another instance must not be blamed for the attempt.
	bad, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", strings.NewReader(`{"password":"wrong"}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: sessionCookie, Value: "stale"})
	resp, err := http.DefaultClient.Do(bad)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp, err = http.Post(srv.URL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"`+auth.DefaultPassword+`"}`))
	require.NoError(t, err)
	resp.Body.Close()
	session := findCookie(resp.Cookies(), sessionCookie)
	require.NotNil(t, session)

	get := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.AddCookie(session)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	rec.Flush(ctx)
	resp = get("/api/audit?action=auth.*")
	var list struct {
		Entries []auditEntryJSON `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	resp.Body.Close()
	require.Len(t, list.Entries, 2)
	require.Equal(t, audit.OutcomeSuccess, list.Entries[0].Outcome)
	require.Equal(t, sessionActor(session.Value), list.Entries[0].ActorID)
	require.Equal(t, audit.OutcomeDenied, list.Entries[1].Outcome)
	require.Empty(t, list.Entries[1].ActorID)

	resp = get("/api/audit/verify")
	var rep audit.VerifyReport
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rep))
	resp.Body.Close()
	require.True(t, rep.OK)
	require.Equal(t, 2, rep.Entries)

	resp = get("/api/audit/export")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))
	require.Len(t, strings.Split(strings.TrimSpace(string(body)), "\n"), 2)

	// The export itself was audited.
	rows := auditRows(t, db, rec)
	require.Equal(t, "audit.export.read", rows[0].Action)

	resp = get("/api/audit?cursor=bad")
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRequestFieldNamesSkipsLargeAndNonJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.Empty(t, requestFieldNames(req))

	big := `{"blob":"` + strings.Repeat("x", auditBodyLimit) + `"}`
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	require.Empty(t, requestFieldNames(req))
	body, _ := io.ReadAll(req.Body)
	require.Equal(t, big, string(body))
}
