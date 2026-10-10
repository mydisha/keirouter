package meter

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// PricingMatch is the auditable result of resolving a provider/model pair.
type PricingMatch struct {
	Price     Price
	Key       string
	Status    string // priced | estimated | free | missing | none
	Source    string
	MatchKind string // exact | provider_alias | canonical_model | provider | none
	SourceURL string
}

// CostBreakdown stores nanodollar components. One token at $0.0028/M costs
// 2.8 nanodollars, so nanos avoid the systematic all-zero rounding caused by
// storing only integer microdollars per request.
type CostBreakdown struct {
	Pricing PricingMatch

	InputCostNanos      int64
	CachedCostNanos     int64
	CacheWriteCostNanos int64
	OutputCostNanos     int64
	ReasoningCostNanos  int64
	// ToolCostNanos prices provider-side tool use (web search requests).
	ToolCostNanos    int64
	CostNanos        int64
	CostMicros       int64
	AvoidedCostNanos int64
	SavedCostNanos   int64

	InputRatePerM      float64
	CachedRatePerM     float64
	CacheWriteRatePerM float64
	OutputRatePerM     float64
	ReasoningRatePerM  float64
}

func normalizeProvider(provider string) string {
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

func modelCandidates(model string) []string {
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
	// Fine-tunes ("ft:gpt-4o-mini:org::id") and dated snapshots
	// ("gpt-4o-2024-08-06") are priced like their base model.
	for _, candidate := range out {
		if base := stripFineTuneAndDate(candidate); base != candidate {
			out = append(out, base)
		}
	}
	return out
}

// stripFineTuneAndDate reduces "ft:<base>:<org>:<suffix>:<id>" to <base> and
// drops a trailing "-YYYY-MM-DD" snapshot date.
func stripFineTuneAndDate(model string) string {
	m := model
	if strings.HasPrefix(m, "ft:") {
		rest := strings.TrimPrefix(m, "ft:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		m = rest
	}
	if n := len(m); n > 11 && m[n-11] == '-' && isDateSuffix(m[n-10:]) {
		m = m[:n-11]
	}
	return m
}

func isDateSuffix(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, r := range s {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// fingerprintReplacer is shared: building a Replacer per call dominated the
// cost of canonical price matching.
var fingerprintReplacer = strings.NewReplacer("-", "", "_", "", ".", "", " ", "")

func modelFingerprint(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	return fingerprintReplacer.Replace(m)
}

// indexModelFingerprints maps each model fingerprint to the provider/model
// price keys that share it, so canonical matching is a lookup instead of a
// scan over every catalog entry.
func indexModelFingerprints(modelPrices map[string]Price) map[string][]string {
	index := make(map[string][]string, len(modelPrices))
	for key := range modelPrices {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			continue
		}
		fp := modelFingerprint(parts[1])
		index[fp] = append(index[fp], key)
	}
	return index
}

func samePrice(a, b Price) bool {
	return a.InputPerM == b.InputPerM && a.OutputPerM == b.OutputPerM &&
		a.CachedInputPerM == b.CachedInputPerM && a.CacheWritePerM == b.CacheWritePerM &&
		a.ReasoningPerM == b.ReasoningPerM && a.LongContextThreshold == b.LongContextThreshold &&
		a.CacheWrite1hPerM == b.CacheWrite1hPerM && a.WebSearchPerK == b.WebSearchPerK &&
		a.LongInputPerM == b.LongInputPerM && a.LongOutputPerM == b.LongOutputPerM &&
		a.LongCachedInputPerM == b.LongCachedInputPerM &&
		a.LongCacheWritePerM == b.LongCacheWritePerM && a.ExplicitFree == b.ExplicitFree
}

func canonicalSourcePriority(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "official":
		return 0
	case "catalog":
		return 1
	case "custom":
		return 2
	case "retail_equivalent":
		return 3
	default:
		return 4
	}
}

func preferCanonicalPrice(candidateKey string, candidate Price, currentKey string, current Price) bool {
	candidatePriority := canonicalSourcePriority(candidate.Source)
	currentPriority := canonicalSourcePriority(current.Source)
	if candidatePriority != currentPriority {
		return candidatePriority < currentPriority
	}
	if candidate.Estimated != current.Estimated {
		return !candidate.Estimated
	}
	return candidateKey < currentKey
}

// ResolvePrice never silently treats an unknown model as free. Cross-provider
// canonical matches are allowed only when all matching catalog entries agree,
// with deterministic provenance favoring the most authoritative source.
func (m *Meter) ResolvePrice(provider, model string) PricingMatch {
	m.pricingMu.RLock()
	defer m.pricingMu.RUnlock()

	rawProvider := strings.ToLower(strings.TrimSpace(provider))
	canonicalProvider := normalizeProvider(provider)
	candidates := modelCandidates(model)

	for _, p := range []string{rawProvider, canonicalProvider} {
		for _, candidate := range candidates {
			if price, ok := m.modelPrices[p+"/"+candidate]; ok {
				kind := "exact"
				estimated := price.Estimated
				if p != rawProvider {
					kind, estimated = "provider_alias", true
				}
				return pricingMatch(p+"/"+candidate, price, kind, estimated)
			}
		}
	}

	fingerprints := map[string]struct{}{}
	for _, candidate := range candidates {
		fingerprints[modelFingerprint(candidate)] = struct{}{}
	}
	var found Price
	foundKey := ""
	for fp := range fingerprints {
		for _, key := range m.modelFingerprints[fp] {
			price := m.modelPrices[key]
			if foundKey == "" {
				found, foundKey = price, key
				continue
			}
			if !samePrice(found, price) {
				return PricingMatch{Status: "missing", MatchKind: "none"}
			}
			if preferCanonicalPrice(key, price, foundKey, found) {
				found, foundKey = price, key
			}
		}
	}
	if foundKey != "" {
		return pricingMatch(foundKey, found, "canonical_model", true)
	}

	if price, ok := m.pricing[rawProvider]; ok && hasAnyRate(price) {
		return pricingMatch(rawProvider, price, "provider", true)
	}
	if price, ok := m.pricing[canonicalProvider]; ok && hasAnyRate(price) {
		return pricingMatch(canonicalProvider, price, "provider_alias", true)
	}
	return PricingMatch{Status: "missing", MatchKind: "none"}
}

func pricingMatch(key string, price Price, kind string, estimated bool) PricingMatch {
	status := "priced"
	if !hasAnyRate(price) {
		if price.ExplicitFree {
			status = "free"
		} else {
			status = "missing"
		}
	} else if estimated || price.Estimated {
		status = "estimated"
	}
	source := price.Source
	if source == "" {
		source = "catalog"
	}
	return PricingMatch{Price: price, Key: key, Status: status, Source: source, MatchKind: kind, SourceURL: price.SourceURL}
}

func hasAnyRate(p Price) bool {
	return p.InputPerM != 0 || p.OutputPerM != 0 || p.CachedInputPerM != 0 ||
		p.CacheWritePerM != 0 || p.ReasoningPerM != 0 || p.LongInputPerM != 0 || p.LongOutputPerM != 0 ||
		p.WebSearchPerK != 0
}

// cacheWrite1hRatio is the default one-hour cache write premium over the
// five-minute write rate (Anthropic: 2.0× input vs 1.25× input).
const cacheWrite1hRatio = 1.6

// serviceTierMultiplier is the OpenAI service-tier price factor applied when
// the catalogue has no explicit per-tier rates: flex is half price, priority
// is double.
func serviceTierMultiplier(provider, tier string) float64 {
	switch normalizeProvider(provider) {
	case "openai", "azure", "azure_openai":
	default:
		return 1
	}
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "flex":
		return 0.5
	case "priority", "fast":
		return 2
	}
	return 1
}

func effectivePrice(p Price, promptTokens int) Price {
	if p.LongContextThreshold <= 0 || promptTokens <= p.LongContextThreshold {
		return p
	}
	if p.LongInputPerM > 0 {
		p.InputPerM = p.LongInputPerM
	}
	if p.LongOutputPerM > 0 {
		p.OutputPerM = p.LongOutputPerM
	}
	if p.LongCachedInputPerM > 0 {
		p.CachedInputPerM = p.LongCachedInputPerM
	}
	if p.LongCacheWritePerM > 0 {
		p.CacheWritePerM = p.LongCacheWritePerM
	}
	return p
}

func tokenCostNanos(tokens int, ratePerM float64) int64 {
	if tokens <= 0 || ratePerM <= 0 {
		return 0
	}
	return int64(math.Round(float64(tokens) * ratePerM * 1000))
}

func clampUsage(u core.Usage) core.Usage {
	if u.PromptTokens < 0 {
		u.PromptTokens = 0
	}
	if u.CompletionTokens < 0 {
		u.CompletionTokens = 0
	}
	if u.CachedTokens < 0 {
		u.CachedTokens = 0
	}
	if u.CacheWriteTokens < 0 {
		u.CacheWriteTokens = 0
	}
	if u.ReasoningTokens < 0 {
		u.ReasoningTokens = 0
	}
	if u.CachedTokens > u.PromptTokens {
		u.CachedTokens = u.PromptTokens
	}
	remaining := u.PromptTokens - u.CachedTokens
	if u.CacheWriteTokens > remaining {
		u.CacheWriteTokens = remaining
	}
	if u.ReasoningTokens > u.CompletionTokens {
		u.ReasoningTokens = u.CompletionTokens
	}
	if u.CacheWrite1hTokens < 0 {
		u.CacheWrite1hTokens = 0
	}
	if u.CacheWrite1hTokens > u.CacheWriteTokens {
		u.CacheWrite1hTokens = u.CacheWriteTokens
	}
	if u.WebSearchRequests < 0 {
		u.WebSearchRequests = 0
	}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	return u
}

// CalculateCost prices one normalized usage snapshot. Reasoning is a subset of
// completion, never an additional token class, which prevents double charging.
func (m *Meter) CalculateCost(provider, model string, raw core.Usage, cacheHit bool, savedInputTokens int) CostBreakdown {
	return m.CalculateCostWith(provider, model, raw, cacheHit, savedInputTokens, "")
}

// CalculateCostWith is CalculateCost with the request's service tier. A cost
// reported by the provider itself (OpenRouter usage.cost) is authoritative
// over the catalogue and short-circuits the computation.
func (m *Meter) CalculateCostWith(provider, model string, raw core.Usage, cacheHit bool, savedInputTokens int, serviceTier string) CostBreakdown {
	u := clampUsage(raw)
	match := m.ResolvePrice(provider, model)
	out := CostBreakdown{Pricing: match}
	if u.ProviderCostNanos > 0 && !cacheHit {
		out.Pricing = PricingMatch{Key: match.Key, Status: "priced", Source: "provider", MatchKind: "provider_reported", SourceURL: match.SourceURL}
		out.CostNanos = u.ProviderCostNanos
		out.CostMicros = int64(math.Round(float64(out.CostNanos) / 1000))
		return out
	}
	if match.Status == "missing" || match.Status == "none" {
		return out
	}
	p := effectivePrice(match.Price, u.PromptTokens)
	if mult := serviceTierMultiplier(provider, serviceTier); mult != 1 {
		p.InputPerM *= mult
		p.OutputPerM *= mult
		p.CachedInputPerM *= mult
		p.CacheWritePerM *= mult
		p.CacheWrite1hPerM *= mult
		p.ReasoningPerM *= mult
	}
	out.InputRatePerM = p.InputPerM
	out.CachedRatePerM = p.CachedInputPerM
	if out.CachedRatePerM == 0 {
		out.CachedRatePerM = p.InputPerM
	}
	out.CacheWriteRatePerM = p.CacheWritePerM
	if out.CacheWriteRatePerM == 0 {
		out.CacheWriteRatePerM = p.InputPerM
	}
	out.OutputRatePerM = p.OutputPerM
	out.ReasoningRatePerM = p.ReasoningPerM
	if out.ReasoningRatePerM == 0 {
		out.ReasoningRatePerM = p.OutputPerM
	}

	write1hRate := p.CacheWrite1hPerM
	if write1hRate == 0 {
		write1hRate = out.CacheWriteRatePerM * cacheWrite1hRatio
	}

	standardInput := u.PromptTokens - u.CachedTokens - u.CacheWriteTokens
	normalOutput := u.CompletionTokens - u.ReasoningTokens
	write5m := u.CacheWriteTokens - u.CacheWrite1hTokens
	out.InputCostNanos = tokenCostNanos(standardInput, out.InputRatePerM)
	out.CachedCostNanos = tokenCostNanos(u.CachedTokens, out.CachedRatePerM)
	out.CacheWriteCostNanos = tokenCostNanos(write5m, out.CacheWriteRatePerM) + tokenCostNanos(u.CacheWrite1hTokens, write1hRate)
	out.OutputCostNanos = tokenCostNanos(normalOutput, out.OutputRatePerM)
	out.ReasoningCostNanos = tokenCostNanos(u.ReasoningTokens, out.ReasoningRatePerM)
	if u.WebSearchRequests > 0 && p.WebSearchPerK > 0 {
		// USD per 1,000 requests → nanodollars per request = perK × 1e6.
		out.ToolCostNanos = int64(math.Round(float64(u.WebSearchRequests) * p.WebSearchPerK * 1e6))
	}
	retail := out.InputCostNanos + out.CachedCostNanos + out.CacheWriteCostNanos + out.OutputCostNanos + out.ReasoningCostNanos + out.ToolCostNanos
	out.SavedCostNanos = tokenCostNanos(savedInputTokens, out.InputRatePerM)
	if cacheHit {
		out.AvoidedCostNanos = retail
	} else {
		out.CostNanos = retail
		out.CostMicros = int64(math.Round(float64(retail) / 1000))
	}
	return out
}

// ReplacePrices atomically refreshes catalog/custom prices for subsequent
// requests. Existing rows retain their immutable snapshots.
func (m *Meter) ReplacePrices(providerPrices, modelPrices map[string]Price) {
	if providerPrices == nil {
		providerPrices = map[string]Price{}
	}
	if modelPrices == nil {
		modelPrices = map[string]Price{}
	}
	index := indexModelFingerprints(modelPrices)
	m.pricingMu.Lock()
	m.pricing, m.modelPrices, m.modelFingerprints = providerPrices, modelPrices, index
	m.pricingMu.Unlock()
}

type pricingBackfillStore interface {
	ListUnpriced(context.Context, time.Time, string, int) ([]store.UnpricedUsageRecord, error)
	UpdateUsagePricing(context.Context, string, store.UsagePricingUpdate) error
}

// BackfillUnpriced estimates deterministic legacy zero-cost rows in stable
// (created_at,id) order. Historical rates are explicitly marked as backfilled
// estimates so analytics can recover missing spend without retroactively
// changing hard-budget enforcement.
func (m *Meter) BackfillUnpriced(ctx context.Context) (int, error) {
	repo, ok := m.usage.(pricingBackfillStore)
	if !ok {
		return 0, nil
	}
	updated := 0
	cursorTime := time.Time{}
	cursorID := ""
	asOf := time.Now().UTC()
	// Rows for models without a price stay unpriced, so they are listed again on
	// every pass and every restart. Resolve each provider/model once per pass.
	unpriceable := map[string]bool{}
	for {
		rows, err := repo.ListUnpriced(ctx, cursorTime, cursorID, 1000)
		if err != nil {
			return updated, err
		}
		if len(rows) == 0 {
			return updated, nil
		}
		for _, row := range rows {
			cursorTime, cursorID = row.CreatedAt, row.ID
			if err := ctx.Err(); err != nil {
				return updated, err
			}
			modelKey := row.Provider + "\x00" + row.Model
			if unpriceable[modelKey] {
				continue
			}
			usage := core.Usage{PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
				CachedTokens: row.CachedTokens, CacheWriteTokens: row.CacheWriteTokens, ReasoningTokens: row.ReasoningTokens,
				CacheWrite1hTokens: row.CacheWrite1hTokens, WebSearchRequests: row.WebSearchRequests}
			cost := m.CalculateCost(row.Provider, row.Model, usage, row.CacheHit, row.SlimTokensSaved+row.HeadroomTokensSaved)
			if cost.Pricing.Status == "missing" || cost.Pricing.Status == "none" {
				unpriceable[modelKey] = true
				continue
			}
			pricingStatus := cost.Pricing.Status
			if pricingStatus == "priced" {
				pricingStatus = "estimated"
			}
			update := store.UsagePricingUpdate{
				CostMicros: cost.CostMicros, CostNanos: cost.CostNanos,
				InputCostNanos: cost.InputCostNanos, CachedCostNanos: cost.CachedCostNanos,
				CacheWriteCostNanos: cost.CacheWriteCostNanos, OutputCostNanos: cost.OutputCostNanos,
				ReasoningCostNanos: cost.ReasoningCostNanos, AvoidedCostNanos: cost.AvoidedCostNanos,
				SavedCostNanos: cost.SavedCostNanos, ToolCostNanos: cost.ToolCostNanos, PricingStatus: pricingStatus,
				PricingSource: cost.Pricing.Source, PricingKey: cost.Pricing.Key,
				PricingMatchKind: cost.Pricing.MatchKind, PricingSourceURL: cost.Pricing.SourceURL,
				PricingAsOf: &asOf, PricingBackfilled: true,
				InputRatePerM: cost.InputRatePerM, CachedRatePerM: cost.CachedRatePerM,
				CacheWriteRatePerM: cost.CacheWriteRatePerM, OutputRatePerM: cost.OutputRatePerM,
				ReasoningRatePerM: cost.ReasoningRatePerM,
			}
			if err := repo.UpdateUsagePricing(ctx, row.ID, update); err != nil {
				return updated, err
			}
			updated++
		}
		if len(rows) < 1000 {
			return updated, nil
		}
	}
}
