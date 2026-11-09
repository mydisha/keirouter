package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
)

// securityHeaders sets the response headers every KeiRouter response carries:
//
//   - X-Content-Type-Options stops browsers from sniffing JSON or text into HTML.
//   - X-Frame-Options keeps the dashboard and login page out of foreign frames
//     (clickjacking). The SPA's CSP repeats this as frame-ancestors.
//   - Referrer-Policy keeps dashboard and key-portal URLs (the portal link
//     carries a key id) out of Referer headers sent to other sites.
//   - X-Robots-Tag keeps search engines from indexing an instance that is
//     reachable from the internet (VPS install, tunnel URL).
//   - Strict-Transport-Security is only sent when the browser reached us over
//     HTTPS, so plain-HTTP LAN installs are not pinned to a scheme they lack.
//
// This runs on every proxied request, so the values are shared, pre-built
// slices assigned under canonical keys: no per-request allocation. Header.Set
// replaces a value and Header.Add appends to a cap-1 slice (forcing a copy),
// so nothing downstream can write into the shared backing arrays.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h["X-Content-Type-Options"] = hdrNosniff
		h["X-Frame-Options"] = hdrFrameDeny
		h["Referrer-Policy"] = hdrNoReferrer
		h["X-Robots-Tag"] = hdrNoIndex
		if s.sessionCookieSecure(r) {
			h["Strict-Transport-Security"] = hdrHSTS
		}
		next.ServeHTTP(w, r)
	})
}

var (
	hdrNosniff    = []string{"nosniff"}
	hdrFrameDeny  = []string{"DENY"}
	hdrNoReferrer = []string{"no-referrer"}
	hdrNoIndex    = []string{"noindex, nofollow, noarchive"}
	hdrHSTS       = []string{"max-age=31536000"}
)

// robotsTxt tells well-behaved crawlers to skip the whole instance. Without it
// the SPA fallback answers /robots.txt with index.html and a 200.
const robotsTxt = "User-agent: *\nDisallow: /\n"

func serveRobots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(robotsTxt))
}

// inlineScriptRE matches <script> elements without a src attribute and
// captures their body. Vite emits only external module scripts; index.html
// carries one inline script (theme bootstrap) that must run before paint.
var inlineScriptRE = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)

// spaCSP builds the Content-Security-Policy for the dashboard's index.html.
// Inline scripts present in the file are allowed by hash, so the policy
// follows the build without a hand-maintained digest.
//
// img-src stays open because operators can point the white-label logo at any
// URL; images cannot run code. connect-src 'self' covers the API and the
// same-origin SSE streams.
func spaCSP(index []byte) string {
	scriptSrc := []string{"'self'"}
	for _, m := range inlineScriptRE.FindAllSubmatch(index, -1) {
		if strings.Contains(strings.ToLower(string(m[1])), "src=") {
			continue
		}
		sum := sha256.Sum256(m[2])
		scriptSrc = append(scriptSrc, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src * data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"worker-src 'self' blob:",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}
