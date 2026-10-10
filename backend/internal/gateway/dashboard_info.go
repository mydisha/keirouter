package gateway

import (
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"time"
)

// adminGatewayInfo returns the facts the dashboard shell shows about this
// instance in one call: version, uptime, where the API listens, and which
// storage engine backs it.
func (s *Server) adminGatewayInfo(w http.ResponseWriter, r *http.Request) {
	uptime := int64(time.Since(sysHistory.started).Seconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        "KeiRouter",
		"version":     s.version,
		"uptime_s":    uptime,
		"started_at":  sysHistory.started.UTC(),
		"listen_addr": s.cfg.Addr(),
		"dialect":     string(s.dbDialect()),
		"go_version":  runtime.Version(),
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
	})
}

// adminHealthTimeline returns every provider's health as an equal-width
// bucket strip (default 24 × 1h) built from persisted snapshots, so one
// request can draw the Overview's uptime bars for all providers.
//
// Bucket status: "idle" (no traffic), "down" (unhealthy, or ≥25% failures),
// "degraded" (degraded status, any rate limiting, or ≥5% failures), "ok".
func (s *Server) adminHealthTimeline(w http.ResponseWriter, r *http.Request) {
	window := parseRangeDuration(r.URL.Query().Get("range"))
	if r.URL.Query().Get("range") == "" {
		window = 24 * time.Hour
	}
	buckets := 24
	if raw := r.URL.Query().Get("buckets"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 6 && n <= 96 {
			buckets = n
		}
	}
	bucketSecs := int64(window.Seconds()) / int64(buckets)
	if bucketSecs < 60 {
		bucketSecs = 60
	}
	now := time.Now().UTC()
	since := now.Add(-time.Duration(bucketSecs*int64(buckets)) * time.Second)

	rollups, err := s.db.ProviderHealth().RollupSnapshots(r.Context(), since, bucketSecs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	type agg struct {
		requests, successes, failures, fallbacks, rateLimited, p95 int64
		strip                                                      []map[string]any
	}
	byProvider := map[string]*agg{}
	order := []string{}
	for _, row := range rollups {
		if row.Bucket < 0 || row.Bucket >= buckets {
			continue
		}
		a, ok := byProvider[row.Provider]
		if !ok {
			a = &agg{strip: make([]map[string]any, buckets)}
			for i := range a.strip {
				a.strip[i] = map[string]any{
					"start":    since.Add(time.Duration(int64(i)*bucketSecs) * time.Second),
					"requests": int64(0), "failures": int64(0), "rate_limited": int64(0),
					"p95_ms": int64(0), "status": "idle",
				}
			}
			byProvider[row.Provider] = a
			order = append(order, row.Provider)
		}
		a.requests += row.Requests
		a.successes += row.Successes
		a.failures += row.Failures
		a.fallbacks += row.Fallbacks
		a.rateLimited += row.RateLimited
		if row.LatencyP95Ms > a.p95 {
			a.p95 = row.LatencyP95Ms
		}
		a.strip[row.Bucket] = map[string]any{
			"start":        since.Add(time.Duration(int64(row.Bucket)*bucketSecs) * time.Second),
			"requests":     row.Requests,
			"failures":     row.Failures,
			"rate_limited": row.RateLimited,
			"p95_ms":       row.LatencyP95Ms,
			"status":       timelineBucketStatus(row.Requests, row.Failures, row.RateLimited, row.WorstStatusRnk),
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byProvider[order[i]].requests > byProvider[order[j]].requests
	})

	providers := make([]map[string]any, 0, len(order))
	for _, id := range order {
		a := byProvider[id]
		display, color, icon := usageProviderMetadata(id)
		providers = append(providers, map[string]any{
			"provider":     id,
			"display_name": display,
			"color":        color,
			"icon":         icon,
			"requests":     a.requests,
			"failures":     a.failures,
			"fallbacks":    a.fallbacks,
			"rate_limited": a.rateLimited,
			"success_rate": ratio(a.successes, a.requests),
			"worst_p95_ms": a.p95,
			"buckets":      a.strip,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"since":          since,
		"until":          now,
		"bucket_seconds": bucketSecs,
		"providers":      providers,
	})
}

func timelineBucketStatus(requests, failures, rateLimited int64, worstRank int) string {
	if requests == 0 {
		return "idle"
	}
	failRate := float64(failures) / float64(requests)
	switch {
	case worstRank >= 3 || failRate >= 0.25:
		return "down"
	case worstRank == 2 || rateLimited > 0 || failRate >= 0.05:
		return "degraded"
	default:
		return "ok"
	}
}
