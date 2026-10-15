package guardrails

import (
	"strings"
)

// Term is one weighted lexicon entry, matched against the word view of a
// text (Fold with Lower and Words). A phrase is a sequence of slots separated
// by spaces; a slot lists one-word alternatives separated by '|' and is
// optional when it ends with '?': "you|u should be? dead". Multi-word
// alternatives need separate terms.
//
// Weight is the 0–100 score of a plain occurrence. Directed is the score when
// the term is aimed at someone ("dasar anjing", "you idiot"); zero means the
// same as Weight. Affix accepts common English and Indonesian inflections
// ("fucking", "dibunuh", "membunuh").
type Term struct {
	Phrase   string
	Weight   int
	Directed int
	Affix    bool
}

// Lexicon is a compiled set of terms. Matching works on word tokens through
// a hash index, so its cost grows with the text, not with the lexicon size.
// Letters may be stretched ("fuuuck" matches "fuck") and inner letters may be
// censored with '*' ("f**k", "b*tch").
type Lexicon struct {
	terms []compiledTerm
	index map[string][]lexEntry
	affix bool
}

type compiledTerm struct {
	Term
	slots []lexSlot
}

type lexSlot struct {
	alts     []lexWord
	optional bool
}

type lexWord struct {
	raw, sq string
}

// eq compares a token with the word. A stretched spelling matches when the
// word is long enough to be unambiguous, or when the stretch is deliberate
// (three or more repeats), so "cook" never matches "cok".
func (w lexWord) eq(tokRaw, tokSq string) bool {
	if tokRaw == w.raw {
		return true
	}
	if tokSq != w.sq {
		return false
	}
	return len(w.sq) >= 5 || hasRun3(tokRaw)
}

func hasRun3(s string) bool {
	for i := 2; i < len(s); i++ {
		if s[i] == s[i-1] && s[i] == s[i-2] {
			return true
		}
	}
	return false
}

// lexEntry points at the slot of a term whose words are indexed.
type lexEntry struct {
	term, slot int
}

// LexiconMatch is one term occurrence, with byte offsets into the view.
type LexiconMatch struct {
	Term       Term
	Start, End int
}

// maxJoinedForms bounds how many run-together spellings ("killyourself")
// are indexed for one multi-word term.
const maxJoinedForms = 32

// CompileLexicon indexes the terms. Multi-word terms are also indexed in
// their run-together form so "killyourself" matches "kill yourself".
func CompileLexicon(terms []Term) *Lexicon {
	l := &Lexicon{index: map[string][]lexEntry{}}
	for _, t := range terms {
		ct := compiledTerm{Term: t}
		for _, raw := range strings.Fields(t.Phrase) {
			sl := lexSlot{optional: strings.HasSuffix(raw, "?")}
			for _, a := range strings.Split(strings.TrimSuffix(raw, "?"), "|") {
				if a != "" {
					sl.alts = append(sl.alts, lexWord{raw: a, sq: squeeze(a)})
				}
			}
			ct.slots = append(ct.slots, sl)
		}
		l.add(ct)
		if joined := joinedForms(ct.slots); len(joined) > 0 {
			l.add(compiledTerm{Term: t, slots: []lexSlot{{alts: joined}}})
		}
	}
	return l
}

func (l *Lexicon) add(ct compiledTerm) {
	l.affix = l.affix || ct.Affix
	ti := len(l.terms)
	l.terms = append(l.terms, ct)
	// Index the first slot, and every following slot while the ones before
	// it are optional, so a match can start at any of them.
	for si, sl := range ct.slots {
		for _, a := range sl.alts {
			l.index[a.sq] = append(l.index[a.sq], lexEntry{term: ti, slot: si})
		}
		if !sl.optional {
			break
		}
	}
}

func joinedForms(slots []lexSlot) []lexWord {
	if len(slots) < 2 {
		return nil
	}
	forms := []string{""}
	for _, sl := range slots {
		if sl.optional || len(forms)*len(sl.alts) > maxJoinedForms {
			return nil
		}
		next := make([]string, 0, len(forms)*len(sl.alts))
		for _, f := range forms {
			for _, a := range sl.alts {
				next = append(next, f+a.raw)
			}
		}
		forms = next
	}
	out := make([]lexWord, len(forms))
	for i, f := range forms {
		out[i] = lexWord{raw: f, sq: squeeze(f)}
	}
	return out
}

// LexToken is one word of a word view, prepared for lexicon lookups.
// Tokenize a view once with LexTokens when several lexicons scan it.
type LexToken struct {
	raw, sq    string
	start, end int
	starred    bool
}

type lexToken = LexToken

// FindAll returns non-overlapping term occurrences in a word view, scanning
// left to right and preferring the heaviest (then longest) term at each
// position.
func (l *Lexicon) FindAll(view string) []LexiconMatch {
	return l.FindAllTokens(LexTokens(view))
}

// FindAllTokens is FindAll over pre-tokenized text.
func (l *Lexicon) FindAllTokens(toks []LexToken) []LexiconMatch {
	var out []LexiconMatch
	for i := 0; i < len(toks); {
		best, bestEnd := -1, -1
		l.candidates(toks[i], func(e lexEntry) {
			ct := &l.terms[e.term]
			if best >= 0 {
				bt := &l.terms[best]
				if ct.Weight < bt.Weight {
					return
				}
			}
			end, ok := ct.matchFrom(toks, i, e.slot)
			if !ok {
				return
			}
			if best < 0 || ct.Weight > l.terms[best].Weight || end > bestEnd {
				best, bestEnd = e.term, end
			}
		})
		if best < 0 {
			i++
			continue
		}
		out = append(out, LexiconMatch{Term: l.terms[best].Term, Start: toks[i].start, End: toks[bestEnd].end})
		i = bestEnd + 1
	}
	return out
}

// candidates yields the index entries that could start at tok.
func (l *Lexicon) candidates(tok lexToken, yield func(lexEntry)) {
	if tok.starred {
		for word, entries := range l.index {
			if globEq(tok.sq, word) {
				for _, e := range entries {
					yield(e)
				}
			}
		}
		return
	}
	for _, e := range l.index[tok.sq] {
		yield(e)
	}
	if !l.affix || len(tok.raw) < 5 {
		return
	}
	direct := l.index[tok.sq]
	forAffixVariants(tok.raw, func(v string) {
		for _, e := range l.index[squeeze(v)] {
			if l.terms[e.term].Affix && !hasEntry(direct, e) {
				yield(e)
			}
		}
	})
}

func hasEntry(es []lexEntry, e lexEntry) bool {
	for _, x := range es {
		if x == e {
			return true
		}
	}
	return false
}

// matchFrom matches the term's slots from slot s0 against tokens starting at
// i and returns the index of the last token consumed.
func (ct *compiledTerm) matchFrom(toks []lexToken, i, s0 int) (int, bool) {
	k := i
	last := -1
	for s := s0; s < len(ct.slots); s++ {
		sl := ct.slots[s]
		if k < len(toks) && ct.slotHas(sl, toks[k]) {
			last = k
			k++
			continue
		}
		if !sl.optional {
			return 0, false
		}
	}
	return last, last >= 0
}

func (ct *compiledTerm) slotHas(sl lexSlot, tok lexToken) bool {
	for _, a := range sl.alts {
		if tok.starred {
			if globEq(tok.sq, a.sq) {
				return true
			}
			continue
		}
		if a.eq(tok.raw, tok.sq) {
			return true
		}
	}
	if !ct.Affix || tok.starred {
		return false
	}
	found := false
	forAffixVariants(tok.raw, func(v string) {
		if found {
			return
		}
		vs := squeeze(v)
		for _, a := range sl.alts {
			if a.eq(v, vs) {
				found = true
				return
			}
		}
	})
	return found
}

// LexTokens splits a word view (see Fold) into tokens.
func LexTokens(view string) []LexToken {
	var out []lexToken
	for i := 0; i < len(view); {
		if view[i] == ' ' || view[i] == '\n' {
			i++
			continue
		}
		j := i
		for j < len(view) && view[j] != ' ' && view[j] != '\n' {
			j++
		}
		w := view[i:j]
		out = append(out, lexToken{raw: w, sq: squeeze(w), start: i, end: j, starred: strings.IndexByte(w, '*') > 0})
		i = j
	}
	return out
}

// squeeze collapses runs of the same character: "fuuuck" → "fuck",
// "kill" → "kil". Both the lexicon and the text are squeezed, so stretched
// spellings compare equal.
func squeeze(w string) string {
	repeat := false
	ascii := true
	for i := 0; i < len(w); i++ {
		if w[i] >= 0x80 {
			ascii = false
		}
		if i > 0 && w[i] == w[i-1] {
			repeat = true
		}
	}
	if !repeat {
		return w
	}
	if ascii {
		b := make([]byte, 0, len(w))
		for i := 0; i < len(w); i++ {
			if i == 0 || w[i] != w[i-1] {
				b = append(b, w[i])
			}
		}
		return string(b)
	}
	var b strings.Builder
	var prev rune = -1
	changed := false
	for _, r := range w {
		if r == prev {
			changed = true
			continue
		}
		prev = r
		b.WriteRune(r)
	}
	if !changed {
		return w
	}
	return b.String()
}

// globEq compares a censored token ("f*ck") with a word: the first and last
// characters must match and each '*' stands for one or more letters.
func globEq(tok, word string) bool {
	if tok == "" || word == "" || tok[0] != word[0] || tok[len(tok)-1] != word[len(word)-1] {
		return false
	}
	return glob(tok, word)
}

func glob(p, s string) bool {
	for len(p) > 0 {
		if p[0] == '*' {
			p = p[1:]
			for k := 1; k <= len(s); k++ {
				if glob(p, s[k:]) {
					return true
				}
			}
			return false
		}
		if len(s) == 0 || p[0] != s[0] {
			return false
		}
		p, s = p[1:], s[1:]
	}
	return len(s) == 0
}

var (
	lexPrefixes = []string{"meng", "meny", "peng", "mem", "men", "pem", "pen", "ber", "ter", "nge", "me", "di", "pe", "ke"}
	lexSuffixes = []string{"ings", "ing", "ers", "ies", "kan", "nya", "lah", "ed", "er", "es", "an", "in", "s", "y", "i"}
)

// forAffixVariants calls fn with the token minus common English and
// Indonesian inflections ("fucking" → "fuck", "dibunuh" → "bunuh",
// "membunuhnya" → "bunuh", colloquial "ngebacok" → "bacok").
func forAffixVariants(w string, fn func(string)) {
	stripSuffix := func(x string) {
		for _, s := range lexSuffixes {
			if len(x) > len(s)+2 && strings.HasSuffix(x, s) {
				fn(x[:len(x)-len(s)])
			}
		}
	}
	stripSuffix(w)
	for _, p := range lexPrefixes {
		if len(w) > len(p)+2 && strings.HasPrefix(w, p) {
			rest := w[len(p):]
			fn(rest)
			stripSuffix(rest)
		}
	}
}

// pronouns mark a term as aimed at someone when they sit right before or
// after it (with a little filler in between: "you are such an idiot").
// "ya" is left out on purpose: in Indonesian it is "yes"/a tag particle
// ("gila ya harganya", "aku bodoh ya"), far more often than English "you".
var pronouns = map[string]bool{
	"you": true, "u": true, "ur": true, "your": true, "youre": true, "yourself": true, "yall": true,
	"lu": true, "lo": true, "loe": true, "elu": true, "elo": true, "lw": true, "luh": true, "kau": true,
	"engkau": true, "kamu": true, "kalian": true, "dirimu": true, "anda": true, "ente": true, "situ": true,
	"sia": true, "maneh": true, "kowe": true, "koe": true, "sampeyan": true, "awakmu": true, "dasar": true,
}

// possessives point at someone's thing, not at them, when they come before
// the term: "take out your trash", "your dog is ugly". After the term they
// still count ("fuck your mom").
var possessives = map[string]bool{"your": true}

// postnominal pronouns are Indonesian/regional pronouns that also mark
// possession when they follow a noun: "laptop kamu lemot" is about the
// laptop, "kamu lemot" is about the person.
var postnominal = map[string]bool{
	"lu": true, "lo": true, "loe": true, "elu": true, "elo": true, "lw": true, "luh": true, "kau": true,
	"engkau": true, "kamu": true, "kalian": true, "anda": true, "ente": true, "situ": true, "sia": true,
	"maneh": true, "kowe": true, "koe": true, "sampeyan": true,
}

// clauseOpeners may sit right before a postnominal pronoun without making it
// possessive: interjections, conjunctions, vocatives and discourse particles
// ("eh kamu goblok", "sumpah lu tolol", "bro lu bego").
var clauseOpeners = map[string]bool{
	"eh": true, "heh": true, "hei": true, "hey": true, "woi": true, "woy": true, "oi": true, "oy": true,
	"dan": true, "tapi": true, "tp": true, "karena": true, "krn": true, "soalnya": true, "sumpah": true,
	"pokoknya": true, "jadi": true, "jd": true, "terus": true, "trus": true, "kalo": true, "kalau": true,
	"klo": true, "bahwa": true, "ternyata": true, "pantes": true, "pantesan": true, "nah": true, "sih": true,
	"lah": true, "iya": true, "ya": true, "yaudah": true, "anjir": true, "njir": true, "anjay": true,
	"astaga": true, "bro": true, "bang": true, "bung": true, "sis": true, "cuy": true, "coy": true,
	"bos": true, "boss": true, "gan": true, "mas": true, "mbak": true, "kak": true, "dek": true,
	"emang": true, "memang": true, "dasar": true, "cuma": true, "hanya": true, "dah": true, "udah": true,
	"kok": true, "tuh": true, "itu": true, "ini": true,
}

var fillers = map[string]bool{
	"are": true, "re": true, "is": true, "a": true, "an": true, "such": true, "so": true, "really": true, "very": true,
	"fucking": true, "total": true, "complete": true, "little": true, "big": true, "absolute": true, "utter": true,
	"stupid": true, "dumb": true, "fat": true, "ugly": true, "lazy": true, "bloody": true, "freaking": true,
	"itu": true, "memang": true, "emang": true, "sungguh": true, "banget": true, "bgt": true, "bener": true,
	"benar": true, "sangat": true, "kok": true, "tuh": true, "ini": true, "si": true, "dah": true, "udah": true,
	"kayak": true, "kek": true, "kaya": true, "macam": true, "seperti": true, "mirip": true, "bagai": true,
	"bagaikan": true, "persis": true, "sok": true,
}

// IsDirected reports whether the lexicon match at [s,e) of a word view is
// aimed at someone: a second-person pronoun (or "dasar") right before it,
// allowing a few filler words ("you are such an idiot", "kamu memang
// goblok"), or right after it ("anjing lu", "fuck you").
func IsDirected(view string, s, e int) bool {
	before := strings.Fields(view[max(s-48, 0):s])
	if s-48 > 0 && len(before) > 0 {
		before = before[1:]
	}
	for i, n := len(before)-1, 0; i >= 0 && n < 4; i, n = i-1, n+1 {
		w := before[i]
		if pronouns[w] {
			if possessives[w] {
				return false
			}
			if postnominal[w] && i > 0 && !clauseOpeners[before[i-1]] && !pronouns[before[i-1]] {
				return false
			}
			return true
		}
		if !fillers[w] {
			break
		}
	}
	after := strings.Fields(view[e:min(e+16, len(view))])
	return len(after) > 0 && pronouns[after[0]] && after[0] != "dasar"
}

// EditDistance is the optimal-string-alignment edit distance (insert, delete,
// substitute, transpose adjacent). It returns limit+1 as soon as the distance
// is known to exceed limit.
func EditDistance(a, b string, limit int) int {
	n, m := len(a), len(b)
	prev2 := make([]int, m+1)
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= m; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
			rowMin = min(rowMin, v)
		}
		if rowMin > limit {
			return limit + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[m]
}
