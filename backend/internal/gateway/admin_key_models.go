package gateway

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// adminKeyModelUsage lists one API key's usage per model for the period, so
// an operator can audit which models a user called.
func (s *Server) adminKeyModelUsage(w http.ResponseWriter, r *http.Request) {
	key, err := s.identity.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	period := r.URL.Query().Get("period")
	since := sinceForPeriod(period, r.URL.Query().Get("tz"))
	models, err := s.usage.ModelUsageByKeySince(r.Context(), key.TenantID, key.ID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "failed to load model usage"))
		return
	}
	rows := make([]map[string]any, 0, len(models))
	for _, m := range models {
		display, _, icon := usageProviderMetadata(m.Provider)
		rows = append(rows, map[string]any{
			"provider":          m.Provider,
			"provider_name":     display,
			"provider_icon":     icon,
			"model":             m.Model,
			"requests":          m.Requests,
			"success_requests":  m.SuccessCount,
			"failed_requests":   m.FailureCount,
			"blocked_requests":  m.BlockedCount,
			"prompt_tokens":     m.PromptTokens,
			"completion_tokens": m.CompletionTokens,
			"cached_tokens":     m.CachedTokens,
			"reasoning_tokens":  m.ReasoningTokens,
			"total_tokens":      m.PromptTokens + m.CompletionTokens,
			"cost_usd":          nanosToUSD(m.CostNanos),
			"avg_latency_ms":    m.AvgLatencyMS,
			"last_used_at":      m.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"period": period, "since": since, "models": rows})
}
