package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

func poolRequest(method, id, body string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest(method, "/proxy-pools/"+id, strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func createTestPool(t *testing.T, s *Server, p store.ProxyPool) store.ProxyPool {
	t.Helper()
	now := time.Now()
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Type == "" {
		p.Type = "http"
	}
	p.TestStatus, p.CreatedAt, p.UpdatedAt = "unknown", now, now
	require.NoError(t, s.pools.Create(context.Background(), p))
	return p
}

func TestProxyPoolTestNeverTogglesActive(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadProxy := "http://" + ln.Addr().String()
	ln.Close()

	pool := createTestPool(t, s, store.ProxyPool{Name: "down", ProxyURL: deadProxy, Strict: true, IsActive: true})

	rec := httptest.NewRecorder()
	s.adminTestProxyPool(rec, poolRequest(http.MethodPost, pool.ID, ""))
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "error", out["status"])

	got, err := s.pools.Get(context.Background(), pool.ID)
	require.NoError(t, err)
	require.True(t, got.IsActive, "a failed test must not disable the pool (its strict accounts would go direct)")
	require.Equal(t, "error", got.TestStatus)
	require.NotEmpty(t, got.LastError)
	require.NotNil(t, got.LastTested)
}

func TestProxyPoolTestReportsEgressIP(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()

	// A forward proxy that answers the probe itself, as a what-is-my-IP
	// service would when reached through it.
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ip":"203.0.113.7"}`)
	}))
	defer proxySrv.Close()
	orig := proxyProbeTargets
	proxyProbeTargets = []string{"http://probe.invalid/ip"}
	defer func() { proxyProbeTargets = orig }()

	pool := createTestPool(t, s, store.ProxyPool{Name: "up", ProxyURL: proxySrv.URL, IsActive: false})
	rec := httptest.NewRecorder()
	s.adminTestProxyPool(rec, poolRequest(http.MethodPost, pool.ID, ""))
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "active", out["status"])
	require.Equal(t, "203.0.113.7", out["egress_ip"])

	got, _ := s.pools.Get(context.Background(), pool.ID)
	require.False(t, got.IsActive, "a passing test must not re-enable a pool the operator disabled")
}

func TestProxyPoolRecordTestKeepsConcurrentEdits(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()
	pool := createTestPool(t, s, store.ProxyPool{Name: "old", ProxyURL: "http://old.example:8080", IsActive: true})

	// Operator edits the pool while a test against the old copy is running.
	rec := httptest.NewRecorder()
	s.adminUpdateProxyPool(rec, poolRequest(http.MethodPatch, pool.ID, `{"name":"new","proxy_url":"http://new.example:8080"}`))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.NoError(t, s.pools.RecordTest(context.Background(), pool.ID, "error", time.Now(), "boom", nil))

	got, _ := s.pools.Get(context.Background(), pool.ID)
	require.Equal(t, "new", got.Name)
	require.Equal(t, "http://new.example:8080", got.ProxyURL)
	require.Equal(t, "error", got.TestStatus)
}

func TestProxyPoolPasswordRedaction(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()
	pool := createTestPool(t, s, store.ProxyPool{Name: "auth", ProxyURL: "http://alice:s3cret@proxy.example:3128", IsActive: true})

	rec := httptest.NewRecorder()
	s.adminListProxyPools(rec, httptest.NewRequest(http.MethodGet, "/proxy-pools", nil))
	require.NotContains(t, rec.Body.String(), "s3cret")
	require.Contains(t, rec.Body.String(), "alice:xxxxx@proxy.example:3128")

	// Saving the edit form unchanged (masked password) keeps the real one.
	rec = httptest.NewRecorder()
	s.adminUpdateProxyPool(rec, poolRequest(http.MethodPatch, pool.ID, `{"proxy_url":"http://alice:xxxxx@proxy2.example:3128"}`))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	got, _ := s.pools.Get(context.Background(), pool.ID)
	require.Equal(t, "http://alice:s3cret@proxy2.example:3128", got.ProxyURL)

	// A new password is taken as typed.
	rec = httptest.NewRecorder()
	s.adminUpdateProxyPool(rec, poolRequest(http.MethodPatch, pool.ID, `{"proxy_url":"http://alice:n3w@proxy2.example:3128"}`))
	require.Equal(t, http.StatusNoContent, rec.Code)
	got, _ = s.pools.Get(context.Background(), pool.ID)
	require.Equal(t, "http://alice:n3w@proxy2.example:3128", got.ProxyURL)
}

func TestProxyPoolDeleteBlockedWhileBound(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()
	pool := createTestPool(t, s, store.ProxyPool{Name: "bound", ProxyURL: "http://p.example:1", IsActive: true})
	now := time.Now()
	acc := store.Account{
		ID: uuid.NewString(), TenantID: adminTenant, Provider: "openai", Label: "a",
		AuthKind: store.AuthAPIKey, Priority: 100, ProxyPoolID: pool.ID, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.vault.Seal(&acc, vault.NewSecret{APIKey: "k", Metadata: map[string]string{}}))
	require.NoError(t, s.accounts.Create(context.Background(), acc))

	rec := httptest.NewRecorder()
	s.adminDeleteProxyPool(rec, poolRequest(http.MethodDelete, pool.ID, ""))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "bound to 1 account")

	_, err := s.pools.Get(context.Background(), pool.ID)
	require.NoError(t, err)

	rec = httptest.NewRecorder()
	s.adminDeleteProxyPool(rec, poolRequest(http.MethodDelete, "does-not-exist", ""))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAccountEgressContext(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()
	s.proxyNotifier = &ProxyNotifier{}
	s.proxyNotifier.NotifyProxy(true, "http://global.example:3128", "")

	relay := createTestPool(t, s, store.ProxyPool{Name: "r", Type: "cloudflare", ProxyURL: "https://relay.example", Strict: true, IsActive: true})
	off := createTestPool(t, s, store.ProxyPool{Name: "off", ProxyURL: "http://off.example:1", IsActive: false})

	route := func(acc store.Account) core.Credentials {
		c, _ := core.ProxyFromContext(s.accountEgressContext(context.Background(), acc))
		return c
	}
	require.Equal(t, "https://relay.example", route(store.Account{ProxyPoolID: relay.ID}).RelayURL)
	require.True(t, route(store.Account{ProxyPoolID: relay.ID}).StrictProxy)
	require.Equal(t, "http://global.example:3128", route(store.Account{}).ProxyURL)
	require.Equal(t, "http://global.example:3128", route(store.Account{ProxyPoolID: off.ID}).ProxyURL, "disabled pool falls back to the global proxy, like dispatch")

	missing := route(store.Account{ProxyPoolID: "gone"})
	require.Equal(t, unresolvedPoolProxy, missing.ProxyURL, "an unresolvable pool must fail closed")
}
