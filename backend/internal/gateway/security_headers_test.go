package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecurityHeaders_NoSniff(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusBadRequest, "<script>alert(1)</script>")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "strict-origin-when-cross-origin", rec.Header().Get("Referrer-Policy"))
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
}

// The popup page must never let a provider name or message break out of
// the HTML or the inline script.
func TestOAuthPopupResult_Escapes(t *testing.T) {
	out := renderOAuthPopupResult(`x</script><script>alert(1)</script>`, "error", `<img src=x onerror=alert(1)>`)
	require.NotContains(t, out, "<script>alert(1)")
	require.NotContains(t, out, "<img src=x")
	require.Contains(t, out, "&lt;img src=x onerror=alert(1)&gt;")
}
