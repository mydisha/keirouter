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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
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

// sharedClient is reused across connectors; the transport pools connections.
// Tuned for AI-proxy workloads: many concurrent long-lived streams to a handful
// of upstream hosts (OpenAI, Anthropic, Google, etc.).
var sharedClient = &http.Client{
	Timeout: 0, // per-request deadlines come from context
	Transport: &http.Transport{
		MaxIdleConns:        200,               // keep more idle conns across all hosts
		MaxIdleConnsPerHost: 20,                // more conns per upstream (parallel streams)
		MaxConnsPerHost:     50,                // cap total conns per host to prevent FD exhaustion
		IdleConnTimeout:     120 * time.Second, // keep idle conns longer for bursty traffic
		TLSHandshakeTimeout: 10 * time.Second,
		// Time-to-headers safety net. Kept in step with the dashboard's
		// response_header_timeout default so slow-but-healthy providers
		// (reasoning models, ollama on modest hardware) aren't cut off before
		// the operator-configured budget. Per-request context deadlines from
		// the pipeline still bound the overall call.
		ResponseHeaderTimeout:  60 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		WriteBufferSize:        16 * 1024, // 16 KB write buffer (reduced from 64 KB)
		ReadBufferSize:         16 * 1024, // 16 KB read buffer (reduced from 64 KB)
		ForceAttemptHTTP2:      true,      // prefer HTTP/2 for multiplexed streams
		MaxResponseHeaderBytes: 64 * 1024, // cap response header size
	},
}

// proxyTransportCache pools *http.Transport instances keyed by proxy config
// string. This prevents creating a new transport (and its goroutine/buffer
// pool) on every proxied request -- a significant memory leak.
var proxyTransportCache sync.Map

// clientFor returns an http.Client configured with proxy settings from creds.
// When creds carry no proxy config, the shared client is returned. Proxy
// transports are cached so the same transport is reused across requests.
func clientFor(creds core.Credentials) *http.Client {
	if creds.ProxyURL == "" && creds.RelayURL == "" {
		return sharedClient
	}
	key := creds.ProxyURL + "|" + creds.RelayURL + "|" + creds.NoProxy
	if v, ok := proxyTransportCache.Load(key); ok {
		return &http.Client{Transport: v.(*http.Transport)}
	}
	t := &http.Transport{
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		WriteBufferSize:       16 * 1024,
		ReadBufferSize:        16 * 1024,
		ForceAttemptHTTP2:     true,
	}
	if creds.ProxyURL != "" {
		if u, err := url.Parse(creds.ProxyURL); err == nil {
			t.Proxy = proxyFunc(u, creds.NoProxy)
		}
	}
	actual, _ := proxyTransportCache.LoadOrStore(key, t)
	return &http.Client{Transport: actual.(*http.Transport)}
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

// doJSON performs a JSON POST and returns the response body, mapping transport
// and HTTP errors to structured ProviderErrors.
func doJSON(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, transportError(ctx, provider, model, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: provider, Model: model, Message: "read body: " + err.Error(), Cause: err}
	}

	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, model, resp, respBody)
	}
	if perr := checkNonJSONResponse(provider, model, resp, respBody); perr != nil {
		return nil, perr
	}
	return respBody, nil
}

// doJSONDecode performs a JSON POST and returns a streaming json.Decoder
// instead of reading the entire response body into memory. The decoder reads
// directly from the response body, eliminating one full copy. The caller MUST
// close the returned body when done.
//
// On error (status >= 400), the body is read and closed internally, and a
// ProviderError is returned with the decoder set to nil.
func doJSONDecode(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) (*json.Decoder, io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, nil, transportError(ctx, provider, model, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, nil, httpStatusError(provider, model, resp, errBody)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, nil, transportError(ctx, provider, model, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, nil, httpStatusError(provider, model, resp, errBody)
	}
	return resp.Body, resp.Header, nil
}

// doJSONMethod performs a JSON request with an explicit method (GET/POST) and
// returns the response body. A nil body sends no payload (for GET).
func doJSONMethod(ctx context.Context, method, provider, model, url string, body []byte, headers map[string]string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, transportError(ctx, provider, model, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: provider, Model: model, Message: "read body: " + err.Error(), Cause: err}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, transportError(ctx, provider, model, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: provider, Model: model, Message: "read body: " + err.Error(), Cause: err}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, transportError(ctx, provider, model, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: provider, Model: model, Message: "read body: " + err.Error(), Cause: err}
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, transportError(ctx, provider, model, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: provider, Model: model, Message: "read body: " + err.Error(), Cause: err}
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, model, resp, respBody)
	}
	return respBody, nil
}

// openStream performs a streaming POST and returns the response for the caller
// to read SSE lines from. The caller must close resp.Body.
func openStream(ctx context.Context, provider, model, url string, body []byte, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: provider, Model: model, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	proxyRewrite(ctx, req)
	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, transportError(ctx, provider, model, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, httpStatusError(provider, model, resp, errBody)
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
	go func() {
		defer close(out)
		defer resp.Body.Close()

		ttft := newTTFTTracker(cfg)

		scanner := sseScanner(resp.Body)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			payload, ok := parseSSEData(scanner.Text())
			if !ok {
				continue
			}
			chunks, perr := codec.ParseStreamLine([]byte(payload), model)
			if perr != nil {
				continue
			}
			for _, ch := range chunks {
				ttft.maybeReport(ch)
				select {
				case out <- ch:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- core.StreamChunk{
				Type: core.ChunkError,
				Err:  &core.ProviderError{Kind: core.ErrTimeout, Provider: provider, Model: model, Message: err.Error(), Cause: err},
			}
		}
	}()
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
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	return sc
}

// sseScannerPooled returns a bufio.Scanner like sseScanner but reuses a buffer
// from the pool. The caller should NOT return the buffer — the scanner owns it
// for the lifetime of the stream.
func sseScannerPooled(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
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

// transportError classifies a transport-level failure (DNS, connection, ctx).
func transportError(ctx context.Context, provider, model string, err error) error {
	kind := core.ErrUpstream
	if ctx.Err() == context.DeadlineExceeded {
		kind = core.ErrTimeout
	}
	return &core.ProviderError{Kind: kind, Provider: provider, Model: model, Message: err.Error(), Cause: err}
}

// httpStatusError maps an HTTP error status to a structured ProviderError.
func httpStatusError(provider, model string, resp *http.Response, body []byte) error {
	kind := core.ErrUpstream
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		kind = core.ErrRateLimit
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		kind = core.ErrAuth
	case resp.StatusCode == http.StatusPaymentRequired:
		kind = core.ErrQuotaExhausted
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		kind = core.ErrBadRequest
	}

	pe := &core.ProviderError{
		Kind:       kind,
		Provider:   provider,
		Model:      model,
		StatusCode: resp.StatusCode,
		Message:    truncateError(body),
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			pe.RetryAfter = time.Duration(secs) * time.Second
		} else if retryAt, err := http.ParseTime(ra); err == nil {
			if wait := time.Until(retryAt); wait > 0 {
				pe.RetryAfter = wait
			}
		}
	}
	return pe
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
