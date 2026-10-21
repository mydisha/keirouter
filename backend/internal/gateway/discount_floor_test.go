package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func trustedStep(model string, mult float64, systemPct int) dispatch.Target {
	return dispatch.Target{
		Provider: "surplus", Model: model, MarketSlug: market.SurplusTrustedPrefix + model,
		BillUpstreamCost: true, UpstreamCostMult: mult, MinDiscountPct: systemPct,
	}
}

func TestUpstreamDiscountForFloor(t *testing.T) {
	cases := []struct {
		name  string
		floor int
		mult  float64
		want  int
	}{
		// User pays upstream × 1.2; paying ≤ 70% of list needs upstream ≤ 58.33%.
		{"markup 20, floor 30", 30, 1.2, 42},
		{"no markup", 30, 1.0, 30},
		{"exact percent stays exact", 50, 1.25, 60},
		{"unset multiplier counts as no markup", 30, 0, 30},
		{"floor 0 is off", 0, 1.2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, upstreamDiscountForFloor(tc.floor, tc.mult))
		})
	}
}

// What the user pays must stay at or below (100-floor)% of list for any
// upstream price Surplus may serve under the header we send.
func TestUpstreamDiscountForFloorKeepsUserPriceUnderFloor(t *testing.T) {
	for floor := 1; floor <= 95; floor++ {
		for _, mult := range []float64{1, 1.05, 1.1, 1.15, 1.2, 1.3, 1.5} {
			n := upstreamDiscountForFloor(floor, mult)
			require.LessOrEqual(t, n, 100)
			userPays := (1 - float64(n)/100) * mult
			require.LessOrEqualf(t, userPays, 1-float64(floor)/100+1e-9, "floor %d mult %v -> %d", floor, mult, n)
		}
	}
}

func TestApplyDiscountFloorRaisesHeaderAndKeepsBilling(t *testing.T) {
	in := []dispatch.Target{trustedStep("glm", 1.2, 10), trustedStep("kimi", 1.2, 60)}

	out, err := applyDiscountFloor(in, 30, "chain-x")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, 42, out[0].MinDiscountPct, "user floor is stricter than the system one")
	require.Equal(t, 60, out[1].MinDiscountPct, "system floor is stricter and stays")
	// The floor only refuses requests; the bill is still upstream cost × markup.
	require.True(t, out[0].BillUpstreamCost)
	require.Equal(t, 1.2, out[0].UpstreamCostMult)
	require.Equal(t, 10, in[0].MinDiscountPct, "input targets are not mutated")
}

func TestApplyDiscountFloorDropsStepsThatCannotProveDiscount(t *testing.T) {
	in := []dispatch.Target{
		{Provider: "inferhub", Model: "glm", MarketSlug: "inferhub:glm"},
		trustedStep("glm", 1.2, 0),
		{Provider: "surplus", Model: "glm", MarketSlug: market.SurplusPrefix + "glm"},
		{Provider: "openai", Model: "gpt-4o"}, // chain fallback model
	}
	out, err := applyDiscountFloor(in, 30, "chain-x")
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, market.SurplusTrustedPrefix+"glm", out[0].MarketSlug)
}

func TestApplyDiscountFloorRejectsWhenNothingQualifies(t *testing.T) {
	in := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}
	_, err := applyDiscountFloor(in, 30, "chain-x")
	var bad badModelError
	require.ErrorAs(t, err, &bad)
	require.Equal(t, http.StatusUnprocessableEntity, bad.HTTPStatus())
	require.Contains(t, bad.Error(), "30%")
}

func TestApplyDiscountFloorOffIsNoop(t *testing.T) {
	in := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}
	out, err := applyDiscountFloor(in, 0, "chain-x")
	require.NoError(t, err)
	require.Equal(t, in, out)
}

// The authenticated key's floor reaches the resolved route: the trusted step
// gets the stricter header and the chain's other steps are dropped.
func TestServerResolveTargetsAppliesKeyDiscountFloor(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	markup := 20.0
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "glm", TenantID: store.DefaultTenantID, Name: "glm", Strategy: "priority", MarkupPercent: &markup,
		Steps: []store.ChainStep{
			{ID: "s1", Provider: "custom-openai-surplus", Model: "glm-5.3", MarketSlug: "surplus-trusted:glm-5.3", Position: 0},
			{ID: "s2", Provider: "openai", Model: "gpt-4o", Position: 1},
		},
		CreatedAt: now, UpdatedAt: now,
	}))
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 1, BestOut: 2, CapIn: 3, CapOut: 4, MinDiscountPct: 10, Providers: []string{"zai"},
	}.Model("glm-5.3")})
	gw.marketCache = cache

	res, err := gw.resolveTargets(ctx, store.DefaultTenantID, "glm")
	require.NoError(t, err)
	require.Len(t, res.Targets, 2, "no key in context: route unchanged")

	keyed := context.WithValue(ctx, apiKeyCtxKey, store.APIKey{ID: "k", MinDiscountPct: 30})
	res, err = gw.resolveTargets(keyed, store.DefaultTenantID, "glm")
	require.NoError(t, err)
	require.Len(t, res.Targets, 1)
	require.Equal(t, 42, res.Targets[0].MinDiscountPct)
	require.InDelta(t, 1.2, res.Targets[0].UpstreamCostMult, 1e-9, "bill stays upstream cost × markup")
}

func TestServerResolveTargetsUsesPerModelDiscount(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	markup := 20.0
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "glm", TenantID: store.DefaultTenantID, Name: "glm", Strategy: "priority", MarkupPercent: &markup,
		Steps: []store.ChainStep{
			{ID: "s1", Provider: "custom-openai-surplus", Model: "glm-5.3", MarketSlug: "surplus-trusted:glm-5.3", Position: 0},
			{ID: "s2", Provider: "openai", Model: "gpt-4o", Position: 1},
		},
		CreatedAt: now, UpdatedAt: now,
	}))
	cache := market.NewSnapshotCache()
	cache.Replace([]market.Model{market.TrustedQuote{
		Found: true, BestIn: 1, BestOut: 2, CapIn: 3, CapOut: 4, MinDiscountPct: 10, Providers: []string{"zai"},
	}.Model("glm-5.3")})
	gw.marketCache = cache

	// The model override (30%) wins over the key default (60%).
	key := store.APIKey{ID: "k", MinDiscountPct: 60, ModelDiscounts: map[string]int{"glm": 30}}
	res, err := gw.resolveTargets(context.WithValue(ctx, apiKeyCtxKey, key), store.DefaultTenantID, "chain:glm")
	require.NoError(t, err)
	require.Len(t, res.Targets, 1)
	require.Equal(t, 42, res.Targets[0].MinDiscountPct)

	// An override of 0 turns the default off: the full chain routes again.
	key.ModelDiscounts = map[string]int{"glm": 0}
	res, err = gw.resolveTargets(context.WithValue(ctx, apiKeyCtxKey, key), store.DefaultTenantID, "glm")
	require.NoError(t, err)
	require.Len(t, res.Targets, 2)
}

// A chain with no step that can guarantee a discount is exempt from the
// customer's minimum: it routes at its set price instead of being refused.
// A trusted chain whose quote is momentarily missing still enforces it.
func TestServerResolveTargetsExemptsChainsWithoutDiscountGuarantee(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "plain", TenantID: store.DefaultTenantID, Name: "plain", Strategy: "priority", InputPerM: 1, OutputPerM: 2,
		Steps:     []store.ChainStep{{ID: "p1", Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "glm", TenantID: store.DefaultTenantID, Name: "glm", Strategy: "priority",
		FallbackProvider: "openai", FallbackModel: "gpt-4o",
		Steps:     []store.ChainStep{{ID: "g1", Provider: "custom-openai-surplus", Model: "glm-5.3", MarketSlug: "surplus-trusted:glm-5.3", Position: 0}},
		CreatedAt: now, UpdatedAt: now,
	}))
	gw.marketCache = market.NewSnapshotCache() // no live quotes at all

	keyed := context.WithValue(ctx, apiKeyCtxKey, store.APIKey{ID: "k", MinDiscountPct: 50})
	res, err := gw.resolveTargets(keyed, store.DefaultTenantID, "plain")
	require.NoError(t, err, "no guarantee: the minimum does not apply")
	require.Len(t, res.Targets, 1)
	require.Equal(t, 0, res.Targets[0].MinDiscountPct)

	_, err = gw.resolveTargets(keyed, store.DefaultTenantID, "glm")
	require.Error(t, err, "trusted chain with no live quote must not fall through to its fallback")
	bad, ok := err.(badModelError)
	require.True(t, ok)
	require.Equal(t, http.StatusUnprocessableEntity, bad.status)
}
