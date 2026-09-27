package gateway

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
)

// publicOverview serves GET /v1/public/overview. Aggregate-only all-time totals
// plus the number of published routing chains. No caller input, no identifiers,
// no model dispatch.
func (s *Server) publicOverview(w http.ResponseWriter, r *http.Request) {
	if s.cacheHit(w, "public-overview") {
		return
	}
	ctx := r.Context()
	// The landing headline cards are all-time, not a rolling 24h window.
	summary, err := s.usage.SummarizeAccurate(ctx, adminTenant, time.Time{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	rows, err := s.publicModelRows(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	writeJSONCached(w, s.insightsCache, "public-overview", map[string]any{
		"total_requests": summary.TotalRequests,
		"total_tokens":   summary.PromptTokens + summary.CompletionTokens,
		"success":        summary.SuccessCount,
		"failed":         summary.FailureCount,
		"model_count":    len(rows),
	})
}

// publicModelRow is one published routing chain, joined with its all-time
// aggregate usage. It carries no caller or account identifier.
type publicModelRow struct {
	Name       string
	ModelID    string
	Provider   string
	ProviderID string
	InputPerM  float64
	OutputPerM float64
	CachedPerM float64
	CacheWrite float64
	Requests   int64
	Tokens     int64
	Users      int
}

// publicModelRows lists one entry per routing chain, with all-time usage
// attributed by chain_id. Chains without steps are skipped (nothing routable to
// advertise). Rows sort by request volume, then name. Purely aggregate; safe to
// publish.
func (s *Server) publicModelRows(ctx context.Context) ([]publicModelRow, error) {
	chains, err := s.chains.ListByTenant(ctx, adminTenant)
	if err != nil {
		return nil, err
	}
	usage, err := s.usage.ChainUsageAccurate(ctx, adminTenant, time.Time{})
	if err != nil {
		return nil, err
	}
	rows := make([]publicModelRow, 0, len(chains))
	for _, c := range chains {
		if len(c.Steps) == 0 {
			continue
		}
		first := c.Steps[0]
		price, _ := connectors.ModelPriceByProviderModel(first.Provider, first.Model)
		u := usage[c.ID]
		rows = append(rows, publicModelRow{
			Name:       c.Name,
			ModelID:    c.Name,
			Provider:   "combo",
			ProviderID: "combo",
			InputPerM:  price.InputPerM,
			OutputPerM: price.OutputPerM,
			CachedPerM: price.CachedInputPerM,
			CacheWrite: price.CacheWritePerM,
			Requests:   u.TotalRequests,
			Tokens:     u.PromptTokens + u.CompletionTokens,
			Users:      u.DistinctKeys,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// publicModels serves GET /v1/public/models: one entry per routing chain, with
// per-1M list prices (from the chain's first step), capabilities, and all-time
// usage aggregated by chain_id. Aggregate only.
func (s *Server) publicModels(w http.ResponseWriter, r *http.Request) {
	if s.cacheHit(w, "public-models") {
		return
	}
	rows, err := s.publicModelRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		caps, _ := capabilityPayload(m.ProviderID, m.ModelID, core.ServiceLLM)
		out = append(out, map[string]any{
			"name":              m.Name,
			"model_id":          m.ModelID,
			"provider":          m.Provider,
			"provider_id":       m.ProviderID,
			"input_per_m":       m.InputPerM,
			"output_per_m":      m.OutputPerM,
			"cached_per_m":      m.CachedPerM,
			"cache_write_per_m": m.CacheWrite,
			"capabilities":      caps,
			"usage": map[string]any{
				"users":    m.Users,
				"requests": m.Requests,
				"tokens":   m.Tokens,
			},
		})
	}
	writeJSONCached(w, s.insightsCache, "public-models", map[string]any{"models": out})
}
