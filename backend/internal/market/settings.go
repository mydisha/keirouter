package market

import (
	"context"
	"encoding/json"
)

const SettingsKey = "market_pricing_settings"

type Settings struct {
	AutoRefresh            bool    `json:"auto_refresh"`
	RefreshIntervalSeconds int     `json:"refresh_interval_seconds"`
	MarkupPercent          float64 `json:"markup_percent"`
	// SafetyMarginEnabled floors the derived rate at the next-best (failover)
	// market price × (1 + SafetyMarginPercent/100), so a cheaper offer going
	// dark cannot make a chain sell below its fallback cost.
	SafetyMarginEnabled bool    `json:"safety_margin_enabled"`
	SafetyMarginPercent float64 `json:"safety_margin_percent"`
	// SurplusFallbackDepth is how many distinct trusted price levels Surplus may
	// fail over across for surplus-trusted slugs (sent as X-Min-Discount).
	// Zero disables the ceiling: Surplus may then fail over to any trusted offer.
	SurplusFallbackDepth int `json:"surplus_fallback_depth"`
	LastFetchedAt       string  `json:"last_fetched_at"`
	LastFetchError      string  `json:"last_fetch_error,omitempty"`
	LastSyncedCount     int     `json:"last_synced_count"`
}

func DefaultSettings() Settings {
	return Settings{AutoRefresh: false, RefreshIntervalSeconds: 30, MarkupPercent: 10, SafetyMarginEnabled: false, SafetyMarginPercent: 10, SurplusFallbackDepth: DefaultSurplusFallbackDepth}
}

func LoadSettings(ctx context.Context, get func(context.Context, string) (string, error)) Settings {
	def := DefaultSettings()
	raw, err := get(ctx, SettingsKey)
	if err != nil || raw == "" {
		return def
	}
	s := def
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return def
	}
	if s.RefreshIntervalSeconds <= 0 {
		s.RefreshIntervalSeconds = def.RefreshIntervalSeconds
	}
	if s.MarkupPercent < 0 {
		s.MarkupPercent = def.MarkupPercent
	}
	if s.SurplusFallbackDepth < 0 {
		s.SurplusFallbackDepth = def.SurplusFallbackDepth
	}
	if s.SafetyMarginPercent < 0 {
		s.SafetyMarginPercent = def.SafetyMarginPercent
	}
	return s
}

func SaveSettings(ctx context.Context, set func(context.Context, string, string) error, s Settings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return set(ctx, SettingsKey, string(raw))
}