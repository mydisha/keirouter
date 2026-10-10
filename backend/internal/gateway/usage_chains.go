package gateway

import (
	"net/http"
	"sort"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// adminChainUsage reports, per routing chain, where its traffic actually
// landed over the period: requests served by each step, how often the first
// choice failed over, success rate and spend. Unlike /health/chains (an
// in-memory 15-minute window) this is computed from persisted usage rows.
func (s *Server) adminChainUsage(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	cacheKey := "chain-usage|" + period + "|" + tz
	if s.cacheHit(w, cacheKey) {
		return
	}
	ctx := r.Context()
	since := sinceForPeriod(period, tz)

	chains, err := s.chains.ListByTenant(ctx, adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	served, err := s.usage.ChainUsage(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	byChain := map[string][]store.ChainServedUsage{}
	for _, row := range served {
		byChain[row.ChainID] = append(byChain[row.ChainID], row)
	}

	out := make([]map[string]any, 0, len(chains))
	for _, c := range chains {
		out = append(out, chainUsageRow(c, byChain[c.ID]))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["requests"].(int64) > out[j]["requests"].(int64)
	})
	writeJSONCached(w, s.insightsCache, cacheKey, map[string]any{
		"period": period,
		"since":  since,
		"chains": out,
	})
}

// chainUsageRow folds the per-target rollups onto the chain's declared steps.
// Targets that served traffic but are no longer steps (the chain was edited,
// or the configured last-resort fallback model) are reported separately.
func chainUsageRow(c store.Chain, rows []store.ChainServedUsage) map[string]any {
	type stepAgg struct {
		position                      int
		provider, model               string
		requests, successes, fellBack int64
		costNanos                     int64
	}
	steps := make([]*stepAgg, 0, len(c.Steps)+1)
	index := map[string]*stepAgg{}
	ordered := append([]store.ChainStep(nil), c.Steps...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	for i, st := range ordered {
		agg := &stepAgg{position: i, provider: st.Provider, model: st.Model}
		steps = append(steps, agg)
		index[st.Provider+"/"+st.Model] = agg
	}
	if c.FallbackProvider != "" && c.FallbackModel != "" {
		if _, dup := index[c.FallbackProvider+"/"+c.FallbackModel]; !dup {
			agg := &stepAgg{position: len(steps), provider: c.FallbackProvider, model: c.FallbackModel}
			steps = append(steps, agg)
			index[c.FallbackProvider+"/"+c.FallbackModel] = agg
		}
	}

	var total, successes, fellBack, costNanos int64
	other := []map[string]any{}
	for _, row := range rows {
		total += row.Requests
		successes += row.Successes
		fellBack += row.FellBack
		costNanos += row.CostNanos
		if agg, ok := index[row.Provider+"/"+row.Model]; ok {
			agg.requests += row.Requests
			agg.successes += row.Successes
			agg.fellBack += row.FellBack
			agg.costNanos += row.CostNanos
			continue
		}
		other = append(other, map[string]any{
			"provider": row.Provider, "model": row.Model, "requests": row.Requests,
		})
	}
	for _, o := range other {
		o["share"] = ratio(o["requests"].(int64), total)
	}

	stepRows := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		stepRows = append(stepRows, map[string]any{
			"position":     st.position,
			"provider":     st.provider,
			"model":        st.model,
			"is_fallback":  c.FallbackProvider == st.provider && c.FallbackModel == st.model && st.position >= len(c.Steps),
			"requests":     st.requests,
			"share":        ratio(st.requests, total),
			"success_rate": ratio(st.successes, st.requests),
			"cost_usd":     nanosToUSD(st.costNanos),
		})
	}
	return map[string]any{
		"chain_id":          c.ID,
		"name":              c.Name,
		"strategy":          c.Strategy,
		"requests":          total,
		"success_rate":      ratio(successes, total),
		"fallback_requests": fellBack,
		"fallback_rate":     ratio(fellBack, total),
		"cost_usd":          nanosToUSD(costNanos),
		"steps":             stepRows,
		"other_targets":     other,
	}
}
