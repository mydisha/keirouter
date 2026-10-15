package topics

import (
	"strings"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// promptIndex is the folded word view of a prompt, built once per request
// and shared by every topic check.
type promptIndex struct {
	view   string
	tokens []string // unique
	set    map[string]bool
	stems  map[string]bool
}

func newPromptIndex(text string) *promptIndex {
	view := guardrails.Fold(text, guardrails.FoldOptions{Lower: true, Words: true}).Text
	p := &promptIndex{view: view, set: map[string]bool{}, stems: map[string]bool{}}
	for _, t := range strings.Fields(view) {
		if p.set[t] {
			continue
		}
		p.set[t] = true
		p.tokens = append(p.tokens, t)
		if st := stem(t); len(st) >= 3 {
			p.stems[st] = true
		}
	}
	return p
}

// stopWords carry no topic on their own and are ignored inside multi-word
// topics ("history of art" needs "history" and "art").
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "and": true, "or": true, "for": true, "to": true, "in": true,
	"on": true, "with": true, "about": true, "dan": true, "atau": true, "yang": true, "di": true, "ke": true,
	"dari": true, "untuk": true, "tentang": true, "dengan": true, "pada": true,
}

// keywordHit reports whether the prompt mentions the topic:
//
//   - the topic phrase appears verbatim on word boundaries, or
//   - every significant topic word appears somewhere in the prompt, where a
//     word also matches its inflections ("program" ↔ "programming",
//     "deploy" ↔ "deployments") and, for long words, a one-letter typo.
//
// Words shorter than three letters ("ai", "go") only match exactly, so
// "ai" never fires on "said" and "go" never fires on "good".
func keywordHit(p *promptIndex, topic string) bool {
	tv := guardrails.Fold(topic, guardrails.FoldOptions{Lower: true, Words: true}).Text
	if tv == "" || p.view == "" {
		return false
	}
	if strings.Contains(" "+p.view+" ", " "+tv+" ") {
		return true
	}
	significant := 0
	for _, tt := range strings.Fields(tv) {
		if stopWords[tt] {
			continue
		}
		significant++
		if !p.has(tt) {
			return false
		}
	}
	return significant > 0
}

func (p *promptIndex) has(topicWord string) bool {
	if p.set[topicWord] {
		return true
	}
	if len(topicWord) < 3 {
		return false
	}
	if st := stem(topicWord); len(st) >= 3 && p.stems[st] {
		return true
	}
	for _, t := range p.tokens {
		if sameWord(t, topicWord) {
			return true
		}
	}
	return false
}

// sameWord matches prefixed inflections and small typos of a topic word
// (equal stems are checked through promptIndex.stems).
func sameWord(tok, topicWord string) bool {
	if len(topicWord) >= 4 && strings.HasPrefix(tok, topicWord) && len(tok)-len(topicWord) <= 4 {
		return true
	}
	if len(tok) >= 4 && strings.HasPrefix(topicWord, tok) && len(topicWord)-len(tok) <= 2 {
		return true
	}
	if len(topicWord) >= 6 && tok[0] == topicWord[0] {
		d := len(tok) - len(topicWord)
		if d >= -1 && d <= 1 && guardrails.EditDistance(tok, topicWord, 1) <= 1 {
			return true
		}
	}
	return false
}

// stem strips the most common English and Indonesian inflections.
func stem(w string) string {
	for _, p := range []string{"meng", "peng", "mem", "men", "pem", "pen", "ber", "ter", "me", "pe", "di"} {
		if len(w) > len(p)+4 && strings.HasPrefix(w, p) {
			w = w[len(p):]
			break
		}
	}
	for _, s := range []string{"ations", "ation", "ments", "ment", "ings", "ing", "ers", "ies", "kan", "nya", "es", "ed", "er", "an", "s"} {
		if len(w) > len(s)+3 && strings.HasSuffix(w, s) {
			w = w[:len(w)-len(s)]
			// "programm" → "program", "runn" → "run"
			if n := len(w); n >= 4 && w[n-1] == w[n-2] && !strings.ContainsRune("aeiouls", rune(w[n-1])) {
				w = w[:n-1]
			}
			return w
		}
	}
	return w
}
