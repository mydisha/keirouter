package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newMCPServer(id, slug string) MCPServer {
	now := time.Now().UTC().Truncate(time.Second)
	return MCPServer{
		ID: id, TenantID: DefaultTenantID, Slug: slug, Name: "Server " + slug, URL: "https://" + slug + ".example/mcp",
		AuthType: "bearer", ForwardHeaders: []string{"X-Team"}, ToolMode: MCPModeAll, TimeoutMS: 60000,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
}

func TestMCPRepo_ServerCRUD(t *testing.T) {
	ctx := context.Background()
	repo := newTestDB(t).MCP()

	srv := newMCPServer("s1", "github")
	require.NoError(t, repo.CreateServer(ctx, srv))
	require.ErrorIs(t, repo.CreateServer(ctx, newMCPServer("s2", "github")), ErrConflict)

	got, err := repo.GetServer(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "github", got.Slug)
	require.Equal(t, []string{"X-Team"}, got.ForwardHeaders)
	require.Equal(t, MCPHealthUnknown, got.HealthStatus)
	require.Nil(t, got.HealthCheckedAt)

	got.Name = "GitHub"
	got.ForwardHeaders = nil
	got.Enabled = false
	require.NoError(t, repo.UpdateServer(ctx, got))
	got, _ = repo.GetServer(ctx, "s1")
	require.Equal(t, "GitHub", got.Name)
	require.Empty(t, got.ForwardHeaders)
	require.False(t, got.Enabled)

	require.NoError(t, repo.SetServerHealth(ctx, "s1", MCPHealthHealthy, "", "2026-07-28", time.Now()))
	require.NoError(t, repo.SetServerHealth(ctx, "s1", MCPHealthFailing, "boom", "", time.Now()))
	got, _ = repo.GetServer(ctx, "s1")
	require.Equal(t, MCPHealthFailing, got.HealthStatus)
	require.Equal(t, "2026-07-28", got.ProtocolVersion, "an empty version keeps the last negotiated one")

	list, err := repo.ListServers(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.ErrorIs(t, repo.UpdateServer(ctx, newMCPServer("missing", "x")), ErrNotFound)
	require.ErrorIs(t, repo.DeleteServer(ctx, "missing"), ErrNotFound)
	_, err = repo.GetServer(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestMCPRepo_ReplaceTools(t *testing.T) {
	ctx := context.Background()
	repo := newTestDB(t).MCP()
	require.NoError(t, repo.CreateServer(ctx, newMCPServer("s1", "a")))

	t1 := time.Now()
	require.NoError(t, repo.ReplaceTools(ctx, "s1", []MCPSyncedTool{
		{Name: "list", Definition: `{"name":"list"}`, DefinitionHash: "h1"},
		{Name: "delete", Definition: `{"name":"delete"}`, DefinitionHash: "h2"},
		{Name: "list", Definition: `{"name":"list"}`, DefinitionHash: "h1"},
	}, t1))
	tools, err := repo.ListTools(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, tools, 2, "a duplicate name from upstream is stored once")
	for _, tl := range tools {
		require.True(t, tl.Enabled, "tool_mode all enables new tools")
		require.Nil(t, tl.ChangedAt, "first sync is not a change")
	}

	require.NoError(t, repo.SetToolSelection(ctx, "s1", MCPModeSelected, []string{"list"}))

	require.NoError(t, repo.ReplaceTools(ctx, "s1", []MCPSyncedTool{
		{Name: "list", Definition: `{"name":"list","description":"new"}`, DefinitionHash: "h1b"},
		{Name: "create", Definition: `{"name":"create"}`, DefinitionHash: "h3"},
	}, time.Now()))
	tools, _ = repo.ListTools(ctx, "s1")
	require.Len(t, tools, 2, "tools removed upstream are deleted")
	byName := map[string]MCPServerTool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	require.True(t, byName["list"].Enabled, "an existing tool keeps its flag")
	require.NotNil(t, byName["list"].ChangedAt, "a changed definition is flagged")
	require.False(t, byName["create"].Enabled, "selected mode does not expose new tools")

	require.NoError(t, repo.SetToolSelection(ctx, "s1", MCPModeSelected, []string{"list", "create"}))
	tools, _ = repo.ListTools(ctx, "s1")
	for _, tl := range tools {
		require.Nil(t, tl.ChangedAt, "saving the selection clears change flags")
		require.True(t, tl.Enabled)
	}

	require.ErrorIs(t, repo.SetToolSelection(ctx, "s1", MCPModeSelected, []string{"nope"}), ErrInvalid)
	require.Error(t, repo.SetToolSelection(ctx, "s1", "some", nil))
	require.ErrorIs(t, repo.SetToolSelection(ctx, "missing", MCPModeAll, nil), ErrNotFound)
	require.ErrorIs(t, repo.ReplaceTools(ctx, "missing", nil, time.Now()), ErrNotFound)

	srv, _ := repo.GetServer(ctx, "s1")
	require.NotNil(t, srv.ToolsSyncedAt)
}

func TestMCPRepo_DeleteCascadesAndKeysDefaultToAll(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := db.MCP()
	require.NoError(t, db.APIKeys().Create(ctx, APIKey{ID: "k1", TenantID: DefaultTenantID, Name: "old key", KeyHash: "x", LookupHash: "l1", Display: "kr_...", CreatedAt: time.Now()}))
	require.NoError(t, db.APIKeys().Create(ctx, APIKey{ID: "k2", TenantID: DefaultTenantID, Name: "playground", KeyHash: "x", LookupHash: "l2", Display: "kr_...", Kind: KeyKindPlayground, CreatedAt: time.Now()}))

	n, err := repo.CountKeysWithMCPMode(ctx, DefaultTenantID, MCPModeAll)
	require.NoError(t, err)
	require.Equal(t, 1, n, "existing operator keys default to all; managed keys are not counted")

	require.NoError(t, repo.CreateServer(ctx, newMCPServer("s1", "a")))
	require.NoError(t, repo.ReplaceTools(ctx, "s1", []MCPSyncedTool{{Name: "t", Definition: "{}", DefinitionHash: "h"}}, time.Now()))
	_, err = db.SQL().ExecContext(ctx, db.rebind(`INSERT INTO api_key_mcp_access (api_key_id, server_id, tool_mode, tools) VALUES (?, ?, 'all', '[]')`), "k1", "s1")
	require.NoError(t, err)

	require.NoError(t, repo.DeleteServer(ctx, "s1"))
	var left int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_server_tools`).Scan(&left))
	require.Zero(t, left)
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_key_mcp_access`).Scan(&left))
	require.Zero(t, left)
}
