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

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/consolelog"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/mcp"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// mcpE2E is a full gateway with the MCP endpoint, one API key, and an
// upstream MCP server built with the official SDK.
type mcpE2E struct {
	gw      *Server
	srv     *httptest.Server
	apiKey  string
	keyID   string
	session string
	mgr     *mcp.Manager
	db      *store.DB
}

type echoIn struct {
	Text string `json:"text" jsonschema:"text to echo"`
}

type echoOut struct {
	Echo string `json:"echo"`
}

// sdkUpstream serves two tools. stateless selects the modern era; a
// stateful handler only speaks the legacy initialize era.
func sdkUpstream(t *testing.T, stateless bool) *httptest.Server {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "upstream", Version: "1.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo text back"}, func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, echoOut, error) {
		return nil, echoOut{Echo: in.Text}, nil
	})
	sdk.AddTool(server, &sdk.Tool{Name: "fail", Description: "Always fails"}, func(_ context.Context, _ *sdk.CallToolRequest, _ echoIn) (*sdk.CallToolResult, echoOut, error) {
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "nope"}}}, echoOut{}, nil
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: stateless})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

func newMCPE2E(t *testing.T, upstreamURL string, recorder ...func(*store.DB) *audit.Recorder) *mcpE2E {
	t.Helper()
	ctx := context.Background()
	httputil.SetAllowPrivateBaseURL(true)
	t.Cleanup(func() { httputil.SetAllowPrivateBaseURL(false) })

	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	idSvc := identity.New(db.APIKeys(), crypto.NewKeyLookup(mk))
	issued, err := idSvc.Create(ctx, store.DefaultTenantID, "", "mcp-key")
	require.NoError(t, err)

	var rec *audit.Recorder
	opts := mcp.Options{Info: mcp.Implementation{Name: "KeiRouter", Version: "test"}}
	if len(recorder) > 0 {
		rec = recorder[0](db)
		opts.Auditor = rec
	}
	mgr, err := mcp.NewManager(ctx, db, sealer, opts)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mgr.Run(runCtx)

	authSvc := auth.New(db.Settings(), "mcp-e2e-signing", time.Hour)
	session, err := authSvc.IssueSession()
	require.NoError(t, err)
	cfg := config.Default()
	cfg.Server.CORSOrigins = []string{"https://dash.example"}
	gw := New(Deps{Config: cfg, Auth: authSvc, DB: db, Settings: db.Settings(), Identity: idSvc, MCP: mgr, Audit: rec})
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	h := &mcpE2E{gw: gw, srv: srv, apiKey: issued.Plaintext, keyID: issued.Record.ID, session: session, mgr: mgr, db: db}
	if upstreamURL != "" {
		admin := &mcpAdminHarness{srv: srv, session: session, db: db, mgr: mgr}
		status, body := admin.do(t, http.MethodPost, "/mcp/servers", map[string]any{
			"slug": "up", "name": "Upstream", "url": upstreamURL, "auth_type": "none",
		})
		require.Equal(t, http.StatusCreated, status, body)
		require.Equal(t, "healthy", body["health_status"], body)
	}
	return h
}

type bearer struct {
	key  string
	next http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return b.next.RoundTrip(r)
}

func (h *mcpE2E) sdkClient(t *testing.T, path, version string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "conformance-client", Version: "1"}, nil)
	tr := &sdk.StreamableClientTransport{
		Endpoint:             h.srv.URL + path,
		HTTPClient:           &http.Client{Transport: bearer{key: h.apiKey, next: http.DefaultTransport}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	cs, err := client.Connect(context.Background(), tr, &sdk.ClientSessionOptions{ProtocolVersion: version})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestMCPConformance_SDKClientThroughGateway(t *testing.T) {
	for _, up := range []struct {
		name      string
		stateless bool
	}{{"modern upstream", true}, {"legacy upstream", false}} {
		for _, client := range []struct{ name, version string }{{"modern client", mcp.VersionModern}, {"legacy client", mcp.VersionLegacyLatest}} {
			t.Run(up.name+"/"+client.name, func(t *testing.T) {
				h := newMCPE2E(t, sdkUpstream(t, up.stateless).URL)
				cs := h.sdkClient(t, "/mcp", client.version)
				require.Equal(t, client.version, cs.InitializeResult().ProtocolVersion)

				tools, err := cs.ListTools(context.Background(), nil)
				require.NoError(t, err)
				names := []string{}
				for _, tl := range tools.Tools {
					names = append(names, tl.Name)
				}
				require.Equal(t, []string{"up__echo", "up__fail"}, names)

				res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__echo", Arguments: map[string]any{"text": "halo"}})
				require.NoError(t, err)
				require.False(t, res.IsError)
				raw, _ := json.Marshal(res.StructuredContent)
				require.JSONEq(t, `{"echo":"halo"}`, string(raw))

				res, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__fail", Arguments: map[string]any{"text": "x"}})
				require.NoError(t, err)
				require.True(t, res.IsError)

				_, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__missing", Arguments: map[string]any{}})
				require.Error(t, err)

				scoped := h.sdkClient(t, "/mcp/up", client.version)
				tools, err = scoped.ListTools(context.Background(), nil)
				require.NoError(t, err)
				require.Equal(t, "echo", tools.Tools[0].Name)
			})
		}
	}
}

func rawMCP(t *testing.T, h *mcpE2E, method, path, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestMCPEndpoint_HTTPRules(t *testing.T) {
	h := newMCPE2E(t, "")
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	auth := map[string]string{"Authorization": "Bearer " + h.apiKey}

	res, body := rawMCP(t, h, http.MethodPost, "/mcp", list, nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Contains(t, body, `"jsonrpc":"2.0"`, "auth errors use a JSON-RPC envelope on /mcp")

	res, body = rawMCP(t, h, http.MethodPost, "/mcp", list, map[string]string{"Authorization": "Bearer kr_wrong"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, body)

	res, _ = rawMCP(t, h, http.MethodPost, "/mcp", list, map[string]string{"Authorization": "Bearer " + h.apiKey, "Origin": "https://evil.example"})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	res, _ = rawMCP(t, h, http.MethodPost, "/mcp", list, map[string]string{"Authorization": "Bearer " + h.apiKey, "Origin": "https://dash.example"})
	require.Equal(t, http.StatusOK, res.StatusCode)

	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		for _, p := range []string{"/mcp", "/mcp/any"} {
			res, _ = rawMCP(t, h, m, p, "", auth)
			require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode, m+" "+p)
			require.Equal(t, http.MethodPost, res.Header.Get("Allow"))
		}
	}

	res, body = rawMCP(t, h, http.MethodPost, "/mcp", list, auth)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`, body)
}

func TestMCPAdmin_KeyAccessFlow(t *testing.T) {
	h := newMCPE2E(t, sdkUpstream(t, true).URL)
	admin := &mcpAdminHarness{srv: h.srv, session: h.session, db: h.db, mgr: h.mgr}
	path := "/keys/" + h.keyID + "/mcp-access"

	status, body := admin.do(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "all", body["mode"], "keys start with every server")

	_, servers := admin.do(t, http.MethodGet, "/mcp/servers", nil)
	serverID := servers["servers"].([]any)[0].(map[string]any)["id"].(string)

	bad := []map[string]any{
		{"mode": "everything"},
		{"mode": "selected", "servers": []any{}},
		{"mode": "all", "servers": []any{map[string]any{"server_id": serverID, "tool_mode": "all"}}},
		{"mode": "selected", "servers": []any{map[string]any{"server_id": "ghost", "tool_mode": "all"}}},
		{"mode": "selected", "servers": []any{map[string]any{"server_id": serverID, "tool_mode": "selected", "tools": []string{}}}},
		{"mode": "selected", "servers": []any{map[string]any{"server_id": serverID, "tool_mode": "all"}, map[string]any{"server_id": serverID, "tool_mode": "all"}}},
	}
	for i, b := range bad {
		status, out := admin.do(t, http.MethodPut, path, b)
		require.Equal(t, http.StatusBadRequest, status, "case %d: %v", i, out)
	}

	status, body = admin.do(t, http.MethodPut, path, map[string]any{"mode": "selected", "servers": []any{
		map[string]any{"server_id": serverID, "tool_mode": "selected", "tools": []string{"echo", "echo"}},
	}})
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []any{"echo"}, body["servers"].([]any)[0].(map[string]any)["tools"])

	// The change applies at once: the access cache was invalidated.
	cs := h.sdkClient(t, "/mcp", mcp.VersionModern)
	tools, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	require.Equal(t, "up__echo", tools.Tools[0].Name)

	status, body = admin.do(t, http.MethodGet, "/mcp/servers/"+serverID+"/access", nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, body["keys"], 1)

	status, _ = admin.do(t, http.MethodGet, "/keys/missing/mcp-access", nil)
	require.Equal(t, http.StatusNotFound, status)

	// Tester and logs.
	status, body = admin.do(t, http.MethodPost, "/mcp/servers/"+serverID+"/call", map[string]any{"tool": "echo", "arguments": map[string]any{"text": "t"}})
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "success", body["status"])
	status, _ = admin.do(t, http.MethodPost, "/mcp/servers/"+serverID+"/call", map[string]any{"tool": "echo", "arguments": []int{1}})
	require.Equal(t, http.StatusBadRequest, status)

	_, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__echo", Arguments: map[string]any{"text": "x"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, b := admin.do(t, http.MethodGet, "/mcp/logs?key_id="+h.keyID, nil)
		return len(b["logs"].([]any)) == 1
	}, 3*time.Second, 50*time.Millisecond)
	status, body = admin.do(t, http.MethodGet, "/mcp/stats?period=24h", nil)
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 2, body["stats"].(map[string]any)["calls"])
	status, _ = admin.do(t, http.MethodGet, "/mcp/stats?period=1y", nil)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = admin.do(t, http.MethodGet, "/mcp/logs?cursor=garbage", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestAdminCreateKey_MCPModeNone(t *testing.T) {
	h := newMCPE2E(t, "")
	admin := &mcpAdminHarness{srv: h.srv, session: h.session, db: h.db, mgr: h.mgr}
	status, body := admin.do(t, http.MethodPost, "/keys", map[string]any{"name": "no-tools", "mcp_mode": "none"})
	require.Equal(t, http.StatusCreated, status, body)
	acc, err := h.db.MCP().GetKeyAccess(context.Background(), body["id"].(string))
	require.NoError(t, err)
	require.Equal(t, "none", acc.Mode)

	status, _ = admin.do(t, http.MethodPost, "/keys", map[string]any{"name": "x", "mcp_mode": "selected"})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMCPEndpoint_ConsoleLines(t *testing.T) {
	h := newMCPE2E(t, sdkUpstream(t, true).URL)
	cs := h.sdkClient(t, "/mcp", mcp.VersionModern)
	_, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	_, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__echo", Arguments: map[string]any{"text": "hi"}})
	require.NoError(t, err)
	_, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__fail", Arguments: map[string]any{"text": "x"}})
	require.NoError(t, err)

	var finals []consolelog.Entry
	for _, e := range h.gw.consoleLog.Entries() {
		if e.Final && strings.HasPrefix(e.Endpoint, "POST /mcp") {
			finals = append(finals, e)
		}
	}
	msgs := []string{}
	for _, e := range finals {
		msgs = append(msgs, e.Level+" "+e.Message)
	}
	joined := strings.Join(msgs, "\n")
	require.Contains(t, joined, "DEBUG MCP · tools/list · 2 tools")
	require.Contains(t, joined, "INFO MCP · up__echo · success")
	require.Contains(t, joined, "WARN MCP · up__fail · tool error")
	require.Contains(t, joined, "· nope", "the tool's own error text is on the line")
	for _, e := range finals {
		if e.Model == "up__echo" {
			require.Equal(t, "mcp/up", e.Provider)
			require.Contains(t, e.Detail, "modern 2026-07-28")
			require.Contains(t, e.Detail, "mcp-key")
		}
	}
}

func TestMCPEndpoint_ToolCallsInAuditLog(t *testing.T) {
	var rec *audit.Recorder
	h := newMCPE2E(t, sdkUpstream(t, true).URL, func(db *store.DB) *audit.Recorder {
		rec = audit.New(db.Audit(), nil, audit.Config{DefaultTenantID: store.DefaultTenantID})
		t.Cleanup(func() { rec.Stop(time.Second) })
		return rec
	})
	cs := h.sdkClient(t, "/mcp", mcp.VersionModern)
	_, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "up__echo", Arguments: map[string]any{"text": "private words"}})
	require.NoError(t, err)

	rows := auditRows(t, h.db, rec)
	var call, create *store.AuditEntry
	for i := range rows {
		switch rows[i].Action {
		case "mcp.tool.call":
			call = &rows[i]
		case "mcp.servers":
			create = &rows[i]
		}
	}
	require.NotNil(t, create, "creating the server is an audited admin action")
	require.NotNil(t, call, "the tool call is in the audit log")
	require.Equal(t, "api_key", call.ActorType)
	require.Equal(t, h.keyID, call.Actor)
	require.Equal(t, "up__echo", call.Target)
	require.Equal(t, "success", call.Outcome)
	raw, _ := json.Marshal(call)
	require.NotContains(t, string(raw), "private words")
}
