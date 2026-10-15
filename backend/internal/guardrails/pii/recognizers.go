// Package pii is KeiRouter's native PII recognizer set. It implements the
// guardrails.Detector interface using regex + validator recognizers, with
// entity names that follow Microsoft Presidio (EMAIL_ADDRESS, PHONE_NUMBER,
// CREDIT_CARD, ...) plus Indonesian identifiers (ID_NIK, ID_KK, ID_NPWP,
// ID_PASSPORT, ID_BPJS) and credentials (API_KEY, PASSWORD, PRIVATE_KEY).
package pii

import (
	"math/big"
	"net"
	"regexp"
	"sort"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Entity is the canonical PII type name. Names match Presidio's
// supported_entities catalog where possible.
type Entity string

const (
	EntityEmail      Entity = "EMAIL_ADDRESS"
	EntityPhone      Entity = "PHONE_NUMBER"
	EntityCreditCard Entity = "CREDIT_CARD"
	EntityIBAN       Entity = "IBAN_CODE"
	EntityIP         Entity = "IP_ADDRESS"
	EntityURL        Entity = "URL"
	EntityPerson     Entity = "PERSON"
	EntityUSSSN      Entity = "US_SSN"
	EntityIDNIK      Entity = "ID_NIK"      // Indonesia: 16-digit national ID
	EntityIDKK       Entity = "ID_KK"       // Indonesia: family card number
	EntityIDNPWP     Entity = "ID_NPWP"     // Indonesia: tax ID
	EntityIDPassport Entity = "ID_PASSPORT" // Indonesia: 1 letter + 7 digits
	EntityIDBPJS     Entity = "ID_BPJS"     // Indonesia: 13-digit health insurance number
	EntityAPIKey     Entity = "API_KEY"     // provider keys, tokens, JWTs
	EntityPassword   Entity = "PASSWORD"
	EntityPrivateKey Entity = "PRIVATE_KEY"
)

// AllEntities returns the catalog in display order. Used by the dashboard's
// multi-select.
func AllEntities() []Entity {
	return []Entity{
		EntityEmail,
		EntityPhone,
		EntityCreditCard,
		EntityIBAN,
		EntityIP,
		EntityURL,
		EntityIDNIK,
		EntityIDKK,
		EntityIDNPWP,
		EntityIDPassport,
		EntityIDBPJS,
		EntityUSSSN,
		EntityAPIKey,
		EntityPassword,
		EntityPrivateKey,
		EntityPerson,
	}
}

// Recognizer is one pattern in the catalog. Confidence is the base score;
// a context word near the match raises it to ContextScore. Postcheck runs
// structural validation (Luhn, mod-97, NIK date) and may reject the match.
type Recognizer struct {
	Entity     Entity
	Pattern    *regexp.Regexp
	Confidence float64
	// Group selects a capture group as the sensitive value, so "api_key=XYZ"
	// masks only XYZ. Zero means the whole match.
	Group int
	// Context words that, when found shortly before or after the match,
	// raise confidence to ContextScore (default 0.85).
	Context      []string
	ContextScore float64
	// Postcheck returns false to drop a match (e.g. failed Luhn for cards).
	Postcheck func(match string) bool
	// TrimRight lists trailing characters removed from a match (sentence
	// punctuation after a URL).
	TrimRight string
	// Any and MinDigits are cheap prefilters: the regex only runs when the
	// lowercased text contains one of Any and a run of at least MinDigits
	// digits (separators allowed). Most text skips most regexes.
	Any       []string
	MinDigits int
	// Need is a custom prefilter for shapes the fields above can't express.
	Need func(lower string) bool
}

const (
	contextBefore = 48
	contextAfter  = 24
)

// Match is a single recognized PII span.
type Match struct {
	Entity Entity
	Start  int
	End    int
	Text   string
	Score  float64
}

var (
	nikPattern    = regexp.MustCompile(`\b\d{2}[\s.\-]?\d{4}[\s.\-]?\d{6}[\s.\-]?\d{4}\b`)
	phoneContext  = []string{"phone", "telp", "telepon", "tlp", "hp", "handphone", "ponsel", "wa", "whatsapp", "mobile", "call", "hubungi", "contact", "kontak", "nomor", "no."}
	secretContext = []string{"key", "token", "secret", "credential", "auth", "bearer"}
)

// defaultRecognizers builds the catalog. Patterns favor validated structure
// (checksums, issuer prefixes, date fields) so that confidence can stay high
// without flooding requests with false positives.
var defaultRecognizers = []Recognizer{
	// Credentials first: they are the most damaging leak and their spans
	// should win over looser patterns (URL, PERSON) that might overlap.
	{
		Entity:     EntityPrivateKey,
		Pattern:    regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`),
		Any:        []string{"private key"},
		Confidence: 1.0,
	},
	{
		Entity: EntityAPIKey,
		Pattern: regexp.MustCompile(`\bsk-ant-(?:api|admin|oat)\d{2}-[A-Za-z0-9_\-]{20,}` +
			`|\bsk-(?:proj-|svcacct-|admin-|or-v1-)?[A-Za-z0-9_\-]{20,}` +
			`|\b(?:AKIA|ASIA|ABIA|ACCA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|APKA)[A-Z0-9]{16}\b` +
			`|\bgh[pousr]_[A-Za-z0-9]{36,255}\b|\bgithub_pat_[A-Za-z0-9_]{22,255}\b` +
			`|\bglpat-[A-Za-z0-9_\-]{20,}` +
			`|\bAIza[0-9A-Za-z_\-]{35}` +
			`|\bxox[abposre]-[A-Za-z0-9\-]{10,}` +
			`|https://hooks\.slack\.com/services/[A-Za-z0-9/_\-]+` +
			`|\b(?:sk|rk|pk|whsec)_(?:live|test)_[A-Za-z0-9]{16,}` +
			`|\bhf_[A-Za-z0-9]{30,}\b|\bnpm_[A-Za-z0-9]{36}\b|\bgsk_[A-Za-z0-9]{20,}\b` +
			`|\b(?:xai|pplx|nvapi)-[A-Za-z0-9_\-]{20,}` +
			`|\bkr_[A-Za-z0-9_\-]{32,}` +
			`|\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
		Any: []string{"sk-", "akia", "asia", "abia", "acca", "agpa", "aida", "aroa", "aipa", "anpa", "anva", "apka", "gh", "glpat-",
			"aiza", "xox", "hooks.slack.com", "_live_", "_test_", "hf_", "npm_", "gsk_", "xai-", "pplx-", "nvapi-", "kr_", "eyj"},
		Confidence: 0.95,
		Postcheck:  hasDigitAndLetter,
	},
	{
		Entity:     EntityAPIKey,
		Pattern:    regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9\-._~+/]{20,}=*)`),
		Any:        []string{"bearer"},
		Group:      1,
		Confidence: 0.85,
		Postcheck:  hasDigitAndLetter,
	},
	{
		Entity: EntityAPIKey,
		Pattern: regexp.MustCompile(`(?i)\b(?:api[_\-\s]?key|apikey|secret(?:[_\-\s]?key)?|access[_\-\s]?(?:token|key)|auth[_\-\s]?token|client[_\-\s]?secret|refresh[_\-\s]?token|token)` +
			`["']?\s*[:=]\s*["']?([A-Za-z0-9_\-+/=.]{16,})`),
		Any:        []string{"key", "secret", "token"},
		Group:      1,
		Confidence: 0.8,
		Postcheck:  hasDigitAndLetter,
	},
	{
		Entity: EntityPassword,
		Pattern: regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|passcode|kata\s?sandi|sandi)` +
			`["']?\s*(?:[:=]|\bis\b|\badalah\b|\bnya\b)\s*["']?([^\s"',;]{6,})`),
		Any:        []string{"pass", "pwd", "sandi"},
		Group:      1,
		Confidence: 0.8,
		Postcheck:  hasNonLetter,
	},
	{
		Entity:     EntityEmail,
		Pattern:    regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9._%+\-]*@[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?)*\.[A-Za-z]{2,24}\b`),
		Any:        []string{"@"},
		Confidence: 0.9,
	},
	{
		// Obfuscated addresses: "budi [at] mail [dot] com", "budi(at)mail.co.id".
		Entity: EntityEmail,
		Pattern: regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9._%+\-]*\s*(?:\[at\]|\(at\)|\{at\}|<at>)\s*[a-z0-9\-]+` +
			`(?:\s*(?:\.|\[dot\]|\(dot\)|\{dot\})\s*[a-z0-9\-]+)+`),
		Any:        []string{"at]", "(at)", "{at}", "<at>"},
		Confidence: 0.8,
	},
	{
		// Fully spelled: "budi at gmail dot com".
		Entity: EntityEmail,
		Pattern: regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9._%+\-]*\s+at\s+[a-z0-9\-]+(?:\s+dot\s+[a-z0-9\-]+)*` +
			`\s+dot\s+(?:com|net|org|id|co|io|ai|edu|gov|info|biz|me|dev|app)\b`),
		Any:        []string{" dot "},
		Confidence: 0.75,
	},
	{
		Entity:     EntityIDNIK,
		Pattern:    nikPattern,
		MinDigits:  16,
		Confidence: 0.6,
		Context:    []string{"nik", "ktp", "e-ktp", "nomor induk", "kependudukan", "national id", "identity number", "id number"},
		Postcheck:  validateNIK,
	},
	{
		// KK numbers share the NIK layout; only context tells them apart.
		Entity:       EntityIDKK,
		Pattern:      nikPattern,
		MinDigits:    16,
		Confidence:   0.3,
		Context:      []string{"kk", "kartu keluarga", "family card"},
		ContextScore: 0.9,
		Postcheck:    validateKK,
	},
	{
		Entity:     EntityIDNPWP,
		Pattern:    regexp.MustCompile(`\b\d{2}[.\-]\d{3}[.\-]\d{3}[.\-]\d[.\-]\d{3}[.\-]\d{3}\b`),
		MinDigits:  15,
		Confidence: 0.85,
		Postcheck:  validateNPWP,
	},
	{
		Entity:       EntityIDNPWP,
		Pattern:      regexp.MustCompile(`\b\d{15,16}\b`),
		MinDigits:    15,
		Confidence:   0.3,
		Context:      []string{"npwp", "tax id", "nomor pokok wajib pajak", "wajib pajak"},
		ContextScore: 0.9,
		Postcheck:    validateNPWP,
	},
	{
		Entity:       EntityIDBPJS,
		Pattern:      regexp.MustCompile(`\b\d{13}\b`),
		MinDigits:    13,
		Confidence:   0.3,
		Context:      []string{"bpjs", "jkn", "kis", "kartu indonesia sehat", "jaminan kesehatan"},
		ContextScore: 0.9,
	},
	{
		Entity:     EntityIDPassport,
		Pattern:    regexp.MustCompile(`\b[A-Z]\d{7}\b`),
		MinDigits:  7,
		Confidence: 0.5,
		Context:    []string{"passport", "paspor"},
	},
	{
		Entity:       EntityUSSSN,
		Pattern:      regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		MinDigits:    9,
		Confidence:   0.6,
		Context:      []string{"ssn", "social security"},
		ContextScore: 0.95,
		Postcheck:    validSSN,
	},
	{
		Entity:       EntityCreditCard,
		Pattern:      regexp.MustCompile(`\b(?:\d[ \-]?){12,18}\d\b`),
		MinDigits:    13,
		Confidence:   0.85,
		Context:      []string{"card", "credit", "debit", "kartu", "visa", "mastercard", "amex", "cc", "cvv", "kredit"},
		ContextScore: 0.95,
		Postcheck:    validCard,
	},
	{
		// Indonesian mobile: +62 / 62 / 0 then 8xx.
		Entity:     EntityPhone,
		Pattern:    regexp.MustCompile(`(?:\+62|\b62|\b0)[\s.\-]?\(?8\d{1,3}\)?[\s.\-]?\d{3,4}[\s.\-]?\d{3,5}\b`),
		MinDigits:  9,
		Confidence: 0.85,
		Postcheck:  phoneDigits,
	},
	{
		// Indonesian landline: (021) 5555 1234, 0251-123456.
		Entity:       EntityPhone,
		Pattern:      regexp.MustCompile(`(?:\+62[\s.\-]?|\(0|\b0)(?:2[1-9]|[3-79]\d)\d?\)?[\s.\-]?\d{3,4}[\s.\-]?\d{3,4}\b`),
		MinDigits:    8,
		Confidence:   0.6,
		Context:      phoneContext,
		ContextScore: 0.9,
		Postcheck:    phoneDigits,
	},
	{
		// International E.164 with optional grouping: +1 (415) 555-2671.
		Entity:     EntityPhone,
		Pattern:    regexp.MustCompile(`\+\d{1,3}[\s.\-]?\(?\d{1,4}\)?(?:[\s.\-]?\d{2,5}){1,4}\b`),
		MinDigits:  8,
		Any:        []string{"+"},
		Confidence: 0.75,
		Context:    phoneContext,
		Postcheck:  phoneDigits,
	},
	{
		// North American style: (415) 555-2671, 415-555-2671.
		Entity:     EntityPhone,
		Pattern:    regexp.MustCompile(`(?:\(\d{3}\)\s?|\b\d{3}[\s.\-])\d{3}[\s.\-]\d{4}\b`),
		MinDigits:  10,
		Confidence: 0.5,
		Context:    phoneContext,
		Postcheck:  phoneDigits,
	},
	{
		Entity:     EntityIBAN,
		Pattern:    regexp.MustCompile(`\b[A-Z]{2}\d{2}(?:[A-Z0-9]{11,30}|(?: [A-Z0-9]{4}){2,7}(?: [A-Z0-9]{1,4})?)\b`),
		MinDigits:  2,
		Confidence: 0.9,
		Postcheck:  validIBAN,
	},
	{
		Entity:     EntityIP,
		Pattern:    regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
		MinDigits:  4,
		Confidence: 0.6,
		Context:    []string{"ip", "host", "server", "address", "alamat"},
		Postcheck:  validIPv4,
	},
	{
		Entity:     EntityIP,
		Pattern:    regexp.MustCompile(`(?i)(?:[0-9a-f]{1,4}|:)(?::[0-9a-f]{0,4}){2,7}(?:%[0-9a-z]+)?`),
		Need:       ipv6ish,
		Confidence: 0.6,
		Postcheck:  validIPv6,
	},
	{
		Entity:     EntityURL,
		Pattern:    regexp.MustCompile("\\b(?:https?|ftp)://[^\\s<>\"'`]+|\\bwww\\.[^\\s<>\"'`]+"),
		Any:        []string{"://", "www."},
		Confidence: 0.6,
		TrimRight:  ".,;:!?)]}>*_",
	},
	{
		// Heuristic only — two or more capitalized words. Confidence stays
		// low so policies that scan PERSON should set min_score accordingly;
		// an honorific or "nama" right before it raises the score.
		Entity:       EntityPerson,
		Pattern:      regexp.MustCompile(`\b[A-Z][a-z]{2,}\s+[A-Z][a-z]{2,}(?:\s+[A-Z][a-z]+)?\b`),
		Confidence:   0.35,
		Context:      []string{"nama", "name", "bapak", "ibu", "pak", "bu", "sdr", "sdri", "mr", "mrs", "ms", "dr", "saudara", "atas nama", "a.n"},
		ContextScore: 0.7,
	},
}

// Recognize scans text and returns all matches whose final score is at least
// minScore and whose Entity is in the allowed set. When allowed is empty, all
// catalog entities run. Offsets index into text.
//
// Matching runs on a folded copy, so fullwidth digits, zero-width characters
// and look-alike letters cannot be used to slip an identifier past the
// patterns; spans are mapped back onto the original text.
func Recognize(text string, allowed map[Entity]bool, minScore float64) []Match {
	if text == "" {
		return nil
	}
	f := guardrails.Fold(text, guardrails.FoldOptions{})
	view := f.Text
	lower := asciiLower(view)
	digits := maxDigitRun(view)
	var out []Match
	for _, rec := range defaultRecognizers {
		if len(allowed) > 0 && !allowed[rec.Entity] {
			continue
		}
		if digits < rec.MinDigits || !containsAny(lower, rec.Any) || (rec.Need != nil && !rec.Need(lower)) {
			continue
		}
		if maxScore(rec) < minScore {
			continue
		}
		for _, idx := range rec.Pattern.FindAllStringSubmatchIndex(view, -1) {
			s, e := idx[0], idx[1]
			if rec.Group > 0 && len(idx) > 2*rec.Group+1 {
				if idx[2*rec.Group] < 0 {
					continue
				}
				s, e = idx[2*rec.Group], idx[2*rec.Group+1]
			}
			if rec.TrimRight != "" {
				e = s + len(strings.TrimRight(view[s:e], rec.TrimRight))
			}
			if e <= s {
				continue
			}
			matched := view[s:e]
			if rec.Postcheck != nil && !rec.Postcheck(matched) {
				continue
			}
			score := rec.Confidence
			if hasContext(lower, idx[0], idx[1], rec.Context) {
				boost := rec.ContextScore
				if boost == 0 {
					boost = 0.85
				}
				if score < boost {
					score = boost
				}
			}
			if score < minScore {
				continue
			}
			os, oe := f.Span(s, e)
			if os < 0 {
				continue
			}
			out = append(out, Match{Entity: rec.Entity, Start: os, End: oe, Text: text[os:oe], Score: score})
		}
	}
	return mergeOverlaps(out, text)
}

// maxScore is the highest score a recognizer can produce.
func maxScore(rec Recognizer) float64 {
	if len(rec.Context) == 0 {
		return rec.Confidence
	}
	boost := rec.ContextScore
	if boost == 0 {
		boost = 0.85
	}
	return max(rec.Confidence, boost)
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

// maxDigitRun is the longest run of digits in s, where up to two separator
// characters (space . - ( ) / +) may sit between digits.
func maxDigitRun(s string) int {
	best, cur, gap := 0, 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			cur++
			gap = 0
			best = max(best, cur)
		case cur > 0 && gap < 2 && strings.IndexByte(" .-()/+", c) >= 0:
			gap++
		default:
			cur, gap = 0, 0
		}
	}
	return best
}

// ipv6ish reports whether s has two hex groups joined by colons, or "::".
func ipv6ish(s string) bool {
	if strings.Contains(s, "::") {
		return true
	}
	isHex := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }
	for i := strings.IndexByte(s, ':'); i >= 0; {
		if i > 0 && i+1 < len(s) && isHex(s[i-1]) && isHex(s[i+1]) {
			if j := strings.IndexByte(s[i+1:], ':'); j > 0 && j <= 4 {
				return true
			}
		}
		next := strings.IndexByte(s[i+1:], ':')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return false
}

// asciiLower lowercases ASCII letters only, so byte offsets are unchanged.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// hasContext reports whether any context word appears as a whole word
// shortly before or after the match.
func hasContext(lowerText string, start, end int, words []string) bool {
	if len(words) == 0 {
		return false
	}
	from := max(start-contextBefore, 0)
	to := min(end+contextAfter, len(lowerText))
	window := lowerText[from:start] + " " + lowerText[end:to]
	for _, w := range words {
		for i := 0; ; {
			j := strings.Index(window[i:], w)
			if j < 0 {
				break
			}
			at := i + j
			before := at == 0 || !isAlnum(window[at-1])
			afterAt := at + len(w)
			after := afterAt >= len(window) || !isAlnum(window[afterAt])
			if before && after {
				return true
			}
			i = at + 1
		}
	}
	return false
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// mergeOverlaps resolves overlapping matches so every sensitive byte is
// masked exactly once: overlapping spans are merged into their union and the
// entity of the highest-scoring member (longest on ties) labels the result.
func mergeOverlaps(matches []Match, text string) []Match {
	if len(matches) < 2 {
		return matches
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End > matches[j].End
	})
	out := matches[:1]
	for _, m := range matches[1:] {
		cur := &out[len(out)-1]
		if m.Start >= cur.End {
			out = append(out, m)
			continue
		}
		better := m.Score > cur.Score || (m.Score == cur.Score && m.End-m.Start > cur.End-cur.Start)
		if better {
			cur.Entity, cur.Score = m.Entity, m.Score
		}
		if m.End > cur.End {
			cur.End = m.End
		}
		if text != "" && cur.End <= len(text) {
			cur.Text = text[cur.Start:cur.End]
		}
	}
	return out
}

// stripNonDigits removes spaces, dashes, and dots from a numeric-looking
// string so postchecks like Luhn can operate on raw digits.
func stripNonDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func allSameDigit(d string) bool {
	for i := 1; i < len(d); i++ {
		if d[i] != d[0] {
			return false
		}
	}
	return true
}

// luhn validates a credit-card-style number with the Luhn checksum.
func luhn(digits string) bool {
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

// validCard requires a Luhn-valid number whose issuer prefix and length match
// a real card network, which rules out most order numbers and IDs that happen
// to pass Luhn.
func validCard(s string) bool {
	d := stripNonDigits(s)
	if !luhn(d) || allSameDigit(d) {
		return false
	}
	n := len(d)
	p2 := atoi(d[:2])
	p3 := atoi(d[:3])
	p4 := atoi(d[:4])
	p6 := atoi(d[:6])
	switch {
	case d[0] == '4':
		return n == 13 || n == 16 || n == 19 // Visa
	case p2 >= 51 && p2 <= 55, p4 >= 2221 && p4 <= 2720:
		return n == 16 // Mastercard
	case p2 == 34 || p2 == 37:
		return n == 15 // Amex
	case p4 == 6011, p2 == 65, p3 >= 644 && p3 <= 649, p6 >= 622126 && p6 <= 622925:
		return n >= 16 && n <= 19 // Discover
	case p4 >= 3528 && p4 <= 3589:
		return n >= 16 && n <= 19 // JCB
	case p3 >= 300 && p3 <= 305, p2 == 36, p2 == 38, p2 == 39:
		return n >= 14 && n <= 19 // Diners
	case p2 == 62:
		return n >= 16 && n <= 19 // UnionPay
	case d[:4] == "9360":
		return n >= 16 && n <= 19 // GPN (Indonesia)
	case p2 >= 50 && p2 <= 69:
		return n >= 12 && n <= 19 // Maestro
	}
	return false
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// validateNIK checks Indonesian NIK structure: 16 digits, an assigned
// province code, and a DDMMYY birth date where women add 40 to the day.
func validateNIK(s string) bool {
	d := stripNonDigits(s)
	if len(d) != 16 || allSameDigit(d) {
		return false
	}
	prov := atoi(d[0:2])
	if prov < 11 || prov > 94 {
		return false
	}
	dd := atoi(d[6:8])
	if dd == 0 || (dd > 31 && dd < 41) || dd > 71 {
		return false
	}
	mm := atoi(d[8:10])
	return mm >= 1 && mm <= 12
}

// validateKK checks the family card layout: region code then a DDMMYY issue
// date (no +40 offset).
func validateKK(s string) bool {
	d := stripNonDigits(s)
	if len(d) != 16 || allSameDigit(d) {
		return false
	}
	prov := atoi(d[0:2])
	if prov < 11 || prov > 94 {
		return false
	}
	dd, mm := atoi(d[6:8]), atoi(d[8:10])
	return dd >= 1 && dd <= 31 && mm >= 1 && mm <= 12
}

// validateNPWP accepts both legacy 15-digit and current 16-digit NPWP and
// rejects runs of a single repeated digit.
func validateNPWP(s string) bool {
	d := stripNonDigits(s)
	if len(d) != 15 && len(d) != 16 {
		return false
	}
	return !allSameDigit(d)
}

func validSSN(s string) bool {
	d := stripNonDigits(s)
	if len(d) != 9 {
		return false
	}
	area, group, serial := d[:3], d[3:5], d[5:]
	return area != "000" && area != "666" && area[0] != '9' && group != "00" && serial != "0000"
}

func phoneDigits(s string) bool {
	d := stripNonDigits(s)
	return len(d) >= 8 && len(d) <= 15 && !allSameDigit(d)
}

// validIBAN applies the ISO 13616 mod-97 checksum.
func validIBAN(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	rearranged := s[4:] + s[:4]
	var b strings.Builder
	for _, r := range rearranged {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteString(itoa(int(r-'A') + 10))
		default:
			return false
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	if !ok {
		return false
	}
	return new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

// validIPv4 confirms each octet is 0-255.
func validIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n := 0
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
			n = n*10 + int(r-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

func validIPv6(s string) bool {
	if i := strings.IndexByte(s, '%'); i >= 0 {
		s = s[:i]
	}
	if strings.Count(s, ":") < 2 || len(strings.Trim(s, ":")) < 2 {
		return false
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() == nil
}

func hasDigitAndLetter(s string) bool {
	var digit, letter bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letter = true
		}
	}
	return digit && letter
}

func hasNonLetter(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
