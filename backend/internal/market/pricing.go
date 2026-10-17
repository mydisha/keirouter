package market

import "math"

type Rate struct {
	InputPerM       float64
	OutputPerM      float64
	CachedInputPerM float64
	CacheWritePerM  float64
	// ReasoningPerM is the reasoning-output rate; zero means the output rate.
	ReasoningPerM float64
}

// ComputeChainRate derives a chain's rates from the single cheapest of its bound
// slugs (by input + output), so the rate is always one a real slug sells at. It
// returns false when none of the slugs are present so the caller keeps the
// existing price.
// Cache multipliers applied to a market input rate to derive cached-input and
// cache-write rates. Shared by chain-rate computation and per-step routing so
// the billed cache rates stay identical on both paths.
const (
	CacheReadMult  = 0.1
	CacheWriteMult = 1.25
)

func ComputeChainRate(slugs []string, models []Model, markupPercent, cacheReadMult, cacheWriteMult float64) (Rate, bool) {
	return ComputeChainRateWithSafety(slugs, models, markupPercent, cacheReadMult, cacheWriteMult, false, 0)
}

// ComputeChainRateWithSafety is ComputeChainRate plus an optional safety floor:
// when enabled, the billed rate is raised to the next-best (failover) ask ×
// (1 + safetyPercent/100) so a cheaper offer going dark cannot bill below the
// fallback cost. Failover rates come from the source (e.g. the second-cheapest
// Surplus offer); when unknown the floor is inert.
func ComputeChainRateWithSafety(slugs []string, models []Model, markupPercent, cacheReadMult, cacheWriteMult float64, safetyEnabled bool, safetyPercent float64) (Rate, bool) {
	bySlug := make(map[string]Model, len(models))
	for _, m := range models {
		bySlug[m.Slug] = m
	}
	mult := 1 + markupPercent/100
	var best Model
	failIn, failOut := math.Inf(1), math.Inf(1)
	found := false
	for _, slug := range slugs {
		m, ok := bySlug[slug]
		if !ok {
			continue
		}
		if m.MinAskIn <= 0 || m.MinAskOut <= 0 {
			continue
		}
		if !found || m.MinAskIn+m.MinAskOut < best.MinAskIn+best.MinAskOut {
			best = m
		}
		found = true
		if m.FailoverIn > 0 {
			failIn = math.Min(failIn, m.FailoverIn)
		}
		if m.FailoverOut > 0 {
			failOut = math.Min(failOut, m.FailoverOut)
		}
	}
	if !found {
		return Rate{}, false
	}
	effectiveInput := best.MinAskIn * mult
	effectiveOutput := best.MinAskOut * mult
	if safetyEnabled {
		safetyMult := 1 + safetyPercent/100
		if !math.IsInf(failIn, 1) {
			effectiveInput = math.Max(effectiveInput, failIn*safetyMult)
		}
		if !math.IsInf(failOut, 1) {
			effectiveOutput = math.Max(effectiveOutput, failOut*safetyMult)
		}
		if effectiveInput == failIn*safetyMult && !validRate(effectiveInput) {
			return Rate{}, false
		}
	}
	if !validRate(effectiveInput) || !validRate(effectiveOutput) {
		return Rate{}, false
	}
	cacheReadBase, cacheWriteBase := best.CacheRead, best.CacheWrite
	cachedInput := effectiveInput * cacheReadMult
	if cacheReadBase > 0 {
		cachedInput = cacheReadBase * mult
	}
	cacheWrite := effectiveInput * cacheWriteMult
	if cacheWriteBase > 0 {
		cacheWrite = cacheWriteBase * mult
	}
	if !validRate(cachedInput) || !validRate(cacheWrite) {
		return Rate{}, false
	}
	return Rate{InputPerM: effectiveInput, OutputPerM: effectiveOutput, CachedInputPerM: cachedInput, CacheWritePerM: cacheWrite}, true
}

func validRate(v float64) bool { return v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v) }
