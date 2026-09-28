package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type fakeChains struct {
	chains []store.Chain
	err    error
}

func (f *fakeChains) ListByTenant(_ context.Context, _ string) ([]store.Chain, error) {
	return f.chains, f.err
}

type fakeAliases struct {
	alias store.ModelAlias
	err   error
}

func (f *fakeAliases) Get(_ context.Context, _ string) (store.ModelAlias, error) {
	return f.alias, f.err
}

func TestResolveTargetsEmptyModel(t *testing.T) {
	_, err := resolveTargets(context.Background(), &fakeChains{}, &fakeAliases{}, nil, "t1", "  ")
	if err == nil {
		t.Fatal("expected error for empty model")
	}
	if _, ok := err.(badModelError); !ok {
		t.Fatalf("want badModelError, got %T", err)
	}
}

func TestResolveTargetsProviderModel(t *testing.T) {
	res, err := resolveTargets(context.Background(), &fakeChains{}, &fakeAliases{}, nil, "t1", "openai/gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(res.Targets))
	}
	if res.Targets[0].Model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", res.Targets[0].Model)
	}
	if res.Targets[0].Provider == "" {
		t.Fatal("provider should not be empty")
	}
}

func TestResolveTargetsKeepsExtraSlashes(t *testing.T) {
	// Vendor-namespaced model ids keep the remaining slashes in the model.
	res, err := resolveTargets(context.Background(), &fakeChains{}, &fakeAliases{}, nil, "t1", "openrouter/anthropic/claude-3.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 1 || res.Targets[0].Model != "anthropic/claude-3.5" {
		t.Fatalf("targets = %+v, want single target model anthropic/claude-3.5", res.Targets)
	}
}

func TestResolveTargetsChainPrefix(t *testing.T) {
	chains := &fakeChains{chains: []store.Chain{{
		ID:       "c1",
		Name:     "fast",
		Strategy: "round_robin",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
			{Position: 1, Provider: "anthropic", Model: "claude-3.5"},
		},
		FallbackProvider: "gemini",
		FallbackModel:    "gemini-2.0",
	}}}

	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, nil, "t1", "chain:fast")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 steps + 1 fallback target.
	if len(res.Targets) != 3 {
		t.Fatalf("got %d targets, want 3: %+v", len(res.Targets), res.Targets)
	}
	if res.Targets[2].Provider != "gemini" || res.Targets[2].Model != "gemini-2.0" {
		t.Fatalf("fallback target = %+v, want gemini/gemini-2.0", res.Targets[2])
	}
	if res.PlanOpts.ChainID != "c1" {
		t.Fatalf("ChainID = %q, want c1", res.PlanOpts.ChainID)
	}
	if res.PlanOpts.Strategy != dispatch.StrategyRoundRobin {
		t.Fatalf("Strategy = %v, want round-robin", res.PlanOpts.Strategy)
	}
}

func TestResolveTargetsFallbackTargetCarriesChainPrice(t *testing.T) {
	// The explicit last-resort fallback target must be billed at the chain
	// price too, so it carries the four chain rates like the chain steps.
	// Rates are non-zero so a missing copy fails the assertion.
	chains := &fakeChains{chains: []store.Chain{{
		ID:       "c-price",
		Name:     "priced",
		Strategy: "priority",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
		},
		FallbackProvider: "gemini",
		FallbackModel:    "gemini-2.0",
		InputPerM:        2.5,
		OutputPerM:       10,
		CacheWritePerM:   3.125,
		CacheReadPerM:    0.25,
	}}}

	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, nil, "t1", "chain:priced")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(res.Targets), res.Targets)
	}
	last := res.Targets[len(res.Targets)-1]
	if last.Provider != "gemini" || last.Model != "gemini-2.0" {
		t.Fatalf("last target = %+v, want fallback gemini/gemini-2.0", last)
	}
	if last.InputPerM != 2.5 || last.OutputPerM != 10 || last.CacheWritePerM != 3.125 || last.CacheReadPerM != 0.25 {
		t.Fatalf("fallback target rates = %+v, want chain rates 2.5/10/3.125/0.25", last)
	}
}

func TestResolveTargetsChainDefaultStrategyIsFallback(t *testing.T) {
	chains := &fakeChains{chains: []store.Chain{{
		ID:   "c2",
		Name: "safe",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
		},
	}}}
	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, nil, "t1", "chain:safe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PlanOpts.Strategy != dispatch.StrategyFallback {
		t.Fatalf("Strategy = %v, want fallback", res.PlanOpts.Strategy)
	}
}

func TestResolveTargetsBareNameChain(t *testing.T) {
	chains := &fakeChains{chains: []store.Chain{{
		ID:   "c3",
		Name: "myroute",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
		},
	}}}
	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, nil, "t1", "myroute")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 1 || res.Targets[0].Provider != "openai" {
		t.Fatalf("targets = %+v, want openai/gpt-4o", res.Targets)
	}
}

func TestResolveTargetsBareNameAlias(t *testing.T) {
	// No matching chain, but an alias resolves.
	chains := &fakeChains{chains: nil}
	aliases := &fakeAliases{alias: store.ModelAlias{Alias: "gpt", Target: "openai/gpt-4o"}}
	res, err := resolveTargets(context.Background(), chains, aliases, nil, "t1", "gpt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 1 || res.Targets[0].Model != "gpt-4o" {
		t.Fatalf("targets = %+v, want openai/gpt-4o", res.Targets)
	}
}

func TestResolveTargetsUnresolvable(t *testing.T) {
	chains := &fakeChains{chains: nil}
	aliases := &fakeAliases{err: errors.New("not found")}
	_, err := resolveTargets(context.Background(), chains, aliases, nil, "t1", "mystery")
	if err == nil {
		t.Fatal("expected error for unresolvable bare name")
	}
	if _, ok := err.(badModelError); !ok {
		t.Fatalf("want badModelError, got %T", err)
	}
}

func TestResolveTargetsChainNotFound(t *testing.T) {
	chains := &fakeChains{chains: []store.Chain{{ID: "c1", Name: "other"}}}
	_, err := resolveTargets(context.Background(), chains, &fakeAliases{}, nil, "t1", "chain:missing")
	if err == nil {
		t.Fatal("expected error for missing chain")
	}
}

// fakeLatency reports canned average latencies keyed by "provider/model".
type fakeLatency struct{ ms map[string]int }

func (f fakeLatency) AvgLatencyMS(_ context.Context, _ string, targets []dispatch.Target) map[string]int {
	out := make(map[string]int)
	for _, t := range targets {
		if v, ok := f.ms[t.Provider+"/"+t.Model]; ok {
			out[t.Provider+"/"+t.Model] = v
		}
	}
	return out
}

func TestResolveTargetsCostStrategyOrdersCheapestFirst(t *testing.T) {
	// deepseek-chat (0.14 + 0.28) is far cheaper than anthropic claude / openai
	// gpt-4o, so the cost strategy must float it to the front while the explicit
	// fallback model stays last.
	chains := &fakeChains{chains: []store.Chain{{
		ID:       "c-cost",
		Name:     "thrifty",
		Strategy: "cost",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
			{Position: 1, Provider: "deepseek", Model: "deepseek-chat"},
		},
		FallbackProvider: "openai",
		FallbackModel:    "gpt-4o",
	}}}

	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, nil, "t1", "chain:thrifty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PlanOpts.Strategy != dispatch.StrategyFallback {
		t.Fatalf("Strategy = %v, want fallback", res.PlanOpts.Strategy)
	}
	if len(res.Targets) != 3 {
		t.Fatalf("got %d targets, want 3: %+v", len(res.Targets), res.Targets)
	}
	if res.Targets[0].Provider != "deepseek" {
		t.Fatalf("cheapest first = %+v, want deepseek step leading", res.Targets[0])
	}
	// The explicit fallback model remains the last resort.
	if res.Targets[2].Provider != "openai" || res.Targets[2].Model != "gpt-4o" {
		t.Fatalf("last target = %+v, want fallback openai/gpt-4o", res.Targets[2])
	}
}

func TestResolveTargetsLatencyStrategyOrdersFastestFirst(t *testing.T) {
	chains := &fakeChains{chains: []store.Chain{{
		ID:       "c-lat",
		Name:     "snappy",
		Strategy: "latency",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
			{Position: 1, Provider: "anthropic", Model: "claude-3.5"},
		},
	}}}
	latency := fakeLatency{ms: map[string]int{
		"openai/gpt-4o":        900,
		"anthropic/claude-3.5": 120,
	}}

	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, latency, "t1", "chain:snappy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(res.Targets), res.Targets)
	}
	if res.Targets[0].Provider != "anthropic" {
		t.Fatalf("fastest first = %+v, want anthropic leading", res.Targets[0])
	}
}

func TestResolveTargetsLatencyStrategyKeepsOrderWithoutData(t *testing.T) {
	// With no measurements yet, the declared order is preserved so routing stays
	// predictable before the background health probe has collected samples.
	chains := &fakeChains{chains: []store.Chain{{
		ID:       "c-lat2",
		Name:     "coldstart",
		Strategy: "latency",
		Steps: []store.ChainStep{
			{Position: 0, Provider: "openai", Model: "gpt-4o"},
			{Position: 1, Provider: "anthropic", Model: "claude-3.5"},
		},
	}}}

	res, err := resolveTargets(context.Background(), chains, &fakeAliases{}, fakeLatency{}, "t1", "chain:coldstart")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Targets[0].Provider != "openai" {
		t.Fatalf("declared order not preserved: %+v", res.Targets)
	}
}
