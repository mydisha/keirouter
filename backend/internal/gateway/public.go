package gateway

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// publicOverview serves GET /v1/public/overview. Aggregate-only: totals and a
// top-model leaderboard. No caller input, no identifiers, no model dispatch.
func (s *Server) publicOverview(w http.ResponseWriter, r *http.Request) {
	if s.cacheHit(w, "public-overview") {
		return
	}
	ctx := r.Context()
	since := sinceForPeriod("24h", "")
	summary, err := s.usage.SummarizeAccurate(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	models, err := s.usage.ByModelAccurate(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	top := topModelsByRequests(models, 10)
	// ponytail: rps is derived from the 24h total (avg), not a true 10s window.
	// Upgrade to a live counter if the landing needs a real instantaneous rate.
	rps := float64(summary.TotalRequests) / (24 * 3600)
	writeJSONCached(w, s.insightsCache, "public-overview", map[string]any{
		"total_requests": summary.TotalRequests,
		"total_tokens":   summary.PromptTokens + summary.CompletionTokens,
		"rps_10s":        rps,
		"success_24h":    summary.SuccessCount,
		"failed_24h":     summary.FailureCount,
		"top_models":     top,
	})
}

// publicModels serves GET /v1/public/models: models actually used in the last
// 24h, with per-1M list prices, capabilities, and aggregated usage. Only active
// models are listed; an idle gateway returns an empty array.
func (s *Server) publicModels(w http.ResponseWriter, r *http.Request) {
	if s.cacheHit(w, "public-models") {
		return
	}
	ctx := r.Context()
	since := sinceForPeriod("24h", "")
	usage, err := s.usage.ByModelAccurate(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	users, err := s.usage.DistinctKeysPerModel(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	sort.Slice(usage, func(i, j int) bool { return usage[i].TotalRequests > usage[j].TotalRequests })

	specs := make(map[string]connectors.ProviderModel)
	for _, pm := range connectors.ModelsByKind(core.ServiceLLM) {
		specs[pm.Provider+"\x00"+pm.Model.ID] = pm
	}

	out := make([]map[string]any, 0, len(usage))
	for _, m := range usage {
		if m.TotalRequests == 0 {
			continue
		}
		key := m.Provider + "\x00" + m.Model
		pm, ok := specs[key]
		if !ok {
			continue
		}
		caps, _ := capabilityPayload(pm.Provider, pm.Model.ID, core.ServiceLLM)
		display, _, _ := usageProviderMetadata(pm.Provider)
		out = append(out, map[string]any{
			"name":         pm.Model.Name,
			"model_id":     pm.Model.ID,
			"provider":     display,
			"input_per_m":  m.InputRatePerM,
			"output_per_m": m.OutputRatePerM,
			"capabilities": caps,
			"usage_24h": map[string]any{
				"users":    users[key],
				"requests": m.TotalRequests,
				"tokens":   m.PromptTokens + m.CompletionTokens,
			},
		})
	}
	writeJSONCached(w, s.insightsCache, "public-models", map[string]any{"models": out})
}

// publicPerformance serves GET /v1/public/performance?model=<id>. The only input
// is a model id matched against the last-24h aggregate set; unknown values are
// rejected before any lookup, so the parameter cannot address other objects.
func (s *Server) publicPerformance(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "unknown model")
		return
	}
	ctx := r.Context()
	since, to := sinceForPeriod("24h", ""), time.Now().UTC()
	models, err := s.usage.ByModelAccurate(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	var found *store.AccurateModelUsage
	for i := range models {
		if models[i].Model == model {
			found = &models[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusBadRequest, "unknown model")
		return
	}
	buckets, err := s.usage.TimelineAccurate(ctx, adminTenant, since, to, 24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	// AccurateTimeBucket carries a 0-based bucket index, not a timestamp. Fill
	// every slot so the client always gets a full 24-bar series; the client
	// labels the bars locally.
	series := make([]map[string]any, 24)
	for i := range series {
		series[i] = map[string]any{"bucket": i, "requests": int64(0), "tokens": int64(0)}
	}
	for _, b := range buckets {
		if b.Bucket >= 0 && b.Bucket < len(series) {
			series[b.Bucket] = map[string]any{
				"bucket":   b.Bucket,
				"requests": b.Requests,
				"tokens":   b.PromptTokens + b.CompletionTokens,
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":          model,
		"avg_latency_ms": found.AvgLatencyMS,
		"avg_ttft_ms":    found.AvgTTFTMS,
		"success_rate":   ratio(found.SuccessCount, found.TotalRequests),
		"series":         series,
	})
}

// topModelsByRequests maps aggregate model rows to the public leaderboard shape
// (display label + counts), sorted by request volume, capped at limit.
func topModelsByRequests(models []store.AccurateModelUsage, limit int) []map[string]any {
	sort.Slice(models, func(i, j int) bool { return models[i].TotalRequests > models[j].TotalRequests })
	if len(models) > limit {
		models = models[:limit]
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{
			"name":     m.Model,
			"requests": m.TotalRequests,
			"tokens":   m.PromptTokens + m.CompletionTokens,
		})
	}
	return out
}
