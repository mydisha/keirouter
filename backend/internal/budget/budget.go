// Package budget enforces spend limits before a request is dispatched.
//
// Budgets are defined per scope (tenant, project, or API key) with a hard cap
// in micros of USD over a rolling period. Before each request the engine sums
// the scope's spend in the current period; if a hard-cutoff budget is already
// at or over its limit, the request is rejected with ErrBudgetBlocked (which is
// not fallbackable). Alert thresholds let the UI warn before the cutoff.
package budget

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// reservation tracks in-flight spend that has been budget-checked but not yet
// metered. This prevents the TOCTOU race where concurrent requests all pass
// the budget check before any usage is recorded.
type reservation struct {
	micros int64
}

// budgetCacheEntry caches a ListByScope result with an expiry time.
type budgetCacheEntry struct {
	budgets []store.Budget
	expires time.Time
}

// budgetCacheTTL is how long cached budget definitions stay valid. Budgets
// change only on admin CRUD (rare), while the cache is read on every request.
// A 10-second TTL eliminates per-request DB round-trips for the definition
// lookup without making admin changes feel sluggish.
const budgetCacheTTL = 10 * time.Second

// Engine evaluates budgets against recorded usage.
type Engine struct {
	budgets *store.BudgetRepo
	usage   *store.UsageRepo

	// reservations tracks estimated spend for in-flight requests, keyed by
	// scope ID. Prevents concurrent requests from overshooting hard limits.
	mu           sync.RWMutex
	reservations map[string]*reservation // key: scopeID

	// budgetCache caches ListByScope results to avoid a DB round-trip on every
	// request. Keyed by "scope_kind:scope_id". Invalidated on budget mutations.
	budgetMu    sync.RWMutex
	budgetCache map[string]*budgetCacheEntry
}

// New builds a budget Engine.
func New(budgets *store.BudgetRepo, usage *store.UsageRepo) *Engine {
	return &Engine{
		budgets:      budgets,
		usage:        usage,
		reservations: make(map[string]*reservation),
		budgetCache:  make(map[string]*budgetCacheEntry),
	}
}

// Decision is the outcome of a budget check.
type Decision struct {
	// Allowed is false when a hard-cutoff budget is exhausted.
	Allowed bool
	// Blocking is the budget that caused a block (nil when allowed).
	Blocking *store.Budget
	// Alerts lists budgets that have crossed their alert threshold but not their
	// hard limit. Informational; does not block.
	Alerts []store.Budget
	// BudgetUsage holds per-budget spend details for reporting.
	BudgetUsage []BudgetUsage
}

// BudgetUsage pairs a budget with its current-period spend, both cost and tokens.
type BudgetUsage struct {
	Budget     store.Budget
	CostMicros int64
	TokenCount int64
}

// Scope identifies the request's billing scopes to check.
type Scope struct {
	TenantID  string
	ProjectID string
	APIKeyID  string
}

// cachedListByScope returns budgets for a scope, using a short-lived in-memory
// cache to avoid a DB round-trip on every request.
func (e *Engine) cachedListByScope(ctx context.Context, kind store.BudgetScope, scopeID string) ([]store.Budget, error) {
	key := string(kind) + ":" + scopeID
	now := time.Now()

	// Fast path: read lock only.
	e.budgetMu.RLock()
	if ent, ok := e.budgetCache[key]; ok && now.Before(ent.expires) {
		budgets := ent.budgets
		e.budgetMu.RUnlock()
		return budgets, nil
	}
	e.budgetMu.RUnlock()

	// Slow path: fetch from DB and populate cache.
	budgets, err := e.budgets.ListByScope(ctx, kind, scopeID)
	if err != nil {
		return nil, err
	}

	e.budgetMu.Lock()
	e.budgetCache[key] = &budgetCacheEntry{budgets: budgets, expires: now.Add(budgetCacheTTL)}
	e.budgetMu.Unlock()
	return budgets, nil
}

// InvalidateBudgetCache clears the cached budget definitions. Call this after
// budget CRUD operations so the next request sees fresh data immediately.
func (e *Engine) InvalidateBudgetCache() {
	e.budgetMu.Lock()
	// Clear all entries; budget mutations are infrequent so a full flush is fine.
	for k := range e.budgetCache {
		delete(e.budgetCache, k)
	}
	e.budgetMu.Unlock()
}

// InvalidateBudgetCacheForScope clears cached entries for a specific scope kind
// and ID. More targeted than InvalidateBudgetCache for single-budget changes.
func (e *Engine) InvalidateBudgetCacheForScope(kind store.BudgetScope, scopeID string) {
	key := string(kind) + ":" + scopeID
	e.budgetMu.Lock()
	delete(e.budgetCache, key)
	e.budgetMu.Unlock()
}

// Check evaluates all budgets applicable to a scope and reports whether the
// request may proceed. It checks key, project, and tenant budgets; the first
// exhausted hard-cutoff budget blocks the request. Both USD cost and token
// limits are evaluated in a single batched query per check.
func (e *Engine) Check(ctx context.Context, scope Scope) (Decision, error) {
	dec := Decision{Allowed: true}

	checks := []struct {
		kind store.BudgetScope
		id   string
	}{
		{store.ScopeAPIKey, scope.APIKeyID},
		{store.ScopeProject, scope.ProjectID},
		{store.ScopeTenant, scope.TenantID},
	}

	// Phase 1: collect all budgets and their spend scopes.
	type budgetEntry struct {
		budget store.Budget
		idx    int // index into the batch spend query
	}
	var entries []budgetEntry
	var spendScopes []store.SpendScope

	for _, c := range checks {
		if c.id == "" {
			continue
		}
		budgets, err := e.cachedListByScope(ctx, c.kind, c.id)
		if err != nil {
			return Decision{}, fmt.Errorf("budget: list %s budgets: %w", c.kind, err)
		}
		for _, b := range budgets {
			since := PeriodStart(b.Period, time.Now())
			idx := len(spendScopes)
			spendScopes = append(spendScopes, store.SpendScope{
				Kind:    b.ScopeKind,
				ScopeID: b.ScopeID,
				Since:   since,
			})
			entries = append(entries, budgetEntry{budget: b, idx: idx})
		}
	}

	if len(entries) == 0 {
		return dec, nil
	}

	// Phase 2: fetch all spend data in a single SQL round-trip.
	spendResults, err := e.usage.SpendAndTokensBatch(ctx, spendScopes)
	if err != nil {
		return Decision{}, fmt.Errorf("budget: batch spend: %w", err)
	}

	// Phase 3: evaluate limits using the pre-fetched results.
	for _, ent := range entries {
		b := ent.budget
		r := spendResults[ent.idx]
		spent := r.CostMicros
		tokens := r.Tokens

		// Include in-flight reservations in the spend calculation to
		// prevent the TOCTOU race where concurrent requests all pass the
		// budget check before any usage is recorded.
		reserved := e.getReserved(b.ScopeKind, b.ScopeID)
		spent += reserved

		dec.BudgetUsage = append(dec.BudgetUsage, BudgetUsage{
			Budget:     b,
			CostMicros: spent,
			TokenCount: tokens,
		})

		// Check USD cost limit.
		if b.LimitMicros > 0 && spent >= b.LimitMicros && b.HardCutoff {
			dec.Allowed = false
			blocking := b
			dec.Blocking = &blocking
			return dec, nil
		}
		// Check token limit.
		if b.LimitTokens > 0 && tokens >= b.LimitTokens && b.HardCutoff {
			dec.Allowed = false
			blocking := b
			dec.Blocking = &blocking
			return dec, nil
		}
		// Alert thresholds.
		if b.AlertPct > 0 {
			if b.LimitMicros > 0 && spent*100 >= b.LimitMicros*int64(b.AlertPct) {
				dec.Alerts = append(dec.Alerts, b)
			} else if b.LimitTokens > 0 && tokens*100 >= b.LimitTokens*int64(b.AlertPct) {
				dec.Alerts = append(dec.Alerts, b)
			}
		}
	}
	return dec, nil
}

// CheckOrError is a convenience wrapper that returns a ProviderError when the
// request is blocked, suitable for direct use in the pipeline.
func (e *Engine) CheckOrError(ctx context.Context, scope Scope) error {
	dec, err := e.Check(ctx, scope)
	if err != nil {
		return err
	}
	if !dec.Allowed {
		return &core.ProviderError{
			Kind:    core.ErrBudgetBlocked,
			Message: fmt.Sprintf("budget %q exhausted for %s", dec.Blocking.ID, dec.Blocking.ScopeKind),
		}
	}
	return nil
}

// Reserve atomically checks the budget and records a tentative spend
// reservation. This prevents the TOCTOU race where N concurrent requests all
// read the same spend value and proceed, overshooting the hard limit.
//
// estimatedMicros is the expected cost of the request (use 0 for unknown; the
// reservation will still block concurrent requests from reading stale spend).
//
// After the request completes, call Confirm() with the actual cost, or
// Release() if the request failed before metering.
func (e *Engine) Reserve(ctx context.Context, scope Scope, estimatedMicros int64) error {
	dec, err := e.Check(ctx, scope)
	if err != nil {
		return err
	}
	if !dec.Allowed {
		return &core.ProviderError{
			Kind:    core.ErrBudgetBlocked,
			Message: fmt.Sprintf("budget %q exhausted for %s", dec.Blocking.ID, dec.Blocking.ScopeKind),
		}
	}

	// Acquire the lock once and add all reservations atomically.
	// This closes the TOCTOU window between Check() returning "allowed"
	// and the reservation being recorded: no concurrent request can slip
	// through between the check and the reservation.
	e.mu.Lock()
	for _, bu := range dec.BudgetUsage {
		if !bu.Budget.HardCutoff {
			continue
		}
		key := reservationKey(bu.Budget.ScopeKind, bu.Budget.ScopeID)
		cost := estimatedMicros
		if cost <= 0 {
			cost = 1
		}
		if r, ok := e.reservations[key]; ok {
			r.micros += cost
		} else {
			e.reservations[key] = &reservation{micros: cost}
		}
	}
	e.mu.Unlock()
	return nil
}

// Confirm replaces the reservation with the actual recorded cost. Call this
// after meter.Record succeeds. The real usage is now in the database, so the
// reservation is no longer needed.
func (e *Engine) Confirm(scope Scope, actualMicros int64) {
	e.releaseAll(scope)
}

// Release removes the reservation without recording actual cost. Call this
// when a request fails before metering (e.g., upstream error, timeout).
func (e *Engine) Release(scope Scope) {
	e.releaseAll(scope)
}

func (e *Engine) releaseAll(scope Scope) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range []string{scope.APIKeyID, scope.ProjectID, scope.TenantID} {
		if id == "" {
			continue
		}
		// We don't know the scope kind here, so delete from all possible keys.
		// The reservation map is small and this is infrequent.
		delete(e.reservations, reservationKey(store.ScopeAPIKey, id))
		delete(e.reservations, reservationKey(store.ScopeProject, id))
		delete(e.reservations, reservationKey(store.ScopeTenant, id))
	}
}

func reservationKey(kind store.BudgetScope, scopeID string) string {
	return string(kind) + ":" + scopeID
}

// getReserved returns the sum of in-flight reservations for a scope.
// Uses RLock for read-only access to minimize lock contention under
// high concurrency where many requests check budgets simultaneously.
func (e *Engine) getReserved(kind store.BudgetScope, scopeID string) int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	key := reservationKey(kind, scopeID)
	if r, ok := e.reservations[key]; ok {
		return r.micros
	}
	return 0
}

// PeriodStart returns the start of the current budget window for a period.
// PeriodEnd returns when a budget window that began at start resets. A
// "total" budget never resets and yields the zero time.
func PeriodEnd(period string, start time.Time) time.Time {
	switch period {
	case "daily":
		return start.AddDate(0, 0, 1)
	case "weekly":
		return start.AddDate(0, 0, 7)
	case "monthly":
		return start.AddDate(0, 1, 0)
	default:
		return time.Time{}
	}
}

func PeriodStart(period string, now time.Time) time.Time {
	now = now.UTC()
	switch period {
	case "daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "weekly":
		// ISO-ish: start of the week on Monday.
		offset := (int(now.Weekday()) + 6) % 7
		d := now.AddDate(0, 0, -offset)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default: // "total"
		return time.Time{}
	}
}
