package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/usagehub"
)

// sinceForPeriod maps a dashboard period query value to a lower-bound time.
// The tz parameter is an IANA timezone string (e.g. "Asia/Jakarta") sent by the
// browser so that "today" means midnight in the user's local time, not the
// server's. Falls back to the server's local time when tz is empty.
func sinceForPeriod(period, tz string) time.Time {
	loc := time.Local
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	now := time.Now().In(loc)
	switch period {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()
	case "24h":
		return time.Now().UTC().Add(-24 * time.Hour)
	case "week":
		return now.AddDate(0, 0, -7).UTC()
	case "month", "":
		return now.AddDate(0, -1, 0).UTC()
	default:
		return now.AddDate(0, 0, -30).UTC()
	}
}

// ---- usage insights ---------------------------------------------------------

// adminUsageInsights returns the rich payload that powers the Usage page: the
// per-provider routing breakdown, a bucketed activity-over-time series, recent
// activity rows, and headline metrics (success rate, average latency).
func (s *Server) adminUsageInsights(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	cacheKey := "insights|" + period + "|" + tz
	if s.cacheHit(w, cacheKey) {
		return
	}
	since := sinceForPeriod(period, tz)
	ctx := r.Context()

	// Run all independent queries concurrently to reduce latency
	// from sum(sequential) to max(parallel).
	var (
		sum           store.Summary
		breakdown     []store.ProviderUsage
		recent        []store.RecentRecord
		timeline      []store.TimeBucket
		ruleSavings     []store.RuleSavings
		clientSavings   []store.ClientSavings
		headroomSavings []store.HeadroomTransformSavings
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		sum, err = s.usage.Summarize(gctx, adminTenant, since)
		return err
	})
	g.Go(func() error {
		var err error
		breakdown, err = s.usage.Breakdown(gctx, adminTenant, since)
		return err
	})
	g.Go(func() error {
		var err error
		recent, err = s.usage.Recent(gctx, adminTenant, 8)
		return err
	})
	g.Go(func() error {
		var err error
		timeline, err = s.usage.Timeline(gctx, adminTenant, since, time.Now(), 24)
		return err
	})
	g.Go(func() error {
		clientSavings, _ = s.usage.SavingsByClient(gctx, adminTenant, since)
		return nil
	})
	g.Go(func() error {
		headroomSavings, _ = s.usage.SavingsByHeadroomTransform(gctx, adminTenant, since)
		return nil
	})
	if err := g.Wait(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sum.SlimBytesSaved > 0 {
		ruleSavings, _ = s.usage.SavingsByRule(ctx, adminTenant, since)
	}

	// Per-provider breakdown with shares of total request volume. Decorate each
	// with the provider's display name/color from the connector catalog.
	providers := make([]map[string]any, 0, len(breakdown))
	for _, p := range breakdown {
		share := 0.0
		if sum.TotalRequests > 0 {
			share = float64(p.TotalRequests) / float64(sum.TotalRequests) * 100
		}
		display, color, icon := p.Provider, "", ""
		if spec, ok := connectors.SpecByID(p.Provider); ok {
			display = spec.DisplayName
			color = spec.Color
			icon = "/providers/" + spec.ID + ".png"
		}
		providers = append(providers, map[string]any{
			"provider":          p.Provider,
			"display_name":      display,
			"color":             color,
			"icon":              icon,
			"total_requests":    p.TotalRequests,
			"prompt_tokens":     p.PromptTokens,
			"completion_tokens": p.CompletionTokens,
			"cost_usd":          float64(p.CostMicros) / 1_000_000,
			"share_pct":         share,
		})
	}

	// Recent activity rows.
	recentRows := make([]map[string]any, 0, len(recent))
	for _, rec := range recent {
		entry := map[string]any{
			"id":         rec.ID,
			"provider":   rec.Provider,
			"model":      rec.Model,
			"tokens":     rec.PromptTokens + rec.CompletionTokens,
			"cost_usd":   float64(rec.CostMicros) / 1_000_000,
			"cache_hit":  rec.CacheHit,
			"latency_ms": rec.LatencyMS,
			"created_at": rec.CreatedAt,
		}
		if rec.TTFTMS > 0 {
			entry["ttft_ms"] = rec.TTFTMS
		}
		if rec.SlimBytesSaved > 0 {
			entry["slim_bytes_saved"] = rec.SlimBytesSaved
			entry["slim_tokens_saved"] = rec.SlimTokensSaved
		}
		if rec.SlimRules != "" {
			entry["slim_rules"] = rec.SlimRules
		}
		if rec.HeadroomActive {
			entry["headroom_active"] = true
			entry["headroom_tokens_saved"] = rec.HeadroomTokensSaved
		}
		if rec.HeadroomTransforms != "" {
			entry["headroom_transforms"] = rec.HeadroomTransforms
		}
		if rec.CavemanActive {
			entry["caveman_active"] = true
		}
		if rec.TerseActive {
			entry["terse_active"] = true
		}
		recentRows = append(recentRows, entry)
	}

	// Bucket the timeline into 24 even slots across the window for the sparkline.
	buckets := bucketTimeline(timeline, since, time.Now(), 24)
	busiestIdx, busiestCount := 0, int64(0)
	for i, b := range buckets {
		if b.count > busiestCount {
			busiestCount, busiestIdx = b.count, i
		}
	}
	series := make([]map[string]any, 0, len(buckets))
	for _, b := range buckets {
		series = append(series, map[string]any{"label": b.label, "count": b.count})
	}

	// Success rate + average latency, computed from the full period in SQL.
	// Cache hits count as successful; avg latency excludes cache hits (0ms).
	// successRate is a 0-1 ratio (frontend multiplies by 100 for display).
	var successRate float64
	if sum.TotalRequests > 0 {
		successRate = float64(sum.SuccessCount) / float64(sum.TotalRequests)
	} else {
		successRate = 1
	}
	avgLatency := int(sum.AvgLatencyMS)
	avgTTFT := int(sum.AvgTTFTMS)

	busiest := ""
	if busiestCount > 0 && busiestIdx < len(buckets) {
		busiest = buckets[busiestIdx].label
	}

	// Token savings analytics from RTK slimmer and Caveman/Terse.
	rules := make([]map[string]any, 0, len(ruleSavings))
	for _, rs := range ruleSavings {
		rules = append(rules, map[string]any{
			"rule":         rs.Rule,
			"count":        rs.Count,
			"bytes_saved":  rs.BytesSaved,
			"tokens_saved": rs.BytesSaved / 4,
		})
	}

	// Estimated USD saved by input-side compression. The slimmer removes input
	// bytes before the upstream call, so the saved tokens are never priced
	// directly. We approximate by valuing them at a blended input rate derived
	// from the period's own usage (cost-weighted across providers/models), which
	// keeps the estimate grounded in what this tenant actually pays. This is an
	// estimate, surfaced as such in the UI.
	blendedInputPerToken := blendedInputRate(sum)
	usdPerToken := func(tokens int64) float64 {
		return float64(tokens) * blendedInputPerToken
	}

	// Per-client savings attribution. Generic across any client (claude-code,
	// codex, cline, ...) — never locked to a specific tool. Clients with no
	// detected identity are grouped under "unknown".
	byClient := make([]map[string]any, 0, len(clientSavings))
	for _, cs := range clientSavings {
		// Skip clients that produced no optimization at all, so the breakdown
		// shows only where optimization actually helped.
		if cs.SlimTokensSaved == 0 && cs.CavemanRequests == 0 && cs.TerseRequests == 0 {
			continue
		}
		byClient = append(byClient, map[string]any{
			"client":           cs.Client,
			"requests":         cs.Requests,
			"bytes_saved":      cs.SlimBytesSaved,
			"tokens_saved":     cs.SlimTokensSaved,
			"usd_saved":        usdPerToken(cs.SlimTokensSaved),
			"caveman_requests": cs.CavemanRequests,
			"terse_requests":   cs.TerseRequests,
		})
	}

	// Headroom savings by transform.
	headroomTransforms := make([]map[string]any, 0, len(headroomSavings))
	for _, hs := range headroomSavings {
		headroomTransforms = append(headroomTransforms, map[string]any{
			"transform":    hs.Transform,
			"count":        hs.Count,
			"tokens_saved": hs.TokensSaved,
		})
	}

	writeJSONCached(w, s.insightsCache, cacheKey, map[string]any{
		"summary": map[string]any{
			"total_requests":     sum.TotalRequests,
			"prompt_tokens":      sum.PromptTokens,
			"completion_tokens":  sum.CompletionTokens,
			"cached_tokens":      sum.CachedTokens,
			"cache_write_tokens": sum.CacheWriteTokens,
			"cost_usd":           float64(sum.CostMicros) / 1_000_000,
			"cache_hits":         sum.CacheHits,
			"success_rate":       successRate,
			"avg_latency_ms":     avgLatency,
			"avg_ttft_ms":        avgTTFT,
			"since":              since,
		},
		"savings": map[string]any{
			"slim_bytes_saved":      sum.SlimBytesSaved,
			"slim_tokens_saved":     sum.SlimTokensSaved,
			"headroom_requests":     sum.HeadroomRequests,
			"headroom_tokens_saved": sum.HeadroomTokensSaved,
			"caveman_requests":      sum.CavemanRequests,
			"terse_requests":        sum.TerseRequests,
			"usd_saved":             usdPerToken(sum.SlimTokensSaved + sum.HeadroomTokensSaved),
			"usd_saved_estimate":    true,
			"rules":                 rules,
			"headroom_transforms":   headroomTransforms,
			"by_client":             byClient,
		},
		"providers": providers,
		"recent":    recentRows,
		"series":    series,
		"busiest":   busiest,
	})
}

// blendedInputRate estimates the USD cost of a single input token for the
// period, derived from the tenant's own spend. It divides total spend by total
// tokens (prompt + completion) to get an average price per token. This is a
// deliberately conservative blended figure: savings happen on the input side,
// but pricing varies by provider/model and isn't stored per saved token, so a
// spend-weighted average grounds the estimate in what the tenant actually paid.
// Returns 0 when there is no usage to derive a rate from.
func blendedInputRate(sum store.Summary) float64 {
	totalTokens := sum.PromptTokens + sum.CompletionTokens
	if totalTokens <= 0 || sum.CostMicros <= 0 {
		return 0
	}
	usd := float64(sum.CostMicros) / 1_000_000
	return usd / float64(totalTokens)
}

// adminModelUsage returns per-provider+model aggregate usage for the granular
// model usage table on the Usage page.
func (s *Server) adminModelUsage(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	cacheKey := "models|" + period + "|" + tz
	if s.cacheHit(w, cacheKey) {
		return
	}
	since := sinceForPeriod(period, tz)
	ctx := r.Context()

	models, err := s.usage.ByModel(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		display := m.Provider
		if spec, ok := connectors.SpecByID(m.Provider); ok {
			display = spec.DisplayName
		}
		entry := map[string]any{
			"provider":          m.Provider,
			"provider_name":     display,
			"model":             m.Model,
			"total_requests":    m.TotalRequests,
			"prompt_tokens":     m.PromptTokens,
			"completion_tokens": m.CompletionTokens,
			"cost_usd":          float64(m.CostMicros) / 1_000_000,
		}
		// Include per-model pricing if available.
		if mp, ok := connectors.ModelPriceByProviderModel(m.Provider, m.Model); ok {
			entry["input_per_m"] = mp.InputPerM
			entry["output_per_m"] = mp.OutputPerM
			entry["cached_input_per_m"] = mp.CachedInputPerM
		}
		out = append(out, entry)
	}
	writeJSONCached(w, s.insightsCache, cacheKey, map[string]any{"models": out})
}

type timeBucket struct {
	label string
	count int64
}

// bucketTimeline applies time labels to the pre-bucketed SQL timeline points.
func bucketTimeline(points []store.TimeBucket, from, to time.Time, n int) []timeBucket {
	if n <= 0 {
		n = 24
	}
	buckets := make([]timeBucket, n)
	span := to.Sub(from)
	if span <= 0 {
		span = time.Hour
	}
	slot := span / time.Duration(n)
	if slot <= 0 {
		slot = time.Minute
	}
	for i := 0; i < n; i++ {
		buckets[i].label = from.Add(time.Duration(i) * slot).Format("15:04")
	}
	for _, p := range points {
		if p.Bucket >= 0 && p.Bucket < n {
			buckets[p.Bucket].count = p.Count
		}
	}
	return buckets
}

// ---- quota tracker ----------------------------------------------------------

// adminQuotaUsage returns per-account usage so the Quota Tracker can show how
// much each connected account has consumed in the period.
func (s *Server) adminQuotaUsage(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	since := sinceForPeriod(period, tz)
	ctx := r.Context()

	accs, err := s.accounts.ListByTenant(ctx, adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byAcct, err := s.usage.ByAccount(ctx, adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	usageByID := make(map[string]store.AccountUsage, len(byAcct))
	for _, u := range byAcct {
		usageByID[u.AccountID] = u
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	out := make([]map[string]any, 0, len(accs))

	for i, a := range accs {
		u := usageByID[a.ID]
		status := "active"
		if a.Disabled {
			status = "paused"
		} else if a.CooldownUntil != nil && a.CooldownUntil.After(time.Now()) {
			status = "needs_attention"
		}
		display := a.Provider
		var inputPerM, outputPerM float64
		var providerNotice string
		if spec, ok := connectors.SpecByID(a.Provider); ok {
			display = spec.DisplayName
			inputPerM = spec.InputPerM
			outputPerM = spec.OutputPerM
			providerNotice = spec.Notice
		}
		usageType := "token"
		if connectors.GetQuotaSource(a.Provider) != nil {
			usageType = "credit"
		}
		entry := map[string]any{
			"id":                 a.ID,
			"provider":           a.Provider,
			"provider_name":      display,
			"label":              a.Label,
			"auth_kind":          a.AuthKind,
			"priority":           a.Priority,
			"status":             status,
			"usage_type":         usageType,
			"total_requests":     u.TotalRequests,
			"prompt_tokens":      u.PromptTokens,
			"completion_tokens":  u.CompletionTokens,
			"cached_tokens":      u.CachedTokens,
			"cache_write_tokens": u.CacheWriteTokens,
			"cost_usd":           float64(u.CostMicros) / 1_000_000,
			"input_per_m":        inputPerM,
			"output_per_m":       outputPerM,
			"updated_at":         a.UpdatedAt,
		}
		if providerNotice != "" {
			entry["notice"] = providerNotice
		}
		out = append(out, entry)

		// Fetch upstream quota for providers that support it (e.g. Kiro) concurrently.
		if qs := connectors.GetQuotaSource(a.Provider); qs != nil && !a.Disabled {
			if creds, err := s.vault.Open(a); err == nil {
				wg.Add(1)
				go func(idx int, qs connectors.QuotaSource, creds core.Credentials) {
					defer wg.Done()
					quotaCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
					quota, qerr := qs.FetchQuota(quotaCtx, creds)
					cancel()
					if qerr == nil && quota != nil {
						var quotas []map[string]any
						for _, q := range quota.Quotas {
							quotas = append(quotas, map[string]any{
								"resource_type": q.ResourceType,
								"used":          q.Used,
								"limit":         q.Limit,
								"remaining":     q.Remaining,
								"reset_at":      q.ResetAt,
							})
						}

						mu.Lock()
						out[idx]["plan_name"] = quota.PlanName
						out[idx]["message"] = quota.Message
						if len(quotas) > 0 {
							out[idx]["upstream_quotas"] = quotas
						}
						mu.Unlock()
					}
				}(i, qs, creds)
			}
		}
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out, "since": since})
}

// ---- usage SSE stream --------------------------------------------------------

// adminUsageStream serves an SSE endpoint that pushes usage events to the
// frontend for near-real-time dashboard updates. When a new usage record is
// inserted, the meter publishes to the usagehub.Hub, which delivers the event
// here. The frontend subscribes via EventSource and invalidates its query cache
// on each event.
func (s *Server) adminUsageStream(w http.ResponseWriter, r *http.Request) {
	if s.usageHub == nil {
		writeError(w, http.StatusServiceUnavailable, "usage hub not configured")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial heartbeat so the client knows the connection is live.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Subscribe to usage events via buffered channel.
	listener := usagehub.NewListener(64)
	s.usageHub.Subscribe(listener)
	defer s.usageHub.Unsubscribe(listener)

	// Keepalive ping every 25s to prevent proxy timeouts.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-listener.C:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// ---- console log ------------------------------------------------------------

// adminConsoleLog returns the buffered log lines from the console log.
func (s *Server) adminConsoleLog(w http.ResponseWriter, r *http.Request) {
	lines := s.consoleLog.Lines()
	writeJSON(w, http.StatusOK, map[string]any{"logs": lines})
}

// ---- proxy pools ------------------------------------------------------------

func (s *Server) adminListProxyPools(w http.ResponseWriter, r *http.Request) {
	pools, err := s.pools.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Count accounts bound to each pool for the dashboard.
	accs, _ := s.accounts.ListByTenant(r.Context(), adminTenant)
	boundCounts := map[string]int{}
	for _, a := range accs {
		if a.ProxyPoolID != "" {
			boundCounts[a.ProxyPoolID]++
		}
	}

	out := make([]map[string]any, 0, len(pools))
	for _, p := range pools {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"proxy_url": p.ProxyURL, "no_proxy": p.NoProxy,
			"strict": p.Strict, "is_active": p.IsActive,
			"test_status": p.TestStatus, "last_tested": p.LastTested,
			"last_error":             p.LastError,
			"bound_connection_count": boundCounts[p.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"pools": out})
}

var validProxyPoolTypes = map[string]bool{
	"http": true, "vercel": true, "cloudflare": true, "deno": true,
}

func (s *Server) adminCreateProxyPool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		ProxyURL string `json:"proxy_url"`
		NoProxy  string `json:"no_proxy"`
		Strict   bool   `json:"strict"`
		IsActive *bool  `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" || body.ProxyURL == "" {
		writeError(w, http.StatusBadRequest, "name and proxy_url are required")
		return
	}

	// SSRF Protection: Validate proxy URL before use
	if err := httputil.ValidateProxyURL(body.ProxyURL); err != nil {
		s.log.Warn("blocked suspicious proxy URL", "url", body.ProxyURL, "error", err)
		writeError(w, http.StatusBadRequest, "invalid proxy_url: URL blocked by security policy")
		return
	}

	poolType := body.Type
	if poolType == "" {
		poolType = "http"
	}
	if !validProxyPoolTypes[poolType] {
		writeError(w, http.StatusBadRequest, "invalid pool type: must be http, vercel, cloudflare, or deno")
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	now := time.Now()
	pool := store.ProxyPool{
		ID:         uuid.NewString(),
		Name:       body.Name,
		Type:       poolType,
		ProxyURL:   body.ProxyURL,
		NoProxy:    body.NoProxy,
		Strict:     body.Strict,
		IsActive:   active,
		TestStatus: "unknown",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.pools.Create(r.Context(), pool); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "proxy pool creation failed"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": pool.ID, "name": pool.Name})
}

func (s *Server) adminUpdateProxyPool(w http.ResponseWriter, r *http.Request) {
	pool, err := s.pools.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "pool not found")
		return
	}
	var body struct {
		Name     *string `json:"name"`
		ProxyURL *string `json:"proxy_url"`
		NoProxy  *string `json:"no_proxy"`
		Strict   *bool   `json:"strict"`
		IsActive *bool   `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name != nil {
		pool.Name = *body.Name
	}
	if body.ProxyURL != nil {
		pool.ProxyURL = *body.ProxyURL
	}
	if body.NoProxy != nil {
		pool.NoProxy = *body.NoProxy
	}
	if body.Strict != nil {
		pool.Strict = *body.Strict
	}
	if body.IsActive != nil {
		pool.IsActive = *body.IsActive
	}
	pool.UpdatedAt = time.Now()
	if err := s.pools.Update(r.Context(), pool); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteProxyPool(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Prevent deletion if any accounts are bound to this pool.
	accs, _ := s.accounts.ListByTenant(r.Context(), adminTenant)
	bound := 0
	for _, a := range accs {
		if a.ProxyPoolID == id {
			bound++
		}
	}
	if bound > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":                  "proxy pool is currently in use",
			"bound_connection_count": bound,
		})
		return
	}

	if err := s.pools.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminTestProxyPool(w http.ResponseWriter, r *http.Request) {
	pool, err := s.pools.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "pool not found")
		return
	}

	now := time.Now()
	pool.LastTested = &now

	result := testProxyPoolConnectivity(pool)

	pool.TestStatus = result.status
	pool.LastError = result.lastError
	// Preserve current is_active — don't force-enable on test pass.
	_ = s.pools.Update(r.Context(), pool)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      pool.TestStatus,
		"last_tested": pool.LastTested,
		"elapsed_ms":  result.elapsedMS,
		"error":       result.lastError,
	})
}

type proxyTestResult struct {
	status    string
	lastError string
	elapsedMS int64
}

// testProxyPoolConnectivity performs a real connectivity check against a proxy
// pool. For HTTP proxies it routes a GET httpbin.org/ip through the proxy; for
// relay types (vercel/cloudflare/deno) it sends relay headers.
func testProxyPoolConnectivity(pool store.ProxyPool) proxyTestResult {
	timeout := 10 * time.Second

	switch pool.Type {
	case "vercel", "cloudflare", "deno":
		return testRelayPool(pool.ProxyURL, timeout)
	default: // "http"
		return testHTTPPool(pool.ProxyURL, timeout)
	}
}

func testHTTPPool(proxyURL string, timeout time.Duration) proxyTestResult {
	start := time.Now()
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return proxyTestResult{status: "error", lastError: "invalid proxy URL: " + err.Error()}
	}
	transport := &http.Transport{Proxy: http.ProxyURL(parsed)}
	client := &http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequest("GET", "https://httpbin.org/ip", nil)
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error()}
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error(), elapsedMS: elapsed}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return proxyTestResult{status: "error", lastError: fmt.Sprintf("proxy returned HTTP %d", resp.StatusCode), elapsedMS: elapsed}
	}
	return proxyTestResult{status: "active", elapsedMS: elapsed}
}

func testRelayPool(relayURL string, timeout time.Duration) proxyTestResult {
	start := time.Now()
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("GET", relayURL, nil)
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error()}
	}
	req.Header.Set("x-relay-target", "https://httpbin.org")
	req.Header.Set("x-relay-path", "/get")
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return proxyTestResult{status: "error", lastError: err.Error(), elapsedMS: elapsed}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return proxyTestResult{status: "error", lastError: fmt.Sprintf("relay returned HTTP %d", resp.StatusCode), elapsedMS: elapsed}
	}
	return proxyTestResult{status: "active", elapsedMS: elapsed}
}

// ---- skills -----------------------------------------------------------------

// skillsKey is the settings key under which skill toggles are stored. Skills
// are reusable system-prompt augmentations the gateway can apply, mirroring
// 9router's Skills feature.
const skillsKey = "skills"

type skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) loadSkills(r *http.Request) []skill {
	skills := []skill{}
	if s.settings == nil {
		return skills
	}
	raw, err := s.settings.Get(r.Context(), skillsKey)
	if err != nil || raw == "" {
		return skills
	}
	_ = json.Unmarshal([]byte(raw), &skills)
	return skills
}

func (s *Server) saveSkills(r *http.Request, skills []skill) error {
	raw, err := json.Marshal(skills)
	if err != nil {
		return err
	}
	return s.settings.Set(r.Context(), skillsKey, string(raw))
}

func (s *Server) adminListSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"skills": s.loadSkills(r)})
}

func (s *Server) adminCreateSkill(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Enabled     *bool  `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	sk := skill{
		ID:          uuid.NewString(),
		Name:        body.Name,
		Description: body.Description,
		Prompt:      body.Prompt,
		Enabled:     enabled,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	skills := append(s.loadSkills(r), sk)
	if err := s.saveSkills(r, skills); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}

func (s *Server) adminUpdateSkill(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	skills := s.loadSkills(r)
	found := false
	for i := range skills {
		if skills[i].ID == id {
			if body.Enabled != nil {
				skills[i].Enabled = *body.Enabled
			}
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	if err := s.saveSkills(r, skills); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	skills := s.loadSkills(r)
	out := skills[:0]
	for _, sk := range skills {
		if sk.ID != id {
			out = append(out, sk)
		}
	}
	if err := s.saveSkills(r, out); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
