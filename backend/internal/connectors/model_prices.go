package connectors

import "strings"

// ModelPrice holds immutable catalog metadata and per-million-token rates for a
// specific model. Source metadata lets analytics distinguish actual list price,
// a user-configured price, and a retail-equivalent estimate.
type ModelPrice struct {
	Provider             string
	Model                string
	InputPerM            float64
	OutputPerM           float64
	CachedInputPerM      float64
	CacheWritePerM       float64
	ReasoningPerM        float64
	CacheWrite1hPerM     float64
	WebSearchPerK        float64
	LongContextThreshold int
	LongInputPerM        float64
	LongOutputPerM       float64
	LongCachedInputPerM  float64
	LongCacheWritePerM   float64
	Source               string
	SourceURL            string
	Estimated            bool
	ExplicitFree         bool
}

// ModelPriceByProviderModel resolves exact provider/model entries first, then
// controlled provider aliases and finally a globally unique canonical model.
// Iterating in reverse lets currentOfficialModelPrices override stale entries.
func ModelPriceByProviderModel(provider, model string) (ModelPrice, bool) {
	table := ModelPricingTable()
	providers := []string{normalizePriceProvider(provider)}
	if p := strings.ToLower(strings.TrimSpace(provider)); p != providers[0] {
		providers = append(providers, p)
	}
	models := priceModelCandidates(model)
	for i := len(table) - 1; i >= 0; i-- {
		mp := table[i]
		for _, p := range providers {
			for _, m := range models {
				if normalizePriceProvider(mp.Provider) == p && strings.EqualFold(mp.Model, m) {
					return mp, true
				}
			}
		}
	}

	// Custom OpenAI-compatible endpoints often expose a vendor model without a
	// billable-provider id. Use a cross-provider match only when every canonical
	// match has the same rates; ambiguity remains visibly unpriced.
	fingerprints := make(map[string]struct{}, len(models))
	for _, m := range models {
		fingerprints[priceModelFingerprint(m)] = struct{}{}
	}
	seenKeys := map[string]struct{}{}
	var found ModelPrice
	hasFound := false
	for i := len(table) - 1; i >= 0; i-- {
		mp := table[i]
		key := normalizePriceProvider(mp.Provider) + "/" + strings.ToLower(mp.Model)
		if _, seen := seenKeys[key]; seen {
			continue
		}
		seenKeys[key] = struct{}{}
		if _, ok := fingerprints[priceModelFingerprint(mp.Model)]; !ok {
			continue
		}
		if !hasFound {
			found, hasFound = mp, true
			continue
		}
		if !sameModelRates(found, mp) {
			return ModelPrice{}, false
		}
	}
	return found, hasFound
}

func normalizePriceProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "codex", "openai-codex":
		return "openai"
	case "claude", "claude-code":
		return "anthropic"
	case "gemini-cli", "antigravity":
		return "gemini"
	case "cloudflare", "workers-ai":
		return "cloudflare-ai"
	default:
		return p
	}
}

func priceModelCandidates(model string) []string {
	m := strings.TrimSpace(model)
	out := []string{m}
	for strings.Contains(m, "/") {
		m = strings.TrimPrefix(m[strings.IndexByte(m, '/')+1:], "/")
		out = append(out, m)
	}
	for _, suffix := range []string{"-xhigh-review", "-high-review", "-low-review", "-none-review", "-review", "-xhigh", "-high", "-low", "-none"} {
		if strings.HasSuffix(strings.ToLower(m), suffix) {
			out = append(out, m[:len(m)-len(suffix)])
			break
		}
	}
	// Fine-tunes and dated snapshots price like their base model.
	for _, candidate := range out {
		base := candidate
		if strings.HasPrefix(base, "ft:") {
			rest := strings.TrimPrefix(base, "ft:")
			if i := strings.IndexByte(rest, ':'); i >= 0 {
				rest = rest[:i]
			}
			base = rest
		}
		if n := len(base); n > 11 && base[n-11] == '-' && looksLikeDate(base[n-10:]) {
			base = base[:n-11]
		}
		if base != candidate {
			out = append(out, base)
		}
	}
	return out
}

func looksLikeDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 4 || i == 7 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func priceModelFingerprint(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	replacer := strings.NewReplacer("-", "", "_", "", ".", "", " ", "")
	return replacer.Replace(m)
}

func sameModelRates(a, b ModelPrice) bool {
	return a.InputPerM == b.InputPerM && a.OutputPerM == b.OutputPerM &&
		a.CachedInputPerM == b.CachedInputPerM && a.CacheWritePerM == b.CacheWritePerM &&
		a.ReasoningPerM == b.ReasoningPerM && a.LongInputPerM == b.LongInputPerM &&
		a.LongOutputPerM == b.LongOutputPerM
}

// ModelPricingTable returns the built-in catalog. Later entries override older
// compatibility entries when provider/model keys collide.
func ModelPricingTable() []ModelPrice {
	var out []ModelPrice
	out = append(out, openaiModelPrices()...)
	out = append(out, anthropicModelPrices()...)
	out = append(out, deepseekModelPrices()...)
	out = append(out, geminiModelPrices()...)
	out = append(out, groqModelPrices()...)
	out = append(out, mistralModelPrices()...)
	out = append(out, xaiModelPrices()...)
	out = append(out, perplexityModelPrices()...)
	out = append(out, cohereModelPrices()...)
	out = append(out, togetherModelPrices()...)
	out = append(out, fireworksModelPrices()...)
	out = append(out, cerebrasModelPrices()...)
	out = append(out, nebiusModelPrices()...)
	out = append(out, nvidiaModelPrices()...)
	out = append(out, openrouterModelPrices()...)
	out = append(out, minimaxModelPrices()...)
	out = append(out, glmModelPrices()...)
	out = append(out, mimoModelPrices()...)
	out = append(out, kiroModelPrices()...)
	out = append(out, currentOfficialModelPrices()...)
	return out
}

// kiroModelPrices returns estimated per-million-token rates for the Kiro
// provider. Kiro itself is subscription/credit-based rather than per-token, so
// these are retail-equivalent estimates of the underlying model (e.g. Claude
// Sonnet/Opus), surfaced so usage statistics can display an approximate cost.
//
// The recorded model ID is the client-facing string with synthetic suffixes
// intact (-thinking, -agentic, -thinking-agentic), so each base model is
// expanded into all four variants at the same rate.
func kiroModelPrices() []ModelPrice {
	// base holds one logical model and its rate; variants are generated below.
	type base struct {
		model           string
		inputPerM       float64
		outputPerM      float64
		cachedInputPerM float64
		cacheWritePerM  float64
		reasoningPerM   float64
	}
	const (
		sonnetIn, sonnetOut, sonnetCached, sonnetWrite = 3.0, 15.0, 0.375, 3.75
		opusIn, opusOut, opusCached, opusWrite         = 15.0, 75.0, 1.875, 18.75
	)
	bases := []base{
		// Claude Sonnet family (current + announced versions).
		{"claude-sonnet-5", sonnetIn, sonnetOut, sonnetCached, sonnetWrite, 0},
		{"claude-sonnet-4.5", sonnetIn, sonnetOut, sonnetCached, sonnetWrite, 0},
		{"claude-sonnet-4.6", sonnetIn, sonnetOut, sonnetCached, sonnetWrite, 0},
		{"claude-sonnet-4.7", sonnetIn, sonnetOut, sonnetCached, sonnetWrite, 0},
		{"claude-sonnet-4.8", sonnetIn, sonnetOut, sonnetCached, sonnetWrite, 0},
		// Claude Opus family.
		{"claude-opus-4.5", opusIn, opusOut, opusCached, opusWrite, 0},
		{"claude-opus-4.6", opusIn, opusOut, opusCached, opusWrite, 0},
		{"claude-opus-4.7", opusIn, opusOut, opusCached, opusWrite, 0},
		{"claude-opus-4.8", opusIn, opusOut, opusCached, opusWrite, 0},
		// Claude Haiku family.
		{"claude-haiku-4.5", 0.8, 4.0, 0.08, 1.0, 0},
		// GPT 5.6 family (Sol/Terra/Luna). Subscription-priced upstream; these
		// are retail-equivalent estimates scaled off the GPT-5 rate (2.5 in /
		// 10 out) by each tier's relative cost, with the standard 50% cache-read
		// discount and cache-write charged at the standard input rate.
		{"gpt-5.6-sol", 6.0, 24.0, 3.0, 6.0, 0},
		{"gpt-5.6-terra", 3.0, 12.0, 1.5, 3.0, 0},
		{"gpt-5.6-luna", 1.5, 6.0, 0.75, 1.5, 0},
		// Non-Claude models exposed by Kiro (estimated from underlying provider).
		{"deepseek-3.2", 0.27, 1.1, 0.07, 0.27, 0},
		{"glm-5", 0.6, 2.2, 0, 0, 0},
		{"MiniMax-M2.5", 0.3, 1.1, 0, 0, 0},
		{"qwen3-coder-next", 0.3, 1.2, 0, 0, 0},
	}
	// Suffix variants share the base rate. "auto" maps to Sonnet 4.5 as a
	// neutral default since Kiro picks the model server-side.
	suffixes := []string{"", "-thinking", "-agentic", "-thinking-agentic"}

	var out []ModelPrice
	add := func(model string, in, outp, cached, write, reason float64) {
		for _, sfx := range suffixes {
			out = append(out, ModelPrice{
				Provider: "kiro", Model: model + sfx,
				InputPerM: in, OutputPerM: outp,
				CachedInputPerM: cached, CacheWritePerM: write, ReasoningPerM: reason,
			})
		}
	}
	for _, b := range bases {
		add(b.model, b.inputPerM, b.outputPerM, b.cachedInputPerM, b.cacheWritePerM, b.reasoningPerM)
	}
	// "auto" only exposes base + -thinking variants in the catalog.
	out = append(out,
		ModelPrice{Provider: "kiro", Model: "auto", InputPerM: sonnetIn, OutputPerM: sonnetOut, CachedInputPerM: sonnetCached, CacheWritePerM: sonnetWrite},
		ModelPrice{Provider: "kiro", Model: "auto-thinking", InputPerM: sonnetIn, OutputPerM: sonnetOut, CachedInputPerM: sonnetCached, CacheWritePerM: sonnetWrite},
	)
	return out
}

func openaiModelPrices() []ModelPrice {
	return []ModelPrice{
		// GPT-5 family — cache write = standard input (OpenAI doesn't charge extra)
		{Provider: "openai", Model: "gpt-5", InputPerM: 2.5, OutputPerM: 10, CachedInputPerM: 1.25, CacheWritePerM: 2.5},
		{Provider: "openai", Model: "gpt-5-mini", InputPerM: 0.4, OutputPerM: 1.6, CachedInputPerM: 0.2, CacheWritePerM: 0.4},
		{Provider: "openai", Model: "gpt-5-nano", InputPerM: 0.1, OutputPerM: 0.4, CachedInputPerM: 0.05, CacheWritePerM: 0.1},
		{Provider: "openai", Model: "gpt-5.4", InputPerM: 2.5, OutputPerM: 10, CachedInputPerM: 1.25, CacheWritePerM: 2.5},
		{Provider: "openai", Model: "gpt-5.4-mini", InputPerM: 0.4, OutputPerM: 1.6, CachedInputPerM: 0.2, CacheWritePerM: 0.4},
		{Provider: "openai", Model: "gpt-5.3-codex", InputPerM: 2.5, OutputPerM: 10, CachedInputPerM: 1.25, CacheWritePerM: 2.5},
		// GPT-4o family
		{Provider: "openai", Model: "gpt-4o", InputPerM: 2.5, OutputPerM: 10, CachedInputPerM: 1.25, CacheWritePerM: 2.5},
		{Provider: "openai", Model: "gpt-4o-2024-11-20", InputPerM: 2.5, OutputPerM: 10, CachedInputPerM: 1.25, CacheWritePerM: 2.5},
		{Provider: "openai", Model: "gpt-4o-2024-08-06", InputPerM: 2.5, OutputPerM: 10, CachedInputPerM: 1.25, CacheWritePerM: 2.5},
		{Provider: "openai", Model: "gpt-4o-mini", InputPerM: 0.15, OutputPerM: 0.6, CachedInputPerM: 0.075, CacheWritePerM: 0.15},
		{Provider: "openai", Model: "gpt-4o-mini-2024-07-18", InputPerM: 0.15, OutputPerM: 0.6, CachedInputPerM: 0.075, CacheWritePerM: 0.15},
		// o-series (reasoning) — cache write = standard input
		{Provider: "openai", Model: "o1", InputPerM: 15, OutputPerM: 60, CachedInputPerM: 7.5, CacheWritePerM: 15, ReasoningPerM: 60},
		{Provider: "openai", Model: "o1-pro", InputPerM: 150, OutputPerM: 600, CachedInputPerM: 75, CacheWritePerM: 150, ReasoningPerM: 600},
		{Provider: "openai", Model: "o3", InputPerM: 2, OutputPerM: 8, CachedInputPerM: 0.5, CacheWritePerM: 2, ReasoningPerM: 8},
		{Provider: "openai", Model: "o3-mini", InputPerM: 1.1, OutputPerM: 4.4, CachedInputPerM: 0.55, CacheWritePerM: 1.1, ReasoningPerM: 4.4},
		{Provider: "openai", Model: "o4-mini", InputPerM: 1.1, OutputPerM: 4.4, CachedInputPerM: 0.275, CacheWritePerM: 1.1, ReasoningPerM: 4.4},
		// Older models (no prompt caching)
		{Provider: "openai", Model: "gpt-4-turbo", InputPerM: 10, OutputPerM: 30},
		{Provider: "openai", Model: "gpt-4", InputPerM: 30, OutputPerM: 60},
		{Provider: "openai", Model: "gpt-3.5-turbo", InputPerM: 0.5, OutputPerM: 1.5},
		// Embeddings
		{Provider: "openai", Model: "text-embedding-3-small", InputPerM: 0.02, OutputPerM: 0},
		{Provider: "openai", Model: "text-embedding-3-large", InputPerM: 0.13, OutputPerM: 0},
		{Provider: "openai", Model: "text-embedding-ada-002", InputPerM: 0.1, OutputPerM: 0},
	}
}

func anthropicModelPrices() []ModelPrice {
	return []ModelPrice{
		// Claude 4 family — cache write = 1.25x standard input
		{Provider: "anthropic", Model: "claude-opus-4-20250514", InputPerM: 15, OutputPerM: 75, CachedInputPerM: 1.875, CacheWritePerM: 18.75},
		{Provider: "anthropic", Model: "claude-opus-4-7", InputPerM: 15, OutputPerM: 75, CachedInputPerM: 1.875, CacheWritePerM: 18.75},
		{Provider: "anthropic", Model: "claude-sonnet-4-20250514", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.375, CacheWritePerM: 3.75},
		{Provider: "anthropic", Model: "claude-sonnet-4-6", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.375, CacheWritePerM: 3.75},
		{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", InputPerM: 0.8, OutputPerM: 4, CachedInputPerM: 0.08, CacheWritePerM: 1.0},
		// Claude 3.5 family
		{Provider: "anthropic", Model: "claude-3-5-sonnet-20241022", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.375, CacheWritePerM: 3.75},
		{Provider: "anthropic", Model: "claude-3-5-sonnet-latest", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.375, CacheWritePerM: 3.75},
		{Provider: "anthropic", Model: "claude-3-5-haiku-20241022", InputPerM: 0.8, OutputPerM: 4, CachedInputPerM: 0.08, CacheWritePerM: 1.0},
		// Claude 3 family
		{Provider: "anthropic", Model: "claude-3-opus-20240229", InputPerM: 15, OutputPerM: 75, CachedInputPerM: 1.875, CacheWritePerM: 18.75},
		{Provider: "anthropic", Model: "claude-3-sonnet-20240229", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.375, CacheWritePerM: 3.75},
		{Provider: "anthropic", Model: "claude-3-haiku-20240307", InputPerM: 0.25, OutputPerM: 1.25, CachedInputPerM: 0.03, CacheWritePerM: 0.3125},
	}
}

func deepseekModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "deepseek", Model: "deepseek-chat", InputPerM: 0.14, OutputPerM: 0.28, CachedInputPerM: 0.0028, CacheWritePerM: 0.14, ReasoningPerM: 0.28},
		{Provider: "deepseek", Model: "deepseek-reasoner", InputPerM: 0.14, OutputPerM: 0.28, CachedInputPerM: 0.0028, CacheWritePerM: 0.14, ReasoningPerM: 0.28},
		{Provider: "deepseek", Model: "deepseek-r1", InputPerM: 0.14, OutputPerM: 0.28, CachedInputPerM: 0.0028, CacheWritePerM: 0.14, ReasoningPerM: 0.28},
		{Provider: "deepseek", Model: "deepseek-v3.2-chat", InputPerM: 0.14, OutputPerM: 0.28, CachedInputPerM: 0.0028, CacheWritePerM: 0.14, ReasoningPerM: 0.28},
		{Provider: "deepseek", Model: "deepseek-v3.2-reasoner", InputPerM: 0.14, OutputPerM: 0.28, CachedInputPerM: 0.0028, CacheWritePerM: 0.14, ReasoningPerM: 0.28},
		{Provider: "deepseek", Model: "deepseek-v4-flash", InputPerM: 0.14, OutputPerM: 0.28, CachedInputPerM: 0.0028, CacheWritePerM: 0.14, ReasoningPerM: 0.28},
		{Provider: "deepseek", Model: "deepseek-v4-pro", InputPerM: 0.435, OutputPerM: 0.87, CachedInputPerM: 0.003625, CacheWritePerM: 0.435, ReasoningPerM: 0.87},
		{Provider: "deepseek", Model: "deepseek-v4-pro-max", InputPerM: 0.435, OutputPerM: 0.87, CachedInputPerM: 0.003625, CacheWritePerM: 0.435, ReasoningPerM: 0.87},
		{Provider: "deepseek", Model: "deepseek-v4-pro-none", InputPerM: 0.435, OutputPerM: 0.87, CachedInputPerM: 0.003625, CacheWritePerM: 0.435, ReasoningPerM: 0.87},
	}
}

func geminiModelPrices() []ModelPrice {
	return []ModelPrice{
		// Gemini 2.5 — cache write = standard input
		{Provider: "gemini", Model: "gemini-2.5-pro", InputPerM: 1.25, OutputPerM: 10, CachedInputPerM: 0.3125, CacheWritePerM: 1.25},
		{Provider: "gemini", Model: "gemini-2.5-flash", InputPerM: 0.15, OutputPerM: 0.6, CachedInputPerM: 0.0375, CacheWritePerM: 0.15},
		{Provider: "gemini", Model: "gemini-2.5-flash-lite", InputPerM: 0.075, OutputPerM: 0.3, CachedInputPerM: 0.01875, CacheWritePerM: 0.075},
		// Gemini 2.0
		{Provider: "gemini", Model: "gemini-2.0-flash", InputPerM: 0.1, OutputPerM: 0.4, CachedInputPerM: 0.025, CacheWritePerM: 0.1},
		{Provider: "gemini", Model: "gemini-2.0-flash-lite", InputPerM: 0.075, OutputPerM: 0.3, CachedInputPerM: 0.01875, CacheWritePerM: 0.075},
		// Gemini 1.5
		{Provider: "gemini", Model: "gemini-1.5-pro", InputPerM: 1.25, OutputPerM: 5, CachedInputPerM: 0.3125, CacheWritePerM: 1.25},
		{Provider: "gemini", Model: "gemini-1.5-flash", InputPerM: 0.075, OutputPerM: 0.3, CachedInputPerM: 0.01875, CacheWritePerM: 0.075},
	}
}

func groqModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "groq", Model: "llama-3.3-70b-versatile", InputPerM: 0.59, OutputPerM: 0.79},
		{Provider: "groq", Model: "llama-3.1-8b-instant", InputPerM: 0.05, OutputPerM: 0.08},
		{Provider: "groq", Model: "mixtral-8x7b-32768", InputPerM: 0.24, OutputPerM: 0.24},
		{Provider: "groq", Model: "gemma2-9b-it", InputPerM: 0.2, OutputPerM: 0.2},
		{Provider: "groq", Model: "whisper-large-v3", InputPerM: 0.0, OutputPerM: 0.0},
		{Provider: "groq", Model: "whisper-large-v3-turbo", InputPerM: 0.0, OutputPerM: 0.0},
	}
}

func mistralModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "mistral", Model: "mistral-large-latest", InputPerM: 2, OutputPerM: 6},
		{Provider: "mistral", Model: "mistral-small-latest", InputPerM: 0.1, OutputPerM: 0.3},
		{Provider: "mistral", Model: "codestral-latest", InputPerM: 0.3, OutputPerM: 0.9},
		{Provider: "mistral", Model: "pixtral-large-latest", InputPerM: 2, OutputPerM: 6},
	}
}

func xaiModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "xai", Model: "grok-3", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.75, CacheWritePerM: 3},
		{Provider: "xai", Model: "grok-3-fast", InputPerM: 5, OutputPerM: 25, CachedInputPerM: 1.25, CacheWritePerM: 5},
		{Provider: "xai", Model: "grok-3-mini", InputPerM: 0.3, OutputPerM: 0.5, CachedInputPerM: 0.075, CacheWritePerM: 0.3, ReasoningPerM: 0.5},
		{Provider: "xai", Model: "grok-2", InputPerM: 2, OutputPerM: 10, CachedInputPerM: 0.5, CacheWritePerM: 2},
	}
}

func perplexityModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "perplexity", Model: "sonar-pro", InputPerM: 3, OutputPerM: 15},
		{Provider: "perplexity", Model: "sonar", InputPerM: 1, OutputPerM: 1},
		{Provider: "perplexity", Model: "sonar-reasoning-pro", InputPerM: 2, OutputPerM: 8},
		{Provider: "perplexity", Model: "sonar-deep-research", InputPerM: 2, OutputPerM: 8},
	}
}

func cohereModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "cohere", Model: "command-r-plus", InputPerM: 2.5, OutputPerM: 10},
		{Provider: "cohere", Model: "command-r", InputPerM: 0.15, OutputPerM: 0.6},
	}
}

func togetherModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "together", Model: "meta-llama/Meta-Llama-3.1-405B-Instruct-Turbo", InputPerM: 3.5, OutputPerM: 3.5},
		{Provider: "together", Model: "meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo", InputPerM: 0.88, OutputPerM: 0.88},
		{Provider: "together", Model: "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo", InputPerM: 0.18, OutputPerM: 0.18},
		{Provider: "together", Model: "deepseek-ai/DeepSeek-V3", InputPerM: 1.25, OutputPerM: 1.25},
		{Provider: "together", Model: "Qwen/Qwen2.5-72B-Instruct-Turbo", InputPerM: 1.2, OutputPerM: 1.2},
	}
}

func fireworksModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "fireworks", Model: "accounts/fireworks/models/llama-v3p1-405b-instruct", InputPerM: 3, OutputPerM: 3},
		{Provider: "fireworks", Model: "accounts/fireworks/models/llama-v3p1-70b-instruct", InputPerM: 0.9, OutputPerM: 0.9},
		{Provider: "fireworks", Model: "accounts/fireworks/models/llama-v3p1-8b-instruct", InputPerM: 0.2, OutputPerM: 0.2},
		{Provider: "fireworks", Model: "accounts/fireworks/models/deepseek-v3", InputPerM: 0.9, OutputPerM: 0.9},
	}
}

func cerebrasModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "cerebras", Model: "llama3.1-8b", InputPerM: 0.1, OutputPerM: 0.1},
		{Provider: "cerebras", Model: "llama3.1-70b", InputPerM: 0.6, OutputPerM: 0.6},
		{Provider: "cerebras", Model: "llama-3.3-70b", InputPerM: 0.6, OutputPerM: 0.6},
	}
}

func nebiusModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "nebius", Model: "Meta-Llama-3.1-405B-Instruct", InputPerM: 1, OutputPerM: 1},
		{Provider: "nebius", Model: "Meta-Llama-3.1-70B-Instruct", InputPerM: 0.13, OutputPerM: 0.13},
		{Provider: "nebius", Model: "Qwen/Qwen2.5-72B-Instruct", InputPerM: 0.13, OutputPerM: 0.13},
	}
}

func nvidiaModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "nvidia", Model: "meta/llama-3.1-405b-instruct", InputPerM: 1, OutputPerM: 1},
		{Provider: "nvidia", Model: "meta/llama-3.1-70b-instruct", InputPerM: 0.13, OutputPerM: 0.13},
		{Provider: "nvidia", Model: "nvidia/llama-3.1-nemotron-70b-instruct", InputPerM: 0.13, OutputPerM: 0.13},
	}
}

func openrouterModelPrices() []ModelPrice {
	// OpenRouter routes can select different upstream variants and prices can
	// change independently. Keep these fallback catalog values explicitly
	// estimated; exact current spend should come from provider billing metadata.
	const sourceURL = "https://openrouter.ai/models"
	estimated := func(model string, input, output, cached, write float64) ModelPrice {
		return ModelPrice{
			Provider: "openrouter", Model: model,
			InputPerM: input, OutputPerM: output, CachedInputPerM: cached, CacheWritePerM: write,
			Source: "retail_equivalent", SourceURL: sourceURL, Estimated: true,
		}
	}
	return []ModelPrice{
		estimated("anthropic/claude-opus-4-7", 15, 75, 1.875, 18.75),
		estimated("anthropic/claude-sonnet-4-6", 3, 15, 0.375, 3.75),
		estimated("openai/gpt-5", 2.5, 10, 1.25, 2.5),
		estimated("openai/gpt-4o", 2.5, 10, 1.25, 2.5),
		estimated("openai/gpt-4o-mini", 0.15, 0.6, 0.075, 0.15),
		estimated("deepseek/deepseek-chat", 0.27, 1.1, 0.07, 0.27),
		estimated("google/gemini-2.5-pro", 1.25, 10, 0.3125, 1.25),
		estimated("google/gemini-2.5-flash", 0.15, 0.6, 0.0375, 0.15),
		estimated("meta-llama/llama-3.3-70b-instruct", 0.1, 0.1, 0, 0),
	}
}

func minimaxModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "minimax", Model: "MiniMax-Text-01", InputPerM: 0.2, OutputPerM: 1.1},
		{Provider: "minimax", Model: "MiniMax-M1", InputPerM: 0.2, OutputPerM: 1.1, ReasoningPerM: 1.1},
		{Provider: "minimax", Model: "MiniMax-M2.5", InputPerM: 0.3, OutputPerM: 1.1},
		{Provider: "minimax", Model: "MiniMax-M3", InputPerM: 0.4, OutputPerM: 1.6, ReasoningPerM: 1.6},
	}
}

func glmModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "glm", Model: "glm-4-plus", InputPerM: 0.6, OutputPerM: 0.6},
		{Provider: "glm", Model: "glm-4-flash", InputPerM: 0, OutputPerM: 0},
		{Provider: "glm", Model: "codegeex-4", InputPerM: 0.6, OutputPerM: 0.6},
	}
}

func mimoModelPrices() []ModelPrice {
	// MiMo standard/pro/omni values are admitted retail-equivalent estimates,
	// not authoritative token invoices. Keep that provenance explicit so they
	// never appear as exact catalog spend.
	estimated := func(provider, model string, input, output float64) ModelPrice {
		return ModelPrice{
			Provider: provider, Model: model, InputPerM: input, OutputPerM: output,
			Source: "retail_equivalent", Estimated: true,
		}
	}
	var out []ModelPrice
	for _, provider := range []string{"xiaomi-mimo", "xiaomi-tokenplan"} {
		out = append(out,
			estimated(provider, "mimo-v2.5-pro", 1.0, 3.0),
			estimated(provider, "mimo-v2.5", 0.2, 0.6),
			estimated(provider, "mimo-v2-pro", 0.5, 1.5),
			estimated(provider, "mimo-v2-omni", 0.2, 0.6),
			estimated(provider, "mimo-v2-flash", 0.1, 0.3),
		)
	}
	// This connector is intentionally a zero-charge tier, not an unknown price.
	out = append(out, ModelPrice{
		Provider: "mimo-free", Model: "mimo-auto", Source: "catalog", ExplicitFree: true,
	})
	return out
}
