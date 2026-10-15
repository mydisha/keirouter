package pii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Detector is the guardrails.Detector implementation for PII.
type Detector struct {
	// presidio, when non-nil, handles policies that set engine="presidio".
	// Policies with engine="" or "native" always use the in-process Go
	// recognizers. The native path stays available as a fallback when the
	// sidecar errors out.
	presidio *PresidioEngine
}

// Config configures the detector. Pass a non-nil Presidio engine to enable
// engine="presidio" routing on a per-policy basis.
type Config struct {
	Presidio *PresidioEngine
}

// New returns the native-Go PII detector with no external engines wired.
func New() *Detector { return &Detector{} }

// NewWithConfig builds a detector with optional external engines.
func NewWithConfig(cfg Config) *Detector {
	return &Detector{presidio: cfg.Presidio}
}

// maxFindings caps the findings attached to one decision so a document full
// of addresses doesn't produce a megabyte audit row.
const maxFindings = 50

// Name identifies this detector in audit logs and policy config.
func (*Detector) Name() string { return "pii" }

// Capabilities reports whether the Presidio engine is wired.
func (d *Detector) Capabilities() map[string]bool {
	return map[string]bool{"presidio": d.presidio != nil}
}

// settings is a PIIConfig with defaults applied.
type settings struct {
	cfg      *guardrails.PIIConfig
	allowed  map[Entity]bool
	blocking map[Entity]bool
	allowVal map[string]bool
	minScore float64
	strategy guardrails.PIIStrategy
}

func newSettings(cfg *guardrails.PIIConfig) settings {
	s := settings{
		cfg:      cfg,
		allowed:  entitySet(cfg.Types),
		blocking: entitySet(cfg.BlockTypes),
		minScore: cfg.MinScore,
		strategy: cfg.Strategy,
	}
	if s.minScore <= 0 {
		s.minScore = 0.5
	}
	if s.strategy == "" {
		s.strategy = guardrails.PIIStrategyRedact
	}
	// Entities that must block are detected even when the type filter
	// leaves them out.
	if s.allowed != nil {
		for e := range s.blocking {
			s.allowed[e] = true
		}
	}
	if len(cfg.AllowList) > 0 {
		s.allowVal = make(map[string]bool, len(cfg.AllowList))
		for _, v := range cfg.AllowList {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				s.allowVal[v] = true
			}
		}
	}
	return s
}

// Inbound scans the request and applies the configured strategy. Masking
// strategies rewrite every affected field in place (system prompt, each
// message, tool call arguments, tool results); block refuses the request.
func (d *Detector) Inbound(ctx context.Context, in *guardrails.InboundRequest, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.PII
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	st := newSettings(cfg)
	surfaces := inboundSurfaces(in, cfg.Scope)
	return d.evaluate(ctx, surfaces, st, "", guardrails.MutatedFieldInPlace)
}

// Outbound scans the model response for leaked PII when ScanOutput is set.
func (d *Detector) Outbound(ctx context.Context, out *guardrails.OutboundResponse, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.PII
	if cfg == nil || !cfg.Enabled || !cfg.ScanOutput {
		return nil, nil
	}
	st := newSettings(cfg)
	if out.Source != nil {
		return d.evaluate(ctx, guardrails.ResponseSurfaces(out.Source), st, "output ", guardrails.MutatedFieldInPlace)
	}
	// Streaming text: return the rewrite instead of writing it anywhere.
	text := out.Text
	surface := guardrails.TextSurface{
		Kind: guardrails.SurfaceText, Role: core.RoleAssistant, Message: -1,
		Get: func() string { return text },
		Set: func(s string) { text = s },
	}
	dec, err := d.evaluate(ctx, []guardrails.TextSurface{surface}, st, "output ", guardrails.MutatedFieldResponse)
	if dec != nil && dec.Action == guardrails.ActionMask {
		dec.Mutated = text
	}
	return dec, err
}

// inboundSurfaces selects the request fields covered by the policy scope.
func inboundSurfaces(in *guardrails.InboundRequest, scope string) []guardrails.TextSurface {
	if in.Source == nil {
		text := in.FlatText
		return []guardrails.TextSurface{{
			Kind: guardrails.SurfaceText, Role: core.RoleUser, Message: -1,
			Get: func() string { return text },
			Set: func(s string) { text = s },
		}}
	}
	all := guardrails.RequestSurfaces(in.Source)
	switch scope {
	case guardrails.PIIScopeUser:
		out := all[:0]
		for _, s := range all {
			if s.Kind == guardrails.SurfaceToolResult || ((s.Role == core.RoleUser || s.Role == core.RoleTool) && s.Kind == guardrails.SurfaceText) {
				out = append(out, s)
			}
		}
		return out
	case guardrails.PIIScopeLastUser:
		last := -1
		for i := len(in.Source.Messages) - 1; i >= 0; i-- {
			if in.Source.Messages[i].Role == core.RoleUser {
				last = i
				break
			}
		}
		out := all[:0]
		for _, s := range all {
			if s.Message == last && last >= 0 && s.Kind == guardrails.SurfaceText {
				out = append(out, s)
			}
		}
		return out
	}
	return all
}

func (d *Detector) evaluate(ctx context.Context, surfaces []guardrails.TextSurface, st settings, verb string, field guardrails.MutatedField) (*guardrails.Decision, error) {
	if len(surfaces) == 0 {
		return nil, nil
	}
	texts := make([]string, len(surfaces))
	for i, s := range surfaces {
		texts[i] = s.Get()
	}
	perSurface := d.recognizeAll(ctx, texts, st)

	var all []Match
	blocked := st.strategy == guardrails.PIIStrategyBlock
	var blockedBy []Match
	for _, ms := range perSurface {
		for _, m := range ms {
			all = append(all, m)
			if st.blocking[m.Entity] {
				blocked = true
				blockedBy = append(blockedBy, m)
			}
		}
	}
	if len(all) == 0 {
		return nil, nil
	}

	if blocked {
		reasonOn := all
		if st.strategy != guardrails.PIIStrategyBlock {
			reasonOn = blockedBy
		}
		return &guardrails.Decision{
			Action:   guardrails.ActionBlock,
			Severity: severityFor(all),
			Reason:   buildReason(verb+"blocked", reasonOn),
			Findings: toFindings(all, nil),
		}, nil
	}

	var redactions []string
	changed := false
	for i, ms := range perSurface {
		if len(ms) == 0 {
			continue
		}
		var rewritten string
		var reps []string
		if surfaces[i].Kind == guardrails.SurfaceToolArgs {
			rewritten, reps = d.maskJSON(ctx, texts[i], ms, st)
		} else {
			rewritten, reps = applyStrategy(texts[i], ms, st.strategy)
		}
		redactions = append(redactions, reps...)
		if rewritten != texts[i] {
			surfaces[i].Set(rewritten)
			changed = true
		}
	}
	if !changed {
		return &guardrails.Decision{
			Action:   guardrails.ActionWarn,
			Severity: severityFor(all),
			Reason:   buildReason(verb+"warned", all),
			Findings: toFindings(all, nil),
		}, nil
	}
	return &guardrails.Decision{
		Action:       guardrails.ActionMask,
		Severity:     severityFor(all),
		Reason:       buildReason(verb+"masked", all),
		Findings:     toFindings(all, redactions),
		MutatedField: field,
	}, nil
}

// recognizeAll returns the matches for each text. Presidio receives every
// text in a single call; the native recognizers run per text and also serve
// as the fallback when the sidecar fails.
func (d *Detector) recognizeAll(ctx context.Context, texts []string, st settings) [][]Match {
	out := make([][]Match, len(texts))
	if d.presidio != nil && strings.EqualFold(st.cfg.Engine, "presidio") {
		if res, err := d.presidio.RecognizeBatch(ctx, texts, st.allowed, st.minScore); err == nil {
			for i := range res {
				out[i] = st.filter(res[i])
			}
			return out
		}
	}
	for i, t := range texts {
		out[i] = st.filter(Recognize(t, st.allowed, st.minScore))
	}
	return out
}

// filter drops allow-listed values.
func (st settings) filter(ms []Match) []Match {
	if len(st.allowVal) == 0 {
		return ms
	}
	out := ms[:0]
	for _, m := range ms {
		if !st.allowVal[strings.ToLower(strings.TrimSpace(m.Text))] {
			out = append(out, m)
		}
	}
	return out
}

// maskJSON masks tool call arguments. A direct splice is used when it keeps
// the JSON valid; otherwise each string value is masked on its own and the
// document is re-encoded. Non-JSON (custom tool) input is masked as text.
func (d *Detector) maskJSON(ctx context.Context, raw string, ms []Match, st settings) (string, []string) {
	spliced, reps := applyStrategy(raw, ms, st.strategy)
	if !json.Valid([]byte(raw)) || json.Valid([]byte(spliced)) {
		return spliced, reps
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return spliced, reps
	}
	reps = reps[:0]
	v = d.maskValue(ctx, v, st, &reps, 0)
	b, err := json.Marshal(v)
	if err != nil {
		return raw, nil
	}
	return string(b), reps
}

func (d *Detector) maskValue(ctx context.Context, v any, st settings, reps *[]string, depth int) any {
	if depth > 64 {
		return v
	}
	switch t := v.(type) {
	case string:
		ms := st.filter(Recognize(t, st.allowed, st.minScore))
		if len(ms) == 0 {
			return t
		}
		s, r := applyStrategy(t, ms, st.strategy)
		*reps = append(*reps, r...)
		return s
	case []any:
		for i := range t {
			t[i] = d.maskValue(ctx, t[i], st, reps, depth+1)
		}
	case map[string]any:
		for k := range t {
			t[k] = d.maskValue(ctx, t[k], st, reps, depth+1)
		}
	}
	return v
}

func entitySet(types []string) map[Entity]bool {
	if len(types) == 0 {
		return nil
	}
	out := make(map[Entity]bool, len(types))
	for _, t := range types {
		if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
			out[Entity(t)] = true
		}
	}
	return out
}

// applyStrategy splices each match's replacement into text. Matches must not
// overlap (Recognize guarantees it).
func applyStrategy(text string, matches []Match, strategy guardrails.PIIStrategy) (string, []string) {
	if len(matches) == 0 {
		return text, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })

	var b strings.Builder
	b.Grow(len(text))
	cursor := 0
	redactions := make([]string, 0, len(matches))
	for _, m := range matches {
		if m.Start < cursor || m.End > len(text) {
			continue
		}
		b.WriteString(text[cursor:m.Start])
		repl := replacement(m, strategy)
		b.WriteString(repl)
		redactions = append(redactions, repl)
		cursor = m.End
	}
	b.WriteString(text[cursor:])
	return b.String(), redactions
}

// replacement renders the substitute for one match under the strategy.
func replacement(m Match, strategy guardrails.PIIStrategy) string {
	switch strategy {
	case guardrails.PIIStrategyReplace, guardrails.PIIStrategyAnonymize:
		return "<" + string(m.Entity) + ">"
	case guardrails.PIIStrategyMask:
		return maskPreservingEdges(m.Text)
	case guardrails.PIIStrategyHash:
		sum := sha256.Sum256([]byte(m.Text))
		return "<" + string(m.Entity) + ":" + hex.EncodeToString(sum[:4]) + ">"
	default: // PIIStrategyRedact
		return "<PII>"
	}
}

// maskPreservingEdges keeps the first 2 and last 2 visible characters and
// replaces the middle with '*'. For short strings (<= 4 chars) everything is
// masked to a constant token. Secrets keep no edges at all.
func maskPreservingEdges(s string) string {
	runes := []rune(s)
	if len(runes) <= 4 {
		return "<MASKED>"
	}
	out := make([]rune, len(runes))
	for i := range runes {
		switch {
		case i < 2, i >= len(runes)-2:
			out[i] = runes[i]
		default:
			out[i] = '*'
		}
	}
	return string(out)
}

// severityFor maps the highest-scoring match to a severity label.
func severityFor(matches []Match) guardrails.Severity {
	max := 0.0
	for _, m := range matches {
		if m.Score > max {
			max = m.Score
		}
	}
	switch {
	case max >= 0.85:
		return guardrails.SeverityHigh
	case max >= 0.65:
		return guardrails.SeverityMedium
	default:
		return guardrails.SeverityLow
	}
}

// buildReason produces a human-readable summary for audit and warning headers.
func buildReason(verb string, matches []Match) string {
	counts := map[Entity]int{}
	for _, m := range matches {
		counts[m.Entity]++
	}
	parts := make([]string, 0, len(counts))
	for e, c := range counts {
		parts = append(parts, string(e)+" x"+itoa(c))
	}
	sort.Strings(parts)
	return "PII " + verb + ": " + strings.Join(parts, ", ")
}

// toFindings converts internal matches into guardrails.Finding records for
// audit. Redactions is optional; when non-nil it MUST be the same length as
// matches in the order returned by applyStrategy. Secret values are never
// copied into the audit log.
func toFindings(matches []Match, redactions []string) []guardrails.Finding {
	n := min(len(matches), maxFindings)
	out := make([]guardrails.Finding, 0, n)
	for i, m := range matches[:n] {
		f := guardrails.Finding{
			Entity: string(m.Entity),
			Score:  m.Score,
			Start:  m.Start,
			End:    m.End,
		}
		switch m.Entity {
		case EntityAPIKey, EntityPassword, EntityPrivateKey:
			f.Original = maskPreservingEdges(truncate(m.Text, 12))
		default:
			f.Original = truncate(m.Text, 64)
		}
		if i < len(redactions) {
			f.Redacted = redactions[i]
		}
		out = append(out, f)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
