// Package connectors implements provider drivers: the components that render a
// canonical request to a provider's wire format, perform the HTTP call, and
// parse the response (unary or streaming) back into canonical chunks.
//
// Connectors are thin and stateless. They delegate format translation to the
// transform package and focus on transport: URL construction, auth headers,
// streaming, and mapping HTTP/transport failures to structured ProviderErrors
// that drive the dispatcher's fallback decisions.
package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// errNonJSONResponse marks a successful HTTP response whose body was not JSON
// (typically an HTML page from a provider's web frontend). It is set as the
// Cause on the ProviderError returned by checkNonJSONResponse so validation can
// distinguish it from an ordinary non-auth upstream response and fail rather
// than false-positively accepting the credential. Test with isNonJSONResponseError.
var errNonJSONResponse = errors.New("upstream returned a non-JSON (HTML) response")

// maxResponseBodyBytes caps the size of upstream response bodies read into
// memory. This prevents a single large response from causing an OOM spike.
// 32 MiB matches the inbound request body limit.
const maxResponseBodyBytes = 32 << 20 // 32 MiB

// Transport tuning. The values follow what LiteLLM's aiohttp transport settled
// on for AI-proxy traffic (many long-lived streams to a handful of hosts) and
// what Go needs on top of that for liveness:
//
//   - dialTimeout bounds TCP connect. Without it a SYN black-hole stalls for
//     the kernel's ~2 minute default before the request fails over.
//   - tcpKeepAlive keeps NAT/LB flow entries alive during long generations.
//   - http2PingInterval/http2PingTimeout detect half-dead HTTP/2 connections
//     (NAT timeouts, provider restarts). Without a ping health check Go keeps
//     multiplexing new streams onto a connection that will never answer.
//   - maxConnsPerHost is deliberately high: HTTP/1.1 upstreams do not
//     multiplex, and a low cap makes requests queue inside the transport with
//     no deadline. FD pressure is bounded by the gateway concurrency limiter.
const (
	dialTimeout         = 10 * time.Second
	tcpKeepAlive        = 30 * time.Second
	tlsHandshakeTO      = 10 * time.Second
	idleConnTimeout     = 120 * time.Second
	http2PingInterval   = 30 * time.Second
	http2PingTimeout    = 15 * time.Second
	maxIdleConns        = 512
	maxIdleConnsPerHost = 32
	maxConnsPerHost     = 256
	transportBufSize    = 16 * 1024
	maxRespHeaderBytes  = 64 * 1024

	// defaultUnaryHeaderTimeout bounds header wait for unary calls issued under
	// a context without a deadline (admin probes, OAuth refresh). Pipeline
	// requests always carry a deadline and are not affected.
	defaultUnaryHeaderTimeout = 120 * time.Second
)

// errResponseHeaderTimeout marks a request whose upstream headers did not
// arrive within the configured budget. It wraps context.DeadlineExceeded so
// the dispatcher treats it like any other self-imposed deadline: fall back,
// but do not cool the account down (the provider may be slow, not broken).
var errResponseHeaderTimeout = fmt.Errorf("upstream response headers not received in time: %w", context.DeadlineExceeded)

func newDialer() *net.Dialer {
	return &net.Dialer{Timeout: dialTimeout, KeepAlive: tcpKeepAlive}
}

// newTransport builds the pooled transport shared by all unproxied requests
// and, with a proxy function, the per-proxy transports.
func newTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  proxy,
		DialContext:            newDialer().DialContext,
		MaxIdleConns:           maxIdleConns,
		MaxIdleConnsPerHost:    maxIdleConnsPerHost,
		MaxConnsPerHost:        maxConnsPerHost,
		IdleConnTimeout:        idleConnTimeout,
		TLSHandshakeTimeout:    tlsHandshakeTO,
		ExpectContinueTimeout:  1 * time.Second,
		WriteBufferSize:        transportBufSize,
		ReadBufferSize:         transportBufSize,
		ForceAttemptHTTP2:      true,
		MaxResponseHeaderBytes: maxRespHeaderBytes,
		// ResponseHeaderTimeout is intentionally unset: the header budget is
		// applied per request (see sendRequest) because unary completions
		// only send headers after generation finishes and must be bounded by
		// the request deadline, while streams get the dashboard TTFB budget.
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: http2PingInterval,
			PingTimeout:     http2PingTimeout,
		},
	}
}

// newFreshConnTransport builds a no-keep-alive transport used to replay a
// request after the pooled transport handed us a stale socket. A fresh TCP
// connection per request guarantees the replay cannot pick another dead
// socket from the pool.
func newFreshConnTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  proxy,
		DialContext:            newDialer().DialContext,
		DisableKeepAlives:      true,
		IdleConnTimeout:        1 * time.Second,
		TLSHandshakeTimeout:    tlsHandshakeTO,
		ExpectContinueTimeout:  1 * time.Second,
		WriteBufferSize:        transportBufSize,
		ReadBufferSize:         transportBufSize,
		MaxResponseHeaderBytes: maxRespHeaderBytes,
		ForceAttemptHTTP2:      false,
	}
}

// sharedClient is reused across connectors; the transport pools connections.
// Environment proxies (HTTPS_PROXY, NO_PROXY) are honoured like LiteLLM's
// trust_env; dashboard/account proxies take precedence via clientFor.
var sharedClient = &http.Client{
	Timeout:   0, // per-request deadlines come from context
	Transport: newTransport(http.ProxyFromEnvironment),
}

// retryClient is used when a pooled idle connection is known to be stale.
var retryClient = &http.Client{
	Timeout:   0,
	Transport: newFreshConnTransport(http.ProxyFromEnvironment),
}

// proxyTransports caches transports keyed by proxy config so proxied requests
// reuse pooled connections instead of building a transport (and its
// goroutines/buffers) per request. Entries are bounded; the least recently
// used transport is closed when the cache overflows.
var proxyTransports = newTransportCache(64)

type transportPair struct {
	pooled *http.Transport
	fresh  *http.Transport
}

// transportCache is a small LRU of proxy transports. Proxy pools can rotate
// through hundreds of egress URLs; without eviction every URL ever used would
// pin its idle connections and buffers for the life of the process.
type transportCache struct {
	mu    sync.Mutex
	max   int
	order []string
	items map[string]transportPair
}

func newTransportCache(max int) *transportCache {
	return &transportCache{max: max, items: make(map[string]transportPair)}
}

func (c *transportCache) get(key string, build func() transportPair) transportPair {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tp, ok := c.items[key]; ok {
		c.touch(key)
		return tp
	}
	tp := build()
	c.items[key] = tp
	c.order = append(c.order, key)
	for len(c.order) > c.max {
		victim := c.order[0]
		c.order = c.order[1:]
		if old, ok := c.items[victim]; ok {
			delete(c.items, victim)
			old.pooled.CloseIdleConnections()
			old.fresh.CloseIdleConnections()
		}
	}
	return tp
}

func (c *transportCache) touch(key string) {
	for i, k := range c.order {
		if k == key {
			copy(c.order[i:], c.order[i+1:])
			c.order[len(c.order)-1] = key
			return
		}
	}
}

func proxyTransportsFor(creds core.Credentials) transportPair {
	key := creds.ProxyURL + "|" + creds.RelayURL + "|" + creds.NoProxy
	return proxyTransports.get(key, func() transportPair {
		var proxy func(*http.Request) (*url.URL, error)
		if creds.ProxyURL != "" {
			if u, err := url.Parse(creds.ProxyURL); err == nil {
				proxy = proxyFunc(u, creds.NoProxy)
			}
		}
		return transportPair{pooled: newTransport(proxy), fresh: newFreshConnTransport(proxy)}
	})
}

// clientFor returns an http.Client configured with proxy settings from creds.
// When creds carry no proxy config, the shared client is returned.
func clientFor(creds core.Credentials) *http.Client {
	if creds.ProxyURL == "" && creds.RelayURL == "" {
		return sharedClient
	}
	return &http.Client{Transport: proxyTransportsFor(creds).pooled}
}

// proxyFunc returns a proxy function that routes requests through proxyURL,
// skipping hosts that match the comma-separated noProxy bypass list.
func proxyFunc(proxyURL *url.URL, noProxy string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if noProxy != "" {
			host := req.URL.Hostname()
			for _, bypass := range strings.Split(noProxy, ",") {
				bypass = strings.TrimSpace(bypass)
				if bypass == "" {
					continue
				}
				if bypass == "*" ||
					strings.EqualFold(host, bypass) ||
					strings.HasSuffix(host, "."+bypass) {
					return nil, nil
				}
			}
		}
		return proxyURL, nil
	}
}

// relayRequest rewrites a request to go through a relay proxy. The relay
// protocol uses x-relay-target (origin) and x-relay-path (path+query) headers.
func relayRequest(req *http.Request, relayURL string) {
	origOrigin := req.URL.Scheme + "://" + req.URL.Host
	origPath := req.URL.Path
	if req.URL.RawQuery != "" {
		origPath += "?" + req.URL.RawQuery
	}
	req.Header.Set("x-relay-target", origOrigin)
	req.Header.Set("x-relay-path", origPath)
	relay, _ := url.Parse(relayURL)
	req.URL = relay
	req.Host = relay.Host
}

// requestBuilder constructs a fresh *http.Request. sendRequest may call it
// twice: once for the initial attempt and once more for a single replay on a
// fresh connection. Bodies are always in-memory byte slices, so rebuilding is
// cheap and keeps the replay free of half-consumed readers.
type requestBuilder func() (*http.Request, error)

// cancelOnClose releases the per-request header-timeout context once the
// response body is closed, so the context (and its timer bookkeeping) does not
// outlive the response.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel(context.Canceled)
	return err
}

// headerBudget resolves the time-to-headers budget for a request issued under
// ctx. Streams carry the dashboard "response header timeout" (TTFB); unary
// calls are bounded by their request deadline, with a safety net for callers
// that set none.
func headerBudget(ctx context.Context) time.Duration {
	if d := core.ResponseHeaderTimeoutFromContext(ctx); d > 0 {
		return d
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return 0
	}
	return defaultUnaryHeaderTimeout
}

// sendRequest performs one upstream call with the resilience rules shared by
// every connector:
//
//   - Headers must arrive within headerBudget(ctx); otherwise the call fails
//     with ErrTimeout wrapping context.DeadlineExceeded (no cooldown).
//   - A request that died before any response bytes because the pooled
//     connection was stale, or because the dial itself failed, is replayed
//     exactly once on a fresh connection (LiteLLM: one retry on
//     ConnectError/RemoteProtocolError with a new client). Replays are safe
//     here because nothing reached the provider.
//   - Transport failures are mapped to ProviderErrors via transportError.
func sendRequest(ctx context.Context, provider, model string, build requestBuilder) (*http.Response, error) {
	resp, err := sendOnce(ctx, provider, model, build, proxyClient(ctx))
	if err == nil || !shouldRetryFreshConnection(ctx, err) {
		return resp, err
	}
	return sendOnce(ctx, provider, model, build, proxyClientForRetry(ctx))
}

func sendOnce(ctx context.Context, provider, model string, build requestBuilder, client *http.Client) (*http.Response, error) {
	req, err := build()
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	proxyRewrite(ctx, req)

	budget := headerBudget(ctx)
	if budget <= 0 {
		resp, err := client.Do(req)
		if err != nil {
			return nil, transportError(ctx, provider, model, err)
		}
		return resp, nil
	}

	hctx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(budget, func() { cancel(errResponseHeaderTimeout) })
	resp, err := client.Do(req.WithContext(hctx))
	timer.Stop()
	if err != nil {
		cancel(context.Canceled)
		if errors.Is(context.Cause(hctx), errResponseHeaderTimeout) && ctx.Err() == nil {
			return nil, &core.ProviderError{
				Kind: core.ErrTimeout, Scope: core.FailureScopeRequest, Provider: provider, Model: model,
				Message: fmt.Sprintf("upstream did not send response headers within %s", budget),
				Cause:   errResponseHeaderTimeout,
			}
		}
		return nil, transportError(ctx, provider, model, err)
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// jsonRequest returns a builder for a JSON request with the given method.
func jsonRequest(ctx context.Context, method, url string, body []byte, headers map[string]string) requestBuilder {
	return func() (*http.Request, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
}

// readErrorBody drains a bounded prefix of an error response for diagnostics.
func readErrorBody(resp *http.Response) []byte {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return b
}

// readBodyLimited reads at most maxResponseBodyBytes and fails loudly when the
// body is larger instead of silently truncating it into a parse error.
func readBodyLimited(provider, model string, resp *http.Response) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeNetwork, Provider: provider, Model: model, Message: "read body: " + err.Error(), Cause: err}
	}
	if len(b) > maxResponseBodyBytes {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: provider, Model: model,
			Message: fmt.Sprintf("upstream response exceeds %d bytes", maxResponseBodyBytes)}
	}
	return b, nil
}

// doJSON performs a JSON POST and returns the response body, mapping transport
// and HTTP errors to structured ProviderErrors.
func doJSON(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) ([]byte, error) {
	return doJSONMethod(ctx, http.MethodPost, provider, model, url, body, headers)
}

// doJSONDecode performs a JSON POST and returns a streaming json.Decoder
// instead of reading the entire response body into memory. The decoder reads
// directly from the response body, eliminating one full copy. The caller MUST
// close the returned body when done.
//
// On error (status >= 400), the body is read and closed internally, and a
// ProviderError is returned with the decoder set to nil.
func doJSONDecode(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) (*json.Decoder, io.ReadCloser, error) {
	resp, err := sendRequest(ctx, provider, model, jsonRequest(ctx, http.MethodPost, url, body, headers))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, nil, httpStatusError(provider, model, resp, readErrorBody(resp))
	}
	// Guard against an HTML page (web frontend) served with HTTP 200 before
	// handing the body to the streaming JSON decoder. Body is not buffered here,
	// so detect by content-type only.
	if perr := checkNonJSONResponse(provider, model, resp, nil); perr != nil {
		resp.Body.Close()
		return nil, nil, perr
	}
	dec := json.NewDecoder(resp.Body)
	return dec, resp.Body, nil
}

// doJSONReader is like doJSON but returns an io.ReadCloser for the response
// body instead of reading it all into memory. The caller must close the reader.
// Used for large responses that will be streamed (e.g. direct pipe path).
func doJSONReader(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) (io.ReadCloser, http.Header, error) {
	resp, err := sendRequest(ctx, provider, model, jsonRequest(ctx, http.MethodPost, url, body, headers))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, nil, httpStatusError(provider, model, resp, readErrorBody(resp))
	}
	return resp.Body, resp.Header, nil
}

// doJSONMethod performs a JSON request with an explicit method (GET/POST) and
// returns the response body. A nil body sends no payload (for GET).
func doJSONMethod(ctx context.Context, method, provider, model, url string, body []byte, headers map[string]string) ([]byte, error) {
	resp, err := sendRequest(ctx, provider, model, jsonRequest(ctx, method, url, body, headers))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := readBodyLimited(provider, model, resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, model, resp, respBody)
	}
	if perr := checkNonJSONResponse(provider, model, resp, respBody); perr != nil {
		return nil, perr
	}
	return respBody, nil
}

// doFormPOST performs an application/x-www-form-urlencoded POST and returns the
// response body, mapping transport and HTTP errors to ProviderErrors. Used for
// OAuth token endpoints (refresh, JWT-bearer assertion exchange).
func doFormPOST(ctx context.Context, provider, model, endpoint string, form url.Values, headers map[string]string) ([]byte, error) {
	encoded := form.Encode()
	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	resp, err := sendRequest(ctx, provider, model, build)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := readBodyLimited(provider, model, resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, model, resp, respBody)
	}
	return respBody, nil
}

// rawResponse carries non-JSON response bytes plus the upstream content type,
// used by binary endpoints like text-to-speech.
type rawResponse struct {
	Body        []byte
	ContentType string
}

// doRaw performs a JSON POST but returns the raw response bytes and content
// type instead of parsing JSON. Used for endpoints that return binary audio.
func doRaw(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) (*rawResponse, error) {
	resp, err := sendRequest(ctx, provider, model, jsonRequest(ctx, http.MethodPost, url, body, headers))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := readBodyLimited(provider, model, resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, model, resp, respBody)
	}
	return &rawResponse{Body: respBody, ContentType: resp.Header.Get("Content-Type")}, nil
}

// multipartField is one non-file form field in a multipart upload.
type multipartField struct{ Name, Value string }

// doMultipart performs a multipart/form-data POST with a single file part plus
// extra text fields, returning the JSON response body. Used by speech-to-text.
func doMultipart(ctx context.Context, provider, model, url, fileField, filename string, fileData []byte, fields []multipartField, headers map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile(fileField, filename)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	if _, err := fw.Write(fileData); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	for _, f := range fields {
		if f.Value == "" {
			continue
		}
		if err := mw.WriteField(f.Name, f.Value); err != nil {
			return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
		}
	}
	if err := mw.Close(); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	payload := buf.Bytes()
	contentType := mw.FormDataContentType()

	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	resp, err := sendRequest(ctx, provider, model, build)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := readBodyLimited(provider, model, resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, model, resp, respBody)
	}
	return respBody, nil
}

// openStream performs a streaming POST and returns the response for the caller
// to read SSE lines from. The caller must close resp.Body. A stale pooled
// connection or failed dial is replayed once on a fresh connection by
// sendRequest; the time-to-headers budget comes from the request context.
func openStream(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) (*http.Response, error) {
	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	resp, err := sendRequest(ctx, provider, model, build)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, httpStatusError(provider, model, resp, readErrorBody(resp))
	}
	// An HTML page served with HTTP 200 on the stream endpoint means the base URL
	// points at a web frontend, not the SSE API. Detect by content-type before
	// the caller starts scanning for "data:" events (which would never arrive).
	if perr := checkNonJSONResponse(provider, model, resp, nil); perr != nil {
		resp.Body.Close()
		return nil, perr
	}
	return resp, nil
}

// streamParser is the subset of a codec the SSE pump needs: turning one
// upstream SSE data payload into canonical chunks.
type streamParser interface {
	ParseStreamLine(line []byte, model string) ([]core.StreamChunk, error)
}

// ttftTracker fires OnFirstChunk exactly once per stream, measuring elapsed
// time from the pipeline's StartedAt (preferred) or the scanner's own start
// time. This eliminates the duplicated ttftReported + isMeaningfulChunk boilerplate
// across every connector's Stream method.
type ttftTracker struct {
	ref  time.Time // reference point for elapsed calculation
	cb   func(time.Duration)
	done bool
}

// newTTFTTracker builds a tracker from a StreamConfig. When cfg.StartedAt is
// set (pipeline provided it), that is used as the TTFT reference so the
// measurement includes HTTP connection time. Otherwise the tracker records
// time.Now() as a fallback reference.
func newTTFTTracker(cfg core.StreamConfig) *ttftTracker {
	ref := cfg.StartedAt
	if ref.IsZero() {
		ref = time.Now()
	}
	return &ttftTracker{ref: ref, cb: cfg.OnFirstChunk}
}

// maybeReport fires the callback if ch is the first meaningful chunk.
func (t *ttftTracker) maybeReport(ch core.StreamChunk) {
	if t.done || t.cb == nil {
		return
	}
	if !isMeaningfulChunk(ch) {
		return
	}
	t.done = true
	t.cb(time.Since(t.ref))
}

// scanOpenAISSE consumes an OpenAI-style SSE response, parsing each "data:"
// payload through the given codec and emitting canonical chunks on the returned
// channel. It owns resp.Body and closes it when done. Shared by the
// OpenAI-compatible subscription connectors (Qwen, iFlow, ...) to avoid
// duplicating the streaming goroutine.
//
// TTFT is measured from cfg.StartedAt (set by the pipeline before the HTTP
// call) to the first meaningful chunk, so it includes connection time.
func scanOpenAISSE(ctx context.Context, provider, model string, resp *http.Response, codec streamParser, cfg core.StreamConfig) <-chan core.StreamChunk {
	out := make(chan core.StreamChunk, 16)
	go streamSSE(ctx, provider, model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		return codec.ParseStreamLine(payload, model)
	})
	return out
}

// isMeaningfulChunk reports whether a stream chunk represents actual model
// output (text, thinking, or a tool call with an ID). Usage, finish, ping,
// and incremental tool-call argument deltas are not meaningful for TTFT.
func isMeaningfulChunk(ch core.StreamChunk) bool {
	switch ch.Type {
	case core.ChunkText:
		return ch.Delta != ""
	case core.ChunkThinking:
		return ch.Delta != ""
	case core.ChunkToolCall:
		return ch.ToolCall != nil && ch.ToolCall.ID != ""
	default:
		return false
	}
}

// sseScanner returns a bufio.Scanner configured for SSE: it reads one logical
// line at a time with a generous buffer for large data payloads. Uses a pooled
// initial buffer to reduce allocation pressure on high-throughput streams.
func sseScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), sseMaxLineBytes)
	return sc
}

// parseSSEData extracts the payload from an SSE "data:" line, or returns ("",
// false) for non-data lines (comments, event:, blank).
func parseSSEData(line string) (string, bool) {
	line = strings.TrimRight(line, "\r")
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

// isSSEKeepAlive reports whether a non-data SSE line is an explicit comment
// heartbeat. Connectors can translate it to ChunkPing so the pipeline's stall
// detector observes that the upstream connection is still active.
func isSSEKeepAlive(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), ":")
}

// transportError classifies a transport-level failure (DNS, connection, ctx).
//
// A net.Error that reports Timeout() (dial timeout, TLS handshake timeout,
// per-read deadlines) is ErrTimeout at provider scope: the host is reachable
// but slow, which is worth a short cooldown. Everything else without an HTTP
// status is a network-scoped ErrUpstream (connection refused, DNS failure,
// reset) that counts toward the provider circuit breaker.
func transportError(ctx context.Context, provider, model string, err error) error {
	kind := core.ErrUpstream
	scope := core.FailureScopeNetwork
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		kind = core.ErrClientCanceled
		scope = core.FailureScopeRequest
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		kind = core.ErrTimeout
		scope = core.FailureScopeRequest
	case isTimeoutNetError(err):
		kind = core.ErrTimeout
		scope = core.FailureScopeProvider
	}
	return &core.ProviderError{
		Kind: kind, Scope: scope, Provider: provider, Model: model,
		Message: err.Error(), Cause: err,
	}
}

func isTimeoutNetError(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// shouldRetryFreshConnection permits one replay before response headers exist.
// Only network-scoped transport errors raised before any response byte arrived
// qualify:
//
//   - the pooled connection was already closed by the server (stale socket);
//   - the dial itself failed (refused, reset, unreachable, DNS);
//   - the connection dropped with EOF before headers (the server went away
//     while the request was in flight; LiteLLM's RemoteProtocolError case).
//
// HTTP responses, timeouts and cancellations are handled by normal fallback so
// a request is never multiplied blindly.
func shouldRetryFreshConnection(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	pe := core.AsProviderError(err)
	if pe.Kind != core.ErrUpstream ||
		pe.StatusCode != 0 ||
		pe.EffectiveScope() != core.FailureScopeNetwork ||
		pe.Cause == nil {
		return false
	}
	if isTimeoutNetError(pe.Cause) {
		return false
	}
	var opErr *net.OpError
	if errors.As(pe.Cause, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(pe.Cause, &dnsErr) {
		return true
	}
	if errors.Is(pe.Cause, io.EOF) || errors.Is(pe.Cause, io.ErrUnexpectedEOF) {
		return true
	}
	message := strings.ToLower(pe.Cause.Error())
	return strings.Contains(message, "server closed idle connection") ||
		strings.Contains(message, "use of closed network connection") ||
		strings.Contains(message, "connection reset by peer") && strings.Contains(message, "write")
}

// httpStatusError maps an HTTP error status to a structured ProviderError.
//
// Classification follows LiteLLM's exception mapping: the body text is
// consulted before the bare status because providers and the gateways in
// front of them routinely wrap one condition in another status (a 429 inside
// a 503, a context-window overflow inside a generic 400, a depleted balance
// inside a 403).
func httpStatusError(provider, model string, resp *http.Response, body []byte) error {
	kind := core.ErrUpstream
	var retryAfter time.Duration
	var creditsExhausted bool
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		// Classify 429 into transient rate-limit vs hard quota exhaustion vs
		// depleted paid balance. Quota exhaustion gets a much longer cooldown
		// than per-minute throttling; a dry balance parks the account.
		kind, retryAfter, creditsExhausted = classify429(resp, body)
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		kind = core.ErrAuth
		// Some gateways report a depleted balance as 403 rather than 402.
		if looksLikeCreditsExhausted(string(body)) {
			kind = core.ErrQuotaExhausted
			creditsExhausted = true
		}
	case resp.StatusCode == http.StatusPaymentRequired:
		kind = core.ErrQuotaExhausted
		if wait := githubMonthlyUsageRetryAfter(provider, resp.StatusCode, body, time.Now()); wait > 0 {
			retryAfter = wait
		} else {
			creditsExhausted = true
		}
	case resp.StatusCode == http.StatusNotFound:
		kind = core.ErrModelUnavailable
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusGatewayTimeout:
		// The provider (or its edge) gave up waiting. Retryable on another
		// account/model like any upstream fault, surfaced as a timeout.
		kind = core.ErrTimeout
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		kind = core.ErrBadRequest
		bodyStr := string(body)
		switch {
		// Some backends report unknown or inaccessible models as a plain 400
		// (Codex: "The 'X' model is not supported when using Codex with a
		// ChatGPT account"). Classify those as model-unavailable so chains
		// fall back to the next model/provider instead of hard-failing.
		case isModelUnsupportedBody(body):
			kind = core.ErrModelUnavailable
		// Anthropic-style APIs return "credit balance is too low" as a plain
		// 400 invalid_request_error. Treat it as a depleted balance so chains
		// fall back to the next account instead of surfacing a request error.
		case looksLikeCreditsExhausted(bodyStr):
			kind = core.ErrQuotaExhausted
			creditsExhausted = true
		// Some APIs (Cloudflare) report a rejected token as a plain 400;
		// treat it as the credential problem it is so the next account is
		// tried and this one is benched.
		case errclass.LooksLikeAuthError(bodyStr):
			kind = core.ErrAuth
		// The prompt does not fit this model: a larger-context target in the
		// chain can still serve it.
		case errclass.LooksLikeContextWindow(bodyStr):
			kind = core.ErrContextWindow
		case errclass.LooksLikeContentFilter(bodyStr):
			kind = core.ErrContentFilter
		// OpenAI "Request too large" (tokens-per-minute) and similar are
		// throttling answers dressed as 400/413.
		case errclass.LooksLikeRateLimitWrapped(body):
			kind, retryAfter, creditsExhausted = classify429(resp, body)
		}
	case resp.StatusCode >= 500:
		// Gemini/Vertex and several gateways wrap "Resource exhausted" or an
		// embedded code 429 in a 5xx. Treat it as throttling so the account is
		// cooled for the hinted window rather than tripping the circuit.
		if errclass.LooksLikeRateLimitWrapped(body) {
			kind, retryAfter, creditsExhausted = classify429(resp, body)
		}
	}

	scope := core.FailureScopeProvider
	switch kind {
	case core.ErrBadRequest, core.ErrContentFilter:
		scope = core.FailureScopeRequest
	case core.ErrModelUnavailable, core.ErrContextWindow:
		scope = core.FailureScopeModel
	case core.ErrAuth, core.ErrRateLimit, core.ErrQuotaExhausted:
		scope = core.FailureScopeAccount
	}

	pe := &core.ProviderError{
		Kind:             kind,
		Scope:            scope,
		Provider:         provider,
		Model:            model,
		StatusCode:       resp.StatusCode,
		Message:          truncateError(body),
		RetryAfter:       retryAfter,
		CreditsExhausted: creditsExhausted,
	}
	// Honour Retry-After on every status (503 "overloaded, retry in 2s" is
	// common). Transient hints are capped so a single odd header cannot park
	// an account for hours; quota resets keep their full horizon.
	if pe.RetryAfter <= 0 {
		pe.RetryAfter = parseRetryAfterHeader(resp.Header.Get("Retry-After"))
	}
	if pe.Kind != core.ErrQuotaExhausted {
		pe.RetryAfter = errclass.CapRetryAfter(pe.RetryAfter)
	}
	return pe
}

const githubMonthlyUsageLimitMessage = "you've reached your additional usage limit for your plan"

// githubMonthlyUsageRetryAfter identifies GitHub Copilot's resettable monthly
// premium-request limit. It is intentionally narrow: unrelated HTTP 402
// responses may represent a depleted paid balance and keep their existing
// credits-exhausted treatment.
func githubMonthlyUsageRetryAfter(provider string, status int, body []byte, now time.Time) time.Duration {
	if provider != "github" || status != http.StatusPaymentRequired ||
		!strings.Contains(strings.ToLower(string(body)), githubMonthlyUsageLimitMessage) {
		return 0
	}
	now = now.UTC()
	reset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return reset.Sub(now)
}

// modelUnsupportedPhrases are error-body fragments that reliably indicate the
// requested model cannot be served by this provider/account (unknown id, no
// access) rather than a malformed request. Matched case-insensitively and only
// on bodies that mention "model" to avoid misclassifying generic errors.
var modelUnsupportedPhrases = []string{
	"model is not supported", "model not supported",
	"model is not available", "model not available",
	"model not found", "model_not_found", "deployment_not_found",
	"invalid model", "unknown model",
	"model does not exist", "does not exist or you do not have access",
	"access to model", "access to the model",
}

// isModelUnsupportedBody reports whether a 4xx body describes a model the
// provider/account cannot serve.
func isModelUnsupportedBody(body []byte) bool {
	s := strings.ToLower(string(body))
	if !strings.Contains(s, "model") {
		return false
	}
	for _, p := range modelUnsupportedPhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// checkNonJSONResponse detects a successful (non-error) HTTP response whose
// body is not JSON — almost always an HTML page served when the configured base
// URL points at a provider's web frontend instead of its API (for example a
// custom base URL missing the "/v1" path segment, so POST {base}/messages hits
// the SPA and returns "<!doctype html>..." with HTTP 200). Without this guard
// the HTML body is handed to the JSON parser, producing a confusing
// "parse response: Syntax error at index 0" and, during validation, a false
// positive because any non-auth HTTP response is otherwise treated as proof the
// credential works.
//
// It returns a ProviderError (nil when the body looks like JSON). StatusCode is
// deliberately left 0: no valid API response was received, so credential
// validation must treat this as "did not reach the API" rather than a
// key-accepted signal. Pass a nil body to check by content-type only (used on
// paths that stream the body instead of buffering it).
func checkNonJSONResponse(provider, model string, resp *http.Response, body []byte) *core.ProviderError {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	isHTML := strings.Contains(ct, "text/html")
	if !isHTML {
		trimmed := bytes.TrimSpace(body)
		// Empty/unknown bodies (e.g. header-only checks) and JSON bodies (which
		// start with '{', '[', '"', a digit, or t/f/n) are accepted. Only an
		// HTML document, which starts with '<', is rejected here.
		if len(trimmed) == 0 || trimmed[0] != '<' {
			return nil
		}
	}
	return &core.ProviderError{
		Kind:     core.ErrUpstream,
		Provider: provider,
		Model:    model,
		Message: fmt.Sprintf("upstream returned a non-JSON response (HTTP %d, content-type %q); "+
			"the base URL likely points at a web page rather than the API endpoint — "+
			"check that it includes the API path segment (e.g. ends with /v1)",
			resp.StatusCode, resp.Header.Get("Content-Type")),
		Cause: errNonJSONResponse,
	}
}

// isNonJSONResponseError reports whether err originates from a non-JSON (HTML)
// upstream response detected by checkNonJSONResponse.
func isNonJSONResponseError(err error) bool {
	return errors.Is(err, errNonJSONResponse)
}

func truncateError(body []byte) string {
	const max = 512
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		return s[:max] + "…"
	}
	if s == "" {
		return "upstream returned an error with empty body"
	}
	return s
}

// bearer builds an Authorization: Bearer header value.
func bearer(token string) string { return "Bearer " + token }

// ---- context-based proxy injection -----------------------------------------

// proxyClient returns an http.Client configured with proxy settings from ctx,
// or the shared client when no proxy is configured.
func proxyClient(ctx context.Context) *http.Client {
	creds, ok := core.ProxyFromContext(ctx)
	if !ok {
		return sharedClient
	}
	return clientFor(creds)
}

// proxyClientForRetry returns a no-keep-alive client for replaying a request
// after a transport-level failure, so the replay cannot grab a stale socket
// from the shared pool. Proxied requests get the cached fresh transport for
// their proxy config.
func proxyClientForRetry(ctx context.Context) *http.Client {
	creds, ok := core.ProxyFromContext(ctx)
	if !ok || (creds.ProxyURL == "" && creds.RelayURL == "") {
		return retryClient
	}
	return &http.Client{Transport: proxyTransportsFor(creds).fresh}
}

// proxyRewrite applies relay header rewriting to req if ctx carries a RelayURL.
func proxyRewrite(ctx context.Context, req *http.Request) {
	creds, ok := core.ProxyFromContext(ctx)
	if !ok || creds.RelayURL == "" {
		return
	}
	relayRequest(req, creds.RelayURL)
}

// mergeHeaders combines connector defaults with credential-supplied headers.
func mergeHeaders(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// joinURL concatenates a base URL and path, collapsing duplicate slashes.
func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	path = strings.TrimLeft(path, "/")
	return fmt.Sprintf("%s/%s", base, path)
}
