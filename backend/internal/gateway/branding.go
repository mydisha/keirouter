package gateway

import (
	"context"
	"encoding/json"
	"net/http"
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
	ColorPalette string `json:"color_palette"` // Color palette identifier (e.g. "kei", "sage-terra", "ocean").
}

func defaultBrandingSettings() BrandingSettings {
	return BrandingSettings{
		Name:         "KeiRouter",
		LogoURL:      "",
		FaviconURL:   "",
		Tagline:      "",
		ColorPalette: "kei",
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

	raw, err := json.Marshal(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.settings.Set(r.Context(), brandingSettingsKey, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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