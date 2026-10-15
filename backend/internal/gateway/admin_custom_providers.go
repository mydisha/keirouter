package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// mountCustomProviders registers the dynamic custom-provider and custom-model
// admin endpoints. Custom providers are user-defined instances of the OpenAI-
// or Anthropic-compatible dialects, each isolated under a unique provider id.
func (s *Server) mountCustomProviders(r chi.Router) {
	r.Get("/custom-providers", s.adminListCustomProviders)
	r.Post("/custom-providers", s.adminCreateCustomProvider)
	r.Patch("/custom-providers/{id}", s.adminUpdateCustomProvider)
	r.Delete("/custom-providers/{id}", s.adminDeleteCustomProvider)

	// Custom models can be attached to any provider id (custom or built-in).
	r.Get("/providers/{id}/custom-models", s.adminListCustomModels)
	r.Post("/providers/{id}/custom-models", s.adminCreateCustomModel)
	r.Patch("/providers/{id}/custom-models/{modelDBID}", s.adminUpdateCustomModel)
	r.Delete("/providers/{id}/custom-models/{modelDBID}", s.adminDeleteCustomModel)

	// Import models: fetch the upstream /models listing and persist each entry
	// as a custom model, so a custom provider becomes routable without manual
	// model entry. Available for any provider with a LiveModelSource.
	r.Post("/providers/{id}/import-models", s.adminImportModels)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// aliasInputRe constrains a user-provided alias to url-safe slug characters
// (letters, digits, hyphen). slugify then lowercases/compacts it.
var aliasInputRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// resolveCustomAlias derives the routing alias for a custom provider. When the
// user supplies an alias it is validated (slug-safe, 1-32 chars, no collision
// with an existing provider alias/id other than the provider's own id). When
// omitted, it defaults to the slugified display name, falling back to the
// unique provider id when the name yields no usable slug.
func resolveCustomAlias(rawAlias, name, ownID string) (string, error) {
	raw := strings.TrimSpace(rawAlias)
	if raw == "" {
		// Default from the display name.
		if a := slugify(name); a != "" {
			if _, ok := connectors.SpecByAlias(a); !ok && a != ownID {
				return a, nil
			}
		}
		return ownID, nil
	}
	if !aliasInputRe.MatchString(raw) {
		return "", fmt.Errorf("alias must contain only letters, digits, and hyphens")
	}
	alias := slugify(raw)
	if alias == "" {
		return "", fmt.Errorf("alias must contain only letters, digits, and hyphens")
	}
	if len(alias) > 32 {
		return "", fmt.Errorf("alias must be 32 characters or fewer")
	}
	if alias != ownID {
		if _, ok := connectors.SpecByAlias(alias); ok {
			return "", fmt.Errorf("alias already in use: %s", alias)
		}
	}
	return alias, nil
}

// slugify produces a short url-safe token from a display name.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
		s = strings.Trim(s, "-")
	}
	return s
}

func customProviderJSON(p store.CustomProvider) map[string]any {
	return map[string]any{
		"id":           p.ID,
		"display_name": p.DisplayName,
		"alias":        p.Alias,
		"dialect":      p.Dialect,
		"base_url":     p.BaseURL,
		"custom":       true,
		"created_at":   p.CreatedAt,
		"updated_at":   p.UpdatedAt,
	}
}

func customModelJSON(m store.CustomModel) map[string]any {
	return map[string]any{
		"db_id":          m.ID,
		"provider_id":    m.ProviderID,
		"id":             m.ModelID,
		"name":           m.DisplayName,
		"kind":           m.Kind,
		"context_window": m.ContextWindow,
		"input_per_m":    m.InputPerM,
		"output_per_m":   m.OutputPerM,
	}
}

func (s *Server) adminListCustomProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.db.CustomProviders().ListProviders(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(providers))
	for _, p := range providers {
		out = append(out, customProviderJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) adminCreateCustomProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string  `json:"display_name"`
		Dialect     string  `json:"dialect"` // "openai" | "anthropic"
		BaseURL     string  `json:"base_url"`
		Alias       *string `json:"alias"` // optional routing prefix; defaults to the generated id
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	dialect, prefix, ok := customDialect(body.Dialect)
	if !ok {
		writeError(w, http.StatusBadRequest, "dialect must be 'openai' or 'anthropic'")
		return
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	if err := httputil.ValidateBaseURL(baseURL); err != nil {
		s.log.Warn("blocked suspicious base_url", "url", baseURL, "error", err)
		writeError(w, http.StatusBadRequest, "invalid base_url: URL blocked by security policy")
		return
	}

	id := uniqueCustomProviderID(prefix, name, func(candidate string) bool {
		if _, exists := connectors.SpecByID(candidate); exists {
			return true
		}
		return false
	})

	// Alias is the user-facing routing prefix ("<alias>/<model>"). It is NOT
	// forced under the custom-openai-/custom-anthropic- namespace, mirroring
	// the decoupling of internal node id from the user-chosen prefix.
	// Empty defaults to the slugified name (fallback: the unique id).
	var aliasInput string
	if body.Alias != nil {
		aliasInput = *body.Alias
	}
	alias, aerr := resolveCustomAlias(aliasInput, name, id)
	if aerr != nil {
		writeError(w, http.StatusBadRequest, aerr.Error())
		return
	}

	p := store.CustomProvider{
		ID:          id,
		TenantID:    adminTenant,
		DisplayName: name,
		Alias:       alias,
		Dialect:     string(dialect),
		BaseURL:     baseURL,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.db.CustomProviders().CreateProvider(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "create custom provider failed"))
		return
	}
	// Register it live so it is immediately routable and discoverable.
	connectors.RegisterDynamicProvider(connectors.DynamicProvider{
		ID: p.ID, DisplayName: p.DisplayName, Alias: p.Alias, Dialect: dialect, BaseURL: p.BaseURL,
	})
	writeJSON(w, http.StatusCreated, customProviderJSON(p))
}

func (s *Server) adminUpdateCustomProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.db.CustomProviders().GetProvider(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown custom provider: "+id)
		return
	}
	var body struct {
		DisplayName *string `json:"display_name"`
		Alias       *string `json:"alias"`
		BaseURL     *string `json:"base_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DisplayName != nil {
		if n := strings.TrimSpace(*body.DisplayName); n != "" {
			existing.DisplayName = n
		}
	}
	if body.Alias != nil {
		alias, aerr := resolveCustomAlias(*body.Alias, existing.DisplayName, existing.ID)
		if aerr != nil {
			writeError(w, http.StatusBadRequest, aerr.Error())
			return
		}
		existing.Alias = alias
	}
	baseURLChanged := false
	if body.BaseURL != nil {
		b := strings.TrimSpace(*body.BaseURL)
		if b == "" {
			writeError(w, http.StatusBadRequest, "base_url cannot be empty")
			return
		}
		if err := httputil.ValidateBaseURL(b); err != nil {
			s.log.Warn("blocked suspicious base_url", "url", b, "error", err)
			writeError(w, http.StatusBadRequest, "invalid base_url: URL blocked by security policy")
			return
		}
		baseURLChanged = b != existing.BaseURL
		existing.BaseURL = b
	}
	if err := s.db.CustomProviders().UpdateProvider(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "update custom provider failed"))
		return
	}
	dialect, _, _ := customDialect(existing.Dialect)
	connectors.RegisterDynamicProvider(connectors.DynamicProvider{
		ID: existing.ID, DisplayName: existing.DisplayName, Alias: existing.Alias, Dialect: dialect, BaseURL: existing.BaseURL,
	})
	if baseURLChanged {
		// A /min{N} segment in the base URL changes which surplus-trusted
		// offers qualify; re-quote now rather than on the next refresh tick.
		s.resyncMarketPricesInBackground("custom provider " + existing.ID)
	}
	writeJSON(w, http.StatusOK, customProviderJSON(existing))
}

func (s *Server) adminDeleteCustomProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// The generic custom-compatible gateways ("custom-openai" /
	// "custom-anthropic") are built-in catalog templates, not user-defined
	// instances. They are never stored in custom_providers and must never be
	// deleted -- only dynamic instances under the custom-openai-* /
	// custom-anthropic-* namespaces can be.
	if id == "custom-openai" || id == "custom-anthropic" {
		writeError(w, http.StatusConflict, "cannot delete built-in custom provider: "+id)
		return
	}

	if _, err := s.db.CustomProviders().GetProvider(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "unknown custom provider: "+id)
		return
	}
	if err := s.db.CustomProviders().DeleteProvider(r.Context(), id); err != nil {
		// A concurrent delete may have removed the row between the GetProvider
		// probe and now; surface it as 404 rather than a 500.
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "unknown custom provider: "+id)
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "delete custom provider failed"))
		return
	}

	// Disable any accounts still bound to the deleted provider so stale
	// credentials are not selected for routing and the dashboard reflects the
	// severed connection. Best-effort: the provider row is already gone, so a
	// failure here is logged but must not undo the delete.
	var accountsDisabled int64
	if s.accounts != nil {
		n, derr := s.accounts.DisableByProvider(r.Context(), adminTenant, id)
		if derr != nil {
			s.log.Warn("disable accounts after provider delete failed", "provider", id, "err", derr)
		} else {
			accountsDisabled = n
		}
	}

	// Drop the in-memory dynamic provider + its models so routing/discovery
	// stop exposing it immediately.
	connectors.UnregisterDynamicProvider(id)
	s.reloadUsagePricing(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                id,
		"deleted":           true,
		"accounts_disabled": accountsDisabled,
	})
}

// ---- custom models ----------------------------------------------------------

func (s *Server) adminListCustomModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	models, err := s.db.CustomProviders().ListManualModelsByProvider(r.Context(), providerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, customModelJSON(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

func (s *Server) adminCreateCustomModel(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	if _, ok := connectors.SpecByID(providerID); !ok {
		writeError(w, http.StatusNotFound, "unknown provider: "+providerID)
		return
	}
	var body struct {
		ModelID       string  `json:"id"`
		DisplayName   string  `json:"name"`
		Kind          string  `json:"kind"`
		ContextWindow int     `json:"context_window"`
		InputPerM     float64 `json:"input_per_m"`
		OutputPerM    float64 `json:"output_per_m"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	modelID := strings.TrimSpace(body.ModelID)
	if modelID == "" {
		writeError(w, http.StatusBadRequest, "model id is required")
		return
	}
	kind := normalizeModelKind(body.Kind)
	if !core.ValidServiceKind(core.ServiceKind(kind)) {
		writeError(w, http.StatusBadRequest, "unknown model kind: "+body.Kind)
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		name = modelID
	}
	m := store.CustomModel{
		ID:            uuid.NewString(),
		TenantID:      adminTenant,
		ProviderID:    providerID,
		ModelID:       modelID,
		DisplayName:   name,
		Kind:          kind,
		ContextWindow: body.ContextWindow,
		InputPerM:     body.InputPerM,
		OutputPerM:    body.OutputPerM,
	}
	if err := s.db.CustomProviders().CreateModel(r.Context(), m); err != nil {
		writeError(w, http.StatusBadRequest, sanitizeError(s.log, err, "create custom model failed (model id may already exist)"))
		return
	}
	s.reloadCustomModels(r.Context(), providerID)
	writeJSON(w, http.StatusCreated, customModelJSON(m))
}

func (s *Server) adminUpdateCustomModel(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	dbID := chi.URLParam(r, "modelDBID")
	existing, err := s.db.CustomProviders().GetModel(r.Context(), dbID)
	if err != nil || existing.ProviderID != providerID {
		writeError(w, http.StatusNotFound, "unknown custom model")
		return
	}
	var body struct {
		ModelID       *string  `json:"id"`
		DisplayName   *string  `json:"name"`
		Kind          *string  `json:"kind"`
		ContextWindow *int     `json:"context_window"`
		InputPerM     *float64 `json:"input_per_m"`
		OutputPerM    *float64 `json:"output_per_m"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ModelID != nil {
		if v := strings.TrimSpace(*body.ModelID); v != "" {
			existing.ModelID = v
		}
	}
	if body.DisplayName != nil {
		existing.DisplayName = strings.TrimSpace(*body.DisplayName)
	}
	if existing.DisplayName == "" {
		existing.DisplayName = existing.ModelID
	}
	if body.Kind != nil {
		kind := normalizeModelKind(*body.Kind)
		if !core.ValidServiceKind(core.ServiceKind(kind)) {
			writeError(w, http.StatusBadRequest, "unknown model kind: "+*body.Kind)
			return
		}
		existing.Kind = kind
	}
	if body.ContextWindow != nil {
		existing.ContextWindow = *body.ContextWindow
	}
	if body.InputPerM != nil {
		existing.InputPerM = *body.InputPerM
	}
	if body.OutputPerM != nil {
		existing.OutputPerM = *body.OutputPerM
	}
	if err := s.db.CustomProviders().UpdateModel(r.Context(), existing); err != nil {
		writeError(w, http.StatusBadRequest, sanitizeError(s.log, err, "update custom model failed"))
		return
	}
	s.reloadCustomModels(r.Context(), providerID)
	writeJSON(w, http.StatusOK, customModelJSON(existing))
}

func (s *Server) adminDeleteCustomModel(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	dbID := chi.URLParam(r, "modelDBID")
	existing, err := s.db.CustomProviders().GetModel(r.Context(), dbID)
	if err != nil || existing.ProviderID != providerID {
		writeError(w, http.StatusNotFound, "unknown custom model")
		return
	}
	if err := s.db.CustomProviders().DeleteModel(r.Context(), dbID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "delete custom model failed"))
		return
	}
	s.reloadCustomModels(r.Context(), providerID)
	writeJSON(w, http.StatusOK, map[string]any{"db_id": dbID, "deleted": true})
}

// reloadCustomModels refreshes the in-memory dynamic model set for a provider
// from the database so routing/discovery reflect the change immediately.
func (s *Server) reloadCustomModels(ctx context.Context, providerID string) {
	models, err := s.db.CustomProviders().ListModelsByProvider(ctx, providerID)
	if err != nil {
		s.log.Warn("reload custom models failed", "provider", providerID, "err", err)
		return
	}
	connectors.SetDynamicModels(providerID, customModelsToSpecs(models))
	s.reloadUsagePricing(ctx)
}

func (s *Server) reloadUsagePricing(ctx context.Context) {
	if s.reloadPricing == nil {
		return
	}
	if err := s.reloadPricing(ctx); err != nil {
		s.log.Warn("reload usage pricing failed", "err", err)
	}
}

func customModelsToSpecs(models []store.CustomModel) []connectors.ModelSpec {
	out := make([]connectors.ModelSpec, 0, len(models))
	for _, m := range models {
		kind := core.ServiceKind(m.Kind)
		if kind == "" {
			kind = core.ServiceLLM
		}
		out = append(out, connectors.ModelSpec{ID: m.ModelID, Name: m.DisplayName, Kind: kind})
	}
	return out
}

func normalizeModelKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return string(core.ServiceLLM)
	}
	return kind
}

// customDialect maps a user-supplied dialect token to a core dialect and the
// provider-id prefix for that dialect.
func customDialect(token string) (core.Dialect, string, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "openai", "openai-compatible", "":
		return core.DialectOpenAI, connectors.CustomOpenAIPrefix, true
	case "anthropic", "anthropic-compatible":
		return core.DialectAnthropic, connectors.CustomAnthropicPrefix, true
	default:
		return "", "", false
	}
}

// uniqueCustomProviderID builds a unique provider id from a prefix and a name
// slug, appending a short uuid suffix when the slug collides.
func uniqueCustomProviderID(prefix, name string, taken func(string) bool) string {
	slug := slugify(name)
	if slug == "" {
		slug = "instance"
	}
	candidate := prefix + slug
	if !taken(candidate) {
		return candidate
	}
	return prefix + slug + "-" + strings.Split(uuid.NewString(), "-")[0]
}

// adminImportModels fetches the upstream /models listing for a provider and
// persists each discovered model as a custom model, so a custom provider
// becomes routable without manual model entry. Models already registered are
// skipped (matched by model id). Requires a LiveModelSource for the provider;
// credentials are taken from the first enabled account, if any.
func (s *Server) adminImportModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	if _, ok := connectors.SpecByID(providerID); !ok {
		writeError(w, http.StatusNotFound, "unknown provider: "+providerID)
		return
	}
	src := connectors.GetLiveModelSource(providerID)
	if src == nil {
		writeError(w, http.StatusBadRequest, "provider does not support model discovery")
		return
	}

	// Collect credentials from the first enabled account, if any.
	var creds core.Credentials
	if s.accounts != nil && s.vault != nil {
		accs, err := s.accounts.ListByProvider(r.Context(), adminTenant, providerID)
		if err == nil {
			for _, acc := range accs {
				if acc.Disabled {
					continue
				}
				if c, oerr := s.vault.Open(acc); oerr == nil {
					creds = c
					break
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	discovered, err := src.ListModels(ctx, creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "model discovery failed: "+sanitizeError(s.log, err, "model discovery failed"))
		return
	}

	existing, _ := s.db.CustomProviders().ListModelsByProvider(r.Context(), providerID)
	seen := make(map[string]bool, len(existing))
	for _, m := range existing {
		seen[m.ModelID] = true
	}

	imported := 0
	skipped := 0
	for _, m := range discovered {
		if m.ID == "" || seen[m.ID] {
			skipped++
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		kind := string(m.Kind)
		if kind == "" {
			kind = string(core.ServiceLLM)
		}
		cm := store.CustomModel{
			ID:          uuid.NewString(),
			TenantID:    adminTenant,
			ProviderID:  providerID,
			ModelID:     m.ID,
			DisplayName: name,
			Kind:        kind,
			Source:      "imported",
		}
		if err := s.db.CustomProviders().CreateModel(r.Context(), cm); err != nil {
			// Likely a unique-constraint race; skip rather than abort the batch.
			skipped++
			continue
		}
		seen[m.ID] = true
		imported++
	}
	if imported > 0 {
		s.reloadCustomModels(r.Context(), providerID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": providerID,
		"imported":    imported,
		"skipped":     skipped,
		"total":       len(discovered),
	})
}
