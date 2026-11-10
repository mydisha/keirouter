package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/mcp"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type mcpAdminHarness struct {
	srv     *httptest.Server
	session string
	db      *store.DB
	mgr     *mcp.Manager
}

func newMCPAdmin(t *testing.T, catalogFile string, catalogOnly bool) *mcpAdminHarness {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	mgr, err := mcp.NewManager(ctx, db, sealer, mcp.Options{
		Info: mcp.Implementation{Name: "KeiRouter", Version: "test"}, CatalogFile: catalogFile, CatalogOnly: catalogOnly,
	})
	require.NoError(t, err)
	authSvc := auth.New(db.Settings(), "mcp-admin-signing-key", time.Hour)
	session, err := authSvc.IssueSession()
	require.NoError(t, err)
	gw := New(Deps{Config: config.Default(), Auth: authSvc, DB: db, Settings: db.Settings(), MCP: mgr})
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)
	return &mcpAdminHarness{srv: srv, session: session, db: db, mgr: mgr}
}

func (h *mcpAdminHarness) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, h.srv.URL+"/api"+path, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.session})
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	}
	return res.StatusCode, out
}

// fakeMCPUpstream is a minimal modern MCP server with two tools.
func fakeMCPUpstream(t *testing.T, wantAuth string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req mcp.Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "server/discover":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}}}}`, req.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"search","description":"Search docs","inputSchema":{"type":"object"}},{"name":"fetch","inputSchema":{"type":"object"}}]}}`, req.ID)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"not found"}}`, req.ID)
		}
	}))
	t.Cleanup(srv.Close)
	httputil.SetAllowPrivateBaseURL(true)
	t.Cleanup(func() { httputil.SetAllowPrivateBaseURL(false) })
	return srv
}

func TestMCPAdmin_CreateSyncsAndHidesSecret(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	up := fakeMCPUpstream(t, "Bearer s3cr3t-token")

	status, body := h.do(t, http.MethodPost, "/mcp/servers", map[string]any{
		"slug": "docs", "name": "Docs", "url": up.URL, "auth_type": "bearer", "secret": "s3cr3t-token",
		"static_headers": map[string]string{"x-team": "core"}, "forward_headers": []string{"x-request-source", "X-Request-Source"},
	})
	require.Equal(t, http.StatusCreated, status, body)
	require.Equal(t, "docs", body["slug"])
	require.Equal(t, true, body["has_secret"])
	require.Equal(t, "healthy", body["health_status"])
	require.Equal(t, "2026-07-28", body["protocol_version"])
	require.EqualValues(t, 2, body["exposed_tool_count"])
	require.Equal(t, []any{"X-Team"}, body["static_header_names"])
	require.Equal(t, []any{"X-Request-Source"}, body["forward_headers"])
	raw, _ := json.Marshal(body)
	require.NotContains(t, string(raw), "s3cr3t-token")
	require.NotContains(t, string(raw), "core", "static header values are secret too")

	tools := body["tools"].([]any)
	require.Equal(t, "docs__fetch", tools[0].(map[string]any)["exposed_name"])
	require.Equal(t, map[string]any{"type": "object"}, tools[0].(map[string]any)["input_schema"], "the tester builds its form from the schema")

	id := body["id"].(string)
	status, list := h.do(t, http.MethodGet, "/mcp/servers", nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, list["servers"], 1)
	require.NotNil(t, h.mgr.Registry.Snapshot().BySlug["docs"])

	status, body = h.do(t, http.MethodPost, "/mcp/servers", map[string]any{"slug": "docs", "name": "Again", "url": up.URL, "auth_type": "none"})
	require.Equal(t, http.StatusConflict, status, body)

	status, _ = h.do(t, http.MethodDelete, "/mcp/servers/"+id, nil)
	require.Equal(t, http.StatusNoContent, status)
	require.Nil(t, h.mgr.Registry.Snapshot().BySlug["docs"])
	status, _ = h.do(t, http.MethodGet, "/mcp/servers/"+id, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestMCPAdmin_FailedSyncStillSaves(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	up := fakeMCPUpstream(t, "Bearer right")
	status, body := h.do(t, http.MethodPost, "/mcp/servers", map[string]any{
		"slug": "x", "name": "X", "url": up.URL, "auth_type": "bearer", "secret": "wrong",
	})
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, "failing", body["health_status"])
	require.Contains(t, body["sync_error"], "credentials")

	// Fix the secret: the update re-negotiates and syncs.
	status, body = h.do(t, http.MethodPatch, "/mcp/servers/"+body["id"].(string), map[string]any{"secret": "right"})
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "healthy", body["health_status"])
}

func TestMCPAdmin_Validation(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	up := fakeMCPUpstream(t, "")
	base := func(over map[string]any) map[string]any {
		b := map[string]any{"slug": "ok", "name": "OK", "url": up.URL, "auth_type": "none"}
		for k, v := range over {
			b[k] = v
		}
		return b
	}
	cases := map[string]map[string]any{
		"slug uppercase":        base(map[string]any{"slug": "GitHub"}),
		"slug underscore":       base(map[string]any{"slug": "git_hub"}),
		"slug too long":         base(map[string]any{"slug": strings.Repeat("a", 33)}),
		"slug edge hyphen":      base(map[string]any{"slug": "-gh"}),
		"no name":               base(map[string]any{"name": "  "}),
		"bad scheme":            base(map[string]any{"url": "ftp://x.example/mcp"}),
		"credentials in url":    base(map[string]any{"url": "https://user:pw@x.example/mcp"}),
		"metadata address":      base(map[string]any{"url": "http://169.254.169.254/latest"}),
		"bad auth type":         base(map[string]any{"auth_type": "oauth"}),
		"header auth no name":   base(map[string]any{"auth_type": "header", "secret": "k"}),
		"basic no username":     base(map[string]any{"auth_type": "basic", "secret": "k"}),
		"reserved static":       base(map[string]any{"static_headers": map[string]string{"Authorization": "x"}}),
		"static crlf":           base(map[string]any{"static_headers": map[string]string{"X-A": "a\r\nX-B: b"}}),
		"forward authorization": base(map[string]any{"forward_headers": []string{"Authorization"}}),
		"forward mcp header":    base(map[string]any{"forward_headers": []string{"Mcp-Session-Id"}}),
		"timeout too low":       base(map[string]any{"timeout_ms": 10}),
		"secret newline":        base(map[string]any{"auth_type": "bearer", "secret": "a\nb"}),
		"unknown preset":        base(map[string]any{"preset_id": "nope"}),
		"preset needs secret":   base(map[string]any{"preset_id": "github", "auth_type": "bearer"}),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			status, out := h.do(t, http.MethodPost, "/mcp/servers", body)
			require.Equal(t, http.StatusBadRequest, status, out)
		})
	}
	status, out := h.do(t, http.MethodPost, "/mcp/servers", base(map[string]any{"colour": "red"}))
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestMCPAdmin_SlugIsImmutable(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	up := fakeMCPUpstream(t, "")
	_, body := h.do(t, http.MethodPost, "/mcp/servers", map[string]any{"slug": "a", "name": "A", "url": up.URL, "auth_type": "none"})
	status, out := h.do(t, http.MethodPatch, "/mcp/servers/"+body["id"].(string), map[string]any{"slug": "b"})
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestMCPAdmin_ToolSelection(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	up := fakeMCPUpstream(t, "")
	_, body := h.do(t, http.MethodPost, "/mcp/servers", map[string]any{"slug": "a", "name": "A", "url": up.URL, "auth_type": "none"})
	id := body["id"].(string)

	status, out := h.do(t, http.MethodPut, "/mcp/servers/"+id+"/tools", map[string]any{"tool_mode": "selected", "enabled": []string{}})
	require.Equal(t, http.StatusBadRequest, status, out)
	status, out = h.do(t, http.MethodPut, "/mcp/servers/"+id+"/tools", map[string]any{"tool_mode": "selected", "enabled": []string{"ghost"}})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"].(map[string]any)["message"], "ghost")

	status, out = h.do(t, http.MethodPut, "/mcp/servers/"+id+"/tools", map[string]any{"tool_mode": "selected", "enabled": []string{"search"}})
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 1, out["exposed_tool_count"])
	require.Len(t, h.mgr.Registry.Snapshot().BySlug["a"].Sorted, 1)
}

func TestMCPAdmin_TestConnectionSavesNothing(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	up := fakeMCPUpstream(t, "Bearer k")
	status, out := h.do(t, http.MethodPost, "/mcp/servers/test", map[string]any{"url": up.URL, "auth_type": "bearer", "secret": "k"})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["ok"])
	require.Len(t, out["tools"], 2)

	status, out = h.do(t, http.MethodPost, "/mcp/servers/test", map[string]any{"url": up.URL, "auth_type": "bearer", "secret": "bad"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, false, out["ok"])
	require.Contains(t, out["error"], "credentials")

	_, list := h.do(t, http.MethodGet, "/mcp/servers", nil)
	require.Empty(t, list["servers"])
}

func TestMCPAdmin_CatalogOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
presets:
  - id: internal
    name: Internal
    url: http://127.0.0.1:{port}/mcp
    auth_types: [none]
    vars:
      - key: port
        label: Port
        type: text
        default: "8080"
`), 0o600))
	h := newMCPAdmin(t, path, true)
	up := fakeMCPUpstream(t, "")

	status, out := h.do(t, http.MethodGet, "/mcp/catalog", nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out["catalog_only"])

	status, out = h.do(t, http.MethodPost, "/mcp/servers", map[string]any{"slug": "c", "name": "C", "url": up.URL, "auth_type": "none"})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"].(map[string]any)["message"], "catalog")

	status, out = h.do(t, http.MethodPost, "/mcp/servers", map[string]any{"slug": "c", "name": "C", "url": "http://127.0.0.2/mcp", "auth_type": "none", "preset_id": "internal"})
	require.Equal(t, http.StatusBadRequest, status, out)

	status, out = h.do(t, http.MethodPost, "/mcp/servers", map[string]any{"slug": "c", "name": "C", "url": up.URL + "/mcp", "auth_type": "none", "preset_id": "internal"})
	require.Equal(t, http.StatusCreated, status, out)
}

func TestMCPAdmin_RequiresSession(t *testing.T) {
	h := newMCPAdmin(t, "", false)
	res, err := http.Get(h.srv.URL + "/api/mcp/servers")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}
