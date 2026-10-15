package connectors

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// deadAddr returns a loopback URL nothing listens on.
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}

func get(t *testing.T, ctx context.Context, target string) (string, error) {
	t.Helper()
	resp, err := sendRequest(ctx, "test", "m", jsonRequest(ctx, http.MethodGet, target, nil, nil))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestSendRequestProxyPoolStrictness(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "origin")
	}))
	defer origin.Close()
	dead := deadAddr(t)

	cases := []struct {
		name    string
		creds   core.Credentials
		wantErr bool
	}{
		{"non-strict proxy down falls back direct", core.Credentials{ProxyURL: dead}, false},
		{"strict proxy down fails", core.Credentials{ProxyURL: dead, StrictProxy: true}, true},
		{"non-strict relay down falls back direct", core.Credentials{RelayURL: dead}, false},
		{"strict relay down fails", core.Credentials{RelayURL: dead, StrictProxy: true}, true},
		{"invalid proxy URL fails closed even when non-strict", core.Credentials{ProxyURL: "http://"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := get(t, core.WithProxy(context.Background(), tc.creds), origin.URL+"/v1")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected failure, got %q", got)
				}
				return
			}
			if err != nil || got != "origin" {
				t.Fatalf("got %q, err %v", got, err)
			}
		})
	}
}

func TestSendRequestUsesHTTPProxy(t *testing.T) {
	var proxied string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A forward proxy receives the absolute target URL.
		proxied = r.URL.String()
		_, _ = io.WriteString(w, "via-proxy")
	}))
	defer proxySrv.Close()

	ctx := core.WithProxy(context.Background(), core.Credentials{ProxyURL: proxySrv.URL, StrictProxy: true})
	got, err := get(t, ctx, "http://upstream.invalid/v1/models?x=1")
	if err != nil || got != "via-proxy" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if proxied != "http://upstream.invalid/v1/models?x=1" {
		t.Fatalf("proxy saw %q", proxied)
	}
}

func TestRelayTransportRewritesEveryClient(t *testing.T) {
	type seen struct{ target, path, host string }
	var got seen
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = seen{r.Header.Get("x-relay-target"), r.Header.Get("x-relay-path"), r.URL.String()}
		_, _ = io.WriteString(w, "relayed")
	}))
	defer relay.Close()

	ctx := core.WithProxy(context.Background(), core.Credentials{RelayURL: relay.URL + "/r?k=secret", StrictProxy: true})

	// sendRequest path.
	body, err := get(t, ctx, "https://api.example.com/v1/chat%2Fx?stream=true")
	if err != nil || body != "relayed" {
		t.Fatalf("sendRequest: %q %v", body, err)
	}
	if got.target != "https://api.example.com" || got.path != "/v1/chat%2Fx?stream=true" || got.host != "/r?k=secret" {
		t.Fatalf("relay saw %+v", got)
	}

	// Direct proxyDo path (connectors that build their own requests).
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.example.com/bootstrap", nil)
	resp, err := proxyDo(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.target != "https://auth.example.com" || got.path != "/bootstrap" {
		t.Fatalf("proxyDo relay saw %+v", got)
	}
	// The caller's request must not be mutated by the rewrite.
	if req.URL.Host != "auth.example.com" || req.Header.Get("x-relay-target") != "" {
		t.Fatalf("caller request mutated: %s %v", req.URL, req.Header)
	}
}

func TestEgressCtxPrefersContextRoute(t *testing.T) {
	ctxRoute := core.Credentials{ProxyURL: "http://ctx-proxy:1"}
	ctx := core.WithProxy(context.Background(), ctxRoute)
	if c, _ := core.ProxyFromContext(egressCtx(ctx, core.Credentials{ProxyURL: "http://creds:1"})); c.ProxyURL != ctxRoute.ProxyURL {
		t.Fatalf("context route should win, got %q", c.ProxyURL)
	}
	if c, _ := core.ProxyFromContext(egressCtx(context.Background(), core.Credentials{RelayURL: "https://relay"})); c.RelayURL != "https://relay" {
		t.Fatalf("creds route should apply when ctx has none, got %+v", c)
	}
}

func TestIsEgressFailure(t *testing.T) {
	if !isEgressFailure(&net.OpError{Op: "proxyconnect", Err: io.EOF}, false) {
		t.Fatal("proxyconnect must count as egress failure")
	}
	if !isEgressFailure(&errProxyRejected{status: "407 Proxy Authentication Required"}, false) {
		t.Fatal("rejected CONNECT must count as egress failure")
	}
	if isEgressFailure(&net.OpError{Op: "dial", Err: io.EOF}, false) {
		t.Fatal("a plain dial error in proxy mode is not the proxy hop")
	}
	if !isEgressFailure(&net.OpError{Op: "dial", Err: io.EOF}, true) {
		t.Fatal("a dial error in relay mode is the relay hop")
	}
	if isEgressFailure(io.ErrUnexpectedEOF, true) {
		t.Fatal("mid-stream EOF must not trigger a direct replay")
	}
	if !strings.Contains((&errProxyRejected{status: "502 Bad Gateway"}).Error(), "502") {
		t.Fatal("status lost")
	}
}

func TestStrictEgressFailureIsRequestScoped(t *testing.T) {
	ctx := core.WithProxy(context.Background(), core.Credentials{ProxyURL: deadAddr(t), StrictProxy: true})
	_, err := get(t, ctx, "http://upstream.invalid/v1")
	pe := core.AsProviderError(err)
	if pe == nil || pe.EffectiveScope() != core.FailureScopeRequest || !strings.Contains(pe.Message, "strict proxy pool") {
		t.Fatalf("want request-scoped strict proxy error, got %#v", err)
	}
}
