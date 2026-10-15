package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

type staticProxy struct{ url, noProxy string }

func (p staticProxy) ProxyURL() string { return p.url }
func (p staticProxy) NoProxy() string  { return p.noProxy }

func newMarkerServer(t *testing.T, marker string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, marker)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchVia(t *testing.T, ctx context.Context, target string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: newProxyTransport()}).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestProxyTransportRouting(t *testing.T) {
	target := newMarkerServer(t, "direct")
	globalSrv := newMarkerServer(t, "global")
	accountSrv := newMarkerServer(t, "account")
	t.Cleanup(func() { SetGlobalProxy(nil) })

	SetGlobalProxy(nil)
	if got := fetchVia(t, context.Background(), target.URL); got != "direct" {
		t.Fatalf("no proxy: got %q", got)
	}

	SetGlobalProxy(staticProxy{url: globalSrv.URL})
	if got := fetchVia(t, context.Background(), target.URL); got != "global" {
		t.Fatalf("global proxy: got %q", got)
	}

	accountCtx := core.WithProxy(context.Background(), core.Credentials{ProxyURL: accountSrv.URL})
	if got := fetchVia(t, accountCtx, target.URL); got != "account" {
		t.Fatalf("account proxy should win over global: got %q", got)
	}

	relaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "relay:"+r.Header.Get("x-relay-target")+r.Header.Get("x-relay-path"))
	}))
	t.Cleanup(relaySrv.Close)
	relayCtx := core.WithProxy(context.Background(), core.Credentials{RelayURL: relaySrv.URL})
	if got, want := fetchVia(t, relayCtx, target.URL+"/token?x=1"), "relay:"+target.URL+"/token?x=1"; got != want {
		t.Fatalf("relay-bound account should refresh through its relay: got %q want %q", got, want)
	}

	SetGlobalProxy(staticProxy{url: globalSrv.URL, noProxy: "127.0.0.1"})
	if got := fetchVia(t, context.Background(), target.URL); got != "direct" {
		t.Fatalf("no_proxy bypass: got %q", got)
	}

	SetGlobalProxy(staticProxy{})
	if got := fetchVia(t, context.Background(), target.URL); got != "direct" {
		t.Fatalf("disabled global proxy: got %q", got)
	}
}
