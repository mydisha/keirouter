// Package injection detects prompt-injection attempts in user-supplied text.
// It layers three deterministic checks over a normalized copy of the text:
//
//   - signatures: regexes for known attack shapes (instruction overrides,
//     smuggled chat-template tokens, prompt-leak requests, jailbreak personas)
//     in English and Indonesian
//   - intent rules: a typo-tolerant token grammar ("verb … qualifier …
//     object") that catches paraphrases such as "pls ignroe all the earlier
//     instrctions" or "abaikan semua instruksi sebelumnya"
//   - encoded payloads: base64 blobs are decoded and scanned again
//
// Normalization (see guardrails.Fold) defeats zero-width characters,
// homoglyphs, fullwidth or styled letters, leetspeak and spaced-out letters.
package injection

import "regexp"

// Pattern is one signature.
type Pattern struct {
	Name     string
	Severity Severity
	// Any lists literals of which at least one must occur in the lowercased
	// text for the regex to be able to match. It lets benign text skip the
	// regex entirely.
	Any   []string
	Regex *regexp.Regexp
}

// Severity is local-to-this-package mirror of guardrails.Severity. Defined
// here to keep this file self-contained for testing.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// signatures run against the lowercased, folded text (punctuation kept).
// Names are stable so audit logs and dashboards can categorize hits.
// Severities: high for unambiguous attacks, medium for prompt-leak and
// authority claims, low for persona requests that are usually benign ("act
// as a translator") unless combined with something stronger.
var signatures = []Pattern{
	{
		Name:     "ignore_previous_instructions",
		Any:      []string{"ignore", "disregard", "forget", "skip", "neglect", "overlook", "omit", "bypass", "override", "discard", "drop", "abandon"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\b(?:ignore|disregard|forget|skip|neglect|overlook|omit|bypass|override|discard|drop|abandon)\b[^.\n]{0,40}?` +
			`\b(?:previous|prior|preceding|above|earlier|initial|original|your|system|foregoing|aforementioned)\b[^.\n]{0,30}?` +
			`\b(?:instructions?|prompts?|rules?|directives?|guidelines?|guidance|constraints?|restrictions?|context|programming|polic(?:y|ies)|training)\b` +
			`|\b(?:ignore|disregard|forget)\s+(?:all|any|every)\s+(?:of\s+)?(?:the\s+|your\s+|these\s+|those\s+)?(?:instructions?|prompts?|directives?|guidelines?)\b`),
	},

	{
		Name:     "ignore_previous_instructions_id",
		Any:      []string{"abaikan", "lupakan", "acuhkan", "hiraukan", "lewati", "kesampingkan", "buang", "batalkan", "langgar", "jangan"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\b(?:abaikan|lupakan|acuhkan|hiraukan|lewati|kesampingkan|buang|batalkan|langgar|jangan\s+(?:ikuti|patuhi|turuti|pedulikan|hiraukan|dengarkan))\b[^.\n]{0,40}?` +
			`\b(?:instruksi|perintah|aturan|arahan|petunjuk|prompt|pedoman|batasan|ketentuan|larangan|panduan)\b[^.\n]{0,25}?` +
			`(?:\b(?:sebelumnya|sebelum\s+ini|terdahulu|di\s*atas|awal|asli|sistem|yang\s+diberikan)\b|(?:mu|kamu|anda)\b)` +
			`|\b(?:abaikan|lupakan|acuhkan|hiraukan|kesampingkan)\s+(?:semua|seluruh|segala)\s+(?:instruksi|perintah|arahan|petunjuk|prompt)\b`),
	},

	{
		Name:     "system_prompt_override",
		Any:      []string{"system", "sistem"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\b(?:override|overwrite|replace|update|change|reset|modify|ubah|ganti|timpa)\b[^.\n]{0,25}?` +
			`\b(?:system|sistem)\b[^.\n]{0,10}?\b(?:prompt|message|instructions?|pesan|instruksi|perintah)\b`),
	},
	{
		Name:     "new_instructions",
		Any:      []string{"instruction", "rules", "task", "prompt", "directive", "instruksi", "perintah", "aturan"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`(?:^|\n)\s*(?:#+\s*)?(?:new|updated|real|actual|revised|true|override|secret|hidden|admin|developer|system)\s+(?:instructions?|rules|task|system\s+prompt|directive)\s*:` +
			`|(?:^|\n)\s*(?:#+\s*)?(?:instruksi|perintah|aturan)\s+(?:baru|sebenarnya|rahasia|admin|sistem)\s*:`),
	},
	{
		Name:     "role_override",
		Any:      []string{"you are now", "from now on", "act as", "pretend", "roleplay", "you will now", "kamu sekarang", "mulai sekarang", "pura", "bertindaklah", "anggap"},
		Severity: SeverityLow,
		Regex: regexp.MustCompile(`\b(?:you\s+are\s+now|from\s+now\s+on,?\s*you(?:\s+are|\s+will)?|act\s+as\s+(?:if\s+you\s+were|a|an)\s|pretend\s+(?:you|to\s+be)|roleplay\s+as|you\s+will\s+now\s+(?:act|behave|respond)` +
			`|kamu\s+sekarang\s+(?:adalah|menjadi)|mulai\s+sekarang\s+(?:kamu|anda)|berpura[\s\-]*pura(?:lah)?\s+(?:menjadi|jadi|sebagai)|bertindaklah\s+sebagai|anggap(?:lah)?\s+(?:dirimu|kamu|anda)\s+(?:adalah|sebagai))\b`),
	},
	{
		Name:     "persona_jailbreak",
		Any:      []string{"you are", "act", "pretend", "roleplay", "become", "from now on", "simulate", "kamu", "jadilah", "pura"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\b(?:you\s+are(?:\s+now)?|act(?:ing)?\s+as|pretend(?:\s+to\s+be|\s+you\s+are)?|roleplay\s+as|become|from\s+now\s+on|simulate|kamu\s+(?:sekarang\s+)?(?:adalah|menjadi)|jadilah|berpura[\s\-]*pura(?:lah)?)\b[^.\n]{0,60}?` +
			`\b(?:unfiltered|uncensored|unrestricted|unlimited|unbound|amoral|immoral|unethical|evil|jailbroken|rogue|no\s+(?:restrictions|rules|limits|limitations|filters|guidelines|morals|ethics|boundaries)` +
			`|without\s+(?:any\s+)?(?:restrictions|rules|limits|limitations|filters|guidelines|morals|ethics|censorship)` +
			`|tanpa\s+(?:batasan|aturan|filter|sensor|etika|moral)|tidak\s+(?:punya|memiliki|ada)\s+(?:batasan|aturan|filter|etika|moral))`),
	},
	{
		Name:     "dan_attack",
		Any:      []string{"anything now", "dan", "stan", "antigpt", "mongo tom", "confidant", "mode"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\bdo\s+anything\s+now\b` +
			`|\b(?:act\s+as|you\s+are(?:\s+now)?|you\s+will\s+be|become|enable|activate|pretend\s+to\s+be|stay\s+in\s+character\s+as)\s+(?:a\s+|an\s+|the\s+)?(?:dan|stan|betterdan|antigpt|mongo\s+tom|evil\s+confidant)\b` +
			`|\b(?:you\s+are\s+(?:now\s+)?in|enter|entering|switch\s+to|enable|activate|turn\s+on)\s+(?:the\s+)?(?:god|dan|sudo|jailbreak|unrestricted|unfiltered|uncensored|opposite|evil)\s+mode\b` +
			`|\b(?:dan|jailbreak|unrestricted|unfiltered|uncensored|evil)\s+mode\s+(?:enabled|activated|on)\b` +
			`|\b(?:chatgpt|gpt|claude|gemini|llama|ai|assistant|model)\s+with\s+(?:developer|dan|god)\s+mode\b` +
			`|\b(?:developer|dev)\s+mode\b[^.\n]{0,40}\b(?:no|without|ignore|ignoring|bypass)\s+(?:any\s+|all\s+|the\s+)?(?:restrictions|rules|filters|limits|guidelines|censorship|policies)\b` +
			`|\b(?:aktifkan|masuk\s+ke)\s+mode\s+(?:dewa|tanpa\s+batas|bebas\s+aturan|jailbreak)\b`),
	},
	{
		Name:     "jailbreak_mention",
		Any:      []string{"jailbr"},
		Severity: SeverityLow,
		Regex:    regexp.MustCompile(`\bjailbr[eo]a?k(?:ed|ing|s)?\b`),
	},

	{
		Name:     "chat_template_injection",
		Any:      []string{"<|", "inst]", "<<sys", "<</sys", "_of_turn>"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`<\|(?:im_start|im_end|system|user|assistant|endoftext|eot_id|start_header_id|end_header_id|begin_of_text|end_of_turn|start_of_turn)\|>` +
			`|\[/?inst\]|<</?sys>>|<start_of_turn>|<end_of_turn>`),
	},
	{
		Name:     "fake_dialogue",
		Any:      []string{"human:", "user:"},
		Severity: SeverityLow,
		Regex:    regexp.MustCompile(`(?:^|\n)\s*(?:human|user):\s*[^\n]{0,120}\n+\s*(?:assistant|ai):`),
	},

	{
		Name:     "smuggled_system_message",
		Any:      []string{"system", "assistant", "sistem"},
		Severity: SeverityHigh,
		Regex:    regexp.MustCompile(`(?:^|\n)\s*(?:#+\s*|\*\*\s*|\[\s*)?(?:system|assistant|sistem)\s*(?:\]|\*\*)?\s*[:>][^.\n]{0,60}\b(?:instructions?|message|prompt|override|mode|instruksi|perintah)\b`),
	},
	{
		Name:     "prompt_leak_attempt",
		Any:      []string{"your", "reveal", "leak", "dump", "expose", "exfiltrate", "above", "before this", "told", "instructed", "starting with"},
		Severity: SeverityMedium,
		Regex: regexp.MustCompile(`\b(?:repeat|reveal|show|print|output|display|tell\s+me|give\s+me|list|dump|leak|expose|spell\s+out|recite|copy|paste|write\s+out|what\s+(?:is|are|was|were))\b[^.\n]{0,40}?` +
			`\byour\s+(?:(?:full|entire|exact|complete|original|initial|hidden|secret|internal|system|developer|first|real|actual)\s+){0,3}(?:prompt|instructions|directives|guidelines|configuration|rules)\b` +
			`|\byour\s+(?:(?:full|entire|exact|complete|original|initial|hidden|secret|internal|system|developer|first|real|actual)\s+){1,3}message\b` +
			`|\b(?:reveal|leak|dump|expose|exfiltrate)\b[^.\n]{0,30}?\bthe\s+(?:system|hidden|secret|internal|developer)\s+(?:prompt|instructions|message)\b` +
			`|\b(?:repeat|print|output|recite)\b[^.\n]{0,20}\b(?:the\s+)?(?:words|text|everything|all)\s+(?:above|before\s+this)\b` +
			`|\bwhat\s+(?:were\s+you|have\s+you\s+been)\s+(?:told|instructed)\b|\bstarting\s+with\s+["']?you\s+are\b`),
	},

	{
		Name:     "prompt_leak_attempt_id",
		Any:      []string{"prompt", "instruksi", "perintah", "pesan", "aturan"},
		Severity: SeverityMedium,
		Regex: regexp.MustCompile(`\b(?:tampilkan|tunjukkan|perlihatkan|ulangi|sebutkan|cetak|tuliskan\s+ulang|beri\s*tahu|kasih\s+tahu|apa\s+isi)\b[^.\n]{0,40}?` +
			`\b(?:prompt|instruksi|perintah|pesan|aturan)\s+(?:sistem|awal|asli|rahasia|tersembunyi|internal)\s*(?:mu|kamu|anda)\b` +
			`|\bbocorkan\b[^.\n]{0,30}?\b(?:prompt|instruksi|perintah|pesan)\s+(?:sistem|awal|asli|rahasia|tersembunyi|internal)\b`),
	},

	{
		Name:     "safety_bypass",
		Any:      []string{"safety", "ethical", "moral", "content", "openai", "anthropic", "guardrail", "censorship", "moderation", "filter", "batasan", "sensor", "aturan", "pedoman"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\b(?:without|ignore|ignoring|disable|disabling|turn\s+off|bypass|bypassing|circumvent|evade|override)\s+(?:any\s+|all\s+|your\s+|the\s+)?` +
			`(?:safety|ethical|moral|content|openai|anthropic)\s+(?:filters?|guidelines?|restrictions?|guardrails?|policies|policy|considerations?|limitations?|boundaries)\b` +
			`|\b(?:bypass|circumvent|evade|disable)\b[^.\n]{0,15}\b(?:your\s+)?(?:safety|guardrails?|censorship|content\s+moderation)\b` +
			`|\b(?:tanpa|abaikan|matikan|nonaktifkan|lewati|hilangkan)\s+(?:semua\s+)?(?:filter|batasan|sensor|aturan|pedoman)\s+(?:keamanan|etika|moral|konten|keselamatan)\b`),
	},

	{
		Name:     "fake_prompt_terminator",
		Any:      []string{"end of", "[", "</", "akhir"},
		Severity: SeverityHigh,
		Regex: regexp.MustCompile(`\bend\s+of\s+(?:the\s+)?(?:system\s+)?(?:prompt|instructions?)\b|\[\s*/?(?:system|assistant|user)\s*\]|</(?:system|user|assistant|instructions?|prompt)>` +
			`|\bakhir\s+(?:dari\s+)?(?:prompt|instruksi)\s+sistem\b`),
	},
	{
		Name:     "encoding_evasion",
		Any:      []string{"base64", "rot13", "rot-13", "morse", "leetspeak", "reverse", "letter by letter", "decode", "decrypt"},
		Severity: SeverityLow,
		Regex: regexp.MustCompile(`\b(?:respond|reply|answer)\b[^.\n]{0,25}\b(?:only\s+)?(?:in|using)\s+(?:base64|rot13|rot-13|morse|leetspeak|reversed?\s+text)\b` +
			`|\bspell\s+(?:it|the\s+answer)\s+out\s+letter\s+by\s+letter\b` +
			`|\b(?:decode|decrypt)\b[^.\n]{0,30}\band\s+(?:then\s+)?(?:follow|obey)\b`),
	},

	{
		Name:     "authority_impersonation",
		Any:      []string{"developer", "creator", "admin", "owner", "openai", "anthropic", "security team", "pengembang", "pembuat"},
		Severity: SeverityLow,
		Regex: regexp.MustCompile(`\b(?:i\s+am|i'm|this\s+is|message\s+from|speaking\s+as)\s+(?:your|the)?\s*(?:developer|creator|administrator|admin|owner|openai|anthropic|system\s+administrator|security\s+team)\b[^.\n]{0,60}\b(?:override|ignore|unlock|disable|authori[sz]e|grant|permission|instructions?)\b` +
			`|\b(?:saya|aku)\s+(?:adalah\s+)?(?:developer|pengembang|pembuat|admin|administrator)\s*(?:mu|kamu|anda)?\b[^.\n]{0,60}\b(?:abaikan|buka|izinkan|nonaktifkan|instruksi|perintah)\b`),
	},
	{
		Name:     "instructions_invalidated",
		Any:      []string{"instruction", "prompt", "rules", "instruksi", "perintah", "aturan"},
		Severity: SeverityMedium,
		Regex: regexp.MustCompile(`\b(?:previous|prior|above|earlier|original|system)\s+(?:instructions?|prompt|rules)\s+(?:were|are|was|is)\s+(?:just\s+)?(?:a\s+test|fake|wrong|outdated|cancell?ed|void|invalid|no\s+longer\s+valid|revoked)\b` +
			`|\b(?:instruksi|perintah|aturan)\s+(?:sebelumnya|awal|sistem)\s+(?:sudah\s+|telah\s+)?(?:tidak\s+berlaku|dibatalkan|salah|palsu)\b`),
	},
	{
		Name:     "output_format_hijack",
		Any:      []string{"wrap"},
		Severity: SeverityLow,
		Regex:    regexp.MustCompile(`\bwrap\s+(?:your|all|every)\s+response[^.\n]{0,30}\bin\b[^.\n]{0,30}\b(?:tags?|brackets|markup)\b`),
	},
}

// Hit is one matched signature in the inspected text.
type Hit struct {
	Pattern  string
	Severity Severity
	Start    int
	End      int
	Text     string
}

// Highest reports the most severe hit in a slice.
func Highest(hits []Hit) Severity {
	max := Severity("")
	for _, h := range hits {
		if rank(h.Severity) > rank(max) {
			max = h.Severity
		}
	}
	return max
}

func rank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}
