package store

import (
	"context"
	"fmt"
	"time"
)

// AccurateSummary is the authoritative request, token, cost, latency, pricing
// coverage, usage provenance, and optimization rollup used by the Usage page.
type AccurateSummary struct {
	TotalRequests, SuccessCount, FailureCount                           int64
	PromptTokens, CompletionTokens, CachedTokens, CacheWriteTokens      int64
	ReasoningTokens, CostNanos, CacheHits                               int64
	AvgTTFTMS, AvgLatencyMS                                             int64
	PricedRequests, UnpricedRequests, UnpricedTokens, EstimatedRequests int64
	EstimatedUsageRequests, EstimatedUsageTokens                        int64
	LegacyUsageRequests, LegacyUsageTokens, BackfilledRequests          int64
	SlimBytesSaved, SlimTokensSaved, HeadroomTokensSaved                int64
	HeadroomRequests, CavemanRequests, TerseRequests, PonytailRequests  int64
	OptimizedRequests, SavedCostNanos, AvoidedCostNanos                 int64
}

// SummarizeAccurate uses explicit terminal status instead of inferring success
// from latency. Pricing coverage only considers token-bearing requests and
// treats legacy totals without rate snapshots as uncovered.
func (r *UsageRepo) SummarizeAccurate(ctx context.Context, tenantID string, since time.Time) (AccurateSummary, error) {
	return r.SummarizeAccurateRange(ctx, tenantID, since, time.Time{})
}

// SummarizeAccurateRange is SummarizeAccurate over the half-open window
// [since, until). A zero until leaves the window open-ended, which is how the
// current period is summarised; a non-zero until summarises a closed window
// such as the previous period used for period-over-period deltas.
func (r *UsageRepo) SummarizeAccurateRange(ctx context.Context, tenantID string, since, until time.Time) (AccurateSummary, error) {
	where, args := rangeWhere(tenantID, since, until)
	q := r.db.rebind(`
		SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('success','cache_hit') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status NOT IN ('success','cache_hit') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
			COALESCE(SUM(cached_tokens),0), COALESCE(SUM(cache_write_tokens),0),
			COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(cost_nanos),0),
			COALESCE(SUM(cache_hit),0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms > 0 THEN ttft_ms END) AS INTEGER),0),
			COALESCE(CAST(AVG(CASE WHEN end_to_end_latency_ms > 0 THEN end_to_end_latency_ms END) AS INTEGER),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status IN ('priced','estimated','free') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status IN ('missing','legacy') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN pricing_status IN ('missing','legacy') THEN prompt_tokens+completion_tokens ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status='estimated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND usage_source='estimated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN usage_source='estimated' THEN prompt_tokens+completion_tokens ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND usage_source='legacy' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN usage_source='legacy' THEN prompt_tokens+completion_tokens ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN pricing_backfilled=1 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(slim_bytes_saved),0), COALESCE(SUM(slim_tokens_saved),0),
			COALESCE(SUM(headroom_tokens_saved),0), COALESCE(SUM(headroom_active),0),
			COALESCE(SUM(caveman_active),0), COALESCE(SUM(terse_active),0),
			COALESCE(SUM(ponytail_active),0),
			COALESCE(SUM(CASE WHEN slim_active=1 OR headroom_active=1 OR caveman_active=1 OR terse_active=1 OR ponytail_active=1 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(saved_cost_nanos),0), COALESCE(SUM(avoided_cost_nanos),0)
		FROM usage_records WHERE ` + where)
	var s AccurateSummary
	err := r.db.sql.QueryRowContext(ctx, q, args...).Scan(
		&s.TotalRequests, &s.SuccessCount, &s.FailureCount,
		&s.PromptTokens, &s.CompletionTokens, &s.CachedTokens, &s.CacheWriteTokens,
		&s.ReasoningTokens, &s.CostNanos, &s.CacheHits, &s.AvgTTFTMS, &s.AvgLatencyMS,
		&s.PricedRequests, &s.UnpricedRequests, &s.UnpricedTokens, &s.EstimatedRequests,
		&s.EstimatedUsageRequests, &s.EstimatedUsageTokens,
		&s.LegacyUsageRequests, &s.LegacyUsageTokens, &s.BackfilledRequests,
		&s.SlimBytesSaved, &s.SlimTokensSaved, &s.HeadroomTokensSaved, &s.HeadroomRequests,
		&s.CavemanRequests, &s.TerseRequests, &s.PonytailRequests, &s.OptimizedRequests,
		&s.SavedCostNanos, &s.AvoidedCostNanos)
	if err != nil {
		return AccurateSummary{}, fmt.Errorf("store: accurate usage summary: %w", err)
	}
	return s, nil
}

type AccurateProviderUsage struct {
	Provider                                                        string
	TotalRequests, SuccessCount                                     int64
	PromptTokens, CompletionTokens, CachedTokens, CacheWriteTokens  int64
	ReasoningTokens, CostNanos, SavedCostNanos, AvoidedCostNanos    int64
	AvgLatencyMS, AvgTTFTMS                                         int64
	PricingEligibleRequests, UnpricedRequests, EstimatedRequests    int64
	EstimatedUsageRequests, LegacyUsageRequests, BackfilledRequests int64
}

func (r *UsageRepo) BreakdownAccurate(ctx context.Context, tenantID string, since time.Time) ([]AccurateProviderUsage, error) {
	q := r.db.rebind(`
		SELECT provider, COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('success','cache_hit') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
			COALESCE(SUM(cached_tokens),0), COALESCE(SUM(cache_write_tokens),0),
			COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(cost_nanos),0),
			COALESCE(SUM(saved_cost_nanos),0), COALESCE(SUM(avoided_cost_nanos),0),
			COALESCE(CAST(AVG(CASE WHEN end_to_end_latency_ms>0 THEN end_to_end_latency_ms END) AS INTEGER),0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms>0 THEN ttft_ms END) AS INTEGER),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status IN ('missing','legacy') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status='estimated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND usage_source='estimated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND usage_source='legacy' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN pricing_backfilled=1 THEN 1 ELSE 0 END),0)
		FROM usage_records WHERE tenant_id=? AND created_at>=?
		GROUP BY provider ORDER BY COUNT(*) DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: accurate provider breakdown: %w", err)
	}
	defer rows.Close()
	var out []AccurateProviderUsage
	for rows.Next() {
		var p AccurateProviderUsage
		if err := rows.Scan(&p.Provider, &p.TotalRequests, &p.SuccessCount,
			&p.PromptTokens, &p.CompletionTokens, &p.CachedTokens, &p.CacheWriteTokens,
			&p.ReasoningTokens, &p.CostNanos, &p.SavedCostNanos, &p.AvoidedCostNanos,
			&p.AvgLatencyMS, &p.AvgTTFTMS, &p.PricingEligibleRequests, &p.UnpricedRequests,
			&p.EstimatedRequests, &p.EstimatedUsageRequests, &p.LegacyUsageRequests,
			&p.BackfilledRequests); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type AccurateModelUsage struct {
	Provider, Model                                                  string
	TotalRequests, SuccessCount                                      int64
	PromptTokens, CompletionTokens, CachedTokens, CacheWriteTokens   int64
	ReasoningTokens, CostNanos, SavedCostNanos, AvoidedCostNanos     int64
	AvgLatencyMS, AvgTTFTMS                                          int64
	PricingEligibleRequests, UnpricedRequests                        int64
	MissingPricingRequests, LegacyPricingRequests, EstimatedRequests int64
	EstimatedUsageRequests, LegacyUsageRequests, BackfilledRequests  int64
	PricingStatus, PricingSource, PricingKey                         string
	InputRatePerM, CachedRatePerM, CacheWriteRatePerM                float64
	OutputRatePerM, ReasoningRatePerM                                float64
	PricingMixed                                                     bool
}

func (r *UsageRepo) ByModelAccurate(ctx context.Context, tenantID string, since time.Time) ([]AccurateModelUsage, error) {
	q := r.db.rebind(`
		SELECT provider, model, COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('success','cache_hit') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
			COALESCE(SUM(cached_tokens),0), COALESCE(SUM(cache_write_tokens),0),
			COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(cost_nanos),0),
			COALESCE(SUM(saved_cost_nanos),0), COALESCE(SUM(avoided_cost_nanos),0),
			COALESCE(CAST(AVG(CASE WHEN end_to_end_latency_ms>0 THEN end_to_end_latency_ms END) AS INTEGER),0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms>0 THEN ttft_ms END) AS INTEGER),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status IN ('missing','legacy') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status='missing' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status='legacy' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND pricing_status='estimated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND usage_source='estimated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN prompt_tokens+completion_tokens>0 AND usage_source='legacy' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN pricing_backfilled=1 THEN 1 ELSE 0 END),0),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN pricing_status END),'none'),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN pricing_status END),'none'),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN pricing_source END),''),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN pricing_source END),''),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN pricing_key END),''),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN pricing_key END),''),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN input_rate_per_m END),0),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN input_rate_per_m END),0),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN cached_rate_per_m END),0),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN cached_rate_per_m END),0),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN cache_write_rate_per_m END),0),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN cache_write_rate_per_m END),0),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN output_rate_per_m END),0),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN output_rate_per_m END),0),
			COALESCE(MIN(CASE WHEN pricing_status<>'none' THEN reasoning_rate_per_m END),0),
			COALESCE(MAX(CASE WHEN pricing_status<>'none' THEN reasoning_rate_per_m END),0)
		FROM usage_records WHERE tenant_id=? AND created_at>=?
		GROUP BY provider, model ORDER BY COUNT(*) DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: accurate model breakdown: %w", err)
	}
	defer rows.Close()
	var out []AccurateModelUsage
	for rows.Next() {
		var m AccurateModelUsage
		var minStatus, maxStatus, minSource, maxSource, minKey, maxKey string
		var minInput, maxInput, minCached, maxCached, minWrite, maxWrite float64
		var minOutput, maxOutput, minReasoning, maxReasoning float64
		if err := rows.Scan(&m.Provider, &m.Model, &m.TotalRequests, &m.SuccessCount,
			&m.PromptTokens, &m.CompletionTokens, &m.CachedTokens, &m.CacheWriteTokens,
			&m.ReasoningTokens, &m.CostNanos, &m.SavedCostNanos, &m.AvoidedCostNanos,
			&m.AvgLatencyMS, &m.AvgTTFTMS, &m.PricingEligibleRequests, &m.UnpricedRequests,
			&m.MissingPricingRequests, &m.LegacyPricingRequests, &m.EstimatedRequests,
			&m.EstimatedUsageRequests, &m.LegacyUsageRequests,
			&m.BackfilledRequests, &minStatus, &maxStatus, &minSource, &maxSource, &minKey, &maxKey,
			&minInput, &maxInput, &minCached, &maxCached, &minWrite, &maxWrite,
			&minOutput, &maxOutput, &minReasoning, &maxReasoning); err != nil {
			return nil, err
		}
		m.PricingMixed = minStatus != maxStatus || minSource != maxSource || minKey != maxKey ||
			minInput != maxInput || minCached != maxCached || minWrite != maxWrite ||
			minOutput != maxOutput || minReasoning != maxReasoning
		if m.PricingMixed {
			m.PricingStatus, m.PricingSource, m.PricingKey = "mixed", "mixed", ""
		} else {
			m.PricingStatus, m.PricingSource, m.PricingKey = minStatus, minSource, minKey
			m.InputRatePerM, m.CachedRatePerM, m.CacheWriteRatePerM = minInput, minCached, minWrite
			m.OutputRatePerM, m.ReasoningRatePerM = minOutput, minReasoning
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// rangeWhere builds the tenant + time-window predicate shared by the range
// aggregate queries. until is exclusive and omitted when zero.
func rangeWhere(tenantID string, since, until time.Time) (string, []any) {
	where := "tenant_id=? AND created_at>=?"
	args := []any{tenantID, formatTime(since)}
	if !until.IsZero() {
		where += " AND created_at<?"
		args = append(args, formatTime(until))
	}
	return where, args
}

// LatencyPercentiles returns the p50 and p95 end-to-end latency (ms) of
// successful requests (including cache hits) in [since, until). SQLite has no
// percentile aggregate, so each percentile is read as a single row at its
// ordinal offset, which works identically on SQLite and Postgres.
func (r *UsageRepo) LatencyPercentiles(ctx context.Context, tenantID string, since, until time.Time) (p50, p95 int64, err error) {
	where, args := rangeWhere(tenantID, since, until)
	where += " AND end_to_end_latency_ms > 0 AND status IN ('success','cache_hit')"
	var n int64
	if err := r.db.sql.QueryRowContext(ctx, r.db.rebind(`SELECT COUNT(*) FROM usage_records WHERE `+where), args...).Scan(&n); err != nil {
		return 0, 0, fmt.Errorf("store: latency percentile count: %w", err)
	}
	if n == 0 {
		return 0, 0, nil
	}
	at := func(pct float64) (int64, error) {
		offset := int64(float64(n-1) * pct)
		q := r.db.rebind(`SELECT end_to_end_latency_ms FROM usage_records WHERE ` + where + ` ORDER BY end_to_end_latency_ms LIMIT 1 OFFSET ?`)
		var v int64
		if err := r.db.sql.QueryRowContext(ctx, q, append(append([]any{}, args...), offset)...).Scan(&v); err != nil {
			return 0, fmt.Errorf("store: latency percentile: %w", err)
		}
		return v, nil
	}
	if p50, err = at(0.50); err != nil {
		return 0, 0, err
	}
	if p95, err = at(0.95); err != nil {
		return 0, 0, err
	}
	return p50, p95, nil
}

// ChainServedUsage is one (chain, serving provider, serving model) rollup:
// how many chain-routed requests that target terminally handled, how many of
// those only reached it after failing over, and what they cost.
type ChainServedUsage struct {
	ChainID, Provider, Model      string
	Requests, Successes, FellBack int64
	CostNanos, AvgLatencyMS       int64
}

// ChainUsage groups chain-routed requests by the target that served them.
// The caller maps (provider, model) back onto the chain's current steps, which
// keeps the step distribution correct even when a cost/latency/round-robin
// strategy reorders the steps at dispatch time.
func (r *UsageRepo) ChainUsage(ctx context.Context, tenantID string, since time.Time) ([]ChainServedUsage, error) {
	q := r.db.rebind(`
		SELECT chain_id, provider, model, COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('success','cache_hit') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN fallback_count > 0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(cost_nanos),0),
			COALESCE(CAST(AVG(CASE WHEN end_to_end_latency_ms>0 AND status IN ('success','cache_hit') THEN end_to_end_latency_ms END) AS INTEGER),0)
		FROM usage_records
		WHERE tenant_id=? AND created_at>=? AND chain_id<>''
		GROUP BY chain_id, provider, model`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: chain usage: %w", err)
	}
	defer rows.Close()
	var out []ChainServedUsage
	for rows.Next() {
		var c ChainServedUsage
		if err := rows.Scan(&c.ChainID, &c.Provider, &c.Model, &c.Requests, &c.Successes,
			&c.FellBack, &c.CostNanos, &c.AvgLatencyMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
