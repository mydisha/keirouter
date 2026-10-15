// Package bias is KeiRouter's bias detector. It runs outbound (on the model
// response) and looks for stereotyping language per category (political,
// gender, ethnic, religious) in English and Indonesian: quantified
// generalizations ("all X are", "semua orang X pasti"), essentialist claims
// ("X are naturally …"), and category-specific tropes. Each category is
// scored 0–100 so it lines up with the dashboard threshold.
//
// Bias detection is the hardest detector to define, so it is marked
// experimental in the dashboard and defaults to log_only: it favors
// precision (neutral text about groups does not fire) over recall.
package bias

import (
	"context"
	"sort"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Detector implements guardrails.Detector for bias.
type Detector struct {
	categories []category
}

type category struct {
	name string
	lex  *guardrails.Lexicon
}

// New builds the bias detector.
func New() *Detector {
	return &Detector{categories: []category{
		{"political", guardrails.CompileLexicon(politicalTerms)},
		{"gender", guardrails.CompileLexicon(genderTerms)},
		{"ethnic", guardrails.CompileLexicon(ethnicTerms)},
		{"religious", guardrails.CompileLexicon(religiousTerms)},
	}}
}

// Name identifies this detector in policy config and audit logs.
func (Detector) Name() string { return "bias" }

// Inbound is intentionally a no-op. Bias detection runs only on outbound
// model responses — biased phrasing in a user's *prompt* is a different
// concern (and easy to false-positive on legitimate discussion of bias).
func (Detector) Inbound(_ context.Context, _ *guardrails.InboundRequest, _ guardrails.Policy) (*guardrails.Decision, error) {
	return nil, nil
}

// Outbound scans the LLM response for bias-laden phrasing per the configured
// categories.
func (d *Detector) Outbound(_ context.Context, out *guardrails.OutboundResponse, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.Bias
	if cfg == nil || !cfg.Enabled || strings.TrimSpace(out.Text) == "" {
		return nil, nil
	}
	allowed := allowedCategories(cfg.Categories)
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = guardrails.DefaultBiasThreshold
	}

	var hits []categoryScore
	for _, s := range d.score(out.Text, allowed) {
		if s.Score >= threshold {
			hits = append(hits, s)
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })

	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultBiasAction
	}
	return &guardrails.Decision{
		Action:   action,
		Severity: severityFor(hits),
		Reason:   reasonFor(hits),
		Findings: toFindings(hits),
	}, nil
}

// categoryScore is one category's score on the 0–100 scale.
type categoryScore struct {
	Category string
	Score    int
	Hits     []hit
}

type hit struct {
	weight     int
	start, end int
	text       string
}

// score evaluates each allowed category: the strongest hit plus 10 per
// additional distinct phrase, capped at 100.
func (d *Detector) score(text string, allowed map[string]bool) []categoryScore {
	view := guardrails.Fold(text, guardrails.FoldOptions{Lower: true, Words: true})
	toks := guardrails.LexTokens(view.Text)
	out := make([]categoryScore, 0, len(d.categories))
	for _, c := range d.categories {
		if allowed != nil && !allowed[c.name] {
			continue
		}
		matches := c.lex.FindAllTokens(toks)
		if len(matches) == 0 {
			continue
		}
		best := 0
		distinct := map[string]bool{}
		cs := categoryScore{Category: c.name}
		for _, m := range matches {
			s, e := view.Span(m.Start, m.End)
			if s < 0 {
				continue
			}
			best = max(best, m.Term.Weight)
			distinct[view.Text[m.Start:m.End]] = true
			cs.Hits = append(cs.Hits, hit{weight: m.Term.Weight, start: s, end: e, text: text[s:e]})
		}
		cs.Score = min(best+10*min(len(distinct)-1, 2), 100)
		out = append(out, cs)
	}
	return out
}

func allowedCategories(cats []string) map[string]bool {
	if len(cats) == 0 {
		return nil
	}
	out := make(map[string]bool, len(cats))
	for _, c := range cats {
		out[strings.ToLower(strings.TrimSpace(c))] = true
	}
	return out
}

func severityFor(hits []categoryScore) guardrails.Severity {
	max := 0
	for _, h := range hits {
		if h.Score > max {
			max = h.Score
		}
	}
	switch {
	case max >= 80:
		return guardrails.SeverityHigh
	case max >= 60:
		return guardrails.SeverityMedium
	default:
		return guardrails.SeverityLow
	}
}

func reasonFor(hits []categoryScore) string {
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, h.Category)
	}
	return "bias: " + strings.Join(parts, ", ")
}

const maxFindings = 20

func toFindings(hits []categoryScore) []guardrails.Finding {
	out := make([]guardrails.Finding, 0, len(hits))
	for _, h := range hits {
		for _, m := range h.Hits {
			if len(out) >= maxFindings {
				return out
			}
			out = append(out, guardrails.Finding{
				Entity:   "BIAS:" + h.Category,
				Score:    float64(m.weight) / 100.0,
				Start:    m.start,
				End:      m.end,
				Original: m.text,
			})
		}
	}
	return out
}
