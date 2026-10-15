package gateway

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStaticFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":           "<!doctype html><title>KeiRouter</title>",
		"assets/app-abc123.js": strings.Repeat("console.log('keirouter');\n", 200),
		"favicon.png":          "png",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func serveStatic(h http.Handler, target, acceptEncoding string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticHandlerGzipsHashedAssetsAndCachesImmutably(t *testing.T) {
	dir := writeStaticFixture(t)
	h := newStaticHandler(dir)

	rec := serveStatic(h, "/assets/app-abc123.js", "br, gzip")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Fatalf("Content-Type = %q", got)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	want, _ := os.ReadFile(filepath.Join(dir, "assets/app-abc123.js"))
	if !bytes.Equal(body, want) {
		t.Fatal("decompressed body does not match source file")
	}

	// Second request is served from the in-memory cache with identical bytes.
	again := serveStatic(h, "/assets/app-abc123.js", "gzip")
	if again.Body.Len() == 0 || again.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("cached gzip response missing")
	}
}

func TestStaticHandlerPlainWhenGzipNotAccepted(t *testing.T) {
	h := newStaticHandler(writeStaticFixture(t))
	for _, ae := range []string{"", "identity", "gzip;q=0"} {
		rec := serveStatic(h, "/assets/app-abc123.js", ae)
		if rec.Header().Get("Content-Encoding") != "" {
			t.Fatalf("Accept-Encoding %q: unexpected Content-Encoding", ae)
		}
		if !strings.HasPrefix(rec.Body.String(), "console.log") {
			t.Fatalf("Accept-Encoding %q: body not plain", ae)
		}
	}
}

func TestStaticHandlerSPAFallbackAndMissingAssets(t *testing.T) {
	h := newStaticHandler(writeStaticFixture(t))

	for _, target := range []string{"/", "/providers/abc", "/index.html"} {
		rec := serveStatic(h, target, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "KeiRouter") {
			t.Fatalf("%s: status=%d body=%q", target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: Cache-Control = %q, want no-cache", target, got)
		}
	}

	if rec := serveStatic(h, "/assets/gone-old.js", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", rec.Code)
	}

	// Traversal stays inside the build dir and falls back to the SPA shell.
	rec := serveStatic(h, "/../../etc/passwd", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "KeiRouter") {
		t.Fatalf("traversal: status=%d body=%q", rec.Code, rec.Body.String())
	}

	if rec := serveStatic(h, "/favicon.png", "gzip"); rec.Header().Get("Content-Encoding") != "" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "max-age=3600") {
		t.Fatalf("favicon headers: %v", rec.Header())
	}
}
