package gateway

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/market"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Chain ordering strategy tokens (persisted on store.Chain.Strategy). "priority"
// is the implicit default (declared order); "round_robin" is handled via the
// dispatcher's rotation. "cost" and "latency" reorder the steps here.
const (
	chainStrategyCost    = "cost"
	chainStrategyLatency = "latency"
)

// resolveResult carries both the targets and the strategy metadata needed
// by the pipeline to apply round-robin or other rotation strategies.
type resolveResult struct {
	Targets  []dispatch.Target
	PlanOpts dispatch.PlanOptions
}

// ChainSource resolves a named chain for a tenant.
type ChainSource interface {
	ListByTenant(ctx context.Context, tenantID string) ([]store.Chain, error)
}

// AliasSource resolves a model alias to a provider/model target.
type AliasSource interface {
	Get(ctx context.Context, alias string) (store.ModelAlias, error)
}

// LatencyReader reports the average observed latency (ms) for the given
// provider/model targets within a tenant, keyed by "provider/model". Pairs with
// no measurement are omitted. The latency chain strategy uses it to order steps
// fastest-first; a nil reader (or an unmeasured pair) leaves the declared order.
type LatencyReader interface {
	AvgLatencyMS(ctx context.Context, tenantID string, targets []dispatch.Target) map[string]int
}

// resolveTargets turns an inbound model string into an ordered fallback chain.
//
// Four forms are supported, in priority order:
//   - "provider/model"  -> a single explicit target (e.g. "openai/gpt-4o").
//     Slashes beyond the first are kept in the model id so vendor-namespaced
//     ids like "anthropic/claude-3.5" via openrouter still work.
//   - "chain:name"       -> the named routing chain's steps.
//   - bare "name"        -> resolved as a chain named "name" if one exists,
//     then as a model alias. A bare name is never assumed to be a provider
//     model; routing stays explicit and predictable.
//
// When a chain is resolved, the returned resolveResult carries the chain ID
// and strategy so the dispatcher can apply round-robin rotation.
func resolveTargets(ctx context.Context, chains ChainSource, aliases AliasSource, latency LatencyReader, tenantID, model string) (resolveResult, error) {
	return resolveTargetsWith(ctx, chains, aliases, latency, nil, 0, tenantID, model)
}

// resolveTargets is the Server-bound entry point. It supplies the live market
// snapshot cache and the configured markup so chains with ReorderByMarket set
// are ordered by their bound slugs' current rates.
func (s *Server) resolveTargets(ctx context.Context, tenantID, model string) (resolveResult, error) {
	var markup float64
	if s.settings != nil {
		markup = market.LoadSettings(ctx, s.settings.Get).MarkupPercent
	}
	return resolveTargetsWith(ctx, s.chains, s.aliases, s.latencyReader(), s.marketCache, markup, tenantID, model)
}

func resolveTargetsWith(ctx context.Context, chains ChainSource, aliases AliasSource, latency LatencyReader, cache *market.SnapshotCache, markupPercent float64, tenantID, model string) (resolveResult, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return resolveResult{}, errBadModel("model is required")
	}

	// chain:<name>
	if name, ok := strings.CutPrefix(model, "chain:"); ok {
		return chainResultWith(ctx, chains, latency, cache, markupPercent, tenantID, name)
	}

	// provider/model — resolve provider alias (e.g. "mmtp" -> "xiaomi-tokenplan").
	if provider, rest, ok := strings.Cut(model, "/"); ok && provider != "" && rest != "" {
		if spec, ok := connectors.SpecByAlias(provider); ok {
			provider = spec.ID
		}
		return resolveResult{Targets: []dispatch.Target{{Provider: provider, Model: rest}}}, nil
	}

	// bare name -> try a chain first
	res, err := chainResultWith(ctx, chains, latency, cache, markupPercent, tenantID, model)
	if err == nil {
		return res, nil
	}
	// The chain exists but is sold out: say so rather than falling through to
	// "unknown model".
	if bad, ok := err.(badModelError); ok && bad.status == http.StatusServiceUnavailable {
		return resolveResult{}, err
	}

	// bare name -> try an alias
	if aliases != nil {
		alias, aerr := aliases.Get(ctx, model)
		if aerr == nil && alias.Target != "" {
			if provider, rest, ok := strings.Cut(alias.Target, "/"); ok && provider != "" && rest != "" {
				return resolveResult{Targets: []dispatch.Target{{Provider: provider, Model: rest}}}, nil
			}
		}
	}

	return resolveResult{}, errBadModel("model must be 'provider/model', a chain name, or an alias: " + model)
}

// chainResult resolves a chain by name and extracts its strategy metadata.
func chainResult(ctx context.Context, chains ChainSource, latency LatencyReader, tenantID, name string) (resolveResult, error) {
	return chainResultWith(ctx, chains, latency, nil, 0, tenantID, name)
}

func chainResultWith(ctx context.Context, chains ChainSource, latency LatencyReader, cache *market.SnapshotCache, markupPercent float64, tenantID, name string) (resolveResult, error) {
	list, err := chains.ListByTenant(ctx, tenantID)
	if err != nil {
		return resolveResult{}, err
	}
	for _, c := range list {
		if c.Name == name {
			steps := dispatch.TargetsFromChain(c)
			if len(steps) == 0 {
				return resolveResult{}, errBadModel("chain has no steps: " + name)
			}

			opts := dispatch.PlanOptions{ChainID: c.ID}
			markupPercent := market.EffectiveMarkup(c.MarkupPercent, markupPercent)
			// A chain marked ReorderByMarket is ordered cheapest-first by the
			// live rate of each step's bound slug. Unresolvable/slugless steps
			// sink last. Runs before the declared strategy switch so it
			// composes with the fallback/round-robin handling below.
			if c.ReorderByMarket {
				steps = orderStepsByMarket(steps, cache, markupPercent)
			}
			steps = stampSurplusTrusted(steps, c.MarketSlugs, cache, markupPercent)
			if len(steps) == 0 && (c.FallbackProvider == "" || c.FallbackModel == "") {
				return resolveResult{}, errSoldOut(name)
			}
			// Ordering strategies reorder the chain steps before the optional
			// fallback model is appended, so the explicit last-resort target
			// always stays last. round-robin rotates at dispatch time; the
			// other strategies produce a fixed order tried in sequence.
			switch normalizeStrategyToken(c.Strategy) {
			case string(dispatch.StrategyRoundRobin), "round_robin", "roundrobin":
				opts.Strategy = dispatch.StrategyRoundRobin
			case chainStrategyCost:
				steps = orderStepsByCost(steps)
				opts.Strategy = dispatch.StrategyFallback
				opts.FixedOrder = true
			case chainStrategyLatency:
				steps = orderStepsByLatency(ctx, latency, tenantID, steps)
				opts.Strategy = dispatch.StrategyFallback
				opts.FixedOrder = true
			default:
				opts.Strategy = dispatch.StrategyFallback
			}

			targets := steps
			// Append fallback model as last-resort target when configured.
			if c.FallbackProvider != "" && c.FallbackModel != "" {
				targets = append(targets, dispatch.Target{
					Provider:       c.FallbackProvider,
					Model:          c.FallbackModel,
					InputPerM:      c.InputPerM,
					OutputPerM:     c.OutputPerM,
					CacheWritePerM: c.CacheWritePerM,
					CacheReadPerM:  c.CacheReadPerM,
				})
				opts.PinnedTail = 1
			}
			return resolveResult{Targets: targets, PlanOpts: opts}, nil
		}
	}
	return resolveResult{}, errBadModel("no chain named " + name)
}

// orderStepsByCost returns the steps ordered cheapest-first by the sum of a
// model's input and output per-million-token rates. The sort is stable, so
// steps with equal (or unknown) cost keep their declared order; models with no
// price entry sink to the end as a last resort.
func orderStepsByCost(steps []dispatch.Target) []dispatch.Target {
	key := func(t dispatch.Target) float64 {
		if mp, ok := connectors.ModelPriceByProviderModel(t.Provider, t.Model); ok {
			return mp.InputPerM + mp.OutputPerM
		}
		return math.MaxFloat64
	}
	out := append([]dispatch.Target(nil), steps...)
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}

// orderStepsByLatency returns the steps ordered fastest-first by average
// observed probe latency. Steps with no measurement (or when no reader is
// available) keep their declared order and sink behind measured steps, so a
// chain still routes predictably before any health data has been collected.
func orderStepsByLatency(ctx context.Context, latency LatencyReader, tenantID string, steps []dispatch.Target) []dispatch.Target {
	if latency == nil {
		return steps
	}
	measured := latency.AvgLatencyMS(ctx, tenantID, steps)
	if len(measured) == 0 {
		return steps
	}
	key := func(t dispatch.Target) int {
		if ms, ok := measured[t.Provider+"/"+t.Model]; ok {
			return ms
		}
		return math.MaxInt
	}
	out := append([]dispatch.Target(nil), steps...)
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}

// orderStepsByMarket orders steps cheapest-first by the live market rate of each
// step's bound slug and stamps the billable (post-markup) rates onto each slugged
// target. Steps without a resolvable slug keep their relative order and sink
// behind priced steps. When no step has a resolvable slug the input order is
// returned unchanged.
func orderStepsByMarket(steps []dispatch.Target, cache *market.SnapshotCache, markupPercent float64) []dispatch.Target {
	out := append([]dispatch.Target(nil), steps...)
	if cache == nil {
		return out
	}
	mult := 1 + markupPercent/100
	type key struct {
		sum float64
		ok  bool
	}
	keyFor := func(t dispatch.Target) key {
		if t.MarketSlug == "" {
			return key{}
		}
		r, ok := cache.Rate(t.MarketSlug)
		if !ok {
			return key{}
		}
		return key{sum: r.InputPerM + r.OutputPerM, ok: true}
	}
	for i := range out {
		if out[i].MarketSlug == "" {
			continue
		}
		r, ok := cache.Rate(out[i].MarketSlug)
		if !ok {
			continue
		}
		out[i].MarketRateIn = r.InputPerM * mult
		out[i].MarketRateOut = r.OutputPerM * mult
		out[i].MarketCacheReadRate = out[i].MarketRateIn * market.CacheReadMult
		out[i].MarketCacheWriteRate = out[i].MarketRateIn * market.CacheWriteMult
		out[i].UpstreamRateIn = r.InputPerM
		out[i].UpstreamRateOut = r.OutputPerM
		out[i].UpstreamCacheReadRate = r.InputPerM * market.CacheReadMult
		out[i].UpstreamCacheWriteRate = r.InputPerM * market.CacheWriteMult
	}
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := keyFor(out[i]), keyFor(out[j])
		if ki.ok != kj.ok {
			return ki.ok // priced before unpriced
		}
		if !ki.ok {
			return false // both unpriced: preserve order
		}
		return ki.sum < kj.sum
	})
	return out
}

// stampSurplusTrusted marks every surplus-trusted step for upstream-cost
// billing. A step without its own slug adopts a chain-level surplus-trusted
// slug naming the same model. The Market*/Upstream* rates carry the X-Min-Discount
// ceiling price (the most Surplus can charge), used only when a response
// reports no cost, so the fallback bill can never undercut a Surplus failover.
// Each step also carries the trusted provider allow-list; a step with no live
// trusted quote is dropped, since without that list Surplus could route the
// request to an untrusted seller.
// It runs after any market reordering so the ceiling overrides cheapest-ask stamps.
func stampSurplusTrusted(steps []dispatch.Target, chainSlugs []string, cache *market.SnapshotCache, markupPercent float64) []dispatch.Target {
	byModel := map[string]string{}
	for _, slug := range chainSlugs {
		if name, ok := strings.CutPrefix(slug, market.SurplusTrustedPrefix); ok && name != "" {
			byModel[name] = slug
		}
	}
	mult := 1 + markupPercent/100
	var out []dispatch.Target
	copied := false
	for i := range steps {
		slug := steps[i].MarketSlug
		if slug == "" {
			slug = byModel[steps[i].Model]
		}
		if !strings.HasPrefix(slug, market.SurplusTrustedPrefix) {
			if copied {
				out = append(out, steps[i])
			}
			continue
		}
		if !copied {
			out = append(make([]dispatch.Target, 0, len(steps)), steps[:i]...)
			copied = true
		}
		var m market.Model
		ok := false
		if cache != nil {
			m, ok = cache.Lookup(slug)
		}
		if !ok || len(m.Providers) == 0 {
			continue
		}
		t := steps[i]
		t.MarketSlug = slug
		t.BillUpstreamCost = true
		t.UpstreamCostMult = mult
		t.MinDiscountPct = m.MinDiscountPct
		t.UpstreamProviders = strings.Join(m.Providers, ",")
		capIn, capOut := m.FailoverIn, m.FailoverOut
		if capIn <= 0 || capOut <= 0 {
			capIn, capOut = m.MinAskIn, m.MinAskOut
		}
		t.UpstreamRateIn, t.UpstreamRateOut = capIn, capOut
		t.UpstreamCacheReadRate = capIn * market.CacheReadMult
		t.UpstreamCacheWriteRate = capIn * market.CacheWriteMult
		t.MarketRateIn, t.MarketRateOut = capIn*mult, capOut*mult
		t.MarketCacheReadRate = t.MarketRateIn * market.CacheReadMult
		t.MarketCacheWriteRate = t.MarketRateIn * market.CacheWriteMult
		out = append(out, t)
	}
	if !copied {
		return steps
	}
	return out
}

// badModelError signals a model string that cannot be routed. status is the
// HTTP status to answer with; zero means 400 (an unknown model is a client
// error).
type badModelError struct {
	msg    string
	status int
}

func (e badModelError) Error() string { return e.msg }

// HTTPStatus is the response status for this error.
func (e badModelError) HTTPStatus() int {
	if e.status == 0 {
		return http.StatusBadRequest
	}
	return e.status
}

func errBadModel(msg string) error { return badModelError{msg: msg} }

// errSoldOut reports a chain whose every market-bound step has no live quote
// (e.g. no trusted Surplus offer clears the price floor). It is temporary, so
// it answers 503 rather than blaming the client.
func errSoldOut(name string) error {
	return badModelError{
		msg:    "model " + name + " is sold out: no offer currently meets its price floor, try again later",
		status: http.StatusServiceUnavailable,
	}
}

// healthLatencyReader adapts the account-health store into a LatencyReader by
// averaging background probe latency across a tenant's accounts for each
// provider/model pair. It reads the health rows once per call, so ordering a
// chain costs a single query regardless of step count.
type healthLatencyReader struct{ health *store.HealthRepo }

func (h healthLatencyReader) AvgLatencyMS(ctx context.Context, tenantID string, targets []dispatch.Target) map[string]int {
	out := make(map[string]int)
	if h.health == nil || len(targets) == 0 {
		return out
	}
	rows, err := h.health.List(ctx, tenantID)
	if err != nil {
		return out
	}
	want := make(map[string]bool, len(targets))
	for _, t := range targets {
		want[t.Provider+"/"+t.Model] = true
	}
	type agg struct{ sum, n int }
	sums := make(map[string]*agg)
	for _, row := range rows {
		k := row.Provider + "/" + row.Model
		if !want[k] || row.LatencyMS <= 0 {
			continue
		}
		a := sums[k]
		if a == nil {
			a = &agg{}
			sums[k] = a
		}
		a.sum += row.LatencyMS
		a.n++
	}
	for k, a := range sums {
		if a.n > 0 {
			out[k] = a.sum / a.n
		}
	}
	return out
}

// latencyReader returns the LatencyReader used by the latency chain strategy.
func (s *Server) latencyReader() LatencyReader { return healthLatencyReader{health: s.health} }
