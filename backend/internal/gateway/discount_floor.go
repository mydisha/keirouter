package gateway

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/market"
)

// maxDiscountFloorPct caps the floor a customer may set. Higher floors match
// no offer, so they would only turn every request into an error.
const maxDiscountFloorPct = 95

// upstreamDiscountForFloor converts a customer's discount floor (on the price
// they pay, markup included) into the discount Surplus must find on its own
// price: the user pays upstream × mult, so paying at most (1-floor) of list
// needs upstream at most (1-floor)/mult of list. It rounds up so the user's
// price never ends above their floor. A multiplier at or below 1 counts as
// no markup, which is stricter.
func upstreamDiscountForFloor(floorPct int, mult float64) int {
	if floorPct <= 0 {
		return 0
	}
	if mult < 1 {
		mult = 1
	}
	keep := (1 - float64(floorPct)/100) / mult
	// The epsilon keeps exact percentages exact: 1-0.5/1.25 = 0.6000000001.
	return int(math.Ceil((1-keep)*100 - 1e-9))
}

// applyDiscountFloor keeps only the chain steps that can guarantee the
// customer's floor: surplus-trusted steps, which Surplus refuses (without
// charging) when no offer is at least X-Min-Discount below list. Other steps
// and the chain fallback cannot prove a discount and are dropped. The header
// becomes the stricter of the system ceiling and the customer's floor. Billing
// is untouched: the bill stays upstream cost × markup, so a floor never makes
// a request cost less than what Surplus charges.
func applyDiscountFloor(targets []dispatch.Target, floorPct int, model string) ([]dispatch.Target, error) {
	if floorPct <= 0 {
		return targets, nil
	}
	out := make([]dispatch.Target, 0, len(targets))
	for _, t := range targets {
		if !t.BillUpstreamCost || !strings.HasPrefix(t.MarketSlug, market.SurplusTrustedPrefix) {
			continue
		}
		t.MinDiscountPct = max(t.MinDiscountPct, upstreamDiscountForFloor(floorPct, t.UpstreamCostMult))
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, errDiscountFloor(model, floorPct)
	}
	return out, nil
}

func errDiscountFloor(model string, floorPct int) error {
	return badModelError{
		msg: fmt.Sprintf("model %s cannot meet your %d%% minimum discount: no route for it can guarantee that price. "+
			"Lower the discount floor in the portal or use another model", model, floorPct),
		status: http.StatusUnprocessableEntity,
	}
}
