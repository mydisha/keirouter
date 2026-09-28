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

	// PostgreSQL accepts at most 65,535 bind parameters; SQLite builds used by
	// KeiRouter allow 32,766. Keep each statement below both limits while one
	// transaction preserves all-or-nothing batch semantics.
	const (
		postgresBindLimit = 65535
		sqliteBindLimit   = 32766
	)
	argsPerRow := len(usageArgs(UsageRecord{}))
	maxRows := sqliteBindLimit / argsPerRow
	if r.db.dialect == DialectPostgres {
		maxRows = postgresBindLimit / argsPerRow
	}
	if maxRows < 1 {
		maxRows = 1
	}
	for start := 0; start < len(records); start += maxRows {
		end := min(start+maxRows, len(records))
		if err := insertUsageBatch(ctx, tx, r.db.rebind, records[start:end]); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit usage batch: %w", err)
	}
	return nil
}

const usageColumns = `id, request_id, tenant_id, project_id, api_key_id, provider, model, account_id, client,
	status, error_kind, prompt_tokens, completion_tokens, cached_tokens, cache_write_tokens,
	reasoning_tokens, usage_source, cost_micros, cost_nanos, input_cost_nanos, cached_cost_nanos,
	cache_write_cost_nanos, output_cost_nanos, reasoning_cost_nanos, avoided_cost_nanos, saved_cost_nanos,
	pricing_status, pricing_source, pricing_key, pricing_match_kind, pricing_source_url,
	pricing_as_of, pricing_backfilled, input_rate_per_m, cached_rate_per_m,
	cache_write_rate_per_m, output_rate_per_m, reasoning_rate_per_m, cache_hit, latency_ms,
	upstream_latency_ms, end_to_end_latency_ms, ttft_ms, slim_bytes_saved, slim_tokens_saved,
	slim_rules, slim_active, caveman_active, terse_active, headroom_tokens_saved,
	headroom_bytes_saved, headroom_active, ponytail_active, chain_id, created_at`

func insertUsageBatch(ctx context.Context, tx *sql.Tx, rebind func(string) string, records []UsageRecord) error {
	argsPerRow := len(usageArgs(UsageRecord{}))
	rowPlaceholders := "(" + strings.TrimSuffix(strings.Repeat("?,", argsPerRow), ",") + ")"
	var b strings.Builder
	b.WriteString("INSERT INTO usage_records (")
	b.WriteString(usageColumns)
	b.WriteString(") VALUES ")
	args := make([]any, 0, len(records)*argsPerRow)
	for i, u := range records {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(rowPlaceholders)
		args = append(args, usageArgs(u)...)
	}
	if _, err := tx.ExecContext(ctx, rebind(b.String()), args...); err != nil {
		return fmt.Errorf("store: record usage batch: %w", err)
	}
	return nil
}

func insertUsage(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, rebind func(string) string, u UsageRecord) error {
	args := usageArgs(u)
	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
	q := rebind("INSERT INTO usage_records (" + usageColumns + ") VALUES " + placeholders)
	_, err := exec.ExecContext(ctx, q, args...)
	return err
}

func usageArgs(u UsageRecord) []any {
	// Keep legacy direct callers lossless while cost_nanos is the authoritative
	// storage unit. Rounding happens once at the compatibility boundary.
	if u.CostNanos == 0 && u.CostMicros != 0 {
		u.CostNanos = u.CostMicros * 1000
	}
	if u.CostMicros == 0 && u.CostNanos != 0 {
		u.CostMicros = (u.CostNanos + 500) / 1000
	}
	return []any{
		u.ID, u.RequestID, u.TenantID, nullString(u.ProjectID), nullString(u.APIKeyID),
		u.Provider, u.Model, nullString(u.AccountID), u.Client, u.Status, u.ErrorKind,
		u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.CacheWriteTokens,
		u.ReasoningTokens, u.UsageSource, u.CostMicros, u.CostNanos,
		u.InputCostNanos, u.CachedCostNanos, u.CacheWriteCostNanos, u.OutputCostNanos,
		u.ReasoningCostNanos, u.AvoidedCostNanos, u.SavedCostNanos,
		u.PricingStatus, u.PricingSource, u.PricingKey, u.PricingMatchKind, u.PricingSourceURL,
		nullTime(u.PricingAsOf), boolToInt(u.PricingBackfilled),
		u.InputRatePerM, u.CachedRatePerM, u.CacheWriteRatePerM, u.OutputRatePerM, u.ReasoningRatePerM,
		boolToInt(u.CacheHit), u.LatencyMS, u.UpstreamLatencyMS, u.EndToEndLatencyMS, u.TTFTMS,
		u.SlimBytesSaved, u.SlimTokensSaved, u.SlimRules,
		boolToInt(u.SlimActive), boolToInt(u.CavemanActive), boolToInt(u.TerseActive),
		u.HeadroomTokensSaved, u.HeadroomBytesSaved, boolToInt(u.HeadroomActive), boolToInt(u.PonytailActive),
		u.ChainID, formatTime(u.CreatedAt),
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
		`SELECT (CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000 FROM usage_records WHERE %s = ? AND created_at >= ?`,
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
		`SELECT (CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000, COALESCE(SUM(prompt_tokens + completion_tokens), 0)
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
			"SELECT (CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000, COALESCE(SUM(prompt_tokens + completion_tokens), 0) FROM usage_records WHERE %s = ? AND created_at >= ?",
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
	CavemanRequests  int64 // requests where caveman was active
	TerseRequests    int64 // requests where terse was active

	HeadroomTokensSaved int64 // SUM(headroom_tokens_saved)
	HeadroomRequests    int64 // SUM(headroom_active)
	PonytailRequests    int64 // SUM(ponytail_active)
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000,
			COALESCE(SUM(cache_hit), 0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms > 0 THEN ttft_ms END) AS INTEGER), 0),
			COALESCE(CAST(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END) AS INTEGER), 0),
			COUNT(CASE WHEN latency_ms > 0 OR cache_hit = 1 THEN 1 END),
			COALESCE(SUM(slim_bytes_saved), 0),
			COALESCE(SUM(slim_tokens_saved), 0),
			COALESCE(SUM(caveman_active), 0),
			COALESCE(SUM(terse_active), 0),
			COALESCE(SUM(headroom_tokens_saved), 0),
			COALESCE(SUM(headroom_active), 0),
			COALESCE(SUM(ponytail_active), 0)
		FROM usage_records
		WHERE tenant_id = ? AND created_at >= ?`)
	var s Summary
	err := r.db.sql.QueryRowContext(ctx, q, tenantID, formatTime(since)).Scan(
		&s.TotalRequests, &s.PromptTokens, &s.CompletionTokens,
		&s.CachedTokens, &s.CacheWriteTokens, &s.CostMicros, &s.CacheHits, &s.AvgTTFTMS,
		&s.AvgLatencyMS, &s.SuccessCount,
		&s.SlimBytesSaved, &s.SlimTokensSaved, &s.CavemanRequests, &s.TerseRequests,
		&s.HeadroomTokensSaved, &s.HeadroomRequests, &s.PonytailRequests)
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000,
			COALESCE(SUM(cache_hit), 0),
			COALESCE(CAST(AVG(CASE WHEN ttft_ms > 0 THEN ttft_ms END) AS INTEGER), 0),
			COALESCE(CAST(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END) AS INTEGER), 0),
			COUNT(CASE WHEN latency_ms > 0 OR cache_hit = 1 THEN 1 END),
			COALESCE(SUM(slim_bytes_saved), 0),
			COALESCE(SUM(slim_tokens_saved), 0),
			COALESCE(SUM(caveman_active), 0),
			COALESCE(SUM(terse_active), 0),
			COALESCE(SUM(headroom_tokens_saved), 0),
			COALESCE(SUM(headroom_active), 0),
			COALESCE(SUM(ponytail_active), 0)
		FROM usage_records
		WHERE api_key_id = ? AND created_at >= ?`)
	var s Summary
	err := r.db.sql.QueryRowContext(ctx, q, keyID, formatTime(since)).Scan(
		&s.TotalRequests, &s.PromptTokens, &s.CompletionTokens,
		&s.CachedTokens, &s.CacheWriteTokens, &s.CostMicros, &s.CacheHits, &s.AvgTTFTMS,
		&s.AvgLatencyMS, &s.SuccessCount,
		&s.SlimBytesSaved, &s.SlimTokensSaved, &s.CavemanRequests, &s.TerseRequests,
		&s.HeadroomTokensSaved, &s.HeadroomRequests, &s.PonytailRequests)
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000
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
	SlimActive       bool   // RTK slimmer was enabled for this request
	CavemanActive    bool
	TerseActive      bool

	HeadroomTokensSaved int  // tokens saved by Headroom (non-phantom)
	HeadroomBytesSaved  int  // bytes saved by Headroom
	HeadroomActive      bool // Headroom produced non-phantom savings
	PonytailActive      bool // Ponytail injection was active

	CreatedAt time.Time
}

// Recent returns the most recent usage records for a tenant, newest first.
func (r *UsageRepo) Recent(ctx context.Context, tenantID string, limit int) ([]RecentRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := r.db.rebind(`
		SELECT id, provider, model, prompt_tokens, completion_tokens, cached_tokens,
		       cache_write_tokens, cost_micros, cache_hit, latency_ms, ttft_ms,
		       slim_bytes_saved, slim_tokens_saved, slim_rules, slim_active, caveman_active, terse_active,
		       headroom_tokens_saved, headroom_bytes_saved, headroom_active, ponytail_active,
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
			rec                            RecentRecord
			cacheHit, caveman, terse       int
			headroomActive, ponytailActive int
			slimActive                     int
			createdAt                      string
		)
		if err := rows.Scan(&rec.ID, &rec.Provider, &rec.Model, &rec.PromptTokens,
			&rec.CompletionTokens, &rec.CachedTokens, &rec.CacheWriteTokens,
			&rec.CostMicros, &cacheHit, &rec.LatencyMS, &rec.TTFTMS,
			&rec.SlimBytesSaved, &rec.SlimTokensSaved, &rec.SlimRules, &slimActive, &caveman, &terse,
			&rec.HeadroomTokensSaved, &rec.HeadroomBytesSaved, &headroomActive, &ponytailActive,
			&createdAt); err != nil {
			return nil, err
		}
		rec.CacheHit = cacheHit != 0
		rec.SlimActive = slimActive != 0
		rec.CavemanActive = caveman != 0
		rec.TerseActive = terse != 0
		rec.HeadroomActive = headroomActive != 0
		rec.PonytailActive = ponytailActive != 0
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000
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
			(CAST(COALESCE(SUM(CASE WHEN pricing_backfilled=0 THEN cost_nanos ELSE 0 END), 0) AS BIGINT) + 500) / 1000
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
	CavemanRequests int64 // requests where caveman was active
	TerseRequests   int64 // requests where terse was active

	HeadroomTokensSaved int64 // tokens saved by Headroom
	PonytailRequests    int64 // requests where Ponytail was active
	OptimizedRequests   int64 // distinct rows where any optimization was active
	SavedCostNanos      int64 // input compression savings from immutable snapshots
	AvoidedCostNanos    int64 // semantic-cache avoided cost from immutable snapshots
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
			COALESCE(SUM(caveman_active), 0),
			COALESCE(SUM(terse_active), 0),
			COALESCE(SUM(headroom_tokens_saved), 0),
			COALESCE(SUM(ponytail_active), 0),
			COALESCE(SUM(CASE WHEN slim_active=1 OR headroom_active=1 OR caveman_active=1 OR terse_active=1 OR ponytail_active=1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(saved_cost_nanos), 0),
			COALESCE(SUM(avoided_cost_nanos), 0)
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
			&c.SlimTokensSaved, &c.CavemanRequests, &c.TerseRequests,
			&c.HeadroomTokensSaved, &c.PonytailRequests, &c.OptimizedRequests,
			&c.SavedCostNanos, &c.AvoidedCostNanos); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecentByKey returns the most recent usage records for a specific API key
// since the given time, newest first. Used by the customer portal to show
// per-request details including token in/out and optimization flags.
func (r *UsageRepo) RecentByKey(ctx context.Context, keyID string, since time.Time, limit int) ([]RecentRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.db.rebind(`
		SELECT id, provider, model, prompt_tokens, completion_tokens, cached_tokens,
		       cache_write_tokens, cost_micros, cache_hit, latency_ms, ttft_ms,
		       slim_bytes_saved, slim_tokens_saved, slim_rules, slim_active, caveman_active, terse_active,
		       headroom_tokens_saved, headroom_bytes_saved, headroom_active, ponytail_active,
		       created_at
		FROM usage_records
		WHERE api_key_id = ? AND created_at >= ?
		ORDER BY created_at DESC
		LIMIT ?`)
	rows, err := r.db.sql.QueryContext(ctx, q, keyID, formatTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent usage by key: %w", err)
	}
	defer rows.Close()
	var out []RecentRecord
	for rows.Next() {
		var (
			rec                            RecentRecord
			cacheHit, caveman, terse       int
			headroomActive, ponytailActive int
			slimActive                     int
			createdAt                      string
		)
		if err := rows.Scan(&rec.ID, &rec.Provider, &rec.Model, &rec.PromptTokens,
			&rec.CompletionTokens, &rec.CachedTokens, &rec.CacheWriteTokens,
			&rec.CostMicros, &cacheHit, &rec.LatencyMS, &rec.TTFTMS,
			&rec.SlimBytesSaved, &rec.SlimTokensSaved, &rec.SlimRules, &slimActive, &caveman, &terse,
			&rec.HeadroomTokensSaved, &rec.HeadroomBytesSaved, &headroomActive, &ponytailActive,
			&createdAt); err != nil {
			return nil, err
		}
		rec.CacheHit = cacheHit != 0
		rec.SlimActive = slimActive != 0
		rec.CavemanActive = caveman != 0
		rec.TerseActive = terse != 0
		rec.HeadroomActive = headroomActive != 0
		rec.PonytailActive = ponytailActive != 0
		rec.CreatedAt = parseTime(createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
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
