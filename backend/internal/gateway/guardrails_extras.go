package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// ---- H.1 SSE audit log stream -------------------------------------------------

// adminGuardrailLogStream serves an SSE endpoint that pushes audit log rows to
// the dashboard. Replaces the 5s polling loop in the Logs tab. Subscriber
// receives every row that lands in guardrail_logs *after* it has been
// successfully inserted (the AuditWriter only publishes after the batch
// commit).
func (s *Server) adminGuardrailLogStream(w http.ResponseWriter, r *http.Request) {
	if s.guardrailHub == nil {
		writeError(w, http.StatusServiceUnavailable, "guardrails log hub not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	listener := guardrails.NewLogListener(64)
	s.guardrailHub.Subscribe(listener)
	defer s.guardrailHub.Unsubscribe(listener)

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case row := <-listener.C:
			data, _ := json.Marshal(serializeLog(row))
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// serializeLog flattens a guardrail log into the same shape adminListGuardrailLogs
// returns so frontend clients can render rows from either source uniformly.
func serializeLog(e store.GuardrailLog) map[string]any {
	var findings any
	_ = json.Unmarshal([]byte(e.Findings), &findings)
	return map[string]any{
		"id":         e.ID,
		"request_id": e.RequestID,
		"api_key_id": e.APIKeyID,
		"provider":   e.Provider,
		"model":      e.Model,
		"chain_id":   e.ChainID,
		"detector":   e.Detector,
		"direction":  e.Direction,
		"action":     e.Action,
		"severity":   e.Severity,
		"reason":     e.Reason,
		"findings":   findings,
		"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// ---- H.3 Policy import/export -------------------------------------------------

// guardrailBundle is the on-wire shape for import/export. Versioning lives in
// the top-level "version" field so future changes can add a migration path
// without breaking older bundles.
type guardrailBundle struct {
	Version  int                `json:"version"`
	Exported string             `json:"exported_at"`
	Policies []guardrailExport `json:"policies"`
}

// guardrailExport is one policy as exported. Scope/ScopeID are preserved so
// imports can recreate per-provider/per-model/per-chain bindings; for per-key
// policies the scope_id may not exist on the target tenant — those rows are
// imported but the binding is invalid until the key is recreated. The
// import endpoint surfaces a per-row note when that happens.
type guardrailExport struct {
	Name    string             `json:"name"`
	Scope   string             `json:"scope"`
	ScopeID string             `json:"scope_id,omitempty"`
	Enabled bool               `json:"enabled"`
	Config  guardrails.Policy `json:"config"`
}

func (s *Server) adminExportGuardrails(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	rows, err := s.guardrailRepo.List(r.Context(), store.DefaultTenantID, store.GuardrailScope(scope))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list guardrails: "+err.Error())
		return
	}
	bundle := guardrailBundle{
		Version:  1,
		Exported: time.Now().UTC().Format(time.RFC3339),
		Policies: make([]guardrailExport, 0, len(rows)),
	}
	for _, row := range rows {
		cfg, _ := guardrails.UnmarshalPolicy(row.Config)
		bundle.Policies = append(bundle.Policies, guardrailExport{
			Name:    row.Name,
			Scope:   string(row.Scope),
			ScopeID: row.ScopeID,
			Enabled: row.Enabled,
			Config:  cfg,
		})
	}
	w.Header().Set("Content-Disposition", `attachment; filename="guardrails-export.json"`)
	writeJSON(w, http.StatusOK, bundle)
}

type importResult struct {
	Imported []map[string]any `json:"imported"`
	Skipped  []map[string]any `json:"skipped"`
}

func (s *Server) adminImportGuardrails(w http.ResponseWriter, r *http.Request) {
	var bundle guardrailBundle
	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if bundle.Version != 1 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported bundle version: %d (expected 1)", bundle.Version))
		return
	}
	out := importResult{}
	for _, p := range bundle.Policies {
		scope := store.GuardrailScope(p.Scope)
		cfgJSON, err := guardrails.MarshalPolicy(p.Config)
		if err != nil {
			out.Skipped = append(out.Skipped, map[string]any{"name": p.Name, "reason": "marshal: " + err.Error()})
			continue
		}
		row := store.GuardrailPolicy{
			ID:       newGuardrailID(),
			TenantID: store.DefaultTenantID,
			Name:     p.Name,
			Scope:    scope,
			ScopeID:  p.ScopeID,
			Enabled:  p.Enabled,
			Config:   cfgJSON,
		}
		if err := s.guardrailRepo.Upsert(r.Context(), row); err != nil {
			out.Skipped = append(out.Skipped, map[string]any{"name": p.Name, "reason": err.Error()})
			continue
		}
		s.guardrails.Resolver().Invalidate(store.DefaultTenantID, scope, p.ScopeID)
		out.Imported = append(out.Imported, map[string]any{"name": p.Name, "scope": p.Scope, "scope_id": p.ScopeID})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- H.5 Built-in policy templates -------------------------------------------

// adminListGuardrailTemplates returns curated starter policies for the
// dashboard's "From template" picker.
func (s *Server) adminListGuardrailTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"templates": guardrailTemplates()})
}

// ---- I.3 GDPR tenant flag endpoints ------------------------------------------

// adminGetGuardrailTenantFlags returns the current tenant guardrails flags so
// the dashboard can render a toggle. Currently only allow_external_engines is
// surfaced; the response is a map so future flags can be added without a
// schema change.
func (s *Server) adminGetGuardrailTenantFlags(w http.ResponseWriter, r *http.Request) {
	flags := map[string]any{
		"allow_external_engines": true,
	}
	if s.guardrailTenantFlag != nil {
		flags["allow_external_engines"] = s.guardrailTenantFlag.AllowExternalEngines(r.Context(), store.DefaultTenantID)
	}
	writeJSON(w, http.StatusOK, flags)
}

func (s *Server) adminPutGuardrailTenantFlags(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "settings store not configured")
		return
	}
	var in struct {
		AllowExternalEngines *bool `json:"allow_external_engines,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if in.AllowExternalEngines != nil {
		v := "true"
		if !*in.AllowExternalEngines {
			v = "false"
		}
		key := "guardrails.allow_external_engines:" + store.DefaultTenantID
		if err := s.settings.Set(r.Context(), key, v); err != nil {
			writeError(w, http.StatusInternalServerError, "save flag: "+err.Error())
			return
		}
		if s.guardrailTenantFlag != nil {
			s.guardrailTenantFlag.Invalidate(store.DefaultTenantID)
		}
	}
	s.adminGetGuardrailTenantFlags(w, r)
}

// ---- I.4 test endpoint rate limiter ------------------------------------------

// guardrailTestRateLimiter is a tiny per-key token-bucket. We keep one bucket
// per session/IP key, refill at rate `capacity / window`, cap at `capacity`.
// Locking is coarse (single mutex) — the test endpoint is interactive and
// expected traffic is low, so this stays simpler than sync.Map.
type guardrailTestRateLimiter struct {
	capacity int
	window   time.Duration

	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens   float64
	last     time.Time
}

func newGuardrailTestRateLimiter(capacity int, window time.Duration) *guardrailTestRateLimiter {
	if capacity <= 0 {
		capacity = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &guardrailTestRateLimiter{
		capacity: capacity,
		window:   window,
		buckets:  make(map[string]*rateBucket),
	}
}

// allow consumes one token from the bucket for key. When the bucket is
// empty it returns false and how long until the next token is available.
func (l *guardrailTestRateLimiter) allow(key string) (bool, time.Duration) {
	if l == nil || key == "" {
		return true, 0
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &rateBucket{tokens: float64(l.capacity), last: now}
		l.buckets[key] = b
	}
	// Refill since last call.
	perSecond := float64(l.capacity) / l.window.Seconds()
	b.tokens += now.Sub(b.last).Seconds() * perSecond
	if b.tokens > float64(l.capacity) {
		b.tokens = float64(l.capacity)
	}
	b.last = now
	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) / perSecond * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// rateLimitKeyFor derives a per-caller bucket key. We prefer the dashboard
// session cookie when present so legitimate dashboard users share one bucket
// across tabs; fall back to the remote IP when the request is sessionless.
func rateLimitKeyFor(r *http.Request) string {
	if c, err := r.Cookie("keirouter_session"); err == nil && c.Value != "" {
		return "sess:" + c.Value
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		addr = addr[:i]
	}
	return "ip:" + addr
}
