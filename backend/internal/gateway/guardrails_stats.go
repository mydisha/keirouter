package gateway

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// adminGuardrailStats returns the guardrails monitoring payload: how much
// traffic the guardrails blocked, masked or warned on, which detectors and
// entities drove it, the latency overhead, and what the LLM judges cost.
func (s *Server) adminGuardrailStats(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusNotImplemented, "guardrail stats not wired")
		return
	}
	q := r.URL.Query()
	period, tz := q.Get("period"), q.Get("tz")
	f := store.GuardrailStatsFilter{
		Since:    sinceForPeriod(period, tz),
		APIKeyID: q.Get("api_key_id"),
		Model:    q.Get("model"),
		ChainID:  q.Get("chain_id"),
		Buckets:  parseTimelineBuckets(q.Get("buckets")),
	}
	cacheKey := "guardrail-stats|" + period + "|" + tz + "|" + strconv.Itoa(f.Buckets) + "|" +
		f.APIKeyID + "|" + f.Model + "|" + f.ChainID
	if s.cacheHit(w, cacheKey) {
		return
	}

	st, err := s.db.GuardrailStats().Stats(r.Context(), adminTenant, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "guardrail stats: "+err.Error())
		return
	}

	t := st.Totals
	rate := func(n int64) float64 {
		if t.Requests == 0 {
			return 0
		}
		return float64(n) / float64(t.Requests)
	}
	timeline := make([]map[string]any, 0, len(st.Timeline))
	for _, b := range st.Timeline {
		timeline = append(timeline, map[string]any{
			"bucket": b.Bucket, "requests": b.Requests, "blocked": b.Blocked,
			"masked": b.Masked, "warned": b.Warned, "log_only": b.LogOnly,
		})
	}
	detectors := make([]map[string]any, 0, len(st.ByDetector))
	for _, d := range st.ByDetector {
		detectors = append(detectors, map[string]any{
			"detector": d.Detector, "direction": d.Direction, "action": d.Action,
			"decisions": d.Decisions, "requests": d.Requests,
		})
	}
	reasons := make([]map[string]any, 0, len(st.TopReasons))
	for _, rr := range st.TopReasons {
		reasons = append(reasons, map[string]any{
			"detector": rr.Detector, "action": rr.Action, "reason": rr.Reason, "requests": rr.Requests,
		})
	}
	entities := make([]map[string]any, 0, len(st.TopEntities))
	for _, e := range st.TopEntities {
		entities = append(entities, map[string]any{"entity": e.Entity, "count": e.Count})
	}

	// Rows older than the audit retention window are gone, so per-detector
	// breakdowns undercount a period that reaches past it. The hour of slack
	// keeps a period equal to the retention (7d vs 7 days) from tripping it.
	logsTruncated := false
	if s.guardrailRetention > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(s.guardrailRetention) * 24 * time.Hour)
		logsTruncated = cutoff.Sub(f.Since) > time.Hour
	}

	writeJSONCached(w, s.insightsCache, cacheKey, map[string]any{
		"since":   f.Since.Format(time.RFC3339),
		"buckets": f.Buckets,
		"totals": map[string]any{
			"requests":           t.Requests,
			"evaluated":          t.Evaluated,
			"blocked":            t.Blocked,
			"blocked_inbound":    t.BlockedInbound,
			"blocked_outbound":   t.BlockedOutbound,
			"blocked_mid_stream": t.BlockedMidStream,
			"masked":             t.Masked,
			"warned":             t.Warned,
			"log_only":           t.LogOnly,
			"block_rate":         rate(t.Blocked),
			"mask_rate":          rate(t.Masked),
			"warn_rate":          rate(t.Warned),
			"overhead_avg_ms":    t.AvgMS,
			"overhead_p50_ms":    t.P50MS,
			"overhead_p95_ms":    t.P95MS,
		},
		"judge": map[string]any{
			"calls":          st.Judge.Calls,
			"tokens":         st.Judge.Tokens,
			"cost_nanos":     st.Judge.CostNanos,
			"avg_latency_ms": st.Judge.AvgLatencyMS,
		},
		"timeline":     timeline,
		"by_detector":  detectors,
		"top_reasons":  reasons,
		"top_entities": entities,
		"top_api_keys": guardrailTargetRows(st.TopAPIKeys),
		"top_models":   guardrailTargetRows(st.TopModels),
		"data_quality": map[string]any{
			"audit_dropped":      s.guardrails.Audit().Dropped(),
			"log_retention_days": s.guardrailRetention,
			"logs_truncated":     logsTruncated,
			"entities_sampled":   st.EntitySampled,
		},
	})
}

func guardrailTargetRows(rows []store.GuardrailTargetRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		out = append(out, map[string]any{
			"key": t.Key, "requests": t.Requests, "blocked": t.Blocked,
			"masked": t.Masked, "warned": t.Warned, "log_only": t.LogOnly,
		})
	}
	return out
}
