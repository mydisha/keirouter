package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// newTestDB opens a migrated database: in-memory SQLite, or a fresh Postgres
// schema when KEIROUTER_AUDIT_PG_DSN is set.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}
	if base := os.Getenv("KEIROUTER_AUDIT_PG_DSN"); base != "" {
		schema := fmt.Sprintf("t_%d", time.Now().UnixNano())
		admin, err := sql.Open("pgx", base)
		require.NoError(t, err)
		_, err = admin.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		_ = admin.Close()
		cfg = config.DatabaseConfig{Driver: "postgres", DSN: base + "&search_path=" + schema}
	}
	db, err := store.Open(ctx, cfg, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestSealer(t *testing.T) *crypto.Sealer {
	t.Helper()
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	s, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	return s
}

func newTestManager(t *testing.T) (*Manager, *store.DB) {
	t.Helper()
	db := newTestDB(t)
	m, err := NewManager(context.Background(), db, newTestSealer(t), Options{Info: testInfo})
	require.NoError(t, err)
	return m, db
}

func addServer(t *testing.T, m *Manager, slug, url string, sec Secret, authType string) store.MCPServer {
	t.Helper()
	now := time.Now().UTC()
	srv := store.MCPServer{
		ID: "srv-" + slug, TenantID: store.DefaultTenantID, Slug: slug, Name: slug, URL: url,
		AuthType: authType, ToolMode: store.MCPModeAll, TimeoutMS: 5000, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, SealSecret(m.Sealer, &srv, sec))
	require.NoError(t, m.Repo.CreateServer(context.Background(), srv))
	return srv
}

func TestSyncServer_StoresCatalogAndBuildsRegistry(t *testing.T) {
	allowPrivate(t)
	m, _ := newTestManager(t)
	f := &fakeServer{modern: true, requireAuth: "Bearer tok", tools: []string{toolWeather, toolRegion, toolBadHeader}}
	up := newFake(t, f)
	srv := addServer(t, m, "weather", up.URL, Secret{Secret: "tok"}, AuthBearer)

	res, err := m.Syncer.SyncServer(context.Background(), srv.ID)
	require.NoError(t, err)
	require.Equal(t, VersionModern, res.ProtocolVersion)
	require.Len(t, res.Tools, 3)

	stored, err := m.Repo.GetServer(context.Background(), srv.ID)
	require.NoError(t, err)
	require.Equal(t, store.MCPHealthHealthy, stored.HealthStatus)
	require.Equal(t, VersionModern, stored.ProtocolVersion)
	require.NotNil(t, stored.ToolsSyncedAt)

	tools, err := m.Repo.ListTools(context.Background(), srv.ID)
	require.NoError(t, err)
	require.Len(t, tools, 3)
	for _, tl := range tools {
		if tl.Name == "bad_header" {
			require.Contains(t, tl.InvalidReason, "number")
		} else {
			require.Empty(t, tl.InvalidReason)
		}
	}

	snap := m.Registry.Snapshot()
	entry := snap.BySlug["weather"]
	require.NotNil(t, entry)
	require.Len(t, entry.Sorted, 2, "the tool with an invalid x-mcp-header is hidden")
	require.Equal(t, "get_weather", entry.Sorted[0].Name)
	require.Contains(t, string(entry.Sorted[0].Prefixed), `"name":"weather__get_weather"`)
	require.Contains(t, string(entry.Sorted[0].Bare), `"name":"get_weather"`)
	sql, _ := entry.Tool("run_sql")
	require.Equal(t, []ParamHeader{{Name: "Region", Path: []string{"region"}}}, sql.Params)
}

func TestSyncServer_FailureKeepsCatalogAndMarksFailing(t *testing.T) {
	allowPrivate(t)
	m, _ := newTestManager(t)
	f := &fakeServer{modern: true, requireAuth: "Bearer tok", tools: []string{toolWeather}}
	up := newFake(t, f)
	srv := addServer(t, m, "weather", up.URL, Secret{Secret: "tok"}, AuthBearer)
	_, err := m.Syncer.SyncServer(context.Background(), srv.ID)
	require.NoError(t, err)

	f.requireAuth = "Bearer rotated"
	_, err = m.Syncer.SyncServer(context.Background(), srv.ID)
	require.ErrorContains(t, err, "credentials")

	stored, _ := m.Repo.GetServer(context.Background(), srv.ID)
	require.Equal(t, store.MCPHealthFailing, stored.HealthStatus)
	require.Contains(t, stored.HealthError, "credentials")
	tools, _ := m.Repo.ListTools(context.Background(), srv.ID)
	require.Len(t, tools, 1, "a failed sync keeps the last good catalog")
	require.Len(t, m.Registry.Snapshot().BySlug["weather"].Sorted, 1)
}

func TestSyncServer_LegacyServer(t *testing.T) {
	allowPrivate(t)
	m, _ := newTestManager(t)
	up := newFake(t, &fakeServer{legacy: true, sse: true, tools: []string{toolWeather}})
	srv := addServer(t, m, "old", up.URL, Secret{}, AuthNone)
	res, err := m.Syncer.SyncServer(context.Background(), srv.ID)
	require.NoError(t, err)
	require.Equal(t, VersionLegacyLatest, res.ProtocolVersion)
}

func TestRegistry_SkipsDisabledServersAndTools(t *testing.T) {
	allowPrivate(t)
	m, _ := newTestManager(t)
	up := newFake(t, &fakeServer{modern: true, tools: []string{toolWeather, toolRegion}})
	srv := addServer(t, m, "a", up.URL, Secret{}, AuthNone)
	_, err := m.Syncer.SyncServer(context.Background(), srv.ID)
	require.NoError(t, err)

	require.NoError(t, m.Repo.SetToolSelection(context.Background(), srv.ID, store.MCPModeSelected, []string{"run_sql"}))
	m.Changed(context.Background())
	entry := m.Registry.Snapshot().BySlug["a"]
	require.Len(t, entry.Sorted, 1)
	require.Equal(t, "run_sql", entry.Sorted[0].Name)

	srv.Enabled = false
	srv.UpdatedAt = time.Now()
	require.NoError(t, m.Repo.UpdateServer(context.Background(), srv))
	m.Changed(context.Background())
	require.Nil(t, m.Registry.Snapshot().BySlug["a"])
}

func TestManager_OtherInstanceSeesChanges(t *testing.T) {
	allowPrivate(t)
	db := newTestDB(t)
	sealer := newTestSealer(t)
	a, err := NewManager(context.Background(), db, sealer, Options{Info: testInfo})
	require.NoError(t, err)
	b, err := NewManager(context.Background(), db, sealer, Options{Info: testInfo})
	require.NoError(t, err)

	up := newFake(t, &fakeServer{modern: true, tools: []string{toolWeather}})
	srv := addServer(t, a, "shared", up.URL, Secret{}, AuthNone)
	_, err = a.Syncer.SyncServer(context.Background(), srv.ID)
	require.NoError(t, err)
	require.Nil(t, b.Registry.Snapshot().BySlug["shared"])

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Syncer.interval = 0
	go b.Run(ctx)
	require.Eventually(t, func() bool { return b.Registry.Snapshot().BySlug["shared"] != nil }, 2*watchInterval, 100*time.Millisecond)
}

func TestSecretRoundTripAndTarget(t *testing.T) {
	sealer := newTestSealer(t)
	srv := store.MCPServer{ID: "x", URL: "https://a.example/mcp", AuthType: AuthBasic}
	require.NoError(t, SealSecret(sealer, &srv, Secret{Secret: "pw", Username: "me@example.com", StaticHeaders: map[string]string{"X-Team": "core"}}))
	require.NotContains(t, srv.SecretCipher, "pw")
	sec, err := OpenSecret(sealer, srv)
	require.NoError(t, err)
	tgt := TargetFor(srv, sec)
	require.Equal(t, "Basic bWVAZXhhbXBsZS5jb206cHc=", tgt.Headers.Get("Authorization"))
	require.Equal(t, "core", tgt.Headers.Get("X-Team"))

	srv.AuthType = AuthHeader
	srv.AuthHeader = "x-api-key"
	require.Equal(t, "pw", TargetFor(srv, sec).Headers.Get("X-Api-Key"))

	srv.AuthType = AuthBearer
	require.Empty(t, TargetFor(srv, Secret{}).Headers.Get("Authorization"), "no key, no auth header")

	require.NoError(t, SealSecret(sealer, &srv, Secret{}))
	require.Empty(t, srv.SecretCipher)
}
