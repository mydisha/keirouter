package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

var testInfo = Implementation{Name: "KeiRouter", Version: "test"}

func TestNegotiate_ModernServer(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{modern: true, tools: []string{toolWeather}}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)

	v, err := c.Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.NoError(t, err)
	require.Equal(t, VersionModern, v)

	got := f.recorded()
	require.Len(t, got, 1)
	require.Equal(t, "server/discover", got[0].Body.Method)
	require.Equal(t, VersionModern, got[0].Header.Get(HeaderProtocolVersion))
	require.Equal(t, "server/discover", got[0].Header.Get(HeaderMethod))
	require.Contains(t, string(got[0].Body.Params), MetaClientInfo)
}

func TestNegotiate_LegacyServerFallsBackToInitialize(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{legacy: true, tools: []string{toolWeather}}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)

	v, err := c.Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.NoError(t, err)
	require.Equal(t, VersionLegacyLatest, v)

	tools, err := c.ListTools(context.Background(), Target{Key: "s1", URL: srv.URL, Version: v})
	require.NoError(t, err)
	require.Len(t, tools, 1)

	got := f.recorded()
	methods := []string{}
	for _, r := range got {
		methods = append(methods, r.Body.Method)
	}
	require.Equal(t, []string{"server/discover", "initialize", "notifications/initialized", "tools/list"}, methods)
	last := got[len(got)-1]
	require.Equal(t, "sess-1", last.Header.Get(HeaderSessionID))
	require.Equal(t, VersionLegacyLatest, last.Header.Get(HeaderProtocolVersion))
	require.NotContains(t, string(last.Body.Params), "io.modelcontextprotocol/", "legacy requests carry no modern _meta keys")
}

func TestNegotiate_AuthFailureDoesNotFallBack(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{legacy: true, requireAuth: "Bearer right"}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)

	_, err := c.Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL, Headers: http.Header{"Authorization": {"Bearer wrong"}}})
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusUnauthorized, he.Status)
	require.Len(t, f.recorded(), 1, "no initialize after a 401")
}

func TestNegotiate_UnsupportedVersionErrorPicksModern(t *testing.T) {
	allowPrivate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"unsupported","data":{"supported":["2026-07-28"]}}}`))
	}))
	defer srv.Close()
	v, err := NewClient(testInfo, nil).Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.NoError(t, err)
	require.Equal(t, VersionModern, v)
}

func TestNegotiate_ModernErrorWithoutCommonVersionFails(t *testing.T) {
	allowPrivate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"unsupported","data":{"supported":["2030-01-01"]}}}`))
	}))
	defer srv.Close()
	_, err := NewClient(testInfo, nil).Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.ErrorContains(t, err, "2030-01-01")
}

func TestCall_ModernSendsHeadersAndMeta(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{modern: true}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)
	tgt := Target{Key: "s1", URL: srv.URL, Version: VersionModern, Headers: http.Header{"Authorization": {"Bearer k"}}}

	params := json.RawMessage(`{"name":"héllo","arguments":{"city":"Jakarta","nested":{"a":[1,2]}},"_meta":{"progressToken":"p1","io.modelcontextprotocol/protocolVersion":"1999-01-01"}}`)
	resp, err := c.Call(context.Background(), tgt, "tools/call", "héllo", params, http.Header{"Mcp-Param-Region": {"us"}})
	require.NoError(t, err)
	require.JSONEq(t, `{"content":[{"type":"text","text":"ok"}],"isError":false}`, string(resp.Result))

	got := f.recorded()[0]
	require.Equal(t, "Bearer k", got.Header.Get("Authorization"))
	require.Equal(t, "us", got.Header.Get("Mcp-Param-Region"))
	require.Equal(t, EncodeHeaderValue("héllo"), got.Header.Get(HeaderName))
	var p struct {
		Arguments json.RawMessage            `json:"arguments"`
		Meta      map[string]json.RawMessage `json:"_meta"`
	}
	require.NoError(t, json.Unmarshal(got.Body.Params, &p))
	require.JSONEq(t, `{"city":"Jakarta","nested":{"a":[1,2]}}`, string(p.Arguments))
	require.JSONEq(t, `"p1"`, string(p.Meta["progressToken"]))
	require.JSONEq(t, `"2026-07-28"`, string(p.Meta[MetaProtocolVersion]), "the client's reserved keys are replaced by the gateway's")
}

func TestCall_SSEResponseSkipsNotifications(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{modern: true, sse: true, callResult: `{"content":[{"type":"text","text":"from sse"}]}`}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)
	resp, err := c.Call(context.Background(), Target{Key: "s1", URL: srv.URL, Version: VersionModern}, "tools/call", "x", json.RawMessage(`{"name":"x"}`), nil)
	require.NoError(t, err)
	require.Contains(t, string(resp.Result), "from sse")
}

func TestCall_LegacySessionExpiredReinitialisesOnce(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{legacy: true}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)
	tgt := Target{Key: "s1", URL: srv.URL, Version: VersionLegacyLatest}

	_, err := c.Call(context.Background(), tgt, "tools/call", "x", json.RawMessage(`{"name":"x"}`), nil)
	require.NoError(t, err)
	f.dropSessions()
	_, err = c.Call(context.Background(), tgt, "tools/call", "x", json.RawMessage(`{"name":"x"}`), nil)
	require.NoError(t, err)
	f.mu.Lock()
	require.Equal(t, 2, f.initCount)
	f.mu.Unlock()
}

func TestCall_UpstreamIDsAreUniqueAcrossCallers(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{legacy: true}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)
	tgt := Target{Key: "s1", URL: srv.URL, Version: VersionLegacyLatest}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Call(context.Background(), tgt, "tools/call", "x", json.RawMessage(`{"name":"x"}`), nil)
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, r := range f.recorded() {
		if r.Body.Method != "tools/call" {
			continue
		}
		id := string(r.Body.ID)
		require.False(t, seen[id], "id %s reused within one session", id)
		seen[id] = true
	}
	require.Len(t, seen, 20)
}

func TestListTools_FollowsPagination(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{modern: true, tools: []string{toolWeather, toolRegion, toolBadHeader}, pageSize: 2}
	srv := newFake(t, f)
	c := NewClient(testInfo, nil)
	tools, err := c.ListTools(context.Background(), Target{Key: "s1", URL: srv.URL, Version: VersionModern})
	require.NoError(t, err)
	require.Len(t, tools, 3)
}

func TestTransport_BlocksPrivateAddressAtDialTime(t *testing.T) {
	srv := newFake(t, &fakeServer{modern: true})
	c := NewClient(testInfo, nil)
	_, err := c.Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "loopback") || strings.Contains(err.Error(), "blocked"), err.Error())
}

func TestWithMeta(t *testing.T) {
	out, err := withMeta(nil, false, testInfo, "")
	require.NoError(t, err)
	require.Nil(t, out)

	out, err = withMeta(json.RawMessage(`{"_meta":{"io.modelcontextprotocol/x":1}}`), false, testInfo, "")
	require.NoError(t, err)
	require.Nil(t, out, "a _meta holding only reserved keys disappears for legacy upstreams")

	_, err = withMeta(json.RawMessage(`[1]`), true, testInfo, "")
	require.Error(t, err)
}

func TestHTTPErrorMessages(t *testing.T) {
	require.Contains(t, (&HTTPError{Status: 401}).Error(), "credentials")
	require.Contains(t, (&HTTPError{Status: 500, Body: "boom"}).Error(), "boom")
	require.Contains(t, (&HTTPError{Status: 400, RPC: &RPCError{Message: "nope"}}).Error(), "nope")
}

// Regression: a dual-era server that answers server/discover but only
// supports initialize-era versions must be used through initialize.
func TestNegotiate_DiscoverWithOnlyLegacyVersionsFallsBack(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{legacy: true, tools: []string{toolWeather}}
	newFake(t, f) // initialises the fake's state
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderMethod) == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["2025-11-25","2025-06-18"]}}`))
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	v, err := NewClient(testInfo, nil).Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.NoError(t, err)
	require.Equal(t, VersionLegacyLatest, v)
}

// Regression: an UnsupportedProtocolVersion error that lists only legacy
// versions means "use initialize", not "give up".
func TestNegotiate_UnsupportedVersionListingLegacyFallsBack(t *testing.T) {
	allowPrivate(t)
	f := &fakeServer{legacy: true, tools: []string{toolWeather}}
	newFake(t, f)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderMethod) == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"unsupported","data":{"supported":["2025-06-18"]}}}`))
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	v, err := NewClient(testInfo, nil).Negotiate(context.Background(), Target{Key: "s1", URL: srv.URL})
	require.NoError(t, err)
	require.Equal(t, VersionLegacyLatest, v)
}
