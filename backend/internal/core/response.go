package core

// FinishReason explains why generation stopped.
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishLength    FinishReason = "length"
	FinishToolCalls FinishReason = "tool_calls"
	FinishError     FinishReason = "error"
	FinishFilter    FinishReason = "content_filter"
)

// UsageSource identifies whether token counts came from an upstream usage
// object, a router estimate, or a semantic-cache replay.
type UsageSource string

const (
	UsageSourceProvider  UsageSource = "provider"
	UsageSourceEstimated UsageSource = "estimated"
	UsageSourceCache     UsageSource = "cache"
	UsageSourceLegacy    UsageSource = "legacy"
	UsageSourceNone      UsageSource = "none"
)

// Usage reports token accounting for a completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedTokens counts prompt tokens served from a provider-side prompt cache
	// (cache reads — typically 50-90% cheaper than standard input).
	CachedTokens int `json:"cached_tokens,omitempty"`
	// CacheWriteTokens counts prompt tokens written into a provider-side prompt
	// cache (cache writes — often priced at 25% *more* than standard input).
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// ReasoningTokens is a subset of CompletionTokens, never an additional token
	// class. This invariant prevents reasoning output from being double charged.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	// Source is router-internal provenance and is never emitted to API clients.
	Source UsageSource `json:"-"`
}

// ChatResponse is the canonical non-streaming completion result.
type ChatResponse struct {
	ID           string       `json:"id"`
	Model        string       `json:"model"`
	Message      Message      `json:"message"`
	FinishReason FinishReason `json:"finish_reason"`
	Usage        Usage        `json:"usage"`
}

// ChunkType discriminates streaming events.
type ChunkType string

const (
	ChunkText     ChunkType = "text"      // incremental assistant text
	ChunkThinking ChunkType = "thinking"  // incremental reasoning text
	ChunkToolCall ChunkType = "tool_call" // (partial) tool invocation
	ChunkUsage    ChunkType = "usage"     // usage update (often final)
	ChunkFinish   ChunkType = "finish"    // terminal event with finish reason
	ChunkError    ChunkType = "error"     // mid-stream error
	ChunkPing     ChunkType = "ping"      // keep-alive / no-op
	// ChunkRedactedThinking carries an Anthropic redacted_thinking block
	// (opaque data in Delta). Only the Anthropic renderer emits it.
	ChunkRedactedThinking ChunkType = "redacted_thinking"
)

// StreamChunk is one provider-agnostic streaming event. The transform layer
// renders a sequence of these into the caller's SSE dialect.
type StreamChunk struct {
	Type ChunkType `json:"type"`

	// Delta carries incremental text for ChunkText / ChunkThinking.
	Delta string `json:"delta,omitempty"`

	// ToolCall carries a (possibly partial) tool invocation. Index identifies
	// which tool call this delta belongs to when multiple are streamed.
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Index    int       `json:"index,omitempty"`

	// Signature carries opaque reasoning-block data to echo back on later turns.
	Signature string `json:"signature,omitempty"`

	FinishReason FinishReason `json:"finish_reason,omitempty"`
	Usage        *Usage       `json:"usage,omitempty"`
	Err          error        `json:"-"`
}
