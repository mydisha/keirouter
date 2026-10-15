package gateway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Complete runs a completion on behalf of the guardrails (the prompt
// injection LLM judge). It is routed like a client request, so any model,
// alias or chain configured in KeiRouter can serve as the judge, and its
// usage is metered against the key whose request triggered it. Guardrails
// are bypassed for the call itself so the judge is never judged.
func (s *Server) Complete(ctx context.Context, model string, req *core.ChatRequest) (*core.ChatResponse, error) {
	if s.pipeline == nil {
		return nil, fmt.Errorf("guardrails judge: pipeline not wired")
	}
	tenant := req.Metadata.TenantID
	if tenant == "" {
		tenant = store.DefaultTenantID
		req.Metadata.TenantID = tenant
	}
	resolved, err := resolveTargets(ctx, s.chains, s.aliases, s.latencyReader(), tenant, model)
	if err != nil {
		return nil, fmt.Errorf("guardrails judge: resolve %q: %w", model, err)
	}
	if len(resolved.Targets) == 0 {
		return nil, fmt.Errorf("guardrails judge: no route for model %q", model)
	}
	req.Model = model
	req.Stream = false
	req.Metadata.Provider = resolved.Targets[0].Provider
	req.Metadata.ChainID = resolved.PlanOpts.ChainID
	res, err := s.pipeline.Chat(guardrails.Bypass(ctx), req, pipeline.Options{
		Targets:   resolved.Targets,
		PlanOpts:  resolved.PlanOpts,
		SkipCache: true,
	})
	if err != nil {
		return nil, err
	}
	return res.Response, nil
}

// adminGuardrailCapabilities reports which optional engines this server can
// run (ML classifier, embeddings, LLM judge, Presidio, OpenAI moderation), so
// the dashboard can flag policy options that would have no effect.
func (s *Server) adminGuardrailCapabilities(w http.ResponseWriter, _ *http.Request) {
	out := map[string]map[string]bool{}
	for _, d := range s.guardrails.Detectors() {
		if c, ok := d.(guardrails.CapabilityReporter); ok {
			out[d.Name()] = c.Capabilities()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": out})
}
