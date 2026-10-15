package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ProviderHealthRepo persists actionable provider health state: current
// rolled-up status, historical snapshots, and synthetic probe results.
type ProviderHealthRepo struct{ db *DB }

// ProviderHealth returns the actionable provider health repository.
func (db *DB) ProviderHealth() *ProviderHealthRepo { return &ProviderHealthRepo{db: db} }

const providerHealthCurrentColumns = `id, provider, provider_account_id, model, capability,
	health_status, health_score, success_rate, error_rate, request_count,
	fallback_count, latency_p95_ms, ttft_p95_ms, consecutive_failures,
	main_issue, recommendation, last_success_at, last_failure_at,
	last_probe_at, last_updated_at`

// HealthKey builds the deterministic unique key for a health dimension tuple.
// Empty dimensions collapse to ” so SQLite and PostgreSQL treat NULLs
// identically under the UNIQUE constraint.
func HealthKey(provider, account, model, capability string) string {
	return provider + ":" + account + ":" + model + ":" + capability
}

// UpsertCurrent inserts or replaces a current health row keyed by health_key.
func (r *ProviderHealthRepo) UpsertCurrent(ctx context.Context, c ProviderHealthCurrent) error {
	if c.ID == "" {
		return fmt.Errorf("store: provider health current: missing id")
	}
	key := HealthKey(c.Provider, c.ProviderAccountID, c.Model, c.Capability)
	q := r.db.rebind(`INSERT INTO provider_health_current (` + providerHealthCurrentColumns + `, health_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(health_key) DO UPDATE SET
			id = excluded.id,
			provider = excluded.provider,
			provider_account_id = excluded.provider_account_id,
			model = excluded.model,
			capability = excluded.capability,
			health_status = excluded.health_status,
			health_score = excluded.health_score,
			success_rate = excluded.success_rate,
			error_rate = excluded.error_rate,
			request_count = excluded.request_count,
			fallback_count = excluded.fallback_count,
			latency_p95_ms = excluded.latency_p95_ms,
			ttft_p95_ms = excluded.ttft_p95_ms,
			consecutive_failures = excluded.consecutive_failures,
			main_issue = excluded.main_issue,
			recommendation = excluded.recommendation,
			last_success_at = excluded.last_success_at,
			last_failure_at = excluded.last_failure_at,
			last_probe_at = excluded.last_probe_at,
			last_updated_at = excluded.last_updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q,
		c.ID, c.Provider, c.ProviderAccountID, c.Model, c.Capability,
		c.HealthStatus, c.HealthScore, c.SuccessRate, c.ErrorRate, c.RequestCount,
		c.FallbackCount, nullIntPtr(c.LatencyP95Ms), nullIntPtr(c.TTFTP95Ms), c.ConsecutiveFailures,
		nullStringPtr(c.MainIssue), nullStringPtr(c.Recommendation),
		nullTime(c.LastSuccessAt), nullTime(c.LastFailureAt), nullTime(c.LastProbeAt),
		formatTime(c.LastUpdatedAt), key)
	if err != nil {
		return fmt.Errorf("store: upsert provider health current: %w", err)
	}
	return nil
}

// GetCurrent fetches one current health row by dimension tuple.
func (r *ProviderHealthRepo) GetCurrent(ctx context.Context, provider, account, model, capability string) (ProviderHealthCurrent, error) {
	q := r.db.rebind(`SELECT ` + providerHealthCurrentColumns + ` FROM provider_health_current WHERE health_key = ?`)
	key := HealthKey(provider, account, model, capability)
	row, err := scanProviderHealthCurrent(r.db.sql.QueryRowContext(ctx, q, key).Scan)
	if err == sql.ErrNoRows {
		return ProviderHealthCurrent{}, ErrNotFound
	}
	if err != nil {
		return ProviderHealthCurrent{}, fmt.Errorf("store: get provider health current: %w", err)
	}
	return row, nil
}

// ListCurrent returns all current health rows, optionally filtered by status.
func (r *ProviderHealthRepo) ListCurrent(ctx context.Context, status string) ([]ProviderHealthCurrent, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		q := r.db.rebind(`SELECT ` + providerHealthCurrentColumns + ` FROM provider_health_current WHERE health_status = ? ORDER BY provider, model`)
		rows, err = r.db.sql.QueryContext(ctx, q, status)
	} else {
		q := r.db.rebind(`SELECT ` + providerHealthCurrentColumns + ` FROM provider_health_current ORDER BY provider, model`)
		rows, err = r.db.sql.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list provider health current: %w", err)
	}
	defer rows.Close()
	var out []ProviderHealthCurrent
	for rows.Next() {
		h, err := scanProviderHealthCurrent(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListCurrentByProvider returns current health rows for one provider.
func (r *ProviderHealthRepo) ListCurrentByProvider(ctx context.Context, provider string) ([]ProviderHealthCurrent, error) {
	q := r.db.rebind(`SELECT ` + providerHealthCurrentColumns + ` FROM provider_health_current WHERE provider = ? ORDER BY model, provider_account_id`)
	rows, err := r.db.sql.QueryContext(ctx, q, provider)
	if err != nil {
		return nil, fmt.Errorf("store: list provider health current by provider: %w", err)
	}
	defer rows.Close()
	var out []ProviderHealthCurrent
	for rows.Next() {
		h, err := scanProviderHealthCurrent(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DeleteTrafficCurrent removes a traffic-derived current row only when it has
// not been refreshed after the supplied cutoff. Probe-only rows have zero
// requests and are intentionally preserved.
func (r *ProviderHealthRepo) DeleteTrafficCurrent(ctx context.Context, provider, account, model, capability string, updatedBefore time.Time) error {
	q := r.db.rebind(`DELETE FROM provider_health_current
		WHERE health_key = ? AND request_count > 0 AND last_updated_at <= ?`)
	if _, err := r.db.sql.ExecContext(ctx, q,
		HealthKey(provider, account, model, capability), formatTime(updatedBefore)); err != nil {
		return fmt.Errorf("store: delete provider health current: %w", err)
	}
	return nil
}

// DeleteStaleTrafficCurrent removes persisted traffic windows left behind by a
// previous process once their collector window has elapsed.
func (r *ProviderHealthRepo) DeleteStaleTrafficCurrent(ctx context.Context, updatedBefore time.Time) error {
	q := r.db.rebind(`DELETE FROM provider_health_current
		WHERE request_count > 0 AND last_updated_at < ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, formatTime(updatedBefore)); err != nil {
		return fmt.Errorf("store: delete stale provider health current: %w", err)
	}
	return nil
}

// UpdateProbeTimestamp marks the last probe time for a health key without
// clobbering traffic-derived fields. Used when real traffic already populates
// the row and a probe merely refreshes last_probe_at.
func (r *ProviderHealthRepo) UpdateProbeTimestamp(ctx context.Context, provider, account, model, capability string, at time.Time) error {
	key := HealthKey(provider, account, model, capability)
	q := r.db.rebind(`UPDATE provider_health_current SET last_probe_at = ?, last_updated_at = ? WHERE health_key = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, formatTime(at), formatTime(at), key)
	if err != nil {
		return fmt.Errorf("store: update probe timestamp: %w", err)
	}
	return nil
}

// InsertSnapshot writes one aggregated historical bucket. The bucket identity
// is its start time and provider dimensions; replacing that identity lets late
// telemetry correct a completed minute without creating duplicate snapshots.
func (r *ProviderHealthRepo) InsertSnapshot(ctx context.Context, s ProviderHealthSnapshot) error {
	if s.ID == "" {
		return fmt.Errorf("store: provider health snapshot: missing id")
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin provider health snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleteQuery := r.db.rebind(`DELETE FROM provider_health_snapshots
		WHERE bucket_start = ? AND provider = ? AND provider_account_id = ? AND model = ? AND capability = ?`)
	if _, err := tx.ExecContext(ctx, deleteQuery, formatTime(s.BucketStart), s.Provider,
		s.ProviderAccountID, s.Model, s.Capability); err != nil {
		return fmt.Errorf("store: replace provider health snapshot: %w", err)
	}
	if err := insertProviderHealthSnapshot(ctx, tx, r.db.rebind, s); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit provider health snapshot: %w", err)
	}
	return nil
}

func insertProviderHealthSnapshot(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, rebind func(string) string, s ProviderHealthSnapshot) error {
	q := rebind(`INSERT INTO provider_health_snapshots (id, bucket_start, bucket_size_seconds,
		provider, provider_account_id, model, capability,
		request_count, success_count, failure_count, fallback_count, final_failure_count,
		input_tokens, output_tokens, estimated_cost_microusd,
		latency_p50_ms, latency_p95_ms, latency_p99_ms,
		ttft_p50_ms, ttft_p95_ms, ttft_p99_ms,
		rate_limited_count, auth_error_count, quota_exceeded_count, timeout_count,
		provider_5xx_count, bad_request_count, network_error_count, unsupported_count, unknown_error_count,
		health_score, health_status, main_issue, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	_, err := exec.ExecContext(ctx, q,
		s.ID, formatTime(s.BucketStart), s.BucketSizeSeconds,
		s.Provider, s.ProviderAccountID, s.Model, s.Capability,
		s.RequestCount, s.SuccessCount, s.FailureCount, s.FallbackCount, s.FinalFailureCount,
		s.InputTokens, s.OutputTokens, s.EstimatedCostMicros,
		nullIntPtr(s.LatencyP50Ms), nullIntPtr(s.LatencyP95Ms), nullIntPtr(s.LatencyP99Ms),
		nullIntPtr(s.TTFTP50Ms), nullIntPtr(s.TTFTP95Ms), nullIntPtr(s.TTFTP99Ms),
		s.RateLimitedCount, s.AuthErrorCount, s.QuotaExceededCount, s.TimeoutCount,
		s.Provider5xxCount, s.BadRequestCount, s.NetworkErrorCount, s.UnsupportedCount, s.UnknownErrorCount,
		s.HealthScore, s.HealthStatus, nullStringPtr(s.MainIssue), formatTime(s.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert provider health snapshot: %w", err)
	}
	return nil
}

// ListSnapshots returns aggregated buckets within [since, now] for a dimension
// tuple. Empty provider/model/capability match any (wildcard via COALESCE).
func (r *ProviderHealthRepo) ListSnapshots(ctx context.Context, provider, account, model, capability string, since time.Time) ([]ProviderHealthSnapshot, error) {
	q := r.db.rebind(`SELECT id, bucket_start, bucket_size_seconds,
		provider, provider_account_id, model, capability,
		request_count, success_count, failure_count, fallback_count, final_failure_count,
		input_tokens, output_tokens, estimated_cost_microusd,
		latency_p50_ms, latency_p95_ms, latency_p99_ms,
		ttft_p50_ms, ttft_p95_ms, ttft_p99_ms,
		rate_limited_count, auth_error_count, quota_exceeded_count, timeout_count,
		provider_5xx_count, bad_request_count, network_error_count, unsupported_count, unknown_error_count,
		health_score, health_status, main_issue, created_at
		FROM provider_health_snapshots
		WHERE bucket_start >= ? AND provider = ?
		ORDER BY bucket_start ASC`)
	rows, err := r.db.sql.QueryContext(ctx, q, formatTime(since), provider)
	if err != nil {
		return nil, fmt.Errorf("store: list provider health snapshots: %w", err)
	}
	defer rows.Close()
	var out []ProviderHealthSnapshot
	for rows.Next() {
		s, err := scanProviderHealthSnapshot(rows.Scan)
		if err != nil {
			return nil, err
		}
		if account != "" && s.ProviderAccountID != account {
			continue
		}
		if model != "" && s.Model != model {
			continue
		}
		if capability != "" && s.Capability != capability {
			continue
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// InsertProbeResult records one synthetic probe outcome.
func (r *ProviderHealthRepo) InsertProbeResult(ctx context.Context, p ProviderProbeResult) error {
	if p.ID == "" {
		return fmt.Errorf("store: provider probe result: missing id")
	}
	q := r.db.rebind(`INSERT INTO provider_probe_results (id, provider, provider_account_id, model, capability,
		status, http_status, latency_ms, ttft_ms, error_type, error_message,
		prompt_tokens, completion_tokens, estimated_cost_microusd, triggered_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q,
		p.ID, p.Provider, p.ProviderAccountID, p.Model, p.Capability,
		p.Status, nullIntPtr(p.HTTPStatus), nullIntPtr(p.LatencyMs), nullIntPtr(p.TTFTMs),
		nullStringPtr(p.ErrorType), nullStringPtr(p.ErrorMessage),
		nullIntPtr(p.PromptTokens), nullIntPtr(p.CompletionTokens), nullInt64Ptr(p.EstimatedCostMicros),
		p.TriggeredBy, formatTime(p.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert provider probe result: %w", err)
	}
	return nil
}

// ListProbeResults returns probe results ordered newest-first, filtered by
// provider and a time window. Pagination via limit/offset.
func (r *ProviderHealthRepo) ListProbeResults(ctx context.Context, provider string, since time.Time, limit, offset int) ([]ProviderProbeResult, int, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if provider != "" {
		q := r.db.rebind(`SELECT id, provider, provider_account_id, model, capability,
			status, http_status, latency_ms, ttft_ms, error_type, error_message,
			prompt_tokens, completion_tokens, estimated_cost_microusd, triggered_by, created_at
			FROM provider_probe_results
			WHERE created_at >= ? AND provider = ?
			ORDER BY created_at DESC LIMIT ? OFFSET ?`)
		rows, err = r.db.sql.QueryContext(ctx, q, formatTime(since), provider, limit, offset)
	} else {
		q := r.db.rebind(`SELECT id, provider, provider_account_id, model, capability,
			status, http_status, latency_ms, ttft_ms, error_type, error_message,
			prompt_tokens, completion_tokens, estimated_cost_microusd, triggered_by, created_at
			FROM provider_probe_results
			WHERE created_at >= ?
			ORDER BY created_at DESC LIMIT ? OFFSET ?`)
		rows, err = r.db.sql.QueryContext(ctx, q, formatTime(since), limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("store: list provider probe results: %w", err)
	}
	defer rows.Close()
	var out []ProviderProbeResult
	for rows.Next() {
		p, err := scanProviderProbeResult(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Total count for pagination.
	var total int
	countQ := r.db.rebind(`SELECT COUNT(*) FROM provider_probe_results WHERE created_at >= ?`)
	countArgs := []any{formatTime(since)}
	if provider != "" {
		countQ = r.db.rebind(`SELECT COUNT(*) FROM provider_probe_results WHERE created_at >= ? AND provider = ?`)
		countArgs = append(countArgs, provider)
	}
	if err := r.db.sql.QueryRowContext(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count provider probe results: %w", err)
	}
	return out, total, nil
}

// --- scanners ---------------------------------------------------------------

func scanProviderHealthCurrent(scan func(dest ...any) error) (ProviderHealthCurrent, error) {
	var (
		c                                   ProviderHealthCurrent
		latencyP95, ttftP95                 sql.NullInt64
		mainIssue, recommendation           sql.NullString
		lastSuccess, lastFailure, lastProbe sql.NullString
		lastUpdated                         string
	)
	if err := scan(
		&c.ID, &c.Provider, &c.ProviderAccountID, &c.Model, &c.Capability,
		&c.HealthStatus, &c.HealthScore, &c.SuccessRate, &c.ErrorRate, &c.RequestCount,
		&c.FallbackCount, &latencyP95, &ttftP95, &c.ConsecutiveFailures,
		&mainIssue, &recommendation, &lastSuccess, &lastFailure, &lastProbe, &lastUpdated,
	); err != nil {
		return ProviderHealthCurrent{}, err
	}
	if latencyP95.Valid {
		v := int(latencyP95.Int64)
		c.LatencyP95Ms = &v
	}
	if ttftP95.Valid {
		v := int(ttftP95.Int64)
		c.TTFTP95Ms = &v
	}
	if mainIssue.Valid {
		s := mainIssue.String
		c.MainIssue = &s
	}
	if recommendation.Valid {
		s := recommendation.String
		c.Recommendation = &s
	}
	if lastSuccess.Valid {
		t := parseTime(lastSuccess.String)
		c.LastSuccessAt = &t
	}
	if lastFailure.Valid {
		t := parseTime(lastFailure.String)
		c.LastFailureAt = &t
	}
	if lastProbe.Valid {
		t := parseTime(lastProbe.String)
		c.LastProbeAt = &t
	}
	c.LastUpdatedAt = parseTime(lastUpdated)
	return c, nil
}

func scanProviderHealthSnapshot(scan func(dest ...any) error) (ProviderHealthSnapshot, error) {
	var (
		s                                  ProviderHealthSnapshot
		bucketStart, createdAt             string
		mainIssue                          sql.NullString
		lp50, lp95, lp99, tp50, tp95, tp99 sql.NullInt64
	)
	if err := scan(
		&s.ID, &bucketStart, &s.BucketSizeSeconds,
		&s.Provider, &s.ProviderAccountID, &s.Model, &s.Capability,
		&s.RequestCount, &s.SuccessCount, &s.FailureCount, &s.FallbackCount, &s.FinalFailureCount,
		&s.InputTokens, &s.OutputTokens, &s.EstimatedCostMicros,
		&lp50, &lp95, &lp99, &tp50, &tp95, &tp99,
		&s.RateLimitedCount, &s.AuthErrorCount, &s.QuotaExceededCount, &s.TimeoutCount,
		&s.Provider5xxCount, &s.BadRequestCount, &s.NetworkErrorCount, &s.UnsupportedCount, &s.UnknownErrorCount,
		&s.HealthScore, &s.HealthStatus, &mainIssue, &createdAt,
	); err != nil {
		return ProviderHealthSnapshot{}, err
	}
	s.BucketStart = parseTime(bucketStart)
	s.CreatedAt = parseTime(createdAt)
	if mainIssue.Valid {
		v := mainIssue.String
		s.MainIssue = &v
	}
	assignIntPtr(lp50, &s.LatencyP50Ms)
	assignIntPtr(lp95, &s.LatencyP95Ms)
	assignIntPtr(lp99, &s.LatencyP99Ms)
	assignIntPtr(tp50, &s.TTFTP50Ms)
	assignIntPtr(tp95, &s.TTFTP95Ms)
	assignIntPtr(tp99, &s.TTFTP99Ms)
	return s, nil
}

func scanProviderProbeResult(scan func(dest ...any) error) (ProviderProbeResult, error) {
	var (
		p                                                 ProviderProbeResult
		httpStatus, latencyMs, ttftMs, promptTok, compTok sql.NullInt64
		costMicros                                        sql.NullInt64
		errType, errMsg                                   sql.NullString
		createdAt                                         string
	)
	if err := scan(
		&p.ID, &p.Provider, &p.ProviderAccountID, &p.Model, &p.Capability,
		&p.Status, &httpStatus, &latencyMs, &ttftMs, &errType, &errMsg,
		&promptTok, &compTok, &costMicros, &p.TriggeredBy, &createdAt,
	); err != nil {
		return ProviderProbeResult{}, err
	}
	assignIntPtr(httpStatus, &p.HTTPStatus)
	assignIntPtr(latencyMs, &p.LatencyMs)
	assignIntPtr(ttftMs, &p.TTFTMs)
	assignIntPtr(promptTok, &p.PromptTokens)
	assignIntPtr(compTok, &p.CompletionTokens)
	if costMicros.Valid {
		v := costMicros.Int64
		p.EstimatedCostMicros = &v
	}
	if errType.Valid {
		v := errType.String
		p.ErrorType = &v
	}
	if errMsg.Valid {
		v := errMsg.String
		p.ErrorMessage = &v
	}
	p.CreatedAt = parseTime(createdAt)
	return p, nil
}

func nullIntPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullStringPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt64Ptr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func assignIntPtr(n sql.NullInt64, dst **int) {
	if n.Valid {
		v := int(n.Int64)
		*dst = &v
	}
}

// ProviderHealthRollup is one provider's aggregate over one timeline bucket,
// summed across every account/model/capability snapshot in that bucket.
type ProviderHealthRollup struct {
	Provider       string
	Bucket         int
	Requests       int64
	Successes      int64
	Failures       int64
	Fallbacks      int64
	RateLimited    int64
	LatencyP95Ms   int64 // worst per-snapshot p95 in the bucket
	WorstStatusRnk int   // 3 unhealthy, 2 degraded, 1 healthy, 0 unknown
}

// RollupSnapshots aggregates every provider's 1-minute health snapshots since
// `since` into buckets of bucketSecs, in one query for all providers. It is
// the data behind the dashboard's per-provider uptime strips.
func (r *ProviderHealthRepo) RollupSnapshots(ctx context.Context, since time.Time, bucketSecs int64) ([]ProviderHealthRollup, error) {
	if bucketSecs <= 0 {
		bucketSecs = 3600
	}
	epochBucket, epochSince := r.db.epochExpr("bucket_start"), r.db.epochExpr("?")
	q := r.db.rebind(fmt.Sprintf(`SELECT provider, CAST((%s-%s)/? AS INTEGER),
		COALESCE(SUM(request_count),0), COALESCE(SUM(success_count),0), COALESCE(SUM(failure_count),0),
		COALESCE(SUM(fallback_count),0), COALESCE(SUM(rate_limited_count),0),
		COALESCE(MAX(latency_p95_ms),0),
		COALESCE(MAX(CASE health_status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END),0)
		FROM provider_health_snapshots
		WHERE bucket_start >= ?
		GROUP BY 1, 2 ORDER BY 1, 2`, epochBucket, epochSince))
	rows, err := r.db.sql.QueryContext(ctx, q, formatTime(since), bucketSecs, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: rollup provider health snapshots: %w", err)
	}
	defer rows.Close()
	var out []ProviderHealthRollup
	for rows.Next() {
		var p ProviderHealthRollup
		if err := rows.Scan(&p.Provider, &p.Bucket, &p.Requests, &p.Successes, &p.Failures,
			&p.Fallbacks, &p.RateLimited, &p.LatencyP95Ms, &p.WorstStatusRnk); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SnapshotSummary is a provider (or provider+model) rollup of persisted
// 1-minute health snapshots over a window. It backs the historical ranges of
// the health dashboard, which the rolling "current" table cannot answer.
type SnapshotSummary struct {
	Provider, Model                        string
	Requests, Successes, Failures          int64
	Fallbacks, FinalFailures               int64
	RateLimited, AuthErrors, QuotaExceeded int64
	Timeouts, Provider5xx, NetworkErrors   int64
	BadRequests                            int64
	LatencyP95Ms, TTFTP95Ms                int64 // worst per-snapshot value
	WorstStatusRank                        int   // 3 unhealthy, 2 degraded, 1 healthy, 0 unknown
	Accounts, Models                       int64
	LastBucket                             time.Time
}

// SummarizeSnapshots aggregates snapshots since `since`, grouped by provider,
// or by provider and model when byModel is set.
func (r *ProviderHealthRepo) SummarizeSnapshots(ctx context.Context, since time.Time, byModel bool) ([]SnapshotSummary, error) {
	modelCol, group := "''", "provider"
	if byModel {
		modelCol, group = "model", "provider, model"
	}
	q := r.db.rebind(`SELECT provider, ` + modelCol + `,
		COALESCE(SUM(request_count),0), COALESCE(SUM(success_count),0), COALESCE(SUM(failure_count),0),
		COALESCE(SUM(fallback_count),0), COALESCE(SUM(final_failure_count),0),
		COALESCE(SUM(rate_limited_count),0), COALESCE(SUM(auth_error_count),0), COALESCE(SUM(quota_exceeded_count),0),
		COALESCE(SUM(timeout_count),0), COALESCE(SUM(provider_5xx_count),0), COALESCE(SUM(network_error_count),0),
		COALESCE(SUM(bad_request_count),0),
		COALESCE(MAX(latency_p95_ms),0), COALESCE(MAX(ttft_p95_ms),0),
		COALESCE(MAX(CASE health_status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END),0),
		COUNT(DISTINCT provider_account_id), COUNT(DISTINCT model), MAX(bucket_start)
		FROM provider_health_snapshots
		WHERE bucket_start >= ?
		GROUP BY ` + group + ` ORDER BY ` + group)
	rows, err := r.db.sql.QueryContext(ctx, q, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: summarize provider health snapshots: %w", err)
	}
	defer rows.Close()
	var out []SnapshotSummary
	for rows.Next() {
		var s SnapshotSummary
		var last string
		if err := rows.Scan(&s.Provider, &s.Model, &s.Requests, &s.Successes, &s.Failures,
			&s.Fallbacks, &s.FinalFailures, &s.RateLimited, &s.AuthErrors, &s.QuotaExceeded,
			&s.Timeouts, &s.Provider5xx, &s.NetworkErrors, &s.BadRequests,
			&s.LatencyP95Ms, &s.TTFTP95Ms, &s.WorstStatusRank, &s.Accounts, &s.Models, &last); err != nil {
			return nil, err
		}
		s.LastBucket = parseTime(last)
		out = append(out, s)
	}
	return out, rows.Err()
}
