// Package currency stores a refreshable USD->IDR exchange rate used to convert
// IDR payment-gateway top-ups into USD credit. Persistence reuses the existing
// key/value SettingsRepo; the rate and its fetch metadata live in one JSON blob.
package currency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
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

// Service owns currency settings persistence and a background refresh loop.
type Service struct {
	settings *store.SettingsRepo

	// persist, initial and readErr are seams for tests; when nil they fall back
	// to settings.Set / settings.Get.
	persist func(ctx context.Context, s Settings) error
	initial Settings
	readErr error
}

// New returns a currency service backed by the given settings repo.
func New(settings *store.SettingsRepo) *Service { return &Service{settings: settings} }

// readRaw returns the stored blob and whether the read succeeded. A missing key
// (store.ErrNotFound) or an absent repo is a successful empty read; any other
// repo error is reported so callers can avoid overwriting stored state.
func (svc *Service) readRaw(ctx context.Context) (string, error) {
	if svc.readErr != nil {
		return "", svc.readErr
	}
	if svc.settings == nil {
		return "", nil
	}
	raw, err := svc.settings.Get(ctx, SettingsKey)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (svc *Service) writeRaw(ctx context.Context, s Settings) error {
	if svc.persist != nil {
		return svc.persist(ctx, s)
	}
	if svc.settings == nil {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return svc.settings.Set(ctx, SettingsKey, string(raw))
}

// load reads the persisted settings strictly, reporting a repo read error so
// callers that write back can abort instead of clobbering stored state.
func (svc *Service) load(ctx context.Context) (Settings, error) {
	raw, err := svc.readRaw(ctx)
	if err != nil {
		return Settings{}, err
	}
	if svc.initial != (Settings{}) {
		return Defaults(svc.initial), nil
	}
	if raw == "" {
		return Defaults(Settings{}), nil
	}
	var s Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Defaults(Settings{}), nil
	}
	return Defaults(s), nil
}

// Load reads the persisted settings, applying defaults. Never errors; a read
// failure is treated as an empty config for display purposes.
func (svc *Service) Load(ctx context.Context) Settings {
	s, err := svc.load(ctx)
	if err != nil {
		return Defaults(Settings{})
	}
	return s
}

// Save validates and persists settings.
func (svc *Service) Save(ctx context.Context, s Settings) error {
	s = Defaults(s)
	if err := Validate(s); err != nil {
		return err
	}
	return svc.writeRaw(ctx, s)
}

// Fetch retrieves the USD->IDR rate from sourceURL.
func (svc *Service) Fetch(ctx context.Context, sourceURL string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("currency: upstream status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	return ParseUSDIDR(body)
}

// Refresh fetches the rate and persists it. On failure it keeps the previous
// rate and records the error; the returned error mirrors the failure.
func (svc *Service) Refresh(ctx context.Context) (Settings, error) {
	s, err := svc.load(ctx)
	if err != nil {
		// Fail closed: the stored state could not be read, so never write a
		// zeroed blob over it.
		return Settings{}, err
	}
	rate, err := svc.Fetch(ctx, s.SourceURL)
	if err != nil {
		s.LastError = err.Error()
		_ = svc.writeRaw(ctx, s)
		return s, err
	}
	s.Rate = rate
	s.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	s.Source = SourceAPI
	s.LastError = ""
	if werr := svc.writeRaw(ctx, s); werr != nil {
		return s, werr
	}
	return s, nil
}

// Current returns the effective rate for consumers (e.g. top-up conversion).
func (svc *Service) Current(ctx context.Context) (float64, string, bool) {
	return Resolve(svc.Load(ctx))
}

// Start launches the background refresh loop. It refreshes once on start when
// no rate exists and auto-refresh is on, then re-checks every minute and
// refreshes when the configured interval has elapsed.
func (svc *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s := svc.Load(ctx)
				if !s.AutoRefreshEnabled {
					continue
				}
				last := time.Time{}
				if s.FetchedAt != "" {
					if t, err := time.Parse(time.RFC3339, s.FetchedAt); err == nil {
						last = t
					}
				}
				due := s.Rate == 0 || time.Since(last) >= time.Duration(s.RefreshIntervalH)*time.Hour
				if due {
					if _, err := svc.Refresh(ctx); err != nil {
						slog.Default().Warn("currency refresh failed", "err", err)
					}
				}
			}
		}
	}()
}
