package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// portalDiscountModel is one row of the portal's Discount Settings table.
type portalDiscountModel struct {
	Model string `json:"model"`
	// Supported is true when the chain has a step that can guarantee a
	// discount (a surplus-trusted step). Elsewhere any minimum refuses every
	// request.
	Supported bool `json:"supported"`
	// OverridePct is the model's own minimum; nil follows the key default.
	OverridePct *int `json:"override_pct"`
	// EffectivePct is what requests for the model must meet.
	EffectivePct int `json:"effective_pct"`
}

// handlePortalDiscounts serves GET /portal/api/discounts: the key's default
// minimum discount and, for every chain the key may call, its override.
func (s *Server) handlePortalDiscounts(w http.ResponseWriter, r *http.Request) {
	u, ok := s.portalUserOrError(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	key, err := s.identity.Get(ctx, u.KeyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key no longer exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load key")
		return
	}
	chains, err := s.keyChains(r, key, u.PlanID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load models")
		return
	}
	overrides, err := s.identity.Keys().ModelDiscounts(ctx, key.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load discounts")
		return
	}
	key.ModelDiscounts = overrides

	models := make([]portalDiscountModel, 0, len(chains))
	for _, c := range chains {
		row := portalDiscountModel{Model: c.Name, Supported: chainCanGuaranteeDiscount(c), EffectivePct: key.EffectiveMinDiscount(c.Name)}
		if pct, ok := overrides[store.NormalizeDiscountModel(c.Name)]; ok {
			row.OverridePct = &pct
		}
		models = append(models, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default_pct": key.MinDiscountPct,
		"max_pct":     maxDiscountFloorPct,
		"models":      models,
	})
}

// handlePortalSetModelDiscount serves PUT /portal/api/discounts/models/{model}.
func (s *Server) handlePortalSetModelDiscount(w http.ResponseWriter, r *http.Request) {
	u, model, ok := s.portalDiscountTarget(w, r)
	if !ok {
		return
	}
	pct, ok := decodeDiscountPct(w, r)
	if !ok {
		return
	}
	if err := s.identity.Keys().SetModelDiscount(r.Context(), u.KeyID, model, pct); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save minimum discount")
		return
	}
	s.identity.InvalidateAuthCacheForKey(u.KeyID)
	writeJSON(w, http.StatusOK, map[string]any{"model": model, "override_pct": pct})
}

// handlePortalDeleteModelDiscount serves DELETE
// /portal/api/discounts/models/{model}: the model follows the default again.
func (s *Server) handlePortalDeleteModelDiscount(w http.ResponseWriter, r *http.Request) {
	u, model, ok := s.portalDiscountTarget(w, r)
	if !ok {
		return
	}
	if err := s.identity.Keys().DeleteModelDiscount(r.Context(), u.KeyID, model); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset minimum discount")
		return
	}
	s.identity.InvalidateAuthCacheForKey(u.KeyID)
	writeJSON(w, http.StatusOK, map[string]any{"model": model, "override_pct": nil})
}

// portalDiscountTarget resolves the signed-in user and the {model} path
// parameter, which must be a chain the key may call.
func (s *Server) portalDiscountTarget(w http.ResponseWriter, r *http.Request) (store.PortalUser, string, bool) {
	u, ok := s.portalUserOrError(w, r)
	if !ok {
		return store.PortalUser{}, "", false
	}
	model := strings.TrimSpace(chi.URLParam(r, "model"))
	key, err := s.identity.Get(r.Context(), u.KeyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key no longer exists")
			return store.PortalUser{}, "", false
		}
		writeError(w, http.StatusInternalServerError, "failed to load key")
		return store.PortalUser{}, "", false
	}
	chains, err := s.keyChains(r, key, u.PlanID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load models")
		return store.PortalUser{}, "", false
	}
	for _, c := range chains {
		if store.NormalizeDiscountModel(c.Name) == store.NormalizeDiscountModel(model) {
			return u, c.Name, true
		}
	}
	writeError(w, http.StatusNotFound, "model not available to this key: "+model)
	return store.PortalUser{}, "", false
}

// keyChains lists the chains the key may call (its own or its plan's model
// access), sorted by name.
func (s *Server) keyChains(r *http.Request, key store.APIKey, planID string) ([]store.Chain, error) {
	ctx := r.Context()
	chains, err := s.chains.ListByTenant(ctx, tenantOf(key))
	if err != nil {
		return nil, err
	}
	allowed, _, err := s.identity.Keys().EffectiveAllowedModels(ctx, key.ID, planID)
	if err != nil {
		return nil, err
	}
	out := make([]store.Chain, 0, len(chains))
	for _, c := range chains {
		if len(allowed) == 0 || modelMatchesAny(c.Name, allowed) || modelMatchesAny("chain:"+c.Name, allowed) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// chainCanGuaranteeDiscount reports whether any step of the chain routes
// through a surplus-trusted slug, the only kind of step applyDiscountFloor
// keeps. It mirrors how chains bind those slugs (per step, or chain-level
// slugs matched to a step's model).
func chainCanGuaranteeDiscount(c store.Chain) bool {
	for _, st := range c.Steps {
		if strings.HasPrefix(st.MarketSlug, market.SurplusTrustedPrefix) {
			return true
		}
		for _, slug := range c.MarketSlugs {
			if name, ok := strings.CutPrefix(slug, market.SurplusTrustedPrefix); ok && name == st.Model {
				return true
			}
		}
	}
	return false
}

// portalUserOrError returns the signed-in portal user, writing the error
// response itself when there is none.
func (s *Server) portalUserOrError(w http.ResponseWriter, r *http.Request) (store.PortalUser, bool) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return store.PortalUser{}, false
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no api key claimed")
			return store.PortalUser{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return store.PortalUser{}, false
	}
	return u, true
}

// decodeDiscountPct reads {"min_discount_pct": N} and checks N is a whole
// percent from 0 to maxDiscountFloorPct.
func decodeDiscountPct(w http.ResponseWriter, r *http.Request) (int, bool) {
	var body struct {
		MinDiscountPct json.Number `json:"min_discount_pct"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<10))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return 0, false
	}
	pct, err := strconv.Atoi(body.MinDiscountPct.String())
	if err != nil || pct < 0 || pct > maxDiscountFloorPct {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("min_discount_pct must be a whole number from 0 to %d", maxDiscountFloorPct))
		return 0, false
	}
	return pct, true
}
