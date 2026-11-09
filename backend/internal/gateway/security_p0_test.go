package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// TestLoopbackOnlyRejectsTunnelAndProxyTraffic: cloudflared and Tailscale
// Funnel forward internet clients from 127.0.0.1, so a loopback peer alone
// let the public reach /api whenever a tunnel was on.
func TestLoopbackOnlyRejectsTunnelAndProxyTraffic(t *testing.T) {
	srv := newAuthGateway(t, config.Default())

	status := func(mutate func(*http.Request)) int {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/status", nil)
		require.NoError(t, err)
		mutate(req)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, http.StatusOK, status(func(*http.Request) {}), "direct local request")
	require.Equal(t, http.StatusOK, status(func(r *http.Request) { r.Host = "localhost:20180" }))
	require.Equal(t, http.StatusOK, status(func(r *http.Request) { r.Host = "app.localhost" }))
	require.Equal(t, http.StatusOK, status(func(r *http.Request) { r.Host = "[::1]:20180" }))

	for _, h := range []string{"Cf-Connecting-Ip", "Cf-Ray", "Tailscale-Funnel-Request", "X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		require.Equal(t, http.StatusForbidden, status(func(r *http.Request) { r.Header.Set(h, "203.0.113.7") }), h)
	}
	// DNS rebinding: a hostile name that re-resolved to 127.0.0.1 keeps its
	// own Host header.
	require.Equal(t, http.StatusForbidden, status(func(r *http.Request) { r.Host = "evil.example:20180" }))
	require.Equal(t, http.StatusForbidden, status(func(r *http.Request) { r.Host = "localhost.evil.example" }))
}

func TestLoopbackOnlyOffAllowsRemote(t *testing.T) {
	cfg := config.Default()
	cfg.Security.BindLoopbackOnly = false
	srv := newAuthGateway(t, cfg)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/status", nil)
	req.Host = "keirouter.example.com"
	req.Header.Set("Cf-Connecting-Ip", "203.0.113.7")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"":                  true,
		"localhost":         true,
		"LOCALHOST:20180":   true,
		"localhost.":        true,
		"ui.localhost:5173": true,
		"127.0.0.1:20180":   true,
		"127.8.9.1":         true,
		"[::1]:20180":       true,
		"::1":               true,
		"10.0.0.5:20180":    false,
		"evil.example":      false,
		"localhost.evil":    false,
		"0.0.0.0:20180":     false,
	} {
		require.Equal(t, want, isLoopbackHost(host), host)
	}
}

// TestDefaultPasswordLocksAdminAPI: the default password is published, so a
// session obtained with it may only change the password.
func TestDefaultPasswordLocksAdminAPI(t *testing.T) {
	srv := newAuthGateway(t, config.Default())

	// Anonymous callers are not told the default is still active.
	resp, err := http.Get(srv.URL + "/api/auth/status")
	require.NoError(t, err)
	var anon map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&anon))
	resp.Body.Close()
	require.Equal(t, false, anon["authenticated"])
	require.Equal(t, false, anon["using_default"])

	resp, err = http.Post(srv.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"password":"`+auth.DefaultPassword+`"}`))
	require.NoError(t, err)
	resp.Body.Close()
	session := findCookie(resp.Cookies(), sessionCookie)
	require.NotNil(t, session)

	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(session)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := do(http.MethodGet, "/api/auth/status", "")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `"using_default":true`)

	code, body = do(http.MethodGet, "/api/update/check", "")
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, body, "password_change_required")

	// Completing onboarding without a new password no longer unlocks it.
	code, _ = do(http.MethodPost, "/api/auth/onboarding/complete", "")
	require.Equal(t, http.StatusOK, code)
	code, _ = do(http.MethodGet, "/api/update/check", "")
	require.Equal(t, http.StatusForbidden, code)

	code, _ = do(http.MethodPost, "/api/auth/password", `{"new_password":"a-new-secret"}`)
	require.Equal(t, http.StatusOK, code)
	code, _ = do(http.MethodGet, "/api/update/check", "")
	require.Equal(t, http.StatusOK, code)
}

func TestSecurityHeadersAndRobots(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })
	authSvc := auth.New(db.Settings(), "", 0)
	_, err = authSvc.EnsureDefaults(ctx)
	require.NoError(t, err)

	dir := t.TempDir()
	inline := "\n      document.documentElement.classList.add('dark');\n    "
	index := `<!doctype html><head><script>` + inline + `</script>` +
		`<script type="module" crossorigin src="/assets/app.js"></script></head><body>KeiRouter</body>`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644))

	gw := New(Deps{Config: config.Default(), Auth: authSvc, DB: db, Settings: db.Settings(), FrontendDir: dir})
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	get := func(path string) (*http.Response, string) {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	for _, p := range []string{"/", "/settings", "/v1", "/healthz", "/api/auth/status", "/robots.txt"} {
		resp, _ := get(p)
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), p)
		require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"), p)
		require.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"), p)
		require.Contains(t, resp.Header.Get("X-Robots-Tag"), "noindex", p)
		require.Empty(t, resp.Header.Get("Strict-Transport-Security"), "plain HTTP must not pin HTTPS: "+p)
	}

	resp, body := get("/robots.txt")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain"))
	require.Contains(t, body, "Disallow: /")

	resp, _ = get("/")
	csp := resp.Header.Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte(inline))
	require.Contains(t, csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	require.Contains(t, csp, "frame-ancestors 'none'")
	require.Contains(t, csp, "object-src 'none'")
	require.NotContains(t, csp, "'unsafe-inline' 'self'", "scripts must not allow unsafe-inline")
	require.NotContains(t, strings.Split(csp, ";")[1], "unsafe-inline")

	resp, body = get("/v1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &info))
	require.NotContains(t, info, "version", "public /v1 must not fingerprint the build")
	require.Equal(t, "KeiRouter", info["name"])
}

func TestSecurityHeadersHSTSOnlyOverHTTPS(t *testing.T) {
	s := &Server{cfg: config.Default()}
	h := s.securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(rec, req)
	require.Contains(t, rec.Header().Get("Strict-Transport-Security"), "max-age=")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Empty(t, rec.Header().Get("Strict-Transport-Security"))
}

// TestStaticHandlerRejectsBackslashTraversal: path.Clean ignores "\", which
// filepath.Join on Windows treats as a separator, so /..%5c..%5cfile escaped
// the build directory without authentication.
func TestStaticHandlerRejectsBackslashTraversal(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	require.NoError(t, os.MkdirAll(dist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "index.html"), []byte("<title>KeiRouter</title>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "master.key"), []byte("TOP-SECRET"), 0o600))
	h := newStaticHandler(dist)

	for _, target := range []string{"/..%5cmaster.key", "/..%5c..%5cmaster.key", "/assets%5c..%5c..%5cmaster.key", "/x%00.js"} {
		rec := serveStatic(h, target, "")
		require.Equal(t, http.StatusNotFound, rec.Code, target)
		require.NotContains(t, rec.Body.String(), "TOP-SECRET", target)
	}
	// Ordinary client-side routes still fall back to the SPA.
	rec := serveStatic(h, "/providers/openai", "")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPathInside(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "srv", "dist")
	require.True(t, pathInside(dir, dir))
	require.True(t, pathInside(dir, filepath.Join(dir, "index.html")))
	require.True(t, pathInside(dir, filepath.Join(dir, "..dots", "x")))
	require.False(t, pathInside(dir, filepath.Join(dir, "..", "master.key")))
	require.False(t, pathInside(dir, filepath.Join(string(filepath.Separator), "etc", "passwd")))
}
