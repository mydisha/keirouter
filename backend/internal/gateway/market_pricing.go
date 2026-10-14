package gateway

import (
	"math"
	"net/http"
	"time"

	"github.com/mydisha/keirouter/backend/internal/market"
)

func (s *Server) adminGetMarketPricingSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "settings store not configured")
		return
	}
	writeJSON(w, http.StatusOK, market.LoadSettings(r.Context(), s.settings.Get))
}

func (s *Server) adminUpdateMarketPricingSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	current := market.LoadSettings(r.Context(), s.settings.Get)
	var patch struct {
		AutoRefresh            *bool    `json:"auto_refresh"`
		RefreshIntervalSeconds *int     `json:"refresh_interval_seconds"`
		MarkupPercent          *float64 `json:"markup_percent"`
		SafetyMarginEnabled    *bool    `json:"safety_margin_enabled"`
		SafetyMarginPercent    *float64 `json:"safety_margin_percent"`
		SurplusFallbackDepth   *int     `json:"surplus_fallback_depth"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.AutoRefresh != nil {
		current.AutoRefresh = *patch.AutoRefresh
	}
	if patch.RefreshIntervalSeconds != nil {
		if *patch.RefreshIntervalSeconds < 1 {
			writeError(w, http.StatusBadRequest, "refresh_interval_seconds must be at least 1")
			return
		}
		current.RefreshIntervalSeconds = *patch.RefreshIntervalSeconds
	}
	if patch.MarkupPercent != nil {
		v := *patch.MarkupPercent
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1000 {
			writeError(w, http.StatusBadRequest, "markup_percent must be between 0 and 1000")
			return
		}
		current.MarkupPercent = v
	}
	if patch.SafetyMarginEnabled != nil {
		current.SafetyMarginEnabled = *patch.SafetyMarginEnabled
	}
	if patch.SafetyMarginPercent != nil {
		v := *patch.SafetyMarginPercent
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1000 {
			writeError(w, http.StatusBadRequest, "safety_margin_percent must be between 0 and 1000")
			return
		}
		current.SafetyMarginPercent = v
	}
	if patch.SurplusFallbackDepth != nil {
		v := *patch.SurplusFallbackDepth
		if v < 0 || v > 20 {
			writeError(w, http.StatusBadRequest, "surplus_fallback_depth must be between 0 and 20")
			return
		}
		current.SurplusFallbackDepth = v
	}
	if err := market.SaveSettings(r.Context(), s.settings.Set, current); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func (s *Server) adminRefreshMarketPrices(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	current := market.LoadSettings(r.Context(), s.settings.Get)
	synced := 0
	if s.syncMarketPrices != nil {
		n, err := s.syncMarketPrices(r.Context())
		if err != nil {
			current.LastFetchError = err.Error()
			_ = market.SaveSettings(r.Context(), s.settings.Set, current)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		synced = n
	}
	current.LastFetchedAt = time.Now().UTC().Format(time.RFC3339)
	current.LastFetchError = ""
	current.LastSyncedCount = synced
	_ = market.SaveSettings(r.Context(), s.settings.Set, current)
	writeJSON(w, http.StatusOK, map[string]any{"synced": synced, "last_fetched_at": current.LastFetchedAt})
}
