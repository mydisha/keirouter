package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/caveman"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/headroom"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/ponytail"
	"github.com/mydisha/keirouter/backend/internal/slimmer"
	"github.com/mydisha/keirouter/backend/internal/terse"
)

// endpointSettingsKey is the settings-store key under which token-saving
// preferences are persisted as a JSON blob (RTK token saver + caveman output
// compression + terse mode).
const endpointSettingsKey = "endpoint_settings"

// EndpointSettings holds the dashboard-configurable token-saving preferences.
type EndpointSettings struct {
	// RTKEnabled toggles input-side tool-output compression (the slimmer).
	RTKEnabled     bool   `json:"rtk_enabled"`
	RTKFilterLevel string `json:"rtk_filter_level"` // "none", "minimal", "aggressive"

	// CavemanEnabled toggles output-side caveman compression; CavemanLevel is
	// one of "lite", "full", "ultra".
	CavemanEnabled bool   `json:"caveman_enabled"`
	CavemanLevel   string `json:"caveman_level"`

	// TerseEnabled toggles terse mode, which serializes messages and tools into
	// the compact TERSE format for token-efficient context.
	TerseEnabled bool   `json:"terse_enabled"`
	TerseLevel   string `json:"terse_level"`

	// Headroom (input-side proxy compression).
	HeadroomEnabled              bool   `json:"headroom_enabled"`
	HeadroomURL                  string `json:"headroom_url"`
	HeadroomCompressUserMessages bool   `json:"headroom_compress_user_messages"`
	HeadroomTimeoutMs            int    `json:"headroom_timeout_ms"`

	// Ponytail (output-side system-prompt injection).
	PonytailEnabled bool   `json:"ponytail_enabled"`
	PonytailLevel   string `json:"ponytail_level"` // "lite" | "full" | "ultra"

	// Routing strategy fields.
	RoutingStrategy  string `json:"routing_strategy"`   // "fill-first" | "round-robin" | "smart-round-robin"
	StickyLimit      int    `json:"sticky_limit"`       // calls per account before switching
	ComboStrategy    string `json:"combo_strategy"`     // "fallback" | "round-robin"
	ComboStickyLimit int    `json:"combo_sticky_limit"` // calls per combo model before switching

	// Outbound proxy settings.
	OutboundProxyEnabled bool   `json:"outbound_proxy_enabled"`
	OutboundProxyURL     string `json:"outbound_proxy_url"`
	OutboundNoProxy      string `json:"outbound_no_proxy"`

	// Observability.
	ObservabilityEnabled *bool `json:"observability_enabled,omitempty"`

	// RateLimitsEnabled toggles per-key RPM/TPM/concurrency enforcement.
	RateLimitsEnabled bool `json:"rate_limits_enabled"`

	// Timeout settings (milliseconds).
	// StreamStallTimeoutMs aborts a stream that produces no data for this long.
	// Increase for reasoning models (Deepseek, GLM) that think before streaming.
	StreamStallTimeoutMs int `json:"stream_stall_timeout_ms"`
	// ResponseHeaderTimeoutMs is the max time waiting for upstream response
	// headers on streaming calls (time to first byte). Non-streaming calls
	// are bounded by RequestTimeoutMs instead, because their headers only
	// arrive once generation has finished.
	ResponseHeaderTimeoutMs int `json:"response_header_timeout_ms"`
	// RequestTimeoutMs bounds non-streaming upstream calls.
	RequestTimeoutMs int `json:"request_timeout_ms"`
}

// defaultEndpointSettings returns the defaults: RTK on, caveman/terse off.
func defaultEndpointSettings() EndpointSettings {
	return EndpointSettings{
		RTKEnabled:                   true,
		RTKFilterLevel:               "none",
		CavemanEnabled:               false,
		CavemanLevel:                 string(caveman.LevelFull),
		TerseEnabled:                 false,
		TerseLevel:                   "medium",
		HeadroomEnabled:              false,
		HeadroomURL:                  "",
		HeadroomCompressUserMessages: false,
		HeadroomTimeoutMs:            3000,
		PonytailEnabled:              false,
		PonytailLevel:                string(ponytail.LevelFull),
		StickyLimit:                  3,
		ComboStrategy:                "fallback",
		ComboStickyLimit:             1,
		OutboundProxyEnabled:         false,
		OutboundProxyURL:             "",
		OutboundNoProxy:              "",
		RateLimitsEnabled:            true,
		// Timeout defaults (ms). Generous bounds to
		// accommodate slow upstream providers and reasoning models.
		// Keep account routing context-sticky by default: repeated turns from
		// the same conversation prefer the same account, while new conversations
		// still spread across accounts.
		RoutingStrategy:         string(dispatch.StrategySmartRoundRobin),
		StreamStallTimeoutMs:    300000, // 5 min
		ResponseHeaderTimeoutMs: 60000,  // 60s
		RequestTimeoutMs:        300000, // 5 min
	}
}

// loadEndpointSettings reads the persisted settings, falling back to defaults
// when unset or unreadable. It never errors: token-saving is best-effort.
func (s *Server) loadEndpointSettings(ctx context.Context) EndpointSettings {
	def := defaultEndpointSettings()
	if s.settings == nil {
		return def
	}
	raw, err := s.settings.Get(ctx, endpointSettingsKey)
	if err != nil || raw == "" {
		return def
	}
	var es EndpointSettings
	if err := json.Unmarshal([]byte(raw), &es); err != nil {
		return def
	}
	var rawFields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &rawFields)
	if _, ok := rawFields["rate_limits_enabled"]; !ok {
		es.RateLimitsEnabled = def.RateLimitsEnabled
	}
	// Backfill empty levels with defaults.
	if es.CavemanLevel == "" {
		es.CavemanLevel = def.CavemanLevel
	}
	if es.RTKFilterLevel == "" {
		es.RTKFilterLevel = def.RTKFilterLevel
	}
	if es.TerseLevel == "" {
		es.TerseLevel = def.TerseLevel
	}
	if es.RoutingStrategy == "" {
		es.RoutingStrategy = def.RoutingStrategy
	} else if normalized, ok := normalizeAccountRoutingStrategy(es.RoutingStrategy); ok {
		es.RoutingStrategy = normalized
	} else {
		es.RoutingStrategy = def.RoutingStrategy
	}
	if es.StickyLimit == 0 {
		es.StickyLimit = def.StickyLimit
	}
	if es.ComboStrategy == "" {
		es.ComboStrategy = def.ComboStrategy
	} else if normalized, ok := normalizeComboRoutingStrategy(es.ComboStrategy); ok {
		es.ComboStrategy = normalized
	} else {
		es.ComboStrategy = def.ComboStrategy
	}
	if es.ComboStickyLimit == 0 {
		es.ComboStickyLimit = def.ComboStickyLimit
	}
	// Backfill timeout defaults for existing settings that predate this feature.
	if es.StreamStallTimeoutMs == 0 {
		es.StreamStallTimeoutMs = def.StreamStallTimeoutMs
	}
	if es.ResponseHeaderTimeoutMs == 0 {
		es.ResponseHeaderTimeoutMs = def.ResponseHeaderTimeoutMs
	}
	if es.RequestTimeoutMs == 0 {
		es.RequestTimeoutMs = def.RequestTimeoutMs
	}
	// Backfill saver defaults for settings that predate this feature. The
	// remaining Headroom/Ponytail fields use Go zero-values (false/"") that
	// already match their defaults.
	if es.HeadroomTimeoutMs == 0 {
		es.HeadroomTimeoutMs = def.HeadroomTimeoutMs
	}
	if es.PonytailLevel == "" {
		es.PonytailLevel = def.PonytailLevel
	}
	return es
}

// slimmerConfig resolves the slimmer (RTK) settings from endpoint settings.
func (s *Server) slimmerConfig() slimmer.Config {
	es := s.loadEndpointSettings(context.Background())
	return slimmer.Config{
		Enabled:     es.RTKEnabled,
		FilterLevel: slimmer.ParseFilterLevel(es.RTKFilterLevel),
	}
}

// terseConfig resolves terse-mode settings from endpoint settings.
func (s *Server) terseConfig() terse.Config {
	es := s.loadEndpointSettings(context.Background())
	return terse.Config{Enabled: es.TerseEnabled, Level: terse.Level(es.TerseLevel)}
}

// cavemanConfig resolves caveman settings from endpoint settings.
func (s *Server) cavemanConfig() caveman.Config {
	es := s.loadEndpointSettings(context.Background())
	return caveman.Config{Enabled: es.CavemanEnabled, Level: caveman.Level(es.CavemanLevel)}
}

// headroomConfig resolves Headroom (input-side proxy compression) settings.
func (s *Server) headroomConfig() headroom.Config {
	es := s.loadEndpointSettings(context.Background())
	return headroom.Config{
		Enabled:              es.HeadroomEnabled,
		URL:                  es.HeadroomURL,
		CompressUserMessages: es.HeadroomCompressUserMessages,
		Timeout:              time.Duration(es.HeadroomTimeoutMs) * time.Millisecond,
	}
}

// ponytailConfig resolves Ponytail (output-side system-prompt injection) settings.
func (s *Server) ponytailConfig() ponytail.Config {
	es := s.loadEndpointSettings(context.Background())
	return ponytail.Config{Enabled: es.PonytailEnabled, Level: ponytail.Level(es.PonytailLevel)}
}

// ---- admin endpoints --------------------------------------------------------

// adminGetEndpointSettings returns the current token-saving preferences.
func (s *Server) adminGetEndpointSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadEndpointSettings(r.Context()))
}

// adminTestHeadroom probes a Headroom proxy to confirm it is running. It accepts
// an optional JSON body {"url": "...", "timeout_ms": N}; when omitted it falls
// back to the saved Headroom settings. This lets the dashboard validate a proxy
// before (or after) saving, without ever leaking credentials: the probe result
// only carries a masked endpoint.
func (s *Server) adminTestHeadroom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL       *string `json:"url"`
		TimeoutMs *int    `json:"timeout_ms"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}

	es := s.loadEndpointSettings(r.Context())

	url := es.HeadroomURL
	if body.URL != nil {
		url = *body.URL
	}
	if strings.TrimSpace(url) == "" {
		writeError(w, http.StatusBadRequest, "headroom_url is required to test the connection")
		return
	}
	if err := httputil.ValidateBaseURL(url); err != nil {
		writeError(w, http.StatusBadRequest, "invalid headroom_url: URL blocked by security policy")
		return
	}

	timeoutMs := es.HeadroomTimeoutMs
	if body.TimeoutMs != nil {
		timeoutMs = *body.TimeoutMs
	}
	if timeoutMs < 1000 || timeoutMs > 60000 {
		writeError(w, http.StatusBadRequest, "headroom_timeout_ms must be between 1000 and 60000 ms")
		return
	}

	result := headroom.New(nil).Probe(r.Context(), headroom.Config{
		Enabled: true,
		URL:     url,
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
	})
	writeJSON(w, http.StatusOK, result)
}

// adminUpdateEndpointSettings persists token-saving preferences. It accepts a
// partial body and merges it over the current settings, validating enum values.
func (s *Server) adminUpdateEndpointSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	current := s.loadEndpointSettings(r.Context())

	var patch struct {
		RTKEnabled     *bool   `json:"rtk_enabled"`
		RTKFilterLevel *string `json:"rtk_filter_level"`
		CavemanEnabled *bool   `json:"caveman_enabled"`
		CavemanLevel   *string `json:"caveman_level"`
		TerseEnabled   *bool   `json:"terse_enabled"`
		TerseLevel     *string `json:"terse_level"`

		HeadroomEnabled              *bool   `json:"headroom_enabled"`
		HeadroomURL                  *string `json:"headroom_url"`
		HeadroomCompressUserMessages *bool   `json:"headroom_compress_user_messages"`
		HeadroomTimeoutMs            *int    `json:"headroom_timeout_ms"`
		PonytailEnabled              *bool   `json:"ponytail_enabled"`
		PonytailLevel                *string `json:"ponytail_level"`

		RoutingStrategy         *string `json:"routing_strategy"`
		StickyLimit             *int    `json:"sticky_limit"`
		ComboStrategy           *string `json:"combo_strategy"`
		ComboStickyLimit        *int    `json:"combo_sticky_limit"`
		OutboundProxyEnabled    *bool   `json:"outbound_proxy_enabled"`
		OutboundProxyURL        *string `json:"outbound_proxy_url"`
		OutboundNoProxy         *string `json:"outbound_no_proxy"`
		ObservabilityEnabled    *bool   `json:"observability_enabled"`
		RateLimitsEnabled       *bool   `json:"rate_limits_enabled"`
		StreamStallTimeoutMs    *int    `json:"stream_stall_timeout_ms"`
		ResponseHeaderTimeoutMs *int    `json:"response_header_timeout_ms"`
		RequestTimeoutMs        *int    `json:"request_timeout_ms"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}

	if patch.RTKEnabled != nil {
		current.RTKEnabled = *patch.RTKEnabled
	}
	if patch.RTKFilterLevel != nil {
		switch *patch.RTKFilterLevel {
		case "none", "minimal", "aggressive":
			current.RTKFilterLevel = *patch.RTKFilterLevel
		default:
			writeError(w, http.StatusBadRequest, "rtk_filter_level must be none, minimal, or aggressive")
			return
		}
	}
	if patch.CavemanEnabled != nil {
		current.CavemanEnabled = *patch.CavemanEnabled
	}
	if patch.CavemanLevel != nil {
		if !caveman.ValidLevel(caveman.Level(*patch.CavemanLevel)) {
			writeError(w, http.StatusBadRequest, "caveman_level must be lite, full, ultra, wenyan-lite, wenyan-full, or wenyan-ultra")
			return
		}
		current.CavemanLevel = *patch.CavemanLevel
	}
	if patch.TerseEnabled != nil {
		current.TerseEnabled = *patch.TerseEnabled
	}
	if patch.TerseLevel != nil {
		if !terse.ValidLevel(terse.Level(*patch.TerseLevel)) {
			writeError(w, http.StatusBadRequest, "terse_level must be light, medium, or aggressive")
			return
		}
		current.TerseLevel = *patch.TerseLevel
	}
	if patch.HeadroomEnabled != nil {
		current.HeadroomEnabled = *patch.HeadroomEnabled
	}
	if patch.HeadroomURL != nil {
		current.HeadroomURL = *patch.HeadroomURL
	}
	if patch.HeadroomCompressUserMessages != nil {
		current.HeadroomCompressUserMessages = *patch.HeadroomCompressUserMessages
	}
	if patch.HeadroomTimeoutMs != nil {
		if *patch.HeadroomTimeoutMs < 1000 || *patch.HeadroomTimeoutMs > 60000 {
			writeError(w, http.StatusBadRequest, "headroom_timeout_ms must be between 1000 and 60000 ms")
			return
		}
		current.HeadroomTimeoutMs = *patch.HeadroomTimeoutMs
	}
	if patch.PonytailEnabled != nil {
		current.PonytailEnabled = *patch.PonytailEnabled
	}
	if patch.PonytailLevel != nil {
		if !ponytail.ValidLevel(ponytail.Level(*patch.PonytailLevel)) {
			writeError(w, http.StatusBadRequest, "ponytail_level must be lite, full, or ultra")
			return
		}
		current.PonytailLevel = *patch.PonytailLevel
	}
	// Headroom requires a proxy URL when enabled. Validate the effective value
	// after merging so partial patches (enabling without a URL, or clearing the
	// URL while enabled) are rejected before persistence.
	if current.HeadroomEnabled && strings.TrimSpace(current.HeadroomURL) == "" {
		writeError(w, http.StatusBadRequest, "headroom_url is required when Headroom is enabled")
		return
	}
	// Only run SSRF validation when this request actually sets headroom_url.
	// Re-validating a previously persisted URL on unrelated patches (e.g.
	// toggling RTK/Caveman) would reject saves whenever the stored Headroom
	// proxy points at a local/private host, which is a supported deployment.
	if patch.HeadroomURL != nil && strings.TrimSpace(current.HeadroomURL) != "" {
		if err := httputil.ValidateBaseURL(current.HeadroomURL); err != nil {
			writeError(w, http.StatusBadRequest, "invalid headroom_url: URL blocked by security policy")
			return
		}
	}
	if patch.RoutingStrategy != nil {
		normalized, ok := normalizeAccountRoutingStrategy(*patch.RoutingStrategy)
		if !ok {
			writeError(w, http.StatusBadRequest, "routing_strategy must be fill-first, round-robin, or smart-round-robin")
			return
		}
		current.RoutingStrategy = normalized
	}
	if patch.StickyLimit != nil {
		if *patch.StickyLimit < 1 {
			writeError(w, http.StatusBadRequest, "sticky_limit must be at least 1")
			return
		}
		current.StickyLimit = *patch.StickyLimit
	}
	if patch.ComboStrategy != nil {
		normalized, ok := normalizeComboRoutingStrategy(*patch.ComboStrategy)
		if !ok {
			writeError(w, http.StatusBadRequest, "combo_strategy must be fallback or round-robin")
			return
		}
		current.ComboStrategy = normalized
	}
	if patch.ComboStickyLimit != nil {
		if *patch.ComboStickyLimit < 1 {
			writeError(w, http.StatusBadRequest, "combo_sticky_limit must be at least 1")
			return
		}
		current.ComboStickyLimit = *patch.ComboStickyLimit
	}
	if patch.OutboundProxyEnabled != nil {
		current.OutboundProxyEnabled = *patch.OutboundProxyEnabled
	}
	if patch.OutboundProxyURL != nil {
		current.OutboundProxyURL = *patch.OutboundProxyURL
	}
	// Same rationale as headroom_url: only validate when this request supplies
	// the proxy URL, so unrelated patches don't fail on a persisted value.
	if patch.OutboundProxyURL != nil && strings.TrimSpace(current.OutboundProxyURL) != "" {
		if err := httputil.ValidateProxyURL(current.OutboundProxyURL); err != nil {
			writeError(w, http.StatusBadRequest, "invalid outbound_proxy_url: URL blocked by security policy")
			return
		}
	}
	if patch.OutboundNoProxy != nil {
		current.OutboundNoProxy = *patch.OutboundNoProxy
	}
	if patch.ObservabilityEnabled != nil {
		current.ObservabilityEnabled = patch.ObservabilityEnabled
	}
	if patch.RateLimitsEnabled != nil {
		current.RateLimitsEnabled = *patch.RateLimitsEnabled
	}
	if patch.StreamStallTimeoutMs != nil {
		if *patch.StreamStallTimeoutMs < 5000 || *patch.StreamStallTimeoutMs > 600000 {
			writeError(w, http.StatusBadRequest, "stream_stall_timeout_ms must be between 5000 and 600000")
			return
		}
		current.StreamStallTimeoutMs = *patch.StreamStallTimeoutMs
	}
	if patch.ResponseHeaderTimeoutMs != nil {
		if *patch.ResponseHeaderTimeoutMs < 5000 || *patch.ResponseHeaderTimeoutMs > 300000 {
			writeError(w, http.StatusBadRequest, "response_header_timeout_ms must be between 5000 and 300000")
			return
		}
		current.ResponseHeaderTimeoutMs = *patch.ResponseHeaderTimeoutMs
	}
	if patch.RequestTimeoutMs != nil {
		if *patch.RequestTimeoutMs < 30000 || *patch.RequestTimeoutMs > 3600000 {
			writeError(w, http.StatusBadRequest, "request_timeout_ms must be between 30000 and 3600000")
			return
		}
		current.RequestTimeoutMs = *patch.RequestTimeoutMs
	}

	// Enforce mutual exclusion: caveman and terse both inject system-prompt
	// directives that conflict, so only one can be active. The last toggle
	// set wins (frontend sends both fields; the one being enabled is the
	// "winner"). As a safety net for direct API callers, if both end up
	// enabled, disable the one that wasn't explicitly set in this patch.
	if current.CavemanEnabled && current.TerseEnabled {
		if patch.TerseEnabled != nil && *patch.TerseEnabled {
			current.CavemanEnabled = false
		} else {
			current.TerseEnabled = false
		}
	}

	raw, err := json.Marshal(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.settings.Set(r.Context(), endpointSettingsKey, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Notify pipeline of timeout changes so they take effect without restart.
	if s.timeoutNotifier != nil {
		s.timeoutNotifier.NotifyTimeouts(
			time.Duration(current.StreamStallTimeoutMs)*time.Millisecond,
			time.Duration(current.ResponseHeaderTimeoutMs)*time.Millisecond,
			time.Duration(current.RequestTimeoutMs)*time.Millisecond,
		)
	}

	// Notify dispatcher of proxy changes so they take effect without restart.
	if s.proxyNotifier != nil {
		s.proxyNotifier.NotifyProxy(
			current.OutboundProxyEnabled,
			current.OutboundProxyURL,
			current.OutboundNoProxy,
		)
	}

	// Notify limiter so changes take effect without restart.
	if s.rateLimiter != nil {
		s.rateLimiter.SetEnabled(current.RateLimitsEnabled)
	}

	writeJSON(w, http.StatusOK, current)
}

func (s *Server) endpointPlanOptions(ctx context.Context, opts dispatch.PlanOptions, targets []dispatch.Target, affinityKey string) dispatch.PlanOptions {
	es := s.loadEndpointSettings(ctx)
	switch es.RoutingStrategy {
	case string(dispatch.StrategyRoundRobin):
		opts.AccountStrategy = dispatch.StrategyRoundRobin
		opts.AccountStickyLimit = es.StickyLimit
	case string(dispatch.StrategySmartRoundRobin):
		opts.AccountStrategy = dispatch.StrategySmartRoundRobin
		opts.AccountStickyLimit = es.StickyLimit
		opts.AccountAffinityKey = affinityKey
	}
	if opts.Strategy == dispatch.StrategyRoundRobin || es.ComboStrategy == string(dispatch.StrategyRoundRobin) {
		opts.Strategy = dispatch.StrategyRoundRobin
		opts.StickyLimit = es.ComboStickyLimit
	}
	for _, target := range targets {
		ps := s.loadProviderRoutingSettings(ctx, target.Provider)
		if ps.RoutingStrategy == "inherit" {
			continue
		}
		if opts.ProviderAccountStrategies == nil {
			opts.ProviderAccountStrategies = map[string]dispatch.AccountRoutingOptions{}
		}
		route := dispatch.AccountRoutingOptions{
			StickyLimit: ps.StickyLimit,
		}
		if ps.AffinityTTLMinutes > 0 {
			route.AffinityTTL = time.Duration(ps.AffinityTTLMinutes) * time.Minute
		}
		switch ps.RoutingStrategy {
		case string(dispatch.StrategyRoundRobin):
			route.Strategy = dispatch.StrategyRoundRobin
		case string(dispatch.StrategySmartRoundRobin):
			route.Strategy = dispatch.StrategySmartRoundRobin
			route.AffinityKey = affinityKey
		default:
			route.Strategy = dispatch.StrategyFallback
		}
		opts.ProviderAccountStrategies[target.Provider] = route
	}
	return opts
}

func normalizeAccountRoutingStrategy(raw string) (string, bool) {
	switch normalizeStrategyToken(raw) {
	case "", "fill-first", "fill_first", "priority", "fallback":
		return "fill-first", true
	case "round-robin", "round_robin", "roundrobin":
		return string(dispatch.StrategyRoundRobin), true
	case "smart-round-robin", "smart_round_robin", "smartroundrobin", "smart":
		return string(dispatch.StrategySmartRoundRobin), true
	default:
		return "", false
	}
}

func normalizeComboRoutingStrategy(raw string) (string, bool) {
	switch normalizeStrategyToken(raw) {
	case "", "fallback", "priority", "fill-first", "fill_first":
		return string(dispatch.StrategyFallback), true
	case "round-robin", "round_robin", "roundrobin":
		return string(dispatch.StrategyRoundRobin), true
	default:
		return "", false
	}
}

func normalizeStrategyToken(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ---- provider routing settings --------------------------------------------

const providerRoutingPrefix = "provider_routing_"

// ProviderRoutingSettings optionally overrides account routing for one
// provider. Inherit uses the global EndpointSettings account strategy.
type ProviderRoutingSettings struct {
	RoutingStrategy    string `json:"routing_strategy"` // inherit | fill-first | round-robin | smart-round-robin
	StickyLimit        int    `json:"sticky_limit"`
	AffinityTTLMinutes int    `json:"affinity_ttl_minutes"`
}

func defaultProviderRoutingSettings() ProviderRoutingSettings {
	return ProviderRoutingSettings{
		RoutingStrategy:    "inherit",
		StickyLimit:        3,
		AffinityTTLMinutes: int(dispatch.DefaultAffinityTTL / time.Minute),
	}
}

func (s *Server) loadProviderRoutingSettings(ctx context.Context, provider string) ProviderRoutingSettings {
	def := defaultProviderRoutingSettings()
	if s.settings == nil || provider == "" {
		return def
	}
	raw, err := s.settings.Get(ctx, providerRoutingPrefix+provider)
	if err != nil || raw == "" {
		return def
	}
	var ps ProviderRoutingSettings
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return def
	}
	if normalized, ok := normalizeProviderRoutingStrategy(ps.RoutingStrategy); ok {
		ps.RoutingStrategy = normalized
	} else {
		ps.RoutingStrategy = def.RoutingStrategy
	}
	if ps.StickyLimit <= 0 {
		ps.StickyLimit = def.StickyLimit
	}
	if ps.AffinityTTLMinutes <= 0 {
		ps.AffinityTTLMinutes = def.AffinityTTLMinutes
	}
	return ps
}

func normalizeProviderRoutingStrategy(raw string) (string, bool) {
	switch normalizeStrategyToken(raw) {
	case "", "inherit":
		return "inherit", true
	case "fill-first", "fill_first", "priority", "fallback":
		return "fill-first", true
	case "round-robin", "round_robin", "roundrobin":
		return string(dispatch.StrategyRoundRobin), true
	case "smart-round-robin", "smart_round_robin", "smartroundrobin", "smart":
		return string(dispatch.StrategySmartRoundRobin), true
	default:
		return "", false
	}
}

func (s *Server) adminGetProviderRouting(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "id")
	if _, ok := connectors.SpecByID(provider); !ok {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	writeJSON(w, http.StatusOK, s.loadProviderRoutingSettings(r.Context(), provider))
}

func (s *Server) adminUpdateProviderRouting(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	provider := chi.URLParam(r, "id")
	if _, ok := connectors.SpecByID(provider); !ok {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	current := s.loadProviderRoutingSettings(r.Context(), provider)
	var patch struct {
		RoutingStrategy    *string `json:"routing_strategy"`
		StickyLimit        *int    `json:"sticky_limit"`
		AffinityTTLMinutes *int    `json:"affinity_ttl_minutes"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.RoutingStrategy != nil {
		normalized, ok := normalizeProviderRoutingStrategy(*patch.RoutingStrategy)
		if !ok {
			writeError(w, http.StatusBadRequest, "routing_strategy must be inherit, fill-first, round-robin, or smart-round-robin")
			return
		}
		current.RoutingStrategy = normalized
	}
	if patch.StickyLimit != nil {
		if *patch.StickyLimit < 1 {
			writeError(w, http.StatusBadRequest, "sticky_limit must be at least 1")
			return
		}
		current.StickyLimit = *patch.StickyLimit
	}
	if patch.AffinityTTLMinutes != nil {
		if *patch.AffinityTTLMinutes < 1 {
			writeError(w, http.StatusBadRequest, "affinity_ttl_minutes must be at least 1")
			return
		}
		current.AffinityTTLMinutes = *patch.AffinityTTLMinutes
	}
	raw, err := json.Marshal(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.settings.Set(r.Context(), providerRoutingPrefix+provider, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, current)
}

// ---- access settings --------------------------------------------------------

// accessSettingsKey is the settings-store key for the Endpoints page access
// options (local access, secure tunnel, Tailscale).
const accessSettingsKey = "access_settings"

// AccessSettings holds the endpoint reachability toggles. These are dashboard
// preferences; the actual tunnels are provisioned out-of-band, so the toggles
// record intent and surface the primary endpoint URL.
type AccessSettings struct {
	LocalEnabled  bool   `json:"local_enabled"`
	TunnelEnabled bool   `json:"tunnel_enabled"`
	Tailscale     bool   `json:"tailscale_enabled"`
	TunnelURL     string `json:"tunnel_url,omitempty"`
	TailscaleURL  string `json:"tailscale_url,omitempty"`
}

func defaultAccessSettings() AccessSettings {
	return AccessSettings{LocalEnabled: true, TunnelEnabled: false, Tailscale: false}
}

func (s *Server) loadAccessSettings(ctx context.Context) AccessSettings {
	def := defaultAccessSettings()
	if s.settings == nil {
		return def
	}
	raw, err := s.settings.Get(ctx, accessSettingsKey)
	if err != nil || raw == "" {
		return def
	}
	var as AccessSettings
	if err := json.Unmarshal([]byte(raw), &as); err != nil {
		return def
	}
	return as
}

// adminGetAccessSettings returns the access options plus the primary endpoint
// URL derived from the request, so the dashboard can render the Endpoints page.
func (s *Server) adminGetAccessSettings(w http.ResponseWriter, r *http.Request) {
	as := s.loadAccessSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"local_enabled":     as.LocalEnabled,
		"tunnel_enabled":    as.TunnelEnabled,
		"tailscale_enabled": as.Tailscale,
		"tunnel_url":        as.TunnelURL,
		"tailscale_url":     as.TailscaleURL,
		"endpoint_url":      s.publicBaseURL(r) + "/v1",
	})
}

// adminUpdateAccessSettings persists the access option toggles, merging a
// partial body over the current settings.
func (s *Server) adminUpdateAccessSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	current := s.loadAccessSettings(r.Context())

	var patch struct {
		LocalEnabled  *bool `json:"local_enabled"`
		TunnelEnabled *bool `json:"tunnel_enabled"`
		Tailscale     *bool `json:"tailscale_enabled"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.LocalEnabled != nil {
		current.LocalEnabled = *patch.LocalEnabled
	}
	if patch.TunnelEnabled != nil {
		current.TunnelEnabled = *patch.TunnelEnabled
	}
	if patch.Tailscale != nil {
		current.Tailscale = *patch.Tailscale
	}

	raw, err := json.Marshal(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.settings.Set(r.Context(), accessSettingsKey, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"local_enabled":     current.LocalEnabled,
		"tunnel_enabled":    current.TunnelEnabled,
		"tailscale_enabled": current.Tailscale,
		"endpoint_url":      s.publicBaseURL(r) + "/v1",
	})
}
