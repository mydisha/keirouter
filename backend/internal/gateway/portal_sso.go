package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	portalSessionCookie = "kr_portal_session"
	portalStateCookie   = "kr_portal_state"
	portalStateTTL      = 5 * time.Minute
)

// portalSubject prefixes the Google subject in the portal session token so it
// can never collide with the dashboard "dashboard" subject.
func portalSubject(googleSub string) string { return "portal:" + googleSub }

// portalGoogleSub strips the "portal:" audience prefix to recover the raw
// Google subject stored in portal_users.google_sub.
func portalGoogleSub(sessionSub string) string {
	return strings.TrimPrefix(sessionSub, "portal:")
}

// portalSSOConfigured reports whether Google sign-in is available.
func (s *Server) portalSSOConfigured() bool { return s.portalSSO != nil }

// handlePortalLoginStart sets a CSRF state cookie and redirects to Google.
func (s *Server) handlePortalLoginStart(w http.ResponseWriter, r *http.Request) {
	if !s.portalSSOConfigured() {
		writeError(w, http.StatusServiceUnavailable, "portal sso not configured")
		return
	}
	state, err := randomState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     portalStateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   int(portalStateTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
	svc := s.portalSSO.WithRedirect(s.portalRedirectURL(r))
	http.Redirect(w, r, svc.AuthURL(state), http.StatusFound)
}

// handlePortalLoginCallback validates state, exchanges the code, and issues a
// portal session cookie.
func (s *Server) handlePortalLoginCallback(w http.ResponseWriter, r *http.Request) {
	if !s.portalSSOConfigured() {
		writeError(w, http.StatusServiceUnavailable, "portal sso not configured")
		return
	}
	c, err := r.Cookie(portalStateCookie)
	gotState := r.URL.Query().Get("state")
	if err != nil || c.Value == "" || gotState == "" || c.Value != gotState {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	// One-shot state cookie.
	http.SetCookie(w, &http.Cookie{Name: portalStateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}
	svc := s.portalSSO.WithRedirect(s.portalRedirectURL(r))
	id, err := svc.Exchange(r.Context(), code)
	if err != nil {
		s.log.Warn("portal: google exchange failed", "err", err)
		writeError(w, http.StatusUnauthorized, "google sign-in failed")
		return
	}
	tok, err := s.auth.IssuePortalSession(portalSubject(id.Sub), id.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}
	s.setPortalCookie(w, r, tok)
	http.Redirect(w, r, "/portal", http.StatusFound)
}

// handlePortalStatus reports the portal session's authentication and claim state.
func (s *Server) handlePortalStatus(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(portalSessionCookie)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	sub, ok := s.auth.SessionSubject(c.Value)
	if !ok || !strings.HasPrefix(sub, "portal:") {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	out := map[string]any{"authenticated": true, "claimed": false, "has_key": false, "provisioning_enabled": false}
	if email, ok := s.auth.SessionEmail(c.Value); ok {
		out["email"] = email
	}
	if planID, err := s.portalDefaultPlanID(r.Context()); err == nil && planID != "" {
		out["provisioning_enabled"] = true
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err == nil {
		out["claimed"] = true
		out["has_key"] = true
		out["key_id"] = u.KeyID
	} else if !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePortalClaim binds the authenticated Google user to an API key. The
// user must present the full key, which is verified with argon2 by identity.
func (s *Server) handlePortalClaim(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "api_key is required")
		return
	}
	key, err := s.identity.Authenticate(r.Context(), strings.TrimSpace(body.APIKey))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	email, _ := s.auth.SessionEmail(portalSessionToken(r))
	u := store.PortalUser{
		GoogleSub: portalGoogleSub(sub),
		Email:     email,
		KeyID:     key.ID,
	}
	if err := s.db.PortalUsers().Upsert(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "this api key is already claimed by another account")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save claim")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key_id": key.ID})
}

// portalDefaultPlanKey is the settings key holding the admin-selected default plan
// for portal self-provisioning. Empty means provisioning is disabled.
const portalDefaultPlanKey = "portal.default_plan_id"

// portalDefaultPlanID returns the configured default plan for the portal.
func (s *Server) portalDefaultPlanID(ctx context.Context) (string, error) {
	if s.settings == nil {
		return "", nil
	}
	v, err := s.settings.Get(ctx, portalDefaultPlanKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// handlePortalPlans lists plans the portal may self-provision. Public: a
// signed-in user needs this before creating a key.
func (s *Server) handlePortalPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.db.Plans().List(r.Context(), store.DefaultTenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list plans")
		return
	}
	out := make([]map[string]any, 0, len(plans))
	for _, p := range plans {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"limit_usd":      float64(p.LimitMicros) / 1_000_000,
			"limit_tokens":   p.LimitTokens,
			"period":         p.Period,
			"allowed_models": store.GetPlanAllowedModels(p),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": out})
}

// handlePortalCreateKey provisions an API key for the signed-in user from the
// admin-selected default plan, then binds it. The plaintext key is returned
// exactly once. Idempotency: a user who already has a binding gets 409.
func (s *Server) handlePortalCreateKey(w http.ResponseWriter, r *http.Request) {
	sessionSub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	sub := portalGoogleSub(sessionSub)
	if _, err := s.db.PortalUsers().GetBySub(r.Context(), sub); err == nil {
		writeError(w, http.StatusConflict, "you already have an api key")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}

	planID, err := s.portalDefaultPlanID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load portal config")
		return
	}
	if planID == "" {
		writeError(w, http.StatusConflict, "portal self-provisioning is not enabled")
		return
	}
	plan, err := s.db.Plans().Get(r.Context(), planID)
	if err != nil {
		writeError(w, http.StatusConflict, "portal plan is not configured")
		return
	}

	email, _ := s.auth.SessionEmail(portalSessionToken(r))
	issued, err := s.provisionPortalKey(r.Context(), plan, sub, email)
	if err != nil {
		s.log.Error("portal create key: provision failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create api key")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":            issued.Plaintext,
		"key_id":         issued.Record.ID,
		"display":        issued.Record.Display,
		"plan_id":        plan.ID,
		"plan_name":      plan.Name,
		"allowed_models": store.GetPlanAllowedModels(plan),
	})
}

// provisionPortalKey creates a key from a plan (budget + model access) and
// binds it to the user atomically. Mirrors adminCreateKey's plan path.
func (s *Server) provisionPortalKey(ctx context.Context, plan store.Plan, sub, email string) (identity.Issued, error) {
	issued, err := s.identity.Generate(store.DefaultTenantID, "", "portal:"+email)
	if err != nil {
		return identity.Issued{}, err
	}
	issued.Record.PlanID = plan.ID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Issued{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.identity.Keys().CreateOnTx(ctx, tx, issued.Record); err != nil {
		return identity.Issued{}, err
	}

	limitMicros := plan.LimitMicros
	limitTokens := plan.LimitTokens
	if limitMicros > 0 || limitTokens > 0 {
		alertPct := plan.AlertPct
		if alertPct < 1 || alertPct > 100 {
			alertPct = 80
		}
		period := plan.Period
		if p, ok := normalizeBudgetPeriod(period); ok {
			period = p
		}
		if period == "" {
			period = "monthly"
		}
		now := time.Now()
		if err := s.budgets.CreateOnTx(ctx, tx, store.Budget{
			ID:          uuid.NewString(),
			TenantID:    store.DefaultTenantID,
			ScopeKind:   store.ScopeAPIKey,
			ScopeID:     issued.Record.ID,
			LimitMicros: limitMicros,
			LimitTokens: limitTokens,
			Period:      period,
			AlertPct:    alertPct,
			HardCutoff:  plan.HardCutoff,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return identity.Issued{}, err
		}
	}

	if models := store.GetPlanAllowedModels(plan); len(models) > 0 {
		if err := s.identity.Keys().SetAllowedModelsOnTx(ctx, tx, issued.Record.ID, models); err != nil {
			return identity.Issued{}, err
		}
	}

	// Bind the new key to the user in the same transaction.
	if err := s.db.PortalUsers().UpsertOnTx(ctx, tx, store.PortalUser{
		GoogleSub: sub, Email: email, KeyID: issued.Record.ID, PlanID: plan.ID,
	}); err != nil {
		return identity.Issued{}, err
	}

	if err := tx.Commit(); err != nil {
		return identity.Issued{}, err
	}
	return issued, nil
}

// ---- admin: portal user management ------------------------------------------

// portalUserView builds the admin-facing view of a binding, joining the key
// and its budget. Secrets (hashes) are never included.
func (s *Server) portalUserView(ctx context.Context, u store.PortalUser) map[string]any {
	view := map[string]any{
		"google_sub": u.GoogleSub,
		"email":      u.Email,
		"key_id":     u.KeyID,
		"plan_id":    u.PlanID,
		"created_at": u.CreatedAt,
	}
	if key, err := s.identity.Get(ctx, u.KeyID); err == nil {
		view["key_name"] = key.Name
		view["display"] = key.Display
		view["disabled"] = key.Disabled
		view["last_used_at"] = key.LastUsedAt
	}
	if budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, u.KeyID); err == nil && len(budgets) > 0 {
		b := budgets[0]
		view["budget"] = map[string]any{
			"limit_micros": b.LimitMicros, "limit_tokens": b.LimitTokens,
			"period": b.Period, "hard_cutoff": b.HardCutoff,
		}
	}
	return view
}

// adminListPortalUsers lists portal bindings with their key + budget.
func (s *Server) adminListPortalUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.PortalUsers().List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, s.portalUserView(r.Context(), u))
	}
	defaultPlan, _ := s.portalDefaultPlanID(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "default_plan_id": defaultPlan})
}

// adminGetPortalSettings returns the portal self-provisioning default plan.
func (s *Server) adminGetPortalSettings(w http.ResponseWriter, r *http.Request) {
	planID, err := s.portalDefaultPlanID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"default_plan_id": planID})
}

// adminUpdatePortalSettings sets the default plan used for self-provisioning.
// An empty plan_id disables portal key creation.
func (s *Server) adminUpdatePortalSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DefaultPlanID string `json:"default_plan_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DefaultPlanID != "" {
		if _, err := s.db.Plans().Get(r.Context(), body.DefaultPlanID); err != nil {
			writeError(w, http.StatusBadRequest, "plan not found")
			return
		}
	}
	if err := s.settings.Set(r.Context(), portalDefaultPlanKey, body.DefaultPlanID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"default_plan_id": body.DefaultPlanID})
}

// adminDeletePortalUser revokes a binding and deletes its key.
func (s *Server) adminDeletePortalUser(w http.ResponseWriter, r *http.Request) {
	sub := chi.URLParam(r, "sub")
	u, err := s.db.PortalUsers().GetBySub(r.Context(), sub)
	if err != nil {
		writeError(w, http.StatusNotFound, "portal user not found")
		return
	}
	if err := s.identity.Delete(r.Context(), u.KeyID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if err := s.db.PortalUsers().Delete(r.Context(), sub); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminSetPortalUserPlan changes the plan for a binding and re-syncs the key's
// budget and model access to match the new plan.
func (s *Server) adminSetPortalUserPlan(w http.ResponseWriter, r *http.Request) {
	sub := chi.URLParam(r, "sub")
	u, err := s.db.PortalUsers().GetBySub(r.Context(), sub)
	if err != nil {
		writeError(w, http.StatusNotFound, "portal user not found")
		return
	}
	var body struct {
		PlanID string `json:"plan_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var plan *store.Plan
	if body.PlanID != "" {
		p, err := s.db.Plans().Get(r.Context(), body.PlanID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "plan not found")
			return
		}
		plan = &p
	}
	if err := s.applyPortalPlan(r.Context(), u.KeyID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if err := s.db.PortalUsers().SetPlanID(r.Context(), sub, body.PlanID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"google_sub": sub, "plan_id": body.PlanID})
}

// applyPortalPlan re-syncs a key's budget and allowed models to a plan. When
// plan is nil the key's budget is removed and models cleared.
func (s *Server) applyPortalPlan(ctx context.Context, keyID string, plan *store.Plan) error {
	var planID string
	var models []string
	var wantBudget bool
	if plan != nil {
		planID = plan.ID
		models = store.GetPlanAllowedModels(*plan)
		wantBudget = plan.LimitMicros > 0 || plan.LimitTokens > 0
	}
	if err := s.identity.Keys().SetPlanID(ctx, keyID, planID); err != nil {
		return err
	}
	if err := s.identity.Keys().SetAllowedModels(ctx, keyID, models); err != nil {
		return err
	}

	existing, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, keyID)
	if err != nil {
		return err
	}
	switch {
	case !wantBudget:
		for _, b := range existing {
			if err := s.budgets.Delete(ctx, b.ID); err != nil {
				return err
			}
		}
	case len(existing) == 0:
		alertPct := plan.AlertPct
		if alertPct < 1 || alertPct > 100 {
			alertPct = 80
		}
		period := plan.Period
		if p, ok := normalizeBudgetPeriod(period); ok {
			period = p
		}
		if period == "" {
			period = "monthly"
		}
		now := time.Now()
		if err := s.budgets.Create(ctx, store.Budget{
			ID: uuid.NewString(), TenantID: store.DefaultTenantID,
			ScopeKind: store.ScopeAPIKey, ScopeID: keyID,
			LimitMicros: plan.LimitMicros, LimitTokens: plan.LimitTokens,
			Period: period, AlertPct: alertPct, HardCutoff: plan.HardCutoff,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
	default:
		b := existing[0]
		b.LimitMicros = plan.LimitMicros
		b.LimitTokens = plan.LimitTokens
		if p, ok := normalizeBudgetPeriod(plan.Period); ok {
			b.Period = p
		}
		if b.AlertPct < 1 || b.AlertPct > 100 {
			b.AlertPct = 80
		}
		b.HardCutoff = plan.HardCutoff
		b.UpdatedAt = time.Now()
		if err := s.budgets.Update(ctx, b); err != nil {
			return err
		}
	}
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCacheForScope(store.ScopeAPIKey, keyID)
	}
	return nil
}

// adminTogglePortalUserKey enables/disables the user's key.
func (s *Server) adminTogglePortalUserKey(w http.ResponseWriter, r *http.Request) {
	sub := chi.URLParam(r, "sub")
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), sub)
	if err != nil {
		writeError(w, http.StatusNotFound, "portal user not found")
		return
	}
	if err := s.identity.SetDisabled(r.Context(), u.KeyID, body.Disabled); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"google_sub": sub, "key_id": u.KeyID, "disabled": body.Disabled})
}

// adminRotatePortalUserKey regenerates the key material in place. The
// plaintext is returned once.
func (s *Server) adminRotatePortalUserKey(w http.ResponseWriter, r *http.Request) {
	sub := chi.URLParam(r, "sub")
	u, err := s.db.PortalUsers().GetBySub(r.Context(), sub)
	if err != nil {
		writeError(w, http.StatusNotFound, "portal user not found")
		return
	}
	existing, err := s.identity.Get(r.Context(), u.KeyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	issued, err := s.identity.Generate(existing.TenantID, existing.ProjectID, existing.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if err := s.identity.Keys().SetKeyMaterial(r.Context(), u.KeyID, issued.Record.KeyHash, issued.Record.LookupHash, issued.Record.Display); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	s.identity.InvalidateAuthCacheForKey(u.KeyID)
	writeJSON(w, http.StatusOK, map[string]any{"google_sub": sub, "key_id": u.KeyID, "key": issued.Plaintext, "display": issued.Record.Display})
}

// handlePortalUsage returns usage for the caller's claimed key.
func (s *Server) handlePortalUsage(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "no api key claimed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}
	key, err := s.identity.Keys().Get(r.Context(), u.KeyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "claimed key no longer exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load key")
		return
	}
	payload, err := s.buildKeyUsageMap(r.Context(), key, parseUsageDays(r))
	if err != nil {
		s.log.Error("portal usage: build failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to build usage")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// handlePortalKey returns a masked preview of the signed-in user's API key plus
// its metadata. The plaintext and hashes are never returned.
func (s *Server) handlePortalKey(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no api key claimed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}
	key, err := s.identity.Get(r.Context(), u.KeyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key no longer exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load key")
		return
	}
	out := map[string]any{
		"key_id":     key.ID,
		"name":       key.Name,
		"display":    key.Display,
		"disabled":   key.Disabled,
		"created_at": key.CreatedAt,
		"plan_id":    u.PlanID,
	}
	if key.LastUsedAt != nil {
		out["last_used_at"] = key.LastUsedAt
	}
	if u.PlanID != "" {
		if plan, err := s.db.Plans().Get(r.Context(), u.PlanID); err == nil {
			out["plan_name"] = plan.Name
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePortalTopups returns the read-only topup ledger for the signed-in
// user's key plus its current budget balance.
func (s *Server) handlePortalTopups(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no api key claimed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}

	topups, err := s.db.Topups().ListByKey(r.Context(), u.KeyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load topups")
		return
	}
	out := make([]map[string]any, 0, len(topups))
	for _, t := range topups {
		out = append(out, map[string]any{
			"id": t.ID, "amount_usd": float64(t.AmountMicros) / 1_000_000,
			"reason": t.Reason, "created_at": t.CreatedAt,
		})
	}

	resp := map[string]any{"topups": out}
	if budgets, err := s.budgets.ListByScope(r.Context(), store.ScopeAPIKey, u.KeyID); err == nil && len(budgets) > 0 {
		b := budgets[0]
		spentMicros, _, _ := s.usage.SpendAndTokens(r.Context(), b.ScopeKind, b.ScopeID, budget.PeriodStart(b.Period, time.Now()))
		limitUSD := float64(b.LimitMicros) / 1_000_000
		spentUSD := float64(spentMicros) / 1_000_000
		remaining := limitUSD - spentUSD
		if remaining < 0 {
			remaining = 0
		}
		resp["balance"] = map[string]any{
			"limit_usd": limitUSD, "spent_usd": spentUSD, "usd_remaining": remaining,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePortalLogout clears the portal session cookie.
func (s *Server) handlePortalLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     portalSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// portalSessionMiddleware requires a valid portal session.
func (s *Server) portalSessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.portalSubject(r); !ok {
			writeError(w, http.StatusUnauthorized, "portal session required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// portalSubject returns the Google subject of the current portal session.
func (s *Server) portalSubject(r *http.Request) (string, bool) {
	tok := portalSessionToken(r)
	if tok == "" {
		return "", false
	}
	sub, ok := s.auth.SessionSubject(tok)
	if !ok || !strings.HasPrefix(sub, "portal:") {
		return "", false
	}
	return sub, true
}

func portalSessionToken(r *http.Request) string {
	c, err := r.Cookie(portalSessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) setPortalCookie(w http.ResponseWriter, r *http.Request, token string) {
	ttl := s.cfg.PortalSSO.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	http.SetCookie(w, &http.Cookie{
		Name:     portalSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
}

// portalRedirectURL returns the configured redirect URL, or derives one from
// the request's public base URL so it works behind a reverse proxy without
// extra configuration.
func (s *Server) portalRedirectURL(r *http.Request) string {
	if u := s.cfg.PortalSSO.RedirectURL; u != "" {
		return u
	}
	return s.publicBaseURL(r) + "/portal/auth/google/callback"
}

// parseUsageDays reads the days query param, defaulting to 30.
func parseUsageDays(r *http.Request) int {
	switch r.URL.Query().Get("days") {
	case "7":
		return 7
	case "14":
		return 14
	case "90":
		return 90
	default:
		return 30
	}
}

// randomState returns a URL-safe random CSRF state.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
