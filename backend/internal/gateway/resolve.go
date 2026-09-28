package gateway

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
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
	model = strings.TrimSpace(model)
	if model == "" {
		return resolveResult{}, errBadModel("model is required")
	}

	// chain:<name>
	if name, ok := strings.CutPrefix(model, "chain:"); ok {
		return chainResult(ctx, chains, latency, tenantID, name)
	}

	// provider/model — resolve provider alias (e.g. "mmtp" -> "xiaomi-tokenplan").
	if provider, rest, ok := strings.Cut(model, "/"); ok && provider != "" && rest != "" {
		if spec, ok := connectors.SpecByAlias(provider); ok {
			provider = spec.ID
		}
		return resolveResult{Targets: []dispatch.Target{{Provider: provider, Model: rest}}}, nil
	}

	// bare name -> try a chain first
	res, err := chainResult(ctx, chains, latency, tenantID, model)
	if err == nil {
		return res, nil
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
			case chainStrategyLatency:
				steps = orderStepsByLatency(ctx, latency, tenantID, steps)
				opts.Strategy = dispatch.StrategyFallback
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

// badModelError signals an unresolvable model string (a client error).
type badModelError struct{ msg string }

func (e badModelError) Error() string { return e.msg }

func errBadModel(msg string) error { return badModelError{msg: msg} }

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
