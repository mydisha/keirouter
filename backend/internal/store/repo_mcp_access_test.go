package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMCPRepo_KeyAccess(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := db.MCP()
	require.NoError(t, db.APIKeys().Create(ctx, APIKey{ID: "k1", TenantID: DefaultTenantID, Name: "k", KeyHash: "x", LookupHash: "l1", Display: "d", CreatedAt: time.Now()}))
	require.NoError(t, db.APIKeys().Create(ctx, APIKey{ID: "k2", TenantID: DefaultTenantID, Name: "k2", KeyHash: "x", LookupHash: "l2", Display: "d", CreatedAt: time.Now()}))
	require.NoError(t, repo.CreateServer(ctx, newMCPServer("s1", "a")))
	require.NoError(t, repo.CreateServer(ctx, newMCPServer("s2", "b")))

	a, err := repo.GetKeyAccess(ctx, "k1")
	require.NoError(t, err)
	require.Equal(t, MCPModeAll, a.Mode)
	require.Empty(t, a.Grants)

	require.NoError(t, repo.SetKeyAccess(ctx, "k1", MCPKeyAccess{Mode: MCPModeSelected, Grants: []MCPKeyGrant{
		{ServerID: "s1", ToolMode: MCPModeSelected, Tools: []string{"list", "get"}},
		{ServerID: "s2", ToolMode: MCPModeAll, Tools: []string{"ignored"}},
	}}))
	a, err = repo.GetKeyAccess(ctx, "k1")
	require.NoError(t, err)
	require.Equal(t, MCPModeSelected, a.Mode)
	require.Equal(t, []MCPKeyGrant{
		{ServerID: "s1", ToolMode: MCPModeSelected, Tools: []string{"list", "get"}},
		{ServerID: "s2", ToolMode: MCPModeAll, Tools: []string{}},
	}, a.Grants)

	keys, err := repo.ListKeysForServer(ctx, DefaultTenantID, "s1")
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"k1": {"list", "get"}, "k2": nil}, keys)

	// Switching mode clears the grants.
	require.NoError(t, repo.SetKeyAccess(ctx, "k1", MCPKeyAccess{Mode: MCPModeNone}))
	a, _ = repo.GetKeyAccess(ctx, "k1")
	require.Equal(t, MCPModeNone, a.Mode)
	require.Empty(t, a.Grants)

	require.ErrorIs(t, repo.SetKeyAccess(ctx, "k1", MCPKeyAccess{Mode: "maybe"}), ErrInvalid)
	require.ErrorIs(t, repo.SetKeyAccess(ctx, "missing", MCPKeyAccess{Mode: MCPModeAll}), ErrNotFound)
	_, err = repo.GetKeyAccess(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)
	require.Error(t, repo.SetKeyAccess(ctx, "k1", MCPKeyAccess{Mode: MCPModeSelected, Grants: []MCPKeyGrant{{ServerID: "ghost", ToolMode: MCPModeAll}}}),
		"a grant for a server that does not exist violates the foreign key")
	a, _ = repo.GetKeyAccess(ctx, "k1")
	require.Equal(t, MCPModeNone, a.Mode, "a failed save leaves the previous access in place")
}

func TestMCPRepo_CallLogsAndStats(t *testing.T) {
	ctx := context.Background()
	repo := newTestDB(t).MCP()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	var logs []MCPCallLog
	statuses := []string{"success", "success", "success", "tool_error", "denied", "rate_limited", "timeout", "success", "success", "success"}
	for i, st := range statuses {
		logs = append(logs, MCPCallLog{
			ID: fmt.Sprintf("log-%02d", i), TenantID: DefaultTenantID, APIKeyID: "k1", ServerID: "s1", ServerSlug: "a",
			Tool: map[bool]string{true: "search_docs", false: "fetch_100%"}[i%2 == 0], Status: st, LatencyMS: int64((i + 1) * 100),
			CreatedAt: base.Add(time.Duration(i/2) * time.Minute), // pairs share a timestamp
		})
	}
	logs = append(logs, MCPCallLog{ID: "old", TenantID: DefaultTenantID, ServerID: "s1", Tool: "x", Status: "success", CreatedAt: base.Add(-48 * time.Hour)})
	require.NoError(t, repo.InsertCallLogs(ctx, logs))

	// Pagination walks every row once, newest first, across equal timestamps.
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, next, err := repo.ListCallLogs(ctx, MCPLogFilter{TenantID: DefaultTenantID, Cursor: cursor, Limit: 3})
		require.NoError(t, err)
		for _, l := range page {
			require.False(t, seen[l.ID], "row %s returned twice", l.ID)
			seen[l.ID] = true
		}
		pages++
		if next == "" {
			break
		}
		cursor = next
	}
	require.Len(t, seen, 11)
	require.Equal(t, 4, pages)

	got, _, err := repo.ListCallLogs(ctx, MCPLogFilter{TenantID: DefaultTenantID, Tool: "100%"})
	require.NoError(t, err)
	require.Len(t, got, 5, "LIKE wildcards in the filter are matched literally")
	got, _, _ = repo.ListCallLogs(ctx, MCPLogFilter{TenantID: DefaultTenantID, Status: "denied"})
	require.Len(t, got, 1)
	_, _, err = repo.ListCallLogs(ctx, MCPLogFilter{TenantID: DefaultTenantID, Cursor: "nonsense"})
	require.ErrorIs(t, err, ErrInvalid)

	st, err := repo.CallStats(ctx, DefaultTenantID, base.Add(-time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 10, st.Calls)
	require.EqualValues(t, 6, st.ByStatus["success"])
	require.EqualValues(t, 10, st.ByServer["s1"])
	require.NotNil(t, st.ErrorRate)
	require.InDelta(t, 2.0/8.0, *st.ErrorRate, 0.0001, "denied and rate-limited calls never reached the upstream")
	require.NotNil(t, st.P95MS)
	require.EqualValues(t, 1000, *st.P95MS)

	empty, err := repo.CallStats(ctx, DefaultTenantID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Zero(t, empty.Calls)
	require.Nil(t, empty.P95MS, "no data is not a zero")

	n, err := repo.DeleteCallLogsBefore(ctx, base.Add(-24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func TestMCPRepo_CallStatsLeavesOutGuardrailBlocks(t *testing.T) {
	ctx := context.Background()
	repo := newTestDB(t).MCP()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	var logs []MCPCallLog
	for i, c := range []struct {
		status  string
		latency int64
	}{{"success", 100}, {"tool_error", 200}, {"blocked", 9000}, {"blocked", 8000}} {
		logs = append(logs, MCPCallLog{ID: fmt.Sprintf("b-%d", i), TenantID: DefaultTenantID, ServerID: "s1", ServerSlug: "a",
			Tool: "t", Status: c.status, LatencyMS: c.latency, CreatedAt: base})
	}
	require.NoError(t, repo.InsertCallLogs(ctx, logs))

	st, err := repo.CallStats(ctx, DefaultTenantID, base.Add(-time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 4, st.Calls)
	require.EqualValues(t, 2, st.ByStatus["blocked"])
	require.NotNil(t, st.ErrorRate)
	require.InDelta(t, 0.5, *st.ErrorRate, 0.0001, "a guardrail block is not a server error")
	require.NotNil(t, st.P95MS)
	require.EqualValues(t, 200, *st.P95MS, "blocked calls stay out of the latency percentile")
}
