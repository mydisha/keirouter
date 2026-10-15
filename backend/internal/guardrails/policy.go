package guardrails

import (
	"encoding/json"
)

// Policy is the merged effective configuration for one request. It is built
// by the Resolver by stacking per-scope rows in order (global → provider →
// model → chain → apikey) so the most specific override wins per detector.
//
// Every field is a pointer so an explicit nil at a higher layer means
// "inherit", whereas a populated value at a higher layer means "override".
type Policy struct {
	// Enabled is the master switch. When non-nil and false, no detectors run.
	Enabled *bool `json:"enabled,omitempty"`

	PII       *PIIConfig       `json:"pii,omitempty"`
	Injection *InjectionConfig `json:"injection,omitempty"`
	Topics    *TopicsConfig    `json:"topics,omitempty"`
	Toxicity  *ToxicityConfig  `json:"toxicity,omitempty"`
	Bias      *BiasConfig      `json:"bias,omitempty"`
}

// IsActive reports whether the policy as a whole is on.
func (p Policy) IsActive() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

// PIIStrategy is how the PII detector handles matches.
type PIIStrategy string

const (
	PIIStrategyRedact    PIIStrategy = "redact"    // replace with <PII>
	PIIStrategyReplace   PIIStrategy = "replace"   // replace with <EMAIL_ADDRESS>
	PIIStrategyMask      PIIStrategy = "mask"      // keep first/last N chars
	PIIStrategyHash      PIIStrategy = "hash"      // sha256 short tag
	PIIStrategyBlock     PIIStrategy = "block"     // refuse the request
	PIIStrategyAnonymize PIIStrategy = "anonymize" // alias of replace, Presidio naming
)

// PIIConfig parametrizes the PII detector.
type PIIConfig struct {
	Enabled  bool        `json:"enabled"`
	Types    []string    `json:"types,omitempty"` // entity names (EMAIL_ADDRESS, ID_NIK, ...)
	Strategy PIIStrategy `json:"strategy,omitempty"`
	// MinScore (0.0–1.0) filters out low-confidence matches; default 0.5.
	MinScore float64 `json:"min_score,omitempty"`
	// ScanOutput also inspects the LLM response for leaked PII.
	ScanOutput bool `json:"scan_output,omitempty"`
	// Engine selects the implementation: "native" (default) or "presidio".
	Engine string `json:"engine,omitempty"`
	// BlockTypes lists entities that refuse the request outright whatever the
	// strategy, e.g. mask emails but block credit cards and API keys.
	BlockTypes []string `json:"block_types,omitempty"`
	// AllowList holds exact values that are never treated as PII, such as a
	// public support address. Matching is case-insensitive.
	AllowList []string `json:"allow_list,omitempty"`
	// Scope picks which parts of the conversation are scanned: "all"
	// (default: system prompt, every message, tool calls and tool results),
	// "user" (user messages and tool results) or "last_user" (only the most
	// recent user message).
	Scope string `json:"scope,omitempty"`
}

// PII scan scopes.
const (
	PIIScopeAll      = "all"
	PIIScopeUser     = "user"
	PIIScopeLastUser = "last_user"
)

// InjectionConfig parametrizes the prompt-injection detector.
type InjectionConfig struct {
	Enabled bool `json:"enabled"`
	// SeverityThreshold drops findings below this level (low | medium | high).
	SeverityThreshold Severity `json:"severity_threshold,omitempty"`
	// Action chosen when a finding meets the threshold. Defaults to block.
	Action Action `json:"action,omitempty"`
	// ScanToolResults also inspects tool results (fetched pages, files, API
	// responses) for indirect prompt injection.
	ScanToolResults bool `json:"scan_tool_results,omitempty"`

	// Classifier adds the ML classifier (Llama Prompt Guard 2 or a DeBERTa
	// injection model served by a sidecar). Ignored when the server has no
	// classifier configured.
	Classifier bool `json:"classifier,omitempty"`
	// ClassifierThreshold is the injection probability (0–1) treated as an
	// attack. Default 0.9; scores within 0.2 below it count as suspicious.
	ClassifierThreshold float64 `json:"classifier_threshold,omitempty"`
	// Semantic compares the prompt with a corpus of known attacks through the
	// configured embeddings provider, catching paraphrases with no keyword in
	// common. Ignored without an embeddings API.
	Semantic bool `json:"semantic,omitempty"`
	// SemanticThreshold is the cosine similarity treated as a match. Default 0.85.
	SemanticThreshold float64 `json:"semantic_threshold,omitempty"`
	// Judge asks an LLM to classify the prompt: "" or "off", "suspicious"
	// (only when another layer saw something below high severity, so it can
	// confirm or clear it) or "always".
	Judge string `json:"judge,omitempty"`
	// JudgeModel is the model (or alias / chain) the judge runs on.
	JudgeModel string `json:"judge_model,omitempty"`
	// Spotlight wraps tool results in secret delimiters and tells the model
	// that text inside them is data, never instructions.
	Spotlight bool `json:"spotlight,omitempty"`
	// Canary plants a secret marker in the system prompt and blocks any
	// response that repeats it, i.e. a leaked system prompt.
	Canary bool `json:"canary,omitempty"`
	// GuardToolCalls refuses tool calls the model makes in a turn whose tool
	// results look like a prompt injection, so injected instructions can't
	// trigger actions.
	GuardToolCalls bool `json:"guard_tool_calls,omitempty"`
}

// Injection judge modes.
const (
	JudgeOff        = "off"
	JudgeSuspicious = "suspicious"
	JudgeAlways     = "always"
)

// Detector defaults. The dashboard shows these values for unset fields, so
// the detectors must apply the same ones.
const (
	DefaultInjectionSeverity Severity = SeverityMedium
	DefaultInjectionAction   Action   = ActionBlock
	DefaultTopicsAction      Action   = ActionWarn
	DefaultToxicityThreshold          = 60
	DefaultToxicityAction    Action   = ActionWarn
	DefaultBiasThreshold              = 60
	DefaultBiasAction        Action   = ActionLogOnly
)

// TopicsConfig parametrizes the topic boundary detector.
type TopicsConfig struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"` // allow | block
	// Topics is a free-form list of topic keywords. Layer-1 detection uses
	// keyword/ngram match; when an Embedder is wired the detector also runs
	// Layer-2 cosine-similarity matching with SimilarityThreshold.
	Topics []string `json:"topics,omitempty"`
	Action Action   `json:"action,omitempty"`
	// Engine selects the matching strategy: "keyword" (default), "embedding"
	// (requires an embedder) or "llm" (an LLM judges the message in context,
	// lets small talk through on allow lists and understands paraphrases
	// and other languages; requires Model).
	Engine string `json:"engine,omitempty"`
	// SimilarityThreshold is the cosine-similarity cutoff used by the embedding
	// path (0.0–1.0). Default 0.6.
	SimilarityThreshold float64 `json:"similarity_threshold,omitempty"`
	// Model is the model (or alias / chain) the "llm" engine runs on.
	Model string `json:"model,omitempty"`
}

// ToxicityConfig parametrizes the toxicity detector.
type ToxicityConfig struct {
	Enabled    bool     `json:"enabled"`
	Categories []string `json:"categories,omitempty"` // profanity, hate, harassment, violence, sexual, self_harm
	Threshold  int      `json:"threshold,omitempty"`  // 0–100
	Action     Action   `json:"action,omitempty"`
	// Engine selects the implementation: "native" (default, keyword catalog)
	// or "openai" (OpenAI Moderation API). The OpenAI engine requires
	// credentials wired at startup; if missing the detector falls back to
	// native.
	Engine string `json:"engine,omitempty"`
}

// BiasConfig parametrizes the bias detector. Outbound-only — runs on the
// model response.
type BiasConfig struct {
	Enabled    bool     `json:"enabled"`
	Categories []string `json:"categories,omitempty"` // political, gender, ethnic, religious
	Threshold  int      `json:"threshold,omitempty"`  // 0–100
	Action     Action   `json:"action,omitempty"`
}

// MarshalPolicy serializes a Policy into its on-disk JSON form. The result
// goes into the guardrail_policies.config column.
func MarshalPolicy(p Policy) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalPolicy reads a Policy from the JSON column. Empty input yields the
// zero policy (everything inherits).
func UnmarshalPolicy(s string) (Policy, error) {
	if s == "" {
		return Policy{}, nil
	}
	var p Policy
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Merge layers src on top of dst. Any non-nil field on src replaces the
// corresponding field on dst (no deep-merge inside config structs — that
// would surprise users by letting Global leak into APIKey overrides).
// Pass policies in order least-specific → most-specific.
func Merge(dst, src Policy) Policy {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.PII != nil {
		dst.PII = src.PII
	}
	if src.Injection != nil {
		dst.Injection = src.Injection
	}
	if src.Topics != nil {
		dst.Topics = src.Topics
	}
	if src.Toxicity != nil {
		dst.Toxicity = src.Toxicity
	}
	if src.Bias != nil {
		dst.Bias = src.Bias
	}
	return dst
}
