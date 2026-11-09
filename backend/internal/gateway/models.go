package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// modelEntry is one entry in a /v1/models listing, in the OpenAI shape plus
// KeiRouter extensions (provider, kind, dimensions).
type modelEntry struct {
	ID               string                      `json:"id"`
	Object           string                      `json:"object"`
	OwnedBy          string                      `json:"owned_by"`
	Provider         string                      `json:"provider,omitempty"`
	Kind             string                      `json:"kind,omitempty"`
	Name             string                      `json:"name,omitempty"`
	Dimensions       int                         `json:"dimensions,omitempty"`
	Capabilities     *modelCapabilities          `json:"capabilities,omitempty"`
	CapabilitySource capability.CapabilitySource `json:"capability_source,omitempty"`
}

// handleListModels reports targetable models: the tenant's chains (as virtual
// models) plus every catalogued LLM model in provider/model form. This lets a
// client discover what it can pass in the `model` field.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": s.targetableModels(r.Context(), tenantOf(key))})
}

// targetableModels lists the chains and the connected providers' models a
// tenant can put in a request's `model` field.
func (s *Server) targetableModels(ctx context.Context, tenantID string) []modelEntry {
	data := make([]modelEntry, 0, 64)
	seen := make(map[string]struct{}, 64)
	usableProviders := s.usableModelProviders(ctx, tenantID)

	// Chains are exposed as "combo" models, matching the upstream convention:
	// a combo chains multiple providers with auto-fallback and is callable by
	// its bare name (and via the chain: prefix). owned_by:"combo" lets client
	// tools surface them distinctly from single-provider models.
	chains, err := s.chains.ListByTenant(ctx, tenantID)
	if err == nil {
		for _, c := range chains {
			data = appendModelEntry(data, seen, modelEntry{
				ID: c.Name, Object: "model", OwnedBy: "combo", Kind: string(core.ServiceLLM), Name: c.Name,
			})
		}
	}

	// Static catalog models for providers the tenant has connected. Without this
	// gate, discovery advertises provider/model ids that the dispatcher will later
	// reject with "no accounts configured".
	for _, pm := range connectors.ModelsByKind(core.ServiceLLM) {
		if !usableProviders[pm.Provider] {
			continue
		}
		caps, source := capabilityPayload(pm.Provider, pm.Model.ID, core.ServiceLLM)
		data = appendModelEntry(data, seen, modelEntry{
			ID:           pm.Provider + "/" + pm.Model.ID,
			Object:       "model",
			OwnedBy:      pm.Provider,
			Provider:     pm.Provider,
			Kind:         string(core.ServiceLLM),
			Name:         pm.Model.Name,
			Capabilities: &caps, CapabilitySource: source,
		})
	}

	// Live model discovery: for providers with a LiveModelSource and connected
	// accounts, fetch the live catalog and merge (live models supplement, not
	// replace, the static catalog).
	liveModels := s.fetchLiveModels(ctx, tenantID)
	for provider, models := range liveModels {
		if !usableProviders[provider] {
			continue
		}
		for _, lm := range models {
			caps, source := capabilityPayload(provider, lm.ID, lm.Kind)
			data = appendModelEntry(data, seen, modelEntry{
				ID:           provider + "/" + lm.ID,
				Object:       "model",
				OwnedBy:      provider,
				Provider:     provider,
				Kind:         string(lm.Kind),
				Name:         lm.Name,
				Capabilities: &caps, CapabilitySource: source,
			})
		}
	}

	return data
}

// handleListModelsByKind serves GET /v1/models/{kind}: it lists every model of
// the requested service kind (llm, embedding, image, stt, tts, search, fetch)
// across the provider catalog, plus a special "chains" view.
func (s *Server) handleListModelsByKind(w http.ResponseWriter, r *http.Request) {
	kindParam := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))

	// "chains" is a convenience view of the tenant's routing chains.
	if kindParam == "chains" {
		s.handleListModels(w, r)
		return
	}

	kind := core.ServiceKind(kindParam)
	if !core.ValidServiceKind(kind) {
		writeError(w, http.StatusBadRequest, "unknown model kind: "+kindParam)
		return
	}

	pairs := connectors.ModelsByKind(kind)
	data := make([]modelEntry, 0, len(pairs))
	seen := make(map[string]struct{}, len(pairs))
	key, _ := authedKey(r.Context())
	usableProviders := s.usableModelProviders(r.Context(), tenantOf(key))
	for _, pm := range pairs {
		if !usableProviders[pm.Provider] {
			continue
		}
		caps, source := capabilityPayload(pm.Provider, pm.Model.ID, pm.Model.Kind)
		data = appendModelEntry(data, seen, modelEntry{
			ID:           pm.Provider + "/" + pm.Model.ID,
			Object:       "model",
			OwnedBy:      pm.Provider,
			Provider:     pm.Provider,
			Kind:         string(pm.Model.Kind),
			Name:         pm.Model.Name,
			Dimensions:   pm.Model.Dimensions,
			Capabilities: &caps, CapabilitySource: source,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "kind": kindParam, "data": data})
}

// fetchLiveModels queries providers that support live model discovery, using
// the first connected account's credentials. Returns a map of provider →
// models. Errors are silently skipped (live discovery is best-effort).
func (s *Server) fetchLiveModels(ctx context.Context, tenantID string) map[string][]connectors.ModelSpec {
	if s.accounts == nil || s.vault == nil {
		return nil
	}
	result := map[string][]connectors.ModelSpec{}

	// Check each provider that has a live model source.
	for provider, src := range map[string]connectors.LiveModelSource{
		"kiro": connectors.GetLiveModelSource("kiro"),
	} {
		if src == nil {
			continue
		}
		accs, err := s.accounts.ListByProvider(ctx, tenantID, provider)
		if err != nil || len(accs) == 0 {
			continue
		}
		// Use the first non-disabled account.
		var acc store.Account
		for _, a := range accs {
			if !a.Disabled && !a.NeedsReconnect {
				acc = a
				break
			}
		}
		if acc.ID == "" {
			continue
		}
		creds, err := s.vault.Open(acc)
		if err != nil {
			continue
		}
		probeCtx, cancel := context.WithTimeout(s.accountEgressContext(ctx, acc), 10*time.Second)
		models, err := src.ListModels(probeCtx, creds)
		cancel()
		if err != nil || len(models) == 0 {
			continue
		}
		result[provider] = models
	}
	return result
}

func (s *Server) usableModelProviders(ctx context.Context, tenantID string) map[string]bool {
	usable := map[string]bool{}
	if s.accounts == nil {
		return usable
	}
	accs, err := s.accounts.ListByTenant(ctx, tenantID)
	if err != nil {
		return usable
	}
	for _, acc := range accs {
		if acc.Provider == "" || acc.Disabled || acc.NeedsReconnect {
			continue
		}
		usable[acc.Provider] = true
	}
	return usable
}

func appendModelEntry(data []modelEntry, seen map[string]struct{}, entry modelEntry) []modelEntry {
	if entry.ID == "" {
		return data
	}
	if _, ok := seen[entry.ID]; ok {
		return data
	}
	seen[entry.ID] = struct{}{}
	return append(data, entry)
}

// handleModelInfo serves GET /v1/models/info?id=<provider/model>: it returns
// metadata for a single model (kind, dimensions, provider, name).
func (s *Server) handleModelInfo(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id query parameter is required")
		return
	}

	provider, model, ok := strings.Cut(id, "/")
	if !ok || provider == "" || model == "" {
		writeError(w, http.StatusBadRequest, "id must be in provider/model form")
		return
	}
	key, _ := authedKey(r.Context())
	if !s.usableModelProviders(r.Context(), tenantOf(key))[provider] {
		writeError(w, http.StatusNotFound, "unknown model: "+id)
		return
	}

	spec, found := connectors.FindModel(provider, model)
	if !found {
		writeError(w, http.StatusNotFound, "unknown model: "+id)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"provider":   provider,
		"model":      spec.ID,
		"name":       spec.Name,
		"kind":       string(spec.Kind),
		"dimensions": spec.Dimensions,
	})
}
