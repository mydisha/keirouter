package gateway

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// publicOverview serves GET /v1/public/overview. Aggregate-only all-time totals
// plus the size of the available model catalogue. No caller input, no
// identifiers, no model dispatch.
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

// publicModelRow is one available catalogue model, joined with its all-time
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

// publicModelRows lists every priced or used LLM in the catalogue, with all-time
// usage merged in. Used models sort first by request volume, then the remaining
// priced catalogue alphabetically. Purely aggregate; safe to publish.
func (s *Server) publicModelRows(ctx context.Context) ([]publicModelRow, error) {
	usage, err := s.usage.ByModelAccurate(ctx, adminTenant, time.Time{})
	if err != nil {
		return nil, err
	}
	users, err := s.usage.DistinctKeysPerModel(ctx, adminTenant, time.Time{})
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]store.AccurateModelUsage, len(usage))
	for _, m := range usage {
		byKey[m.Provider+"\x00"+m.Model] = m
	}

	// One card per model id: the same model offered by several providers is a
	// single catalogue entry. First provider in catalogue order wins.
	seenModel := make(map[string]bool)
	rows := make([]publicModelRow, 0, len(usage))
	for _, pm := range connectors.ModelsByKind(core.ServiceLLM) {
		if seenModel[pm.Model.ID] {
			continue
		}
		key := pm.Provider + "\x00" + pm.Model.ID
		u := byKey[key]
		price, priced := connectors.ModelPriceByProviderModel(pm.Provider, pm.Model.ID)
		if u.TotalRequests == 0 && (!priced || (price.InputPerM <= 0 && price.OutputPerM <= 0)) {
			continue
		}
		seenModel[pm.Model.ID] = true
		display, _, _ := usageProviderMetadata(pm.Provider)
		rows = append(rows, publicModelRow{
			Name:       pm.Model.Name,
			ModelID:    pm.Model.ID,
			Provider:   display,
			ProviderID: pm.Provider,
			InputPerM:  price.InputPerM,
			OutputPerM: price.OutputPerM,
			CachedPerM: price.CachedInputPerM,
			CacheWrite: price.CacheWritePerM,
			Requests:   u.TotalRequests,
			Tokens:     u.PromptTokens + u.CompletionTokens,
			Users:      users[key],
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

// publicModels serves GET /v1/public/models: the available LLM catalogue (every
// priced model, plus any model with recorded usage) with per-1M list prices,
// capabilities, and all-time aggregated usage. Aggregate only.
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
