// Package observ provides KeiRouter's native observability: Prometheus metrics
// covering request volume, latency, token usage, cost, fallbacks, and cache
// hits. Metrics are exposed at /metrics for scraping by Prometheus/Grafana.
//
// Keeping instrumentation in one place lets the pipeline and gateway record
// signals through a single typed API rather than scattering metric names.
package observ

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds the registered Prometheus collectors.
type Metrics struct {
	registry *prometheus.Registry

	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	TimeToFirstToken *prometheus.HistogramVec
	TokensTotal     *prometheus.CounterVec
	CostMicros      *prometheus.CounterVec
	Fallbacks       *prometheus.CounterVec
	CacheHits       prometheus.Counter
	CacheMisses     prometheus.Counter
	CacheLatency    *prometheus.HistogramVec
	CacheSize       prometheus.Gauge
	UpstreamErrors  *prometheus.CounterVec

	// Token-saving analytics.
	SlimBytesSaved  *prometheus.CounterVec // by rule
	HeadroomTokensSaved *prometheus.CounterVec // by transform
	HeadroomRequests prometheus.Counter
	CavemanRequests prometheus.Counter     // requests with caveman active
	TerseRequests   prometheus.Counter     // requests with terse active

	// Guardrails activity.
	GuardrailDecisions *prometheus.CounterVec   // by detector + action
	GuardrailEval      *prometheus.HistogramVec // per-detector eval latency
}

// New builds a Metrics instance backed by a dedicated registry. Using a private
// registry (rather than the global default) keeps tests isolated and avoids
// duplicate-registration panics.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	m := &Metrics{
		registry: reg,
		RequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_requests_total",
			Help: "Total chat requests, labeled by provider, model, and outcome.",
		}, []string{"provider", "model", "outcome"}),
		RequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "keirouter_request_duration_seconds",
			Help:    "Upstream request latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 12), // 50ms .. ~100s
		}, []string{"provider", "model"}),
		TimeToFirstToken: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "keirouter_ttft_seconds",
			Help:    "Time-to-first-token for streaming requests in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 8), // 100ms .. ~12.8s
		}, []string{"provider", "model"}),
		TokensTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_tokens_total",
			Help: "Total tokens processed, labeled by provider, model, and kind.",
		}, []string{"provider", "model", "kind"}),
		CostMicros: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_cost_micros_total",
			Help: "Estimated cost in micros of USD, labeled by provider and model.",
		}, []string{"provider", "model"}),
		Fallbacks: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_fallbacks_total",
			Help: "Fallback events, labeled by the error kind that triggered them.",
		}, []string{"kind"}),
		CacheHits: factory.NewCounter(prometheus.CounterOpts{
			Name: "keirouter_cache_hits_total",
			Help: "Semantic cache hits.",
		}),
		CacheMisses: factory.NewCounter(prometheus.CounterOpts{
			Name: "keirouter_cache_misses_total",
			Help: "Semantic cache misses.",
		}),
		CacheLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "keirouter_cache_lookup_seconds",
			Help:    "Cache lookup latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 10), // 0.5ms .. ~256ms
		}, []string{"backend"}),
		CacheSize: factory.NewGauge(prometheus.GaugeOpts{
			Name: "keirouter_cache_entries",
			Help: "Current number of entries in the semantic cache.",
		}),
		UpstreamErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_upstream_errors_total",
			Help: "Upstream errors, labeled by provider and error kind.",
		}, []string{"provider", "kind"}),
		SlimBytesSaved: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_slim_bytes_saved_total",
			Help: "Total bytes saved by RTK slimmer, labeled by rule.",
		}, []string{"rule"}),
		HeadroomTokensSaved: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_headroom_tokens_saved_total",
			Help: "Total exact input tokens saved by Headroom, labeled by transform.",
		}, []string{"transform"}),
		HeadroomRequests: factory.NewCounter(prometheus.CounterOpts{
			Name: "keirouter_headroom_requests_total",
			Help: "Total requests where Headroom compression was active.",
		}),
		CavemanRequests: factory.NewCounter(prometheus.CounterOpts{
			Name: "keirouter_caveman_requests_total",
			Help: "Total requests with caveman output compression active.",
		}),
		TerseRequests: factory.NewCounter(prometheus.CounterOpts{
			Name: "keirouter_terse_requests_total",
			Help: "Total requests with terse output compression active.",
		}),
		GuardrailDecisions: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "keirouter_guardrail_decisions_total",
			Help: "Guardrail decisions, labeled by detector and action.",
		}, []string{"detector", "action"}),
		GuardrailEval: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "keirouter_guardrail_eval_seconds",
			Help:    "Per-detector evaluation latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12), // 0.5ms .. ~2s
		}, []string{"detector"}),
	}
	return m
}

// Registry exposes the underlying Prometheus registry for the /metrics handler.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// RecordRequest records a completed (or failed) request's outcome, latency,
// tokens, and cost in one call. ttftSeconds is the time-to-first-token for
// streaming requests; pass 0 for non-streaming or cache hits.
func (m *Metrics) RecordRequest(provider, model, outcome string, seconds float64,
	promptTokens, completionTokens, cachedTokens int, costMicros int64, ttftSeconds float64) {
	m.RequestsTotal.WithLabelValues(provider, model, outcome).Inc()
	m.RequestDuration.WithLabelValues(provider, model).Observe(seconds)
	if ttftSeconds > 0 {
		m.TimeToFirstToken.WithLabelValues(provider, model).Observe(ttftSeconds)
	}
	if promptTokens > 0 {
		m.TokensTotal.WithLabelValues(provider, model, "prompt").Add(float64(promptTokens))
	}
	if completionTokens > 0 {
		m.TokensTotal.WithLabelValues(provider, model, "completion").Add(float64(completionTokens))
	}
	if cachedTokens > 0 {
		m.TokensTotal.WithLabelValues(provider, model, "cached").Add(float64(cachedTokens))
	}
	if costMicros > 0 {
		m.CostMicros.WithLabelValues(provider, model).Add(float64(costMicros))
	}
}

// RecordFallback notes that a fallback occurred due to the given error kind.
func (m *Metrics) RecordFallback(kind string) {
	m.Fallbacks.WithLabelValues(kind).Inc()
}

// RecordUpstreamError counts an upstream error by provider and kind.
func (m *Metrics) RecordUpstreamError(provider, kind string) {
	m.UpstreamErrors.WithLabelValues(provider, kind).Inc()
}

// RecordCache notes a cache hit or miss.
func (m *Metrics) RecordCache(hit bool) {
	if hit {
		m.CacheHits.Inc()
	} else {
		m.CacheMisses.Inc()
	}
}

// RecordCacheLookup records how long a cache lookup took, labeled by backend.
func (m *Metrics) RecordCacheLookup(backend string, seconds float64) {
	m.CacheLatency.WithLabelValues(backend).Observe(seconds)
}

// SetCacheSize updates the current cache entry count gauge.
func (m *Metrics) SetCacheSize(n int) {
	m.CacheSize.Set(float64(n))
}

// RecordSlimSavings records bytes saved by a specific RTK rule.
func (m *Metrics) RecordSlimSavings(rule string, bytesSaved int) {
	if bytesSaved > 0 {
		m.SlimBytesSaved.WithLabelValues(rule).Add(float64(bytesSaved))
	}
}

// RecordHeadroomSavings records exact tokens saved by Headroom.
func (m *Metrics) RecordHeadroomSavings(transform string, tokensSaved int) {
	if tokensSaved > 0 {
		m.HeadroomTokensSaved.WithLabelValues(transform).Add(float64(tokensSaved))
		m.HeadroomRequests.Inc()
	}
}

// RecordCavemanActivation notes a request where caveman was active.
func (m *Metrics) RecordCavemanActivation() {
	m.CavemanRequests.Inc()
}

// RecordTerseActivation notes a request where terse was active.
func (m *Metrics) RecordTerseActivation() {
	m.TerseRequests.Inc()
}

// RecordGuardrailDecision counts a guardrail decision by detector + action.
// Passing the empty action is treated as "allow" so callers can use the
// raw Decision.Action value without massaging it.
func (m *Metrics) RecordGuardrailDecision(detector, action string) {
	if action == "" {
		action = "allow"
	}
	m.GuardrailDecisions.WithLabelValues(detector, action).Inc()
}

// RecordGuardrailLatency records how long a detector took to evaluate.
func (m *Metrics) RecordGuardrailLatency(detector string, seconds float64) {
	m.GuardrailEval.WithLabelValues(detector).Observe(seconds)
}