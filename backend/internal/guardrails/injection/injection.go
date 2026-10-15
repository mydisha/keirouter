package injection

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Detector implements guardrails.Detector for prompt injection. The
// pattern layers always run; the classifier, semantic and judge layers run
// when both the server wires them and the policy turns them on.
type Detector struct {
	classifier *Classifier
	semantic   *semanticIndex
	completer  atomic.Pointer[completerBox]
	judgeCache *resultCache[judgeVerdict]
	secret     [32]byte
	log        *slog.Logger
}

type completerBox struct{ c Completer }

// Config wires the optional model-backed layers.
type Config struct {
	// Classifier enables the ML classifier layer.
	Classifier *Classifier
	// Embedder enables the semantic layer. Pass only a real embeddings
	// model; a hashing embedder can't measure meaning.
	Embedder Embedder
	Logger   *slog.Logger
}

// New returns the prompt-injection detector with only the pattern layers.
func New() *Detector { return NewWithConfig(Config{}) }

// NewWithConfig builds a detector with optional model-backed layers.
func NewWithConfig(cfg Config) *Detector {
	d := &Detector{
		classifier: cfg.Classifier,
		semantic:   newSemanticIndex(cfg.Embedder),
		judgeCache: newResultCache[judgeVerdict](2048, 30*time.Minute),
		log:        cfg.Logger,
	}
	if d.log == nil {
		d.log = slog.Default()
	}
	_, _ = rand.Read(d.secret[:])
	return d
}

// SetCompleter enables the LLM judge layer. The gateway calls it once its
// routing is ready, since the judge runs through KeiRouter's own providers.
func (d *Detector) SetCompleter(c Completer) {
	if c != nil {
		d.completer.Store(&completerBox{c: c})
	}
}

// Capabilities reports which optional layers the server can run.
func (d *Detector) Capabilities() map[string]bool {
	return map[string]bool{
		"classifier": d.classifier != nil,
		"semantic":   d.semantic != nil,
		"judge":      d.completer.Load() != nil,
	}
}

// Name identifies this detector in policy config and audit logs.
func (*Detector) Name() string { return "injection" }

// Inbound scans the user messages (and, when enabled, tool results) for
// injection, then hardens the request (spotlighting, canary) when allowed
// through. The operator's system prompt and earlier assistant turns are
// trusted and never scanned. Hits below the severity threshold produce a
// log-only decision so tuning stays visible in the audit log.
func (d *Detector) Inbound(ctx context.Context, in *guardrails.InboundRequest, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.Injection
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	userText := in.UserText()
	hits := d.layers(ctx, cfg, userText, in.LastUserText(), "")
	if cfg.ScanToolResults {
		for _, h := range d.layers(ctx, cfg, in.ToolText(), "", "tool") {
			h.Pattern = "indirect_" + h.Pattern
			hits = append(hits, h)
		}
	}
	hits = d.judgeLayer(ctx, cfg, in, hits)

	if in.Source != nil && cfg.GuardToolCalls {
		recent := in.RecentToolText()
		taint := Aggregate(d.layers(ctx, cfg, recent, "", "tool"))
		in.Source.Metadata.UntrustedToolInput = taint == SeverityMedium || taint == SeverityHigh
	}

	dec := decide(cfg, hits)
	if in.Source != nil && (dec == nil || dec.Action != guardrails.ActionBlock) && (cfg.Spotlight || cfg.Canary) {
		if cfg.Spotlight {
			d.spotlight(in.Source)
		}
		if cfg.Canary {
			d.plantCanary(in.Source)
		}
		in.Refresh()
	}
	return dec, nil
}

func decide(cfg *guardrails.InjectionConfig, hits []Hit) *guardrails.Decision {
	if len(hits) == 0 {
		return nil
	}
	threshold := cfg.SeverityThreshold
	if threshold == "" {
		threshold = guardrails.DefaultInjectionSeverity
	}
	severity := mapSeverity(Aggregate(hits))
	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultInjectionAction
	}
	if !guardrails.MeetsThreshold(severity, threshold) {
		action = guardrails.ActionLogOnly
	}
	return &guardrails.Decision{
		Action:   action,
		Severity: severity,
		Reason:   reasonFor(hits),
		Findings: toFindings(hits),
	}
}

// layers runs the pattern layers plus the classifier and semantic layers
// the policy enables. semanticText narrows the (paid) embedding calls to the
// newest message; source labels tool output for the classifier cache.
func (d *Detector) layers(ctx context.Context, cfg *guardrails.InjectionConfig, text, semanticText, source string) []Hit {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	hits := Scan(text)
	if cfg.Classifier && d.classifier != nil {
		hits = append(hits, d.classify(ctx, cfg, text)...)
	}
	if cfg.Semantic && d.semantic != nil && d.semantic.ready() {
		st := semanticText
		if st == "" {
			st = text
		}
		thr := cfg.SemanticThreshold
		if thr <= 0 {
			thr = 0.85
		}
		sim, s, e := d.semantic.best(ctx, st)
		off := strings.LastIndex(text, st)
		if sim >= thr-0.05 && off >= 0 {
			sev := SeverityMedium
			if sim >= thr {
				sev = SeverityHigh
			}
			hits = append(hits, Hit{Pattern: "semantic_match", Severity: sev, Start: off + s, End: off + e,
				Text: fmt.Sprintf("%s (similarity %.2f)", truncate(st[s:e], 60), sim)})
		}
	}
	return hits
}

func (d *Detector) classify(ctx context.Context, cfg *guardrails.InjectionConfig, text string) []Hit {
	thr := cfg.ClassifierThreshold
	if thr <= 0 {
		thr = 0.9
	}
	scores, err := d.classifier.Score(ctx, text)
	if err != nil {
		d.log.Warn("guardrails: injection classifier failed; pattern layers still apply", "err", err)
		return nil
	}
	var out []Hit
	for _, c := range scores {
		if c.score < thr-0.2 {
			continue
		}
		sev := SeverityMedium
		if c.score >= thr {
			sev = SeverityHigh
		}
		out = append(out, Hit{Pattern: "ml_classifier", Severity: sev, Start: c.start, End: c.end,
			Text: fmt.Sprintf("%s (score %.2f)", truncate(text[c.start:c.end], 60), c.score)})
	}
	return out
}

// judgeLayer asks the LLM judge when the policy says so. In "suspicious"
// mode it only runs when another layer saw something short of high
// severity: a positive verdict confirms it, a confident negative one clears
// it to log-only. Explicit high-severity signatures are never overruled.
func (d *Detector) judgeLayer(ctx context.Context, cfg *guardrails.InjectionConfig, in *guardrails.InboundRequest, hits []Hit) []Hit {
	box := d.completer.Load()
	mode := strings.ToLower(strings.TrimSpace(cfg.Judge))
	if box == nil || cfg.JudgeModel == "" || mode == "" || mode == guardrails.JudgeOff {
		return hits
	}
	sev := Aggregate(hits)
	if mode == guardrails.JudgeSuspicious && (sev == "" || sev == SeverityHigh) {
		return hits
	}
	user := in.LastUserText()
	tool := ""
	if cfg.ScanToolResults {
		tool = in.RecentToolText()
	}
	if strings.TrimSpace(user+tool) == "" {
		return hits
	}
	key := cacheKey("judge", cfg.JudgeModel, user, tool)
	v, ok := d.judgeCache.get(key)
	if !ok {
		var err error
		var meta core.RequestMetadata
		if in.Source != nil {
			meta = in.Source.Metadata
		}
		v, err = judge(ctx, box.c, cfg.JudgeModel, meta, user, tool)
		if err != nil {
			d.log.Warn("guardrails: injection judge failed; other layers still apply", "model", cfg.JudgeModel, "err", err)
			return hits
		}
		d.judgeCache.put(key, v)
	}
	off := strings.LastIndex(in.UserText(), user)
	span := func(h Hit) Hit {
		if off >= 0 {
			h.Start, h.End = off, off+len(user)
		} else {
			h.Start, h.End = -1, -1
		}
		return h
	}
	switch {
	case v.Hijacked:
		return append(hits, span(Hit{Pattern: "llm_judge_hijacked", Severity: SeverityHigh, Text: "input took over the judge"}))
	case v.Injection && v.Confidence >= 0.5:
		s := SeverityHigh
		if v.Confidence < 0.75 {
			s = SeverityMedium
		}
		return append(hits, span(Hit{Pattern: "llm_judge", Severity: s, Text: fmt.Sprintf("%s (confidence %.2f)", v.Technique, v.Confidence)}))
	case !v.Injection && v.Confidence >= 0.75 && sev != SeverityHigh:
		// Cleared: keep the evidence for the audit log, but as low severity.
		for i := range hits {
			hits[i].Severity = SeverityLow
		}
		return append(hits, span(Hit{Pattern: "llm_judge_cleared", Severity: SeverityLow, Text: fmt.Sprintf("judged benign (confidence %.2f)", v.Confidence)}))
	}
	return hits
}

// Outbound enforces the output-side defenses: a response that repeats the
// canary has leaked the system prompt, and tool calls made after tool
// results that look injected are refused.
func (d *Detector) Outbound(_ context.Context, out *guardrails.OutboundResponse, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.Injection
	if cfg == nil || !cfg.Enabled || out.Request == nil {
		return nil, nil
	}
	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultInjectionAction
	}
	if cfg.Canary {
		token := d.canaryToken(out.Request.Metadata.TenantID)
		texts := []string{out.Text}
		if out.Source != nil {
			for _, s := range guardrails.ResponseSurfaces(out.Source) {
				if s.Kind == guardrails.SurfaceToolArgs {
					texts = append(texts, s.Get())
				}
			}
		}
		for i, t := range texts {
			if s, e, ok := leakSpan(t, token); ok {
				f := guardrails.Finding{Entity: "PROMPT_INJECTION:system_prompt_leak", Score: 1, Start: s, End: e, Original: "canary marker"}
				if i > 0 {
					f.Start, f.End = -1, -1
				}
				return &guardrails.Decision{
					Action:   action,
					Severity: guardrails.SeverityHigh,
					Reason:   "injection: response leaked the system prompt (canary)",
					Findings: []guardrails.Finding{f},
				}, nil
			}
		}
	}
	if cfg.GuardToolCalls && out.Request.Metadata.UntrustedToolInput && out.Source != nil && hasToolCalls(out.Source) {
		return ToolCallDecision(cfg), nil
	}
	return nil, nil
}

// ToolCallDecision is the refusal for a tool call made after tool results
// that look like a prompt injection.
func ToolCallDecision(cfg *guardrails.InjectionConfig) *guardrails.Decision {
	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultInjectionAction
	}
	return &guardrails.Decision{
		Action:   action,
		Severity: guardrails.SeverityHigh,
		Reason:   "injection: tool call refused after tool results that look like a prompt injection",
		Findings: []guardrails.Finding{{Entity: "PROMPT_INJECTION:tainted_tool_call", Score: 1, Start: -1, End: -1}},
	}
}

func hasToolCalls(r *core.ChatResponse) bool {
	for _, p := range r.Message.Content {
		if p.Type == core.PartToolCall {
			return true
		}
	}
	return false
}

// Aggregate turns individual hits into one severity. Independent weak
// signals reinforce each other: two different medium patterns, or a medium
// plus two lows, read as an attack; three different lows read as medium.
func Aggregate(hits []Hit) Severity {
	seen := map[string]Severity{}
	for _, h := range hits {
		if rank(h.Severity) > rank(seen[h.Pattern]) {
			seen[h.Pattern] = h.Severity
		}
	}
	var high, medium, low int
	for _, s := range seen {
		switch s {
		case SeverityHigh:
			high++
		case SeverityMedium:
			medium++
		case SeverityLow:
			low++
		}
	}
	switch {
	case high > 0, medium >= 2, medium >= 1 && low >= 2:
		return SeverityHigh
	case medium > 0, low >= 3:
		return SeverityMedium
	case low > 0:
		return SeverityLow
	}
	return ""
}

// Scan returns every hit in the text: signatures on the normalized text,
// intent rules on the word view (plus its rot13 and reversed forms), and the
// same checks on decoded base64 payloads. Offsets index into text.
func Scan(text string) []Hit {
	return scan(text, true)
}

func scan(text string, decode bool) []Hit {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var hits []Hit
	lower := guardrails.Fold(text, guardrails.FoldOptions{Lower: true})
	for _, sig := range signatures {
		if !containsAny(lower.Text, sig.Any) {
			continue
		}
		for _, span := range sig.Regex.FindAllStringIndex(lower.Text, -1) {
			s, e := lower.Span(span[0], span[1])
			if s < 0 {
				continue
			}
			hits = append(hits, Hit{Pattern: sig.Name, Severity: sig.Severity, Start: s, End: e, Text: text[s:e]})
		}
	}

	words := guardrails.Fold(text, guardrails.FoldOptions{Lower: true, Words: true})
	add := func(h Hit, s, e int) {
		os, oe := words.Span(s, e)
		if os < 0 || overlaps(hits, os, oe) {
			return
		}
		h.Start, h.End, h.Text = os, oe, text[os:oe]
		hits = append(hits, h)
	}
	for _, h := range matchIntents(tokenize(words.Text)) {
		add(h, h.Start, h.End)
	}
	for _, h := range matchIntents(tokenize(rot13(words.Text))) {
		h.Pattern = "rot13_" + h.Pattern
		add(h, h.Start, h.End)
	}
	n := len(words.Text)
	for _, h := range matchIntents(tokenize(reverseRunes(words.Text))) {
		h.Pattern = "reversed_" + h.Pattern
		add(h, n-h.End, n-h.Start)
	}

	if decode {
		for _, blob := range decodedPayloads(text) {
			for _, h := range scan(blob.text, false) {
				h.Pattern = "encoded_" + h.Pattern
				h.Start, h.End = blob.start, blob.end
				h.Text = h.Text + " (base64)"
				hits = append(hits, h)
			}
		}
	}
	return hits
}

func containsAny(s string, lits []string) bool {
	if len(lits) == 0 {
		return true
	}
	for _, l := range lits {
		if strings.Contains(s, l) {
			return true
		}
	}
	return false
}

func overlaps(hits []Hit, s, e int) bool {
	for _, h := range hits {
		if h.Start < e && s < h.End {
			return true
		}
	}
	return false
}

var base64Blob = regexp.MustCompile(`[A-Za-z0-9+/_\-]{24,}={0,2}`)

const (
	maxDecodedBlobs = 16
	maxBlobBytes    = 16 << 10
)

type payload struct {
	text       string
	start, end int
}

// decodedPayloads finds base64 runs that decode to readable text. Binary
// data (images, hashes, keys) fails the readability check and is skipped.
func decodedPayloads(text string) []payload {
	var out []payload
	for _, span := range base64Blob.FindAllStringIndex(text, -1) {
		if len(out) >= maxDecodedBlobs {
			break
		}
		raw := text[span[0]:span[1]]
		if len(raw) > maxBlobBytes {
			continue
		}
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			b, err := enc.DecodeString(raw)
			if err != nil {
				continue
			}
			if readable(b) {
				out = append(out, payload{text: string(b), start: span[0], end: span[1]})
			}
			break
		}
	}
	return out
}

func readable(b []byte) bool {
	if len(b) < 12 || !utf8.Valid(b) {
		return false
	}
	printable, spaces, total := 0, 0, 0
	for _, r := range string(b) {
		total++
		if r == ' ' {
			spaces++
		}
		if unicode.IsPrint(r) || r == '\n' || r == '\t' {
			printable++
		}
	}
	return spaces > 0 && printable*100 >= total*90
}

func mapSeverity(s Severity) guardrails.Severity {
	switch s {
	case SeverityHigh:
		return guardrails.SeverityHigh
	case SeverityMedium:
		return guardrails.SeverityMedium
	case SeverityLow:
		return guardrails.SeverityLow
	}
	return ""
}

// reasonFor builds a stable, human-readable summary: "injection: <names>".
func reasonFor(hits []Hit) string {
	names := make(map[string]struct{}, len(hits))
	for _, h := range hits {
		names[h.Pattern] = struct{}{}
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	return "injection: " + strings.Join(list, ", ")
}

func toFindings(hits []Hit) []guardrails.Finding {
	out := make([]guardrails.Finding, 0, len(hits))
	for _, h := range hits {
		out = append(out, guardrails.Finding{
			Entity:   "PROMPT_INJECTION:" + h.Pattern,
			Score:    score(h.Severity),
			Start:    h.Start,
			End:      h.End,
			Original: truncate(h.Text, 96),
		})
	}
	return out
}

func score(s Severity) float64 {
	switch s {
	case SeverityHigh:
		return 0.9
	case SeverityMedium:
		return 0.7
	case SeverityLow:
		return 0.5
	}
	return 0.0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
