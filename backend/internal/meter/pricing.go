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
	CostNanos           int64
	CostMicros          int64
	AvoidedCostNanos    int64
	SavedCostNanos      int64

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
	return out
}

func modelFingerprint(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	return strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(m)
}

func samePrice(a, b Price) bool {
	return a.InputPerM == b.InputPerM && a.OutputPerM == b.OutputPerM &&
		a.CachedInputPerM == b.CachedInputPerM && a.CacheWritePerM == b.CacheWritePerM &&
		a.ReasoningPerM == b.ReasoningPerM && a.LongContextThreshold == b.LongContextThreshold &&
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
	for key, price := range m.modelPrices {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			continue
		}
		if _, ok := fingerprints[modelFingerprint(parts[1])]; !ok {
			continue
		}
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
		p.CacheWritePerM != 0 || p.ReasoningPerM != 0 || p.LongInputPerM != 0 || p.LongOutputPerM != 0
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
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	return u
}

// chainPrice returns the operator-configured step price when present. All-zero
// rates mean "unset" and fall back to catalog resolution.
func chainPrice(ev Event) (Price, bool) {
	if ev.InputPerM <= 0 && ev.OutputPerM <= 0 && ev.CacheWritePerM <= 0 && ev.CacheReadPerM <= 0 {
		return Price{}, false
	}
	return Price{
		InputPerM: ev.InputPerM, OutputPerM: ev.OutputPerM,
		CacheWritePerM: ev.CacheWritePerM, CachedInputPerM: ev.CacheReadPerM,
		ReasoningPerM: ev.OutputPerM,
		Source:        "chain",
	}, true
}

// calculateCostFromPrice runs the same token math as CalculateCost but with an
// already-resolved price match. It takes the full match so the catalog path
// keeps its provenance (key, match kind, estimated status) instead of both
// paths drifting.
func calculateCostFromPrice(match PricingMatch, uUsage core.Usage, cacheHit bool, savedInputTokens int) CostBreakdown {
	out := CostBreakdown{Pricing: match}
	if match.Status == "missing" || match.Status == "none" {
		return out
	}
	u := clampUsage(uUsage)
	p := effectivePrice(match.Price, u.PromptTokens)
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
	standardInput := u.PromptTokens - u.CachedTokens - u.CacheWriteTokens
	normalOutput := u.CompletionTokens - u.ReasoningTokens
	out.InputCostNanos = tokenCostNanos(standardInput, out.InputRatePerM)
	out.CachedCostNanos = tokenCostNanos(u.CachedTokens, out.CachedRatePerM)
	out.CacheWriteCostNanos = tokenCostNanos(u.CacheWriteTokens, out.CacheWriteRatePerM)
	out.OutputCostNanos = tokenCostNanos(normalOutput, out.OutputRatePerM)
	out.ReasoningCostNanos = tokenCostNanos(u.ReasoningTokens, out.ReasoningRatePerM)
	retail := out.InputCostNanos + out.CachedCostNanos + out.CacheWriteCostNanos + out.OutputCostNanos + out.ReasoningCostNanos
	out.SavedCostNanos = tokenCostNanos(savedInputTokens, out.InputRatePerM)
	if cacheHit {
		out.AvoidedCostNanos = retail
	} else {
		out.CostNanos = retail
		out.CostMicros = int64(math.Round(float64(retail) / 1000))
	}
	return out
}

// CalculateCost prices one normalized usage snapshot. Reasoning is a subset of
// completion, never an additional token class, which prevents double charging.
func (m *Meter) CalculateCost(provider, model string, raw core.Usage, cacheHit bool, savedInputTokens int) CostBreakdown {
	match := m.ResolvePrice(provider, model)
	out := CostBreakdown{Pricing: match}
	if match.Status == "missing" || match.Status == "none" {
		return out
	}
	return calculateCostFromPrice(match, raw, cacheHit, savedInputTokens)
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
	m.pricingMu.Lock()
	m.pricing, m.modelPrices = providerPrices, modelPrices
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
			usage := core.Usage{PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
				CachedTokens: row.CachedTokens, CacheWriteTokens: row.CacheWriteTokens, ReasoningTokens: row.ReasoningTokens}
			cost := m.CalculateCost(row.Provider, row.Model, usage, row.CacheHit, row.SlimTokensSaved+row.HeadroomTokensSaved)
			if cost.Pricing.Status == "missing" || cost.Pricing.Status == "none" {
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
				SavedCostNanos: cost.SavedCostNanos, PricingStatus: pricingStatus,
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
