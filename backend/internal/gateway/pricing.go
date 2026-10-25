package gateway

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	// maxPricingModels caps how many models one /v1/pricing call may name.
	maxPricingModels = 50
	// pricingRequestsPerMinute is the per-key budget for /v1/pricing. Prices
	// refresh about every 5s, so a widget polling at that pace needs 12 a
	// minute; 30 leaves room for a second tab and page reloads.
	pricingRequestsPerMinute = 30
	// pricingCacheTTL collapses bursts from many keys into one pricing pass.
	// It is well under the market refresh interval, so it adds at most 2s of
	// staleness.
	pricingCacheTTL = 2 * time.Second
)

// keyPrice is one model's entry in /v1/pricing. Prices come from Surplus only
// and are what the customer pays, markup included; upstream cost and margin
// are never exposed.
type keyPrice struct {
	Model          string   `json:"model"`
	Available      bool     `json:"available"`
	InputPerM      *float64 `json:"input_per_m"`
	OutputPerM     *float64 `json:"output_per_m"`
	CachedPerM     *float64 `json:"cached_input_per_m"`
	CacheWritePerM *float64 `json:"cache_write_per_m"`
	// MaxInputPerM/MaxOutputPerM bound what a request can cost when Surplus
	// fails over to a pricier seller; null when unknown.
	MaxInputPerM       *float64 `json:"max_input_per_m"`
	MaxOutputPerM      *float64 `json:"max_output_per_m"`
	OfficialInputPerM  *float64 `json:"official_input_per_m"`
	OfficialOutputPerM *float64 `json:"official_output_per_m"`
	DiscountPct        *int     `json:"discount_pct"`
}

// pricingCache holds the last priced list of every Surplus chain for
// pricingCacheTTL. One entry only, so arbitrary ?model= values cannot grow it.
type pricingCache struct {
	mu      sync.Mutex
	builtAt time.Time
	byName  map[string]keyPrice
	// idToName maps each priced chain's ID to its lower-cased name.
	idToName map[string]string
	asOf     time.Time
}

// handlePricing serves GET /v1/pricing[?model=a,b] for an API key: the live
// Surplus price of each chain the key may call (or only the named ones),
// from the in-memory market snapshot, so it is as fresh as the last market
// sync (as_of). Chains not priced by Surplus and chains the key may not call
// are left out; names asked for but not listed are skipped,
// and naming only such models is a 404.
func (s *Server) handlePricing(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
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
	chains, err := s.keyChains(r, key, key.PlanID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing unavailable")
		return
	}
	allowed := make(map[string]keyPrice, len(chains))
	for _, c := range chains {
		if p, ok := prices.byName[strings.ToLower(c.Name)]; ok {
			allowed[strings.ToLower(c.Name)] = p
		}
	}
	writePriceList(w, allowed, wanted, prices.asOf)
}

// writePriceList answers with the allowed prices, narrowed to wanted when
// it is set. Wanted names outside allowed are skipped; when
// none of them is allowed the answer is a 404.
func writePriceList(w http.ResponseWriter, allowed map[string]keyPrice, wanted []string, asOf time.Time) {
	selected := make([]keyPrice, 0, len(allowed))
	unknown := []string{}
	if len(wanted) == 0 {
		for _, p := range allowed {
			selected = append(selected, p)
		}
	} else {
		seen := map[string]bool{}
		for _, name := range wanted {
			k := strings.ToLower(name)
			if seen[k] {
				continue
			}
			seen[k] = true
			if p, ok := allowed[k]; ok {
				selected = append(selected, p)
			} else {
				unknown = append(unknown, name)
			}
		}
		if len(selected) == 0 {
			writeError(w, http.StatusNotFound, "model not available: "+strings.Join(unknown, ", "))
			return
		}
	}
	sort.Slice(selected, func(i, j int) bool { return strings.ToLower(selected[i].Model) < strings.ToLower(selected[j].Model) })

	var asOfJSON any
	if !asOf.IsZero() {
		asOfJSON = asOf.UTC().Format(time.RFC3339)
	}
	// Prices move with every market sync; never let a proxy hold them.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"currency": "USD",
		"unit":     "per_1m_tokens",
		"as_of":    asOfJSON,
		"models":   selected,
	})
}

// priceList is one build of the Surplus price list.
type priceList struct {
	byName   map[string]keyPrice // lower-cased chain name -> price
	idToName map[string]string   // chain ID -> lower-cased chain name
	asOf     time.Time
}

// surplusPrices returns every Surplus-priced chain, rebuilt at most once per
// pricingCacheTTL.
func (s *Server) surplusPrices(ctx context.Context) (priceList, error) {
	c := &s.pricing
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byName != nil && time.Since(c.builtAt) < pricingCacheTTL {
		return priceList{byName: c.byName, idToName: c.idToName, asOf: c.asOf}, nil
	}
	chains, err := s.chains.ListByTenant(ctx, adminTenant)
	if err != nil {
		return priceList{}, err
	}
	settings := market.DefaultSettings()
	if s.settings != nil {
		settings = market.LoadSettings(ctx, s.settings.Get)
	}
	byName := make(map[string]keyPrice, len(chains))
	idToName := make(map[string]string, len(chains))
	for _, ch := range chains {
		slugs := surplusSlugs(ch)
		if len(ch.Steps) == 0 || len(slugs) == 0 {
			continue
		}
		name := strings.ToLower(ch.Name)
		byName[name] = s.surplusChainPrice(ch, slugs, settings)
		idToName[ch.ID] = name
	}
	var asOf time.Time
	if s.marketCache != nil {
		asOf = s.marketCache.FetchedAt()
	}
	c.byName, c.idToName, c.asOf, c.builtAt = byName, idToName, asOf, time.Now()
	return priceList{byName: byName, idToName: idToName, asOf: asOf}, nil
}

// surplusSlugs lists the chain's Surplus market slugs (chain-level and per
// step), trusted or not. A chain without one is not priced by Surplus.
func surplusSlugs(c store.Chain) []string {
	var out []string
	seen := map[string]bool{}
	add := func(slug string) {
		if seen[slug] {
			return
		}
		if strings.HasPrefix(slug, market.SurplusTrustedPrefix) || strings.HasPrefix(slug, market.SurplusPrefix) {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	for _, slug := range c.MarketSlugs {
		add(slug)
	}
	for _, st := range c.Steps {
		add(st.MarketSlug)
	}
	return out
}

// surplusChainPrice prices one chain from its Surplus slugs in the live
// snapshot, the way the market sync would, markup included.
func (s *Server) surplusChainPrice(c store.Chain, slugs []string, settings market.Settings) keyPrice {
	p := keyPrice{Model: c.Name}
	if c.Disabled || s.marketCache == nil {
		return p
	}
	models := s.marketCache.Models(slugs)
	markup := market.EffectiveMarkup(c.MarkupPercent, settings.MarkupPercent)
	rate, ok := market.ComputeChainRateWithSafety(slugs, models, markup, market.CacheReadMult, market.CacheWriteMult,
		settings.SafetyMarginEnabled, settings.SafetyMarginPercent)
	if !ok {
		return p // sold out: no live Surplus quote
	}
	p.Available = true
	p.InputPerM, p.OutputPerM = price(rate.InputPerM), price(rate.OutputPerM)
	p.CachedPerM, p.CacheWritePerM = price(rate.CachedInputPerM), price(rate.CacheWritePerM)
	if maxIn, maxOut, ok := chainMaxRate(models, markup); ok {
		p.MaxInputPerM, p.MaxOutputPerM = price(atLeast(maxIn, rate.InputPerM)), price(atLeast(maxOut, rate.OutputPerM))
	}
	if ref, ok := s.chainOfficialPrice(c); ok {
		p.OfficialInputPerM, p.OfficialOutputPerM = price(ref.InputPerM), price(ref.OutputPerM)
		if d := discountPercent(rate.InputPerM, rate.OutputPerM, ref); d > 0 {
			p.DiscountPct = &d
		}
	}
	return p
}

// chainMaxRate is the most a request can be billed per 1M tokens on the
// cheapest bound slug: its failover cap (for surplus-trusted slugs, the
// highest price the X-Min-Discount ceiling admits) times the markup.
func chainMaxRate(models []market.Model, markupPercent float64) (float64, float64, bool) {
	var best market.Model
	found := false
	for _, m := range models {
		if m.MinAskIn <= 0 || m.MinAskOut <= 0 {
			continue
		}
		if !found || m.MinAskIn+m.MinAskOut < best.MinAskIn+best.MinAskOut {
			best, found = m, true
		}
	}
	if !found || best.FailoverIn <= 0 || best.FailoverOut <= 0 {
		return 0, 0, false
	}
	mult := 1 + markupPercent/100
	return best.FailoverIn * mult, best.FailoverOut * mult, true
}

// pricingModelFilter reads ?model=a,b (repeatable) into a list of names.
func pricingModelFilter(r *http.Request) ([]string, error) {
	var names []string
	for _, v := range r.URL.Query()["model"] {
		for _, n := range strings.Split(v, ",") {
			if n = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(n), "chain:")); n != "" {
				names = append(names, n)
			}
		}
	}
	if len(names) > maxPricingModels {
		return nil, errTooManyPricingModels
	}
	return names, nil
}

var errTooManyPricingModels = badModelError{msg: "at most 50 models per request", status: http.StatusBadRequest}

// pricingRateLimiter gives /v1/pricing its own budget per API key, separate
// from the key's inference traffic. It runs after authMiddleware.
func (s *Server) pricingRateLimiter(next http.Handler) http.Handler {
	limiter := newIPLimiter(pricingRequestsPerMinute, time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _ := authedKey(r.Context())
		id := "key:" + key.ID
		if !limiter.Allow(id) {
			_, wait := limiter.Exceeded(id)
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: at most 30 pricing requests per minute")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// price rounds a per-1M rate to 9 decimals (a thousandth of a micro-dollar)
// so float noise such as 0.0013574000000000001 never reaches clients.
func price(v float64) *float64 {
	r := math.Round(v*1e9) / 1e9
	return &r
}

// atLeast keeps a max price from reading below the current price when float
// rounding or a safety floor lifts the current one.
func atLeast(maxV, cur float64) float64 {
	if maxV < cur {
		return cur
	}
	return maxV
}
