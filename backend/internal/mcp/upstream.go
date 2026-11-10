package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mydisha/keirouter/backend/internal/httputil"
)

// MaxUpstreamResponseBytes caps one upstream response body (JSON or SSE).
const MaxUpstreamResponseBytes = 32 << 20

// maxListPages bounds tools/list pagination so a misbehaving server cannot
// keep the sync loop busy forever.
const maxListPages = 100

// Target describes one upstream MCP server for a request.
type Target struct {
	// Key scopes the cached legacy session; the server id.
	Key string
	URL string
	// Headers are set on every request: auth, static headers.
	Headers http.Header
	// Version is the negotiated protocol version. Empty means not negotiated.
	Version string
}

// HTTPError is a non-2xx answer from an upstream server.
type HTTPError struct {
	Status int
	RPC    *RPCError // the JSON-RPC error in the body, when there was one
	Body   string    // a short prefix of the body otherwise
}

func (e *HTTPError) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden:
		return fmt.Sprintf("upstream rejected the credentials (HTTP %d)", e.Status)
	case e.RPC != nil:
		return fmt.Sprintf("upstream HTTP %d: %s", e.Status, e.RPC.Message)
	case e.Body != "":
		return fmt.Sprintf("upstream HTTP %d: %s", e.Status, e.Body)
	default:
		return fmt.Sprintf("upstream HTTP %d", e.Status)
	}
}

// Client talks to upstream MCP servers over Streamable HTTP, in either
// protocol era. It is safe for concurrent use.
type Client struct {
	httpc  *http.Client
	info   Implementation
	nextID atomic.Int64

	mu       sync.Mutex
	sessions map[string]*legacySession
}

type legacySession struct {
	mu      sync.Mutex // serialises initialize for one server
	ready   bool
	id      string
	version string
}

// NewClient builds a client. A nil transport selects the default MCP
// transport, which refuses to connect to addresses the outbound policy blocks.
func NewClient(info Implementation, transport http.RoundTripper) *Client {
	if transport == nil {
		transport = newTransport()
	}
	return &Client{
		httpc:    &http.Client{Transport: transport, CheckRedirect: noCrossOriginRedirect},
		info:     info,
		sessions: map[string]*legacySession{},
	}
}

// noCrossOriginRedirect stops redirects that would carry credentials to
// another origin.
func noCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return http.ErrUseLastResponse
	}
	return nil
}

func newTransport() *http.Transport {
	safe := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: dialControl}
	plain := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	proxies := envProxyHosts()
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			if proxies[strings.ToLower(host)] {
				// The operator's own egress proxy; the target URL was checked
				// when it was saved and the proxy resolves it.
				return plain.DialContext(ctx, network, addr)
			}
			return safe.DialContext(ctx, network, addr)
		},
		MaxIdleConns:           256,
		MaxIdleConnsPerHost:    32,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		ForceAttemptHTTP2:      true,
	}
}

// dialControl applies the outbound address policy to the resolved IP, so a
// hostname cannot be rebound to a private address after it was validated.
func dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("unexpected dial address %q", address)
	}
	return httputil.ValidateIP(ip)
}

func envProxyHosts() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
			out[strings.ToLower(u.Hostname())] = true
		}
	}
	return out
}

// Forget drops the cached legacy session for a server, for example after its
// URL or credentials changed.
func (c *Client) Forget(key string) {
	c.mu.Lock()
	delete(c.sessions, key)
	c.mu.Unlock()
}

func (c *Client) session(key string) *legacySession {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[key]
	if s == nil {
		s = &legacySession{}
		c.sessions[key] = s
	}
	return s
}

// Negotiate finds the protocol version to use with a server. It sends a
// modern server/discover request first; a reply or a recognised modern error
// means a modern server. Anything else falls back to the legacy initialize
// handshake. Authentication failures and network errors are returned as is.
func (c *Client) Negotiate(ctx context.Context, t Target) (string, error) {
	params, _ := withMeta(nil, true, c.info, "")
	resp, err := c.post(ctx, t, c.nextID.Add(1), "server/discover", "", params, nil, nil)
	if err == nil && resp != nil && resp.Error == nil {
		var dr struct {
			SupportedVersions []string `json:"supportedVersions"`
		}
		if json.Unmarshal(resp.Result, &dr) == nil && slices.Contains(dr.SupportedVersions, VersionModern) {
			return VersionModern, nil
		}
		if !slices.ContainsFunc(dr.SupportedVersions, IsLegacy) {
			return "", fmt.Errorf("server supports protocol versions %v; KeiRouter supports %v", dr.SupportedVersions, SupportedVersions)
		}
		// It answers discovery but only speaks initialize-era versions.
		return c.negotiateLegacy(ctx, t)
	}

	rpcErr := rpcErrorOf(resp, err)
	if rpcErr != nil && rpcErr.Code == CodeUnsupportedProtocolVersion {
		var data unsupportedVersionData
		_ = json.Unmarshal(rpcErr.Data, &data)
		if slices.Contains(data.Supported, VersionModern) {
			return VersionModern, nil
		}
		// A dual-era server that only offers initialize-era versions: use the
		// legacy handshake below.
		if !slices.ContainsFunc(data.Supported, IsLegacy) {
			return "", fmt.Errorf("server supports protocol versions %v; KeiRouter supports %v", data.Supported, SupportedVersions)
		}
	} else if rpcErr != nil && IsModernErrorCode(rpcErr.Code) {
		return "", fmt.Errorf("server rejected discovery: %s", rpcErr.Message)
	}
	var he *HTTPError
	if errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) {
		return "", err
	}
	if err != nil && !errors.As(err, &he) {
		return "", err // network error, timeout
	}
	if he != nil && he.Status >= 500 {
		return "", err
	}

	return c.negotiateLegacy(ctx, t)
}

// negotiateLegacy runs the initialize handshake and returns the version the
// server agreed to.
func (c *Client) negotiateLegacy(ctx context.Context, t Target) (string, error) {
	s := c.session(t.Key)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = false
	if err := c.initialize(ctx, t, s); err != nil {
		return "", err
	}
	return s.version, nil
}

func rpcErrorOf(resp *Response, err error) *RPCError {
	if resp != nil && resp.Error != nil {
		return resp.Error
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.RPC
	}
	return nil
}

// initialize runs the legacy handshake. The caller holds s.mu.
func (c *Client) initialize(ctx context.Context, t Target, s *legacySession) error {
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": VersionLegacyLatest,
		"capabilities":    map[string]any{},
		"clientInfo":      c.info,
	})
	id := c.nextID.Add(1)
	req, err := c.newRequest(ctx, t, envelope(id, "initialize", params))
	if err != nil {
		return err
	}
	resp, sessionID, err := c.do(req, idJSON(id), nil)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if resp == nil {
		return errors.New("initialize: empty response")
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize: %s", resp.Error.Message)
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(resp.Result, &ir); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if !IsLegacy(ir.ProtocolVersion) {
		return fmt.Errorf("server negotiated protocol version %q, which KeiRouter does not support", ir.ProtocolVersion)
	}
	s.id = sessionID
	s.version = ir.ProtocolVersion

	note, err := c.newRequest(ctx, t, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		return err
	}
	setLegacyHeaders(note, s.id, s.version)
	if _, _, err := c.do(note, nil, nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}
	s.ready = true
	return nil
}

// Call sends one request to a server and returns its JSON-RPC response with
// the upstream id. The caller restores the client's id. extra carries headers
// such as Mcp-Param-*. A legacy session that expired is re-initialised once.
func (c *Client) Call(ctx context.Context, t Target, method, name string, params json.RawMessage, extra http.Header) (*Response, error) {
	return c.call(ctx, t, method, name, params, extra, "", nil)
}

// Notify receives each message an upstream sends on a response stream before
// the response itself: notifications and server requests, as raw JSON. msg
// is reused after the call returns, so keep a copy to hold on to it.
type Notify func(msg []byte)

// CallTool sends tools/call for tool, replacing params.name with the
// upstream's own tool name. Arguments are forwarded byte for byte. notify,
// when set, sees the messages the server streams before the result.
func (c *Client) CallTool(ctx context.Context, t Target, tool string, params json.RawMessage, extra http.Header, notify Notify) (*Response, error) {
	return c.call(ctx, t, "tools/call", tool, params, extra, tool, notify)
}

func (c *Client) call(ctx context.Context, t Target, method, name string, params json.RawMessage, extra http.Header, setName string, notify Notify) (*Response, error) {
	if t.Version == "" {
		return nil, errors.New("protocol version not negotiated")
	}
	if IsModern(t.Version) {
		body, err := withMeta(params, true, c.info, setName)
		if err != nil {
			return nil, err
		}
		return c.post(ctx, t, c.nextID.Add(1), method, name, body, extra, notify)
	}

	body, err := withMeta(params, false, c.info, setName)
	if err != nil {
		return nil, err
	}
	s := c.session(t.Key)
	for attempt := 0; attempt < 2; attempt++ {
		s.mu.Lock()
		if !s.ready {
			if err := c.initialize(ctx, t, s); err != nil {
				s.mu.Unlock()
				return nil, err
			}
		}
		sessID, version := s.id, s.version
		s.mu.Unlock()

		id := c.nextID.Add(1)
		req, err := c.newRequest(ctx, t, envelope(id, method, body))
		if err != nil {
			return nil, err
		}
		setLegacyHeaders(req, sessID, version)
		for k, v := range extra {
			req.Header[k] = v
		}
		resp, _, err := c.do(req, idJSON(id), notify)
		var he *HTTPError
		if attempt == 0 && sessID != "" && errors.As(err, &he) && he.Status == http.StatusNotFound {
			s.mu.Lock()
			if s.id == sessID {
				s.ready = false
			}
			s.mu.Unlock()
			continue
		}
		return resp, err
	}
	return nil, errors.New("legacy session could not be re-established")
}

// ListTools returns every tool definition the server exposes, following
// pagination. Definitions are returned exactly as received.
func (c *Client) ListTools(ctx context.Context, t Target) ([]json.RawMessage, error) {
	var (
		out    []json.RawMessage
		cursor string
	)
	for page := 0; page < maxListPages; page++ {
		var params json.RawMessage
		if cursor != "" {
			params, _ = json.Marshal(map[string]string{"cursor": cursor})
		}
		resp, err := c.Call(ctx, t, "tools/list", "", params, nil)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return nil, errors.New("tools/list: empty response")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("tools/list: %s", resp.Error.Message)
		}
		var lr struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(resp.Result, &lr); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		out = append(out, lr.Tools...)
		if lr.NextCursor == "" || lr.NextCursor == cursor {
			return out, nil
		}
		cursor = lr.NextCursor
	}
	return nil, fmt.Errorf("tools/list: more than %d pages", maxListPages)
}

// post sends one modern request with the mirrored request headers.
func (c *Client) post(ctx context.Context, t Target, id int64, method, name string, params json.RawMessage, extra http.Header, notify Notify) (*Response, error) {
	req, err := c.newRequest(ctx, t, envelope(id, method, params))
	if err != nil {
		return nil, err
	}
	req.Header.Set(HeaderProtocolVersion, VersionModern)
	req.Header.Set(HeaderMethod, method)
	if name != "" {
		req.Header.Set(HeaderName, EncodeHeaderValue(name))
	}
	for k, v := range extra {
		req.Header[k] = v
	}
	resp, _, err := c.do(req, idJSON(id), notify)
	return resp, err
}

func (c *Client) newRequest(ctx context.Context, t Target, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range t.Headers {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req, nil
}

func setLegacyHeaders(req *http.Request, sessionID, version string) {
	if sessionID != "" {
		req.Header.Set(HeaderSessionID, sessionID)
	}
	// 2025-03-26 predates the MCP-Protocol-Version header.
	if version != "" && version != "2025-03-26" {
		req.Header.Set(HeaderProtocolVersion, version)
	}
}

// do sends req and decodes the reply. wantID is the request's JSON-RPC id,
// nil for a notification. It returns a nil Response for 202 Accepted, and the
// Mcp-Session-Id response header. notify sees streamed messages that precede
// the response.
func (c *Client) do(req *http.Request, wantID json.RawMessage, notify Notify) (*Response, string, error) {
	res, err := c.httpc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	sessionID := res.Header.Get(HeaderSessionID)
	body := io.LimitReader(res.Body, MaxUpstreamResponseBytes+1)

	if res.StatusCode == http.StatusAccepted {
		_, _ = io.Copy(io.Discard, body)
		return nil, sessionID, nil
	}
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(body, 64<<10))
		he := &HTTPError{Status: res.StatusCode}
		var env Response
		if json.Unmarshal(raw, &env) == nil && env.Error != nil {
			he.RPC = env.Error
		} else {
			he.Body = snippet(raw)
		}
		return nil, sessionID, he
	}
	if ct == "text/event-stream" {
		resp, err := readSSEResponse(body, wantID, notify)
		return resp, sessionID, err
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, sessionID, err
	}
	if len(raw) > MaxUpstreamResponseBytes {
		return nil, sessionID, errors.New("upstream response too large")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, sessionID, nil
	}
	var env Response
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, sessionID, fmt.Errorf("upstream returned invalid JSON: %w", err)
	}
	return &env, sessionID, nil
}

// readSSEResponse reads SSE events until the JSON-RPC response whose id
// matches want. Other messages before it go to notify, or are skipped.
func readSSEResponse(r io.Reader, want json.RawMessage, notify Notify) (*Response, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var data bytes.Buffer
	read := 0
	for {
		line, err := br.ReadBytes('\n')
		read += len(line)
		if read > MaxUpstreamResponseBytes {
			return nil, errors.New("upstream response too large")
		}
		trimmed := bytes.TrimRight(line, "\r\n")
		switch {
		case len(trimmed) == 0:
			if data.Len() > 0 {
				if resp := matchResponse(data.Bytes(), want); resp != nil {
					return resp, nil
				}
				if notify != nil {
					notify(data.Bytes())
				}
				data.Reset()
			}
		case bytes.HasPrefix(trimmed, []byte("data:")):
			v := bytes.TrimPrefix(trimmed[5:], []byte(" "))
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.Write(v)
		}
		if err != nil {
			if data.Len() > 0 {
				if resp := matchResponse(data.Bytes(), want); resp != nil {
					return resp, nil
				}
			}
			if errors.Is(err, io.EOF) {
				return nil, errors.New("upstream stream ended before the response")
			}
			return nil, err
		}
	}
}

func matchResponse(event []byte, want json.RawMessage) *Response {
	var env Response
	if err := json.Unmarshal(event, &env); err != nil {
		return nil
	}
	if env.Result == nil && env.Error == nil {
		return nil // notification or request
	}
	if len(want) > 0 && !sameID(env.ID, want) {
		return nil
	}
	resp := env
	return &resp
}

func sameID(a, b json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}

func envelope(id int64, method string, params json.RawMessage) []byte {
	var b bytes.Buffer
	b.WriteString(`{"jsonrpc":"2.0","id":`)
	b.WriteString(strconv.FormatInt(id, 10))
	b.WriteString(`,"method":`)
	m, _ := json.Marshal(method)
	b.Write(m)
	if len(params) > 0 {
		b.WriteString(`,"params":`)
		b.Write(params)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func idJSON(id int64) json.RawMessage { return strconv.AppendInt(nil, id, 10) }

// withMeta rewrites the reserved `_meta` keys of params for an upstream
// request. Reserved keys from the client are dropped; for a modern upstream
// the gateway's own version, identity and capabilities are added. Other
// `_meta` keys (progressToken, traceparent, vendor keys) pass through, and
// every other params field is copied byte for byte.
func withMeta(params json.RawMessage, modern bool, info Implementation, setName string) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(params)) > 0 && string(bytes.TrimSpace(params)) != "null" {
		if err := json.Unmarshal(params, &fields); err != nil {
			return nil, fmt.Errorf("params must be a JSON object: %w", err)
		}
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := fields["_meta"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("_meta must be a JSON object: %w", err)
		}
	}
	if setName != "" {
		n, _ := json.Marshal(setName)
		fields["name"] = n
	}
	for k := range meta {
		if strings.HasPrefix(k, reservedMetaPrefix) {
			delete(meta, k)
		}
	}
	if modern {
		v, _ := json.Marshal(VersionModern)
		i, _ := json.Marshal(info)
		meta[MetaProtocolVersion] = v
		meta[MetaClientInfo] = i
		meta[MetaClientCapabilities] = json.RawMessage(`{}`)
	}
	if len(meta) == 0 {
		delete(fields, "_meta")
	} else {
		m, err := json.Marshal(meta)
		if err != nil {
			return nil, err
		}
		fields["_meta"] = m
	}
	if len(fields) == 0 {
		if modern {
			return json.RawMessage(`{}`), nil
		}
		return nil, nil
	}
	return json.Marshal(fields)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
