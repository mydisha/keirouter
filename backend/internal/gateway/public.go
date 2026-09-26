package gateway

import (
	"net/http"
	"sort"

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
	byModel := make(map[string]store.AccurateModelUsage, len(usage))
	for _, m := range usage {
		byModel[m.Provider+"\x00"+m.Model] = m
	}

	out := make([]map[string]any, 0, len(usage))
	for _, pm := range connectors.ModelsByKind(core.ServiceLLM) {
		key := pm.Provider + "\x00" + pm.Model.ID
		m, ok := byModel[key]
		if !ok || m.TotalRequests == 0 {
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
	sort.Slice(out, func(i, j int) bool {
		return out[i]["usage_24h"].(map[string]any)["requests"].(int64) >
			out[j]["usage_24h"].(map[string]any)["requests"].(int64)
	})
	writeJSONCached(w, s.insightsCache, "public-models", map[string]any{"models": out})
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
