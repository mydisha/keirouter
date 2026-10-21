package gateway

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// maxPricingModels caps how many models one /v1/public/pricing call may name.
const maxPricingModels = 50

// publicPrice is one model's entry in /v1/public/pricing. Every price is
// what a customer pays, markup included; upstream cost and margin are never
// exposed.
type publicPrice struct {
	Model     string `json:"model"`
	Available bool   `json:"available"`
	// PriceSource is "market" when the price follows the live market
	// snapshot, "fixed" when the chain has a set price.
	PriceSource    string   `json:"price_source"`
	InputPerM      *float64 `json:"input_per_m"`
	OutputPerM     *float64 `json:"output_per_m"`
	CachedPerM     *float64 `json:"cached_input_per_m"`
	CacheWritePerM *float64 `json:"cache_write_per_m"`
	// MaxInputPerM/MaxOutputPerM bound what a request can cost when the
	// marketplace fails over to a pricier seller; null when unknown.
	MaxInputPerM       *float64 `json:"max_input_per_m"`
	MaxOutputPerM      *float64 `json:"max_output_per_m"`
	OfficialInputPerM  *float64 `json:"official_input_per_m"`
	OfficialOutputPerM *float64 `json:"official_output_per_m"`
	DiscountPct        *int     `json:"discount_pct"`
}

// publicPricing serves GET /v1/public/pricing[?model=a,b]: the live price of
// each chain (or only the named ones) from the in-memory market snapshot, so
// it is as fresh as the last market sync (see as_of) rather than the stored
// chain rate. Unknown names are listed under "unknown"; naming only unknown
// models is a 404.
func (s *Server) publicPricing(w http.ResponseWriter, r *http.Request) {
	wanted, err := pricingModelFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	chains, err := s.chains.ListByTenant(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing unavailable")
		return
	}
	settings := market.DefaultSettings()
	if s.settings != nil {
		settings = market.LoadSettings(r.Context(), s.settings.Get)
	}

	byName := make(map[string]store.Chain, len(chains))
	for _, c := range chains {
		if len(c.Steps) > 0 {
			byName[strings.ToLower(c.Name)] = c
		}
	}
	var selected []store.Chain
	unknown := []string{}
	if len(wanted) == 0 {
		for _, c := range byName {
			selected = append(selected, c)
		}
	} else {
		seen := map[string]bool{}
		for _, name := range wanted {
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			if c, ok := byName[key]; ok {
				selected = append(selected, c)
			} else {
				unknown = append(unknown, name)
			}
		}
		if len(selected) == 0 {
			writeError(w, http.StatusNotFound, "unknown model: "+strings.Join(unknown, ", "))
			return
		}
	}
	sort.Slice(selected, func(i, j int) bool { return strings.ToLower(selected[i].Name) < strings.ToLower(selected[j].Name) })

	prices := make([]publicPrice, 0, len(selected))
	for _, c := range selected {
		prices = append(prices, s.chainPublicPrice(c, settings))
	}
	var asOf any
	if s.marketCache != nil {
		if t := s.marketCache.FetchedAt(); !t.IsZero() {
			asOf = t.UTC().Format(time.RFC3339)
		}
	}
	// Prices move with every market sync; never let a proxy hold them.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"currency": "USD",
		"unit":     "per_1m_tokens",
		"as_of":    asOf,
		"models":   prices,
		"unknown":  unknown,
	})
}

// chainPublicPrice prices one chain the way the market sync would write it,
// but from the live snapshot.
func (s *Server) chainPublicPrice(c store.Chain, settings market.Settings) publicPrice {
	p := publicPrice{Model: c.Name, Available: true, PriceSource: "fixed"}
	var in, out, cached, write float64

	if len(c.MarketSlugs) > 0 {
		p.PriceSource = "market"
		if s.chainSoldOut(c) || s.marketCache == nil {
			p.Available = false
			return p
		}
		models := s.marketCache.Models(c.MarketSlugs)
		markup := market.EffectiveMarkup(c.MarkupPercent, settings.MarkupPercent)
		rate, ok := market.ComputeChainRateWithSafety(c.MarketSlugs, models, markup, market.CacheReadMult, market.CacheWriteMult,
			settings.SafetyMarginEnabled, settings.SafetyMarginPercent)
		if !ok {
			p.Available = false
			return p
		}
		in, out, cached, write = rate.InputPerM, rate.OutputPerM, rate.CachedInputPerM, rate.CacheWritePerM
		if maxIn, maxOut, ok := chainMaxRate(models, markup); ok {
			p.MaxInputPerM, p.MaxOutputPerM = price(atLeast(maxIn, in)), price(atLeast(maxOut, out))
		}
	} else {
		in, out, cached, write = c.InputPerM, c.OutputPerM, c.CacheReadPerM, c.CacheWritePerM
		if in <= 0 && out <= 0 && cached <= 0 && write <= 0 {
			price, _ := connectors.ModelPriceByProviderModel(c.Steps[0].Provider, c.Steps[0].Model)
			in, out, cached, write = price.InputPerM, price.OutputPerM, price.CachedInputPerM, price.CacheWritePerM
		}
	}
	p.InputPerM, p.OutputPerM, p.CachedPerM, p.CacheWritePerM = price(in), price(out), price(cached), price(write)
	if ref, ok := s.chainOfficialPrice(c); ok {
		p.OfficialInputPerM, p.OfficialOutputPerM = price(ref.InputPerM), price(ref.OutputPerM)
		if d := discountPercent(in, out, ref); d > 0 {
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
