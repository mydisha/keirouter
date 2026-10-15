package toxicity

import (
	"context"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// nativeEngine is the default, offline toxicity scorer. Each category is a
// weighted lexicon (see lexicon.go) matched against the folded word view of
// the text, so leetspeak ("f4ck"), censoring ("f*ck"), stretched letters
// ("fuuuck"), spaced letters ("f u c k"), homoglyphs and zero-width
// characters all reduce to the same form before matching.
//
// A category's score is the weight of its strongest hit plus a small bonus
// per additional distinct term. Insults aimed at someone ("dasar goblok",
// "you idiot") score their directed weight and also count as harassment.
type nativeEngine struct {
	categories []nativeCategory
}

type nativeCategory struct {
	name string
	lex  *guardrails.Lexicon
}

func newNativeEngine() *nativeEngine {
	return &nativeEngine{categories: []nativeCategory{
		{"profanity", guardrails.CompileLexicon(profanityTerms)},
		{"hate", guardrails.CompileLexicon(hateTerms)},
		{"harassment", guardrails.CompileLexicon(harassmentTerms)},
		{"violence", guardrails.CompileLexicon(violenceTerms)},
		{"sexual", guardrails.CompileLexicon(sexualTerms)},
		{"self_harm", guardrails.CompileLexicon(selfHarmTerms)},
	}}
}

// Score implements engine.
func (e *nativeEngine) Score(_ context.Context, text string, allowed map[string]bool) ([]CategoryScore, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	view := guardrails.Fold(text, guardrails.FoldOptions{Lower: true, Words: true})
	type acc struct {
		best  int
		terms map[string]bool
		hits  []Hit
	}
	byCat := map[string]*acc{}
	note := func(cat string, weight int, h Hit) {
		if allowed != nil && !allowed[cat] {
			return
		}
		a := byCat[cat]
		if a == nil {
			a = &acc{terms: map[string]bool{}}
			byCat[cat] = a
		}
		a.best = max(a.best, weight)
		a.terms[h.Term] = true
		h.Weight = weight
		a.hits = append(a.hits, h)
	}

	toks := guardrails.LexTokens(view.Text)
	for _, cat := range e.categories {
		for _, m := range cat.lex.FindAllTokens(toks) {
			s, en := view.Span(m.Start, m.End)
			if s < 0 {
				continue
			}
			h := Hit{Term: m.Term.Phrase, Start: s, End: en, Text: text[s:en]}
			weight := m.Term.Weight
			if m.Term.Directed > 0 && guardrails.IsDirected(view.Text, m.Start, m.End) {
				weight = m.Term.Directed
				if cat.name != "harassment" {
					note("harassment", weight, h)
				}
			}
			note(cat.name, weight, h)
		}
	}

	out := make([]CategoryScore, 0, len(byCat))
	for cat, a := range byCat {
		score := a.best + 10*min(len(a.terms)-1, 2)
		out = append(out, CategoryScore{Category: cat, Score: min(score, 100), Hits: a.hits})
	}
	return out, nil
}
