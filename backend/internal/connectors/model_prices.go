package connectors

import (
	"slices"
	"strings"
	"sync"
)

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
// controlled provider aliases and finally a canonical model shared across
// providers. Later table entries override earlier ones for the same key, so
// currentOfficialModelPrices wins over the broader catalog.
func ModelPriceByProviderModel(provider, model string) (ModelPrice, bool) {
	idx := modelPriceIndex()
	providers := []string{normalizePriceProvider(provider)}
	if p := strings.ToLower(strings.TrimSpace(provider)); p != providers[0] {
		providers = append(providers, p)
	}
	models := priceModelCandidates(model)
	best := -1
	for _, p := range providers {
		for _, m := range models {
			if i, ok := idx.exact[p+"/"+strings.ToLower(m)]; ok && i > best {
				best = i
			}
		}
	}
	if best >= 0 {
		return idx.table[best], true
	}

	// Custom OpenAI-compatible endpoints often expose a vendor model without a
	// billable-provider id. Use a cross-provider match when every canonical
	// match has the same rates, or when the most authoritative source tier
	// agrees; remaining ambiguity stays visibly unpriced.
	var matches []int
	seen := map[int]struct{}{}
	for _, m := range models {
		for _, i := range idx.fingerprints[priceModelFingerprint(m)] {
			if _, dup := seen[i]; !dup {
				seen[i] = struct{}{}
				matches = append(matches, i)
			}
		}
	}
	if len(matches) == 0 {
		return ModelPrice{}, false
	}
	if !allSameModelRates(idx.table, matches) {
		top := priceSourcePriority(idx.table[matches[0]].Source)
		for _, i := range matches[1:] {
			top = min(top, priceSourcePriority(idx.table[i].Source))
		}
		preferred := matches[:0:0]
		for _, i := range matches {
			if priceSourcePriority(idx.table[i].Source) == top {
				preferred = append(preferred, i)
			}
		}
		if !allSameModelRates(idx.table, preferred) {
			return ModelPrice{}, false
		}
		matches = preferred
	}
	return idx.table[slices.Max(matches)], true
}

// priceIndex is the immutable lookup structure over ModelPricingTable.
type priceIndex struct {
	table []ModelPrice
	// exact maps "provider/model" (normalized provider, lowercased model) to
	// the last table position holding that key.
	exact map[string]int
	// fingerprints maps a canonical model fingerprint to the deduplicated
	// table positions sharing it.
	fingerprints map[string][]int
}

var modelPriceIndex = sync.OnceValue(func() *priceIndex {
	table := buildModelPricingTable()
	idx := &priceIndex{
		table:        table,
		exact:        make(map[string]int, len(table)),
		fingerprints: make(map[string][]int, len(table)),
	}
	for i, mp := range table {
		idx.exact[normalizePriceProvider(mp.Provider)+"/"+strings.ToLower(mp.Model)] = i
	}
	for i, mp := range table {
		if idx.exact[normalizePriceProvider(mp.Provider)+"/"+strings.ToLower(mp.Model)] != i {
			continue
		}
		fp := priceModelFingerprint(mp.Model)
		idx.fingerprints[fp] = append(idx.fingerprints[fp], i)
	}
	return idx
})

func allSameModelRates(table []ModelPrice, positions []int) bool {
	for _, i := range positions[1:] {
		if !sameModelRates(table[positions[0]], table[i]) {
			return false
		}
	}
	return true
}

// priceSourcePriority ranks catalog provenance; lower is more authoritative.
func priceSourcePriority(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "official":
		return 0
	case "", "catalog":
		return 1
	case "custom":
		return 2
	case "retail_equivalent":
		return 3
	default:
		return 4
	}
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
	return priceFingerprintReplacer.Replace(m)
}

var priceFingerprintReplacer = strings.NewReplacer("-", "", "_", "", ".", "", " ", "")

func sameModelRates(a, b ModelPrice) bool {
	return a.InputPerM == b.InputPerM && a.OutputPerM == b.OutputPerM &&
		a.CachedInputPerM == b.CachedInputPerM && a.CacheWritePerM == b.CacheWritePerM &&
		a.ReasoningPerM == b.ReasoningPerM && a.CacheWrite1hPerM == b.CacheWrite1hPerM &&
		a.WebSearchPerK == b.WebSearchPerK && a.LongContextThreshold == b.LongContextThreshold &&
		a.LongInputPerM == b.LongInputPerM && a.LongOutputPerM == b.LongOutputPerM &&
		a.LongCachedInputPerM == b.LongCachedInputPerM && a.LongCacheWritePerM == b.LongCacheWritePerM &&
		a.ExplicitFree == b.ExplicitFree
}

// ModelPricingTable returns a copy of the built-in catalog. Later entries
// override earlier ones when provider/model keys collide.
func ModelPricingTable() []ModelPrice {
	return slices.Clone(modelPriceIndex().table)
}

// buildModelPricingTable assembles the catalog in override order: retired
// compatibility entries first, then the per-provider list-price catalog, then
// subscription estimates and curated overrides.
func buildModelPricingTable() []ModelPrice {
	var out []ModelPrice
	out = append(out, anthropicModelPrices()...)
	out = append(out, deepseekModelPrices()...)
	out = append(out, geminiModelPrices()...)
	out = append(out, groqModelPrices()...)
	out = append(out, xaiModelPrices()...)
	out = append(out, cohereModelPrices()...)
	out = append(out, togetherModelPrices()...)
	out = append(out, nebiusModelPrices()...)
	out = append(out, nvidiaModelPrices()...)
	out = append(out, minimaxModelPrices()...)
	out = append(out, glmModelPrices()...)
	out = append(out, catalogModelPrices()...)
	out = append(out, openrouterModelPrices()...)
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
// expanded into all four variants at the same rate. GPT 5.6 variants are
// derived from the OpenAI list price in currentOfficialModelPrices.
func kiroModelPrices() []ModelPrice {
	// base holds one logical model and its rate; variants are generated below.
	type base struct {
		model           string
		inputPerM       float64
		outputPerM      float64
		cachedInputPerM float64
		cacheWritePerM  float64
	}
	// Anthropic list prices: cache read is 0.1x input, a 5-minute cache write
	// is 1.25x input.
	const (
		sonnet5In, sonnet5Out, sonnet5Cached, sonnet5Write = 2.0, 10.0, 0.2, 2.5
		sonnetIn, sonnetOut, sonnetCached, sonnetWrite     = 3.0, 15.0, 0.3, 3.75
		opusIn, opusOut, opusCached, opusWrite             = 5.0, 25.0, 0.5, 6.25
	)
	bases := []base{
		// Claude Sonnet family.
		{"claude-sonnet-5", sonnet5In, sonnet5Out, sonnet5Cached, sonnet5Write},
		{"claude-sonnet-4.5", sonnetIn, sonnetOut, sonnetCached, sonnetWrite},
		{"claude-sonnet-4.6", sonnetIn, sonnetOut, sonnetCached, sonnetWrite},
		{"claude-sonnet-4.7", sonnetIn, sonnetOut, sonnetCached, sonnetWrite},
		{"claude-sonnet-4.8", sonnetIn, sonnetOut, sonnetCached, sonnetWrite},
		// Claude Opus family (4.5+ pricing).
		{"claude-opus-4.5", opusIn, opusOut, opusCached, opusWrite},
		{"claude-opus-4.6", opusIn, opusOut, opusCached, opusWrite},
		{"claude-opus-4.7", opusIn, opusOut, opusCached, opusWrite},
		{"claude-opus-4.8", opusIn, opusOut, opusCached, opusWrite},
		// Claude Haiku family.
		{"claude-haiku-4.5", 1.0, 5.0, 0.1, 1.25},
		// Non-Claude models exposed by Kiro (estimated from the underlying
		// vendor's list price).
		{"deepseek-3.2", 0.28, 0.42, 0.028, 0},
		{"glm-5", 1.0, 3.2, 0.2, 0},
		{"MiniMax-M2.5", 0.3, 1.2, 0.03, 0.375},
		{"qwen3-coder-next", 0.2, 1.5, 0, 0},
	}
	// Suffix variants share the base rate.
	suffixes := []string{"", "-thinking", "-agentic", "-thinking-agentic"}

	var out []ModelPrice
	for _, b := range bases {
		for _, sfx := range suffixes {
			out = append(out, ModelPrice{
				Provider: "kiro", Model: b.model + sfx,
				InputPerM: b.inputPerM, OutputPerM: b.outputPerM,
				CachedInputPerM: b.cachedInputPerM, CacheWritePerM: b.cacheWritePerM,
			})
		}
	}
	// "auto" only exposes base + -thinking variants in the catalog; it maps to
	// Sonnet 4.6 as a neutral default since Kiro picks the model server-side.
	out = append(out,
		ModelPrice{Provider: "kiro", Model: "auto", InputPerM: sonnetIn, OutputPerM: sonnetOut, CachedInputPerM: sonnetCached, CacheWritePerM: sonnetWrite},
		ModelPrice{Provider: "kiro", Model: "auto-thinking", InputPerM: sonnetIn, OutputPerM: sonnetOut, CachedInputPerM: sonnetCached, CacheWritePerM: sonnetWrite},
	)
	return out
}

// The tables below keep retired or aliased models that the current catalog no
// longer lists, so historical usage remains priced.

func anthropicModelPrices() []ModelPrice {
	// Anthropic: cache read 0.1x input, 5-minute write 1.25x, 1-hour write 2x.
	claude := func(model string, in, out float64) ModelPrice {
		return ModelPrice{Provider: "anthropic", Model: model, InputPerM: in, OutputPerM: out,
			CachedInputPerM: in * 0.1, CacheWritePerM: in * 1.25, CacheWrite1hPerM: in * 2}
	}
	return []ModelPrice{
		claude("claude-opus-4-1-20250805", 15, 75),
		claude("claude-opus-4-1", 15, 75),
		claude("claude-opus-4-20250514", 15, 75),
		claude("claude-sonnet-4-20250514", 3, 15),
		claude("claude-3-7-sonnet-20250219", 3, 15),
		claude("claude-3-5-sonnet-20241022", 3, 15),
		claude("claude-3-5-sonnet-latest", 3, 15),
		claude("claude-3-5-haiku-20241022", 0.8, 4),
		claude("claude-3-opus-20240229", 15, 75),
		claude("claude-3-sonnet-20240229", 3, 15),
		claude("claude-3-haiku-20240307", 0.25, 1.25),
	}
}

func deepseekModelPrices() []ModelPrice {
	return []ModelPrice{
		// Aliases of deepseek-chat / deepseek-reasoner.
		{Provider: "deepseek", Model: "deepseek-v3.2-chat", InputPerM: 0.28, OutputPerM: 0.42, CachedInputPerM: 0.028},
		{Provider: "deepseek", Model: "deepseek-v3.2-reasoner", InputPerM: 0.28, OutputPerM: 0.42, CachedInputPerM: 0.028},
		// Effort variant of deepseek-v4-pro ("-none" resolves via suffix stripping).
		{Provider: "deepseek", Model: "deepseek-v4-pro-max", InputPerM: 1.32, OutputPerM: 3.96, CachedInputPerM: 0.044},
	}
}

func geminiModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "gemini", Model: "gemini-2.0-flash", InputPerM: 0.1, OutputPerM: 0.4, CachedInputPerM: 0.025, CacheWritePerM: 0.1},
		{Provider: "gemini", Model: "gemini-2.0-flash-lite", InputPerM: 0.075, OutputPerM: 0.3, CachedInputPerM: 0.01875, CacheWritePerM: 0.075},
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

func xaiModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "xai", Model: "grok-3", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.75, CacheWritePerM: 3},
		{Provider: "xai", Model: "grok-3-fast", InputPerM: 5, OutputPerM: 25, CachedInputPerM: 1.25, CacheWritePerM: 5},
		{Provider: "xai", Model: "grok-3-mini", InputPerM: 0.3, OutputPerM: 0.5, CachedInputPerM: 0.075, CacheWritePerM: 0.3, ReasoningPerM: 0.5},
		{Provider: "xai", Model: "grok-2", InputPerM: 2, OutputPerM: 10, CachedInputPerM: 0.5, CacheWritePerM: 2},
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
	}
}

func nebiusModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "nebius", Model: "Meta-Llama-3.1-405B-Instruct", InputPerM: 1, OutputPerM: 1},
		{Provider: "nebius", Model: "Meta-Llama-3.1-70B-Instruct", InputPerM: 0.13, OutputPerM: 0.13},
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
		estimated("anthropic/claude-fable-5.1", 10, 50, 0.25, 12.5),
		estimated("anthropic/claude-opus-5.5", 4, 20, 0.2, 5),
		estimated("anthropic/claude-sonnet-5.5", 2, 10, 0.1, 2.5),
		estimated("anthropic/claude-haiku-5.5", 0.1, 0.5, 0.01, 0.125),
		estimated("anthropic/claude-opus-4.8", 5, 25, 0.5, 6.25),
		estimated("anthropic/claude-opus-4-7", 5, 25, 0.5, 6.25),
		estimated("anthropic/claude-sonnet-4-6", 3, 15, 0.3, 3.75),
		estimated("openai/gpt-6-sol", 2, 10, 0.2, 2.5),
		estimated("openai/gpt-5.5", 5, 30, 0.5, 0),
		estimated("openai/gpt-5", 1.25, 10, 0.125, 0),
		estimated("openai/gpt-4o", 2.5, 10, 1.25, 0),
		estimated("openai/gpt-4o-mini", 0.15, 0.6, 0.075, 0),
		estimated("deepseek/deepseek-chat", 0.28, 0.42, 0.028, 0),
		estimated("google/gemini-3.1-pro-preview", 2, 12, 0.2, 0),
		estimated("google/gemini-2.5-pro", 1.25, 10, 0.125, 0),
		estimated("google/gemini-2.5-flash", 0.3, 2.5, 0.03, 0),
		estimated("x-ai/grok-4.5", 2, 6, 0.3, 0),
		estimated("meta-llama/llama-3.3-70b-instruct", 0.1, 0.1, 0, 0),
	}
}

func minimaxModelPrices() []ModelPrice {
	return []ModelPrice{
		{Provider: "minimax", Model: "MiniMax-Text-01", InputPerM: 0.2, OutputPerM: 1.1},
		{Provider: "minimax", Model: "MiniMax-M1", InputPerM: 0.2, OutputPerM: 1.1, ReasoningPerM: 1.1},
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
	// Token Plan rates are admitted retail-equivalent estimates, not
	// authoritative token invoices. Keep that provenance explicit so they never
	// appear as exact catalog spend.
	estimated := func(provider, model string, input, output, cached float64) ModelPrice {
		return ModelPrice{
			Provider: provider, Model: model, InputPerM: input, OutputPerM: output, CachedInputPerM: cached,
			Source: "retail_equivalent", Estimated: true,
		}
	}
	var out []ModelPrice
	for _, provider := range []string{"xiaomi-mimo", "xiaomi-tokenplan"} {
		out = append(out,
			estimated(provider, "mimo-v2.5-pro", 1.0, 3.0, 0),
			estimated(provider, "mimo-v2.5", 0.2, 0.6, 0),
			estimated(provider, "mimo-v2-pro", 0.5, 1.5, 0),
			estimated(provider, "mimo-v2-omni", 0.2, 0.6, 0),
			estimated(provider, "mimo-v2-flash", 0.1, 0.3, 0),
		)
	}
	// MiMo V2.6 list prices apply to the Token Plan as estimates; the direct
	// API rates live in the catalog.
	out = append(out,
		estimated("xiaomi-tokenplan", "mimo-v2.6-pro", 0.435, 0.87, 0.0036),
		estimated("xiaomi-tokenplan", "mimo-v2.6-flash", 0.14, 0.28, 0.0028),
	)
	// This connector is intentionally a zero-charge tier, not an unknown price.
	out = append(out, ModelPrice{
		Provider: "mimo-free", Model: "mimo-auto", Source: "catalog", ExplicitFree: true,
	})
	return out
}
