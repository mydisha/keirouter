package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// The dashboard playground chats with any routable model from the admin UI.
// Its requests run through the same pipeline as /v1 under a managed key
// (identity.Playground), so routing, chains, guardrails, skills, budgets and
// usage behave exactly as they do for a client. The dashboard session is the
// only credential; the key's secret is never known to anyone.
//
// The group sits beside /api rather than inside it: every chat turn would
// otherwise be written to the admin audit log.
func (s *Server) mountPlayground(r chi.Router, limit func(http.Handler) http.Handler) {
	r.Route("/api/playground", func(r chi.Router) {
		r.Use(s.loopbackOnly)
		r.Use(s.sessionMiddleware)
		r.Get("/models", s.handlePlaygroundModels)
		r.Group(func(r chi.Router) {
			r.Use(limit)
			r.Use(s.playgroundAuth)
			r.Post("/chat/completions", s.handleOpenAIChat)
		})
	})
}

// playgroundAuth puts the playground key in context for the chat handler and
// names it on the console trace.
func (s *Server) playgroundAuth(next http.Handler) http.Handler {
	return s.tracedAuth(next, func(w http.ResponseWriter, r *http.Request, tr *reqTrace, next http.Handler) {
		key, err := s.identity.Playground(r.Context(), adminTenant)
		if err != nil {
			s.log.Error("playground key", "err", err)
			tr.reject(http.StatusInternalServerError, "Playground key unavailable", err.Error(),
				"KeiRouter could not load the dashboard playground's key. Check database health.")
			writeRequestError(w, r, http.StatusInternalServerError, "playground unavailable")
			return
		}
		tr.base.Key = key.Name
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyCtxKey, key)))
	})
}

// playgroundModel is one chat model the playground can pick, with the
// controls its composer offers.
type playgroundModel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	Icon         string `json:"icon,omitempty"`
	// Chain marks a routing chain called by name; its steps decide the rest.
	Chain bool `json:"chain,omitempty"`
	// Reasoning: a reasoning effort reaches the model and changes how it thinks.
	Reasoning bool `json:"reasoning"`
	// CanDisableReasoning: "none" turns thinking off rather than being ignored.
	CanDisableReasoning bool `json:"can_disable_reasoning"`
	Vision              bool `json:"vision"`
	PDF                 bool `json:"pdf"`
	ContextWindow       int  `json:"context_window,omitempty"`
}

// handlePlaygroundModels lists the chains and connected chat models, as
// /v1/models does, with the playground's controls per model.
func (s *Server) handlePlaygroundModels(w http.ResponseWriter, r *http.Request) {
	entries := s.targetableModels(r.Context(), adminTenant)
	out := make([]playgroundModel, 0, len(entries))
	for _, e := range entries {
		if e.Kind != string(core.ServiceLLM) {
			continue
		}
		if e.OwnedBy == "combo" {
			out = append(out, playgroundModel{ID: e.ID, Name: e.Name, Chain: true})
			continue
		}
		m := playgroundModel{ID: e.ID, Name: e.Name, Provider: e.Provider, Icon: "/providers/" + providerIconID(e.Provider) + ".png"}
		if m.Name == "" {
			m.Name = strings.TrimPrefix(e.ID, e.Provider+"/")
		}
		dialect := core.DialectOpenAI
		if spec, ok := connectors.SpecByID(e.Provider); ok {
			m.ProviderName = spec.DisplayName
			if spec.Dialect != "" {
				dialect = spec.Dialect
			}
		}
		m.Reasoning, m.CanDisableReasoning = transform.ReasoningControls(dialect, e.Provider, strings.TrimPrefix(e.ID, e.Provider+"/"))
		if c := e.Capabilities; c != nil {
			m.Vision, m.PDF, m.ContextWindow = c.Vision, c.PDF, c.ContextWindow
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out, "key_name": identity.PlaygroundKeyName})
}
