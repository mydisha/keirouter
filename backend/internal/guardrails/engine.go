package guardrails

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// MetricsRecorder is the minimal Prometheus sink the engine emits to. It maps
// directly to observ.Metrics. Keeping it as an interface avoids dragging the
// observ package into the guardrails dependency graph.
type MetricsRecorder interface {
	RecordGuardrailDecision(detector, action string)
	RecordGuardrailLatency(detector string, seconds float64)
}

// TenantPolicy lets the engine enforce per-tenant overrides that are NOT part
// of a detector policy — currently just the GDPR "allow_external_engines"
// switch which forces detectors back to their native engine when set false.
type TenantPolicy interface {
	AllowExternalEngines(ctx context.Context, tenantID string) bool
}

// Engine orchestrates registered detectors against a request. It runs them
// in registration order, stops on the first block, applies masks in-place,
// and emits audit log rows. A nil engine is a valid "guardrails disabled"
// state — all methods are nil-safe.
type Engine struct {
	resolver  *Resolver
	audit     *AuditWriter
	detectors []Detector
	log       *slog.Logger
	metrics   MetricsRecorder
	tenant    TenantPolicy
}

// EngineConfig collects engine dependencies.
type EngineConfig struct {
	Resolver  *Resolver
	Audit     *AuditWriter
	Detectors []Detector
	Logger    *slog.Logger
	// Metrics is optional; when set, the engine records per-detector decision
	// counts + evaluation latency.
	Metrics MetricsRecorder
	// TenantPolicy is optional; when set, the engine enforces per-tenant
	// overrides like the GDPR allow_external_engines switch before running
	// detectors.
	TenantPolicy TenantPolicy
}

// NewEngine builds an engine. Detectors run in slice order; callers should
// register the cheaper/faster detectors first (PII regex before any ML).
func NewEngine(cfg EngineConfig) *Engine {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		resolver:  cfg.Resolver,
		audit:     cfg.Audit,
		detectors: cfg.Detectors,
		log:       log,
		metrics:   cfg.Metrics,
		tenant:    cfg.TenantPolicy,
	}
}

// Result is what the engine reports back to the pipeline for a single
// direction (inbound or outbound). Action is the strictest action across all
// detectors; Decisions is the per-detector breakdown for audit + headers.
type Result struct {
	Action    Action
	Reason    string
	Decisions []Decision
	// Text is the inspected text after every mask was applied. It is set by
	// OutboundChunk, where the caller forwards the rewritten text itself.
	Text string
}

func (r *Result) add(dec Decision) {
	r.Decisions = append(r.Decisions, dec)
	if rank(dec.Action) > rank(r.Action) {
		r.Action = dec.Action
		r.Reason = dec.Reason
	}
}

// policyFor resolves the effective policy for a live request.
func (e *Engine) policyFor(ctx context.Context, req *core.ChatRequest) Policy {
	policy := e.resolver.Effective(ctx, e.keyFor(req))
	return e.applyTenantOverrides(ctx, req.Metadata.TenantID, policy)
}

// Inbound runs all detectors against the incoming request. If any detector
// produces Action == ActionBlock the engine stops and returns immediately
// (further detectors don't run; the request will be refused). Masking
// detectors rewrite the request fields in place.
func (e *Engine) Inbound(ctx context.Context, req *core.ChatRequest) Result {
	if e == nil || req == nil || bypassed(ctx) {
		return Result{Action: ActionAllow}
	}
	policy := e.policyFor(ctx, req)
	if !policy.IsActive() {
		return Result{Action: ActionAllow}
	}

	attachOutcome(req)
	in := &InboundRequest{Source: req, FlatText: flattenRequest(req)}
	final := Result{Action: ActionAllow}

	for _, d := range e.detectors {
		start := time.Now()
		dec, err := d.Inbound(ctx, in, policy)
		e.recordLatency(req, d.Name(), start)
		if err != nil {
			e.log.Warn("guardrail detector error (inbound)", "detector", d.Name(), "err", err)
			continue
		}
		if dec == nil || dec.Action == "" || dec.Action == ActionAllow {
			e.recordDecision(d.Name(), ActionAllow)
			continue
		}
		dec.Detector = d.Name()
		dec.Direction = DirectionInbound
		e.report(req, dec)
		final.add(*dec)

		if dec.Action == ActionMask {
			if dec.MutatedField != MutatedFieldInPlace && dec.Mutated != "" {
				applyMutation(req, dec)
			}
			// Later detectors must see the rewritten content.
			in.Refresh()
		}
		if dec.Action == ActionBlock {
			return final
		}
	}
	return final
}

// Outbound runs detectors against a finalized (non-streaming) response.
// Streaming output goes through a StreamGuard instead.
func (e *Engine) Outbound(ctx context.Context, req *core.ChatRequest, resp *core.ChatResponse) Result {
	if e == nil || req == nil || resp == nil || bypassed(ctx) {
		return Result{Action: ActionAllow}
	}
	policy := e.policyFor(ctx, req)
	if !policy.IsActive() {
		return Result{Action: ActionAllow}
	}
	attachOutcome(req)
	out := &OutboundResponse{Source: resp, Request: req, Text: flattenResponse(resp), Streaming: false}
	res := e.evalOutbound(ctx, policy, out, true)
	for i := range res.Decisions {
		e.report(req, &res.Decisions[i])
	}
	return res
}

// OutboundChunk runs the outbound detectors against a piece of streamed text.
// Result.Text carries the text with every mask applied; on Block the caller
// must stop the stream. Prefer NewStreamGuard, which also handles matches that
// straddle chunk boundaries.
func (e *Engine) OutboundChunk(ctx context.Context, req *core.ChatRequest, text string) Result {
	if e == nil || req == nil || text == "" || bypassed(ctx) {
		return Result{Action: ActionAllow, Text: text}
	}
	policy := e.policyFor(ctx, req)
	if !policy.IsActive() {
		return Result{Action: ActionAllow, Text: text}
	}
	attachOutcome(req)
	res := e.evalOutbound(ctx, policy, &OutboundResponse{Request: req, Text: text, Streaming: true}, false)
	for i := range res.Decisions {
		e.report(req, &res.Decisions[i])
	}
	return res
}

// OutboundText evaluates text as a model response, skipping the detectors
// listed in skip. Only the kept decisions are audited. The dashboard's test
// panel uses it to exercise output-only detectors on a sample prompt.
func (e *Engine) OutboundText(ctx context.Context, req *core.ChatRequest, text string, skip map[string]bool) Result {
	if e == nil || req == nil || text == "" || bypassed(ctx) {
		return Result{Action: ActionAllow, Text: text}
	}
	policy := e.policyFor(ctx, req)
	if !policy.IsActive() {
		return Result{Action: ActionAllow, Text: text}
	}
	attachOutcome(req)
	all := e.evalOutbound(ctx, policy, &OutboundResponse{Request: req, Text: text, Streaming: true}, false)
	res := Result{Action: ActionAllow, Text: all.Text}
	for _, d := range all.Decisions {
		if skip[d.Detector] {
			continue
		}
		e.report(req, &d)
		res.add(d)
	}
	return res
}

// evalOutbound runs every detector's Outbound pass without writing audit
// rows. Masks are chained so each detector sees the previous rewrite.
func (e *Engine) evalOutbound(ctx context.Context, policy Policy, out *OutboundResponse, countAllow bool) Result {
	final := Result{Action: ActionAllow}
	for _, d := range e.detectors {
		start := time.Now()
		dec, err := d.Outbound(ctx, out, policy)
		e.recordLatency(out.Request, d.Name(), start)
		if err != nil {
			e.log.Warn("guardrail detector error (outbound)", "detector", d.Name(), "err", err)
			continue
		}
		if dec == nil || dec.Action == "" || dec.Action == ActionAllow {
			if countAllow {
				e.recordDecision(d.Name(), ActionAllow)
			}
			continue
		}
		dec.Detector = d.Name()
		dec.Direction = DirectionOutbound
		final.add(*dec)
		if dec.Action == ActionMask {
			switch {
			case dec.MutatedField == MutatedFieldInPlace && out.Source != nil:
				out.Text = flattenResponse(out.Source)
			case dec.Mutated != "":
				if out.Source != nil {
					applyResponseMutation(out.Source, dec)
				}
				out.Text = dec.Mutated
			}
		}
		if dec.Action == ActionBlock {
			break
		}
	}
	final.Text = out.Text
	return final
}

// report records metrics, folds the decision into the request's outcome, and
// writes the audit row for one decision.
func (e *Engine) report(req *core.ChatRequest, dec *Decision) {
	e.recordDecision(dec.Detector, dec.Action)
	req.Metadata.Guardrail.Note(string(dec.Action), rank(dec.Action), dec.Detector, string(dec.Direction))
	e.logDecision(req, dec)
}

// recordLatency charges one detector evaluation to the metrics and to the
// request's guardrail overhead.
func (e *Engine) recordLatency(req *core.ChatRequest, name string, start time.Time) {
	elapsed := time.Since(start)
	if req != nil {
		req.Metadata.Guardrail.AddElapsed(elapsed)
	}
	if e.metrics == nil {
		return
	}
	e.metrics.RecordGuardrailLatency(name, elapsed.Seconds())
}

// attachOutcome gives req a GuardrailOutcome once a policy applies to it, so
// its usage row records that guardrails ran even when nothing fired.
func attachOutcome(req *core.ChatRequest) {
	if req.Metadata.Guardrail == nil {
		req.Metadata.Guardrail = &core.GuardrailOutcome{}
	}
}

func (e *Engine) recordDecision(name string, action Action) {
	if e.metrics == nil {
		return
	}
	if action == "" {
		action = ActionAllow
	}
	e.metrics.RecordGuardrailDecision(name, string(action))
}

// applyTenantOverrides enforces tenant-wide flags before the policy reaches
// detectors. Currently it implements the GDPR allow_external_engines switch:
// when off, every engine selector is reset to its native default and the
// embedding / LLM judge layers are switched off, so no prompt text is sent to
// another service for classification.
func (e *Engine) applyTenantOverrides(ctx context.Context, tenantID string, p Policy) Policy {
	if e == nil || e.tenant == nil {
		return p
	}
	if e.tenant.AllowExternalEngines(ctx, tenantID) {
		return p
	}
	if p.PII != nil && p.PII.Engine != "" && p.PII.Engine != "native" {
		clone := *p.PII
		clone.Engine = "native"
		p.PII = &clone
	}
	if p.Toxicity != nil && p.Toxicity.Engine != "" && p.Toxicity.Engine != "native" {
		clone := *p.Toxicity
		clone.Engine = "native"
		p.Toxicity = &clone
	}
	if p.Topics != nil && p.Topics.Engine != "" && p.Topics.Engine != "keyword" {
		clone := *p.Topics
		clone.Engine = "keyword"
		p.Topics = &clone
	}
	// Embedding and judge layers send prompt text to another service.
	if p.Injection != nil && (p.Injection.Semantic || (p.Injection.Judge != "" && p.Injection.Judge != JudgeOff)) {
		clone := *p.Injection
		clone.Semantic = false
		clone.Judge = JudgeOff
		p.Injection = &clone
	}
	return p
}

// EffectivePolicy is a passthrough for the admin API and per-key UI preview.
func (e *Engine) EffectivePolicy(ctx context.Context, k Key) Policy {
	if e == nil || e.resolver == nil {
		return Policy{}
	}
	return e.resolver.Effective(ctx, k)
}

// Resolver returns the underlying resolver so the admin layer can invalidate
// cache entries after writes.
func (e *Engine) Resolver() *Resolver {
	if e == nil {
		return nil
	}
	return e.resolver
}

// Audit returns the underlying audit writer so handlers (e.g. the policy
// test endpoint) can write extra rows tagged for their use case.
func (e *Engine) Audit() *AuditWriter {
	if e == nil {
		return nil
	}
	return e.audit
}

// Detectors returns the registered detector list (for /api/system display).
func (e *Engine) Detectors() []Detector {
	if e == nil {
		return nil
	}
	return e.detectors
}

// keyFor extracts the 5-tuple from a live request. We read Model from the
// request itself (the gateway populates metadata.Provider/ChainID but the
// canonical Model lives on the request body where dispatch consumes it).
func (e *Engine) keyFor(req *core.ChatRequest) Key {
	return Key{
		TenantID: req.Metadata.TenantID,
		Provider: req.Metadata.Provider,
		Model:    req.Model,
		ChainID:  req.Metadata.ChainID,
		APIKeyID: req.Metadata.APIKeyID,
	}
}

func (e *Engine) logDecision(req *core.ChatRequest, dec *Decision) {
	if e.audit == nil {
		return
	}
	m := req.Metadata
	e.audit.Write(context.Background(), store.GuardrailLog{
		TenantID:  defaultTenant(m.TenantID),
		RequestID: m.RequestID,
		APIKeyID:  m.APIKeyID,
		Provider:  m.Provider,
		Model:     req.Model,
		ChainID:   m.ChainID,
		Detector:  dec.Detector,
		Direction: string(dec.Direction),
		Action:    string(dec.Action),
		Severity:  string(dec.Severity),
		Reason:    dec.Reason,
		Findings:  MarshalDecisionFindings(dec),
	})
}

func defaultTenant(t string) string {
	if t == "" {
		return store.DefaultTenantID
	}
	return t
}

// flattenRequest concatenates the textual content of System + Messages with
// role-tagged delimiters. Detectors operate on this single string so they
// don't have to walk the message slice. Tool calls and non-text parts are
// skipped — they aren't currently in scope for text-content guardrails.
func flattenRequest(req *core.ChatRequest) string {
	if req == nil {
		return ""
	}
	var b strings.Builder
	if req.System != "" {
		b.WriteString("<<system>>\n")
		b.WriteString(req.System)
		b.WriteString("\n")
	}
	for _, msg := range req.Messages {
		b.WriteString("<<")
		b.WriteString(string(msg.Role))
		b.WriteString(">>\n")
		b.WriteString(messageText(msg))
		b.WriteString("\n")
	}
	return b.String()
}

func flattenResponse(resp *core.ChatResponse) string {
	if resp == nil {
		return ""
	}
	return responseText(resp)
}

// applyMutation writes a detector's single-field rewrite back to the request:
// the system prompt for MutatedFieldSystem, otherwise the most recent user
// message. Detectors that rewrite several fields do it themselves and report
// MutatedFieldInPlace.
func applyMutation(req *core.ChatRequest, dec *Decision) {
	if dec == nil || req == nil {
		return
	}
	// Field-specific overrides take priority.
	switch dec.MutatedField {
	case MutatedFieldSystem:
		req.System = dec.Mutated
		return
	case MutatedFieldMessages, MutatedFieldNone:
	}

	// Default: rewrite the most recent user message.
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == core.RoleUser {
			setMessageText(&req.Messages[i], dec.Mutated)
			return
		}
	}
	// No user message — fall back to system.
	req.System = dec.Mutated
}

func applyResponseMutation(resp *core.ChatResponse, dec *Decision) {
	if dec == nil || resp == nil {
		return
	}
	setResponseText(resp, dec.Mutated)
}
