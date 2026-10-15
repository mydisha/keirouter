package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// cliToolsRouter mounts only the CLI tool endpoints (skipping admin auth) on a
// gateway whose tool configs live in a temp home.
func cliToolsRouter(t *testing.T) (*e2eHarness, http.Handler, string) {
	t.Helper()
	h := newE2E(t, openAIUpstream())
	home := t.TempDir()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "HERMES_HOME"} {
		t.Setenv(k, "")
	}
	h.gateway.cliToolHome = home
	r := chi.NewRouter()
	h.gateway.mountCLITools(r)
	return h, r, home
}

func doCLI(t *testing.T, r http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:20180"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestCLIToolConfigure_RejectsUnknownKey(t *testing.T) {
	_, r, home := cliToolsRouter(t)

	// The masked display form and the old "sk_keirouter" default both fail
	// gateway auth, so neither may be written into a tool.
	for _, key := range []string{"sk_keirouter", "kr_AbC1…7xQ2"} {
		code, out := doCLI(t, r, http.MethodPost, "/cli-tools/claude/configure",
			`{"base_url":"http://localhost:20180","api_key":"`+key+`"}`)
		require.Equal(t, http.StatusBadRequest, code, out)
	}
	_, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	require.True(t, os.IsNotExist(err), "nothing should be written for a rejected key")
}

func TestCLIToolConfigure_WritesAndReportsSettings(t *testing.T) {
	h, r, home := cliToolsRouter(t)

	code, out := doCLI(t, r, http.MethodPost, "/cli-tools/claude/configure",
		`{"base_url":"http://127.0.0.1:20180/v1","api_key":"`+h.apiKey+`","model":"openai/gpt-4o"}`)
	require.Equal(t, http.StatusOK, code, out)

	tool := out["tool"].(map[string]any)
	require.Equal(t, true, tool["configured"])
	settings := tool["settings"].(map[string]any)
	require.Equal(t, "http://127.0.0.1:20180", settings["base_url"])
	require.Equal(t, "openai/gpt-4o", settings["model"])
	require.Equal(t, "valid", settings["key_status"])
	require.Equal(t, "test-key", settings["key_name"])
	// 127.0.0.1 and the request's localhost host are the same gateway.
	require.Equal(t, true, settings["endpoint_match"])
	require.NotContains(t, settings["api_key_hint"], h.apiKey[6:])

	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	require.NoError(t, err)
	require.Contains(t, string(raw), h.apiKey)

	// Reconfiguring without a key keeps the stored one.
	code, out = doCLI(t, r, http.MethodPost, "/cli-tools/claude/configure",
		`{"base_url":"http://localhost:20180","model":"openai/gpt-5"}`)
	require.Equal(t, http.StatusOK, code, out)
	settings = out["tool"].(map[string]any)["settings"].(map[string]any)
	require.Equal(t, "valid", settings["key_status"])
	require.Equal(t, "openai/gpt-5", settings["model"])

	// The list endpoint reports the same connection.
	code, out = doCLI(t, r, http.MethodGet, "/cli-tools", "")
	require.Equal(t, http.StatusOK, code)
	var claude map[string]any
	for _, v := range out["tools"].([]any) {
		if m := v.(map[string]any); m["id"] == "claude" {
			claude = m
		}
	}
	require.NotNil(t, claude)
	require.Equal(t, true, claude["configured"])
	require.NotEmpty(t, claude["snippet"])

	code, out = doCLI(t, r, http.MethodPost, "/cli-tools/claude/remove", "")
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, false, out["tool"].(map[string]any)["configured"])
}

func TestCLIToolConfigure_Validation(t *testing.T) {
	h, r, home := cliToolsRouter(t)

	code, _ := doCLI(t, r, http.MethodPost, "/cli-tools/opencode/configure",
		`{"base_url":"http://localhost:20180","api_key":"`+h.apiKey+`"}`)
	require.Equal(t, http.StatusBadRequest, code, "opencode needs a model")

	code, _ = doCLI(t, r, http.MethodPost, "/cli-tools/claude/configure",
		`{"base_url":"ftp://x","api_key":"`+h.apiKey+`"}`)
	require.Equal(t, http.StatusBadRequest, code)

	code, _ = doCLI(t, r, http.MethodPost, "/cli-tools/claude/configure",
		`{"base_url":"http://localhost:20180"}`)
	require.Equal(t, http.StatusBadRequest, code, "no key given and none stored")

	// An unparseable config is reported, not overwritten.
	path := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"broken":`), 0o600))
	code, _ = doCLI(t, r, http.MethodPost, "/cli-tools/claude/configure",
		`{"base_url":"http://localhost:20180","api_key":"`+h.apiKey+`"}`)
	require.Equal(t, http.StatusUnprocessableEntity, code)

	code, _ = doCLI(t, r, http.MethodGet, "/cli-tools/nope", "")
	require.Equal(t, http.StatusNotFound, code)
}

func TestEndpointKey(t *testing.T) {
	same := [][2]string{
		{"http://localhost:20180", "http://127.0.0.1:20180/v1"},
		{"http://[::1]:20180/", "http://0.0.0.0:20180"},
		{"https://gw.example.com", "https://GW.example.com:443/v1"},
	}
	for _, p := range same {
		a, _ := endpointKey(p[0])
		b, _ := endpointKey(p[1])
		require.Equal(t, a, b, p)
	}
	a, _ := endpointKey("http://localhost:20180")
	b, _ := endpointKey("http://localhost:20181")
	require.NotEqual(t, a, b)
}
