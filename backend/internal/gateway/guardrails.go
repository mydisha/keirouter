package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/guardrails/pii"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// mountGuardrails registers the guardrail admin endpoints under /api/guardrails.
// All endpoints are loopback + session protected by the parent router; no extra
// middleware is needed here.
func (s *Server) mountGuardrails(r chi.Router) {
	if s.guardrailRepo == nil {
		// Guardrails not wired (older app build). Endpoints respond 501 so the
		// frontend degrades gracefully instead of seeing 404 + retry storms.
		r.HandleFunc("/guardrails*", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotImplemented, "guardrails not wired")
		})
		return
	}
	r.Get("/guardrails", s.adminListGuardrails)
	r.Post("/guardrails", s.adminCreateGuardrail)
	r.Get("/guardrails/effective", s.adminEffectiveGuardrail)
	r.Get("/guardrails/entities", s.adminListGuardrailEntities)
	r.Get("/guardrails/capabilities", s.adminGuardrailCapabilities)
	r.Get("/guardrails/templates", s.adminListGuardrailTemplates)
	r.Get("/guardrails/export", s.adminExportGuardrails)
	r.Post("/guardrails/import", s.adminImportGuardrails)
	r.Get("/guardrails/tenant-flags", s.adminGetGuardrailTenantFlags)
	r.Put("/guardrails/tenant-flags", s.adminPutGuardrailTenantFlags)
	r.Get("/guardrails/stats", s.adminGuardrailStats)
	r.Get("/guardrails/logs", s.adminListGuardrailLogs)
	r.Get("/guardrails/logs/stream", s.adminGuardrailLogStream)
	r.Post("/guardrails/test", s.adminTestGuardrail)
	r.Get("/guardrails/{id}", s.adminGetGuardrail)
	r.Patch("/guardrails/{id}", s.adminUpdateGuardrail)
	r.Delete("/guardrails/{id}", s.adminDeleteGuardrail)
}

// guardrailDTO is the JSON shape exchanged with the dashboard.
type guardrailDTO struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Scope     string            `json:"scope"`
	ScopeID   string            `json:"scope_id"`
	Enabled   bool              `json:"enabled"`
	Config    guardrails.Policy `json:"config"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

func toDTO(p store.GuardrailPolicy) guardrailDTO {
	cfg, _ := guardrails.UnmarshalPolicy(p.Config)
	return guardrailDTO{
		ID:        p.ID,
		Name:      p.Name,
		Scope:     string(p.Scope),
		ScopeID:   p.ScopeID,
		Enabled:   p.Enabled,
		Config:    cfg,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) adminListGuardrails(w http.ResponseWriter, r *http.Request) {
	scope := store.GuardrailScope(strings.TrimSpace(r.URL.Query().Get("scope")))
	rows, err := s.guardrailRepo.List(r.Context(), store.DefaultTenantID, scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list guardrails: "+err.Error())
		return
	}
	out := make([]guardrailDTO, 0, len(rows))
	for _, p := range rows {
		out = append(out, toDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"guardrails": out})
}

func (s *Server) adminGetGuardrail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.guardrailRepo.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "guardrail not found")
		return
	}
	writeJSON(w, http.StatusOK, toDTO(p))
}

type guardrailWriteInput struct {
	Name    string             `json:"name"`
	Scope   string             `json:"scope"`
	ScopeID string             `json:"scope_id"`
	Enabled *bool              `json:"enabled,omitempty"`
	Config  *guardrails.Policy `json:"config,omitempty"`
}

func (s *Server) adminCreateGuardrail(w http.ResponseWriter, r *http.Request) {
	var in guardrailWriteInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	scope := store.GuardrailScope(strings.TrimSpace(in.Scope))
	if !isValidScope(scope) {
		writeError(w, http.StatusBadRequest, "invalid scope: "+in.Scope)
		return
	}
	scopeID := strings.TrimSpace(in.ScopeID)
	if scope == store.GuardrailScopeGlobal {
		scopeID = ""
	} else if scopeID == "" {
		writeError(w, http.StatusBadRequest, "scope_id required for non-global scope")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		// Auto-name policies users don't bother naming.
		in.Name = defaultPolicyName(scope, scopeID)
	}

	cfg := guardrails.Policy{}
	if in.Config != nil {
		cfg = *in.Config
	}
	cfgJSON, err := guardrails.MarshalPolicy(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := time.Now().UTC()
	p := store.GuardrailPolicy{
		ID:        newGuardrailID(),
		TenantID:  store.DefaultTenantID,
		Scope:     scope,
		ScopeID:   scopeID,
		Name:      in.Name,
		Enabled:   enabled,
		Config:    cfgJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.guardrailRepo.Upsert(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, "save guardrail: "+err.Error())
		return
	}
	s.invalidateGuardrail(p.TenantID, p.Scope, p.ScopeID)

	// Upsert may have hit an existing row; re-fetch by scope to return the canonical row.
	final, err := s.guardrailRepo.GetByScope(r.Context(), store.DefaultTenantID, scope, scopeID)
	if err != nil {
		final = p
	}
	writeJSON(w, http.StatusOK, toDTO(final))
}

func (s *Server) adminUpdateGuardrail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.guardrailRepo.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "guardrail not found")
		return
	}
	var in guardrailWriteInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Name) != "" {
		existing.Name = in.Name
	}
	if in.Enabled != nil {
		existing.Enabled = *in.Enabled
	}
	if in.Config != nil {
		cfgJSON, err := guardrails.MarshalPolicy(*in.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
			return
		}
		existing.Config = cfgJSON
	}
	existing.UpdatedAt = time.Now().UTC()
	if err := s.guardrailRepo.Upsert(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, "save guardrail: "+err.Error())
		return
	}
	s.invalidateGuardrail(existing.TenantID, existing.Scope, existing.ScopeID)
	writeJSON(w, http.StatusOK, toDTO(existing))
}

func (s *Server) adminDeleteGuardrail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.guardrailRepo.Get(r.Context(), id)
	if err == nil {
		s.invalidateGuardrail(existing.TenantID, existing.Scope, existing.ScopeID)
	}
	if err := s.guardrailRepo.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete guardrail: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminEffectiveGuardrail returns the merged effective policy for the given
// scope dimensions. Used by the dashboard's "what will actually apply" preview.
func (s *Server) adminEffectiveGuardrail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := guardrails.Key{
		TenantID: store.DefaultTenantID,
		Provider: q.Get("provider"),
		Model:    q.Get("model"),
		ChainID:  q.Get("chain"),
		APIKeyID: q.Get("apikey"),
	}
	policy := s.guardrails.EffectivePolicy(r.Context(), key)
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":  key,
		"policy": policy,
	})
}

// adminListGuardrailEntities returns the PII entity catalog so the dashboard
// can render its multi-select without hardcoding the list.
func (s *Server) adminListGuardrailEntities(w http.ResponseWriter, _ *http.Request) {
	entities := pii.AllEntities()
	out := make([]string, len(entities))
	for i, e := range entities {
		out[i] = string(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": out})
}

func (s *Server) adminListGuardrailLogs(w http.ResponseWriter, r *http.Request) {
	if s.guardrailLogs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": []any{}})
		return
	}
	q := r.URL.Query()
	f := store.GuardrailLogFilter{
		APIKeyID:  q.Get("api_key_id"),
		Detector:  q.Get("detector"),
		Action:    q.Get("action"),
		Direction: q.Get("direction"),
		Model:     q.Get("model"),
		ChainID:   q.Get("chain_id"),
		RequestID: q.Get("request_id"),
	}
	// period reuses the dashboard's window names (24h, 7d, 30d, ...) so a
	// drill-down from the stats view lists the same rows it counted.
	if period := q.Get("period"); period != "" {
		f.Since = sinceForPeriod(period, q.Get("tz"))
	}
	if v := q.Get("limit"); v != "" {
		// best-effort parse; the repo clamps to a sane upper bound
		var n int
		_, _ = parseIntInto(v, &n)
		f.Limit = n
	}
	rows, err := s.guardrailLogs.List(r.Context(), store.DefaultTenantID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list guardrail logs: "+err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		var findings any
		_ = json.Unmarshal([]byte(e.Findings), &findings)
		out = append(out, map[string]any{
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
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": out})
}

// adminTestGuardrail dry-runs the engine against caller-supplied text and
// returns the decisions without touching any provider. Used by the dashboard's
// test panel for tuning policies.
type testGuardrailInput struct {
	Text   string             `json:"text"`
	Config *guardrails.Policy `json:"config,omitempty"`
}

// guardrailTestBurst is how many dry runs a dashboard session may make per
// minute.
const guardrailTestBurst = 30

func (s *Server) adminTestGuardrail(w http.ResponseWriter, r *http.Request) {
	// Rate-limit the test endpoint per session. A dry run can reach paid
	// engines (LLM judge, OpenAI Moderation, Presidio), so a stolen dashboard
	// cookie must not turn it into a free classification API. The bucket
	// allows a burst of guardrailTestBurst runs and refills one run every
	// two seconds, which keeps interactive tuning unthrottled.
	if s.guardrailTestRL != nil {
		if ok, wait := s.guardrailTestRL.allow(rateLimitKeyFor(r)); !ok {
			secs := max(int(math.Ceil(wait.Seconds())), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeError(w, http.StatusTooManyRequests, fmt.Sprintf(
				"test limit reached (%d runs per minute); try again in %ds", guardrailTestBurst, secs))
			return
		}
	}
	var in testGuardrailInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if in.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	// Synthetic request. Metadata is tagged with a test-* request id and a
	// "test-panel" api key id so audit log rows from the dashboard's test
	// panel are distinguishable from real traffic. The engine's logDecision
	// uses these fields verbatim.
	req := &core.ChatRequest{
		Metadata: core.RequestMetadata{
			TenantID:  store.DefaultTenantID,
			APIKeyID:  "test-panel",
			RequestID: store.GuardrailTestRequestPrefix + newGuardrailID()[3:11],
		},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: in.Text}}},
		},
	}
	// If the caller supplied an inline policy, run a one-off engine to avoid
	// touching the stored ones. Otherwise use the live engine + tenant defaults.
	engine := s.guardrails
	if in.Config != nil {
		// Reuse the live audit writer so test-panel runs show up in the
		// dashboard's Audit Logs tab alongside real traffic.
		engine = guardrails.NewEngine(guardrails.EngineConfig{
			Resolver:  guardrails.NewResolver(&staticResolver{pol: *in.Config}, 0),
			Audit:     s.guardrails.Audit(),
			Detectors: s.guardrails.Detectors(),
			Logger:    s.log,
		})
	}
	writeJSON(w, http.StatusOK, dryRunGuardrails(r.Context(), engine, req, in.Text))
	// Flush the audit writer so the row from this test run appears in the
	// dashboard immediately (the ticker would otherwise hold it ~1s).
	if a := s.guardrails.Audit(); a != nil {
		a.FlushNow()
	}
}

// dryRunGuardrails evaluates the sample as a prompt, then as a model
// response for detectors that only inspect output (bias, or PII output
// scanning when the prompt pass didn't fire), and reports the merged result
// plus the prompt as it would reach the provider.
func dryRunGuardrails(ctx context.Context, engine *guardrails.Engine, req *core.ChatRequest, text string) map[string]any {
	res := engine.Inbound(ctx, req)
	out := map[string]any{
		"action":      res.Action,
		"reason":      res.Reason,
		"decisions":   res.Decisions,
		"masked_text": nil,
	}
	if res.Action == guardrails.ActionBlock {
		return out
	}
	if masked := req.Messages[0].TextContent(); masked != text {
		out["masked_text"] = masked
	}
	fired := map[string]bool{}
	for _, d := range res.Decisions {
		fired[d.Detector] = true
	}
	for _, d := range engine.OutboundText(ctx, req, text, fired).Decisions {
		res.Decisions = append(res.Decisions, d)
		if guardrails.StrictestAction(res.Action, d.Action) != res.Action {
			res.Action, res.Reason = d.Action, d.Reason
		}
	}
	out["action"], out["reason"], out["decisions"] = res.Action, res.Reason, res.Decisions
	return out
}

// staticResolver returns the same policy regardless of the scope tuple. Used
// only by the test endpoint so the user can preview a config in isolation.
type staticResolver struct {
	pol guardrails.Policy
}

func (s *staticResolver) GetByScope(_ context.Context, tenantID string, scope store.GuardrailScope, scopeID string) (store.GuardrailPolicy, error) {
	if scope != store.GuardrailScopeGlobal {
		return store.GuardrailPolicy{}, store.ErrNotFound
	}
	cfg, err := guardrails.MarshalPolicy(s.pol)
	if err != nil {
		return store.GuardrailPolicy{}, err
	}
	return store.GuardrailPolicy{
		TenantID: tenantID,
		Scope:    scope,
		ScopeID:  scopeID,
		Enabled:  true,
		Config:   cfg,
	}, nil
}

func (s *Server) invalidateGuardrail(tenantID string, scope store.GuardrailScope, scopeID string) {
	if s.guardrails == nil {
		return
	}
	if res := s.guardrails.Resolver(); res != nil {
		res.Invalidate(tenantID, scope, scopeID)
	}
}

func isValidScope(s store.GuardrailScope) bool {
	switch s {
	case store.GuardrailScopeGlobal,
		store.GuardrailScopeProvider,
		store.GuardrailScopeModel,
		store.GuardrailScopeChain,
		store.GuardrailScopeAPIKey:
		return true
	}
	return false
}

func defaultPolicyName(scope store.GuardrailScope, scopeID string) string {
	if scope == store.GuardrailScopeGlobal {
		return "Global Guardrails"
	}
	return string(scope) + ":" + scopeID
}

func newGuardrailID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "gp_" + hex.EncodeToString(b[:])
}

// parseIntInto is a tiny strconv-free helper for the limit query param. Returns
// the digit count consumed and any parse error. Keeps imports lean.
func parseIntInto(s string, dst *int) (int, error) {
	n := 0
	consumed := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		consumed++
	}
	*dst = n
	return consumed, nil
}
