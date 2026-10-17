package gateway

import (
	"math"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// officialPrice is the list price a chain's public discount is measured
// against, in USD per 1M tokens, with where it came from.
type officialPrice struct {
	InputPerM  float64
	OutputPerM float64
	// Source is manual | surplus | openrouter | catalog.
	Source string
}

// chainOfficialPrice resolves a chain's official price: the operator's manual
// value when both sides are set, otherwise the automatic reference.
func (s *Server) chainOfficialPrice(c store.Chain) (officialPrice, bool) {
	if c.OfficialInputPerM != nil && c.OfficialOutputPerM != nil {
		ref := market.Reference{InputPerM: *c.OfficialInputPerM, OutputPerM: *c.OfficialOutputPerM}
		if ref.Valid() {
			return officialPrice{InputPerM: ref.InputPerM, OutputPerM: ref.OutputPerM, Source: "manual"}, true
		}
	}
	return s.autoOfficialPrice(c)
}

// autoOfficialPrice looks the chain's model up in the automatic references, in
// order: the Surplus feed's direct price, OpenRouter's list price, then the
// built-in catalog. Candidate names come from the chain's market slugs and
// step models.
func (s *Server) autoOfficialPrice(c store.Chain) (officialPrice, bool) {
	names := officialCandidateNames(c)
	if s.marketCache != nil {
		for _, name := range names {
			if m, ok := s.marketCache.Lookup(market.SurplusPrefix + name); ok {
				if ref := (market.Reference{InputPerM: m.DirectIn, OutputPerM: m.DirectOut}); ref.Valid() {
					return officialPrice{InputPerM: ref.InputPerM, OutputPerM: ref.OutputPerM, Source: "surplus"}, true
				}
			}
		}
	}
	for _, name := range names {
		if ref, ok := s.references.Lookup(name); ok {
			return officialPrice{InputPerM: ref.InputPerM, OutputPerM: ref.OutputPerM, Source: "openrouter"}, true
		}
	}
	for _, st := range c.Steps {
		price, ok := connectors.ModelPriceByProviderModel(st.Provider, st.Model)
		if !ok || price.Estimated || (price.Source != "official" && price.Source != "catalog") {
			continue
		}
		if ref := (market.Reference{InputPerM: price.InputPerM, OutputPerM: price.OutputPerM}); ref.Valid() {
			return officialPrice{InputPerM: ref.InputPerM, OutputPerM: ref.OutputPerM, Source: "catalog"}, true
		}
	}
	return officialPrice{}, false
}

// officialCandidateNames lists the model names a chain may be known by, most
// specific first: market slug names (prefix stripped), then step models,
// then the bare names of vendor-prefixed ones.
func officialCandidateNames(c store.Chain) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	slugName := func(slug string) string {
		for _, prefix := range []string{market.SurplusTrustedPrefix, market.SurplusPrefix} {
			if name, ok := strings.CutPrefix(slug, prefix); ok {
				return name
			}
		}
		return slug
	}
	for _, st := range c.Steps {
		add(slugName(st.MarketSlug))
	}
	for _, slug := range c.MarketSlugs {
		add(slugName(slug))
	}
	for _, st := range c.Steps {
		add(st.Model)
	}
	// Vendor-prefixed names ("ali/deepseek-v4.1-flash") also try the bare
	// model name, so they match the Surplus feed like an unprefixed chain.
	for _, name := range out {
		if i := strings.LastIndex(name, "/"); i >= 0 {
			add(name[i+1:])
		}
	}
	return out
}

// discountPercent is how much cheaper the offered price is than the official
// one, over input plus output per 1M (the same blend Surplus quotes). It is
// rounded down to a whole percent so the badge never overstates the saving;
// zero means no discount to show.
func discountPercent(inputPerM, outputPerM float64, official officialPrice) int {
	offered := inputPerM + outputPerM
	list := official.InputPerM + official.OutputPerM
	if offered <= 0 || list <= 0 || offered >= list {
		return 0
	}
	return int(math.Floor((1 - offered/list) * 100))
}
