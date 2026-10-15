package gateway

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/health"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// mountProviderHealth registers the actionable provider health dashboard API.
// These sit alongside the existing /health/accounts endpoints and reuse the
// same admin auth + loopback middleware.
func (s *Server) mountProviderHealth(r chi.Router) {
	r.Get("/health/overview", s.adminHealthOverview)
	r.Get("/health/timeline", s.adminHealthTimeline)
	r.Get("/health/providers/{provider}", s.adminHealthProviderDetail)
	r.Get("/health/models", s.adminHealthModels)
	r.Get("/health/chains", s.adminHealthChains)
	r.Get("/health/chains/{id}", s.adminHealthChainDetail)
	r.Get("/health/probes", s.adminHealthProbeHistory)
	r.Post("/health/probes/run", s.adminHealthRunProbe)
}

// parseRange resolves a ?range= duration (e.g. 1h, 24h) to a since time.
// Defaults to 1h. Caps at 30d to bound snapshot scans.
func parseRange(raw string) time.Time {
	d := parseRangeDuration(raw)
	if d <= 0 {
		d = time.Hour
	}
	if d > 30*24*time.Hour {
		d = 30 * 24 * time.Hour
	}
	return time.Now().Add(-d)
}

func parseRangeDuration(raw string) time.Duration {
	switch raw {
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h", "":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return time.Hour
}

// adminHealthOverview returns summary cards + a per-provider status table.
// provider_health_current is computed by the telemetry service over its
// configured rolling window. The response publishes that effective window so
// callers never mistake an arbitrary query range for historical aggregation.
func (s *Server) adminHealthOverview(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "provider health not configured")
		return
	}
	statusFilter := r.URL.Query().Get("status")
	requestedRange := r.URL.Query().Get("range")
	if s.healthRangeIsHistorical(requestedRange) {
		s.historicalHealthOverview(w, r, requestedRange, statusFilter)
		return
	}

	rows, err := s.db.ProviderHealth().ListCurrent(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	// Aggregate account/model/capability keys into one truthful provider row.
	type provAgg struct {
		provider       string
		status         string
		score          int
		accounts       map[string]struct{}
		models         map[string]struct{}
		requests       int64
		successes      float64
		failures       float64
		fallbacks      int64
		latencyP95     int
		ttftP95        int
		lastProbe      *time.Time
		mainIssue      string
		recommendation string
	}
	byProvider := map[string]*provAgg{}
	for _, current := range rows {
		agg, ok := byProvider[current.Provider]
		if !ok {
			agg = &provAgg{
				provider: current.Provider,
				accounts: map[string]struct{}{},
				models:   map[string]struct{}{},
				score:    current.HealthScore,
				status:   current.HealthStatus,
			}
			if current.MainIssue != nil {
				agg.mainIssue = *current.MainIssue
			}
			if current.Recommendation != nil {
				agg.recommendation = *current.Recommendation
			}
			byProvider[current.Provider] = agg
		} else {
			replaceIssue := rank(current.HealthStatus) > rank(agg.status) || current.HealthScore < agg.score
			if rank(current.HealthStatus) > rank(agg.status) {
				agg.status = current.HealthStatus
			}
			if current.HealthScore < agg.score {
				agg.score = current.HealthScore
			}
			if current.MainIssue != nil && *current.MainIssue != "" && (agg.mainIssue == "" || replaceIssue) {
				agg.mainIssue = *current.MainIssue
				if current.Recommendation != nil {
					agg.recommendation = *current.Recommendation
				}
			}
		}
		if current.ProviderAccountID != "" {
			agg.accounts[current.ProviderAccountID] = struct{}{}
		}
		if current.Model != "" {
			agg.models[current.Model] = struct{}{}
		}
		agg.requests += current.RequestCount
		agg.successes += float64(current.RequestCount) * current.SuccessRate
		agg.failures += float64(current.RequestCount) * current.ErrorRate
		agg.fallbacks += current.FallbackCount
		if current.LatencyP95Ms != nil && *current.LatencyP95Ms > agg.latencyP95 {
			agg.latencyP95 = *current.LatencyP95Ms
		}
		if current.TTFTP95Ms != nil && *current.TTFTP95Ms > agg.ttftP95 {
			agg.ttftP95 = *current.TTFTP95Ms
		}
		if current.LastProbeAt != nil && (agg.lastProbe == nil || current.LastProbeAt.After(*agg.lastProbe)) {
			agg.lastProbe = current.LastProbeAt
		}
	}

	providerIDs := make([]string, 0, len(byProvider))
	for provider := range byProvider {
		providerIDs = append(providerIDs, provider)
	}
	sort.Strings(providerIDs)

	summary := map[string]int64{
		"healthy": 0, "degraded": 0, "unhealthy": 0, "unknown": 0, "disabled": 0,
		"fallbacks": 0,
	}
	if s.providerHealth != nil {
		summary["telemetry_dropped"] = int64(s.providerHealth.DroppedEvents())
	}
	providers := make([]map[string]any, 0, len(byProvider))
	var totalProviderP95 int
	var providerP95Count int
	for _, provider := range providerIDs {
		agg := byProvider[provider]
		summary[agg.status]++
		summary["fallbacks"] += agg.fallbacks
		if agg.latencyP95 > 0 {
			totalProviderP95 += agg.latencyP95
			providerP95Count++
		}
		if statusFilter != "" && agg.status != statusFilter {
			continue
		}
		successRate, errorRate := 0.0, 0.0
		if agg.requests > 0 {
			successRate = agg.successes / float64(agg.requests) * 100
			errorRate = agg.failures / float64(agg.requests) * 100
		}
		entry := map[string]any{
			"provider":         agg.provider,
			"status":           agg.status,
			"score":            agg.score,
			"accounts":         len(agg.accounts),
			"models_monitored": len(agg.models),
			"success_rate":     successRate,
			"error_rate":       errorRate,
			"latency_p95_ms":   agg.latencyP95,
			"ttft_p95_ms":      agg.ttftP95,
			"fallback_count":   agg.fallbacks,
			"main_issue":       agg.mainIssue,
			"recommendation":   agg.recommendation,
		}
		if agg.lastProbe != nil {
			entry["last_probe_at"] = *agg.lastProbe
		}
		providers = append(providers, entry)
	}

	avgP95 := 0
	if providerP95Count > 0 {
		avgP95 = totalProviderP95 / providerP95Count
	}
	generatedAt := time.Now().UTC()
	windowDuration := time.Duration(0)
	if s.providerHealth != nil {
		windowDuration = s.providerHealth.RollingWindow()
	}
	window := map[string]any{
		"kind":             "rolling_current",
		"duration_seconds": int64(windowDuration.Seconds()),
		"requested_range":  requestedRange,
		"generated_at":     generatedAt,
	}
	if windowDuration > 0 {
		window["since"] = generatedAt.Add(-windowDuration)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"window": window,
		"summary": map[string]any{
			"healthy":                 summary["healthy"],
			"degraded":                summary["degraded"],
			"unhealthy":               summary["unhealthy"],
			"unknown":                 summary["unknown"],
			"disabled":                summary["disabled"],
			"fallbacks":               summary["fallbacks"],
			"avg_p95_latency_ms":      avgP95,
			"telemetry_dropped":       summary["telemetry_dropped"],
			"telemetry_dropped_scope": "process_lifetime",
		},
		"providers": providers,
	})
}

// adminHealthProviderDetail returns detailed metrics + snapshots for one provider.
// Short ranges read the live rolling window; longer ranges — or any range when
// the live window is empty, e.g. right after a restart — are aggregated from
// persisted snapshots. Model rows always share one normalized shape (status,
// percentages) whichever source produced them.
func (s *Server) adminHealthProviderDetail(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	requestedRange := r.URL.Query().Get("range")
	since := parseRange(requestedRange)

	rows, err := s.db.ProviderHealth().ListCurrentByProvider(r.Context(), provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	snaps, _ := s.db.ProviderHealth().ListSnapshots(r.Context(), provider, "", "", "", since)

	errBreakdown := map[string]int64{}
	for _, sn := range snaps {
		errBreakdown["rate_limited"] += sn.RateLimitedCount
		errBreakdown["auth_error"] += sn.AuthErrorCount
		errBreakdown["quota_exceeded"] += sn.QuotaExceededCount
		errBreakdown["timeout"] += sn.TimeoutCount
		errBreakdown["provider_5xx"] += sn.Provider5xxCount
		errBreakdown["bad_request"] += sn.BadRequestCount
		errBreakdown["network_error"] += sn.NetworkErrorCount
	}

	if s.healthRangeIsHistorical(requestedRange) || len(rows) == 0 {
		sums, err := s.db.ProviderHealth().SummarizeSnapshots(r.Context(), since, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		live := liveStatusByProvider(rows, true)
		var total store.SnapshotSummary
		total.Provider = provider
		models := []map[string]any{}
		for _, sum := range sums {
			if sum.Provider != provider {
				continue
			}
			addSnapshotSummary(&total, sum)
			entry := snapshotRow(sum, live[sum.Provider+"\x00"+sum.Model])
			entry["model"] = sum.Model
			entry["last_updated_at"] = sum.LastBucket
			models = append(models, entry)
		}
		if total.Requests == 0 && len(rows) == 0 {
			writeError(w, http.StatusNotFound, "no health data for provider: "+provider)
			return
		}
		status, score := snapshotStatus(total)
		if cur := liveStatusByProvider(rows, false)[provider]; cur != "" {
			status = cur
		}
		issue := snapshotIssue(total)
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":       provider,
			"status":         status,
			"score":          score,
			"main_issue":     issue,
			"recommendation": health.RecommendationForIssue(issue),
			"metrics": map[string]any{
				"requests":       total.Requests,
				"success_rate":   pct(total.Successes, total.Requests),
				"error_rate":     pct(total.Failures, total.Requests),
				"latency_p95_ms": total.LatencyP95Ms,
				"ttft_p95_ms":    total.TTFTP95Ms,
				"fallback_count": total.Fallbacks,
			},
			"error_breakdown": errBreakdown,
			"models":          models,
			"snapshots":       snaps,
		})
		return
	}

	var requests, fallbacks int64
	var successes, failures int64
	var score int = 100
	status := health.StatusHealthy
	var mainIssue, recommendation string
	var lp95, ttft95 int
	models := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		requests += c.RequestCount
		successes += int64(float64(c.RequestCount) * c.SuccessRate)
		failures += int64(float64(c.RequestCount) * c.ErrorRate)
		fallbacks += c.FallbackCount
		if c.HealthScore < score {
			score = c.HealthScore
		}
		if rank(c.HealthStatus) > rank(status) {
			status = c.HealthStatus
		}
		if c.MainIssue != nil && *c.MainIssue != "" && mainIssue == "" {
			mainIssue = *c.MainIssue
		}
		if c.Recommendation != nil && *c.Recommendation != "" && recommendation == "" {
			recommendation = *c.Recommendation
		}
		if c.LatencyP95Ms != nil && *c.LatencyP95Ms > lp95 {
			lp95 = *c.LatencyP95Ms
		}
		if c.TTFTP95Ms != nil && *c.TTFTP95Ms > ttft95 {
			ttft95 = *c.TTFTP95Ms
		}
		models = append(models, currentModelRow(c))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider":       provider,
		"status":         status,
		"score":          score,
		"main_issue":     mainIssue,
		"recommendation": recommendation,
		"metrics": map[string]any{
			"requests":       requests,
			"success_rate":   pct(successes, requests),
			"error_rate":     pct(failures, requests),
			"latency_p95_ms": lp95,
			"ttft_p95_ms":    ttft95,
			"fallback_count": fallbacks,
		},
		"error_breakdown": errBreakdown,
		"models":          models,
		"snapshots":       snaps,
	})
}

// currentModelRow normalizes a live current-window row to the model row shape
// the dashboard reads (status + percentages), matching snapshotRow.
func currentModelRow(c store.ProviderHealthCurrent) map[string]any {
	entry := map[string]any{
		"provider":            c.Provider,
		"provider_account_id": c.ProviderAccountID,
		"model":               c.Model,
		"capability":          c.Capability,
		"status":              c.HealthStatus,
		"score":               c.HealthScore,
		"requests":            c.RequestCount,
		"success_rate":        c.SuccessRate * 100,
		"error_rate":          c.ErrorRate * 100,
		"fallback_count":      c.FallbackCount,
		"last_updated_at":     c.LastUpdatedAt,
	}
	if c.LatencyP95Ms != nil {
		entry["latency_p95_ms"] = *c.LatencyP95Ms
	}
	if c.TTFTP95Ms != nil {
		entry["ttft_p95_ms"] = *c.TTFTP95Ms
	}
	if c.MainIssue != nil {
		entry["main_issue"] = *c.MainIssue
	}
	return entry
}

// adminHealthModels returns the model-level health matrix.
func (s *Server) adminHealthModels(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	if requestedRange := r.URL.Query().Get("range"); s.healthRangeIsHistorical(requestedRange) {
		s.historicalHealthModels(w, r, requestedRange, statusFilter)
		return
	}
	rows, err := s.db.ProviderHealth().ListCurrent(r.Context(), statusFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		if c.Model == "" {
			continue
		}
		entry := map[string]any{
			"provider":        c.Provider,
			"model":           c.Model,
			"capability":      c.Capability,
			"status":          c.HealthStatus,
			"score":           c.HealthScore,
			"success_rate":    pct(int64(float64(c.RequestCount)*c.SuccessRate), c.RequestCount),
			"error_rate":      pct(int64(float64(c.RequestCount)*c.ErrorRate), c.RequestCount),
			"fallback_count":  c.FallbackCount,
			"last_updated_at": c.LastUpdatedAt,
		}
		if c.LatencyP95Ms != nil {
			entry["latency_p95_ms"] = *c.LatencyP95Ms
		}
		if c.TTFTP95Ms != nil {
			entry["ttft_p95_ms"] = *c.TTFTP95Ms
		}
		if c.MainIssue != nil {
			entry["main_issue"] = *c.MainIssue
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// adminHealthChains returns chain health: fallback rate, final failures, and
// affected providers, derived from real-traffic telemetry joined with chain
// config + current provider health.
func (s *Server) adminHealthChains(w http.ResponseWriter, r *http.Request) {
	chains, err := s.chains.ListByTenant(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	healthRows, _ := s.db.ProviderHealth().ListCurrent(r.Context(), "")
	healthByKey := map[string]store.ProviderHealthCurrent{}
	for _, h := range healthRows {
		healthByKey[store.HealthKey(h.Provider, h.ProviderAccountID, h.Model, h.Capability)] = h
	}

	// Real-traffic chain stats: the telemetry aggregator's rolling window for
	// short ranges, persisted usage rows for longer ones.
	chainStats := map[string]health.ChainStat{}
	if requestedRange := r.URL.Query().Get("range"); s.healthRangeIsHistorical(requestedRange) {
		chainStats = s.persistedChainStats(r, parseRange(requestedRange))
	} else if s.providerHealth != nil {
		for _, st := range s.providerHealth.ChainStats() {
			chainStats[st.ChainID] = st
		}
	}

	out := make([]map[string]any, 0, len(chains))
	for _, c := range chains {
		worstStatus := health.StatusHealthy
		var affectedProvider, affectedModel, mainIssue string
		for _, step := range c.Steps {
			// Match any account/capability for this provider+model.
			for _, h := range healthRows {
				if h.Provider == step.Provider && h.Model == step.Model {
					if rank(h.HealthStatus) > rank(worstStatus) {
						worstStatus = h.HealthStatus
						affectedProvider = step.Provider
						affectedModel = step.Model
						if h.MainIssue != nil {
							mainIssue = *h.MainIssue
						}
					}
				}
			}
		}
		entry := map[string]any{
			"chain_id":          c.ID,
			"name":              c.Name,
			"status":            worstStatus,
			"affected_provider": affectedProvider,
			"affected_model":    affectedModel,
			"main_issue":        mainIssue,
			"step_count":        len(c.Steps),
		}
		if st, ok := chainStats[c.ID]; ok {
			entry["requests"] = st.Requests
			entry["fallback_rate"] = st.FallbackRate * 100
			entry["final_failure_count"] = st.FinalFailures
			entry["fallback_count"] = st.Fallbacks
		} else {
			entry["requests"] = 0
			entry["fallback_rate"] = 0.0
			entry["final_failure_count"] = 0
			entry["fallback_count"] = 0
		}
		entry["recommendation"] = chainRecommendation(worstStatus, mainIssue, affectedProvider, affectedModel)
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chains": out})
}

// adminHealthChainDetail returns step-level health + usage for one chain.
func (s *Server) adminHealthChainDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := s.chains.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "chain not found")
		return
	}
	healthRows, _ := s.db.ProviderHealth().ListCurrent(r.Context(), "")
	chainStats := map[string]health.ChainStat{}
	if s.providerHealth != nil {
		for _, st := range s.providerHealth.ChainStats() {
			chainStats[st.ChainID] = st
		}
	}

	steps := make([]map[string]any, 0, len(c.Steps))
	for _, step := range c.Steps {
		var status string = health.StatusUnknown
		var mainIssue string
		var score int = 100
		for _, h := range healthRows {
			if h.Provider == step.Provider && h.Model == step.Model {
				if rank(h.HealthStatus) > rank(status) || status == health.StatusUnknown {
					status = h.HealthStatus
					score = h.HealthScore
					if h.MainIssue != nil {
						mainIssue = *h.MainIssue
					}
				}
			}
		}
		steps = append(steps, map[string]any{
			"position":   step.Position,
			"provider":   step.Provider,
			"model":      step.Model,
			"status":     status,
			"score":      score,
			"main_issue": mainIssue,
		})
	}

	resp := map[string]any{
		"chain_id": c.ID,
		"name":     c.Name,
		"strategy": c.Strategy,
		"steps":    steps,
	}
	if st, ok := chainStats[c.ID]; ok {
		resp["requests"] = st.Requests
		resp["fallback_rate"] = st.FallbackRate * 100
		resp["final_failure_count"] = st.FinalFailures
		resp["fallback_count"] = st.Fallbacks
	}
	if c.FallbackProvider != "" {
		resp["fallback_provider"] = c.FallbackProvider
		resp["fallback_model"] = c.FallbackModel
	}
	writeJSON(w, http.StatusOK, resp)
}

// chainRecommendation derives a next-step suggestion from a chain's worst
// provider status and the affected provider/model.
func chainRecommendation(status, mainIssue, provider, model string) string {
	switch status {
	case health.StatusUnhealthy, health.StatusDegraded:
		return "Move a healthy fallback provider above " + provider + "/" + model + " temporarily, or add capacity."
	case health.StatusUnknown:
		return "Run a manual probe on " + provider + "/" + model + " to populate health data."
	}
	_ = mainIssue
	return ""
}

// adminHealthProbeHistory returns paginated probe results.
func (s *Server) adminHealthProbeHistory(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	since := parseRange(r.URL.Query().Get("range"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if limit <= 0 {
		limit = 50
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	items, total, err := s.db.ProviderHealth().ListProbeResults(r.Context(), provider, since, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, p := range items {
		entry := map[string]any{
			"time":                p.CreatedAt,
			"provider":            p.Provider,
			"provider_account_id": p.ProviderAccountID,
			"model":               p.Model,
			"capability":          p.Capability,
			"status":              p.Status,
			"triggered_by":        p.TriggeredBy,
		}
		if p.HTTPStatus != nil {
			entry["http_status"] = *p.HTTPStatus
		}
		if p.LatencyMs != nil {
			entry["latency_ms"] = *p.LatencyMs
		}
		if p.TTFTMs != nil {
			entry["ttft_ms"] = *p.TTFTMs
		}
		if p.ErrorType != nil {
			entry["error_type"] = *p.ErrorType
		}
		if p.ErrorMessage != nil {
			entry["error_message"] = *p.ErrorMessage
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"pagination": map[string]int{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

// adminHealthRunProbe triggers a manual synthetic probe.
func (s *Server) adminHealthRunProbe(w http.ResponseWriter, r *http.Request) {
	if s.probeRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "probe runner not configured")
		return
	}
	var body struct {
		Provider          string `json:"provider"`
		ProviderAccountID string `json:"provider_account_id"`
		Model             string `json:"model"`
		Capability        string `json:"capability"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Provider == "" || body.Model == "" {
		writeError(w, http.StatusBadRequest, "provider and model are required")
		return
	}
	res, err := s.probeRunner.Run(r.Context(), health.ProbeRequest{
		Provider:          body.Provider,
		ProviderAccountID: body.ProviderAccountID,
		Model:             body.Model,
		Capability:        body.Capability,
		TriggeredBy:       "manual",
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := map[string]any{
		"status":   res.Status,
		"provider": res.Provider,
		"model":    res.Model,
		"message":  "Probe completed successfully.",
	}
	if res.HTTPStatus != nil {
		resp["http_status"] = *res.HTTPStatus
	}
	if res.LatencyMs != nil {
		resp["latency_ms"] = *res.LatencyMs
	}
	if res.TTFTMs != nil {
		resp["ttft_ms"] = *res.TTFTMs
	}
	if res.ErrorType != nil {
		resp["error_type"] = *res.ErrorType
	}
	if res.ErrorMessage != nil {
		resp["message"] = *res.ErrorMessage
	}
	writeJSON(w, http.StatusOK, resp)
}

// rank orders statuses so the worst one wins in a rollup.
func rank(status string) int {
	switch status {
	case health.StatusDisabled:
		return 5
	case health.StatusUnhealthy:
		return 4
	case health.StatusDegraded:
		return 3
	case health.StatusUnknown:
		return 2
	case health.StatusHealthy:
		return 1
	}
	return 0
}

func pct(n, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
