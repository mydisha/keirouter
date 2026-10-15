package httputil

import (
	"net/http"
	"net/url"
)

// RelayRequest rewrites req in place to go through a relay (vercel /
// cloudflare / deno). The relay protocol carries the original origin in
// x-relay-target and the escaped path plus query in x-relay-path; the relay
// URL itself, including its own query, becomes the request URL. Callers that
// must not mutate the caller's request pass a clone.
func RelayRequest(req *http.Request, relay *url.URL) {
	origOrigin := req.URL.Scheme + "://" + req.URL.Host
	origPath := req.URL.EscapedPath()
	if origPath == "" {
		origPath = "/"
	}
	if req.URL.RawQuery != "" {
		origPath += "?" + req.URL.RawQuery
	}
	req.Header.Set("x-relay-target", origOrigin)
	req.Header.Set("x-relay-path", origPath)
	u := *relay
	req.URL = &u
	req.Host = relay.Host
}
