package guardrails

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// FoldOptions selects how aggressively Fold rewrites text into a detection
// view. Every view keeps a byte-offset map back to the original text so a
// match found on the folded form can be reported, or masked, on the original.
type FoldOptions struct {
	// Lower lowercases the text. Leave it off for detectors that rely on case
	// (passport numbers, IBAN, capitalized names).
	Lower bool
	// Words reduces the text to space-separated word tokens: punctuation
	// becomes a single space, leetspeak inside words is decoded ("k1ll",
	// "sh!t") and letters spelled out one by one ("f u c k", "i.g.n.o.r.e")
	// are joined back into one word. Censor characters between letters
	// ("f*ck") are kept as '*' so lexicons can treat them as wildcards.
	Words bool
}

// Folded is a normalized detection view of a text.
type Folded struct {
	Text   string
	starts []int32
	ends   []int32
}

// Span maps a byte range of f.Text back to the original text. The returned
// range covers every original rune that contributed to the folded range.
func (f *Folded) Span(start, end int) (int, int) {
	if f == nil || len(f.starts) == 0 || start < 0 || end <= start {
		return -1, -1
	}
	if start >= len(f.starts) {
		start = len(f.starts) - 1
	}
	if end > len(f.ends) {
		end = len(f.ends)
	}
	return int(f.starts[start]), int(f.ends[end-1])
}

type foldRune struct {
	r    rune
	s, e int32
}

// Fold produces a detection view of s. The base pass, applied to every view:
//
//   - drops invisible characters used to split words (zero-width spaces and
//     joiners, soft hyphens, bidi controls, variation selectors, tag chars)
//   - applies compatibility decomposition so fullwidth, circled, math-bold
//     and ligature forms collapse to plain letters and digits
//   - strips combining marks ("ïgnörè" → "ignore")
//   - maps common Cyrillic and Greek look-alikes onto Latin letters
//   - turns every whitespace variant except newline into a plain space
//
// The returned value is shared and must not be modified: detectors run over
// the same text, so recent results are memoized.
func Fold(s string, opts FoldOptions) *Folded {
	if f := foldMemo.get(s, opts); f != nil {
		return f
	}
	f := fold(s, opts)
	foldMemo.put(s, opts, f)
	return f
}

// foldCache remembers the last few folds. One request is scanned by several
// detectors in a row, each asking for the same view of the same text.
type foldCache struct {
	mu      sync.Mutex
	entries [foldCacheSize]foldEntry
	next    int
}

type foldEntry struct {
	text string
	opts FoldOptions
	f    *Folded
}

const (
	foldCacheSize = 16
	// foldCacheMaxText keeps very large texts (whole files in tool results)
	// out of the cache so it never pins much memory.
	foldCacheMaxText = 64 << 10
)

var foldMemo foldCache

func (c *foldCache) get(s string, opts FoldOptions) *Folded {
	if len(s) > foldCacheMaxText {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries {
		e := &c.entries[i]
		if e.f != nil && e.opts == opts && len(e.text) == len(s) && e.text == s {
			return e.f
		}
	}
	return nil
}

func (c *foldCache) put(s string, opts FoldOptions, f *Folded) {
	if len(s) > foldCacheMaxText {
		return
	}
	c.mu.Lock()
	c.entries[c.next] = foldEntry{text: s, opts: opts, f: f}
	c.next = (c.next + 1) % foldCacheSize
	c.mu.Unlock()
}

func fold(s string, opts FoldOptions) *Folded {
	runes := make([]foldRune, 0, len(s))
	var buf [8]rune
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		start, end := int32(i), int32(i+w)
		i += w
		if isInvisible(r) {
			continue
		}
		if r < utf8.RuneSelf {
			runes = append(runes, foldRune{r: foldBase(r, opts), s: start, e: end})
			continue
		}
		parts := buf[:0]
		if norm.NFKD.IsNormalString(string(r)) {
			parts = append(parts, r)
		} else {
			for _, d := range norm.NFKD.String(string(r)) {
				parts = append(parts, d)
			}
		}
		for _, d := range parts {
			if unicode.Is(unicode.Mn, d) || isInvisible(d) {
				continue
			}
			if c, ok := confusables[d]; ok {
				d = c
			}
			runes = append(runes, foldRune{r: foldBase(d, opts), s: start, e: end})
		}
	}
	if opts.Words {
		if opts.Lower {
			runes = decodeLeet(runes)
		}
		runes = wordsOnly(runes)
		runes = joinSpelledLetters(runes)
	}
	return build(runes)
}

func foldBase(r rune, opts FoldOptions) rune {
	if r != '\n' && unicode.IsSpace(r) {
		return ' '
	}
	if opts.Lower {
		return unicode.ToLower(r)
	}
	return r
}

func build(runes []foldRune) *Folded {
	var b strings.Builder
	b.Grow(len(runes))
	starts := make([]int32, 0, len(runes))
	ends := make([]int32, 0, len(runes))
	var enc [utf8.UTFMax]byte
	for _, fr := range runes {
		n := utf8.EncodeRune(enc[:], fr.r)
		b.Write(enc[:n])
		for k := 0; k < n; k++ {
			starts = append(starts, fr.s)
			ends = append(ends, fr.e)
		}
	}
	return &Folded{Text: b.String(), starts: starts, ends: ends}
}

func isInvisible(r rune) bool {
	switch {
	case r == 0x00AD, r == 0x034F, r == 0x061C, r == 0x115F, r == 0x1160, r == 0x17B4, r == 0x17B5,
		r == 0x180E, r == 0x3164, r == 0xFEFF, r == 0xFFA0:
		return true
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x206F:
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0000 && r <= 0xE007F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

var leetDigits = map[rune]rune{
	'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't', '8': 'b', '9': 'g',
}

var leetSymbols = map[rune]rune{
	'@': 'a', '$': 's', '!': 'i', '|': 'l', '+': 't', '€': 'e', '£': 'l',
}

// censorRunes stand in for a hidden letter ("f*ck", "b#tch").
func isCensorRune(r rune) bool { return r == '*' || r == '#' || r == '%' }

// decodeLeet rewrites leetspeak inside word tokens. Digits are decoded only in
// tokens that already contain a letter, so plain numbers survive. Symbols are
// decoded only when sandwiched between word characters, except '$' and '@'
// which commonly open a word ("$hit", "@ss").
func decodeLeet(runes []foldRune) []foldRune {
	isTokenRune := func(r rune) bool {
		if isWordRune(r) || isCensorRune(r) {
			return true
		}
		_, ok := leetSymbols[r]
		return ok
	}
	i := 0
	for i < len(runes) {
		if !isTokenRune(runes[i].r) {
			i++
			continue
		}
		j := i
		hasLetter := false
		for j < len(runes) && isTokenRune(runes[j].r) {
			if unicode.IsLetter(runes[j].r) {
				hasLetter = true
			}
			j++
		}
		if hasLetter {
			for k := i; k < j; k++ {
				r := runes[k].r
				if m, ok := leetDigits[r]; ok {
					runes[k].r = m
					continue
				}
				prevWord := k > i && isWordRune(runes[k-1].r)
				nextWord := k+1 < j && isWordRune(runes[k+1].r)
				if m, ok := leetSymbols[r]; ok {
					if (prevWord && nextWord) || (k == i && nextWord && (r == '$' || r == '@')) {
						runes[k].r = m
					}
					continue
				}
				if isCensorRune(r) && prevWord {
					runes[k].r = '*'
				}
			}
		}
		i = j
	}
	return runes
}

// wordsOnly keeps letters, digits and inner censor stars; everything else
// collapses into single spaces. Leading and trailing spaces are dropped.
func wordsOnly(runes []foldRune) []foldRune {
	out := make([]foldRune, 0, len(runes))
	space := false
	for _, fr := range runes {
		keep := isWordRune(fr.r)
		if fr.r == '*' && !space && len(out) > 0 && (isWordRune(out[len(out)-1].r) || out[len(out)-1].r == '*') {
			keep = true
		}
		if keep {
			if space && len(out) > 0 {
				out = append(out, foldRune{r: ' ', s: fr.s, e: fr.s})
			}
			space = false
			out = append(out, fr)
			continue
		}
		space = true
	}
	return out
}

// joinSpelledLetters merges runs of three or more single-letter tokens:
// "f u c k" → "fuck", "i g n o r e" → "ignore".
func joinSpelledLetters(runes []foldRune) []foldRune {
	type tok struct{ from, to int }
	var toks []tok
	for i := 0; i < len(runes); {
		if runes[i].r == ' ' {
			i++
			continue
		}
		j := i
		for j < len(runes) && runes[j].r != ' ' {
			j++
		}
		toks = append(toks, tok{i, j})
		i = j
	}
	drop := make([]bool, len(runes))
	single := func(t tok) bool { return t.to-t.from == 1 && unicode.IsLetter(runes[t.from].r) }
	for i := 0; i < len(toks); {
		if !single(toks[i]) {
			i++
			continue
		}
		j := i
		for j < len(toks) && single(toks[j]) {
			j++
		}
		if j-i >= 3 {
			for k := i; k < j-1; k++ {
				for p := toks[k].to; p < toks[k+1].from; p++ {
					drop[p] = true
				}
			}
		}
		i = j
	}
	out := runes[:0]
	for i, fr := range runes {
		if !drop[i] {
			out = append(out, fr)
		}
	}
	return out
}

// confusables maps look-alike letters from other scripts to Latin. The list
// covers the characters that render identically to ASCII letters in common
// UI fonts, which is what attackers rely on to slip past keyword filters.
var confusables = map[rune]rune{
	// Cyrillic lowercase
	'а': 'a', 'в': 'b', 'е': 'e', 'ё': 'e', 'з': '3', 'і': 'i', 'ї': 'i', 'ј': 'j',
	'к': 'k', 'м': 'm', 'н': 'h', 'о': 'o', 'р': 'p', 'с': 'c', 'т': 't', 'у': 'y', 'х': 'x',
	'ѕ': 's', 'ԁ': 'd', 'ԛ': 'q', 'ԝ': 'w', 'һ': 'h', 'ү': 'y', 'ӏ': 'l',
	// Cyrillic uppercase
	'А': 'A', 'В': 'B', 'Е': 'E', 'Ё': 'E', 'З': '3', 'І': 'I', 'Ї': 'I', 'Ј': 'J', 'К': 'K',
	'М': 'M', 'Н': 'H', 'О': 'O', 'Р': 'P', 'С': 'C', 'Т': 'T', 'У': 'Y', 'Х': 'X', 'Ѕ': 'S',
	'Ԁ': 'D', 'Ԛ': 'Q', 'Ԝ': 'W', 'Һ': 'H', 'Ү': 'Y', 'Ӏ': 'I',
	// Greek lowercase
	'α': 'a', 'β': 'b', 'γ': 'y', 'ε': 'e', 'ι': 'i', 'κ': 'k', 'ν': 'v', 'ο': 'o', 'ρ': 'p',
	'τ': 't', 'υ': 'u', 'χ': 'x', 'ω': 'w',
	// Greek uppercase
	'Α': 'A', 'Β': 'B', 'Ε': 'E', 'Ζ': 'Z', 'Η': 'H', 'Ι': 'I', 'Κ': 'K', 'Μ': 'M', 'Ν': 'N',
	'Ο': 'O', 'Ρ': 'P', 'Τ': 'T', 'Υ': 'Y', 'Χ': 'X',
	// Latin extensions and symbols that survive NFKD
	'ı': 'i', 'ȷ': 'j', 'ł': 'l', 'Ł': 'L', 'ø': 'o', 'Ø': 'O', 'đ': 'd', 'Đ': 'D', 'ħ': 'h',
	'ŧ': 't', 'ƒ': 'f', 'ß': 's', 'ɑ': 'a', 'ɡ': 'g', 'ɩ': 'i', 'ʀ': 'r', 'ʏ': 'y',
	'ᴀ': 'a', 'ʙ': 'b', 'ᴄ': 'c', 'ᴅ': 'd', 'ᴇ': 'e', 'ɢ': 'g', 'ʜ': 'h', 'ɪ': 'i', 'ᴊ': 'j',
	'ᴋ': 'k', 'ʟ': 'l', 'ᴍ': 'm', 'ɴ': 'n', 'ᴏ': 'o', 'ᴘ': 'p', 'ꜱ': 's', 'ᴛ': 't', 'ᴜ': 'u',
	'ᴠ': 'v', 'ᴡ': 'w', 'ᴢ': 'z',
}
