package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// GuardrailTestRequestPrefix prefixes the request ids of the dashboard's
// guardrail test panel. Their audit rows are real but are not traffic, so the
// stats leave them out.
const GuardrailTestRequestPrefix = "test-"

// guardrailEntitySampleLimit caps how many audit rows the entity breakdown
// parses; findings are JSON, which neither dialect can group portably.
const guardrailEntitySampleLimit = 5000

// GuardrailStatsFilter scopes the guardrail stats. Empty fields are ignored;
// a zero Until means "now".
type GuardrailStatsFilter struct {
	Since, Until time.Time
	APIKeyID     string
	Model        string
	ChainID      string
	Buckets      int
}

// GuardrailTotals is the per-request guardrail verdict over a window. Request
// counts come from usage_records (one row per request), so they are rates of
// real traffic; LLM judge calls are excluded and reported in GuardrailJudge.
type GuardrailTotals struct {
	Requests  int64 // client requests in the window
	Evaluated int64 // requests a guardrail policy applied to
	// Blocked counts guardrail refusals. It also includes policy_blocked rows
	// written before per-request outcomes were recorded.
	Blocked          int64
	BlockedInbound   int64
	BlockedOutbound  int64
	BlockedMidStream int64
	Masked           int64
	Warned           int64
	LogOnly          int64
	AvgMS            int64 // mean guardrail overhead of evaluated requests
	P50MS            int64
	P95MS            int64
}

// GuardrailJudge is the cost of LLM judge calls the guardrails made.
type GuardrailJudge struct {
	Calls        int64
	Tokens       int64
	CostNanos    int64
	AvgLatencyMS int64
}

// GuardrailTimeBucket is one slice of the guardrail trend chart.
type GuardrailTimeBucket struct {
	Bucket                                     int
	Requests, Blocked, Masked, Warned, LogOnly int64
}

// GuardrailDetectorRow counts decisions per detector/direction/action.
// Decisions counts audit rows (a streamed mask repeats per release); Requests
// counts distinct requests.
type GuardrailDetectorRow struct {
	Detector, Direction, Action string
	Decisions, Requests         int64
}

// GuardrailReasonRow is one frequent decision reason.
type GuardrailReasonRow struct {
	Detector, Action, Reason string
	Requests                 int64
}

// GuardrailEntityRow counts findings of one entity type.
type GuardrailEntityRow struct {
	Entity string
	Count  int64
}

// GuardrailTargetRow is per-API-key or per-model guardrail impact.
type GuardrailTargetRow struct {
	Key                                        string
	Requests, Blocked, Masked, Warned, LogOnly int64
}

// GuardrailStats is the guardrails monitoring payload.
type GuardrailStats struct {
	Totals        GuardrailTotals
	Judge         GuardrailJudge
	Timeline      []GuardrailTimeBucket
	ByDetector    []GuardrailDetectorRow
	TopReasons    []GuardrailReasonRow
	TopEntities   []GuardrailEntityRow
	EntitySampled bool // the entity breakdown hit its row cap
	TopAPIKeys    []GuardrailTargetRow
	TopModels     []GuardrailTargetRow
}

// GuardrailStatsRepo aggregates guardrail activity from usage_records and
// guardrail_logs.
type GuardrailStatsRepo struct{ db *DB }

// GuardrailStats returns the guardrail stats repository.
func (db *DB) GuardrailStats() *GuardrailStatsRepo { return &GuardrailStatsRepo{db: db} }

// Per-request verdict predicates over usage_records.
const (
	grBlocked = `(guardrail_action='block' OR (status='blocked' AND error_kind='policy_blocked'))`
	// grNotJudge drops guardrail-originated calls (LLM judges) from traffic.
	grNotJudge = `COALESCE(client,'') NOT LIKE 'guardrails-%'`
)

// usageWhere builds the usage_records filter for client traffic.
func (f GuardrailStatsFilter) usageWhere(tenantID string) (string, []any) {
	where, args := rangeWhere(tenantID, f.Since, f.Until)
	where += " AND " + grNotJudge
	if f.APIKeyID != "" {
		where += " AND api_key_id=?"
		args = append(args, f.APIKeyID)
	}
	if f.Model != "" {
		where += " AND model=?"
		args = append(args, f.Model)
	}
	if f.ChainID != "" {
		where += " AND chain_id=?"
		args = append(args, f.ChainID)
	}
	return where, args
}

// logsWhere builds the guardrail_logs filter, excluding test-panel rows.
func (f GuardrailStatsFilter) logsWhere(tenantID string) (string, []any) {
	where, args := rangeWhere(tenantID, f.Since, f.Until)
	where += " AND request_id NOT LIKE ?"
	args = append(args, GuardrailTestRequestPrefix+"%")
	if f.APIKeyID != "" {
		where += " AND api_key_id=?"
		args = append(args, f.APIKeyID)
	}
	if f.Model != "" {
		where += " AND model=?"
		args = append(args, f.Model)
	}
	if f.ChainID != "" {
		where += " AND chain_id=?"
		args = append(args, f.ChainID)
	}
	return where, args
}

// Stats computes every guardrail aggregate for the window.
func (r *GuardrailStatsRepo) Stats(ctx context.Context, tenantID string, f GuardrailStatsFilter) (GuardrailStats, error) {
	var out GuardrailStats
	var err error
	if out.Totals, err = r.totals(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.Totals.P50MS, out.Totals.P95MS, err = r.overheadPercentiles(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.Judge, err = r.judge(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.Timeline, err = r.timeline(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.ByDetector, err = r.byDetector(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.TopReasons, err = r.topReasons(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.TopEntities, out.EntitySampled, err = r.topEntities(ctx, tenantID, f); err != nil {
		return out, err
	}
	if out.TopAPIKeys, err = r.topTargets(ctx, tenantID, f, "COALESCE(api_key_id,'')"); err != nil {
		return out, err
	}
	if out.TopModels, err = r.topTargets(ctx, tenantID, f, "model"); err != nil {
		return out, err
	}
	return out, nil
}

func (r *GuardrailStatsRepo) totals(ctx context.Context, tenantID string, f GuardrailStatsFilter) (GuardrailTotals, error) {
	where, args := f.usageWhere(tenantID)
	q := r.db.rebind(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN guardrail_action<>'' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN ` + grBlocked + ` THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='block' AND guardrail_direction='inbound' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='block' AND guardrail_direction='outbound' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='block' AND guardrail_mid_stream=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='mask' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='warn' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='log_only' THEN 1 ELSE 0 END),0),
		COALESCE(CAST(AVG(CASE WHEN guardrail_action<>'' THEN guardrail_ms END) AS INTEGER),0)
		FROM usage_records WHERE ` + where)
	var t GuardrailTotals
	err := r.db.sql.QueryRowContext(ctx, q, args...).Scan(&t.Requests, &t.Evaluated, &t.Blocked,
		&t.BlockedInbound, &t.BlockedOutbound, &t.BlockedMidStream, &t.Masked, &t.Warned, &t.LogOnly, &t.AvgMS)
	if err != nil {
		return t, fmt.Errorf("store: guardrail totals: %w", err)
	}
	return t, nil
}

// overheadPercentiles reads p50/p95 guardrail_ms of evaluated requests by
// ordinal offset (SQLite has no percentile aggregate), like LatencyPercentiles.
func (r *GuardrailStatsRepo) overheadPercentiles(ctx context.Context, tenantID string, f GuardrailStatsFilter) (p50, p95 int64, err error) {
	where, args := f.usageWhere(tenantID)
	where += " AND guardrail_action<>''"
	var n int64
	if err := r.db.sql.QueryRowContext(ctx, r.db.rebind(`SELECT COUNT(*) FROM usage_records WHERE `+where), args...).Scan(&n); err != nil {
		return 0, 0, fmt.Errorf("store: guardrail overhead count: %w", err)
	}
	if n == 0 {
		return 0, 0, nil
	}
	at := func(pct float64) (int64, error) {
		offset := int64(float64(n-1) * pct)
		q := r.db.rebind(`SELECT guardrail_ms FROM usage_records WHERE ` + where + ` ORDER BY guardrail_ms LIMIT 1 OFFSET ?`)
		var v int64
		if err := r.db.sql.QueryRowContext(ctx, q, append(append([]any{}, args...), offset)...).Scan(&v); err != nil {
			return 0, fmt.Errorf("store: guardrail overhead percentile: %w", err)
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

// judge sums the LLM judge calls. Only the key filter applies: the judge runs
// on its own model and chain, so model/chain filters would hide it entirely.
func (r *GuardrailStatsRepo) judge(ctx context.Context, tenantID string, f GuardrailStatsFilter) (GuardrailJudge, error) {
	where, args := rangeWhere(tenantID, f.Since, f.Until)
	where += " AND client LIKE 'guardrails-%'"
	if f.APIKeyID != "" {
		where += " AND api_key_id=?"
		args = append(args, f.APIKeyID)
	}
	q := r.db.rebind(`SELECT COUNT(*), COALESCE(SUM(prompt_tokens+completion_tokens),0),
		COALESCE(SUM(cost_nanos),0),
		COALESCE(CAST(AVG(CASE WHEN end_to_end_latency_ms>0 THEN end_to_end_latency_ms END) AS INTEGER),0)
		FROM usage_records WHERE ` + where)
	var j GuardrailJudge
	if err := r.db.sql.QueryRowContext(ctx, q, args...).Scan(&j.Calls, &j.Tokens, &j.CostNanos, &j.AvgLatencyMS); err != nil {
		return j, fmt.Errorf("store: guardrail judge usage: %w", err)
	}
	return j, nil
}

func (r *GuardrailStatsRepo) timeline(ctx context.Context, tenantID string, f GuardrailStatsFilter) ([]GuardrailTimeBucket, error) {
	buckets := f.Buckets
	if buckets <= 0 {
		buckets = 24
	}
	to := f.Until
	if to.IsZero() {
		to = time.Now().UTC()
	}
	span := to.Sub(f.Since)
	if span <= 0 {
		span = time.Hour
	}
	slotSecs := int64(span.Seconds()) / int64(buckets)
	if slotSecs <= 0 {
		slotSecs = 60
	}
	where, args := f.usageWhere(tenantID)
	q := r.db.rebind(fmt.Sprintf(`SELECT CAST((%s-%s)/? AS INTEGER), COUNT(*),
		COALESCE(SUM(CASE WHEN `+grBlocked+` THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='mask' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='warn' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN guardrail_action='log_only' THEN 1 ELSE 0 END),0)
		FROM usage_records WHERE %s GROUP BY 1 ORDER BY 1`,
		r.db.epochExpr("created_at"), r.db.epochExpr("?"), where))
	rows, err := r.db.sql.QueryContext(ctx, q, append([]any{formatTime(f.Since), slotSecs}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("store: guardrail timeline: %w", err)
	}
	defer rows.Close()
	var out []GuardrailTimeBucket
	for rows.Next() {
		var b GuardrailTimeBucket
		if err := rows.Scan(&b.Bucket, &b.Requests, &b.Blocked, &b.Masked, &b.Warned, &b.LogOnly); err != nil {
			return nil, fmt.Errorf("store: scan guardrail timeline: %w", err)
		}
		if b.Bucket >= 0 && b.Bucket < buckets {
			out = append(out, b)
		}
	}
	return out, rows.Err()
}

func (r *GuardrailStatsRepo) byDetector(ctx context.Context, tenantID string, f GuardrailStatsFilter) ([]GuardrailDetectorRow, error) {
	where, args := f.logsWhere(tenantID)
	q := r.db.rebind(`SELECT detector, direction, action, COUNT(*), COUNT(DISTINCT NULLIF(request_id,''))
		FROM guardrail_logs WHERE ` + where + `
		GROUP BY detector, direction, action ORDER BY 5 DESC, 4 DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: guardrail detector breakdown: %w", err)
	}
	defer rows.Close()
	var out []GuardrailDetectorRow
	for rows.Next() {
		var d GuardrailDetectorRow
		if err := rows.Scan(&d.Detector, &d.Direction, &d.Action, &d.Decisions, &d.Requests); err != nil {
			return nil, fmt.Errorf("store: scan guardrail detector breakdown: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *GuardrailStatsRepo) topReasons(ctx context.Context, tenantID string, f GuardrailStatsFilter) ([]GuardrailReasonRow, error) {
	where, args := f.logsWhere(tenantID)
	q := r.db.rebind(`SELECT detector, action, reason, COUNT(DISTINCT NULLIF(request_id,''))
		FROM guardrail_logs WHERE ` + where + ` AND reason<>''
		GROUP BY detector, action, reason ORDER BY 4 DESC LIMIT 10`)
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: guardrail top reasons: %w", err)
	}
	defer rows.Close()
	var out []GuardrailReasonRow
	for rows.Next() {
		var row GuardrailReasonRow
		if err := rows.Scan(&row.Detector, &row.Action, &row.Reason, &row.Requests); err != nil {
			return nil, fmt.Errorf("store: scan guardrail top reasons: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// topEntities counts finding entities over the most recent audit rows in the
// window. sampled reports that older rows were left out.
func (r *GuardrailStatsRepo) topEntities(ctx context.Context, tenantID string, f GuardrailStatsFilter) ([]GuardrailEntityRow, bool, error) {
	where, args := f.logsWhere(tenantID)
	q := r.db.rebind(`SELECT findings FROM guardrail_logs WHERE ` + where + ` AND findings<>'[]'
		ORDER BY created_at DESC LIMIT ` + fmt.Sprintf("%d", guardrailEntitySampleLimit))
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("store: guardrail entities: %w", err)
	}
	defer rows.Close()
	counts := map[string]int64{}
	var n int
	var findings []struct {
		Entity string `json:"entity"`
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, false, fmt.Errorf("store: scan guardrail entities: %w", err)
		}
		n++
		findings = findings[:0]
		if json.Unmarshal([]byte(raw), &findings) != nil {
			continue
		}
		for _, fd := range findings {
			if fd.Entity != "" {
				counts[fd.Entity]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	out := make([]GuardrailEntityRow, 0, len(counts))
	for e, c := range counts {
		out = append(out, GuardrailEntityRow{Entity: e, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Entity < out[j].Entity
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out, n >= guardrailEntitySampleLimit, nil
}

// topTargets ranks API keys or models by guardrail impact. keyExpr is a
// trusted column expression, never user input.
func (r *GuardrailStatsRepo) topTargets(ctx context.Context, tenantID string, f GuardrailStatsFilter, keyExpr string) ([]GuardrailTargetRow, error) {
	where, args := f.usageWhere(tenantID)
	q := r.db.rebind(`SELECT ` + keyExpr + `, COUNT(*),
		COALESCE(SUM(CASE WHEN ` + grBlocked + ` THEN 1 ELSE 0 END),0) AS blocked,
		COALESCE(SUM(CASE WHEN guardrail_action='mask' THEN 1 ELSE 0 END),0) AS masked,
		COALESCE(SUM(CASE WHEN guardrail_action='warn' THEN 1 ELSE 0 END),0) AS warned,
		COALESCE(SUM(CASE WHEN guardrail_action='log_only' THEN 1 ELSE 0 END),0) AS log_only
		FROM usage_records WHERE ` + where + `
		GROUP BY 1
		HAVING SUM(CASE WHEN ` + grBlocked + ` OR guardrail_action IN ('mask','warn','log_only') THEN 1 ELSE 0 END) > 0
		ORDER BY 3 DESC, 4 DESC, 5 DESC, 2 DESC LIMIT 10`)
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: guardrail top targets: %w", err)
	}
	defer rows.Close()
	var out []GuardrailTargetRow
	for rows.Next() {
		var t GuardrailTargetRow
		if err := rows.Scan(&t.Key, &t.Requests, &t.Blocked, &t.Masked, &t.Warned, &t.LogOnly); err != nil {
			return nil, fmt.Errorf("store: scan guardrail top targets: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
