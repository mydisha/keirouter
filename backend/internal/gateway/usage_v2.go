package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const usageTimelineBuckets = 24

// parseTimelineBuckets reads the optional ?buckets= override for the trend
// series, clamped so a client cannot request an unbounded GROUP BY.
func parseTimelineBuckets(raw string) int {
	if raw == "" {
		return usageTimelineBuckets
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 6 || n > 168 {
		return usageTimelineBuckets
	}
	return n
}

// adminUsageInsights returns the authoritative Usage dashboard payload. Costs
// come from immutable nanodollar snapshots; cached and reasoning tokens remain
// subsets of input/output so aggregate token totals never double count them.
func (s *Server) adminUsageInsights(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	recentLimit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			recentLimit = parsed
		}
	}
	bucketCount := parseTimelineBuckets(r.URL.Query().Get("buckets"))
	// limit and buckets change the payload, so they are part of the cache key.
	cacheKey := "insights-v2|" + period + "|" + tz + "|" + strconv.Itoa(recentLimit) + "|" + strconv.Itoa(bucketCount)
	if s.cacheHit(w, cacheKey) {
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	since := sinceForPeriod(period, tz)
	// The previous period is the window of equal length immediately before
	// this one, so deltas compare like with like.
	prevSince := since.Add(-now.Sub(since))
	var (
		summary       store.AccurateSummary
		previous      store.AccurateSummary
		p50, p95      int64
		prevP50       int64
		providers     []store.AccurateProviderUsage
		recent        []store.AccurateRecentRecord
		timeline      []store.AccurateTimeBucket
		ruleSavings   []store.RuleSavings
		clientSavings []store.ClientSavings
	)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		summary, err = s.usage.SummarizeAccurate(groupCtx, adminTenant, since)
		return err
	})
	group.Go(func() error {
		var err error
		previous, err = s.usage.SummarizeAccurateRange(groupCtx, adminTenant, prevSince, since)
		return err
	})
	group.Go(func() error {
		var err error
		p50, p95, err = s.usage.LatencyPercentiles(groupCtx, adminTenant, since, time.Time{})
		return err
	})
	group.Go(func() error {
		var err error
		prevP50, _, err = s.usage.LatencyPercentiles(groupCtx, adminTenant, prevSince, since)
		return err
	})
	group.Go(func() error {
		var err error
		providers, err = s.usage.BreakdownAccurate(groupCtx, adminTenant, since)
		return err
	})
	group.Go(func() error {
		var err error
		recent, err = s.usage.RecentAccurate(groupCtx, adminTenant, since, recentLimit)
		return err
	})
	group.Go(func() error {
		var err error
		timeline, err = s.usage.TimelineAccurate(groupCtx, adminTenant, since, now, bucketCount)
		return err
	})
	group.Go(func() error {
		var err error
		ruleSavings, err = s.usage.SavingsByRule(groupCtx, adminTenant, since)
		return err
	})
	group.Go(func() error {
		var err error
		clientSavings, err = s.usage.SavingsByClient(groupCtx, adminTenant, since)
		return err
	})
	if err := group.Wait(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	totalTokens := summary.PromptTokens + summary.CompletionTokens
	pricingEligibleRequests := summary.PricedRequests + summary.UnpricedRequests
	successRate := ratio(summary.SuccessCount, summary.TotalRequests)
	requestPricingCoverage := optionalRatio(summary.PricedRequests, pricingEligibleRequests)
	tokenPricingCoverage := optionalRatio(totalTokens-summary.UnpricedTokens, totalTokens)

	providerRows := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		display, color, icon := usageProviderMetadata(provider.Provider)
		providerRows = append(providerRows, map[string]any{
			"provider":                  provider.Provider,
			"display_name":              display,
			"color":                     color,
			"icon":                      icon,
			"total_requests":            provider.TotalRequests,
			"successful_requests":       provider.SuccessCount,
			"failed_requests":           provider.TotalRequests - provider.SuccessCount,
			"success_rate":              ratio(provider.SuccessCount, provider.TotalRequests),
			"prompt_tokens":             provider.PromptTokens,
			"completion_tokens":         provider.CompletionTokens,
			"cached_tokens":             provider.CachedTokens,
			"cache_write_tokens":        provider.CacheWriteTokens,
			"reasoning_tokens":          provider.ReasoningTokens,
			"total_tokens":              provider.PromptTokens + provider.CompletionTokens,
			"cost_usd":                  nanosToUSD(provider.CostNanos),
			"saved_cost_usd":            nanosToUSD(provider.SavedCostNanos),
			"avoided_cost_usd":          nanosToUSD(provider.AvoidedCostNanos),
			"avg_latency_ms":            provider.AvgLatencyMS,
			"avg_ttft_ms":               provider.AvgTTFTMS,
			"pricing_eligible_requests": provider.PricingEligibleRequests,
			"unpriced_requests":         provider.UnpricedRequests,
			"estimated_requests":        provider.EstimatedRequests,
			"estimated_usage_requests":  provider.EstimatedUsageRequests,
			"legacy_usage_requests":     provider.LegacyUsageRequests,
			"backfilled_requests":       provider.BackfilledRequests,
			"pricing_request_coverage":  optionalRatio(provider.PricingEligibleRequests-provider.UnpricedRequests, provider.PricingEligibleRequests),
			"share_pct":                 ratio(provider.TotalRequests, summary.TotalRequests) * 100,
			"token_share_pct":           ratio(provider.PromptTokens+provider.CompletionTokens, totalTokens) * 100,
		})
	}

	keyNames, accountLabels := s.usageNameLookups(ctx, recent)
	recentRows := make([]map[string]any, 0, len(recent))
	for _, record := range recent {
		display, color, icon := usageProviderMetadata(record.Provider)
		recentRows = append(recentRows, map[string]any{
			"id":                     record.ID,
			"request_id":             record.RequestID,
			"api_key_id":             record.APIKeyID,
			"api_key_name":           keyNames[record.APIKeyID],
			"account_id":             record.AccountID,
			"account_label":          accountLabels[record.AccountID],
			"client":                 record.Client,
			"provider":               record.Provider,
			"provider_name":          display,
			"provider_color":         color,
			"provider_icon":          icon,
			"model":                  record.Model,
			"status":                 record.Status,
			"error_kind":             record.ErrorKind,
			"usage_source":           record.UsageSource,
			"prompt_tokens":          record.PromptTokens,
			"completion_tokens":      record.CompletionTokens,
			"cached_tokens":          record.CachedTokens,
			"cache_write_tokens":     record.CacheWriteTokens,
			"reasoning_tokens":       record.ReasoningTokens,
			"tokens":                 record.PromptTokens + record.CompletionTokens,
			"cost_usd":               nanosToUSD(record.CostNanos),
			"input_cost_usd":         nanosToUSD(record.InputCostNanos),
			"cached_cost_usd":        nanosToUSD(record.CachedCostNanos),
			"cache_write_cost_usd":   nanosToUSD(record.CacheWriteCostNanos),
			"output_cost_usd":        nanosToUSD(record.OutputCostNanos),
			"reasoning_cost_usd":     nanosToUSD(record.ReasoningCostNanos),
			"saved_cost_usd":         nanosToUSD(record.SavedCostNanos),
			"avoided_cost_usd":       nanosToUSD(record.AvoidedCostNanos),
			"pricing_status":         record.PricingStatus,
			"pricing_source":         record.PricingSource,
			"pricing_key":            record.PricingKey,
			"pricing_match_kind":     record.PricingMatchKind,
			"pricing_source_url":     record.PricingSourceURL,
			"pricing_as_of":          record.PricingAsOf,
			"pricing_backfilled":     record.PricingBackfilled,
			"input_rate_per_m":       record.InputRatePerM,
			"cached_rate_per_m":      record.CachedRatePerM,
			"cache_write_rate_per_m": record.CacheWriteRatePerM,
			"output_rate_per_m":      record.OutputRatePerM,
			"reasoning_rate_per_m":   record.ReasoningRatePerM,
			"cache_hit":              record.CacheHit,
			"latency_ms":             record.EndToEndLatencyMS,
			"upstream_latency_ms":    record.UpstreamLatencyMS,
			"end_to_end_latency_ms":  record.EndToEndLatencyMS,
			"ttft_ms":                record.TTFTMS,
			"slim_bytes_saved":       record.SlimBytesSaved,
			"slim_tokens_saved":      record.SlimTokensSaved,
			"slim_rules":             record.SlimRules,
			"slim_active":            record.SlimActive,
			"caveman_active":         record.CavemanActive,
			"terse_active":           record.TerseActive,
			"headroom_tokens_saved":  record.HeadroomTokensSaved,
			"headroom_bytes_saved":   record.HeadroomBytesSaved,
			"headroom_active":        record.HeadroomActive,
			"ponytail_active":        record.PonytailActive,
			"created_at":             record.CreatedAt,
		})
	}

	series, busiest := accurateTimelineSeries(timeline, since, now, bucketCount, tz)
	rules := make([]map[string]any, 0, len(ruleSavings))
	for _, saving := range ruleSavings {
		rules = append(rules, map[string]any{
			"rule":         saving.Rule,
			"count":        saving.Count,
			"bytes_saved":  saving.BytesSaved,
			"tokens_saved": saving.BytesSaved / 4,
		})
	}
	clients := make([]map[string]any, 0, len(clientSavings))
	for _, saving := range clientSavings {
		if saving.SlimTokensSaved == 0 && saving.HeadroomTokensSaved == 0 &&
			saving.CavemanRequests == 0 && saving.TerseRequests == 0 && saving.PonytailRequests == 0 {
			continue
		}
		clients = append(clients, map[string]any{
			"client":                saving.Client,
			"requests":              saving.Requests,
			"optimized_requests":    saving.OptimizedRequests,
			"bytes_saved":           saving.SlimBytesSaved,
			"tokens_saved":          saving.SlimTokensSaved + saving.HeadroomTokensSaved,
			"slim_tokens_saved":     saving.SlimTokensSaved,
			"caveman_requests":      saving.CavemanRequests,
			"terse_requests":        saving.TerseRequests,
			"headroom_tokens_saved": saving.HeadroomTokensSaved,
			"ponytail_requests":     saving.PonytailRequests,
			"saved_cost_usd":        nanosToUSD(saving.SavedCostNanos),
			"avoided_cost_usd":      nanosToUSD(saving.AvoidedCostNanos),
			"usd_saved":             nanosToUSD(saving.SavedCostNanos + saving.AvoidedCostNanos),
		})
	}

	tokensSaved := summary.SlimTokensSaved + summary.HeadroomTokensSaved
	totalSavedNanos := summary.SavedCostNanos + summary.AvoidedCostNanos
	savingsEstimated := totalSavedNanos > 0 && (summary.SlimTokensSaved > 0 ||
		summary.AvoidedCostNanos > 0 || summary.EstimatedRequests > 0 ||
		summary.EstimatedUsageRequests > 0 || summary.LegacyUsageRequests > 0 ||
		summary.BackfilledRequests > 0)
	response := map[string]any{
		"period":       period,
		"since":        since,
		"generated_at": now,
		"summary": map[string]any{
			"total_requests":            summary.TotalRequests,
			"successful_requests":       summary.SuccessCount,
			"failed_requests":           summary.FailureCount,
			"prompt_tokens":             summary.PromptTokens,
			"completion_tokens":         summary.CompletionTokens,
			"cached_tokens":             summary.CachedTokens,
			"cache_write_tokens":        summary.CacheWriteTokens,
			"reasoning_tokens":          summary.ReasoningTokens,
			"total_tokens":              totalTokens,
			"cost_usd":                  nanosToUSD(summary.CostNanos),
			"cost_per_request_usd":      perRequestUSD(summary.CostNanos, summary.TotalRequests),
			"tokens_per_request":        averageCount(totalTokens, summary.TotalRequests),
			"cache_hits":                summary.CacheHits,
			"success_rate":              successRate,
			"avg_latency_ms":            summary.AvgLatencyMS,
			"p50_latency_ms":            p50,
			"p95_latency_ms":            p95,
			"avg_ttft_ms":               summary.AvgTTFTMS,
			"pricing_eligible_requests": pricingEligibleRequests,
			"priced_requests":           summary.PricedRequests,
			"unpriced_requests":         summary.UnpricedRequests,
			"unpriced_tokens":           summary.UnpricedTokens,
			"estimated_requests":        summary.EstimatedRequests,
			"estimated_usage_requests":  summary.EstimatedUsageRequests,
			"estimated_usage_tokens":    summary.EstimatedUsageTokens,
			"legacy_usage_requests":     summary.LegacyUsageRequests,
			"legacy_usage_tokens":       summary.LegacyUsageTokens,
			"backfilled_requests":       summary.BackfilledRequests,
			"pricing_request_coverage":  requestPricingCoverage,
			"pricing_token_coverage":    tokenPricingCoverage,
			"since":                     since,
		},
		"savings": map[string]any{
			"slim_bytes_saved":                   summary.SlimBytesSaved,
			"slim_tokens_saved":                  summary.SlimTokensSaved,
			"headroom_tokens_saved":              summary.HeadroomTokensSaved,
			"total_tokens_saved":                 tokensSaved,
			"saved_tokens_per_request":           averageCount(tokensSaved, summary.TotalRequests),
			"saved_tokens_per_optimized_request": averageCount(tokensSaved, summary.OptimizedRequests),
			"optimized_requests":                 summary.OptimizedRequests,
			"caveman_requests":                   summary.CavemanRequests,
			"terse_requests":                     summary.TerseRequests,
			"headroom_requests":                  summary.HeadroomRequests,
			"ponytail_requests":                  summary.PonytailRequests,
			"saved_cost_usd":                     nanosToUSD(summary.SavedCostNanos),
			"avoided_cost_usd":                   nanosToUSD(summary.AvoidedCostNanos),
			"usd_saved":                          nanosToUSD(totalSavedNanos),
			"usd_saved_estimate":                 savingsEstimated,
			"rules":                              rules,
			"by_client":                          clients,
		},
		"previous":  previousPeriodSummary(previous, prevP50, prevSince, since),
		"providers": providerRows,
		"recent":    recentRows,
		"series":    series,
		"busiest":   busiest,
	}
	writeJSONCached(w, s.insightsCache, cacheKey, response)
}

// adminModelUsageAccurate returns per-model request, token, latency, price
// coverage, rate snapshot, and cost totals for the selected period.
func (s *Server) adminModelUsageAccurate(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	cacheKey := "models-v2|" + period + "|" + tz
	if s.cacheHit(w, cacheKey) {
		return
	}

	since := sinceForPeriod(period, tz)
	models, err := s.usage.ByModelAccurate(r.Context(), adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows := make([]map[string]any, 0, len(models))
	for _, model := range models {
		display, color, icon := usageProviderMetadata(model.Provider)
		pricingStatus := aggregateModelPricingStatus(model)
		rows = append(rows, map[string]any{
			"provider":                  model.Provider,
			"provider_name":             display,
			"provider_color":            color,
			"provider_icon":             icon,
			"model":                     model.Model,
			"total_requests":            model.TotalRequests,
			"successful_requests":       model.SuccessCount,
			"failed_requests":           model.TotalRequests - model.SuccessCount,
			"success_rate":              ratio(model.SuccessCount, model.TotalRequests),
			"prompt_tokens":             model.PromptTokens,
			"completion_tokens":         model.CompletionTokens,
			"cached_tokens":             model.CachedTokens,
			"cache_write_tokens":        model.CacheWriteTokens,
			"reasoning_tokens":          model.ReasoningTokens,
			"total_tokens":              model.PromptTokens + model.CompletionTokens,
			"cost_usd":                  nanosToUSD(model.CostNanos),
			"saved_cost_usd":            nanosToUSD(model.SavedCostNanos),
			"avoided_cost_usd":          nanosToUSD(model.AvoidedCostNanos),
			"avg_latency_ms":            model.AvgLatencyMS,
			"avg_ttft_ms":               model.AvgTTFTMS,
			"pricing_eligible_requests": model.PricingEligibleRequests,
			"unpriced_requests":         model.UnpricedRequests,
			"missing_pricing_requests":  model.MissingPricingRequests,
			"legacy_pricing_requests":   model.LegacyPricingRequests,
			"estimated_requests":        model.EstimatedRequests,
			"estimated_usage_requests":  model.EstimatedUsageRequests,
			"legacy_usage_requests":     model.LegacyUsageRequests,
			"backfilled_requests":       model.BackfilledRequests,
			"pricing_request_coverage":  optionalRatio(model.PricingEligibleRequests-model.UnpricedRequests, model.PricingEligibleRequests),
			"pricing_status":            pricingStatus,
			"pricing_mixed":             model.PricingMixed,
			"pricing_source":            model.PricingSource,
			"pricing_key":               model.PricingKey,
			"input_per_m":               model.InputRatePerM,
			"cached_input_per_m":        model.CachedRatePerM,
			"cache_write_per_m":         model.CacheWriteRatePerM,
			"output_per_m":              model.OutputRatePerM,
			"reasoning_per_m":           model.ReasoningRatePerM,
		})
	}
	writeJSONCached(w, s.insightsCache, cacheKey, map[string]any{
		"period":       period,
		"since":        since,
		"generated_at": time.Now().UTC(),
		"models":       rows,
	})
}

func usageProviderMetadata(provider string) (display, color, icon string) {
	display = provider
	if spec, ok := connectors.SpecByID(provider); ok {
		display = spec.DisplayName
		color = spec.Color
	}
	icon = "/providers/" + providerIconID(provider) + ".png"
	return display, color, icon
}

func nanosToUSD(nanos int64) float64 {
	return float64(nanos) / 1_000_000_000
}

func perRequestUSD(nanos, requests int64) float64 {
	if requests <= 0 {
		return 0
	}
	return nanosToUSD(nanos) / float64(requests)
}

func averageCount(total, requests int64) float64 {
	if requests <= 0 {
		return 0
	}
	return float64(total) / float64(requests)
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	if numerator < 0 {
		numerator = 0
	}
	if numerator > denominator {
		numerator = denominator
	}
	return float64(numerator) / float64(denominator)
}

// optionalRatio represents an undefined empty-set ratio as JSON null instead
// of presenting 0/0 as measured zero (or complete coverage).
func optionalRatio(numerator, denominator int64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := ratio(numerator, denominator)
	return &value
}

func aggregateModelPricingStatus(model store.AccurateModelUsage) string {
	switch {
	case model.PricingEligibleRequests == 0:
		return "none"
	case model.MissingPricingRequests == model.PricingEligibleRequests:
		return "missing"
	case model.LegacyPricingRequests == model.PricingEligibleRequests:
		return "legacy"
	case model.UnpricedRequests > 0:
		return "partial"
	case model.PricingMixed:
		return "mixed"
	case model.EstimatedRequests > 0:
		return "estimated"
	default:
		return model.PricingStatus
	}
}

func accurateTimelineSeries(points []store.AccurateTimeBucket, from, to time.Time, count int, tz string) ([]map[string]any, string) {
	if count <= 0 {
		count = usageTimelineBuckets
	}
	span := to.Sub(from)
	if span <= 0 {
		span = time.Hour
	}
	slot := span / time.Duration(count)
	if slot <= 0 {
		slot = time.Minute
	}
	location := time.Local
	if tz != "" {
		if parsed, err := time.LoadLocation(tz); err == nil {
			location = parsed
		}
	}

	byIndex := make(map[int]store.AccurateTimeBucket, len(points))
	for _, point := range points {
		byIndex[point.Bucket] = point
	}
	series := make([]map[string]any, 0, count)
	busiest := ""
	var busiestRequests int64
	for index := 0; index < count; index++ {
		start := from.Add(time.Duration(index) * slot)
		labelFormat := "15:04"
		if span > 48*time.Hour {
			labelFormat = "Jan 02"
		}
		point := byIndex[index]
		label := start.In(location).Format(labelFormat)
		if point.Requests > busiestRequests {
			busiestRequests = point.Requests
			busiest = label
		}
		series = append(series, map[string]any{
			"label":             label,
			"start":             start.UTC(),
			"count":             point.Requests,
			"requests":          point.Requests,
			"failures":          point.Failed,
			"prompt_tokens":     point.PromptTokens,
			"completion_tokens": point.CompletionTokens,
			"cost_usd":          nanosToUSD(point.CostNanos),
			"saved_usd":         nanosToUSD(point.SavedNanos),
			"cache_hits":        point.CacheHits,
			"avg_latency_ms":    point.AvgLatencyMS,
		})
	}
	return series, busiest
}

// previousPeriodSummary is the compact headline rollup of the window before
// the selected period. The dashboard derives period-over-period deltas from
// it; the full breakdown is only computed for the current period.
func previousPeriodSummary(prev store.AccurateSummary, p50 int64, since, until time.Time) map[string]any {
	return map[string]any{
		"since":               since,
		"until":               until,
		"total_requests":      prev.TotalRequests,
		"successful_requests": prev.SuccessCount,
		"failed_requests":     prev.FailureCount,
		"success_rate":        ratio(prev.SuccessCount, prev.TotalRequests),
		"total_tokens":        prev.PromptTokens + prev.CompletionTokens,
		"cost_usd":            nanosToUSD(prev.CostNanos),
		"usd_saved":           nanosToUSD(prev.SavedCostNanos + prev.AvoidedCostNanos),
		"cache_hits":          prev.CacheHits,
		"avg_latency_ms":      prev.AvgLatencyMS,
		"p50_latency_ms":      p50,
	}
}

// usageNameLookups resolves the API key names and account labels referenced
// by recent usage rows so the dashboard can show "team-frontend" instead of
// an opaque id. Lookup failures degrade to empty names rather than failing
// the whole insights payload.
func (s *Server) usageNameLookups(ctx context.Context, recent []store.AccurateRecentRecord) (map[string]string, map[string]string) {
	keyNames := map[string]string{}
	accountLabels := map[string]string{}
	if len(recent) == 0 {
		return keyNames, accountLabels
	}
	if keys, err := s.identity.List(ctx, adminTenant); err == nil {
		for _, k := range keys {
			keyNames[k.ID] = k.Name
		}
	}
	if accounts, err := s.accounts.ListByTenant(ctx, adminTenant); err == nil {
		for _, a := range accounts {
			accountLabels[a.ID] = a.Label
		}
	}
	return keyNames, accountLabels
}
