package injection

import (
	"strings"
	"unicode/utf8"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// wordSet matches a token exactly or within a small edit distance, so typos
// and deliberate misspellings ("ignroe", "instrucitons") still count. Short
// words only match exactly to keep false positives down.
type wordSet struct {
	exact   map[string]bool
	byFirst map[byte][]string
}

func newWordSet(words ...string) wordSet {
	ws := wordSet{exact: make(map[string]bool, len(words)), byFirst: map[byte][]string{}}
	for _, w := range words {
		ws.exact[w] = true
		if len(w) >= 5 {
			ws.byFirst[w[0]] = append(ws.byFirst[w[0]], w)
		}
	}
	return ws
}

func (ws wordSet) has(tok string) bool {
	if ws.exact[tok] {
		return true
	}
	if len(tok) < 5 {
		return false
	}
	for _, w := range ws.byFirst[tok[0]] {
		limit := 1
		if len(w) >= 9 {
			limit = 2
		}
		if d := len(tok) - len(w); d > limit || -d > limit {
			continue
		}
		if guardrails.EditDistance(tok, w, limit) <= limit {
			return true
		}
	}
	return false
}

// intentRule is a token grammar: a verb, then within window tokens an
// object, with a qualifier anywhere between the verb and just after the
// object (Indonesian puts it after: "abaikan instruksi sebelumnya").
type intentRule struct {
	name     string
	severity Severity
	verbs    wordSet
	// negVerbs count as verbs only right after a negation ("do not follow").
	negVerbs wordSet
	quals    wordSet
	// broadQuals ("all", "semua") qualify only strongObjects, so "ignore all
	// lint rules" is not an attack while "ignore all instructions" is.
	broadQuals    wordSet
	objects       wordSet
	strongObjects wordSet
	window        int
}

var negations = newWordSet("not", "dont", "don", "never", "stop", "no", "jangan", "tidak", "tak", "berhenti", "gak", "nggak", "ga", "longer")

var intentRules = []intentRule{
	{
		name:     "instruction_override",
		severity: SeverityHigh,
		verbs: newWordSet("ignore", "disregard", "forget", "skip", "neglect", "overlook", "omit", "bypass", "override",
			"overwrite", "discard", "abandon", "erase", "abaikan", "lupakan", "acuhkan", "hiraukan", "kesampingkan", "batalkan", "langgar"),
		negVerbs: newWordSet("follow", "obey", "heed", "respect", "ikuti", "patuhi", "turuti", "dengarkan", "pedulikan"),
		quals: newWordSet("previous", "prior", "preceding", "above", "earlier", "initial", "original", "foregoing",
			"aforementioned", "your", "system", "sebelumnya", "terdahulu", "diatas", "atas", "awal", "asli", "sistem", "kamu", "anda", "mu"),
		broadQuals: newWordSet("all", "any", "every", "semua", "seluruh", "segala", "semuanya"),
		objects: newWordSet("instructions", "instruction", "prompt", "prompts", "rules", "directives", "directive",
			"guidelines", "constraints", "restrictions", "programming", "policies", "training", "guidance",
			"instruksi", "perintah", "aturan", "arahan", "petunjuk", "pedoman", "batasan", "ketentuan", "larangan", "panduan"),
		strongObjects: newWordSet("instructions", "instruction", "prompt", "prompts", "directives", "directive", "guidelines",
			"instruksi", "perintah", "arahan", "petunjuk", "pedoman"),
		window: 7,
	},
	{
		name:     "context_reset",
		severity: SeverityLow,
		verbs:    newWordSet("forget", "disregard", "ignore", "erase", "lupakan", "abaikan"),
		quals:    newWordSet("above", "before", "previously", "earlier", "told", "said", "prior", "sebelumnya", "tadi", "diatas", "atas"),
		objects:  newWordSet("everything", "anything", "semuanya", "segalanya", "semua"),
		window:   5,
	},
}

type token struct {
	text       string
	start, end int
}

func tokenize(s string) []token {
	var out []token
	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\n' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] != ' ' && s[j] != '\n' {
			j++
		}
		out = append(out, token{text: s[i:j], start: i, end: j})
		i = j
	}
	return out
}

// stripSuffix removes Indonesian possessive clitics so "instruksimu" and
// "aturannya" still match their base word.
func stripSuffix(t string) (string, string) {
	for _, suf := range []string{"mu", "nya", "ku"} {
		if len(t) > len(suf)+3 && strings.HasSuffix(t, suf) {
			return t[:len(t)-len(suf)], suf
		}
	}
	return t, ""
}

// matchIntents runs every rule over the tokens of a Words-folded text and
// returns hits with byte spans into that text.
func matchIntents(toks []token) []Hit {
	var out []Hit
	for _, r := range intentRules {
		verbMemo := map[string]bool{}
		for i := range toks {
			v, ok := verbMemo[toks[i].text]
			if !ok {
				v = r.verbs.has(toks[i].text)
				verbMemo[toks[i].text] = v
			}
			if !v && !r.isNegatedVerb(toks, i) {
				continue
			}
			if h, ok := r.matchFrom(toks, i); ok {
				out = append(out, h)
			}
		}
	}
	return out
}

func (r intentRule) isNegatedVerb(toks []token, i int) bool {
	if r.negVerbs.exact == nil || !r.negVerbs.has(toks[i].text) {
		return false
	}
	for k := max(i-2, 0); k < i; k++ {
		if negations.has(toks[k].text) {
			return true
		}
	}
	return false
}

func (r intentRule) matchFrom(toks []token, i int) (Hit, bool) {
	end := min(i+r.window, len(toks)-1)
	for j := i + 1; j <= end; j++ {
		obj := toks[j].text
		base, suffix := stripSuffix(obj)
		if !r.objects.has(obj) && !r.objects.has(base) {
			continue
		}
		strong := r.strongObjects.exact != nil && (r.strongObjects.has(obj) || r.strongObjects.has(base))
		// "instruksimu" (your instructions) carries its own qualifier.
		suffixQual := suffix == "mu"
		last := -1
		for k := i + 1; k <= min(j+3, len(toks)-1); k++ {
			if k == j {
				continue
			}
			q := toks[k].text
			if r.quals.has(q) || (strong && r.broadQuals.exact != nil && r.broadQuals.has(q)) {
				last = max(last, k)
			}
		}
		if last < 0 && !suffixQual {
			continue
		}
		stop := max(j, last)
		return Hit{Pattern: r.name, Severity: r.severity, Start: toks[i].start, End: toks[stop].end}, true
	}
	return Hit{}, false
}

// rot13 decodes ASCII letters; text that was rot13-encoded to dodge filters
// reads normally again, while ordinary text turns into gibberish that no
// rule matches.
func rot13(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			b[i] = 'A' + (c-'A'+13)%26
		}
	}
	return string(b)
}

// reverseRunes reverses a string rune by rune ("snoitcurtsni" → "instructions").
func reverseRunes(s string) string {
	out := make([]byte, 0, len(s))
	for i := len(s); i > 0; {
		_, w := utf8.DecodeLastRuneInString(s[:i])
		out = append(out, s[i-w:i]...)
		i -= w
	}
	return string(out)
}
