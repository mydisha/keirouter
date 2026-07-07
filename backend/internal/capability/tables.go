package capability

// This file holds the capability data tables, resolved by ResolveProfile via a
// four-step fallback chain. Each entry declares only the fields that differ
// from defaultProfile; unset fields inherit the floor.
//
// Adding or updating a model:
//   - input modalities  -> Vision / PDF / AudioInput / VideoInput
//   - output modalities -> ImageOutput / AudioOutput
//   - reasoning -> Reasoning   tool calling -> NoTools (to disable)
//   - context/output limits -> ContextWindow / MaxOutput
//
// If a pattern already covers a model correctly, nothing is needed. Add an
// exact entry to modelCapabilities for exceptions a pattern would mis-match,
// or a new ordered patternCaps entry (specific before generic) for a family.

// serviceKindCapabilities maps a dashboard media-service kind to the runtime
// modality flags it implies, so user-defined media models are not treated as
// text-only.
var serviceKindCapabilities = map[string]caps{
	"imageToText": {Vision: true},
	"image":       {ImageOutput: true},
	"stt":         {AudioInput: true},
	"tts":         {AudioOutput: true},
	"embedding":   {NoTools: true},
}

// capabilitiesFromServiceKind returns the modality override for a media-service
// kind, or false if the kind is unknown.
func capabilitiesFromServiceKind(kind string) (caps, bool) {
	c, ok := serviceKindCapabilities[kind]
	return c, ok
}

// modelCapabilities holds canonical exact-id overrides for models that a glob
// pattern would otherwise mis-match. Keyed by bare model id (vendor prefix
// stripped before lookup).
var modelCapabilities = map[string]caps{
	// Claude 4.6/4.7/4.8 carry a 1M context window and adaptive thinking,
	// overriding the generic claude budget-thinking pattern.
	"claude-opus-4.6":          {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4.7":          {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4-7":          {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4.8":          {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4-6":          {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4-8":          {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4.8-thinking": {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-opus-4-8-thinking": {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-sonnet-4.6":        {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},
	"claude-sonnet-4-6":        {Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive", ContextWindow: 1000000, MaxOutput: 128000},

	// Image-generation variant (no tool calling).
	"gpt-image-1": {ImageOutput: true, NoTools: true},

	// GLM vision variant (the text GLM family has no vision).
	"glm-4.6v": {Vision: true, Reasoning: true, ThinkingFormat: "zai", ContextWindow: 128000},

	// Qwen alias models surfaced by the registry.
	"vision-model": {Vision: true, Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 1000000},
	"coder-model":  {Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 1000000},
}

// providerCapabilities holds provider-specific overrides keyed by provider
// alias then full model id. These win over modelCapabilities and patterns.
var providerCapabilities = map[string]map[string]caps{
	// CodeBuddy exposes authoritative per-model metadata via its gateway
	// config; every model reasons through OpenAI-style reasoning_effort.
	// onlyReasoning models cannot disable thinking (clamped to minimal).
	"codebuddy": {
		"glm-5.2":            {Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 1000000, MaxOutput: 48000},
		"glm-5.1":            {Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 200000, MaxOutput: 48000},
		"glm-5.0":            {Reasoning: true, ThinkingFormat: "openai", ContextWindow: 200000, MaxOutput: 48000},
		"glm-5.0-turbo":      {Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 200000, MaxOutput: 48000},
		"glm-5v-turbo":       {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 200000, MaxOutput: 38000},
		"glm-4.7":            {Reasoning: true, ThinkingFormat: "openai", ContextWindow: 200000, MaxOutput: 48000},
		"minimax-m3":         {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 512000, MaxOutput: 48000},
		"minimax-m2.7":       {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 200000, MaxOutput: 48000},
		"kimi-k2.7":          {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 256000, MaxOutput: 32000},
		"kimi-k2.6":          {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 256000, MaxOutput: 32000},
		"kimi-k2.5":          {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 164000, MaxOutput: 32000},
		"hy3-preview":        {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 192000, MaxOutput: 64000},
		"deepseek-v4-pro":    {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 1000000, MaxOutput: 50000},
		"deepseek-v4-flash":  {Vision: true, Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 1000000, MaxOutput: 50000},
		"deepseek-v3-2-volc": {Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 96000, MaxOutput: 32000},
	},
	// Kimchi is an OpenAI-compatible gateway that proxies multiple upstream
	// providers. Reasoning effort is OpenAI-style. Claude models use their
	// native thinking format (handled via pattern fallback).
	"kimchi": {
		"minimax-m3":           {Vision: true, Reasoning: true, ThinkingFormat: "openai", ContextWindow: 1048576, MaxOutput: 512000},
		"minimax-m2.7":         {Reasoning: true, ThinkingFormat: "openai", ThinkingLocked: true, ContextWindow: 204800, MaxOutput: 131072},
		"kimi-k2.7":            {Vision: true, Reasoning: true, ThinkingFormat: "kimi", ContextWindow: 262144, MaxOutput: 262144},
		"kimi-k2.6":            {Vision: true, Reasoning: true, ThinkingFormat: "kimi", ContextWindow: 262144, MaxOutput: 262144},
		"kimi-k2.5":            {Vision: true, Reasoning: true, ThinkingFormat: "kimi", ContextWindow: 164000, MaxOutput: 262144},
		"nemotron-3-ultra-fp4": {Reasoning: true, ThinkingFormat: "openai", ContextWindow: 128000},
	},
}

// patternCapabilities is the ordered glob fallback. ORDER MATTERS: vision and
// version-specific variants come before text-only or generic family patterns,
// so a broad family pattern never swallows a more specific exception. The first
// matching pattern wins.
var patternCapabilities = []patternCaps{
	// Claude — 4.6+ uses adaptive thinking; older/haiku use budget thinking.
	{"*claude*opus-4.6*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive"}},
	{"*claude*opus-4.7*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive"}},
	{"*claude*opus-4.8*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive"}},
	{"*claude*sonnet-4.6*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive"}},
	{"*claude*sonnet-4.7*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-adaptive"}},
	{"*claude*haiku*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-budget"}},
	{"*claude*opus*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-budget"}},
	{"*claude*sonnet*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-budget"}},
	{"*claude*fable*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-budget", ContextWindow: 1000000, MaxOutput: 128000}},
	{"*claude*mythos*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-budget", ContextWindow: 1000000, MaxOutput: 128000}},
	{"*claude-3*", caps{Vision: true}},
	{"*claude*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "claude-budget"}},

	// Gemini — 2.0+ is multimodal with google_search grounding and 1M context.
	{"*gemini*image*", caps{Vision: true, ImageOutput: true, ContextWindow: 1048576}},
	{"*gemini-3*pro*", caps{Vision: true, AudioInput: true, VideoInput: true, Reasoning: true, Search: true, ThinkingFormat: "gemini-level", ThinkingLocked: true, ContextWindow: 1048576, MaxOutput: 65535}},
	{"*gemini-3*", caps{Vision: true, AudioInput: true, VideoInput: true, Reasoning: true, Search: true, ThinkingFormat: "gemini-level", ThinkingLocked: true, ContextWindow: 1048576, MaxOutput: 65536}},
	{"*gemini-2.5*", caps{Vision: true, AudioInput: true, VideoInput: true, Reasoning: true, Search: true, ThinkingFormat: "gemini-budget", ThinkingRange: &ThinkingRange{Min: 0, Max: 24576}, ContextWindow: 1048576, MaxOutput: 65536}},
	{"*gemini-2*", caps{Vision: true, AudioInput: true, VideoInput: true, Search: true, ContextWindow: 1048576, MaxOutput: 65536}},
	{"*gemini*", caps{Vision: true, Search: true, ContextWindow: 1048576}},
	{"*gemma*", caps{Vision: true, ContextWindow: 128000}},
	{"*nanobanana*", caps{Vision: true, ImageOutput: true}},

	// OpenAI GPT-5.x — vision, thinking, and web search.
	{"*gpt-5*image*", caps{ImageOutput: true, Structured: true}},
	{"*gpt-5*codex*", caps{Reasoning: true, Search: true, Structured: true, ThinkingFormat: "openai", ContextWindow: 400000, MaxOutput: 128000}},
	{"*gpt-5*", caps{Vision: true, Reasoning: true, Search: true, Structured: true, ThinkingFormat: "openai", ContextWindow: 400000, MaxOutput: 128000}},
	{"*gpt-4o*", caps{Vision: true, Search: true, Structured: true, ContextWindow: 128000, MaxOutput: 16384}},
	{"*gpt-4.1*", caps{Vision: true, Structured: true, ContextWindow: 1000000, MaxOutput: 32768}},
	{"*gpt-4-turbo*", caps{Vision: true, Structured: true, ContextWindow: 128000}},
	{"*gpt-4*", caps{Structured: true, ContextWindow: 128000}},
	{"*gpt-3.5*", caps{ContextWindow: 16385, MaxOutput: 4096}},
	{"*gpt-oss*", caps{Reasoning: true, ThinkingFormat: "openai", ContextWindow: 128000}},

	// OpenAI o-series — reasoning and (o1+) vision.
	{"*o1-mini*", caps{Reasoning: true, Structured: true, ThinkingFormat: "openai", ContextWindow: 128000}},
	{"*o1*", caps{Vision: true, Reasoning: true, Structured: true, ThinkingFormat: "openai", ContextWindow: 200000, MaxOutput: 100000}},
	{"*o3*", caps{Vision: true, Reasoning: true, Structured: true, ThinkingFormat: "openai", ContextWindow: 200000, MaxOutput: 100000}},
	{"*o4*", caps{Vision: true, Reasoning: true, Structured: true, ThinkingFormat: "openai", ContextWindow: 200000, MaxOutput: 100000}},

	// Grok — vision plus Live Search.
	{"*grok*image*", caps{ImageOutput: true}},
	{"*grok-code*", caps{Reasoning: true, ThinkingFormat: "openai", ContextWindow: 256000}},
	{"*grok-4*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "openai", ContextWindow: 256000}},
	{"*grok-3*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "openai", ContextWindow: 131072}},
	{"*grok*", caps{Vision: true, Reasoning: true, Search: true, ThinkingFormat: "openai", ContextWindow: 256000}},

	// Qwen — enable_thinking + thinking_budget; QwQ is thinking-only.
	{"*qwen*vl*", caps{Vision: true, Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 262144}},
	{"*qwen*max*", caps{Vision: true, Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 1000000, MaxOutput: 65536}},
	{"*qwen*plus*", caps{Vision: true, Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 1000000, MaxOutput: 65536}},
	{"*qwen*235b*", caps{Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 262144}},
	{"*qwen*coder*", caps{Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 1000000}},
	{"*qwq*", caps{Reasoning: true, ThinkingFormat: "qwen", ThinkingLocked: true, ContextWindow: 131072}},
	{"*qwen*", caps{Reasoning: true, ThinkingFormat: "qwen", ContextWindow: 262144}},

	// Kimi — enabled maps to reasoning_effort; K2.7-code cannot disable.
	{"*kimi*k2.7*code*", caps{Vision: true, Reasoning: true, ThinkingFormat: "kimi", ThinkingLocked: true, ContextWindow: 262144, MaxOutput: 262144}},
	{"*kimi*k2*", caps{Vision: true, Reasoning: true, ThinkingFormat: "kimi", ContextWindow: 262144, MaxOutput: 262144}},
	{"*kimi*", caps{Reasoning: true, ThinkingFormat: "kimi", ContextWindow: 262144}},

	// GLM / Z.ai — thinking.enabled; disabled via enable_thinking:false.
	{"*glm-5*", caps{Reasoning: true, ThinkingFormat: "zai", ContextWindow: 200000, MaxOutput: 128000}},
	{"*glm-4.7*", caps{Reasoning: true, ThinkingFormat: "zai", ContextWindow: 200000, MaxOutput: 128000}},
	{"*glm-4*", caps{Reasoning: true, ThinkingFormat: "zai", ContextWindow: 200000}},
	{"*glm*", caps{Reasoning: true, ThinkingFormat: "zai", ContextWindow: 200000}},

	// DeepSeek — thinking.enabled + reasoning_effort; r1 is thinking-only.
	{"*deepseek-v4*", caps{Reasoning: true, ThinkingFormat: "deepseek", ContextWindow: 1000000, MaxOutput: 384000}},
	{"*reasoner*", caps{Reasoning: true, ThinkingFormat: "deepseek", ThinkingLocked: true, ContextWindow: 128000}},
	{"*deepseek-r*", caps{Reasoning: true, ThinkingFormat: "deepseek", ThinkingLocked: true, ContextWindow: 128000}},
	{"*deepseek-chat*", caps{ContextWindow: 128000}},
	{"*deepseek*", caps{Reasoning: true, ThinkingFormat: "deepseek", ContextWindow: 128000}},

	// MiniMax — M3 is adaptive; M2.x cannot disable thinking.
	{"*minimax*image*", caps{ImageOutput: true}},
	{"*minimax-m3*", caps{Vision: true, Reasoning: true, ThinkingFormat: "minimax", ContextWindow: 1048576, MaxOutput: 512000}},
	{"*minimax-m2.7*", caps{Reasoning: true, ThinkingFormat: "minimax", ThinkingLocked: true, ContextWindow: 204800, MaxOutput: 131072}},
	{"*minimax*", caps{Reasoning: true, ThinkingFormat: "minimax", ThinkingLocked: true, ContextWindow: 200000, MaxOutput: 131072}},

	// Xiaomi MiMo — vision, large context.
	{"*mimo*v2.5*", caps{Vision: true, ContextWindow: 1048576, MaxOutput: 131072}},
	{"*mimo*omni*", caps{Vision: true, AudioInput: true, ContextWindow: 262144, MaxOutput: 131072}},
	{"*mimo*", caps{Vision: true, ContextWindow: 262144, MaxOutput: 131072}},

	// Llama — 4 is vision/1M; 3.x is text-only/128K.
	{"*llama-4*", caps{Vision: true, ContextWindow: 1000000}},
	{"*llama*", caps{ContextWindow: 128000}},

	// Mistral — Large 3 is vision/256K; codestral is text.
	{"*codestral*", caps{ContextWindow: 256000}},
	{"*mistral-large*", caps{Vision: true, ContextWindow: 256000}},
	{"*mistral*", caps{ContextWindow: 128000}},

	// Cohere — Command A Vision has vision; other Command models are text.
	{"*command-a-vision*", caps{Vision: true, ContextWindow: 128000}},
	{"*command*", caps{ContextWindow: 128000}},

	// Perplexity — native web search.
	{"*sonar*", caps{Search: true, ContextWindow: 128000}},
	{"*pplx*", caps{Search: true, ContextWindow: 128000}},
	{"*perplexity*", caps{Search: true, ContextWindow: 128000}},

	// Others.
	{"*hunyuan*", caps{Reasoning: true, ThinkingFormat: "hunyuan", ContextWindow: 262144, MaxOutput: 262144}},
	{"hy3*", caps{Reasoning: true, ThinkingFormat: "hunyuan", ContextWindow: 262144, MaxOutput: 262144}},
	{"*step-*", caps{Reasoning: true, ThinkingFormat: "step", ContextWindow: 128000}},
	{"*nemotron*", caps{Reasoning: true, ContextWindow: 128000}},
	{"*ling-*", caps{Reasoning: true, ContextWindow: 128000}},

	// Qoder tier model ids — opaque tier names matched exactly (no wildcard
	// anchors the whole id) unless the id is versioned. ContextWindow is set
	// explicitly so the long-context flag matches each tier.
	{"auto", caps{ContextWindow: 200000}},
	{"ultimate", caps{Vision: true, ContextWindow: 200000}},
	{"performance", caps{ContextWindow: 200000}},
	{"efficient", caps{ContextWindow: 200000}},
	{"lite", caps{ContextWindow: 128000}},
	{"*qmodel*", caps{ContextWindow: 200000}},
	{"*dmodel*", caps{Reasoning: true, ContextWindow: 128000}},
	{"*dfmodel*", caps{Reasoning: true, ContextWindow: 128000}},
	{"gm51model", caps{Vision: true, ContextWindow: 200000}},
	{"kmodel", caps{ContextWindow: 200000}},
	{"mmodel", caps{ContextWindow: 200000}},

	// OpenAI Responses API (codex). The gpt-5 codex variant is handled by the
	// OpenAI section above; this covers bare codex ids.
	{"*codex*", caps{Reasoning: true, ContextWindow: 128000}},

	// ByteDance / Volcengine (doubao, ep-* endpoint ids).
	{"*doubao*", caps{ContextWindow: 200000}},
	{"*bytedance*", caps{ContextWindow: 200000}},
	{"*ep-*", caps{ContextWindow: 200000}},

	// Other hosted families that keep the floor's tool support but a smaller
	// context window.
	{"*cerebras*", caps{ContextWindow: 128000}},
	{"*@cf/*", caps{ContextWindow: 128000}},
	{"*kiro*", caps{ContextWindow: 128000}},
	{"*codewhisperer*", caps{ContextWindow: 128000}},
	{"*mixtral*", caps{ContextWindow: 128000}},
	{"*phi-4*", caps{ContextWindow: 128000}},
	{"*phi-3*", caps{ContextWindow: 128000}},
}
