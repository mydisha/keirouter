package oauth

import (
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
)

// ProxyReader supplies the dashboard's global outbound proxy. The gateway's
// ProxyNotifier implements it; ProxyURL returns "" while the proxy is off.
type ProxyReader interface {
	ProxyURL() string
	NoProxy() string
}

type proxyReaderBox struct{ r ProxyReader }

var globalProxy atomic.Pointer[proxyReaderBox]

// SetGlobalProxy installs the global outbound proxy used by OAuth logins and
// token refreshes that carry no per-account proxy in their context.
func SetGlobalProxy(r ProxyReader) {
	if r == nil {
		globalProxy.Store(nil)
		return
	}
	globalProxy.Store(&proxyReaderBox{r: r})
}

// maxProxyTransports bounds the per-proxy transport cache. OAuth traffic is
// light, so a handful of distinct proxies is the realistic ceiling; the cache
// is simply reset when it fills.
const maxProxyTransports = 16

// proxyTransport routes each OAuth request through the proxy chosen for it:
// the account's proxy from the request context (set by the dispatcher before
// a refresh), else the global outbound proxy, else the environment proxy.
// Relay-bound accounts send token exchanges through their relay too, so a
// refresh leaves from the same egress as the API calls it authorizes.
type proxyTransport struct {
	direct *http.Transport

	mu      sync.Mutex
	proxied map[string]http.RoundTripper
}

func newProxyTransport() *proxyTransport {
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = http.ProxyFromEnvironment
	return &proxyTransport{direct: direct, proxied: map[string]http.RoundTripper{}}
}

func (t *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if creds, ok := core.ProxyFromContext(req.Context()); ok && creds.ProxyURL == "" && creds.RelayURL != "" {
		relay, err := url.Parse(creds.RelayURL)
		if err != nil || relay.Host == "" {
			if req.Body != nil {
				req.Body.Close()
			}
			return nil, errors.New("invalid relay URL")
		}
		out := req.Clone(req.Context())
		httputil.RelayRequest(out, relay)
		return t.direct.RoundTrip(out)
	}
	proxyURL, noProxy := requestProxy(req)
	if proxyURL == "" {
		return t.direct.RoundTrip(req)
	}
	return t.transportFor(proxyURL, noProxy).RoundTrip(req)
}

func requestProxy(req *http.Request) (proxyURL, noProxy string) {
	if creds, ok := core.ProxyFromContext(req.Context()); ok && creds.ProxyURL != "" {
		return creds.ProxyURL, creds.NoProxy
	}
	if box := globalProxy.Load(); box != nil {
		return box.r.ProxyURL(), box.r.NoProxy()
	}
	return "", ""
}

func (t *proxyTransport) transportFor(proxyURL, noProxy string) http.RoundTripper {
	key := proxyURL + "|" + noProxy
	t.mu.Lock()
	defer t.mu.Unlock()
	if rt, ok := t.proxied[key]; ok {
		return rt
	}
	if len(t.proxied) >= maxProxyTransports {
		for _, rt := range t.proxied {
			if tr, ok := rt.(*http.Transport); ok {
				tr.CloseIdleConnections()
			}
		}
		t.proxied = map[string]http.RoundTripper{}
	}
	tr := t.direct.Clone()
	if u, err := url.Parse(proxyURL); err == nil && u.Host != "" {
		tr.Proxy = httputil.ProxyFunc(u, noProxy)
	} else {
		// Fail closed rather than leaking the real egress IP.
		tr.Proxy = func(*http.Request) (*url.URL, error) { return nil, errors.New("invalid proxy URL") }
	}
	t.proxied[key] = tr
	return tr
}
