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
	Eligible      int
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
	}
}

type trustedBookEnvelope struct {
	Offers []struct {
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
func ParseSurplusTrustedBook(r io.Reader, depth int) (TrustedQuote, error) {
	var body trustedBookEnvelope
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return TrustedQuote{}, err
	}
	if depth < 1 {
		depth = 1
	}
	type ask struct{ in, out, directIn, directOut float64 }
	var asks []ask
	for _, o := range body.Offers {
		if !o.Trusted || !o.Available || !o.Healthy {
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
		asks = append(asks, a)
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
	if level.directIn > 0 && level.directOut > 0 {
		// The ceiling must admit this level on both rates, so take the smaller
		// discount and floor it. The epsilon absorbs float noise on exact
		// percentages (1-0.06)*100 = 93.999...
		disc := math.Min(1-level.in/level.directIn, 1-level.out/level.directOut) * 100
		n := int(math.Floor(disc + 1e-9))
		if n > 0 && n <= 100 {
			q.MinDiscountPct = n
			keep := 1 - float64(n)/100
			q.CapIn = math.Max(level.in, level.directIn*keep)
			q.CapOut = math.Max(level.out, level.directOut*keep)
		}
	}
	return q, nil
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
func FetchSurplusTrustedBook(ctx context.Context, marketsURL, model string, depth int) (TrustedQuote, error) {
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
	return ParseSurplusTrustedBook(resp.Body, depth)
}
