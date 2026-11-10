package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// serveEnv is a Manager with one synced upstream and one API key.
type serveEnv struct {
	m     *Manager
	db    *store.DB
	f     *fakeServer
	srv   store.MCPServer
	keyID string
}

func newServeEnv(t *testing.T, f *fakeServer, opts ...func(*Options)) *serveEnv {
	t.Helper()
	allowPrivate(t)
	db := newTestDB(t)
	o := Options{Info: testInfo}
	for _, fn := range opts {
		fn(&o)
	}
	m, err := NewManager(context.Background(), db, newTestSealer(t), o)
	require.NoError(t, err)
	up := newFake(t, f)
	srv := addServer(t, m, "wx", up.URL, Secret{Secret: "upstream-token"}, AuthBearer)
	_, err = m.Syncer.SyncServer(context.Background(), srv.ID)
	require.NoError(t, err)
	require.NoError(t, db.APIKeys().Create(context.Background(), store.APIKey{
		ID: "key-1", TenantID: store.DefaultTenantID, Name: "k", KeyHash: "x", LookupHash: "l", Display: "kr_", CreatedAt: time.Now(),
	}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go m.calls.run(ctx)
	return &serveEnv{m: m, db: db, f: f, srv: srv, keyID: "key-1"}
}

type rpcReply struct {
	Status int
	Header http.Header
	Body   struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	Raw string
}

// modernReq builds a 2026-07-28 request with matching headers.
func modernReq(id int, method string, params map[string]any) (*http.Request, string) {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{MetaProtocolVersion: VersionModern, MetaClientInfo: map[string]string{"name": "test-client", "version": "1"}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	r.Header.Set(HeaderProtocolVersion, VersionModern)
	r.Header.Set(HeaderMethod, method)
	if n, ok := params["name"].(string); ok {
		r.Header.Set(HeaderName, EncodeHeaderValue(n))
	}
	return r, string(body)
}

func legacyReq(id int, method string, params map[string]any) *http.Request {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != 0 {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	body, _ := json.Marshal(m)
	return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
}

func (e *serveEnv) do(t *testing.T, r *http.Request, scope string) rpcReply {
	t.Helper()
	w := httptest.NewRecorder()
	e.m.Serve(w, r, Caller{KeyID: e.keyID, RequestID: "req-1", Client: "ua"}, scope)
	var out rpcReply
	out.Status = w.Code
	out.Header = w.Header()
	out.Raw = w.Body.String()
	if w.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out.Body), out.Raw)
	}
	return out
}

func toolNames(t *testing.T, result json.RawMessage) []string {
	t.Helper()
	var r struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(result, &r))
	names := []string{}
	for _, tl := range r.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func TestServe_ModernListAndCall(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, requireAuth: "Bearer upstream-token", tools: []string{toolWeather, toolRegion}})

	req, _ := modernReq(1, "tools/list", nil)
	rep := e.do(t, req, "")
	require.Equal(t, http.StatusOK, rep.Status, rep.Raw)
	require.Equal(t, []string{"wx__get_weather", "wx__run_sql"}, toolNames(t, rep.Body.Result))
	require.Contains(t, string(rep.Body.Result), `"cacheScope":"private"`)
	require.Contains(t, string(rep.Body.Result), `"resultType":"complete"`)

	req, _ = modernReq(7, "tools/call", map[string]any{"name": "wx__get_weather", "arguments": map[string]any{"city": "Bandung"}})
	req.Header.Set("Authorization", "Bearer kr-client-key")
	rep = e.do(t, req, "")
	require.Equal(t, http.StatusOK, rep.Status, rep.Raw)
	require.JSONEq(t, `7`, string(rep.Body.ID), "the client's id comes back, not the upstream one")
	require.Contains(t, string(rep.Body.Result), `"text":"ok"`)

	calls := e.f.recorded()
	last := calls[len(calls)-1]
	require.Equal(t, "tools/call", last.Body.Method)
	require.Equal(t, "Bearer upstream-token", last.Header.Get("Authorization"), "the client's key never reaches the upstream")
	require.Equal(t, "get_weather", last.Header.Get(HeaderName))
	require.NotEqual(t, "7", string(last.Body.ID))
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal(last.Body.Params, &p))
	require.Equal(t, "get_weather", p.Name)
	require.JSONEq(t, `{"city":"Bandung"}`, string(p.Arguments))

	require.Eventually(t, func() bool {
		logs, _, err := e.m.Repo.ListCallLogs(context.Background(), store.MCPLogFilter{TenantID: store.DefaultTenantID})
		return err == nil && len(logs) == 1 && logs[0].Status == StatusSuccess && logs[0].Client == "test-client" && logs[0].Tool == "get_weather"
	}, 3*time.Second, 50*time.Millisecond)
}

func TestServe_ScopedEndpointUsesBareNames(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}})
	rep := e.do(t, legacyReq(1, "tools/list", nil), "wx")
	require.Equal(t, []string{"get_weather"}, toolNames(t, rep.Body.Result))
	rep = e.do(t, legacyReq(2, "tools/call", map[string]any{"name": "get_weather"}), "wx")
	require.Contains(t, string(rep.Body.Result), `"text":"ok"`)

	rep = e.do(t, legacyReq(3, "tools/list", nil), "nope")
	require.Equal(t, http.StatusNotFound, rep.Status)
}

func TestServe_LegacyClient(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}})
	rep := e.do(t, legacyReq(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}), "")
	require.Equal(t, http.StatusOK, rep.Status)
	require.Empty(t, rep.Header.Get(HeaderSessionID), "the gateway never mints sessions")
	require.Contains(t, string(rep.Body.Result), `"protocolVersion":"2025-06-18"`)

	rep = e.do(t, legacyReq(1, "initialize", map[string]any{"protocolVersion": "2024-01-01"}), "")
	require.Contains(t, string(rep.Body.Result), `"protocolVersion":"`+VersionLegacyLatest+`"`)

	rep = e.do(t, legacyReq(0, "notifications/initialized", nil), "")
	require.Equal(t, http.StatusAccepted, rep.Status)

	rep = e.do(t, legacyReq(2, "ping", nil), "")
	require.JSONEq(t, `{}`, string(rep.Body.Result))

	rep = e.do(t, legacyReq(3, "tools/list", nil), "")
	require.Equal(t, []string{"wx__get_weather"}, toolNames(t, rep.Body.Result))
	require.NotContains(t, string(rep.Body.Result), "cacheScope")

	rep = e.do(t, legacyReq(4, "resources/list", nil), "")
	require.Equal(t, http.StatusOK, rep.Status, "legacy clients get method-not-found inside a 200")
	require.Equal(t, CodeMethodNotFound, rep.Body.Error.Code)
}

func TestServe_ModernProtocolErrors(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}})

	req, _ := modernReq(1, "tools/list", nil)
	req.Header.Del(HeaderMethod)
	rep := e.do(t, req, "")
	require.Equal(t, http.StatusBadRequest, rep.Status)
	require.Equal(t, CodeHeaderMismatch, rep.Body.Error.Code)

	req, _ = modernReq(1, "tools/call", map[string]any{"name": "wx__get_weather"})
	req.Header.Set(HeaderName, "wx__other")
	rep = e.do(t, req, "")
	require.Equal(t, CodeHeaderMismatch, rep.Body.Error.Code)

	req, _ = modernReq(1, "tools/call", map[string]any{"name": "wx__gét"})
	require.True(t, strings.HasPrefix(req.Header.Get(HeaderName), "=?base64?"))
	rep = e.do(t, req, "")
	require.Equal(t, CodeInvalidParams, rep.Body.Error.Code, "a base64 Mcp-Name is decoded before comparing")

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}`
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set(HeaderProtocolVersion, "2099-01-01")
	req.Header.Set(HeaderMethod, "tools/list")
	rep = e.do(t, req, "")
	require.Equal(t, http.StatusBadRequest, rep.Status)
	require.Equal(t, CodeUnsupportedProtocolVersion, rep.Body.Error.Code)
	require.Contains(t, string(rep.Body.Error.Data), VersionModern)

	req, _ = modernReq(1, "resources/list", nil)
	rep = e.do(t, req, "")
	require.Equal(t, http.StatusNotFound, rep.Status)
	require.Equal(t, CodeMethodNotFound, rep.Body.Error.Code)

	req, _ = modernReq(1, "ping", nil)
	require.Equal(t, http.StatusNotFound, e.do(t, req, "").Status, "ping is gone in the modern era")

	req = legacyReq(1, "tools/list", nil)
	req.Header.Set(HeaderProtocolVersion, VersionModern)
	require.Equal(t, http.StatusBadRequest, e.do(t, req, "").Status, "modern header without _meta")

	req, _ = modernReq(1, "server/discover", nil)
	rep = e.do(t, req, "")
	require.Contains(t, string(rep.Body.Result), `"supportedVersions":["2026-07-28"]`)

	for name, raw := range map[string]string{
		"batch":     `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`,
		"bad json":  `{"jsonrpc":`,
		"no method": `{"jsonrpc":"2.0","id":1}`,
		"jsonrpc 1": `{"jsonrpc":"1.0","id":1,"method":"tools/list"}`,
	} {
		rep := e.do(t, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(raw)), "")
		require.Equal(t, http.StatusBadRequest, rep.Status, name)
		require.NotNil(t, rep.Body.Error, name)
	}

	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wx__get_weather","arguments":{"x":"` + strings.Repeat("a", MaxRequestBytes) + `"}}}`
	rep = e.do(t, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(big)), "")
	require.Equal(t, http.StatusRequestEntityTooLarge, rep.Status)
}

func TestServe_AccessModes(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather, toolRegion}})
	ctx := context.Background()
	call := func(name string) rpcReply {
		return e.do(t, legacyReq(1, "tools/call", map[string]any{"name": name}), "")
	}

	require.NoError(t, e.m.Repo.SetKeyAccess(ctx, e.keyID, store.MCPKeyAccess{Mode: store.MCPModeNone}))
	e.m.InvalidateAccess(e.keyID)
	rep := e.do(t, legacyReq(1, "tools/list", nil), "")
	require.Empty(t, toolNames(t, rep.Body.Result))
	denied := call("wx__get_weather")
	missing := call("wx__does_not_exist")
	require.Equal(t, CodeInvalidParams, denied.Body.Error.Code)
	require.Equal(t, strings.Replace(missing.Body.Error.Message, "does_not_exist", "get_weather", 1), denied.Body.Error.Message,
		"a forbidden tool and a missing tool look the same")

	require.NoError(t, e.m.Repo.SetKeyAccess(ctx, e.keyID, store.MCPKeyAccess{Mode: store.MCPModeSelected, Grants: []store.MCPKeyGrant{
		{ServerID: e.srv.ID, ToolMode: store.MCPModeSelected, Tools: []string{"run_sql"}},
	}}))
	e.m.InvalidateAccess(e.keyID)
	rep = e.do(t, legacyReq(1, "tools/list", nil), "")
	require.Equal(t, []string{"wx__run_sql"}, toolNames(t, rep.Body.Result))
	require.NotNil(t, call("wx__get_weather").Body.Error)
	require.Nil(t, call("wx__run_sql").Body.Error)

	// Deleting the only granted server leaves a selected key with nothing,
	// never with every server.
	require.NoError(t, e.m.Repo.DeleteServer(ctx, e.srv.ID))
	f2 := &fakeServer{modern: true, tools: []string{toolWeather}}
	up2 := newFake(t, f2)
	other := addServer(t, e.m, "other", up2.URL, Secret{}, AuthNone)
	_, err := e.m.Syncer.SyncServer(ctx, other.ID)
	require.NoError(t, err)
	e.m.InvalidateAccess(e.keyID)
	acc, err := e.m.Repo.GetKeyAccess(ctx, e.keyID)
	require.NoError(t, err)
	require.Equal(t, store.MCPModeSelected, acc.Mode)
	require.Empty(t, acc.Grants)
	rep = e.do(t, legacyReq(1, "tools/list", nil), "")
	require.Empty(t, toolNames(t, rep.Body.Result))

	require.Eventually(t, func() bool {
		logs, _, _ := e.m.Repo.ListCallLogs(ctx, store.MCPLogFilter{TenantID: store.DefaultTenantID, Status: StatusDenied})
		return len(logs) >= 2
	}, 3*time.Second, 50*time.Millisecond)
}

func TestServe_RateLimitBeforeUpstream(t *testing.T) {
	lim := limits.NewMemory(limits.MemoryConfig{Enabled: true})
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}}, func(o *Options) {
		o.Limiter = lim
		o.CallLimits = limits.EffectiveLimits{RPM: 2}
	})
	call := func() rpcReply {
		return e.do(t, legacyReq(1, "tools/call", map[string]any{"name": "wx__get_weather"}), "")
	}
	require.Equal(t, http.StatusOK, call().Status)
	require.Equal(t, http.StatusOK, call().Status)
	before := len(e.f.recorded())
	rep := call()
	require.Equal(t, http.StatusTooManyRequests, rep.Status)
	require.NotEmpty(t, rep.Header.Get("Retry-After"))
	require.Equal(t, before, len(e.f.recorded()), "a limited call never reaches the upstream")

	// The LLM bucket of the same key is untouched.
	_, dec, err := lim.Acquire(context.Background(), limits.Request{APIKeyID: e.keyID, Limits: limits.EffectiveLimits{RPM: 2}})
	require.NoError(t, err)
	require.True(t, dec.Allowed)
}

func TestServe_ConcurrencyReleasedAfterFailure(t *testing.T) {
	lim := limits.NewMemory(limits.MemoryConfig{Enabled: true})
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}}, func(o *Options) {
		o.Limiter = lim
		o.CallLimits = limits.EffectiveLimits{Concurrency: 1}
	})
	e.f.requireAuth = "Bearer rotated" // every call now fails upstream
	for i := 0; i < 3; i++ {
		rep := e.do(t, legacyReq(1, "tools/call", map[string]any{"name": "wx__get_weather"}), "")
		require.Equal(t, http.StatusOK, rep.Status, "attempt %d: %s", i, rep.Raw)
		require.Contains(t, string(rep.Body.Result), `"isError":true`)
	}
}

func TestServe_BreakerOpensAfterFailures(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}})
	// Point the server at a closed port: connection refused.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	snap := e.m.Registry.Snapshot()
	srv := snap.BySlug["wx"]
	srv.targetOnce.Do(func() {})
	srv.target = Target{Key: srv.ID, URL: deadURL, Version: VersionModern}

	for i := 0; i < breakerThreshold; i++ {
		rep := e.do(t, legacyReq(1, "tools/call", map[string]any{"name": "wx__get_weather"}), "")
		require.Contains(t, string(rep.Body.Result), "could not be reached")
	}
	rep := e.do(t, legacyReq(1, "tools/call", map[string]any{"name": "wx__get_weather"}), "")
	require.Contains(t, string(rep.Body.Result), "paused calls")
}

func TestServe_ParamHeadersAndForwarding(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolRegion}})
	srv, err := e.m.Repo.GetServer(context.Background(), e.srv.ID)
	require.NoError(t, err)
	srv.ForwardHeaders = []string{"X-Request-Source"}
	require.NoError(t, e.m.Repo.UpdateServer(context.Background(), srv))
	e.m.Changed(context.Background())

	args := map[string]any{"region": "eu-west", "query": "select 1"}
	req, _ := modernReq(1, "tools/call", map[string]any{"name": "wx__run_sql", "arguments": args})
	rep := e.do(t, req, "")
	require.Equal(t, http.StatusBadRequest, rep.Status, "a modern client must send Mcp-Param-Region")
	require.Equal(t, CodeHeaderMismatch, rep.Body.Error.Code)

	req, _ = modernReq(1, "tools/call", map[string]any{"name": "wx__run_sql", "arguments": args})
	req.Header.Set("Mcp-Param-Region", "us-east")
	require.Equal(t, http.StatusBadRequest, e.do(t, req, "").Status)

	req, _ = modernReq(1, "tools/call", map[string]any{"name": "wx__run_sql", "arguments": args})
	req.Header.Set("Mcp-Param-Region", "eu-west")
	req.Header.Set("X-Request-Source", "ci")
	req.Header.Set("X-Not-Listed", "secret")
	req.Header.Set("Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	rep = e.do(t, req, "")
	require.Equal(t, http.StatusOK, rep.Status, rep.Raw)

	up := e.f.recorded()
	last := up[len(up)-1].Header
	require.Equal(t, "eu-west", last.Get("Mcp-Param-Region"), "the gateway mirrors the header to a modern upstream")
	require.Equal(t, "ci", last.Get("X-Request-Source"))
	require.Empty(t, last.Get("X-Not-Listed"))
	require.NotEmpty(t, last.Get("Traceparent"))

	// A legacy client does not send Mcp-Param headers; the gateway still
	// adds them for the modern upstream.
	rep = e.do(t, legacyReq(2, "tools/call", map[string]any{"name": "wx__run_sql", "arguments": args}), "")
	require.Equal(t, http.StatusOK, rep.Status, rep.Raw)
	up = e.f.recorded()
	require.Equal(t, "eu-west", up[len(up)-1].Header.Get("Mcp-Param-Region"))
}

func TestServe_ResultShapeAcrossEras(t *testing.T) {
	e := newServeEnv(t, &fakeServer{legacy: true, tools: []string{toolWeather}, callResult: `{"content":[]}`})
	req, _ := modernReq(1, "tools/call", map[string]any{"name": "wx__get_weather"})
	rep := e.do(t, req, "")
	require.JSONEq(t, `{"resultType":"complete","content":[]}`, string(rep.Body.Result), "modern clients always get resultType")

	e2 := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather},
		callResult: `{"resultType":"input_required","inputRequests":{"a":{"method":"elicitation/create","params":{}}}}`})
	rep = e2.do(t, legacyReq(1, "tools/call", map[string]any{"name": "wx__get_weather"}), "")
	require.Contains(t, string(rep.Body.Result), `"isError":true`)
	require.Contains(t, string(rep.Body.Result), VersionModern)

	req, _ = modernReq(1, "tools/call", map[string]any{"name": "wx__get_weather"})
	rep = e2.do(t, req, "")
	require.Contains(t, string(rep.Body.Result), `"input_required"`, "modern clients get the multi round-trip result as is")
}

func TestServe_UpstreamRPCErrorPassesThrough(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolExplode}})
	rep := e.do(t, legacyReq(9, "tools/call", map[string]any{"name": "wx__explode"}), "")
	require.Equal(t, http.StatusOK, rep.Status)
	require.JSONEq(t, `9`, string(rep.Body.ID))
	require.Equal(t, CodeInvalidParams, rep.Body.Error.Code)
	require.Equal(t, "bad arguments for explode", rep.Body.Error.Message)
}

func TestInvoke_Dashboard(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}})
	res, err := e.m.Invoke(context.Background(), e.srv.ID, "get_weather", json.RawMessage(`{"city":"x"}`))
	require.NoError(t, err)
	require.Equal(t, StatusSuccess, res.Status)
	_, err = e.m.Invoke(context.Background(), e.srv.ID, "missing", nil)
	require.Error(t, err)
	require.Eventually(t, func() bool {
		logs, _, _ := e.m.Repo.ListCallLogs(context.Background(), store.MCPLogFilter{TenantID: store.DefaultTenantID, APIKeyID: "dashboard"})
		return len(logs) == 1
	}, 3*time.Second, 50*time.Millisecond)
}

func TestEnsureResultType(t *testing.T) {
	require.Equal(t, `{"resultType":"complete","a":1}`, string(ensureResultType(json.RawMessage(`{"a":1}`))))
	require.Equal(t, `{"resultType":"complete"}`, string(ensureResultType(json.RawMessage(`{}`))))
	require.Equal(t, `{"resultType":"x"}`, string(ensureResultType(json.RawMessage(`{"resultType":"x"}`))))
	require.Equal(t, `[1]`, string(ensureResultType(json.RawMessage(`[1]`))))
}

type fakeAuditor struct {
	mu     sync.Mutex
	events []audit.Event
}

func (f *fakeAuditor) Record(ev audit.Event) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
}

func (f *fakeAuditor) all() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Event(nil), f.events...)
}

func TestServe_AuditsEveryToolCall(t *testing.T) {
	aud := &fakeAuditor{}
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather}, callResult: `{"content":[{"type":"text","text":"city not found"}],"isError":true}`}, func(o *Options) {
		o.Auditor = aud
	})
	caller := Caller{KeyID: e.keyID, RequestID: "req-9", ClientIP: "10.0.0.7", UserAgent: "claude-code/2", Route: "/mcp", TraceID: "t1", SpanID: "s1"}

	req, _ := modernReq(3, "tools/call", map[string]any{"name": "wx__get_weather", "arguments": map[string]any{"city": "secret-place"}})
	w := httptest.NewRecorder()
	out := e.m.Serve(w, req, caller, "")
	require.Equal(t, http.StatusOK, out.HTTPStatus)
	require.Equal(t, "tools/call", out.Method)
	require.Equal(t, "wx__get_weather", out.Tool)
	require.Equal(t, "wx", out.Server)
	require.Equal(t, StatusToolError, out.CallStatus)
	require.Equal(t, "city not found", out.Error, "the tool's own message explains the failure")
	require.True(t, out.Modern)

	evs := aud.all()
	require.Len(t, evs, 1)
	ev := evs[0]
	require.Equal(t, "mcp.tool.call", ev.Action)
	require.Equal(t, audit.OutcomeFailure, ev.Outcome)
	require.Equal(t, audit.ActorAPIKey, ev.ActorType)
	require.Equal(t, e.keyID, ev.ActorID)
	require.Equal(t, "wx__get_weather", ev.ResourceID)
	require.Equal(t, "10.0.0.7", ev.ClientIP)
	require.Equal(t, "req-9", ev.RequestID)
	require.Equal(t, "t1", ev.TraceID)
	require.Equal(t, "tool_error", ev.Details["status"])
	require.Equal(t, VersionModern, ev.Details["protocol_version"])
	for k, v := range ev.Details {
		require.NotContains(t, v, "secret-place", "arguments never reach the audit log (%s)", k)
	}

	// A denied call is audited as denied.
	require.NoError(t, e.m.Repo.SetKeyAccess(context.Background(), e.keyID, store.MCPKeyAccess{Mode: store.MCPModeNone}))
	e.m.InvalidateAccess(e.keyID)
	out = e.m.Serve(httptest.NewRecorder(), legacyReq(4, "tools/call", map[string]any{"name": "wx__get_weather"}), caller, "")
	require.Equal(t, StatusDenied, out.CallStatus)
	evs = aud.all()
	require.Len(t, evs, 2)
	require.Equal(t, audit.OutcomeDenied, evs[1].Outcome)

	// Calls to tools that do not exist and non-call methods are not audited.
	e.m.Serve(httptest.NewRecorder(), legacyReq(5, "tools/call", map[string]any{"name": "nope__x"}), caller, "")
	out = e.m.Serve(httptest.NewRecorder(), legacyReq(6, "tools/list", nil), caller, "")
	require.Equal(t, 0, out.ToolCount)
	require.Len(t, aud.all(), 2)
}

func TestServe_OutcomeForListAndRejects(t *testing.T) {
	e := newServeEnv(t, &fakeServer{modern: true, tools: []string{toolWeather, toolRegion}})
	out := e.m.Serve(httptest.NewRecorder(), legacyReq(1, "tools/list", nil), Caller{KeyID: e.keyID}, "")
	require.Equal(t, 2, out.ToolCount)
	require.False(t, out.Modern)

	req, _ := modernReq(1, "tools/list", nil)
	req.Header.Del(HeaderMethod)
	out = e.m.Serve(httptest.NewRecorder(), req, Caller{KeyID: e.keyID}, "")
	require.Equal(t, http.StatusBadRequest, out.HTTPStatus)
	require.NotEmpty(t, out.Error)
}
