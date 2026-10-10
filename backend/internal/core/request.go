package core

import "encoding/json"

// Dialect identifies an API wire format spoken by a client or a provider.
type Dialect string

const (
	DialectOpenAI          Dialect = "openai"           // /v1/chat/completions
	DialectOpenAIResponses Dialect = "openai_responses" // /v1/responses
	DialectAnthropic       Dialect = "anthropic"        // /v1/messages
	DialectGemini          Dialect = "gemini"           // :generateContent
	DialectOllama          Dialect = "ollama"           // /api/chat

	// Provider-specific upstream dialects. These identify reverse-engineered or
	// vendor-proprietary wire formats spoken by certain providers. A provider
	// carrying one of these is registered for discovery and account management,
	// but only becomes routable once a dedicated connector for the dialect
	// lands. The default registry skips creating connectors for dialects it
	// cannot yet drive.
	DialectKiro        Dialect = "kiro"         // AWS CodeWhisperer eventstream
	DialectGeminiCLI   Dialect = "gemini_cli"   // Google CloudCode internal
	DialectVertex      Dialect = "vertex"       // Vertex AI (SA-authenticated)
	DialectCursor      Dialect = "cursor"       // Cursor connect-proto
	DialectAntigravity Dialect = "antigravity"  // Antigravity CloudCode
	DialectCommandCode Dialect = "command_code" // Command Code generate API
	DialectQoder       Dialect = "qoder"        // Qoder COSY-signed inference
	DialectMimoFree    Dialect = "mimo_free"    // Xiaomi MiMo free tier (bootstrap JWT)
	DialectWebCookie   Dialect = "web_cookie"   // browser-session cookie providers
)

// ChatRequest is the canonical, dialect-independent representation of a chat
// completion request. The transform layer parses inbound bodies into this and
// renders it back out per the target connector's dialect.
type ChatRequest struct {
	// Model is the requested model id, already stripped of any router alias
	// prefix (e.g. "kr/claude-sonnet-4.5" -> "claude-sonnet-4.5").
	Model string `json:"model"`

	Messages []Message `json:"messages"`

	// System holds top-level system instructions hoisted out of Messages for
	// dialects that carry them separately (Anthropic, Gemini).
	System string `json:"system,omitempty"`
	// SystemCacheControl is an Anthropic cache breakpoint that was attached to
	// the (last) system block; the flattened System text is cached as a whole.
	SystemCacheControl json.RawMessage `json:"system_cache_control,omitempty"`

	Tools      []Tool `json:"tools,omitempty"`
	ToolChoice any    `json:"tool_choice,omitempty"`
	// ParallelToolCalls, when set, requests (true) or forbids (false) the
	// model issuing several tool calls in one turn. OpenAI: parallel_tool_calls;
	// Anthropic: tool_choice.disable_parallel_tool_use. Nil leaves the
	// provider default.
	ParallelToolCalls *bool    `json:"parallel_tool_calls,omitempty"`
	Stop              []string `json:"stop,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`

	// Stream requests an incremental SSE response.
	Stream bool `json:"stream"`

	// IncludeUsage reflects the client's opt-in to receive a usage event on the
	// streaming response (OpenAI's stream_options.include_usage). When set, the
	// pipeline guarantees a final usage event is emitted to the client even if
	// the upstream provider never reports one — synthesizing an estimate as a
	// fallback. Router-internal; never serialized upstream.
	IncludeUsage bool `json:"-"`

	// Reasoning controls extended thinking / reasoning effort where supported.
	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`

	// PreviousResponseID chains a Responses API request to the server-side
	// state of an earlier response. It is only meaningful for a Responses
	// dialect upstream; other dialects cannot honour it.
	PreviousResponseID string `json:"previous_response_id,omitempty"`

	// ResponseFormat carries structured-output constraints (json_schema, etc).
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`

	// Metadata is router-internal context (not serialized upstream) such as the
	// originating client and the resolved capability requirements.
	Metadata RequestMetadata `json:"-"`

	// Extra preserves dialect-specific fields the canonical model does not model
	// explicitly, so they can be passed through on same-dialect routing.
	Extra map[string]json.RawMessage `json:"-"`
}

// ReasoningConfig expresses extended-thinking intent in a provider-neutral way.
type ReasoningConfig struct {
	// Effort is one of "low", "medium", "high" (maps to provider knobs).
	Effort string `json:"effort,omitempty"`
	// MaxTokens caps the thinking budget where the provider supports it.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// Tool is a function/tool definition advertised to the model.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Raw holds a provider-native tool definition that has no function-style
	// equivalent (Anthropic server tools such as web_search_20250305, bash,
	// text_editor, computer). It is forwarded verbatim to a same-dialect
	// upstream and dropped for other dialects.
	Raw json.RawMessage `json:"raw,omitempty"`
	// CacheControl is an Anthropic prompt-caching breakpoint on this tool.
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	// Parameters is a JSON Schema object describing the tool arguments.
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// RequestMetadata is router-internal context attached during the pipeline.
type RequestMetadata struct {
	// ClientKind is the detected calling tool (claude-code, cursor, codex, ...).
	ClientKind string
	// ClientBetas are the anthropic-beta features the client asked for
	// (context-1m, interleaved-thinking, ...). Forwarded to the Anthropic
	// connector so API-key users keep the features their client relies on.
	ClientBetas []string
	// SourceDialect is the wire format the client used.
	SourceDialect Dialect
	// APIKeyID is the id of the authenticated KeiRouter key (for metering/audit).
	APIKeyID string
	// TenantID / ProjectID scope the request for multi-tenant deployments.
	TenantID  string
	ProjectID string
	// RequestID is generated at the gateway edge and propagated through the
	// pipeline so audit log rows can be correlated with usage records.
	RequestID string
	// Provider is the resolved upstream provider alias (openai, anthropic, ...).
	// Populated by the gateway after dispatcher target resolution; used by the
	// guardrails resolver for provider-scoped policy lookup.
	Provider string
	// ChainID is the routing chain id when the request was routed through one
	// (empty for direct provider/model targeting). Used by the guardrails
	// resolver for chain-scoped policy lookup.
	ChainID string
	// FallbackCount is how many upstream attempts failed over to a later
	// target before the terminal attempt. The pipeline increments it at each
	// fallback so the persisted usage row records whether the first choice
	// served the request.
	FallbackCount int
	// ContextAffinityKey is a privacy-safe, stable conversation/session key used
	// to keep account routing and provider-side session/cache IDs sticky across
	// fallback and follow-up requests.
	ContextAffinityKey string
	// RequiredCapabilities the chosen model must satisfy (anti-downgrade guard).
	RequiredCapabilities CapabilitySet
}
