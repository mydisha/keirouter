package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/consolelog"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// mountAdmin registers the dashboard admin endpoints on the given router. These
// manage API keys, provider accounts, routing chains, budgets, and usage.
func (s *Server) mountAdmin(r chi.Router) {
	r.Get("/providers", s.adminListProviders)
	r.Get("/providers/{id}/models", s.adminProviderModels)
	r.Get("/providers/{id}/routing", s.adminGetProviderRouting)
	r.Post("/providers/{id}/routing", s.adminUpdateProviderRouting)
	r.Patch("/providers/{id}/routing", s.adminUpdateProviderRouting)

	r.Get("/keys", s.adminListKeys)
	r.Post("/keys", s.adminCreateKey)
	r.Patch("/keys/{id}", s.adminUpdateKey)
	r.Delete("/keys/{id}", s.adminDeleteKey)

	r.Get("/accounts", s.adminListAccounts)
	r.Post("/accounts", s.adminCreateAccount)
	r.Post("/accounts/bulk", s.adminBulkCreateAccounts)
	r.Post("/validate-key", s.adminValidateKey)
	r.Patch("/accounts/{id}", s.adminUpdateAccount)
	r.Delete("/accounts/{id}", s.adminDeleteAccount)
	r.Post("/accounts/{id}/test", s.adminTestAccount)
	r.Get("/accounts/{id}/quota", s.adminAccountQuota)
	r.Get("/accounts/{id}/codex-reset-credits", s.adminCodexResetCredits)
	r.Post("/accounts/{id}/codex-consume-credit", s.adminCodexConsumeCredit)
	r.Get("/accounts/{id}/codex-usage-details", s.adminCodexUsageDetails)

	r.Get("/chains", s.adminListChains)
	r.Post("/chains", s.adminCreateChain)
	r.Patch("/chains/{id}", s.adminUpdateChain)
	r.Delete("/chains/{id}", s.adminDeleteChain)

	r.Get("/plans", s.adminListPlans)
	r.Post("/plans", s.adminCreatePlan)
	r.Patch("/plans/{id}", s.adminUpdatePlan)
	r.Delete("/plans/{id}", s.adminDeletePlan)
	r.Get("/plans/{id}/keys", s.adminListPlanKeys)

	r.Get("/budgets", s.adminListBudgets)
	r.Get("/budgets/status", s.adminBudgetStatus)
	r.Post("/budgets", s.adminCreateBudget)
	r.Patch("/budgets/{id}", s.adminUpdateBudget)
	r.Delete("/budgets/{id}", s.adminDeleteBudget)

	r.Get("/usage", s.adminUsageSummary)
	r.Get("/usage/insights", s.adminUsageInsights)
	r.Get("/usage/models", s.adminModelUsageAccurate)
	r.Get("/usage/stream", s.adminUsageStream)
	r.Get("/quota", s.adminQuotaUsage)
	r.Get("/health/accounts", s.adminListAccountHealth)
	r.Post("/health/check-now", s.adminRunHealthCheck)
	s.mountProviderHealth(r)
	r.Get("/console", s.adminConsoleLog)
	r.Delete("/console", s.adminConsoleClear)
	r.Get("/console/stream", s.adminConsoleStream)

	r.Get("/proxy-pools", s.adminListProxyPools)
	r.Post("/proxy-pools", s.adminCreateProxyPool)
	r.Post("/proxy-pools/cloudflare-deploy", s.adminDeployCloudflareRelay)
	r.Patch("/proxy-pools/{id}", s.adminUpdateProxyPool)
	r.Delete("/proxy-pools/{id}", s.adminDeleteProxyPool)
	r.Post("/proxy-pools/{id}/test", s.adminTestProxyPool)

	r.Get("/skills", s.adminListSkills)
	r.Post("/skills", s.adminCreateSkill)
	r.Post("/skills/{id}", s.adminUpdateSkill)
	r.Delete("/skills/{id}", s.adminDeleteSkill)

	r.Get("/models/alias", s.adminListAliases)
	r.Put("/models/alias", s.adminSetAlias)
	r.Delete("/models/alias", s.adminDeleteAlias)

	r.Get("/models/disabled", s.adminListDisabledModels)
	r.Post("/models/disabled", s.adminDisableModels)
	r.Delete("/models/disabled", s.adminEnableModels)

	r.Get("/settings/endpoint", s.adminGetEndpointSettings)
	r.Post("/settings/endpoint", s.adminUpdateEndpointSettings)
	r.Post("/settings/headroom-test", s.adminTestHeadroom)
	r.Get("/settings/access", s.adminGetAccessSettings)
	r.Post("/settings/access", s.adminUpdateAccessSettings)
	r.Get("/settings/database", s.adminExportDatabase)
	r.Post("/settings/database", s.adminImportDatabase)
	r.Post("/settings/database/import-foreign", s.adminImportForeignConfig)
	r.Get("/settings/sqlite", s.adminSQLiteStatus)
	r.Get("/settings/sqlite/backup", s.adminSQLiteBackup)
	r.Post("/settings/sqlite/restore", s.adminSQLiteRestore)
	r.Post("/settings/proxy-test", s.adminTestProxy)

	// Update check (queries GitHub for the latest release + changelog).
	r.Get("/update/check", s.adminUpdateCheck)

	// Tunnel management endpoints.
	r.Get("/tunnel/status", s.adminTunnelStatus)
	r.Post("/tunnel/enable", s.adminTunnelEnable)
	r.Post("/tunnel/disable", s.adminTunnelDisable)
	r.Get("/tunnel/tailscale-check", s.adminTailscaleCheck)
	r.Post("/tunnel/tailscale-enable", s.adminTailscaleEnable)
	r.Post("/tunnel/tailscale-disable", s.adminTailscaleDisable)
	r.Post("/tunnel/tailscale-install", s.adminTailscaleInstall)

	s.mountOAuth(r)
	s.mountKiro(r)
	s.mountCustomFlows(r)
	s.mountCustomProviders(r)

	s.mountCLITools(r)

	// Branding / white-label settings.
	r.Get("/settings/branding", s.adminGetBranding)
	r.Post("/settings/branding", s.adminUpdateBranding)

	// System monitoring (CPU, memory, disk, Go runtime).
	r.Get("/system", s.adminSystem)
	r.Get("/system/history", s.adminSystemHistory)
	r.Get("/system/resources", s.adminSystemResourceHistory)

	// Guardrails (content safety policies + audit log).
	s.mountGuardrails(r)
}

const adminTenant = store.DefaultTenantID

// ---- providers --------------------------------------------------------------

func (s *Server) adminListProviders(w http.ResponseWriter, r *http.Request) {
	// Optional ?kind= filter restricts to providers serving a service kind.
	kindFilter := core.ServiceKind(r.URL.Query().Get("kind"))

	specs := connectors.Catalog()
	out := make([]map[string]any, 0, len(specs))
	for _, p := range specs {
		if kindFilter != "" && !core.HasServiceKind(p.ServiceKinds, kindFilter) {
			continue
		}
		kinds := p.ServiceKinds
		if len(kinds) == 0 {
			kinds = []core.ServiceKind{core.ServiceLLM}
		}
		entry := map[string]any{
			"id":            p.ID,
			"display_name":  p.DisplayName,
			"alias":         p.Alias,
			"dialect":       p.Dialect,
			"auth_kind":     p.AuthKind,
			"auth_modes":    p.AuthModesOf(),
			"service_kinds": kinds,
			"color":         p.Color,
			"website":       p.Website,
			"api_key_url":   p.APIKeyURL,
			"icon":          "/providers/" + providerIconID(p.ID) + ".png",
			"deprecated":    p.Deprecated,
			"hidden":        p.Hidden,
			"pinned":        p.Pinned,
			"notice":        p.Notice,
			"drivable":      connectors.DrivableDialect(p.Dialect) || webProvider(p.ID),
			"input_per_m":   p.InputPerM,
			"output_per_m":  p.OutputPerM,
		}
		// Custom (user-defined) provider instances expose their configured base
		// URL so the dashboard can surface it on the provider detail page.
		if p.Custom {
			entry["custom"] = true
			entry["base_url"] = p.BaseURL
		}
		if len(p.Regions) > 0 {
			regions := make([]map[string]string, 0, len(p.Regions))
			for _, r := range p.Regions {
				regions = append(regions, map[string]string{
					"id":       r.ID,
					"label":    r.Label,
					"base_url": r.BaseURL,
				})
			}
			entry["regions"] = regions
			entry["default_region"] = p.DefaultRegion
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// providerIconID returns the provider ID used for the icon filename. Dynamic
// (user-created) custom providers inherit the logo of their base type so they
// show the OpenAI/Anthropic icon instead of a broken image or initials.
func providerIconID(id string) string {
	if strings.HasPrefix(id, connectors.CustomOpenAIPrefix) {
		return "custom-openai"
	}
	if strings.HasPrefix(id, connectors.CustomAnthropicPrefix) {
		return "custom-anthropic"
	}
	return id
}

// webProvider reports whether a provider is served by the web search/fetch
// connector (so it is routable even though its dialect is the generic openai).
func webProvider(id string) bool {
	switch id {
	case "tavily", "exa", "serper", "brave-search", "searxng", "firecrawl", "jina-reader":
		return true
	default:
		return false
	}
}

// adminProviderModels returns the model list for a specific provider. It
// includes static catalog models and, when a connected account exists, live
// models from the upstream (e.g. Kiro's ListAvailableModels).
func (s *Server) adminProviderModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	if _, ok := connectors.SpecByID(providerID); !ok {
		writeError(w, http.StatusNotFound, "unknown provider: "+providerID)
		return
	}
	kindFilter := core.ServiceKind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind"))))
	if kindFilter != "" && !core.ValidServiceKind(kindFilter) {
		writeError(w, http.StatusBadRequest, "unknown model kind: "+string(kindFilter))
		return
	}

	type modelInfo struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Custom     bool   `json:"custom,omitempty"`
		DBID       string `json:"db_id,omitempty"`
		Discovered bool   `json:"discovered,omitempty"`
	}
	modelKind := func(kind core.ServiceKind) core.ServiceKind {
		if kind == "" {
			return core.ServiceLLM
		}
		return kind
	}

	// User-registered custom models for this provider (db-backed). These are
	// tracked separately so the dashboard can render an editable section.
	customByID := map[string]store.CustomModel{}
	if cms, cerr := s.db.CustomProviders().ListModelsByProvider(r.Context(), providerID); cerr == nil {
		for _, cm := range cms {
			customByID[cm.ModelID] = cm
		}
	}

	// Static catalog models (already merged with custom models by
	// ModelsForProvider). Flag any entry that is a user-defined custom model.
	static := connectors.ModelsForProvider(providerID)
	seen := map[string]bool{}
	out := make([]modelInfo, 0, len(static))
	for _, m := range static {
		kind := modelKind(m.Kind)
		if kindFilter != "" && kind != kindFilter {
			continue
		}
		mi := modelInfo{ID: m.ID, Name: m.Name, Kind: string(kind)}
		if cm, ok := customByID[m.ID]; ok {
			mi.Custom = true
			mi.DBID = cm.ID
		}
		out = append(out, mi)
		seen[m.ID] = true
	}

	// Live model discovery (best-effort). A connected account's credentials are
	// preferred since most upstreams gate /models behind auth. When no account
	// yields models and nothing else is in the catalog, fall back to an
	// unauthenticated fetch so providers whose /models endpoint is public (e.g.
	// sumopod) still populate before an account is connected.
	if src := connectors.GetLiveModelSource(providerID); src != nil {
		appendLive := func(creds core.Credentials) bool {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			models, merr := src.ListModels(ctx, creds)
			cancel()
			if merr != nil || len(models) == 0 {
				return false
			}
			added := false
			for _, lm := range models {
				kind := modelKind(lm.Kind)
				if kindFilter != "" && kind != kindFilter {
					continue
				}
				if seen[lm.ID] {
					continue
				}
				out = append(out, modelInfo{ID: lm.ID, Name: lm.Name, Kind: string(kind)})
				seen[lm.ID] = true
				added = true
			}
			return added
		}

		discovered := false
		if s.accounts != nil && s.vault != nil {
			if accs, err := s.accounts.ListByProvider(r.Context(), adminTenant, providerID); err == nil {
				for _, acc := range accs {
					if acc.Disabled {
						continue
					}
					creds, oerr := s.vault.Open(acc)
					if oerr != nil {
						continue
					}
					if appendLive(creds) {
						discovered = true
						break // only use first valid account
					}
				}
			}
		}

		// Public fallback: only when we have nothing else to show, to avoid an
		// extra upstream round-trip for providers that already have models.
		if !discovered && len(out) == 0 {
			appendLive(core.Credentials{})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// ---- API keys ---------------------------------------------------------------

func (s *Server) adminListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.identity.List(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		entry := map[string]any{
			"id": k.ID, "name": k.Name, "display": k.Display,
			"disabled": k.Disabled, "plan_id": k.PlanID, "created_at": k.CreatedAt,
		}
		// Resolve plan name.
		if k.PlanID != "" {
			if plan, perr := s.db.Plans().Get(r.Context(), k.PlanID); perr == nil {
				entry["plan_name"] = plan.Name
			}
		}
		// Attach allowed models (empty = all allowed).
		if models, merr := s.identity.Keys().GetAllowedModels(r.Context(), k.ID); merr == nil {
			entry["allowed_models"] = models
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (s *Server) adminCreateKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id"`
		// Optional plan assignment. When set, the key inherits the plan's
		// budget rules (unless per-key overrides are also provided).
		PlanID string `json:"plan_id"`
		// Optional per-key budget overrides — these take precedence over plan
		// defaults when the key has a plan assigned.
		BudgetLimitUSD    *float64 `json:"budget_limit_usd"`
		BudgetLimitTokens *int64   `json:"budget_limit_tokens"`
		BudgetPeriod      string   `json:"budget_period"`
		BudgetAlertPct    *int     `json:"budget_alert_pct"`
		BudgetHardCutoff  *bool    `json:"budget_hard_cutoff"`
		// Optional per-key model access restriction (overrides plan models).
		AllowedModels []string `json:"allowed_models"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.BudgetLimitUSD != nil && *body.BudgetLimitUSD < 0 {
		writeError(w, http.StatusBadRequest, "budget_limit_usd must not be negative")
		return
	}
	if body.BudgetLimitTokens != nil && *body.BudgetLimitTokens < 0 {
		writeError(w, http.StatusBadRequest, "budget_limit_tokens must not be negative")
		return
	}

	// Resolve plan if one was specified.
	var plan *store.Plan
	if body.PlanID != "" {
		p, err := s.db.Plans().Get(r.Context(), body.PlanID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "plan not found")
				return
			}
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		plan = &p
	}

	// Generate key material (crypto operations, no DB write yet).
	issued, err := s.identity.Generate(adminTenant, body.ProjectID, body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	// Set plan_id on the key record.
	if plan != nil {
		issued.Record.PlanID = plan.ID
	}

	// Determine effective budget: per-key overrides > plan defaults.
	hasPerKeyBudget := (body.BudgetLimitUSD != nil && *body.BudgetLimitUSD > 0) ||
		(body.BudgetLimitTokens != nil && *body.BudgetLimitTokens > 0)
	hasPlanBudget := plan != nil && (plan.LimitMicros > 0 || plan.LimitTokens > 0)
	hasBudget := hasPerKeyBudget || hasPlanBudget
	hasPerKeyModels := len(body.AllowedModels) > 0
	hasPlanModels := plan != nil && plan.AllowedModels != ""
	hasModels := hasPerKeyModels || hasPlanModels

	if !hasBudget && !hasModels && plan == nil {
		// Simple path: no budget, no models, no plan — insert key directly.
		if err := s.identity.CreateFromIssued(r.Context(), issued); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": issued.Record.ID, "name": issued.Record.Name,
			"key": issued.Plaintext, "display": issued.Record.Display, "plan_id": issued.Record.PlanID,
		})
		return
	}

	// Transactional path: key + budget + model access atomically.
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "transaction start failed")
		return
	}
	defer func() { _ = tx.Rollback() }() // no-op after commit

	if err := s.identity.Keys().CreateOnTx(r.Context(), tx, issued.Record); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var budgetRec store.Budget
	if hasBudget {
		// Resolve effective values: per-key overrides win, then plan, then defaults.
		hardCutoff := true
		alertPct := 80
		period := "monthly"
		var limitMicros int64
		var limitTokens int64

		if plan != nil {
			hardCutoff = plan.HardCutoff
			alertPct = plan.AlertPct
			period = plan.Period
			limitMicros = plan.LimitMicros
			limitTokens = plan.LimitTokens
		}

		// Per-key overrides take precedence.
		if body.BudgetHardCutoff != nil {
			hardCutoff = *body.BudgetHardCutoff
		}
		if body.BudgetAlertPct != nil {
			alertPct = *body.BudgetAlertPct
		}
		if body.BudgetPeriod != "" {
			if p, ok := normalizeBudgetPeriod(body.BudgetPeriod); ok {
				period = p
			}
		}
		if body.BudgetLimitUSD != nil && *body.BudgetLimitUSD > 0 {
			limitMicros = int64(*body.BudgetLimitUSD * 1_000_000)
		}
		if body.BudgetLimitTokens != nil && *body.BudgetLimitTokens > 0 {
			limitTokens = *body.BudgetLimitTokens
		}

		if limitMicros <= 0 && limitTokens <= 0 {
			// Plan had no limits and no per-key overrides — skip budget creation.
			hasBudget = false
		} else {
			if alertPct < 1 || alertPct > 100 {
				writeError(w, http.StatusBadRequest, "budget_alert_pct must be between 1 and 100")
				return
			}

			now := time.Now()
			budgetRec = store.Budget{
				ID:          uuid.NewString(),
				TenantID:    adminTenant,
				ScopeKind:   store.ScopeAPIKey,
				ScopeID:     issued.Record.ID,
				LimitMicros: limitMicros,
				LimitTokens: limitTokens,
				Period:      period,
				AlertPct:    alertPct,
				HardCutoff:  hardCutoff,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := s.budgets.CreateOnTx(r.Context(), tx, budgetRec); err != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				return
			}
		}
	}

	if hasModels {
		// Per-key models take precedence over plan models.
		effectiveModels := body.AllowedModels
		if !hasPerKeyModels && plan != nil {
			effectiveModels = store.GetPlanAllowedModels(*plan)
		}
		if len(effectiveModels) > 0 {
			if err := s.identity.Keys().SetAllowedModelsOnTx(r.Context(), tx, issued.Record.ID, effectiveModels); err != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "transaction commit failed")
		return
	}

	// Invalidate the budget definition cache so the next request picks up
	// the newly-created budget immediately.
	if hasBudget && s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCache()
	}

	resp := map[string]any{
		"id": issued.Record.ID, "name": issued.Record.Name,
		"key": issued.Plaintext, "display": issued.Record.Display, "plan_id": issued.Record.PlanID,
	}
	if hasBudget {
		resp["budget"] = map[string]any{
			"id": budgetRec.ID, "scope_kind": string(budgetRec.ScopeKind),
			"limit_micros": budgetRec.LimitMicros, "limit_tokens": budgetRec.LimitTokens,
			"period": budgetRec.Period, "alert_pct": budgetRec.AlertPct, "hard_cutoff": budgetRec.HardCutoff,
		}
	}
	effectiveModels := body.AllowedModels
	if !hasPerKeyModels && plan != nil {
		effectiveModels = store.GetPlanAllowedModels(*plan)
	}
	if len(effectiveModels) > 0 {
		resp["allowed_models"] = effectiveModels
	}
	if plan != nil {
		resp["plan"] = map[string]any{
			"id": plan.ID, "name": plan.Name,
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) adminDeleteKey(w http.ResponseWriter, r *http.Request) {
	if err := s.identity.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminUpdateKey toggles a key's disabled state and/or updates its model access.
func (s *Server) adminUpdateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Disabled      *bool    `json:"disabled"`
		AllowedModels []string `json:"allowed_models"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Disabled == nil && body.AllowedModels == nil {
		writeError(w, http.StatusBadRequest, "disabled or allowed_models field is required")
		return
	}
	if body.Disabled != nil {
		if err := s.identity.SetDisabled(r.Context(), id, *body.Disabled); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
	}
	if body.AllowedModels != nil {
		if err := s.identity.Keys().SetAllowedModels(r.Context(), id, body.AllowedModels); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "disabled": body.Disabled, "allowed_models": body.AllowedModels})
}

// ---- accounts ---------------------------------------------------------------

func (s *Server) adminListAccounts(w http.ResponseWriter, r *http.Request) {
	accs, err := s.accounts.ListByTenant(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(accs))
	for _, a := range accs {
		// Never expose secret material.
		out = append(out, map[string]any{
			"id": a.ID, "provider": a.Provider, "label": a.Label,
			"auth_kind": a.AuthKind, "priority": a.Priority,
			"disabled": a.Disabled, "proxy_pool_id": a.ProxyPoolID,
			"needs_reconnect": a.NeedsReconnect,
			"created_at":      a.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (s *Server) adminCreateAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider          string `json:"provider"`
		Label             string `json:"label"`
		APIKey            string `json:"api_key"`
		BaseURL           string `json:"base_url"`
		Region            string `json:"region"`
		AccountID         string `json:"account_id"`
		AzureEndpoint     string `json:"azure_endpoint"`
		AzureDeployment   string `json:"azure_deployment"`
		AzureAPIVersion   string `json:"azure_api_version"`
		AzureOrganization string `json:"azure_organization"`
		ProxyPoolID       string `json:"proxy_pool_id"`
		Priority          int    `json:"priority"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	spec, ok := connectors.SpecByID(body.Provider)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown provider: "+body.Provider)
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusInternalServerError, "vault not configured")
		return
	}
	if err := s.validateProxyPoolID(r.Context(), body.ProxyPoolID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	authKind := accountAuthKind(spec, body.APIKey)
	if authKind != store.AuthNone && strings.TrimSpace(body.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "api_key is required")
		return
	}

	// SSRF Protection: Validate base_url before use
	if body.BaseURL != "" {
		if err := httputil.ValidateBaseURL(body.BaseURL); err != nil {
			s.log.Warn("blocked suspicious base_url", "url", body.BaseURL, "error", err)
			writeError(w, http.StatusBadRequest, "invalid base_url: URL blocked by security policy")
			return
		}
	}
	if body.AzureEndpoint != "" {
		if err := httputil.ValidateBaseURL(body.AzureEndpoint); err != nil {
			s.log.Warn("blocked suspicious azure_endpoint", "url", body.AzureEndpoint, "error", err)
			writeError(w, http.StatusBadRequest, "invalid azure_endpoint: URL blocked by security policy")
			return
		}
	}
	meta, err := providerAccountMetadata(spec, providerMetadataInput{
		BaseURL:           body.BaseURL,
		Region:            body.Region,
		AccountID:         body.AccountID,
		AzureEndpoint:     body.AzureEndpoint,
		AzureDeployment:   body.AzureDeployment,
		AzureAPIVersion:   body.AzureAPIVersion,
		AzureOrganization: body.AzureOrganization,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now()
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = spec.DisplayName
	}
	acc := store.Account{
		ID:        uuid.NewString(),
		TenantID:  adminTenant,
		Provider:  body.Provider,
		Label:     label,
		AuthKind:  authKind,
		Priority:  defaultInt(body.Priority, 100),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if body.ProxyPoolID != "" {
		acc.ProxyPoolID = strings.TrimSpace(body.ProxyPoolID)
	}
	if err := s.vault.Seal(&acc, vault.NewSecret{APIKey: body.APIKey, Metadata: meta}); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "vault seal failed"))
		return
	}

	// Validate credentials against the upstream before persisting.
	if verr := s.validateAccountCredentials(r.Context(), acc); verr != nil {
		writeError(w, http.StatusBadRequest, sanitizeError(s.log, verr, "credential validation failed"))
		return
	}

	if err := s.accounts.Create(r.Context(), acc); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "account creation failed"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": acc.ID, "provider": acc.Provider, "label": acc.Label})
}

// bulkMaxItems caps the number of credentials accepted in a single bulk import
// to bound memory, upstream validation fan-out, and DB write time.
const bulkMaxItems = 1000

// bulkValidateConcurrency bounds how many upstream credential probes run at
// once during a bulk import with validation enabled.
const bulkValidateConcurrency = 6

type bulkAccountItem struct {
	Label   string `json:"label"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
}

type bulkAccountsRequest struct {
	Provider string `json:"provider"`
	// Shared settings applied to every item unless an item overrides them.
	BaseURL           string `json:"base_url"`
	Region            string `json:"region"`
	AccountID         string `json:"account_id"`
	AzureEndpoint     string `json:"azure_endpoint"`
	AzureDeployment   string `json:"azure_deployment"`
	AzureAPIVersion   string `json:"azure_api_version"`
	AzureOrganization string `json:"azure_organization"`
	Priority          int    `json:"priority"`
	ProxyPoolID       string `json:"proxy_pool_id"`
	// Validate probes each credential against the upstream before persisting.
	// Off by default for bulk to avoid slow imports and upstream rate limits.
	Validate bool              `json:"validate"`
	Items    []bulkAccountItem `json:"items"`
}

type bulkAccountResult struct {
	Index  int    `json:"index"`
	Label  string `json:"label"`
	Status string `json:"status"` // created | error | skipped
	ID     string `json:"id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// adminBulkCreateAccounts imports many provider credentials in one request. It
// reuses the same sealing, metadata, validation, and persistence path as the
// single-create handler, but reports a per-item outcome so partial failures
// don't abort the whole batch. Upstream validation (when enabled) runs with a
// bounded worker pool; DB writes are serialized to stay friendly to SQLite.
func (s *Server) adminBulkCreateAccounts(w http.ResponseWriter, r *http.Request) {
	var body bulkAccountsRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	spec, ok := connectors.SpecByID(body.Provider)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown provider: "+body.Provider)
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusInternalServerError, "vault not configured")
		return
	}
	if len(body.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items is required")
		return
	}
	if len(body.Items) > bulkMaxItems {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many items: %d (max %d)", len(body.Items), bulkMaxItems))
		return
	}
	if err := s.validateProxyPoolID(r.Context(), body.ProxyPoolID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// SSRF Protection: validate the shared base/endpoint URLs once up front.
	if body.BaseURL != "" {
		if err := httputil.ValidateBaseURL(body.BaseURL); err != nil {
			s.log.Warn("blocked suspicious base_url", "url", body.BaseURL, "error", err)
			writeError(w, http.StatusBadRequest, "invalid base_url: URL blocked by security policy")
			return
		}
	}
	if body.AzureEndpoint != "" {
		if err := httputil.ValidateBaseURL(body.AzureEndpoint); err != nil {
			s.log.Warn("blocked suspicious azure_endpoint", "url", body.AzureEndpoint, "error", err)
			writeError(w, http.StatusBadRequest, "invalid azure_endpoint: URL blocked by security policy")
			return
		}
	}

	results := make([]bulkAccountResult, len(body.Items))
	var (
		seen    = map[string]struct{}{} // de-dup api keys within the batch
		seenMu  sync.Mutex
		writeMu sync.Mutex // serialize DB writes (SQLite-friendly)
		sem     = make(chan struct{}, bulkValidateConcurrency)
		wg      sync.WaitGroup
	)

	for i, item := range body.Items {
		label := strings.TrimSpace(item.Label)
		key := strings.TrimSpace(item.APIKey)
		results[i] = bulkAccountResult{Index: i, Label: label}

		authKind := accountAuthKind(spec, key)
		if authKind != store.AuthNone && key == "" {
			results[i].Status = "error"
			results[i].Error = "api_key is required"
			continue
		}

		// De-duplicate identical keys within the same batch.
		if key != "" {
			seenMu.Lock()
			if _, dup := seen[key]; dup {
				seenMu.Unlock()
				results[i].Status = "skipped"
				results[i].Error = "duplicate api key in batch"
				continue
			}
			seen[key] = struct{}{}
			seenMu.Unlock()
		}

		// Per-item base URL overrides the shared one when present.
		baseURL := strings.TrimSpace(item.BaseURL)
		if baseURL == "" {
			baseURL = body.BaseURL
		}
		if baseURL != "" {
			if err := httputil.ValidateBaseURL(baseURL); err != nil {
				results[i].Status = "error"
				results[i].Error = "invalid base_url: URL blocked by security policy"
				continue
			}
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(i int, label, key, baseURL string, authKind store.AuthKind) {
			defer wg.Done()
			defer func() { <-sem }()

			meta, err := providerAccountMetadata(spec, providerMetadataInput{
				BaseURL:           baseURL,
				Region:            body.Region,
				AccountID:         body.AccountID,
				AzureEndpoint:     body.AzureEndpoint,
				AzureDeployment:   body.AzureDeployment,
				AzureAPIVersion:   body.AzureAPIVersion,
				AzureOrganization: body.AzureOrganization,
			})
			if err != nil {
				results[i].Status = "error"
				results[i].Error = err.Error()
				return
			}

			now := time.Now()
			displayLabel := label
			if displayLabel == "" {
				displayLabel = fmt.Sprintf("%s-%d", spec.DisplayName, i+1)
			}
			acc := store.Account{
				ID:        uuid.NewString(),
				TenantID:  adminTenant,
				Provider:  body.Provider,
				Label:     displayLabel,
				AuthKind:  authKind,
				Priority:  defaultInt(body.Priority, 100),
				CreatedAt: now,
				UpdatedAt: now,
			}
			if body.ProxyPoolID != "" {
				acc.ProxyPoolID = strings.TrimSpace(body.ProxyPoolID)
			}
			if err := s.vault.Seal(&acc, vault.NewSecret{APIKey: key, Metadata: meta}); err != nil {
				results[i].Status = "error"
				results[i].Error = "vault seal failed"
				return
			}

			if body.Validate {
				if verr := s.validateAccountCredentials(r.Context(), acc); verr != nil {
					results[i].Status = "error"
					results[i].Error = sanitizeError(s.log, verr, "credential validation failed")
					return
				}
			}

			writeMu.Lock()
			err = s.accounts.Create(r.Context(), acc)
			writeMu.Unlock()
			if err != nil {
				results[i].Status = "error"
				results[i].Error = sanitizeError(s.log, err, "account creation failed")
				return
			}
			results[i].Status = "created"
			results[i].ID = acc.ID
			results[i].Label = displayLabel
		}(i, label, key, baseURL, authKind)
	}

	wg.Wait()

	sort.Slice(results, func(a, b int) bool { return results[a].Index < results[b].Index })
	var created, failed, skipped int
	for _, res := range results {
		switch res.Status {
		case "created":
			created++
		case "skipped":
			skipped++
		default:
			failed++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":   len(results),
		"created": created,
		"failed":  failed,
		"skipped": skipped,
		"results": results,
	})
}

func (s *Server) adminDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.accounts.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminValidateKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider          string `json:"provider"`
		Label             string `json:"label"`
		APIKey            string `json:"api_key"`
		BaseURL           string `json:"base_url"`
		Region            string `json:"region"`
		AccountID         string `json:"account_id"`
		AzureEndpoint     string `json:"azure_endpoint"`
		AzureDeployment   string `json:"azure_deployment"`
		AzureAPIVersion   string `json:"azure_api_version"`
		AzureOrganization string `json:"azure_organization"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	spec, ok := connectors.SpecByID(body.Provider)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown provider: "+body.Provider)
		return
	}
	authKind := accountAuthKind(spec, body.APIKey)
	if authKind != store.AuthNone && strings.TrimSpace(body.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "provider and api_key are required")
		return
	}
	if s.vault == nil || s.conns == nil {
		writeError(w, http.StatusInternalServerError, "vault or connectors not configured")
		return
	}

	// SSRF Protection: Validate base_url before use
	if body.BaseURL != "" {
		if err := httputil.ValidateBaseURL(body.BaseURL); err != nil {
			s.log.Warn("blocked suspicious base_url", "url", body.BaseURL, "error", err)
			writeError(w, http.StatusBadRequest, "invalid base_url: URL blocked by security policy")
			return
		}
	}
	if body.AzureEndpoint != "" {
		if err := httputil.ValidateBaseURL(body.AzureEndpoint); err != nil {
			s.log.Warn("blocked suspicious azure_endpoint", "url", body.AzureEndpoint, "error", err)
			writeError(w, http.StatusBadRequest, "invalid azure_endpoint: URL blocked by security policy")
			return
		}
	}
	meta, err := providerAccountMetadata(spec, providerMetadataInput{
		BaseURL:           body.BaseURL,
		Region:            body.Region,
		AccountID:         body.AccountID,
		AzureEndpoint:     body.AzureEndpoint,
		AzureDeployment:   body.AzureDeployment,
		AzureAPIVersion:   body.AzureAPIVersion,
		AzureOrganization: body.AzureOrganization,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "message": err.Error()})
		return
	}

	// Build a temporary in-memory account without persisting.
	acc := store.Account{
		ID:       "validate-temp",
		Provider: body.Provider,
		AuthKind: authKind,
	}
	if err := s.vault.Seal(&acc, vault.NewSecret{APIKey: body.APIKey, Metadata: meta}); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "vault seal failed"))
		return
	}

	if verr := s.validateAccountCredentials(r.Context(), acc); verr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "message": verr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) adminUpdateAccount(w http.ResponseWriter, r *http.Request) {
	acc, err := s.accounts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	var body struct {
		Label       *string `json:"label"`
		Priority    *int    `json:"priority"`
		Disabled    *bool   `json:"disabled"`
		ProxyPoolID *string `json:"proxy_pool_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label != nil {
		acc.Label = *body.Label
	}
	if body.Priority != nil {
		acc.Priority = *body.Priority
	}
	if body.Disabled != nil {
		acc.Disabled = *body.Disabled
	}
	if body.ProxyPoolID != nil {
		poolID := strings.TrimSpace(*body.ProxyPoolID)
		if err := s.validateProxyPoolID(r.Context(), poolID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		acc.ProxyPoolID = poolID
	}
	if err := s.accounts.Update(r.Context(), acc); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": acc.ID, "provider": acc.Provider, "label": acc.Label,
		"priority": acc.Priority, "disabled": acc.Disabled,
		"proxy_pool_id": acc.ProxyPoolID,
	})
}

func (s *Server) validateProxyPoolID(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if s.pools == nil {
		return fmt.Errorf("proxy pools not configured")
	}
	if _, err := s.pools.Get(ctx, id); err != nil {
		return fmt.Errorf("proxy pool not found")
	}
	return nil
}

func (s *Server) adminTestAccount(w http.ResponseWriter, r *http.Request) {
	acc, err := s.accounts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	verr := s.validateAccountCredentials(r.Context(), acc)
	if verr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"id":       acc.ID,
			"provider": acc.Provider,
			"label":    acc.Label,
			"status":   "error",
			"message":  verr.Error(),
		})
		return
	}
	// Validation passed: clear needs_reconnect if it was flagged, since a
	// successful probe means the current credentials are accepted.
	if acc.NeedsReconnect {
		if err := s.accounts.SetNeedsReconnect(r.Context(), acc.ID, false); err != nil {
			s.log.Warn("failed to clear needs_reconnect after successful test", "account", acc.ID, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       acc.ID,
		"provider": acc.Provider,
		"label":    acc.Label,
		"status":   "ok",
	})
}

// adminAccountQuota fetches upstream quota/credit info for a specific account.
func (s *Server) adminAccountQuota(w http.ResponseWriter, r *http.Request) {
	acc, err := s.accounts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	qs := connectors.GetQuotaSource(acc.Provider)
	if qs == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":  acc.Provider,
			"supported": false,
			"message":   "Upstream quota not available for this provider.",
		})
		return
	}

	if s.vault == nil {
		writeError(w, http.StatusInternalServerError, "vault not configured")
		return
	}

	// Quota endpoints are often called after the page has been idle for a while.
	// Refresh OAuth credentials first so one stale account does not lose its
	// usage panel while the rest of the provider page continues to work.
	quotaAcc := acc
	if s.refresher != nil {
		if refreshed, refreshErr := s.refresher.EnsureFresh(r.Context(), acc); refreshErr == nil {
			quotaAcc = refreshed
		}
	}

	creds, err := s.vault.Open(quotaAcc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not decrypt credentials")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	quota, qerr := qs.FetchQuota(ctx, creds)
	if qerr != nil {
		writeError(w, http.StatusBadGateway, qerr.Error())
		return
	}

	var quotas []map[string]any
	for _, q := range quota.Quotas {
		quotas = append(quotas, map[string]any{
			"resource_type": q.ResourceType,
			"used":          q.Used,
			"limit":         q.Limit,
			"remaining":     q.Remaining,
			"reset_at":      q.ResetAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider":  acc.Provider,
		"supported": true,
		"plan_name": quota.PlanName,
		"message":   quota.Message,
		"quotas":    quotas,
	})
}

// adminCodexResetCredits fetches available Codex rate-limit reset credits and
// their expiry details from the ChatGPT backend API. Only valid for Codex
// (OpenAI) accounts with OAuth credentials.
func (s *Server) adminCodexResetCredits(w http.ResponseWriter, r *http.Request) {
	acc, err := s.accounts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if acc.Provider != "codex" {
		writeError(w, http.StatusBadRequest, "only supported for codex accounts")
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusInternalServerError, "vault not configured")
		return
	}

	creds, err := s.vault.Open(acc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not decrypt credentials")
		return
	}
	if creds.AccessToken == "" {
		writeError(w, http.StatusBadRequest, "no access token available; re-authorize the connection")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	result, ferr := fetchCodexResetCredits(ctx, creds.AccessToken, creds.Extra)
	if ferr != nil {
		writeError(w, http.StatusBadGateway, ferr.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// adminCodexConsumeCredit consumes one Codex rate-limit reset credit. This is
// irreversible — it permanently spends 1 credit to reset the user's rate limit.
func (s *Server) adminCodexConsumeCredit(w http.ResponseWriter, r *http.Request) {
	acc, err := s.accounts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if acc.Provider != "codex" {
		writeError(w, http.StatusBadRequest, "only supported for codex accounts")
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusInternalServerError, "vault not configured")
		return
	}

	var body struct {
		// RedeemRequestID is an idempotency key generated by the caller. Older
		// dashboard builds sent a credit row's identifier here, so keep accepting
		// it while generating a fresh UUID when it is omitted.
		RedeemRequestID string `json:"redeem_request_id"`
		CreditID        string `json:"credit_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.RedeemRequestID == "" {
		body.RedeemRequestID = uuid.NewString()
	}

	creds, err := s.vault.Open(acc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not decrypt credentials")
		return
	}
	if creds.AccessToken == "" {
		writeError(w, http.StatusBadRequest, "no access token available; re-authorize the connection")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	result, cerr := consumeCodexResetCredit(ctx, creds.AccessToken, creds.Extra, body.RedeemRequestID, body.CreditID)
	if cerr != nil {
		writeError(w, http.StatusBadGateway, cerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ---- Codex reset credits helpers --------------------------------------------

const (
	codexResetCreditsURL        = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetCreditsConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	codexUsageURL               = "https://chatgpt.com/backend-api/wham/usage"
)

// codexAccountID extracts the ChatGPT account ID from provider metadata.
func codexAccountID(extra map[string]string) string {
	if extra == nil {
		return ""
	}
	for _, key := range []string{"workspaceId", "accountId", "chatgptAccountId"} {
		if v := extra[key]; v != "" {
			return v
		}
	}
	return ""
}

// codexCreditInfo represents a single reset credit with status and expiry.
type codexCreditInfo struct {
	ID              string `json:"id,omitempty"`
	RedeemRequestID string `json:"redeem_request_id,omitempty"`
	Status          string `json:"status"`
	GrantedAt       string `json:"granted_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
}

// codexUsageDetails represents Codex usage information including rate limits and reset credits.
type codexUsageDetails struct {
	UsageData    *codexUsageData        `json:"usage_data,omitempty"`
	ResetCredits *codexResetCreditsData `json:"reset_credits,omitempty"`
	Error        string                 `json:"error,omitempty"`
}

type codexUsageData struct {
	PlanType               string `json:"plan_type"`
	Allowed                bool   `json:"allowed"`
	LimitReached           bool   `json:"limit_reached"`
	PrimaryUsedPercent     int    `json:"primary_used_percent"`
	PrimaryResetAt         int64  `json:"primary_reset_at"`
	PrimaryWindowSeconds   int64  `json:"primary_window_seconds"`
	SecondaryUsedPercent   int    `json:"secondary_used_percent"`
	SecondaryResetAt       int64  `json:"secondary_reset_at"`
	SecondaryWindowSeconds int64  `json:"secondary_window_seconds"`
	CreditsBalance         string `json:"credits_balance"`
	HasCredits             bool   `json:"has_credits"`
	Unlimited              bool   `json:"unlimited"`
	ResetCreditsAvailable  int    `json:"reset_credits_available"`
}

type codexResetCreditsData struct {
	AvailableCount int               `json:"available_count"`
	Credits        []codexCreditInfo `json:"credits"`
}

// adminCodexUsageDetails fetches both usage data and reset credits for a Codex account.
func (s *Server) adminCodexUsageDetails(w http.ResponseWriter, r *http.Request) {
	acc, err := s.accounts.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if acc.Provider != "codex" {
		writeError(w, http.StatusBadRequest, "only supported for codex accounts")
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusInternalServerError, "vault not configured")
		return
	}

	creds, err := s.vault.Open(acc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not decrypt credentials")
		return
	}
	if creds.AccessToken == "" {
		writeError(w, http.StatusBadRequest, "no access token available; re-authorize the connection")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// Usage windows and reset credits come from independent upstream endpoints.
	// Fetch them together so expanding an account costs one round-trip instead
	// of waiting for two sequential network calls.
	var (
		usageData    *codexUsageData
		usageErr     error
		resetCredits codexResetCreditsData
		resetErr     error
		wg           sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		usageData, usageErr = fetchCodexUsage(ctx, creds.AccessToken, creds.Extra)
	}()
	go func() {
		defer wg.Done()
		resetCredits, resetErr = fetchCodexResetCredits(ctx, creds.AccessToken, creds.Extra)
	}()
	wg.Wait()

	result := codexUsageDetails{}

	if usageErr == nil && usageData != nil {
		result.UsageData = usageData
	} else {
		result.Error = "Failed to fetch usage data"
		if usageErr != nil {
			result.Error = usageErr.Error()
		}
	}

	if resetErr == nil {
		result.ResetCredits = &codexResetCreditsData{
			AvailableCount: resetCredits.AvailableCount,
			Credits:        resetCredits.Credits,
		}
	} else {
		if result.Error != "" {
			result.Error += "; Failed to fetch reset credits"
		} else {
			result.Error = "Failed to fetch reset credits"
		}
		if resetErr != nil {
			result.Error += ": " + resetErr.Error()
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// fetchCodexUsage retrieves Codex usage data from the ChatGPT backend API.
// The wham/usage endpoint returns rate limits as percentage-based windows
// (primary = 5h rolling, secondary = weekly), plus credits and reset credits.
func fetchCodexUsage(ctx context.Context, accessToken string, extra map[string]string) (*codexUsageData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex-cli-rs/0.1.0")
	if accountID := codexAccountID(extra); accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}

	if resp.StatusCode >= 400 {
		var errData struct {
			Message string `json:"message"`
			Error   string `json:"error"`
			Detail  string `json:"detail"`
		}
		_ = json.Unmarshal(bodyBytes, &errData)
		msg := errData.Message
		if msg == "" {
			msg = errData.Error
		}
		if msg == "" {
			msg = errData.Detail
		}
		if msg == "" {
			msg = fmt.Sprintf("Codex usage API unavailable (%d): %s", resp.StatusCode, string(bodyBytes))
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var raw struct {
		PlanType  string `json:"plan_type"`
		RateLimit struct {
			Allowed       bool `json:"allowed"`
			LimitReached  bool `json:"limit_reached"`
			PrimaryWindow struct {
				UsedPercent        int   `json:"used_percent"`
				LimitWindowSeconds int64 `json:"limit_window_seconds"`
				ResetAfterSeconds  int64 `json:"reset_after_seconds"`
				ResetAt            int64 `json:"reset_at"`
			} `json:"primary_window"`
			SecondaryWindow struct {
				UsedPercent        int   `json:"used_percent"`
				LimitWindowSeconds int64 `json:"limit_window_seconds"`
				ResetAfterSeconds  int64 `json:"reset_after_seconds"`
				ResetAt            int64 `json:"reset_at"`
			} `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits struct {
			HasCredits bool   `json:"has_credits"`
			Unlimited  bool   `json:"unlimited"`
			Balance    string `json:"balance"`
		} `json:"credits"`
		RateLimitResetCredits struct {
			AvailableCount int `json:"available_count"`
		} `json:"rate_limit_reset_credits"`
	}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &codexUsageData{
		PlanType:               raw.PlanType,
		Allowed:                raw.RateLimit.Allowed,
		LimitReached:           raw.RateLimit.LimitReached,
		PrimaryUsedPercent:     raw.RateLimit.PrimaryWindow.UsedPercent,
		PrimaryResetAt:         raw.RateLimit.PrimaryWindow.ResetAt,
		PrimaryWindowSeconds:   raw.RateLimit.PrimaryWindow.LimitWindowSeconds,
		SecondaryUsedPercent:   raw.RateLimit.SecondaryWindow.UsedPercent,
		SecondaryResetAt:       raw.RateLimit.SecondaryWindow.ResetAt,
		SecondaryWindowSeconds: raw.RateLimit.SecondaryWindow.LimitWindowSeconds,
		CreditsBalance:         raw.Credits.Balance,
		HasCredits:             raw.Credits.HasCredits,
		Unlimited:              raw.Credits.Unlimited,
		ResetCreditsAvailable:  raw.RateLimitResetCredits.AvailableCount,
	}, nil
}

// fetchCodexResetCredits retrieves available Codex rate-limit reset credits.
func fetchCodexResetCredits(ctx context.Context, accessToken string, extra map[string]string) (codexResetCreditsData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexResetCreditsURL, nil)
	if err != nil {
		return codexResetCreditsData{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("originator", "codex_cli_rs")
	if accountID := codexAccountID(extra); accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return codexResetCreditsData{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var data struct {
		AvailableCount  *int `json:"available_count"`
		AvailableCount2 *int `json:"availableCount"`
		Credits         []struct {
			ID               string `json:"id"`
			CreditID         string `json:"credit_id"`
			RedeemRequestID  string `json:"redeem_request_id"`
			RedeemRequestID2 string `json:"redeemRequestId"`
			Status           string `json:"status"`
			GrantedAt        any    `json:"granted_at"`
			GrantedAt2       any    `json:"grantedAt"`
			ExpiresAt        any    `json:"expires_at"`
			ExpiresAt2       any    `json:"expiresAt"`
			Title            string `json:"title"`
			Description      string `json:"description"`
		} `json:"credits"`
		Message string `json:"message"`
		Error   string `json:"error"`
		Detail  string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		if resp.StatusCode >= 400 {
			msg := data.Message
			if msg == "" {
				msg = data.Error
			}
			if msg == "" {
				msg = data.Detail
			}
			if msg == "" {
				msg = fmt.Sprintf("Codex reset credits API unavailable (%d)", resp.StatusCode)
			}
			return codexResetCreditsData{}, fmt.Errorf("%s", msg)
		}
		return codexResetCreditsData{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode >= 400 {
		msg := data.Message
		if msg == "" {
			msg = data.Error
		}
		if msg == "" {
			msg = data.Detail
		}
		if msg == "" {
			msg = fmt.Sprintf("Codex reset credits API unavailable (%d)", resp.StatusCode)
		}
		return codexResetCreditsData{}, fmt.Errorf("%s", msg)
	}

	available := 0
	if data.AvailableCount != nil {
		available = *data.AvailableCount
	} else if data.AvailableCount2 != nil {
		available = *data.AvailableCount2
	}

	credits := make([]codexCreditInfo, 0, len(data.Credits))
	for _, c := range data.Credits {
		info := codexCreditInfo{
			ID:              c.ID,
			RedeemRequestID: c.RedeemRequestID,
			Status:          defaultStr(c.Status, "unknown"),
			Title:           c.Title,
			Description:     c.Description,
		}
		if info.ID == "" {
			info.ID = c.CreditID
		}
		if info.RedeemRequestID == "" && c.RedeemRequestID2 != "" {
			info.RedeemRequestID = c.RedeemRequestID2
		}
		info.GrantedAt = codexTimeString(c.GrantedAt)
		if info.GrantedAt == "" {
			info.GrantedAt = codexTimeString(c.GrantedAt2)
		}
		info.ExpiresAt = codexTimeString(c.ExpiresAt)
		if info.ExpiresAt == "" {
			info.ExpiresAt = codexTimeString(c.ExpiresAt2)
		}
		credits = append(credits, info)
	}

	return codexResetCreditsData{
		AvailableCount: available,
		Credits:        credits,
	}, nil
}

// codexTimeString normalizes the reset-credit API's mixed timestamp formats.
// The upstream has returned both RFC3339 strings and Unix seconds/milliseconds.
func codexTimeString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		seconds := int64(v)
		if seconds > 10_000_000_000 {
			seconds /= 1000
		}
		if seconds > 0 {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
	case json.Number:
		if number, err := v.Int64(); err == nil {
			return codexTimeString(float64(number))
		}
	}
	return ""
}

// consumeCodexResetCredit consumes one Codex rate-limit reset credit.
func consumeCodexResetCredit(ctx context.Context, accessToken string, extra map[string]string, redeemRequestID, creditID string) (map[string]any, error) {
	req, err := newCodexResetRequest(ctx, accessToken, extra, redeemRequestID, creditID)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return parseCodexConsumeResponse(resp.StatusCode, body), nil
}

func newCodexResetRequest(ctx context.Context, accessToken string, extra map[string]string, redeemRequestID, creditID string) (*http.Request, error) {
	payloadData := map[string]string{
		"redeem_request_id": redeemRequestID,
	}
	if creditID != "" {
		payloadData["credit_id"] = creditID
	}
	payload, _ := json.Marshal(payloadData)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexResetCreditsConsumeURL,
		bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex-cli-rs/0.1.0")
	if accountID := codexAccountID(extra); accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	return req, nil
}

func parseCodexConsumeResponse(statusCode int, body []byte) map[string]any {
	var data struct {
		Code         string `json:"code"`
		Outcome      string `json:"outcome"`
		WindowsReset int    `json:"windows_reset"`
		Message      string `json:"message"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &data)
	}

	outcome := data.Outcome
	if outcome == "" {
		outcome = data.Code
	}
	ok := statusCode < 400 && (outcome == "reset" || outcome == "alreadyRedeemed" || outcome == "already_redeemed" || data.WindowsReset > 0)
	noCredit := statusCode < 400 && (outcome == "noCredit" || outcome == "no_credit")
	if data.Message == "" {
		switch outcome {
		case "nothingToReset", "nothing_to_reset":
			data.Message = "No eligible rate-limit window needs resetting."
		case "noCredit", "no_credit":
			data.Message = "No reset credits are available."
		}
	}

	return map[string]any{
		"ok":            ok,
		"no_credit":     noCredit,
		"status":        statusCode,
		"code":          data.Code,
		"outcome":       outcome,
		"windows_reset": data.WindowsReset,
		"message":       data.Message,
	}
}

// ---- chains -----------------------------------------------------------------

func (s *Server) adminListChains(w http.ResponseWriter, r *http.Request) {
	chains, err := s.chains.ListByTenant(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(chains))
	for _, c := range chains {
		steps := make([]map[string]any, 0, len(c.Steps))
		for _, st := range c.Steps {
			steps = append(steps, map[string]any{"provider": st.Provider, "model": st.Model, "position": st.Position})
		}
		entry := map[string]any{
			"id": c.ID, "name": c.Name, "strategy": c.Strategy, "steps": steps,
		}
		if c.FallbackProvider != "" && c.FallbackModel != "" {
			entry["fallback_provider"] = c.FallbackProvider
			entry["fallback_model"] = c.FallbackModel
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chains": out})
}

func (s *Server) adminCreateChain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name             string `json:"name"`
		Strategy         string `json:"strategy"`
		FallbackProvider string `json:"fallback_provider"`
		FallbackModel    string `json:"fallback_model"`
		Steps            []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"steps"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" || len(body.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "name and at least one step are required")
		return
	}
	if err := validateChainName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate fallback provider if set.
	if body.FallbackProvider != "" {
		if _, ok := connectors.SpecByID(body.FallbackProvider); !ok {
			writeError(w, http.StatusBadRequest, "unknown fallback provider: "+body.FallbackProvider)
			return
		}
		if body.FallbackModel == "" {
			writeError(w, http.StatusBadRequest, "fallback_model is required when fallback_provider is set")
			return
		}
	}

	now := time.Now()
	chain := store.Chain{
		ID:               uuid.NewString(),
		TenantID:         adminTenant,
		Name:             body.Name,
		Strategy:         defaultStr(body.Strategy, "priority"),
		FallbackProvider: body.FallbackProvider,
		FallbackModel:    body.FallbackModel,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	for i, st := range body.Steps {
		if _, ok := connectors.SpecByID(st.Provider); !ok {
			writeError(w, http.StatusBadRequest, "unknown provider in step: "+st.Provider)
			return
		}
		chain.Steps = append(chain.Steps, store.ChainStep{
			ID: uuid.NewString(), ChainID: chain.ID, Position: i,
			Provider: st.Provider, Model: st.Model, CreatedAt: now,
		})
	}
	if err := s.chains.Create(r.Context(), chain); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": chain.ID, "name": chain.Name})
}

func (s *Server) adminDeleteChain(w http.ResponseWriter, r *http.Request) {
	if err := s.chains.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminUpdateChain(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.chains.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "chain not found")
		return
	}

	var body struct {
		Name             *string `json:"name"`
		Strategy         *string `json:"strategy"`
		FallbackProvider *string `json:"fallback_provider"`
		FallbackModel    *string `json:"fallback_model"`
		Steps            *[]struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"steps"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Name != nil {
		if err := validateChainName(*body.Name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.Name = *body.Name
	}
	if body.Strategy != nil {
		existing.Strategy = *body.Strategy
	}
	if body.FallbackProvider != nil {
		existing.FallbackProvider = *body.FallbackProvider
	}
	if body.FallbackModel != nil {
		existing.FallbackModel = *body.FallbackModel
	}
	if body.Steps != nil {
		now := time.Now()
		existing.Steps = make([]store.ChainStep, len(*body.Steps))
		for i, st := range *body.Steps {
			existing.Steps[i] = store.ChainStep{
				ID:        uuid.NewString(),
				ChainID:   id,
				Position:  i,
				Provider:  st.Provider,
				Model:     st.Model,
				CreatedAt: now,
			}
		}
	}

	if err := s.chains.Update(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "name": existing.Name})
}

// ---- plans ------------------------------------------------------------------

func (s *Server) adminListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.db.Plans().List(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(plans))
	for _, p := range plans {
		keyCount, _ := s.db.Plans().CountKeys(r.Context(), p.ID)
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"limit_micros": p.LimitMicros, "limit_tokens": p.LimitTokens,
			"rpm_limit": p.RPMLimit, "tpm_limit": p.TPMLimit, "concurrency_limit": p.ConcurrencyLimit,
			"period": p.Period, "alert_pct": p.AlertPct, "hard_cutoff": p.HardCutoff,
			"allowed_models": store.GetPlanAllowedModels(p),
			"key_count":      keyCount,
			"created_at":     p.CreatedAt, "updated_at": p.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": out})
}

func (s *Server) adminCreatePlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		LimitUSD         float64  `json:"limit_usd"`
		LimitTokens      int64    `json:"limit_tokens"`
		RPMLimit         int64    `json:"rpm_limit"`
		TPMLimit         int64    `json:"tpm_limit"`
		ConcurrencyLimit int64    `json:"concurrency_limit"`
		Period           string   `json:"period"`
		AlertPct         int      `json:"alert_pct"`
		HardCutoff       *bool    `json:"hard_cutoff"`
		AllowedModels    []string `json:"allowed_models"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.LimitUSD < 0 {
		writeError(w, http.StatusBadRequest, "limit_usd must not be negative")
		return
	}
	if body.LimitTokens < 0 {
		writeError(w, http.StatusBadRequest, "limit_tokens must not be negative")
		return
	}
	if body.RPMLimit < 0 || body.TPMLimit < 0 || body.ConcurrencyLimit < 0 {
		writeError(w, http.StatusBadRequest, "rate limits must not be negative")
		return
	}
	period, ok := normalizeBudgetPeriod(body.Period)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid period")
		return
	}
	alertPct := defaultInt(body.AlertPct, 80)
	if alertPct < 1 || alertPct > 100 {
		writeError(w, http.StatusBadRequest, "alert_pct must be between 1 and 100")
		return
	}
	hardCutoff := true
	if body.HardCutoff != nil {
		hardCutoff = *body.HardCutoff
	}

	now := time.Now()
	p := store.Plan{
		ID:               uuid.NewString(),
		TenantID:         adminTenant,
		Name:             body.Name,
		Description:      body.Description,
		LimitMicros:      int64(body.LimitUSD * 1_000_000),
		LimitTokens:      body.LimitTokens,
		RPMLimit:         body.RPMLimit,
		TPMLimit:         body.TPMLimit,
		ConcurrencyLimit: body.ConcurrencyLimit,
		Period:           period,
		AlertPct:         alertPct,
		HardCutoff:       hardCutoff,
		AllowedModels:    store.SetPlanAllowedModels(body.AllowedModels),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.db.Plans().Create(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"limit_micros": p.LimitMicros, "limit_tokens": p.LimitTokens,
		"rpm_limit": p.RPMLimit, "tpm_limit": p.TPMLimit, "concurrency_limit": p.ConcurrencyLimit,
		"period": p.Period, "alert_pct": p.AlertPct, "hard_cutoff": p.HardCutoff,
		"allowed_models": store.GetPlanAllowedModels(p),
	})
}

func (s *Server) adminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.db.Plans().Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var body struct {
		Name             *string  `json:"name"`
		Description      *string  `json:"description"`
		LimitUSD         *float64 `json:"limit_usd"`
		LimitTokens      *int64   `json:"limit_tokens"`
		RPMLimit         *int64   `json:"rpm_limit"`
		TPMLimit         *int64   `json:"tpm_limit"`
		ConcurrencyLimit *int64   `json:"concurrency_limit"`
		Period           *string  `json:"period"`
		AlertPct         *int     `json:"alert_pct"`
		HardCutoff       *bool    `json:"hard_cutoff"`
		AllowedModels    []string `json:"allowed_models"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Name != nil {
		if *body.Name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		existing.Name = *body.Name
	}
	if body.Description != nil {
		existing.Description = *body.Description
	}
	if body.LimitUSD != nil {
		if *body.LimitUSD < 0 {
			writeError(w, http.StatusBadRequest, "limit_usd must not be negative")
			return
		}
		existing.LimitMicros = int64(*body.LimitUSD * 1_000_000)
	}
	if body.LimitTokens != nil {
		if *body.LimitTokens < 0 {
			writeError(w, http.StatusBadRequest, "limit_tokens must not be negative")
			return
		}
		existing.LimitTokens = *body.LimitTokens
	}
	if body.RPMLimit != nil {
		if *body.RPMLimit < 0 {
			writeError(w, http.StatusBadRequest, "rpm_limit must not be negative")
			return
		}
		existing.RPMLimit = *body.RPMLimit
	}
	if body.TPMLimit != nil {
		if *body.TPMLimit < 0 {
			writeError(w, http.StatusBadRequest, "tpm_limit must not be negative")
			return
		}
		existing.TPMLimit = *body.TPMLimit
	}
	if body.ConcurrencyLimit != nil {
		if *body.ConcurrencyLimit < 0 {
			writeError(w, http.StatusBadRequest, "concurrency_limit must not be negative")
			return
		}
		existing.ConcurrencyLimit = *body.ConcurrencyLimit
	}
	if body.Period != nil {
		period, ok := normalizeBudgetPeriod(*body.Period)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid period")
			return
		}
		existing.Period = period
	}
	if body.AlertPct != nil {
		if *body.AlertPct < 1 || *body.AlertPct > 100 {
			writeError(w, http.StatusBadRequest, "alert_pct must be between 1 and 100")
			return
		}
		existing.AlertPct = *body.AlertPct
	}
	if body.HardCutoff != nil {
		existing.HardCutoff = *body.HardCutoff
	}
	if body.AllowedModels != nil {
		existing.AllowedModels = store.SetPlanAllowedModels(body.AllowedModels)
	}
	existing.UpdatedAt = time.Now()

	if err := s.db.Plans().Update(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": existing.ID, "name": existing.Name, "description": existing.Description,
		"limit_micros": existing.LimitMicros, "limit_tokens": existing.LimitTokens,
		"rpm_limit": existing.RPMLimit, "tpm_limit": existing.TPMLimit, "concurrency_limit": existing.ConcurrencyLimit,
		"period": existing.Period, "alert_pct": existing.AlertPct, "hard_cutoff": existing.HardCutoff,
		"allowed_models": store.GetPlanAllowedModels(existing),
	})
}

func (s *Server) adminDeletePlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	keyCount, err := s.db.Plans().CountKeys(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if keyCount > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("plan has %d API key(s) assigned — reassign or delete them first", keyCount))
		return
	}
	if err := s.db.Plans().Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminListPlanKeys(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "id")
	if _, err := s.db.Plans().Get(r.Context(), planID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	keys, err := s.identity.List(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	var out []map[string]any
	for _, k := range keys {
		if k.PlanID == planID {
			entry := map[string]any{
				"id": k.ID, "name": k.Name, "display": k.Display,
				"disabled": k.Disabled, "created_at": k.CreatedAt,
			}
			if models, merr := s.identity.Keys().GetAllowedModels(r.Context(), k.ID); merr == nil {
				entry["allowed_models"] = models
			}
			out = append(out, entry)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// ---- budgets ----------------------------------------------------------------

func (s *Server) adminListBudgets(w http.ResponseWriter, r *http.Request) {
	budgets, err := s.budgets.ListByTenant(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(budgets))
	for _, b := range budgets {
		out = append(out, map[string]any{
			"id": b.ID, "scope_kind": b.ScopeKind, "scope_id": b.ScopeID,
			"limit_micros": b.LimitMicros, "limit_tokens": b.LimitTokens,
			"period": b.Period, "alert_pct": b.AlertPct, "hard_cutoff": b.HardCutoff,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": out})
}

func (s *Server) adminCreateBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ScopeKind   string  `json:"scope_kind"`
		ScopeID     string  `json:"scope_id"`
		LimitUSD    float64 `json:"limit_usd"`
		LimitTokens int64   `json:"limit_tokens"`
		Period      string  `json:"period"`
		AlertPct    int     `json:"alert_pct"`
		HardCutoff  *bool   `json:"hard_cutoff"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.LimitUSD <= 0 && body.LimitTokens <= 0 {
		writeError(w, http.StatusBadRequest, "limit_usd or limit_tokens must be positive")
		return
	}
	if body.LimitUSD < 0 {
		writeError(w, http.StatusBadRequest, "limit_usd must not be negative")
		return
	}
	if body.LimitTokens < 0 {
		writeError(w, http.StatusBadRequest, "limit_tokens must not be negative")
		return
	}
	period, ok := normalizeBudgetPeriod(body.Period)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid budget period")
		return
	}
	alertPct := defaultInt(body.AlertPct, 80)
	if alertPct < 1 || alertPct > 100 {
		writeError(w, http.StatusBadRequest, "alert_pct must be between 1 and 100")
		return
	}
	scopeKind := store.BudgetScope(defaultStr(body.ScopeKind, string(store.ScopeTenant)))
	scopeID := strings.TrimSpace(body.ScopeID)
	switch scopeKind {
	case store.ScopeTenant:
		scopeID = defaultStr(scopeID, adminTenant)
	case store.ScopeAPIKey:
		if scopeID == "" {
			writeError(w, http.StatusBadRequest, "scope_id is required for api_key budgets")
			return
		}
		if _, err := s.identity.Get(r.Context(), scopeID); err != nil {
			writeError(w, http.StatusBadRequest, "api key not found")
			return
		}
	case store.ScopeProject:
		if scopeID == "" {
			writeError(w, http.StatusBadRequest, "scope_id is required for project budgets")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid budget scope")
		return
	}
	hardCutoff := true
	if body.HardCutoff != nil {
		hardCutoff = *body.HardCutoff
	}

	now := time.Now()
	b := store.Budget{
		ID:          uuid.NewString(),
		TenantID:    adminTenant,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		LimitMicros: int64(body.LimitUSD * 1_000_000),
		LimitTokens: body.LimitTokens,
		Period:      period,
		AlertPct:    alertPct,
		HardCutoff:  hardCutoff,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.budgets.Create(r.Context(), b); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": b.ID})
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCache()
	}
}

func (s *Server) adminDeleteBudget(w http.ResponseWriter, r *http.Request) {
	if err := s.budgets.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCache()
	}
}

func (s *Server) adminUpdateBudget(w http.ResponseWriter, r *http.Request) {
	existing, err := s.budgets.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "budget not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var body struct {
		LimitUSD    *float64 `json:"limit_usd"`
		LimitTokens *int64   `json:"limit_tokens"`
		Period      *string  `json:"period"`
		AlertPct    *int     `json:"alert_pct"`
		HardCutoff  *bool    `json:"hard_cutoff"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.LimitUSD != nil {
		if *body.LimitUSD < 0 {
			writeError(w, http.StatusBadRequest, "limit_usd must not be negative")
			return
		}
		existing.LimitMicros = int64(*body.LimitUSD * 1_000_000)
	}
	if body.LimitTokens != nil {
		if *body.LimitTokens < 0 {
			writeError(w, http.StatusBadRequest, "limit_tokens must not be negative")
			return
		}
		existing.LimitTokens = *body.LimitTokens
	}
	if body.Period != nil {
		period, ok := normalizeBudgetPeriod(*body.Period)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid budget period")
			return
		}
		existing.Period = period
	}
	if body.AlertPct != nil {
		if *body.AlertPct < 1 || *body.AlertPct > 100 {
			writeError(w, http.StatusBadRequest, "alert_pct must be between 1 and 100")
			return
		}
		existing.AlertPct = *body.AlertPct
	}
	if body.HardCutoff != nil {
		existing.HardCutoff = *body.HardCutoff
	}
	if existing.LimitMicros <= 0 && existing.LimitTokens <= 0 {
		writeError(w, http.StatusBadRequest, "limit_usd or limit_tokens must be positive")
		return
	}
	existing.UpdatedAt = time.Now()

	if err := s.budgets.Update(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCache()
	}
}

func normalizeBudgetPeriod(period string) (string, bool) {
	period = defaultStr(period, "monthly")
	switch period {
	case "daily", "weekly", "monthly", "total":
		return period, true
	default:
		return "", false
	}
}

// adminBudgetStatus returns all budgets enriched with current-period spend data.
func (s *Server) adminBudgetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	budgets, err := s.budgets.ListByTenant(ctx, adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	now := time.Now()
	scopes := make([]store.SpendScope, 0, len(budgets))
	sinceByBudget := make([]time.Time, len(budgets))
	for i, b := range budgets {
		since := budget.PeriodStart(b.Period, now)
		sinceByBudget[i] = since
		scopes = append(scopes, store.SpendScope{Kind: b.ScopeKind, ScopeID: b.ScopeID, Since: since})
	}
	spendResults, err := s.usage.SpendAndTokensBatch(ctx, scopes)
	if err != nil {
		s.log.Error("budget status: batch spend lookup failed", "err", err)
		spendResults = make([]store.SpendResult, len(budgets))
	}

	out := make([]map[string]any, 0, len(budgets))
	for i, b := range budgets {
		since := sinceByBudget[i]
		var spent, tokens int64
		if i < len(spendResults) {
			spent = spendResults[i].CostMicros
			tokens = spendResults[i].Tokens
		}

		pctUsed := 0.0
		if b.LimitMicros > 0 {
			pctUsed = float64(spent) / float64(b.LimitMicros) * 100
		}
		tokPctUsed := 0.0
		if b.LimitTokens > 0 {
			tokPctUsed = float64(tokens) / float64(b.LimitTokens) * 100
		}

		// Resolve scope display name.
		scopeName := string(b.ScopeKind)
		if b.ScopeKind == store.ScopeAPIKey {
			if key, kerr := s.identity.Get(ctx, b.ScopeID); kerr == nil && key.Name != "" {
				scopeName = key.Name
			}
		}

		out = append(out, map[string]any{
			"id":              b.ID,
			"scope_kind":      b.ScopeKind,
			"scope_id":        b.ScopeID,
			"scope_name":      scopeName,
			"limit_micros":    b.LimitMicros,
			"limit_tokens":    b.LimitTokens,
			"period":          b.Period,
			"alert_pct":       b.AlertPct,
			"hard_cutoff":     b.HardCutoff,
			"spent_micros":    spent,
			"spent_tokens":    tokens,
			"pct_used":        pctUsed,
			"tokens_pct_used": tokPctUsed,
			"period_start":    since,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": out})
}

// ---- usage ------------------------------------------------------------------

func (s *Server) adminUsageSummary(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	since := time.Now().AddDate(0, 0, -30)
	switch period {
	case "today":
		now := time.Now()
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "week":
		since = time.Now().AddDate(0, 0, -7)
	case "month", "":
		since = time.Now().AddDate(0, -1, 0)
	}

	sum, err := s.usage.Summarize(r.Context(), adminTenant, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_requests":     sum.TotalRequests,
		"prompt_tokens":      sum.PromptTokens,
		"completion_tokens":  sum.CompletionTokens,
		"cached_tokens":      sum.CachedTokens,
		"cache_write_tokens": sum.CacheWriteTokens,
		"cost_usd":           float64(sum.CostMicros) / 1_000_000,
		"cache_hits":         sum.CacheHits,
		"since":              since,
	})
}

// ---- model aliases ----------------------------------------------------------

func (s *Server) adminListAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := s.aliases.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make(map[string]string, len(aliases))
	for _, a := range aliases {
		out[a.Alias] = a.Target
	}
	writeJSON(w, http.StatusOK, map[string]any{"aliases": out})
}

func (s *Server) adminSetAlias(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias  string `json:"alias"`
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Alias == "" || body.Target == "" {
		writeError(w, http.StatusBadRequest, "alias and target are required")
		return
	}
	if !strings.Contains(body.Target, "/") {
		writeError(w, http.StatusBadRequest, "target must be in 'provider/model' format")
		return
	}
	if err := s.aliases.Set(r.Context(), body.Alias, body.Target); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) adminDeleteAlias(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		writeError(w, http.StatusBadRequest, "alias query param is required")
		return
	}
	if err := s.aliases.Delete(r.Context(), alias); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- disabled models --------------------------------------------------------

const disabledModelsPrefix = "disabled_models_" // + provider alias

func (s *Server) loadDisabledModels(ctx context.Context, provider string) []string {
	if s.settings == nil {
		return nil
	}
	raw, err := s.settings.Get(ctx, disabledModelsPrefix+provider)
	if err != nil || raw == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return ids
}

func (s *Server) saveDisabledModels(ctx context.Context, provider string, ids []string) error {
	if s.settings == nil {
		return nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, disabledModelsPrefix+provider, string(raw))
}

func (s *Server) adminListDisabledModels(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider query param is required")
		return
	}
	ids := s.loadDisabledModels(r.Context(), provider)
	writeJSON(w, http.StatusOK, map[string]any{"ids": ids})
}

func (s *Server) adminDisableModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string   `json:"providerAlias"`
		IDs      []string `json:"ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Provider == "" {
		writeError(w, http.StatusBadRequest, "providerAlias is required")
		return
	}
	existing := s.loadDisabledModels(r.Context(), body.Provider)
	seen := map[string]bool{}
	for _, id := range existing {
		seen[id] = true
	}
	for _, id := range body.IDs {
		seen[id] = true
	}
	merged := make([]string, 0, len(seen))
	for id := range seen {
		merged = append(merged, id)
	}
	if err := s.saveDisabledModels(r.Context(), body.Provider, merged); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ids": merged})
}

func (s *Server) adminEnableModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string   `json:"providerAlias"`
		IDs      []string `json:"ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Provider == "" {
		writeError(w, http.StatusBadRequest, "providerAlias is required")
		return
	}
	existing := s.loadDisabledModels(r.Context(), body.Provider)
	remove := map[string]bool{}
	for _, id := range body.IDs {
		remove[id] = true
	}
	var kept []string
	for _, id := range existing {
		if !remove[id] {
			kept = append(kept, id)
		}
	}
	if err := s.saveDisabledModels(r.Context(), body.Provider, kept); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ids": kept})
}

// ---- console SSE stream -----------------------------------------------------

func (s *Server) adminConsoleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial history.
	entries := s.consoleLog.Entries()
	initData, _ := json.Marshal(map[string]any{"type": "init", "logs": entries})
	fmt.Fprintf(w, "data: %s\n\n", initData)
	flusher.Flush()

	// Subscribe to new log lines via buffered channel.
	listener := consolelog.NewListener(256)
	s.consoleLog.Subscribe(listener)
	defer s.consoleLog.Unsubscribe(listener)

	// Keepalive ping every 25s.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-listener.C:
			var data []byte
			if ev.Clear {
				data, _ = json.Marshal(map[string]any{"type": "clear"})
			} else {
				data, _ = json.Marshal(map[string]any{"type": "line", "log": ev.Entry})
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// adminConsoleClear clears the log buffer.
func (s *Server) adminConsoleClear(w http.ResponseWriter, r *http.Request) {
	s.consoleLog.Clear()
	w.WriteHeader(http.StatusNoContent)
}

// ---- database export/import -------------------------------------------------

func (s *Server) adminExportDatabase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	export := map[string]any{}

	// Optional passphrase enables a portable backup: each sealed credential is
	// re-keyed from the local master key to a passphrase-derived key, so the
	// backup can be restored on a machine with a different master key.
	passphrase := strings.TrimSpace(r.URL.Query().Get("passphrase"))
	portable := passphrase != ""
	export["portable"] = portable

	// Export providers (accounts) — includes encrypted credentials.
	accs, _ := s.accounts.ListByTenant(ctx, adminTenant)
	accountsOut := make([]map[string]any, 0, len(accs))
	for _, a := range accs {
		out := map[string]any{
			"id": a.ID, "provider": a.Provider, "label": a.Label,
			"auth_kind": a.AuthKind, "priority": a.Priority,
			"disabled": a.Disabled, "proxy_pool_id": a.ProxyPoolID,
			"metadata": a.Metadata,
		}
		if portable {
			if err := s.exportPortableSecrets(out, a, passphrase); err != nil {
				s.consoleLog.Log("ERROR", fmt.Sprintf("Portable export failed for account %s", a.ID), err.Error())
				writeError(w, http.StatusInternalServerError, "portable export failed: cannot re-key account "+a.ID+" (master key mismatch?)")
				return
			}
		} else {
			if a.SecretWrappedDEK != "" {
				out["secret_wrapped_dek"] = a.SecretWrappedDEK
				out["secret_ciphertext"] = a.SecretCiphertext
			}
			if a.TokenWrappedDEK != "" {
				out["token_wrapped_dek"] = a.TokenWrappedDEK
				out["token_ciphertext"] = a.TokenCiphertext
			}
			if a.RefreshWrappedDEK != "" {
				out["refresh_wrapped_dek"] = a.RefreshWrappedDEK
				out["refresh_ciphertext"] = a.RefreshCiphertext
			}
		}
		if a.TokenExpiresAt != nil {
			out["token_expires_at"] = a.TokenExpiresAt
		}
		accountsOut = append(accountsOut, out)
	}
	export["accounts"] = accountsOut

	// Export chains.
	chains, _ := s.chains.ListByTenant(ctx, adminTenant)
	chainsOut := make([]map[string]any, 0, len(chains))
	for _, c := range chains {
		steps := make([]map[string]any, 0, len(c.Steps))
		for _, st := range c.Steps {
			steps = append(steps, map[string]any{
				"provider": st.Provider, "model": st.Model, "position": st.Position,
			})
		}
		chainsOut = append(chainsOut, map[string]any{
			"name": c.Name, "strategy": c.Strategy, "steps": steps,
		})
	}
	export["chains"] = chainsOut

	// Export API keys (names only, not hashes).
	keys, _ := s.identity.List(ctx, adminTenant)
	keysOut := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		keysOut = append(keysOut, map[string]any{
			"name": k.Name, "disabled": k.Disabled,
		})
	}
	export["keys"] = keysOut

	// Export budgets.
	budgets, _ := s.budgets.ListByTenant(ctx, adminTenant)
	budgetsOut := make([]map[string]any, 0, len(budgets))
	for _, b := range budgets {
		budgetsOut = append(budgetsOut, map[string]any{
			"scope_kind": b.ScopeKind, "scope_id": b.ScopeID,
			"limit_micros": b.LimitMicros, "period": b.Period,
			"alert_pct": b.AlertPct, "hard_cutoff": b.HardCutoff,
		})
	}
	export["budgets"] = budgetsOut

	// Export proxy pools.
	pools, _ := s.pools.List(ctx)
	poolsOut := make([]map[string]any, 0, len(pools))
	for _, p := range pools {
		poolsOut = append(poolsOut, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"proxy_url": p.ProxyURL, "no_proxy": p.NoProxy,
			"strict": p.Strict, "is_active": p.IsActive,
		})
	}
	export["proxy_pools"] = poolsOut

	// Export settings.
	export["endpoint_settings"] = s.loadEndpointSettings(ctx)
	export["access_settings"] = s.loadAccessSettings(ctx)

	// Export aliases.
	aliases, _ := s.aliases.List(ctx)
	aliasMap := map[string]string{}
	for _, a := range aliases {
		aliasMap[a.Alias] = a.Target
	}
	export["aliases"] = aliasMap

	writeJSON(w, http.StatusOK, export)
}

func (s *Server) adminImportDatabase(w http.ResponseWriter, r *http.Request) {
	var payload map[string]json.RawMessage
	if !decodeJSON(w, r, &payload) {
		return
	}
	ctx := r.Context()
	imported := 0

	// A portable backup carries passphrase-encrypted secrets; the passphrase is
	// supplied alongside the payload so we can re-key into the local master key.
	portable := false
	if raw, ok := payload["portable"]; ok {
		_ = json.Unmarshal(raw, &portable)
	}
	passphrase := ""
	if raw, ok := payload["passphrase"]; ok {
		_ = json.Unmarshal(raw, &passphrase)
	}
	passphrase = strings.TrimSpace(passphrase)
	if portable && passphrase == "" {
		writeError(w, http.StatusBadRequest, "this backup is portable: a passphrase is required to import it")
		return
	}

	// Import providers (accounts) — preserves encrypted credentials.
	if raw, ok := payload["accounts"]; ok {
		var accounts []struct {
			ID                string                `json:"id"`
			Provider          string                `json:"provider"`
			Label             string                `json:"label"`
			AuthKind          string                `json:"auth_kind"`
			Priority          int                   `json:"priority"`
			Disabled          bool                  `json:"disabled"`
			ProxyPoolID       string                `json:"proxy_pool_id"`
			Metadata          string                `json:"metadata"`
			SecretWrappedDEK  string                `json:"secret_wrapped_dek"`
			SecretCiphertext  string                `json:"secret_ciphertext"`
			TokenWrappedDEK   string                `json:"token_wrapped_dek"`
			TokenCiphertext   string                `json:"token_ciphertext"`
			RefreshWrappedDEK string                `json:"refresh_wrapped_dek"`
			RefreshCiphertext string                `json:"refresh_ciphertext"`
			PortableSecret    portableAccountSecret `json:"portable_secret"`
			TokenExpiresAt    *string               `json:"token_expires_at"`
		}
		if err := json.Unmarshal(raw, &accounts); err == nil {
			for _, a := range accounts {
				now := time.Now()
				var expiresAt *time.Time
				if a.TokenExpiresAt != nil {
					if t, err := time.Parse(time.RFC3339, *a.TokenExpiresAt); err == nil {
						expiresAt = &t
					}
				}
				acc := store.Account{
					ID:                defaultStr(a.ID, uuid.NewString()),
					TenantID:          adminTenant,
					Provider:          a.Provider,
					Label:             a.Label,
					AuthKind:          store.AuthKind(defaultStr(a.AuthKind, "api_key")),
					SecretWrappedDEK:  a.SecretWrappedDEK,
					SecretCiphertext:  a.SecretCiphertext,
					TokenWrappedDEK:   a.TokenWrappedDEK,
					TokenCiphertext:   a.TokenCiphertext,
					RefreshWrappedDEK: a.RefreshWrappedDEK,
					RefreshCiphertext: a.RefreshCiphertext,
					TokenExpiresAt:    expiresAt,
					Metadata:          a.Metadata,
					Priority:          defaultInt(a.Priority, 100),
					Disabled:          a.Disabled,
					ProxyPoolID:       a.ProxyPoolID,
					CreatedAt:         now,
					UpdatedAt:         now,
				}
				if portable {
					if err := s.importPortableSecrets(&acc, a.PortableSecret, passphrase); err != nil {
						s.consoleLog.Log("ERROR", fmt.Sprintf("Portable import failed for account %s", acc.ID), err.Error())
						writeError(w, http.StatusBadRequest, "portable import failed: wrong passphrase or corrupt backup")
						return
					}
				}
				if err := s.accounts.Create(ctx, acc); err == nil {
					imported++
				}
			}
		}
	}

	// Import chains.
	if raw, ok := payload["chains"]; ok {
		var chains []struct {
			Name     string `json:"name"`
			Strategy string `json:"strategy"`
			Steps    []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Position int    `json:"position"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(raw, &chains); err == nil {
			for _, c := range chains {
				now := time.Now()
				chain := store.Chain{
					ID:        uuid.NewString(),
					TenantID:  adminTenant,
					Name:      c.Name,
					Strategy:  defaultStr(c.Strategy, "priority"),
					CreatedAt: now,
					UpdatedAt: now,
				}
				for _, st := range c.Steps {
					chain.Steps = append(chain.Steps, store.ChainStep{
						ID: uuid.NewString(), ChainID: chain.ID, Position: st.Position,
						Provider: st.Provider, Model: st.Model, CreatedAt: now,
					})
				}
				if err := s.chains.Create(ctx, chain); err == nil {
					imported++
				}
			}
		}
	}

	// Import budgets.
	if raw, ok := payload["budgets"]; ok {
		var budgets []struct {
			ScopeKind   string `json:"scope_kind"`
			ScopeID     string `json:"scope_id"`
			LimitMicros int64  `json:"limit_micros"`
			Period      string `json:"period"`
			AlertPct    int    `json:"alert_pct"`
			HardCutoff  bool   `json:"hard_cutoff"`
		}
		if err := json.Unmarshal(raw, &budgets); err == nil {
			for _, b := range budgets {
				now := time.Now()
				budget := store.Budget{
					ID:          uuid.NewString(),
					TenantID:    adminTenant,
					ScopeKind:   store.BudgetScope(defaultStr(b.ScopeKind, string(store.ScopeTenant))),
					ScopeID:     defaultStr(b.ScopeID, adminTenant),
					LimitMicros: b.LimitMicros,
					Period:      defaultStr(b.Period, "monthly"),
					AlertPct:    defaultInt(b.AlertPct, 80),
					HardCutoff:  b.HardCutoff,
					CreatedAt:   now,
					UpdatedAt:   now,
				}
				if err := s.budgets.Create(ctx, budget); err == nil {
					imported++
				}
			}
		}
		if s.budgetEngine != nil {
			s.budgetEngine.InvalidateBudgetCache()
		}
	}

	// Import proxy pools.
	if raw, ok := payload["proxy_pools"]; ok {
		var pools []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			ProxyURL string `json:"proxy_url"`
			NoProxy  string `json:"no_proxy"`
			Strict   bool   `json:"strict"`
			IsActive *bool  `json:"is_active"`
		}
		if err := json.Unmarshal(raw, &pools); err == nil {
			for _, p := range pools {
				now := time.Now()
				pool := store.ProxyPool{
					ID:         defaultStr(p.ID, uuid.NewString()),
					Name:       p.Name,
					Type:       defaultStr(p.Type, "http"),
					ProxyURL:   p.ProxyURL,
					NoProxy:    p.NoProxy,
					Strict:     p.Strict,
					IsActive:   p.IsActive == nil || *p.IsActive,
					TestStatus: "unknown",
					CreatedAt:  now,
					UpdatedAt:  now,
				}
				if err := s.pools.Create(ctx, pool); err == nil {
					imported++
				}
			}
		}
	}

	// Import endpoint settings.
	if raw, ok := payload["endpoint_settings"]; ok {
		if err := s.settings.Set(ctx, endpointSettingsKey, string(raw)); err == nil {
			imported++
		}
	}

	// Import aliases.
	if raw, ok := payload["aliases"]; ok {
		var aliases map[string]string
		if err := json.Unmarshal(raw, &aliases); err == nil {
			for alias, target := range aliases {
				_ = s.aliases.Set(ctx, alias, target)
				imported++
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"imported": imported})
}

// ---- proxy test -------------------------------------------------------------

func (s *Server) adminTestProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProxyURL string `json:"proxyUrl"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ProxyURL == "" {
		writeError(w, http.StatusBadRequest, "proxyUrl is required")
		return
	}

	// Validate proxy URL syntax only — proxy URLs are admin-configured trusted
	// infrastructure, so SSRF restrictions (which guard outbound target URLs)
	// do not apply here. Localhost proxies (Clash, V2Ray, etc.) are expected.
	parsed, err := url.Parse(body.ProxyURL)
	if err != nil || parsed.Host == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "invalid proxy URL: " + err.Error()})
		return
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "unsupported proxy scheme: " + parsed.Scheme})
		return
	}

	start := time.Now()
	transport := &http.Transport{Proxy: http.ProxyURL(parsed)}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), "GET", "https://httpbin.org/ip", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	result := map[string]any{
		"ok":        resp.StatusCode < 400,
		"status":    resp.StatusCode,
		"elapsedMs": elapsed.Milliseconds(),
	}

	// Parse exit IP from httpbin.org/ip response body.
	if resp.StatusCode < 400 {
		var ipInfo struct {
			Origin string `json:"origin"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&ipInfo); err == nil && ipInfo.Origin != "" {
			result["exitIP"] = ipInfo.Origin
		}
	} else {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if len(errBody) > 0 {
			result["error"] = string(errBody)
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// ---- helpers ----------------------------------------------------------------

type providerMetadataInput struct {
	BaseURL           string
	Region            string
	AccountID         string
	AzureEndpoint     string
	AzureDeployment   string
	AzureAPIVersion   string
	AzureOrganization string
}

func accountAuthKind(spec connectors.ProviderSpec, apiKey string) store.AuthKind {
	if strings.TrimSpace(apiKey) == "" && spec.AuthKind == "none" {
		return store.AuthNone
	}
	return store.AuthAPIKey
}

func providerAccountMetadata(spec connectors.ProviderSpec, in providerMetadataInput) (map[string]string, error) {
	meta := map[string]string{}

	baseURL := strings.TrimSpace(in.BaseURL)
	if in.Region != "" {
		meta["region"] = strings.TrimSpace(in.Region)
		if resolved := connectors.ResolveRegionBaseURL(spec.ID, in.Region); resolved != "" {
			baseURL = resolved
		}
	}
	if spec.BaseURL == "" && spec.ID != "azure" && baseURL == "" {
		return nil, fmt.Errorf("base_url is required for %s", spec.DisplayName)
	}
	if baseURL != "" {
		meta["base_url"] = baseURL
	}

	switch spec.ID {
	case "cloudflare-ai":
		accountID := strings.TrimSpace(in.AccountID)
		if accountID == "" {
			return nil, errors.New("account_id is required for Cloudflare Workers AI")
		}
		// OpenAICompatible resolves {accountId} placeholders from Extra.
		meta["accountId"] = accountID
	case "azure":
		endpoint := strings.TrimRight(strings.TrimSpace(in.AzureEndpoint), "/")
		deployment := strings.TrimSpace(in.AzureDeployment)
		if endpoint == "" {
			return nil, errors.New("azure_endpoint is required for Azure OpenAI")
		}
		if deployment == "" {
			return nil, errors.New("azure_deployment is required for Azure OpenAI")
		}
		meta["azure_endpoint"] = endpoint
		meta["deployment"] = deployment
		if v := strings.TrimSpace(in.AzureAPIVersion); v != "" {
			meta["api_version"] = v
		}
		if v := strings.TrimSpace(in.AzureOrganization); v != "" {
			meta["organization"] = v
		}
	}

	return meta, nil
}

// validateAccountCredentials unseals an account's credentials and, if the
// connector implements core.Validator, probes the upstream to confirm they are
// accepted. Returns nil when validation passes or the connector does not support
// it. No-auth accounts still run connector probes when available so local
// endpoints such as Ollama/SearXNG can verify reachability.
//
// When the initial probe fails with an auth error and the account is OAuth,
// it retries once after forcing a token refresh (even if the token hasn't
// reached its local expiry — tokens can be invalidated server-side before
// expiry). A permanent refresh failure marks the account as needing
// reconnection.
func (s *Server) validateAccountCredentials(ctx context.Context, acc store.Account) error {
	if s.conns == nil || s.vault == nil {
		return nil // can't validate without registry + vault
	}
	// Skip validation for providers behind WAF/CDN that block probes.
	if spec, ok := connectors.SpecByID(acc.Provider); ok && spec.SkipValidation {
		return nil
	}
	conn, err := s.conns.Get(acc.Provider)
	if err != nil {
		return nil // provider has no connector; skip validation
	}
	v, ok := conn.(core.Validator)
	if !ok {
		return nil // connector doesn't support validation
	}
	// Refresh OAuth tokens if they are about to expire so the upstream probe
	// does not fail with a stale access token.
	if s.refresher != nil {
		if refreshed, err := s.refresher.EnsureFresh(ctx, acc); err == nil {
			acc = refreshed
		}
		// If refresh fails, fall through with the original account — Validate
		// will report the upstream error, which is more actionable.
	}
	creds, err := s.vault.Open(acc)
	if err != nil {
		return errors.New("could not decrypt credentials")
	}
	// Apply a reasonable timeout for the probe.
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	probeErr := v.Validate(probeCtx, creds)
	if probeErr == nil {
		return nil
	}

	// If validation failed with an auth error on an OAuth account, the token
	// may have been invalidated server-side before its local expiry. Force a
	// refresh retry and mark the account if the refresh is permanently dead.
	if acc.AuthKind == store.AuthOAuth && s.refresher != nil && s.accounts != nil {
		pe := core.AsProviderError(probeErr)
		if pe != nil && pe.Kind == core.ErrAuth {
			if refreshed, rerr := s.refresher.ForceRefresh(ctx, acc); rerr != nil {
				return probeErr // ForceRefresh already marks needs_reconnect if permanent
			} else {
				// Refresh succeeded — retry validation with the new token.
				newCreds, cerr := s.vault.Open(refreshed)
				if cerr == nil {
					probeCtx2, cancel2 := context.WithTimeout(ctx, 15*time.Second)
					defer cancel2()
					retryErr := v.Validate(probeCtx2, newCreds)
					if retryErr == nil {
						// Clear needs_reconnect if it was set.
						if acc.NeedsReconnect {
							_ = s.accounts.SetNeedsReconnect(ctx, acc.ID, false)
						}
						return nil
					}
					// Retry still failed — mark for reconnect if it's an auth error.
					retryPE := core.AsProviderError(retryErr)
					if retryPE != nil && retryPE.Kind == core.ErrAuth {
						_ = s.accounts.SetNeedsReconnect(ctx, acc.ID, true)
					}
					return retryErr
				}
			}
		}
	}

	return probeErr
}

// decodeJSON decodes a request body into v, writing a 400 on failure. It
// returns false when the caller should stop.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, http.ErrBodyReadAfterClose) {
			writeError(w, http.StatusBadRequest, "empty body")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func defaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func defaultBool(v, def bool) bool {
	return v || (!v && def)
}

// validateChainName rejects combo names that would conflict with routing resolution.
// Names must be alphanumeric with hyphens/underscores only, no slashes, colons,
// or leading/trailing whitespace. This prevents ambiguity with "provider/model"
// and "chain:name" formats in resolveTargets.
func validateChainName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("combo name is required")
	}
	if len(name) > 128 {
		return fmt.Errorf("combo name too long (max 128 characters)")
	}
	if strings.ContainsAny(name, "/:\\@#?") {
		return fmt.Errorf("combo name cannot contain / : \\ @ # ? characters")
	}
	if strings.HasPrefix(name, "chain:") {
		return fmt.Errorf("combo name cannot start with 'chain:' prefix")
	}
	// Must match ^[a-zA-Z0-9][a-zA-Z0-9_-]*$
	for i, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if c == '-' || c == '_' {
			if i == 0 {
				return fmt.Errorf("combo name must start with a letter or digit")
			}
			continue
		}
		return fmt.Errorf("combo name can only contain letters, digits, hyphens, and underscores")
	}
	return nil
}
