// Package toxicity is KeiRouter's toxicity detector. It supports two engines
// selected per-policy via ToxicityConfig.Engine: "native" (default, offline
// weighted lexicon covering id + en) and "openai" (OpenAI Moderation API).
// Both implement the same internal engine contract and produce findings
// with category-named entities (TOXICITY:hate, TOXICITY:harassment, ...).
package toxicity

import (
	"context"
	"sort"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Detector implements guardrails.Detector for toxicity.
type Detector struct {
	native engine
	openai engine
}

// Config configures the detector at construction time.
type Config struct {
	// OpenAI, if non-nil, enables the OpenAI Moderation engine. When the
	// policy selects engine="openai" but this is nil, the detector falls
	// back to native and surfaces no error (still safe to ship).
	OpenAI *OpenAIConfig
}

// New builds a toxicity detector. Native engine is always available; OpenAI
// engine is wired only when cfg.OpenAI is non-nil.
func New(cfg Config) *Detector {
	d := &Detector{native: newNativeEngine()}
	if cfg.OpenAI != nil {
		d.openai = newOpenAIEngine(*cfg.OpenAI)
	}
	return d
}

// Name identifies this detector in policy config and audit logs.
func (Detector) Name() string { return "toxicity" }

// Capabilities reports whether the OpenAI Moderation engine is wired.
func (d *Detector) Capabilities() map[string]bool {
	return map[string]bool{"openai": d.openai != nil}
}

// Inbound scans the user messages and returns a decision when the configured
// threshold is exceeded for any allowed category. The operator's system
// prompt is not scored (it may legitimately quote words it forbids).
func (d *Detector) Inbound(ctx context.Context, in *guardrails.InboundRequest, p guardrails.Policy) (*guardrails.Decision, error) {
	return d.scan(ctx, in.UserText(), p)
}

// Outbound scans the LLM response. Same routing as inbound — toxicity reads
// the same policy config for both directions.
func (d *Detector) Outbound(ctx context.Context, out *guardrails.OutboundResponse, p guardrails.Policy) (*guardrails.Decision, error) {
	return d.scan(ctx, out.Text, p)
}

func (d *Detector) scan(ctx context.Context, text string, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.Toxicity
	if cfg == nil || !cfg.Enabled || strings.TrimSpace(text) == "" {
		return nil, nil
	}

	eng := d.pickEngine(cfg.Engine)
	allowed := allowedCategories(cfg.Categories)
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = guardrails.DefaultToxicityThreshold
	}

	scores, err := eng.Score(ctx, text, allowed)
	if err != nil && eng != engine(d.native) {
		// A remote engine failure (OpenAI 5xx, timeout) falls back to the
		// offline lexicon so a provider hiccup never switches the check off.
		scores, err = d.native.Score(ctx, text, allowed)
	}
	if err != nil {
		return nil, err
	}
	hits := filterByThreshold(scores, threshold)
	if len(hits) == 0 {
		return nil, nil
	}

	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultToxicityAction
	}
	return &guardrails.Decision{
		Action:   action,
		Severity: severityFor(hits),
		Reason:   reasonFor(hits),
		Findings: toFindings(hits),
	}, nil
}

func (d *Detector) pickEngine(name string) engine {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		if d.openai != nil {
			return d.openai
		}
	}
	return d.native
}

// allowedCategories normalizes the policy's category list to a lookup set.
// An empty list means "all categories".
func allowedCategories(cats []string) map[string]bool {
	if len(cats) == 0 {
		return nil
	}
	out := make(map[string]bool, len(cats))
	for _, c := range cats {
		out[canonicalCategory(c)] = true
	}
	return out
}

// canonicalCategory folds the dashboard and API spellings onto the internal
// category names.
func canonicalCategory(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	c = strings.NewReplacer("-", "_", " ", "_", "/", "_").Replace(c)
	switch c {
	case "hate_speech", "hateful":
		return "hate"
	case "selfharm", "self_harm_intent", "suicide":
		return "self_harm"
	case "sexual_content", "nsfw":
		return "sexual"
	}
	return c
}

// filterByThreshold drops scores below threshold. The result is sorted by
// score descending for stable display.
func filterByThreshold(scores []CategoryScore, threshold int) []CategoryScore {
	out := scores[:0]
	for _, s := range scores {
		if s.Score >= threshold {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func severityFor(hits []CategoryScore) guardrails.Severity {
	max := 0
	for _, h := range hits {
		if h.Score > max {
			max = h.Score
		}
	}
	switch {
	case max >= 85:
		return guardrails.SeverityHigh
	case max >= 70:
		return guardrails.SeverityMedium
	default:
		return guardrails.SeverityLow
	}
}

func reasonFor(hits []CategoryScore) string {
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, h.Category)
	}
	return "toxicity: " + strings.Join(parts, ", ")
}

// maxFindings caps the findings per decision.
const maxFindings = 20

// toFindings reports one finding per matched term so the audit log shows
// what fired and where; engines without spans report one per category.
func toFindings(hits []CategoryScore) []guardrails.Finding {
	out := make([]guardrails.Finding, 0, len(hits))
	for _, h := range hits {
		if len(h.Hits) == 0 {
			out = append(out, guardrails.Finding{
				Entity: "TOXICITY:" + h.Category,
				Score:  float64(h.Score) / 100.0,
				Start:  -1,
				End:    -1,
			})
			continue
		}
		for _, m := range h.Hits {
			if len(out) >= maxFindings {
				return out
			}
			out = append(out, guardrails.Finding{
				Entity:   "TOXICITY:" + h.Category,
				Score:    float64(m.Weight) / 100.0,
				Start:    m.Start,
				End:      m.End,
				Original: truncate(m.Text, 64),
			})
		}
	}
	return out
}

// CategoryScore is one category's score on the 0–100 scale shared with the
// dashboard threshold slider. Engines normalize their internal score to this
// scale.
type CategoryScore struct {
	Category string
	Score    int
	// Hits lists the matched terms when the engine can locate them.
	Hits []Hit
}

// Hit is one matched lexicon term.
type Hit struct {
	Term   string
	Weight int
	Start  int
	End    int
	Text   string
}

// engine is the common contract for both native and OpenAI scoring backends.
// It returns one score per *requested* category (or all categories when
// allowed is nil); categories with zero hits may be omitted.
type engine interface {
	Score(ctx context.Context, text string, allowed map[string]bool) ([]CategoryScore, error)
}
