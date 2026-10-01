package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/stretchr/testify/require"
)

// TestPortalAPIRoutesDoNotShadowSPA guards the browser-refresh contract: the
// SPA client routes /portal/key, /portal/usage and /portal/topup must be served
// the app shell, never the portal JSON API. The portal session API therefore
// lives under /portal/api/*.
func TestPortalAPIRoutesDoNotShadowSPA(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=root>SPA</div>"), 0o644))

	s := &Server{cfg: config.Default(), frontendDir: dir}
	router := s.routes()

	for _, p := range []string{"/portal/key", "/portal/usage", "/portal/topup"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		require.Equal(t, http.StatusOK, rec.Code, "SPA route %s must serve the app shell", p)
		require.Contains(t, rec.Body.String(), "SPA", "SPA route %s must not return API JSON", p)
	}

	for _, p := range []string{"/portal/api/key", "/portal/api/usage", "/portal/api/topups"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code, "portal API %s must require a session", p)
	}
}
