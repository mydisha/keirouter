package gateway

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// publicPricingSettingsKey stores which chains GET /v1/public/pricing shows.
const publicPricingSettingsKey = "public_pricing"

// publicPricingSettings is the operator's choice for the public price API.
// The zero value (off, nothing listed) is the default, so no price becomes
// public until an admin picks it.
type publicPricingSettings struct {
	Enabled bool `json:"enabled"`
	// ChainIDs are the chains the public API may show. IDs, not names, so a
	// rename keeps the choice; a deleted chain simply drops out.
	ChainIDs []string `json:"chain_ids"`
}

func (s *Server) loadPublicPricingSettings(ctx context.Context) publicPricingSettings {
	var out publicPricingSettings
	if s.settings == nil {
		return out
	}
	raw, err := s.settings.Get(ctx, publicPricingSettingsKey)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// publicPricing serves GET /v1/public/pricing[?model=a,b] without an API key:
// the live Surplus price of the chains an admin published, in the same shape
// as /v1/pricing. Off, it answers 404.
func (s *Server) publicPricing(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadPublicPricingSettings(r.Context())
	if !cfg.Enabled {
		writeError(w, http.StatusNotFound, "public pricing is not enabled")
		return
	}
	wanted, err := pricingModelFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	prices, err := s.surplusPrices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing unavailable")
		return
	}
	allowed := make(map[string]keyPrice, len(cfg.ChainIDs))
	for _, id := range cfg.ChainIDs {
		if name, ok := prices.idToName[id]; ok {
			allowed[name] = prices.byName[name]
		}
	}
	writePriceList(w, allowed, wanted, prices.asOf)
}

// publicPricingRateLimiter caps GET /v1/public/pricing per client IP (the
// real client behind a local proxy; see clientIP), with its own budget so it
// neither eats nor shares the landing page's allowance.
func (s *Server) publicPricingRateLimiter(next http.Handler) http.Handler {
	limiter := newIPLimiter(pricingRequestsPerMinute, time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		if !limiter.Allow(ip) {
			_, wait := limiter.Exceeded(ip)
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: at most 30 pricing requests per minute")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminPublicPricingModel is one Surplus-priced chain in the admin tab, with
// its live price for a preview.
type adminPublicPricingModel struct {
	ChainID   string `json:"chain_id"`
	Published bool   `json:"published"`
	keyPrice
}

// adminGetPublicPricing serves the admin tab: the setting plus every chain
// that can be published (Surplus-priced), each with its live price.
func (s *Server) adminGetPublicPricing(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadPublicPricingSettings(r.Context())
	prices, err := s.surplusPrices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing unavailable")
		return
	}
	published := make(map[string]bool, len(cfg.ChainIDs))
	for _, id := range cfg.ChainIDs {
		published[id] = true
	}
	models := make([]adminPublicPricingModel, 0, len(prices.idToName))
	ids := make([]string, 0, len(cfg.ChainIDs))
	for id, name := range prices.idToName {
		models = append(models, adminPublicPricingModel{ChainID: id, Published: published[id], keyPrice: prices.byName[name]})
		if published[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].Model) < strings.ToLower(models[j].Model) })
	sort.Strings(ids)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":   cfg.Enabled,
		"chain_ids": ids,
		"models":    models,
		"path":      "/v1/public/pricing",
	})
}

// adminUpdatePublicPricing saves the setting. Every chain ID must be a chain
// priced by Surplus; anything else is refused rather than silently dropped.
func (s *Server) adminUpdatePublicPricing(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "settings store not configured")
		return
	}
	var body publicPricingSettings
	if !decodeJSON(w, r, &body) {
		return
	}
	prices, err := s.surplusPrices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing unavailable")
		return
	}
	seen := make(map[string]bool, len(body.ChainIDs))
	ids := make([]string, 0, len(body.ChainIDs))
	for _, id := range body.ChainIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, ok := prices.idToName[id]; !ok {
			writeError(w, http.StatusBadRequest, "not a Surplus-priced chain: "+id)
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	body.ChainIDs = ids
	raw, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.settings.Set(r.Context(), publicPricingSettingsKey, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.adminGetPublicPricing(w, r)
}
