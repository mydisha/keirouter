package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	// SurplusTrustedPrefix keys Surplus models priced only from offers that are
	// trusted, available and healthy in the per-model order book. Requests
	// routed through such a slug are billed from the upstream's actual cost
	// (usage.buyer_cost_micro), not from the displayed rate.
	SurplusTrustedPrefix = "surplus-trusted:"
	// DefaultSurplusFallbackDepth is how many distinct price levels Surplus may
	// fail over across before the X-Min-Discount ceiling refuses the request.
	DefaultSurplusFallbackDepth = 3
	// ceilingMarginPct is how many points the X-Min-Discount ceiling sits
	// below the deepest allowed level's own discount. Surplus gates on an
	// estimated cost (rounded to whole microdollars, plus buffers), which reads
	// lower than the book discount on small requests; a ceiling set exactly at
	// the book discount would refuse offers it is meant to admit.
	ceilingMarginPct = 2
)

// TrustedQuote is the trusted-only view of one Surplus order book, in USD/M.
type TrustedQuote struct {
	Found bool
	// BestIn/BestOut is the cheapest trusted+available+healthy ask: the rate
	// shown to users.
	BestIn, BestOut float64
	// MinDiscountPct is the X-Min-Discount ceiling covering the first `depth`
	// distinct price levels. Zero means no list price was published, so no
	// ceiling can be proven and the header is not sent.
	MinDiscountPct int
	// CapIn/CapOut is the most Surplus can charge under MinDiscountPct (or the
	// deepest allowed level's own price when no list price is known). It is the
	// fallback bill when a response carries no buyer_cost_micro.
	CapIn, CapOut float64
	// FloorCapIn/FloorCapOut is the most a /min{N} provider floor alone lets
	// Surplus charge (list × (1-N/100)); zero when no floor applies. It bounds
	// the fallback bill when no X-Min-Discount ceiling is sent.
	FloorCapIn, FloorCapOut float64
	// Providers is the sorted allow-list of provider ids behind the eligible
	// offers. It is sent upstream as the body field `provider`, so Surplus
	// cannot route the request to an untrusted seller.
	Providers []string
	Eligible  int
}

// Model converts the quote into a snapshot model keyed surplus-trusted:<name>.
func (q TrustedQuote) Model(name string) Model {
	return Model{
		Slug:           SurplusTrustedPrefix + name,
		MinAskIn:       q.BestIn,
		MinAskOut:      q.BestOut,
		FailoverIn:     q.CapIn,
		FailoverOut:    q.CapOut,
		MinDiscountPct: q.MinDiscountPct,
		Providers:      q.Providers,
	}
}

type trustedBookEnvelope struct {
	Offers []struct {
		ProviderID        string   `json:"provider_id"`
		Trusted           bool     `json:"trusted"`
		Available         bool     `json:"available"`
		Healthy           bool     `json:"healthy"`
		PriceInputPer1M   *float64 `json:"price_input_per_1m"`
		PriceOutputPer1M  *float64 `json:"price_output_per_1m"`
		DirectInputPer1M  *float64 `json:"direct_input_per_1m"`
		DirectOutputPer1M *float64 `json:"direct_output_per_1m"`
	} `json:"offers"`
}

// ParseSurplusTrustedBook reads GET /api/markets/:model and quotes only offers
// with trusted, available and healthy all true. depth (>=1) is how many
// distinct price levels the ceiling admits; fewer levels use the deepest.
// floorPct > 0 is the provider's /min{N} floor: offers that cannot prove at
// least that discount below list on both rates are dropped, as Surplus would
// skip them.
func ParseSurplusTrustedBook(r io.Reader, depth, floorPct int) (TrustedQuote, error) {
	var body trustedBookEnvelope
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return TrustedQuote{}, err
	}
	if depth < 1 {
		depth = 1
	}
	type ask struct{ in, out, directIn, directOut float64 }
	var asks []ask
	providers := map[string]bool{}
	for _, o := range body.Offers {
		if !o.Trusted || !o.Available || !o.Healthy || o.ProviderID == "" {
			continue
		}
		if o.PriceInputPer1M == nil || o.PriceOutputPer1M == nil {
			continue
		}
		a := ask{in: *o.PriceInputPer1M / surplusUnitScale, out: *o.PriceOutputPer1M / surplusUnitScale}
		if !validRate(a.in) || !validRate(a.out) || a.out <= 0 {
			continue
		}
		if o.DirectInputPer1M != nil && o.DirectOutputPer1M != nil {
			a.directIn = *o.DirectInputPer1M / surplusUnitScale
			a.directOut = *o.DirectOutputPer1M / surplusUnitScale
		}
		if floorPct > 0 && !meetsFloor(a.in, a.out, a.directIn, a.directOut, floorPct) {
			continue
		}
		asks = append(asks, a)
		providers[o.ProviderID] = true
	}
	if len(asks) == 0 {
		return TrustedQuote{}, nil
	}
	sort.Slice(asks, func(i, j int) bool {
		si, sj := asks[i].in+asks[i].out, asks[j].in+asks[j].out
		if si != sj {
			return si < sj
		}
		return asks[i].out < asks[j].out
	})
	// Walk distinct price levels; the level-th one bounds the ceiling.
	level := asks[0]
	levels := 1
	for _, a := range asks[1:] {
		if levels >= depth {
			break
		}
		if a.in != level.in || a.out != level.out {
			level = a
			levels++
		}
	}
	q := TrustedQuote{Found: true, BestIn: asks[0].in, BestOut: asks[0].out, Eligible: len(asks), CapIn: level.in, CapOut: level.out}
	for p := range providers {
		q.Providers = append(q.Providers, p)
	}
	sort.Strings(q.Providers)
	if floorPct > 0 {
		// Every eligible offer passed the floor, so list × (1-N) bounds them all;
		// the max with the deepest offer only guards float rounding.
		keep := 1 - float64(floorPct)/100
		deepest := asks[len(asks)-1]
		q.FloorCapIn = math.Max(deepest.in, deepest.directIn*keep)
		q.FloorCapOut = math.Max(deepest.out, deepest.directOut*keep)
	}
	if level.directIn > 0 && level.directOut > 0 {
		// The ceiling must admit this level on both rates, so take the smaller
		// discount and floor it. The epsilon absorbs float noise on exact
		// percentages (1-0.06)*100 = 93.999...
		disc := math.Min(1-level.in/level.directIn, 1-level.out/level.directOut) * 100
		n := int(math.Floor(disc+1e-9)) - ceilingMarginPct
		// The provider's /min{N} floor applies regardless (Surplus takes the
		// stricter of URL and header), so the ceiling never needs to go lower.
		n = max(n, floorPct)
		if n > 0 && n <= 100 {
			q.MinDiscountPct = n
			keep := 1 - float64(n)/100
			q.CapIn = math.Max(level.in, level.directIn*keep)
			q.CapOut = math.Max(level.out, level.directOut*keep)
		}
	}
	return q, nil
}

// meetsFloor reports whether an offer is at least floorPct below its list
// price on both rates. Surplus estimates the discount on the blended request
// cost, so checking each rate is stricter: an offer kept here always qualifies
// upstream. Unknown list prices cannot prove a discount and fail.
func meetsFloor(in, out, directIn, directOut float64, floorPct int) bool {
	if directIn <= 0 || directOut <= 0 {
		return false
	}
	keep := 1 - float64(floorPct)/100
	const eps = 1e-12
	return in <= directIn*keep+eps && out <= directOut*keep+eps
}

// MinDiscountFloor parses the /min{N} path segment of a Surplus base URL
// (e.g. https://api.surplusintelligence.ai/min30/v1 → 30). It follows
// Surplus's grammar: min0..min100, no leading zeros, signs or decimals.
// Zero means no floor (min0 never tightens a trusted quote).
func MinDiscountFloor(baseURL string) int {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return 0
	}
	for _, seg := range strings.Split(u.Path, "/") {
		digits, ok := strings.CutPrefix(seg, "min")
		if !ok || digits == "" || len(digits) > 3 {
			continue
		}
		if len(digits) > 1 && digits[0] == '0' {
			continue
		}
		n := 0
		valid := true
		for _, c := range digits {
			if c < '0' || c > '9' {
				valid = false
				break
			}
			n = n*10 + int(c-'0')
		}
		if valid && n <= 100 {
			return n
		}
	}
	return 0
}

// SurplusBookURL returns the per-model order-book URL under marketsURL
// (defaulting to the public feed).
func SurplusBookURL(marketsURL, model string) string {
	if marketsURL == "" {
		marketsURL = SurplusAPIURL
	}
	return strings.TrimSuffix(marketsURL, "/") + "/" + url.PathEscape(model)
}

// FetchSurplusTrustedBook downloads one model's order book and quotes its
// trusted-only offers.
func FetchSurplusTrustedBook(ctx context.Context, marketsURL, model string, depth, floorPct int) (TrustedQuote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SurplusBookURL(marketsURL, model), nil)
	if err != nil {
		return TrustedQuote{}, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return TrustedQuote{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TrustedQuote{}, fmt.Errorf("surplus book status %d", resp.StatusCode)
	}
	return ParseSurplusTrustedBook(resp.Body, depth, floorPct)
}
