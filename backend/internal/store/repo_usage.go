package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// UsageRepo records and aggregates per-request metering data.
type UsageRepo struct{ db *DB }

// Usage returns the usage repository.
func (db *DB) Usage() *UsageRepo { return &UsageRepo{db: db} }

// Record inserts a usage row for a completed request.
func (r *UsageRepo) Record(ctx context.Context, u UsageRecord) error {
	if err := insertUsage(ctx, r.db.sql, r.db.rebind, u); err != nil {
		return fmt.Errorf("store: record usage: %w", err)
	}
	return nil
}

// RecordBatch inserts multiple usage rows in one transaction. It is compatible
// with both SQLite and Postgres because placeholders are rebound through DB.
func (r *UsageRepo) RecordBatch(ctx context.Context, records []UsageRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin usage batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const cols = 29
	var b strings.Builder
	b.WriteString(`INSERT INTO usage_records
		(id, tenant_id, project_id, api_key_id, provider, model, account_id, client,
		 prompt_tokens, completion_tokens, cached_tokens, cache_write_tokens,
		 cost_micros, cache_hit, latency_ms, ttft_ms,
		 slim_bytes_saved, slim_tokens_saved, slim_rules, caveman_active, terse_active,
		 headroom_active, headroom_tokens_before, headroom_tokens_after, headroom_tokens_saved,
		 headroom_compression_ratio, headroom_transforms, headroom_ccr_hashes,
		 created_at) VALUES `)
	args := make([]any, 0, len(records)*cols)
	for i, u := range records {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args, usageArgs(u)...)
	}
	q := r.db.rebind(b.String())
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store: record usage batch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit usage batch: %w", err)
	}
	return nil
}

func insertUsage(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, rebind func(string) string, u UsageRecord) error {
	q := rebind(`
		INSERT INTO usage_records
			(id, tenant_id, project_id, api_key_id, provider, model, account_id, client,
			 prompt_tokens, completion_tokens, cached_tokens, cache_write_tokens,
			 cost_micros, cache_hit, latency_ms, ttft_ms,
			 slim_bytes_saved, slim_tokens_saved, slim_rules, caveman_active, terse_active,
			 headroom_active, headroom_tokens_before, headroom_tokens_after, headroom_tokens_saved,
			 headroom_compression_ratio, headroom_transforms, headroom_ccr_hashes,
			 created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := exec.ExecContext(ctx, q, usageArgs(u)...)
	return err
}

func usageArgs(u UsageRecord) []any {
	return []any{
		u.ID, u.TenantID, nullString(u.ProjectID), nullString(u.APIKeyID),
		u.Provider, u.Model, nullString(u.AccountID), u.Client,
		u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.CacheWriteTokens,
		u.CostMicros, boolToInt(u.CacheHit), u.LatencyMS, u.TTFTMS,
		u.SlimBytesSaved, u.SlimTokensSaved, u.SlimRules, boolToInt(u.CavemanActive), boolToInt(u.TerseActive),
		boolToInt(u.HeadroomActive), u.HeadroomTokensBefore, u.HeadroomTokensAfter, u.HeadroomTokensSaved,
		u.HeadroomCompressionRatio, u.HeadroomTransforms, u.HeadroomCCRHashes,
		formatTime(u.CreatedAt),
	}
}

// SpendSince returns total cost in micros for a budget scope since the given
// time. Used by the budget engine to enforce hard limits.
func (r *UsageRepo) SpendSince(ctx context.Context, scope BudgetScope, scopeID string, since time.Time) (int64, error) {
	column := scopeColumn(scope)
	if column == "" {
		return 0, fmt.Errorf("store: unknown budget scope %q", scope)
	}

	q := r.db.rebind(fmt.Sprintf(
		`SELECT COALESCE(SUM(cost_micros), 0) FROM usage_records WHERE %s = ? AND created_at >= ?`,
		column))
	var total int64
	if err := r.db.sql.QueryRowContext(ctx, q, scopeID, formatTime(since)).Scan(&total); err != nil {
		return 0, fmt.Errorf("store: spend since: %w", err)
	}
	return total, nil
}

// SpendAndTokens returns both cost (micros) and total tokens consumed for a
// budget scope since the given time. This is the combined query used by the
// budget engine to check USD and token limits in a single round trip.
func (r *UsageRepo) SpendAndTokens(ctx context.Context, scope BudgetScope, scopeID string, since time.Time) (costMicros int64, tokens int64, err error) {
	column := scopeColumn(scope)
	if column == "" {
		return 0, 0, fmt.Errorf("store: unknown budget scope %q", scope)
	}

	q := r.db.rebind(fmt.Sprintf(
		`SELECT COALESCE(SUM(cost_micros), 0), COALESCE(SUM(prompt_tokens + completion_tokens), 0)
		 FROM usage_records WHERE %s = ? AND created_at >= ?`,
		column))
	if err := r.db.sql.QueryRowContext(ctx, q, scopeID, formatTime(since)).Scan(&costMicros, &tokens); err != nil {
		return 0, 0, fmt.Errorf("store: spend and tokens: %w", err)
	}
	return costMicros, tokens, nil
}

// SpendScope identifies a scope for batch spend queries.
type SpendScope struct {
	Kind    BudgetScope
	ScopeID string
	Since   time.Time
}

// SpendResult holds the result of a single scope's spend query.
type SpendResult struct {
	CostMicros int64
	Tokens     int64
}

// SpendAndTokensBatch fetches spend+tokens for multiple scopes in a single
// SQL round-trip using UNION ALL. This eliminates N sequential queries when
// the budget engine checks key+project+tenant scopes per request.
func (r *UsageRepo) SpendAndTokensBatch(ctx context.Context, scopes []SpendScope) ([]SpendResult, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	results := make([]SpendResult, len(scopes))

	// Build UNION ALL query: one SELECT per scope.
	var query string
	args := make([]any, 0, len(scopes)*2)
	for i, s := range scopes {
		column := scopeColumn(s.Kind)
		if column == "" {
			return nil, fmt.Errorf("store: unknown budget scope %q", s.Kind)
		}
		if i > 0 {
			query += " UNION ALL "
		}
		query += fmt.Sprintf(
			"SELECT COALESCE(SUM(cost_micros), 0), COALESCE(SUM(prompt_tokens + completion_tokens), 0) FROM usage_records WHERE %s = ? AND created_at >= ?",
			column)
		args = append(args, s.ScopeID, formatTime(s.Since))
	}
	query = r.db.rebind(query)

	rows, err := r.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: spend batch: %w", err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() && i < len(results) {
		if err := rows.Scan(&results[i].CostMicros, &results[i].Tokens); err != nil {
			return nil, fmt.Errorf("store: spend batch scan: %w", err)
		}
		i++
	}
	return results, rows.Err()
}

// scopeColumn maps a budget scope to its SQL column name.
func scopeColumn(scope BudgetScope) string {
	switch scope {
	case ScopeTenant:
		return "tenant_id"
	case ScopeProject:
		return "project_id"
	case ScopeAPIKey:
		return "api_key_id"
	default:
		return ""
	}
}

// Summary aggregates usage for a tenant over a time window.
type Summary struct {
	TotalRequests    int64
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	CacheWriteTokens int64
	CostMicros       int64
	CacheHits        int64
	AvgTTFTMS        int64 // average time-to-first-token for streaming requests
	AvgLatencyMS     int64 // average latency for non-cache requests
	SuccessCount     int64 // requests that succeeded (latency > 0 or cache hit)
	SlimBytesSaved   int64 // total bytes saved by RTK slimmer
	SlimTokensSaved  int64 // total estimated tokens saved by RTK
	HeadroomRequests int64 // requests where Headroom compressed input
	HeadroomTokensSaved int64 // exact tokens saved by Headroom
	CavemanRequests  int64 // requests where caveman was active
	TerseRequests    int64 // requests where terse was active
}

// Summarize returns aggregate usage for a tenant since the given time.
func (r *UsageRepo) Summarize(ctx context.Context, tenantID string, since time.Time) (Summary, error) {
	q := r.db.rebind(`
		SELECT
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0),
			COALESCE(SUM(cost_micros), 0),
			COALESCE(SUM(cache_hit), 0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms > 0 THEN ttft_ms END) AS INTEGER), 0),
			COALESCE(CAST(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END) AS INTEGER), 0),
			COUNT(CASE WHEN latency_ms > 0 OR cache_hit = 1 THEN 1 END),
			COALESCE(SUM(slim_bytes_saved), 0),
			COALESCE(SUM(slim_tokens_saved), 0),
			COALESCE(SUM(headroom_active), 0),
			COALESCE(SUM(headroom_tokens_saved), 0),
			COALESCE(SUM(caveman_active), 0),
			COALESCE(SUM(terse_active), 0)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ?`)
	var s Summary
	err := r.db.sql.QueryRowContext(ctx, q, tenantID, formatTime(since)).Scan(
		&s.TotalRequests, &s.PromptTokens, &s.CompletionTokens,
		&s.CachedTokens, &s.CacheWriteTokens, &s.CostMicros, &s.CacheHits, &s.AvgTTFTMS,
		&s.AvgLatencyMS, &s.SuccessCount,
		&s.SlimBytesSaved, &s.SlimTokensSaved, &s.HeadroomRequests, &s.HeadroomTokensSaved, &s.CavemanRequests, &s.TerseRequests)
	if err != nil {
		return Summary{}, fmt.Errorf("store: summarize usage: %w", err)
	}
	return s, nil
}

// SummarizeByKey returns aggregate usage for a specific API key since the given
// time. This scopes stats to the individual key rather than the whole tenant.
func (r *UsageRepo) SummarizeByKey(ctx context.Context, keyID string, since time.Time) (Summary, error) {
	q := r.db.rebind(`
		SELECT
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0),
			COALESCE(SUM(cost_micros), 0),
			COALESCE(SUM(cache_hit), 0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms > 0 THEN ttft_ms END) AS INTEGER), 0),
			COALESCE(CAST(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END) AS INTEGER), 0),
			COUNT(CASE WHEN latency_ms > 0 OR cache_hit = 1 THEN 1 END),
			COALESCE(SUM(slim_bytes_saved), 0),
			COALESCE(SUM(slim_tokens_saved), 0),
			COALESCE(SUM(headroom_active), 0),
			COALESCE(SUM(headroom_tokens_saved), 0),
			COALESCE(SUM(caveman_active), 0),
			COALESCE(SUM(terse_active), 0)
		FROM usage_records
		WHERE api_key_id = ? AND created_at >= ?`)
	var s Summary
	err := r.db.sql.QueryRowContext(ctx, q, keyID, formatTime(since)).Scan(
		&s.TotalRequests, &s.PromptTokens, &s.CompletionTokens,
		&s.CachedTokens, &s.CacheWriteTokens, &s.CostMicros, &s.CacheHits, &s.AvgTTFTMS,
		&s.AvgLatencyMS, &s.SuccessCount,
		&s.SlimBytesSaved, &s.SlimTokensSaved, &s.HeadroomRequests, &s.HeadroomTokensSaved, &s.CavemanRequests, &s.TerseRequests)
	if err != nil {
		return Summary{}, fmt.Errorf("store: summarize usage by key: %w", err)
	}
	return s, nil
}

// ProviderUsage aggregates usage for a single upstream provider. It powers the
// routing-activity map and per-provider breakdown on the Usage page.
type ProviderUsage struct {
	Provider         string
	TotalRequests    int64
	PromptTokens     int64
	CompletionTokens int64
	CostMicros       int64
}

// Breakdown returns per-provider aggregate usage for a tenant since the given
// time, ordered by request volume (busiest first).
func (r *UsageRepo) Breakdown(ctx context.Context, tenantID string, since time.Time) ([]ProviderUsage, error) {
	q := r.db.rebind(`
		SELECT
			provider,
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cost_micros), 0)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ?
		GROUP BY provider
		ORDER BY COUNT(*) DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: usage breakdown: %w", err)
	}
	defer rows.Close()

	var out []ProviderUsage
	for rows.Next() {
		var p ProviderUsage
		if err := rows.Scan(&p.Provider, &p.TotalRequests, &p.PromptTokens, &p.CompletionTokens, &p.CostMicros); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecentRecord is a single recent request row for the activity feed and console.
type RecentRecord struct {
	ID               string
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	CacheWriteTokens int
	CostMicros       int64
	CacheHit         bool
	LatencyMS        int
	TTFTMS           int    // time-to-first-token in ms (0 for non-streaming)
	SlimBytesSaved   int    // bytes saved by RTK slimmer
	SlimTokensSaved  int    // estimated tokens saved by RTK
	SlimRules        string // comma-separated rule names that fired
	HeadroomActive   bool
	HeadroomTokensSaved int
	HeadroomTransforms string
	CavemanActive    bool
	TerseActive      bool
	CreatedAt        time.Time
}

// Recent returns the most recent usage records for a tenant, newest first.
func (r *UsageRepo) Recent(ctx context.Context, tenantID string, limit int) ([]RecentRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := r.db.rebind(`
		SELECT id, provider, model, prompt_tokens, completion_tokens, cached_tokens,
		       cache_write_tokens, cost_micros, cache_hit, latency_ms, ttft_ms,
		       slim_bytes_saved, slim_tokens_saved, slim_rules,
		       headroom_active, headroom_tokens_saved, headroom_transforms,
		       caveman_active, terse_active,
		       created_at
		FROM usage_records
		WHERE tenant_id = ?
		ORDER BY created_at DESC
		LIMIT ?`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent usage: %w", err)
	}
	defer rows.Close()

	var out []RecentRecord
	for rows.Next() {
		var (
			rec                      RecentRecord
			cacheHit, caveman, terse, headroom int
			createdAt                string
		)
		if err := rows.Scan(&rec.ID, &rec.Provider, &rec.Model, &rec.PromptTokens,
			&rec.CompletionTokens, &rec.CachedTokens, &rec.CacheWriteTokens,
			&rec.CostMicros, &cacheHit, &rec.LatencyMS, &rec.TTFTMS,
			&rec.SlimBytesSaved, &rec.SlimTokensSaved, &rec.SlimRules,
			&headroom, &rec.HeadroomTokensSaved, &rec.HeadroomTransforms, &caveman, &terse,
			&createdAt); err != nil {
			return nil, err
		}
		rec.CacheHit = cacheHit != 0
		rec.HeadroomActive = headroom != 0
		rec.CavemanActive = caveman != 0
		rec.TerseActive = terse != 0
		rec.CreatedAt = parseTime(createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// AccountUsage aggregates usage keyed by upstream account. It powers the quota
// tracker, which shows how much each connected account has consumed.
type AccountUsage struct {
	AccountID        string
	TotalRequests    int64
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	CacheWriteTokens int64
	CostMicros       int64
}

// ByAccount returns per-account aggregate usage for a tenant since the given
// time. Records with no associated account are grouped under an empty id.
func (r *UsageRepo) ByAccount(ctx context.Context, tenantID string, since time.Time) ([]AccountUsage, error) {
	q := r.db.rebind(`
		SELECT
			COALESCE(account_id, ''),
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0),
			COALESCE(SUM(cost_micros), 0)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ?
		GROUP BY account_id`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: usage by account: %w", err)
	}
	defer rows.Close()

	var out []AccountUsage
	for rows.Next() {
		var a AccountUsage
		if err := rows.Scan(&a.AccountID, &a.TotalRequests, &a.PromptTokens,
			&a.CompletionTokens, &a.CachedTokens, &a.CacheWriteTokens, &a.CostMicros); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ModelUsage aggregates usage for a single provider+model pair. It powers the
// per-model breakdown table on the Usage page.
type ModelUsage struct {
	Provider         string
	Model            string
	TotalRequests    int64
	PromptTokens     int64
	CompletionTokens int64
	CostMicros       int64
}

// ByModel returns per-model aggregate usage for a tenant since the given time,
// ordered by request volume (busiest first).
func (r *UsageRepo) ByModel(ctx context.Context, tenantID string, since time.Time) ([]ModelUsage, error) {
	q := r.db.rebind(`
		SELECT
			provider,
			model,
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cost_micros), 0)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ?
		GROUP BY provider, model
		ORDER BY COUNT(*) DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: usage by model: %w", err)
	}
	defer rows.Close()

	var out []ModelUsage
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Provider, &m.Model, &m.TotalRequests, &m.PromptTokens, &m.CompletionTokens, &m.CostMicros); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ByModelByKey returns per-model aggregate usage for a specific API key since
// the given time, ordered by request volume (busiest first). Used by the portal
// to show per-model breakdown.
func (r *UsageRepo) ByModelByKey(ctx context.Context, keyID string, since time.Time) ([]ModelUsage, error) {
	q := r.db.rebind(`
		SELECT
			provider,
			model,
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cost_micros), 0)
		FROM usage_records
		WHERE api_key_id = ? AND created_at >= ?
		GROUP BY provider, model
		ORDER BY COUNT(*) DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, keyID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: usage by model for key: %w", err)
	}
	defer rows.Close()

	var out []ModelUsage
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Provider, &m.Model, &m.TotalRequests, &m.PromptTokens, &m.CompletionTokens, &m.CostMicros); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// TimePoint is the created_at + cost of a single record, used to build the
// activity-over-time series in the handler (bucketed in Go for engine
// portability).
// TimeBucket represents an aggregated count of events for a specific time slice.
type TimeBucket struct {
	Bucket int
	Count  int64
}

// Timeline returns aggregated usage grouped into 'buckets' even time slices between
// 'since' and 'to'. This delegates the bucketing to SQLite instead of fetching
// thousands of rows into Go memory.
func (r *UsageRepo) Timeline(ctx context.Context, tenantID string, since time.Time, to time.Time, buckets int) ([]TimeBucket, error) {
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

	epochCreated := r.db.epochExpr("created_at")
	epochSince := r.db.epochExpr("?")
	q := r.db.rebind(fmt.Sprintf(`
		SELECT
			CAST((%s - %s) / ? AS INTEGER) as bucket,
			COUNT(*) as count
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ? AND created_at <= ?
		GROUP BY bucket
		ORDER BY bucket ASC`, epochCreated, epochSince))

	rows, err := r.db.sql.QueryContext(ctx, q, formatTime(since), slotSecs, tenantID, formatTime(since), formatTime(to))
	if err != nil {
		return nil, fmt.Errorf("store: usage timeline: %w", err)
	}
	defer rows.Close()

	var out []TimeBucket
	for rows.Next() {
		var tb TimeBucket
		if err := rows.Scan(&tb.Bucket, &tb.Count); err != nil {
			return nil, err
		}
		if tb.Bucket >= 0 && tb.Bucket < buckets {
			out = append(out, tb)
		}
	}
	return out, rows.Err()
}

// DailyPoint represents one day of usage for a specific API key.
type DailyPoint struct {
	Date             string `json:"date"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CostMicros       int64  `json:"cost_micros"`
}

// DailyByKey returns per-day usage breakdown for a specific API key since the
// given time. Used by the customer portal to show usage trends.
func (r *UsageRepo) DailyByKey(ctx context.Context, keyID string, since time.Time) ([]DailyPoint, error) {
	dayExpr := r.db.dateExpr("created_at")
	q := r.db.rebind(fmt.Sprintf(`
		SELECT
			%s as day,
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cost_micros), 0)
		FROM usage_records
		WHERE api_key_id = ? AND created_at >= ?
		GROUP BY day
		ORDER BY day ASC`, dayExpr))
	rows, err := r.db.sql.QueryContext(ctx, q, keyID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: daily usage by key: %w", err)
	}
	defer rows.Close()

	var out []DailyPoint
	for rows.Next() {
		var dp DailyPoint
		if err := rows.Scan(&dp.Date, &dp.Requests, &dp.PromptTokens, &dp.CompletionTokens, &dp.CostMicros); err != nil {
			return nil, err
		}
		out = append(out, dp)
	}
	return out, rows.Err()
}

// RuleSavings aggregates savings per RTK slimmer rule.
type RuleSavings struct {
	Rule       string
	Count      int64 // number of requests where this rule fired
	BytesSaved int64 // total bytes saved by this rule
}

// SavingsByRule returns per-rule RTK savings aggregated from the slim_rules
// column. Since rules are stored as comma-separated strings, we parse them in
// Go rather than in SQL for engine portability.
func (r *UsageRepo) SavingsByRule(ctx context.Context, tenantID string, since time.Time) ([]RuleSavings, error) {
	q := r.db.rebind(`
		SELECT slim_rules, slim_bytes_saved
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ? AND slim_rules != ''`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: savings by rule: %w", err)
	}
	defer rows.Close()

	totals := map[string]*RuleSavings{}
	for rows.Next() {
		var rules string
		var bytesSaved int64
		if err := rows.Scan(&rules, &bytesSaved); err != nil {
			return nil, err
		}
		parts := splitRules(rules)
		if len(parts) == 0 {
			continue
		}
		perRule := bytesSaved / int64(len(parts))
		for _, name := range parts {
			if _, ok := totals[name]; !ok {
				totals[name] = &RuleSavings{Rule: name}
			}
			totals[name].Count++
			totals[name].BytesSaved += perRule
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]RuleSavings, 0, len(totals))
	for _, rs := range totals {
		out = append(out, *rs)
	}
	// Sort by bytes saved descending.
	sort.Slice(out, func(i, j int) bool {
		return out[i].BytesSaved > out[j].BytesSaved
	})
	return out, nil
}

// ClientSavings aggregates token-saving optimization results per calling
// client (claude-code, codex, cline, ...). It answers "which tools benefit
// from optimization, and by how much" without locking attribution to any
// specific tool — every client that produces savings appears here.
type ClientSavings struct {
	Client          string
	Requests        int64 // requests from this client in the window
	SlimBytesSaved  int64 // bytes saved by RTK slimmer
	SlimTokensSaved int64 // estimated tokens saved by RTK (bytes/4)
	HeadroomRequests int64 // requests where Headroom compressed input
	HeadroomTokensSaved int64 // exact tokens saved by Headroom
	CavemanRequests int64 // requests where caveman was active
	TerseRequests   int64 // requests where terse was active
}

// SavingsByClient returns per-client optimization savings for a tenant since
// the given time, ordered by tokens saved (most impactful first). Records with
// no detected client are grouped under "unknown".
func (r *UsageRepo) SavingsByClient(ctx context.Context, tenantID string, since time.Time) ([]ClientSavings, error) {
	q := r.db.rebind(`
		SELECT
			CASE WHEN client IS NULL OR client = '' THEN 'unknown' ELSE client END AS client,
			COUNT(*),
			COALESCE(SUM(slim_bytes_saved), 0),
			COALESCE(SUM(slim_tokens_saved), 0),
			COALESCE(SUM(headroom_active), 0),
			COALESCE(SUM(headroom_tokens_saved), 0),
			COALESCE(SUM(caveman_active), 0),
			COALESCE(SUM(terse_active), 0)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ?
		GROUP BY client
		ORDER BY COALESCE(SUM(slim_tokens_saved + headroom_tokens_saved), 0) DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: savings by client: %w", err)
	}
	defer rows.Close()

	var out []ClientSavings
	for rows.Next() {
		var c ClientSavings
		if err := rows.Scan(&c.Client, &c.Requests, &c.SlimBytesSaved,
			&c.SlimTokensSaved, &c.HeadroomRequests, &c.HeadroomTokensSaved, &c.CavemanRequests, &c.TerseRequests); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// HeadroomTransformSavings aggregates exact Headroom token savings by transform.
type HeadroomTransformSavings struct {
	Transform   string
	Count       int64
	TokensSaved int64
}

// SavingsByHeadroomTransform returns per-transform savings from Headroom.
func (r *UsageRepo) SavingsByHeadroomTransform(ctx context.Context, tenantID string, since time.Time) ([]HeadroomTransformSavings, error) {
	q := r.db.rebind(`
		SELECT headroom_transforms, headroom_tokens_saved
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ? AND headroom_tokens_saved > 0`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: headroom savings by transform: %w", err)
	}
	defer rows.Close()

	totals := map[string]*HeadroomTransformSavings{}
	for rows.Next() {
		var transforms string
		var tokensSaved int64
		if err := rows.Scan(&transforms, &tokensSaved); err != nil {
			return nil, err
		}
		parts := splitRules(transforms)
		if len(parts) == 0 {
			parts = []string{"headroom"}
		}
		perTransform := tokensSaved / int64(len(parts))
		for _, name := range parts {
			if _, ok := totals[name]; !ok {
				totals[name] = &HeadroomTransformSavings{Transform: name}
			}
			totals[name].Count++
			totals[name].TokensSaved += perTransform
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]HeadroomTransformSavings, 0, len(totals))
	for _, rs := range totals {
		out = append(out, *rs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TokensSaved > out[j].TokensSaved })
	return out, nil
}

// splitRules splits a comma-separated rule string into individual names,
// trimming whitespace and skipping empties.
func splitRules(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := s[start:i]
			// trim inline
			for len(part) > 0 && part[0] == ' ' {
				part = part[1:]
			}
			for len(part) > 0 && part[len(part)-1] == ' ' {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
