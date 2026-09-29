// Package currency stores a refreshable USD->IDR exchange rate used to convert
// IDR payment-gateway top-ups into USD credit. Persistence reuses the existing
// key/value SettingsRepo; the rate and its fetch metadata live in one JSON blob.
package currency

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
)

// SettingsKey is the settings-store key holding the currency JSON blob.
const SettingsKey = "currency_settings"

// DefaultSourceURL is the free, key-less exchange-rate endpoint (verified shape:
// {"result":"success","rates":{"IDR":<float>,"...":<float>}}).
const DefaultSourceURL = "https://open.er-api.com/v6/latest/USD"

// DefaultIntervalH is the default auto-refresh interval in hours.
const DefaultIntervalH = 24

// Rate source labels reported to the UI.
const (
	SourceAPI      = "api"
	SourceOverride = "override"
	SourceNone     = "none"
)

// Settings is the persisted currency configuration plus last-fetched rate state.
type Settings struct {
	AutoRefreshEnabled bool    `json:"auto_refresh_enabled"`
	RefreshIntervalH   int     `json:"refresh_interval_h"`
	OverrideEnabled    bool    `json:"override_enabled"`
	OverrideRate       float64 `json:"override_rate"`
	SourceURL          string  `json:"source_url"`

	Rate      float64 `json:"rate"`
	FetchedAt string  `json:"fetched_at"`
	Source    string  `json:"source"`
	LastError string  `json:"last_error"`
}

// Defaults fills unset/invalid fields with their defaults. Auto-refresh defaults
// to on for a fresh (zero) config; a persisted config is left as-is so an
// explicit auto_refresh_enabled=false is preserved.
func Defaults(s Settings) Settings {
	if s == (Settings{}) {
		s.AutoRefreshEnabled = true
	}
	if s.RefreshIntervalH <= 0 {
		s.RefreshIntervalH = DefaultIntervalH
	}
	if strings.TrimSpace(s.SourceURL) == "" {
		s.SourceURL = DefaultSourceURL
	}
	return s
}

// Validate rejects a configuration that cannot be persisted or used.
func Validate(s Settings) error {
	if s.RefreshIntervalH < 1 {
		return errors.New("refresh_interval_h must be at least 1")
	}
	if s.OverrideEnabled && !(s.OverrideRate > 0) {
		return errors.New("override_rate must be positive when override is enabled")
	}
	u, err := url.Parse(strings.TrimSpace(s.SourceURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("source_url must be a valid http(s) URL")
	}
	return nil
}

// Resolve returns the effective USD->IDR rate and its source. Override wins when
// enabled and valid; otherwise the last fetched rate is used; otherwise ok=false.
func Resolve(s Settings) (rate float64, source string, ok bool) {
	if s.OverrideEnabled && s.OverrideRate > 0 && !math.IsInf(s.OverrideRate, 0) {
		return s.OverrideRate, SourceOverride, true
	}
	if s.Rate > 0 && !math.IsInf(s.Rate, 0) {
		return s.Rate, SourceAPI, true
	}
	return 0, SourceNone, false
}

// ParseUSDIDR extracts the IDR rate from an er-api response body.
func ParseUSDIDR(body []byte) (float64, error) {
	var resp struct {
		Result string             `json:"result"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("currency: decode response: %w", err)
	}
	if resp.Result != "success" {
		return 0, fmt.Errorf("currency: provider returned result %q", resp.Result)
	}
	rate, ok := resp.Rates["IDR"]
	if !ok || rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return 0, errors.New("currency: response has no valid IDR rate")
	}
	return rate, nil
}
