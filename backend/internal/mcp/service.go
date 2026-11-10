package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/fastjson"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// MaxRequestBytes caps one inbound JSON-RPC message.
const MaxRequestBytes = 4 << 20

// listTTLMS is the freshness hint on tools/list results.
const listTTLMS = 60000

// Caller is the authenticated client of one /mcp request.
type Caller struct {
	KeyID     string
	RequestID string
	Client    string
	TraceID   string
	SpanID    string
	ClientIP  string
	UserAgent string
	// Route is the matched route, "/mcp" or "/mcp/{server}", for the audit log.
	Route string
}

// Outcome summarises one /mcp request for the console.
type Outcome struct {
	HTTPStatus int
	Method     string
	Modern     bool
	// Version is the protocol version the client used, when known.
	Version string
	// Tool is the name the client called; Server the slug it resolved to.
	Tool   string
	Server string
	// CallStatus is the tool-call log status (success, denied, ...).
	CallStatus string
	Error      string
	ToolCount  int
	UpstreamMS int64
	// Guardrail says what a guardrail did to the call, such as "pii masked".
	Guardrail string
	// Progress counts the progress notifications relayed to the client.
	Progress int
}

// statusWriter remembers the status code written to the client.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the Flusher underneath.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type inbound struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (r *inbound) IsNotification() bool { return len(r.ID) == 0 || string(r.ID) == "null" }

type inboundParams struct {
	Name            string  `json:"name"`
	Cursor          *string `json:"cursor"`
	ProtocolVersion string  `json:"protocolVersion"`
	Meta            struct {
		ProtocolVersion string          `json:"io.modelcontextprotocol/protocolVersion"`
		ClientInfo      *Implementation `json:"io.modelcontextprotocol/clientInfo"`
	} `json:"_meta"`
	ClientInfo *Implementation `json:"clientInfo"`
}

// exchange is one request being served.
type exchange struct {
	w      http.ResponseWriter
	r      *http.Request
	caller Caller
	scope  string // server slug for /mcp/{slug}; empty for /mcp
	req    inbound
	params inboundParams
	modern bool
	size   int
	out    *Outcome
}

// Serve answers one Streamable HTTP POST to /mcp or /mcp/{scope}. The
// caller has authenticated the key and checked the Origin header. The
// returned Outcome describes what happened, for the console.
func (m *Manager) Serve(rw http.ResponseWriter, r *http.Request, caller Caller, scope string) Outcome {
	sw := &statusWriter{ResponseWriter: rw}
	out := Outcome{Server: scope}
	m.serve(sw, r, caller, scope, &out)
	out.HTTPStatus = sw.status
	if out.HTTPStatus == 0 {
		out.HTTPStatus = http.StatusOK
	}
	return out
}

func (m *Manager) serve(w http.ResponseWriter, r *http.Request, caller Caller, scope string, out *Outcome) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		writeRPCError(w, http.StatusRequestEntityTooLarge, nil, CodeInvalidRequest, "request body is larger than 4 MiB")
		return
	}
	x := &exchange{w: w, r: r, caller: caller, scope: scope, size: len(body), out: out}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		writeRPCError(w, http.StatusBadRequest, nil, CodeInvalidRequest, "JSON-RPC batches are not supported")
		return
	}
	if err := fastjson.UnmarshalNoCopy(body, &x.req); err != nil {
		out.Error = "invalid JSON"
		writeRPCError(w, http.StatusBadRequest, nil, CodeParseError, "invalid JSON")
		return
	}
	out.Method = x.req.Method
	if x.req.JSONRPC != "2.0" || x.req.Method == "" {
		writeRPCError(w, http.StatusBadRequest, x.req.ID, CodeInvalidRequest, "not a JSON-RPC 2.0 request")
		return
	}
	if len(x.req.Params) > 0 && !bytes.Equal(bytes.TrimSpace(x.req.Params), []byte("null")) {
		if err := fastjson.UnmarshalNoCopy(x.req.Params, &x.params); err != nil {
			writeRPCError(w, http.StatusBadRequest, x.req.ID, CodeInvalidParams, "params must be an object")
			return
		}
	}
	if x.params.Meta.ClientInfo != nil && x.params.Meta.ClientInfo.Name != "" {
		x.caller.Client = x.params.Meta.ClientInfo.Name
	}
	if !m.checkEra(x) {
		out.Error = "protocol headers rejected"
		return
	}
	out.Modern = x.modern
	out.Version = x.params.Meta.ProtocolVersion
	if !x.modern && x.req.Method == "initialize" {
		out.Version = x.params.ProtocolVersion
	} else if !x.modern {
		out.Version = x.r.Header.Get(HeaderProtocolVersion)
	}
	if x.req.IsNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if x.scope != "" && m.Registry.Snapshot().BySlug[x.scope] == nil {
		writeRPCError(w, http.StatusNotFound, x.req.ID, CodeInvalidParams, "unknown MCP server "+strconv.Quote(x.scope))
		return
	}

	switch x.req.Method {
	case "server/discover":
		m.serveDiscover(x)
	case "initialize":
		m.serveInitialize(x)
	case "ping":
		if x.modern {
			x.methodNotFound()
			return
		}
		writeRPCResult(w, http.StatusOK, x.req.ID, []byte(`{}`))
	case "tools/list":
		m.serveListTools(x)
	case "tools/call":
		m.serveCallTool(x)
	default:
		x.methodNotFound()
	}
}

// checkEra decides the protocol era of a request and applies the modern
// era's header rules. It writes the error and returns false on failure.
func (m *Manager) checkEra(x *exchange) bool {
	h := x.r.Header
	pv := x.params.Meta.ProtocolVersion
	if pv == "" {
		if IsModern(h.Get(HeaderProtocolVersion)) && x.req.Method != "initialize" {
			writeRPCError(x.w, http.StatusBadRequest, x.req.ID, CodeHeaderMismatch,
				"MCP-Protocol-Version is "+VersionModern+" but the request has no _meta protocol version")
			return false
		}
		return true // legacy
	}
	x.modern = true
	if !IsModern(pv) {
		data, _ := json.Marshal(unsupportedVersionData{Supported: []string{VersionModern}, Requested: pv})
		writeRPCErrorData(x.w, http.StatusBadRequest, x.req.ID, CodeUnsupportedProtocolVersion, "Unsupported protocol version", data)
		return false
	}
	mismatch := func(msg string) bool {
		writeRPCError(x.w, http.StatusBadRequest, x.req.ID, CodeHeaderMismatch, "Header mismatch: "+msg)
		return false
	}
	if hv := h.Get(HeaderProtocolVersion); hv != pv {
		return mismatch("MCP-Protocol-Version header " + strconv.Quote(hv) + " does not match _meta " + strconv.Quote(pv))
	}
	if hm := h.Get(HeaderMethod); hm != x.req.Method {
		return mismatch("Mcp-Method header " + strconv.Quote(hm) + " does not match method " + strconv.Quote(x.req.Method))
	}
	if x.req.Method == "tools/call" {
		hn, ok := DecodeHeaderValue(h.Get(HeaderName))
		if !ok || h.Get(HeaderName) == "" || hn != x.params.Name {
			return mismatch("Mcp-Name header does not match params.name")
		}
	}
	return true
}

func (x *exchange) methodNotFound() {
	status := http.StatusOK
	if x.modern {
		status = http.StatusNotFound
	}
	writeRPCError(x.w, status, x.req.ID, CodeMethodNotFound, "method not found: "+x.req.Method)
}

func (m *Manager) serverInfoJSON() []byte {
	b, _ := json.Marshal(m.info)
	return b
}

func (m *Manager) serveDiscover(x *exchange) {
	var b bytes.Buffer
	b.WriteString(`{"resultType":"complete","supportedVersions":["` + VersionModern + `"],"capabilities":{"tools":{}},"_meta":{"` + MetaServerInfo + `":`)
	b.Write(m.serverInfoJSON())
	b.WriteString(`},"ttlMs":3600000,"cacheScope":"public"}`)
	writeRPCResult(x.w, http.StatusOK, x.req.ID, b.Bytes())
}

func (m *Manager) serveInitialize(x *exchange) {
	version := VersionLegacyLatest
	if IsLegacy(x.params.ProtocolVersion) {
		version = x.params.ProtocolVersion
	}
	var b bytes.Buffer
	b.WriteString(`{"protocolVersion":"` + version + `","capabilities":{"tools":{"listChanged":false}},"serverInfo":`)
	b.Write(m.serverInfoJSON())
	b.WriteString(`}`)
	writeRPCResult(x.w, http.StatusOK, x.req.ID, b.Bytes())
}

func (m *Manager) serveListTools(x *exchange) {
	if x.params.Cursor != nil && *x.params.Cursor != "" {
		writeRPCError(x.w, http.StatusOK, x.req.ID, CodeInvalidParams, "invalid cursor")
		return
	}
	access, err := m.access.get(x.r.Context(), x.caller.KeyID)
	if err != nil {
		writeRPCError(x.w, http.StatusInternalServerError, x.req.ID, CodeInternalError, "could not load access")
		return
	}
	buf := getBuffer()
	defer putBuffer(buf)
	buf.WriteString(`{"tools":[`)
	x.out.ToolCount = appendTools(buf, m.Registry.Snapshot(), access, x.scope)
	buf.WriteString(`]`)
	if x.modern {
		buf.WriteString(`,"resultType":"complete","ttlMs":` + strconv.Itoa(listTTLMS) + `,"cacheScope":"private","_meta":{"` + MetaServerInfo + `":`)
		buf.Write(m.serverInfoJSON())
		buf.WriteString(`}`)
	}
	buf.WriteString(`}`)
	writeRPCResult(x.w, http.StatusOK, x.req.ID, buf.Bytes())
}

// appendTools writes the comma-separated tool definitions a key may see and
// returns how many it wrote.
func appendTools(buf *bytes.Buffer, snap *Snapshot, access *Access, scope string) int {
	n := 0
	add := func(def []byte) {
		if n > 0 {
			buf.WriteByte(',')
		}
		buf.Write(def)
		n++
	}
	if scope != "" {
		srv := snap.BySlug[scope]
		if srv == nil || !access.CanUseServer(srv.ID) {
			return 0
		}
		if access.AllToolsOf(srv.ID) {
			buf.Write(srv.joinedBare)
			return len(srv.Sorted)
		}
		for _, t := range srv.Sorted {
			if access.CanCall(srv.ID, t.Name) {
				add(t.Bare)
			}
		}
		return n
	}
	if access.Mode == store.MCPModeAll {
		buf.Write(snap.allPrefixed)
		return snap.toolCount
	}
	for _, srv := range snap.Ordered {
		if !access.CanUseServer(srv.ID) {
			continue
		}
		if access.AllToolsOf(srv.ID) {
			if len(srv.joinedPrefixed) == 0 {
				continue
			}
			if n > 0 {
				buf.WriteByte(',')
			}
			buf.Write(srv.joinedPrefixed)
			n += len(srv.Sorted)
			continue
		}
		for _, t := range srv.Sorted {
			if access.CanCall(srv.ID, t.Name) {
				add(t.Prefixed)
			}
		}
	}
	return n
}

// resolveTool maps the name a client called to a server and tool. It
// returns nil when the tool does not exist or is not exposed.
func resolveTool(snap *Snapshot, scope, name string) (*Server, *Tool) {
	slug, tool := scope, name
	if scope == "" {
		var ok bool
		if slug, tool, ok = SplitToolName(name); !ok {
			return nil, nil
		}
	}
	srv := snap.BySlug[slug]
	if srv == nil {
		return nil, nil
	}
	t, ok := srv.Tool(tool)
	if !ok {
		return srv, nil
	}
	return srv, t
}

func (m *Manager) serveCallTool(x *exchange) {
	start := time.Now()
	ctx := x.r.Context()
	snap := m.Registry.Snapshot()
	srv, tool := resolveTool(snap, x.scope, x.params.Name)
	x.out.Tool = x.params.Name
	if srv != nil {
		x.out.Server = srv.Slug
	}
	unknown := func() {
		x.out.Error = "unknown tool"
		writeRPCError(x.w, http.StatusOK, x.req.ID, CodeInvalidParams, "Unknown tool: "+x.params.Name)
	}
	if srv == nil || tool == nil {
		unknown()
		return
	}
	entry := store.MCPCallLog{
		RequestID: x.caller.RequestID, APIKeyID: x.caller.KeyID, ServerID: srv.ID, ServerSlug: srv.Slug,
		Tool: tool.Name, Client: x.caller.Client, TraceID: x.caller.TraceID, RequestBytes: int64(x.size),
	}
	finish := func(status, errText string, respBytes int, upstream time.Duration, httpStatus int) {
		entry.Status, entry.Error = status, truncate(errText, 500)
		entry.LatencyMS = time.Since(start).Milliseconds()
		entry.UpstreamMS = upstream.Milliseconds()
		entry.ResponseBytes = int64(respBytes)
		m.recordCall(entry)
		m.auditCall(x, entry, httpStatus)
		x.out.CallStatus, x.out.Error, x.out.UpstreamMS = status, entry.Error, entry.UpstreamMS
	}

	access, err := m.access.get(ctx, x.caller.KeyID)
	if err != nil {
		writeRPCError(x.w, http.StatusInternalServerError, x.req.ID, CodeInternalError, "could not load access")
		return
	}
	if !access.CanCall(srv.ID, tool.Name) {
		finish(StatusDenied, "key has no access to this tool", 0, 0, http.StatusOK)
		unknown() // same answer as a missing tool, so access does not reveal what exists
		return
	}
	if x.modern && len(tool.Params) > 0 {
		// Arguments are only read for tools with x-mcp-header annotations.
		var p struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = fastjson.UnmarshalNoCopy(x.req.Params, &p)
		if msg := checkParamHeaders(x.r.Header, p.Arguments, tool.Params); msg != "" {
			writeRPCError(x.w, http.StatusBadRequest, x.req.ID, CodeHeaderMismatch, "Header mismatch: "+msg)
			return
		}
	}

	release, decision, err := m.limiter.Acquire(ctx, limits.Request{
		APIKeyID: "mcp:" + x.caller.KeyID, Limits: m.limits, Provider: "mcp", Model: srv.Slug,
	})
	if err != nil {
		writeRPCError(x.w, http.StatusServiceUnavailable, x.req.ID, CodeInternalError, "rate limiter unavailable")
		return
	}
	if !decision.Allowed {
		finish(StatusRateLimited, decision.LimitKind+" limit reached", 0, 0, http.StatusTooManyRequests)
		secs := int(decision.RetryAfter.Seconds() + 0.999)
		if secs < 1 {
			secs = 1
		}
		x.w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeRPCError(x.w, http.StatusTooManyRequests, x.req.ID, -32000,
			fmt.Sprintf("MCP rate limit reached (%s); retry in %ds", decision.LimitKind, secs))
		return
	}
	defer release(0)

	params := x.req.Params
	guard := m.guardFor(ctx, x.caller, srv, tool)
	if guard != nil {
		var v guardVerdict
		params, v = guard.checkArgs(params)
		x.out.Guardrail = v.label()
		if v.blocked {
			finish(StatusBlocked, "arguments blocked by "+v.detector+": "+v.reason, 0, 0, http.StatusOK)
			writeRPCResult(x.w, http.StatusOK, x.req.ID, toolErrorResult("KeiRouter blocked this tool call: "+v.reason))
			return
		}
	}

	relay := newProgressRelay(x)
	var notify Notify
	if relay != nil {
		notify = relay.notify
	}
	res := m.invoke(ctx, srv, tool, params, forwardHeaders(x.r.Header, srv.ForwardHeaders), notify)
	if relay != nil {
		x.out.Progress = relay.sent
	}
	if res.rpcErr != nil {
		finish(res.status, res.rpcErr.Message, 0, res.upstream, http.StatusOK)
		buf := getBuffer()
		defer putBuffer(buf)
		appendRPCError(buf, x.req.ID, res.rpcErr.Code, res.rpcErr.Message, res.rpcErr.Data)
		relay.reply(x.w, buf.Bytes())
		return
	}
	result := res.result
	if guard != nil && (res.status == StatusSuccess || res.status == StatusToolError) {
		var v guardVerdict
		result, v = guard.checkResult(result)
		if l := v.label(); l != "" {
			x.out.Guardrail = joinLabel(x.out.Guardrail, l)
		}
		if v.blocked {
			result = toolErrorResult("KeiRouter blocked the result of this tool call: " + v.reason)
			res.status, res.errText = StatusBlocked, "result blocked by "+v.detector+": "+v.reason
		}
	}
	if x.modern {
		result = ensureResultType(result)
	} else if resultType(result) == "input_required" {
		result = toolErrorResult("This tool needs input that only clients speaking MCP " + VersionModern + " can provide.")
		res.status = StatusToolError
	}
	if res.status == StatusToolError && res.errText == "" {
		res.errText = toolErrorText(result)
	}
	finish(res.status, res.errText, len(result), res.upstream, http.StatusOK)
	buf := getBuffer()
	defer putBuffer(buf)
	appendRPCResult(buf, x.req.ID, result)
	relay.reply(x.w, buf.Bytes())
}

func joinLabel(a, b string) string {
	if a == "" {
		return b
	}
	return a + ", " + b
}

// invokeResult is the outcome of one upstream tools/call.
type invokeResult struct {
	result   json.RawMessage
	rpcErr   *RPCError
	status   string
	errText  string
	upstream time.Duration
}

// invoke calls a tool upstream. Transport failures come back as an isError
// result the model can read; upstream JSON-RPC errors as rpcErr.
func (m *Manager) invoke(ctx context.Context, srv *Server, tool *Tool, params json.RawMessage, extra http.Header, notify Notify) invokeResult {
	if ok, wait := m.breaker.allow(srv.ID); !ok {
		msg := fmt.Sprintf("%s is failing; KeiRouter paused calls to it for %ds.", srv.Name, int(wait.Seconds()+0.999))
		return invokeResult{result: toolErrorResult(msg), status: StatusUpstreamError, errText: "circuit open"}
	}
	target, err := m.targetFor(srv)
	if err != nil {
		return invokeResult{result: toolErrorResult("KeiRouter could not read this server's credentials."), status: StatusUpstreamError, errText: err.Error()}
	}
	if extra == nil {
		extra = http.Header{}
	}
	if IsModern(target.Version) {
		var args json.RawMessage
		var p struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		if len(tool.Params) > 0 && json.Unmarshal(params, &p) == nil {
			args = p.Arguments
		}
		for _, ph := range tool.Params {
			if v, ok, err := ParamHeaderValue(args, ph); err == nil && ok {
				extra.Set(ph.HeaderKey(), v)
			}
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(srv.TimeoutMS)*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	resp, err := m.Client.CallTool(callCtx, target, tool.Name, params, extra, notify)
	upstream := time.Since(t0)
	if err != nil {
		var he *HTTPError
		switch {
		case ctx.Err() != nil:
			return invokeResult{result: toolErrorResult("The client cancelled the call."), status: StatusUpstreamError, errText: "cancelled", upstream: upstream}
		case errors.Is(err, context.DeadlineExceeded) || callCtx.Err() != nil:
			m.breaker.failure(srv.ID)
			return invokeResult{result: toolErrorResult(fmt.Sprintf("%s did not answer within %ds.", srv.Name, srv.TimeoutMS/1000)), status: StatusTimeout, errText: "timeout", upstream: upstream}
		case errors.As(err, &he):
			if he.Status >= 500 {
				m.breaker.failure(srv.ID)
			} else {
				m.breaker.success(srv.ID)
			}
			return invokeResult{result: toolErrorResult(srv.Name + ": " + he.Error()), status: StatusUpstreamError, errText: he.Error(), upstream: upstream}
		default:
			m.breaker.failure(srv.ID)
			return invokeResult{result: toolErrorResult(srv.Name + " could not be reached."), status: StatusUpstreamError, errText: err.Error(), upstream: upstream}
		}
	}
	m.breaker.success(srv.ID)
	if resp == nil {
		return invokeResult{result: toolErrorResult(srv.Name + " returned an empty response."), status: StatusUpstreamError, errText: "empty response", upstream: upstream}
	}
	if resp.Error != nil {
		return invokeResult{rpcErr: resp.Error, status: StatusUpstreamError, upstream: upstream}
	}
	status := StatusSuccess
	if isErrorResult(resp.Result) {
		status = StatusToolError
	}
	return invokeResult{result: resp.Result, status: status, upstream: upstream}
}

// InvokeResult is what the dashboard tester shows.
type InvokeResult struct {
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *RPCError       `json:"error,omitempty"`
	LatencyMS int64           `json:"latency_ms"`
}

// Invoke calls a server's tool on behalf of the dashboard. It bypasses key
// access and limits, and is logged with the "dashboard" key id.
func (m *Manager) Invoke(ctx context.Context, serverID, tool string, args json.RawMessage) (InvokeResult, error) {
	snap := m.Registry.Snapshot()
	srv := snap.ByID[serverID]
	if srv == nil {
		return InvokeResult{}, errors.New("server is disabled or unknown")
	}
	t, ok := srv.Tool(tool)
	if !ok {
		return InvokeResult{}, fmt.Errorf("tool %q is not exposed by %s", tool, srv.Name)
	}
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage(`{}`)
	}
	name, _ := json.Marshal(tool)
	params := append(append(append([]byte(`{"name":`), name...), `,"arguments":`...), args...)
	params = append(params, '}')
	start := time.Now()
	res := m.invoke(ctx, srv, t, params, nil, nil)
	out := InvokeResult{Status: res.status, Result: res.result, Error: res.rpcErr, LatencyMS: time.Since(start).Milliseconds()}
	m.recordCall(store.MCPCallLog{
		APIKeyID: "dashboard", ServerID: srv.ID, ServerSlug: srv.Slug, Tool: t.Name, Status: res.status,
		Error: truncate(firstNonEmpty(res.errText, rpcMessage(res.rpcErr)), 500), LatencyMS: out.LatencyMS,
		UpstreamMS: res.upstream.Milliseconds(), RequestBytes: int64(len(params)), ResponseBytes: int64(len(res.result)),
		Client: "dashboard",
	})
	return out, nil
}

func (m *Manager) recordCall(e store.MCPCallLog) {
	e.ID = uuid.NewString()
	e.TenantID = m.tenant
	e.CreatedAt = time.Now().UTC()
	m.calls.record(e)
	if m.metrics != nil {
		m.metrics.RecordMCPCall(e.ServerSlug, e.Status, float64(e.LatencyMS)/1000)
	}
}

// checkParamHeaders validates the Mcp-Param-* headers of a modern request
// against the tool's annotated arguments.
func checkParamHeaders(h http.Header, args json.RawMessage, params []ParamHeader) string {
	for _, p := range params {
		want, ok, err := ParamHeaderValue(args, p)
		if err != nil {
			return err.Error()
		}
		got := h.Get(p.HeaderKey())
		if !ok {
			continue
		}
		if got == "" {
			return p.HeaderKey() + " header is required"
		}
		gv, gok := DecodeHeaderValue(got)
		wv, _ := DecodeHeaderValue(want)
		if !gok || gv != wv {
			return p.HeaderKey() + " header does not match the argument"
		}
	}
	return ""
}

func forwardHeaders(in http.Header, names []string) http.Header {
	out := http.Header{}
	for _, n := range names {
		if v := in.Values(n); len(v) > 0 {
			out[http.CanonicalHeaderKey(n)] = append([]string(nil), v...)
		}
	}
	for _, n := range []string{"Traceparent", "Tracestate"} {
		if v := in.Get(n); v != "" {
			out.Set(n, v)
		}
	}
	return out
}

// toolErrorText returns the first text block of an isError result, so the
// log and console say why the tool failed.
func toolErrorText(result json.RawMessage) string {
	n, err := sonic.Get(result, "content", 0, "text")
	if err != nil {
		return ""
	}
	s, _ := n.String()
	return truncate(s, 200)
}

func isErrorResult(result json.RawMessage) bool {
	n, err := sonic.Get(result, "isError")
	if err != nil {
		return false
	}
	b, err := n.Bool()
	return err == nil && b
}

func resultType(result json.RawMessage) string {
	n, err := sonic.Get(result, "resultType")
	if err != nil {
		return ""
	}
	s, _ := n.String()
	return s
}

// ensureResultType adds resultType "complete" to a result from a legacy
// upstream, which modern clients require.
func ensureResultType(result json.RawMessage) json.RawMessage {
	if resultType(result) != "" {
		return result
	}
	trimmed := bytes.TrimLeft(result, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return result
	}
	rest := bytes.TrimLeft(trimmed[1:], " \t\r\n")
	out := make([]byte, 0, len(result)+26)
	out = append(out, `{"resultType":"complete"`...)
	if len(rest) > 0 && rest[0] != '}' {
		out = append(out, ',')
	}
	return append(out, rest...)
}

func toolErrorResult(text string) json.RawMessage {
	t, _ := json.Marshal(text)
	return json.RawMessage(`{"content":[{"type":"text","text":` + string(t) + `}],"isError":true}`)
}

func writeRPCResult(w http.ResponseWriter, status int, id, result []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	buf := getBuffer()
	defer putBuffer(buf)
	appendRPCResult(buf, id, result)
	_, _ = w.Write(buf.Bytes())
}

func appendRPCResult(buf *bytes.Buffer, id, result []byte) {
	buf.WriteString(`{"jsonrpc":"2.0","id":`)
	writeID(buf, id)
	buf.WriteString(`,"result":`)
	buf.Write(result)
	buf.WriteByte('}')
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	writeRPCErrorData(w, status, id, code, msg, nil)
}

func writeRPCErrorData(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string, data json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	buf := getBuffer()
	defer putBuffer(buf)
	appendRPCError(buf, id, code, msg, data)
	_, _ = w.Write(buf.Bytes())
}

func appendRPCError(buf *bytes.Buffer, id json.RawMessage, code int, msg string, data json.RawMessage) {
	m, _ := json.Marshal(msg)
	buf.WriteString(`{"jsonrpc":"2.0","id":`)
	writeID(buf, id)
	buf.WriteString(`,"error":{"code":` + strconv.Itoa(code) + `,"message":`)
	buf.Write(m)
	if len(data) > 0 {
		buf.WriteString(`,"data":`)
		buf.Write(data)
	}
	buf.WriteString(`}}`)
}

// WriteHTTPError writes a JSON-RPC error with no id, for failures before the
// body is read (authentication, Origin, method not allowed).
func WriteHTTPError(w http.ResponseWriter, status int, msg string) {
	code := CodeInvalidRequest
	if status >= 500 {
		code = CodeInternalError
	}
	writeRPCError(w, status, nil, code, msg)
}

func writeID(buf *bytes.Buffer, id json.RawMessage) {
	if len(bytes.TrimSpace(id)) == 0 {
		buf.WriteString("null")
		return
	}
	buf.Write(id)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func rpcMessage(e *RPCError) string {
	if e == nil {
		return ""
	}
	return e.Message
}
