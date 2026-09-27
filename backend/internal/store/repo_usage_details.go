package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AccurateRecentRecord retains exact token classes, status, latency, and price
// provenance for the request detail drawer.
type AccurateRecentRecord struct {
	ID, RequestID, Provider, Model, Status, ErrorKind, UsageSource string
	PromptTokens, CompletionTokens, CachedTokens, CacheWriteTokens int
	ReasoningTokens                                                int
	CostNanos, InputCostNanos, CachedCostNanos                     int64
	CacheWriteCostNanos, OutputCostNanos, ReasoningCostNanos       int64
	AvoidedCostNanos, SavedCostNanos                               int64
	PricingStatus, PricingSource, PricingKey                       string
	PricingMatchKind, PricingSourceURL                             string
	PricingAsOf                                                    *time.Time
	PricingBackfilled                                              bool
	InputRatePerM, CachedRatePerM, CacheWriteRatePerM              float64
	OutputRatePerM, ReasoningRatePerM                              float64
	CacheHit                                                       bool
	LatencyMS, UpstreamLatencyMS, EndToEndLatencyMS, TTFTMS        int
	SlimBytesSaved, SlimTokensSaved                                int
	SlimRules                                                      string
	SlimActive, CavemanActive, TerseActive                         bool
	HeadroomTokensSaved, HeadroomBytesSaved                        int
	HeadroomActive, PonytailActive                                 bool
	CreatedAt                                                      time.Time
}

func (r *UsageRepo) RecentAccurate(ctx context.Context, tenantID string, since time.Time, limit int) ([]AccurateRecentRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := r.db.rebind(`
		SELECT id, request_id, provider, model, status, error_kind, usage_source,
			prompt_tokens, completion_tokens, cached_tokens, cache_write_tokens, reasoning_tokens,
			cost_nanos, input_cost_nanos, cached_cost_nanos, cache_write_cost_nanos,
			output_cost_nanos, reasoning_cost_nanos, avoided_cost_nanos, saved_cost_nanos,
			pricing_status, pricing_source, pricing_key, pricing_match_kind, pricing_source_url,
			pricing_as_of, pricing_backfilled, input_rate_per_m, cached_rate_per_m,
			cache_write_rate_per_m, output_rate_per_m, reasoning_rate_per_m,
			cache_hit, latency_ms, upstream_latency_ms, end_to_end_latency_ms, ttft_ms,
			slim_bytes_saved, slim_tokens_saved, slim_rules, slim_active, caveman_active, terse_active,
			headroom_tokens_saved, headroom_bytes_saved, headroom_active, ponytail_active, created_at
		FROM usage_records
		WHERE tenant_id=? AND created_at>=?
		ORDER BY created_at DESC LIMIT ?`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("store: accurate recent usage: %w", err)
	}
	defer rows.Close()
	var out []AccurateRecentRecord
	for rows.Next() {
		var rec AccurateRecentRecord
		var cacheHit, pricingBackfilled, slim, caveman, terse, headroom, ponytail int
		var pricingAsOf sql.NullString
		var createdAt string
		if err := rows.Scan(
			&rec.ID, &rec.RequestID, &rec.Provider, &rec.Model, &rec.Status, &rec.ErrorKind, &rec.UsageSource,
			&rec.PromptTokens, &rec.CompletionTokens, &rec.CachedTokens, &rec.CacheWriteTokens, &rec.ReasoningTokens,
			&rec.CostNanos, &rec.InputCostNanos, &rec.CachedCostNanos, &rec.CacheWriteCostNanos,
			&rec.OutputCostNanos, &rec.ReasoningCostNanos, &rec.AvoidedCostNanos, &rec.SavedCostNanos,
			&rec.PricingStatus, &rec.PricingSource, &rec.PricingKey,
			&rec.PricingMatchKind, &rec.PricingSourceURL, &pricingAsOf, &pricingBackfilled,
			&rec.InputRatePerM, &rec.CachedRatePerM, &rec.CacheWriteRatePerM,
			&rec.OutputRatePerM, &rec.ReasoningRatePerM,
			&cacheHit, &rec.LatencyMS, &rec.UpstreamLatencyMS, &rec.EndToEndLatencyMS, &rec.TTFTMS,
			&rec.SlimBytesSaved, &rec.SlimTokensSaved, &rec.SlimRules, &slim, &caveman, &terse,
			&rec.HeadroomTokensSaved, &rec.HeadroomBytesSaved, &headroom, &ponytail, &createdAt); err != nil {
			return nil, err
		}
		rec.CacheHit = cacheHit != 0
		rec.PricingBackfilled = pricingBackfilled != 0
		if pricingAsOf.Valid {
			parsed := parseTime(pricingAsOf.String)
			rec.PricingAsOf = &parsed
		}
		rec.SlimActive, rec.CavemanActive, rec.TerseActive = slim != 0, caveman != 0, terse != 0
		rec.HeadroomActive, rec.PonytailActive = headroom != 0, ponytail != 0
		rec.CreatedAt = parseTime(createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// AccurateTimeBucket powers a multi-series request/token/cost trend chart.
type AccurateTimeBucket struct {
	Bucket                                           int
	Requests, Failed, PromptTokens, CompletionTokens int64
	CostNanos                                        int64
}

func (r *UsageRepo) TimelineAccurate(ctx context.Context, tenantID string, since, to time.Time, buckets int) ([]AccurateTimeBucket, error) {
	if buckets <= 0 {
		buckets = 24
	}
	span := to.Sub(since)
	if span <= 0 {
		span = time.Hour
	}
	slotSecs := int64(span.Seconds()) / int64(buckets)
	if slotSecs <= 0 {
		slotSecs = 60
	}
	epochCreated, epochSince := r.db.epochExpr("created_at"), r.db.epochExpr("?")
	q := r.db.rebind(fmt.Sprintf(`
		SELECT CAST((%s-%s)/? AS INTEGER), COUNT(*),
			COALESCE(SUM(CASE WHEN status NOT IN ('success','cache_hit') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
			COALESCE(SUM(cost_nanos),0)
		FROM usage_records
		WHERE tenant_id=? AND created_at>=? AND created_at<=?
		GROUP BY 1 ORDER BY 1`, epochCreated, epochSince))
	rows, err := r.db.sql.QueryContext(ctx, q, formatTime(since), slotSecs, tenantID, formatTime(since), formatTime(to))
	if err != nil {
		return nil, fmt.Errorf("store: accurate usage timeline: %w", err)
	}
	defer rows.Close()
	var out []AccurateTimeBucket
	for rows.Next() {
		var b AccurateTimeBucket
		if err := rows.Scan(&b.Bucket, &b.Requests, &b.Failed, &b.PromptTokens, &b.CompletionTokens, &b.CostNanos); err != nil {
			return nil, err
		}
		if b.Bucket >= 0 && b.Bucket < buckets {
			out = append(out, b)
		}
	}
	return out, rows.Err()
}

// UnpricedUsageRecord is the minimum historical fact needed by Meter to apply
// a newly available deterministic catalog price.
type UnpricedUsageRecord struct {
	ID, Provider, Model                                                             string
	PromptTokens, CompletionTokens, CachedTokens, CacheWriteTokens, ReasoningTokens int
	CacheHit                                                                        bool
	SlimTokensSaved, HeadroomTokensSaved                                            int
	CreatedAt                                                                       time.Time
}

func (r *UsageRepo) ListUnpriced(ctx context.Context, after time.Time, afterID string, limit int) ([]UnpricedUsageRecord, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	q := r.db.rebind(`
		SELECT id, provider, model, prompt_tokens, completion_tokens, cached_tokens,
			cache_write_tokens, reasoning_tokens, cache_hit, slim_tokens_saved, headroom_tokens_saved,
			created_at
		FROM usage_records
		WHERE cost_nanos=0 AND prompt_tokens+completion_tokens>0
			AND pricing_status IN ('missing','legacy')
			AND (created_at>? OR (created_at=? AND id>?))
		ORDER BY created_at ASC, id ASC LIMIT ?`)
	cursor := formatTime(after)
	rows, err := r.db.sql.QueryContext(ctx, q, cursor, cursor, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list unpriced usage: %w", err)
	}
	defer rows.Close()
	var out []UnpricedUsageRecord
	for rows.Next() {
		var u UnpricedUsageRecord
		var cacheHit int
		var createdAt string
		if err := rows.Scan(&u.ID, &u.Provider, &u.Model, &u.PromptTokens, &u.CompletionTokens,
			&u.CachedTokens, &u.CacheWriteTokens, &u.ReasoningTokens, &cacheHit,
			&u.SlimTokensSaved, &u.HeadroomTokensSaved, &createdAt); err != nil {
			return nil, err
		}
		u.CacheHit = cacheHit != 0
		u.CreatedAt = parseTime(createdAt)
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsagePricingUpdate is an immutable pricing snapshot computed by Meter.
type UsagePricingUpdate struct {
	CostMicros, CostNanos                                 int64
	InputCostNanos, CachedCostNanos, CacheWriteCostNanos  int64
	OutputCostNanos, ReasoningCostNanos, AvoidedCostNanos int64
	SavedCostNanos                                        int64
	PricingStatus, PricingSource, PricingKey              string
	PricingMatchKind, PricingSourceURL                    string
	PricingAsOf                                           *time.Time
	PricingBackfilled                                     bool
	InputRatePerM, CachedRatePerM, CacheWriteRatePerM     float64
	OutputRatePerM, ReasoningRatePerM                     float64
}

func (r *UsageRepo) UpdateUsagePricing(ctx context.Context, id string, p UsagePricingUpdate) error {
	q := r.db.rebind(`UPDATE usage_records SET
		cost_micros=?, cost_nanos=?, input_cost_nanos=?, cached_cost_nanos=?,
		cache_write_cost_nanos=?, output_cost_nanos=?, reasoning_cost_nanos=?,
		avoided_cost_nanos=?, saved_cost_nanos=?, pricing_status=?, pricing_source=?, pricing_key=?,
		pricing_match_kind=?, pricing_source_url=?, pricing_as_of=?, pricing_backfilled=?,
		input_rate_per_m=?, cached_rate_per_m=?, cache_write_rate_per_m=?, output_rate_per_m=?, reasoning_rate_per_m=?
		WHERE id=?`)
	_, err := r.db.sql.ExecContext(ctx, q,
		p.CostMicros, p.CostNanos, p.InputCostNanos, p.CachedCostNanos,
		p.CacheWriteCostNanos, p.OutputCostNanos, p.ReasoningCostNanos,
		p.AvoidedCostNanos, p.SavedCostNanos, p.PricingStatus, p.PricingSource, p.PricingKey,
		p.PricingMatchKind, p.PricingSourceURL, nullTime(p.PricingAsOf), boolToInt(p.PricingBackfilled),
		p.InputRatePerM, p.CachedRatePerM, p.CacheWriteRatePerM, p.OutputRatePerM, p.ReasoningRatePerM, id)
	if err != nil {
		return fmt.Errorf("store: update usage pricing: %w", err)
	}
	return nil
}
