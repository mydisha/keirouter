package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/proxy"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// ---- proxy pools ------------------------------------------------------------

// unresolvedPoolProxy is a deliberately hostless proxy URL. Every egress path
// treats a proxy URL without a host as fail-closed, so an account whose pool
// cannot be loaded errors out instead of leaking the server's own IP.
const unresolvedPoolProxy = "unresolved-proxy-pool:"

// accountEgressContext returns ctx carrying the egress route the account's
// traffic uses — its proxy pool, else the dashboard global proxy — mirroring
// the dispatcher. Account-scoped admin calls (validation, quota, model
// discovery, token refresh) go through it so they leave from the same IP as
// the account's requests.
func (s *Server) accountEgressContext(ctx context.Context, acc store.Account) context.Context {
	var route core.Credentials
	if s.pools != nil && acc.ProxyPoolID != "" {
		if err := proxy.ResolvePool(ctx, s.pools, acc.ProxyPoolID, &route); err != nil {
			s.log.Warn("proxy pools: cannot resolve account pool; failing closed",
				"account", acc.ID, "pool", acc.ProxyPoolID, "error", err)
			route = core.Credentials{ProxyURL: unresolvedPoolProxy, StrictProxy: true}
		}
	}
	if route.ProxyURL == "" && route.RelayURL == "" && s.proxyNotifier != nil {
		route.ProxyURL = s.proxyNotifier.ProxyURL()
		route.NoProxy = s.proxyNotifier.NoProxy()
		route.StrictProxy = true // global proxy fails closed, as in dispatch
	}
	if route.ProxyURL == "" && route.RelayURL == "" {
		return ctx
	}
	route.AccountID = acc.ID
	return core.WithProxy(ctx, route)
}

// redactedPassword is what url.URL.Redacted puts in place of a password. A
// proxy URL echoed back with this password keeps the stored one.
const redactedPassword = "xxxxx"

func (s *Server) adminListProxyPools(w http.ResponseWriter, r *http.Request) {
	pools, err := s.pools.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "listing proxy pools failed"))
		return
	}

	// Count accounts bound to each pool for the dashboard.
	boundCounts, err := s.accounts.CountByProxyPool(r.Context())
	if err != nil {
		s.log.Warn("proxy pools: count bound accounts", "error", err)
	}

	out := make([]map[string]any, 0, len(pools))
	for _, p := range pools {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"proxy_url": redactProxyURL(p.ProxyURL), "no_proxy": p.NoProxy,
			"strict": p.Strict, "is_active": p.IsActive,
			"test_status": p.TestStatus, "last_tested": p.LastTested,
			"last_error":             p.LastError,
			"bound_connection_count": boundCounts[p.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"pools": out})
}

var validProxyPoolTypes = map[string]bool{
	"http": true, "vercel": true, "cloudflare": true, "deno": true,
}

func (s *Server) adminCreateProxyPool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		ProxyURL string `json:"proxy_url"`
		NoProxy  string `json:"no_proxy"`
		Strict   bool   `json:"strict"`
		IsActive *bool  `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.ProxyURL = strings.TrimSpace(body.ProxyURL)
	if body.Name == "" || body.ProxyURL == "" {
		writeError(w, http.StatusBadRequest, "name and proxy_url are required")
		return
	}

	poolType := strings.ToLower(strings.TrimSpace(body.Type))
	if poolType == "" {
		poolType = "http"
	}
	if !validProxyPoolTypes[poolType] {
		writeError(w, http.StatusBadRequest, "invalid pool type: must be http, vercel, cloudflare, or deno")
		return
	}
	if err := validateProxyPoolURL(poolType, body.ProxyURL); err != nil {
		s.log.Warn("blocked invalid proxy URL", "url", redactProxyURL(body.ProxyURL), "type", poolType, "error", err)
		writeError(w, http.StatusBadRequest, "invalid proxy_url: "+err.Error())
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	now := time.Now()
	pool := store.ProxyPool{
		ID:         uuid.NewString(),
		Name:       body.Name,
		Type:       poolType,
		ProxyURL:   body.ProxyURL,
		NoProxy:    strings.TrimSpace(body.NoProxy),
		Strict:     body.Strict,
		IsActive:   active,
		TestStatus: "unknown",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.pools.Create(r.Context(), pool); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "proxy pool creation failed"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": pool.ID, "name": pool.Name})
}

func (s *Server) adminUpdateProxyPool(w http.ResponseWriter, r *http.Request) {
	pool, err := s.pools.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "pool not found")
		return
	}
	var body struct {
		Name     *string `json:"name"`
		ProxyURL *string `json:"proxy_url"`
		NoProxy  *string `json:"no_proxy"`
		Strict   *bool   `json:"strict"`
		IsActive *bool   `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		pool.Name = name
	}
	if body.ProxyURL != nil {
		next := restoreRedactedPassword(strings.TrimSpace(*body.ProxyURL), pool.ProxyURL)
		if err := validateProxyPoolURL(pool.Type, next); err != nil {
			writeError(w, http.StatusBadRequest, "invalid proxy_url: "+err.Error())
			return
		}
		if next != pool.ProxyURL {
			// The last verdict described the old endpoint.
			pool.TestStatus = "unknown"
			pool.LastError = ""
		}
		pool.ProxyURL = next
	}
	if body.NoProxy != nil {
		pool.NoProxy = strings.TrimSpace(*body.NoProxy)
	}
	if body.Strict != nil {
		pool.Strict = *body.Strict
	}
	if body.IsActive != nil {
		pool.IsActive = *body.IsActive
	}
	pool.UpdatedAt = time.Now()
	if err := s.pools.Update(r.Context(), pool); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "proxy pool update failed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateProxyPoolURL(poolType, rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("URL is required")
	}
	if poolType != "http" {
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("relay URL must be an absolute http or https URL")
		}
	}
	if err := httputil.ValidateProxyURL(rawURL); err != nil {
		return fmt.Errorf("blocked by security policy: %w", err)
	}
	if poolType == "http" {
		parsed, _ := url.Parse(rawURL)
		if parsed.Port() == "" && (parsed.Scheme == "socks5" || parsed.Scheme == "socks5h") {
			return fmt.Errorf("SOCKS5 proxy URL needs an explicit port")
		}
	}
	return nil
}

// redactProxyURL hides the password embedded in a proxy URL so it never
// reaches logs or the dashboard. Unparseable input is returned unchanged
// because it cannot carry a recognisable userinfo section.
func redactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return u.Redacted()
}

// restoreRedactedPassword maps a proxy URL the dashboard echoed back (with the
// password shown as "xxxxx") onto the stored password, so saving an edit form
// without retyping the password keeps it instead of storing the mask.
func restoreRedactedPassword(next, current string) string {
	nu, err := url.Parse(next)
	if err != nil || nu.User == nil {
		return next
	}
	pw, ok := nu.User.Password()
	if !ok || pw != redactedPassword {
		return next
	}
	cu, err := url.Parse(current)
	if err != nil || cu.User == nil {
		return next
	}
	if stored, ok := cu.User.Password(); ok && cu.User.Username() == nu.User.Username() {
		nu.User = url.UserPassword(nu.User.Username(), stored)
		return nu.String()
	}
	return next
}

func (s *Server) adminDeleteProxyPool(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pools.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "pool not found")
		return
	}

	// Prevent deletion if any accounts (in any tenant) are bound to this pool:
	// their requests would start failing on the dangling reference.
	counts, err := s.accounts.CountByProxyPool(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "proxy pool deletion failed"))
		return
	}
	if bound := counts[id]; bound > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("proxy pool is bound to %d account%s; unbind %s first",
				bound, plural(bound), map[bool]string{true: "it", false: "them"}[bound == 1]),
			"bound_connection_count": bound,
		})
		return
	}

	if err := s.pools.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "proxy pool deletion failed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminTestProxyPool(w http.ResponseWriter, r *http.Request) {
	pool, err := s.pools.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "pool not found")
		return
	}

	result := testProxyPoolConnectivity(r.Context(), pool)
	now := time.Now()

	// Record only the verdict. The test never toggles is_active: silently
	// disabling a pool after one failed probe would send its accounts out
	// directly (exposing the egress IP the pool exists to hide), and
	// re-enabling one after a pass would override the operator.
	if err := s.pools.RecordTest(context.WithoutCancel(r.Context()), pool.ID, result.status, now, result.lastError, nil); err != nil {
		s.log.Warn("proxy pools: record test result", "pool", pool.ID, "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      result.status,
		"last_tested": now,
		"elapsed_ms":  result.elapsedMS,
		"error":       result.lastError,
		"egress_ip":   result.egressIP,
	})
}

type proxyTestResult struct {
	status     string
	lastError  string
	elapsedMS  int64
	httpStatus int
	egressIP   string
}

const proxyProbeTimeout = 10 * time.Second

// proxyProbeTargets answer with the caller's public IP. Two independent
// services keep one provider's outage from failing every pool test.
var proxyProbeTargets = []string{
	"https://api.ipify.org?format=json",
	"https://httpbin.org/ip",
}

// relayProbeTargets echo the request headers back as {"headers": {...}}, which
// proves the relay actually forwarded the request to the target.
var relayProbeTargets = []string{
	"https://httpbin.org/headers",
	"https://echo.free.beeceptor.com",
}

// testProxyPoolConnectivity performs a real connectivity check against a proxy
// pool. For HTTP/SOCKS proxies it fetches a what-is-my-IP endpoint through the
// proxy; for relay types (vercel/cloudflare/deno) it sends relay headers.
func testProxyPoolConnectivity(ctx context.Context, pool store.ProxyPool) proxyTestResult {
	switch pool.Type {
	case "vercel", "cloudflare", "deno":
		return testRelayPool(ctx, pool.ProxyURL, proxyProbeTimeout)
	default: // "http"
		return testHTTPPool(ctx, pool.ProxyURL, proxyProbeTimeout)
	}
}

func testHTTPPool(ctx context.Context, proxyURL string, timeout time.Duration) proxyTestResult {
	start := time.Now()
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Host == "" {
		return proxyTestResult{status: "error", lastError: "invalid proxy URL"}
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(parsed),
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}

	var first proxyTestResult
	for i, target := range proxyProbeTargets {
		res := probeEgressIP(ctx, client, target)
		res.elapsedMS = time.Since(start).Milliseconds()
		if res.status == "active" {
			return res
		}
		if i == 0 {
			first = res
		}
		// A proxy that cannot be reached (or rejects us) fails the same way
		// for every target; only retry when the probe target itself erred.
		if res.httpStatus == 0 && isProxyTransportFailure(res.lastError) {
			break
		}
	}
	first.elapsedMS = time.Since(start).Milliseconds()
	return first
}

func probeEgressIP(ctx context.Context, client *http.Client, target string) proxyTestResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return proxyTestResult{status: "error", lastError: redactErrorURL(err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return proxyTestResult{status: "error", lastError: fmt.Sprintf("probe through proxy returned HTTP %d", resp.StatusCode), httpStatus: resp.StatusCode}
	}
	var body struct {
		IP     string `json:"ip"`
		Origin string `json:"origin"`
	}
	_ = json.Unmarshal(raw, &body)
	return proxyTestResult{status: "active", httpStatus: resp.StatusCode, egressIP: defaultStr(body.IP, body.Origin)}
}

// isProxyTransportFailure reports whether a probe error came from reaching the
// proxy itself (dial, CONNECT, auth) rather than from the probe target.
func isProxyTransportFailure(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "proxyconnect") ||
		strings.Contains(msg, "proxy authentication required") ||
		strings.Contains(msg, "socks connect")
}

// redactErrorURL drops net/http's "Get \"<url>\": " prefix, keeping only the
// transport error that explains why the probe failed.
func redactErrorURL(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return err.Error()
}

func testRelayPool(ctx context.Context, relayURL string, timeout time.Duration) proxyTestResult {
	start := time.Now()
	client := &http.Client{Timeout: timeout}

	var first proxyTestResult
	for i, target := range relayProbeTargets {
		res := probeRelay(ctx, client, relayURL, target)
		res.elapsedMS = time.Since(start).Milliseconds()
		if res.status == "active" {
			return res
		}
		if i == 0 {
			first = res
		}
		// The relay itself is down or missing (dial error, 404 from the
		// platform): a second target cannot change the verdict.
		if res.httpStatus == 0 || res.httpStatus == http.StatusNotFound {
			break
		}
	}
	first.elapsedMS = time.Since(start).Milliseconds()
	return first
}

func probeRelay(ctx context.Context, client *http.Client, relayURL, target string) proxyTestResult {
	tu, err := url.Parse(target)
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relayURL, nil)
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error()}
	}
	path := tu.EscapedPath()
	if path == "" {
		path = "/"
	}
	req.Header.Set("x-relay-target", tu.Scheme+"://"+tu.Host)
	req.Header.Set("x-relay-path", path)
	probeID := uuid.NewString()
	req.Header.Set("x-keirouter-relay-probe", probeID)
	resp, err := client.Do(req)
	if err != nil {
		return proxyTestResult{status: "error", lastError: redactErrorURL(err)}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return proxyTestResult{status: "error", lastError: fmt.Sprintf("relay returned HTTP %d", resp.StatusCode), httpStatus: resp.StatusCode}
	}
	if readErr != nil {
		return proxyTestResult{status: "error", lastError: "could not read relay probe response", httpStatus: resp.StatusCode}
	}
	var echoed struct {
		Headers map[string]any `json:"headers"`
	}
	if json.Unmarshal(raw, &echoed) != nil || !relayProbeHeaderMatches(echoed.Headers, probeID) {
		return proxyTestResult{status: "error", lastError: "relay response did not confirm request forwarding", httpStatus: resp.StatusCode}
	}
	return proxyTestResult{status: "active", httpStatus: resp.StatusCode}
}

func relayProbeHeaderMatches(headers map[string]any, probeID string) bool {
	for key, value := range headers {
		if strings.EqualFold(key, "x-keirouter-relay-probe") && fmt.Sprint(value) == probeID {
			return true
		}
	}
	return false
}
