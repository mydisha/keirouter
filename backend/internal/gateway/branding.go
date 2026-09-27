package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// brandingSettingsKey is the settings-store key for white-label branding.
const brandingSettingsKey = "branding_settings"

// BrandingSettings holds configurable branding for the dashboard and portal.
// When empty, defaults to KeiRouter branding.
type BrandingSettings struct {
	Name         string `json:"name"`          // Display name (e.g. "KeiRouter", "Acme AI Gateway")
	LogoURL      string `json:"logo_url"`      // URL to logo image (SVG/PNG). Empty = default logo.
	FaviconURL   string `json:"favicon_url"`   // URL to favicon (PNG/ICO). Empty = default favicon.
	Tagline      string `json:"tagline"`       // Optional short tagline shown on portal login.
	ColorPalette string `json:"color_palette"` // Color palette identifier (e.g. "sage-terra", "ocean", "midnight").
	// APIKeyPrefix is the canonical "token_" prefix stamped onto newly created
	// API keys. Existing keys are unaffected.
	APIKeyPrefix string `json:"api_key_prefix"`
}

func defaultBrandingSettings() BrandingSettings {
	return BrandingSettings{
		Name:         "KeiRouter",
		LogoURL:      "",
		FaviconURL:   "",
		Tagline:      "",
		ColorPalette: "sage-terra",
		APIKeyPrefix: crypto.DefaultKeyPrefix,
	}
}

// loadBrandingSettings reads the persisted branding, falling back to defaults
// when unset. Never errors.
func (s *Server) loadBrandingSettings(ctx context.Context) BrandingSettings {
	def := defaultBrandingSettings()
	if s.settings == nil {
		return def
	}
	raw, err := s.settings.Get(ctx, brandingSettingsKey)
	if err != nil || raw == "" {
		return def
	}
	var bs BrandingSettings
	if err := json.Unmarshal([]byte(raw), &bs); err != nil {
		return def
	}
	// Backfill defaults for empty fields.
	if bs.Name == "" {
		bs.Name = def.Name
	}
	if normalized, ok := identity.NormalizePrefix(bs.APIKeyPrefix); ok {
		bs.APIKeyPrefix = normalized
	} else {
		bs.APIKeyPrefix = def.APIKeyPrefix
	}
	return bs
}

// ---- admin endpoints --------------------------------------------------------

// adminGetBranding returns the current branding configuration.
func (s *Server) adminGetBranding(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadBrandingSettings(r.Context()))
}

// adminUpdateBranding persists branding configuration. Accepts a partial body
// and merges over the current settings.
func (s *Server) adminUpdateBranding(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	current := s.loadBrandingSettings(r.Context())

	var patch struct {
		Name         *string `json:"name"`
		LogoURL      *string `json:"logo_url"`
		FaviconURL   *string `json:"favicon_url"`
		Tagline      *string `json:"tagline"`
		ColorPalette *string `json:"color_palette"`
		APIKeyPrefix *string `json:"api_key_prefix"`
	}
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.Name != nil {
		current.Name = *patch.Name
	}
	if patch.LogoURL != nil {
		current.LogoURL = *patch.LogoURL
	}
	if patch.FaviconURL != nil {
		current.FaviconURL = *patch.FaviconURL
	}
	if patch.Tagline != nil {
		current.Tagline = *patch.Tagline
	}
	if patch.ColorPalette != nil {
		current.ColorPalette = *patch.ColorPalette
	}
	if patch.APIKeyPrefix != nil {
		normalized, ok := identity.NormalizePrefix(*patch.APIKeyPrefix)
		if !ok {
			writeError(w, http.StatusBadRequest, "api_key_prefix must be 1-16 lowercase letters or digits")
			return
		}
		current.APIKeyPrefix = normalized
	}

	raw, err := json.Marshal(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.settings.Set(r.Context(), brandingSettingsKey, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.identity != nil {
		s.identity.SetKeyPrefix(current.APIKeyPrefix)
	}
	writeJSON(w, http.StatusOK, current)
}

// ---- public portal endpoint -------------------------------------------------

// portalBranding returns branding config for the public portal (no auth).
// This allows the portal to display the custom name and logo without requiring
// a dashboard session.
func (s *Server) portalBranding(w http.ResponseWriter, r *http.Request) {
	bs := s.loadBrandingSettings(r.Context())
	writeJSON(w, http.StatusOK, bs)
}

// LoadAPIKeyPrefix reads the persisted API key prefix from the settings store,
// normalized to its canonical "token_" form. It returns the default ("kr_") when
// unset, unreadable, or invalid. Exported so non-Server callers (app startup,
// CLI bootstrap) share the same persisted branding blob.
func LoadAPIKeyPrefix(ctx context.Context, settings *store.SettingsRepo) string {
	if settings == nil {
		return crypto.DefaultKeyPrefix
	}
	raw, err := settings.Get(ctx, brandingSettingsKey)
	if err != nil || raw == "" {
		return crypto.DefaultKeyPrefix
	}
	var bs BrandingSettings
	if err := json.Unmarshal([]byte(raw), &bs); err != nil {
		return crypto.DefaultKeyPrefix
	}
	if normalized, ok := identity.NormalizePrefix(bs.APIKeyPrefix); ok {
		return normalized
	}
	return crypto.DefaultKeyPrefix
}
