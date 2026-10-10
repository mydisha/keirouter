package gateway

import (
	"net/http"
	"sort"
	"time"

	"github.com/mydisha/keirouter/backend/internal/health"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// healthRangeIsHistorical reports whether a requested health range is longer
// than the telemetry service's rolling "current" window. Such ranges are
// answered from persisted snapshots instead of the current table, so 24h/7d
// views survive restarts and actually cover the period they claim.
func (s *Server) healthRangeIsHistorical(raw string) bool {
	if raw == "" {
		return false
	}
	window := 15 * time.Minute
	if s.providerHealth != nil && s.providerHealth.RollingWindow() > 0 {
		window = s.providerHealth.RollingWindow()
	}
	return parseRangeDuration(raw) > window
}

// snapshotIssue picks the dominant error class of a snapshot rollup.
func snapshotIssue(sum store.SnapshotSummary) string {
	candidates := []struct {
		kind  health.ProviderErrorType
		count int64
	}{
		{health.ProviderErrorRateLimited, sum.RateLimited},
		{health.ProviderErrorAuth, sum.AuthErrors},
		{health.ProviderErrorQuotaExceeded, sum.QuotaExceeded},
		{health.ProviderErrorTimeout, sum.Timeouts},
		{health.ProviderErrorProvider5xx, sum.Provider5xx},
		{health.ProviderErrorNetwork, sum.NetworkErrors},
		{health.ProviderErrorBadRequest, sum.BadRequests},
	}
	best, bestCount := "", int64(0)
	for _, c := range candidates {
		if c.count > bestCount {
			best, bestCount = string(c.kind), c.count
		}
	}
	return best
}

// snapshotStatus derives a range-level status and score from a rollup. The
// thresholds mirror the Overview's uptime strips: ≥25% failures or any
// unhealthy minute is unhealthy; ≥5% failures, rate limiting or a degraded
// minute is degraded.
func snapshotStatus(sum store.SnapshotSummary) (string, int) {
	if sum.Requests == 0 {
		return health.StatusUnknown, 0
	}
	failRate := float64(sum.Failures) / float64(sum.Requests)
	score := int((1 - failRate) * 100)
	switch {
	case failRate >= 0.25:
		return health.StatusUnhealthy, score
	case failRate >= 0.05 || sum.RateLimited > 0 || sum.WorstStatusRank >= 2:
		return health.StatusDegraded, score
	default:
		return health.StatusHealthy, score
	}
}

// liveStatusByProvider returns the worst current status per provider from the
// rolling window, used to show "status now" next to historical metrics.
func liveStatusByProvider(rows []store.ProviderHealthCurrent, byModel bool) map[string]string {
	out := map[string]string{}
	for _, c := range rows {
		key := c.Provider
		if byModel {
			key += "\x00" + c.Model
		}
		if cur, ok := out[key]; !ok || rank(c.HealthStatus) > rank(cur) {
			out[key] = c.HealthStatus
		}
	}
	return out
}

func snapshotRow(sum store.SnapshotSummary, live string) map[string]any {
	status, score := snapshotStatus(sum)
	issue := snapshotIssue(sum)
	entry := map[string]any{
		"provider":            sum.Provider,
		"status":              status,
		"range_status":        status,
		"score":               score,
		"accounts":            sum.Accounts,
		"models_monitored":    sum.Models,
		"requests":            sum.Requests,
		"success_rate":        pct(sum.Successes, sum.Requests),
		"error_rate":          pct(sum.Failures, sum.Requests),
		"latency_p95_ms":      sum.LatencyP95Ms,
		"ttft_p95_ms":         sum.TTFTP95Ms,
		"fallback_count":      sum.Fallbacks,
		"final_failure_count": sum.FinalFailures,
		"rate_limited_count":  sum.RateLimited,
		"main_issue":          issue,
		"recommendation":      health.RecommendationForIssue(issue),
		"last_seen_at":        sum.LastBucket,
	}
	if live != "" {
		// The badge shows what is happening now; metrics describe the range.
		entry["status"] = live
		entry["live_status"] = live
	}
	return entry
}

func (s *Server) historicalHealthOverview(w http.ResponseWriter, r *http.Request, requestedRange, statusFilter string) {
	since := parseRange(requestedRange)
	sums, err := s.db.ProviderHealth().SummarizeSnapshots(r.Context(), since, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	current, _ := s.db.ProviderHealth().ListCurrent(r.Context(), "")
	live := liveStatusByProvider(current, false)

	summary := map[string]int64{"healthy": 0, "degraded": 0, "unhealthy": 0, "unknown": 0, "disabled": 0, "fallbacks": 0}
	providers := make([]map[string]any, 0, len(sums))
	var p95Total, p95Count int64
	for _, sum := range sums {
		entry := snapshotRow(sum, live[sum.Provider])
		status := entry["status"].(string)
		summary[status]++
		summary["fallbacks"] += sum.Fallbacks
		if sum.LatencyP95Ms > 0 {
			p95Total += sum.LatencyP95Ms
			p95Count++
		}
		if statusFilter != "" && status != statusFilter {
			continue
		}
		providers = append(providers, entry)
	}
	sort.SliceStable(providers, func(i, j int) bool {
		return providers[i]["requests"].(int64) > providers[j]["requests"].(int64)
	})
	avgP95 := int64(0)
	if p95Count > 0 {
		avgP95 = p95Total / p95Count
	}
	dropped := int64(0)
	if s.providerHealth != nil {
		dropped = int64(s.providerHealth.DroppedEvents())
	}
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{
		"window": map[string]any{
			"kind":             "historical",
			"duration_seconds": int64(now.Sub(since).Seconds()),
			"requested_range":  requestedRange,
			"generated_at":     now,
			"since":            since.UTC(),
		},
		"summary": map[string]any{
			"healthy":                 summary["healthy"],
			"degraded":                summary["degraded"],
			"unhealthy":               summary["unhealthy"],
			"unknown":                 summary["unknown"],
			"disabled":                summary["disabled"],
			"fallbacks":               summary["fallbacks"],
			"avg_p95_latency_ms":      avgP95,
			"telemetry_dropped":       dropped,
			"telemetry_dropped_scope": "process_lifetime",
		},
		"providers": providers,
	})
}

func (s *Server) historicalHealthModels(w http.ResponseWriter, r *http.Request, requestedRange, statusFilter string) {
	since := parseRange(requestedRange)
	sums, err := s.db.ProviderHealth().SummarizeSnapshots(r.Context(), since, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	current, _ := s.db.ProviderHealth().ListCurrent(r.Context(), "")
	live := liveStatusByProvider(current, true)
	out := make([]map[string]any, 0, len(sums))
	for _, sum := range sums {
		if sum.Model == "" {
			continue
		}
		entry := snapshotRow(sum, live[sum.Provider+"\x00"+sum.Model])
		if statusFilter != "" && entry["status"] != statusFilter {
			continue
		}
		entry["model"] = sum.Model
		entry["last_updated_at"] = sum.LastBucket
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["requests"].(int64) > out[j]["requests"].(int64)
	})
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// persistedChainStats maps chain id → usage-derived request/fallback/final
// failure counts since `since`, for ranges the in-memory telemetry window
// cannot cover (or after a restart cleared it).
func (s *Server) persistedChainStats(r *http.Request, since time.Time) map[string]health.ChainStat {
	rows, err := s.usage.ChainUsage(r.Context(), adminTenant, since)
	if err != nil {
		return nil
	}
	out := map[string]health.ChainStat{}
	for _, row := range rows {
		st := out[row.ChainID]
		st.ChainID = row.ChainID
		st.Requests += row.Requests
		st.Fallbacks += row.FellBack
		st.FinalFailures += row.Requests - row.Successes
		out[row.ChainID] = st
	}
	for id, st := range out {
		if st.Requests > 0 {
			st.FallbackRate = float64(st.Fallbacks) / float64(st.Requests)
		}
		out[id] = st
	}
	return out
}

// addSnapshotSummary folds one rollup into a running total; latency fields
// keep the worst value and the status rank keeps the worst status.
func addSnapshotSummary(total *store.SnapshotSummary, s store.SnapshotSummary) {
	total.Requests += s.Requests
	total.Successes += s.Successes
	total.Failures += s.Failures
	total.Fallbacks += s.Fallbacks
	total.FinalFailures += s.FinalFailures
	total.RateLimited += s.RateLimited
	total.AuthErrors += s.AuthErrors
	total.QuotaExceeded += s.QuotaExceeded
	total.Timeouts += s.Timeouts
	total.Provider5xx += s.Provider5xx
	total.NetworkErrors += s.NetworkErrors
	total.BadRequests += s.BadRequests
	total.Accounts += s.Accounts
	total.Models++
	if s.LatencyP95Ms > total.LatencyP95Ms {
		total.LatencyP95Ms = s.LatencyP95Ms
	}
	if s.TTFTP95Ms > total.TTFTP95Ms {
		total.TTFTP95Ms = s.TTFTP95Ms
	}
	if s.WorstStatusRank > total.WorstStatusRank {
		total.WorstStatusRank = s.WorstStatusRank
	}
	if s.LastBucket.After(total.LastBucket) {
		total.LastBucket = s.LastBucket
	}
}
