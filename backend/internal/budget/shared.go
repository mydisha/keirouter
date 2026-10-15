package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// EnableSharedCounter switches spend lookups to c, shared by every replica,
// and starts the worker that feeds it from ObserveUsage. Counters are
// re-seeded from the usage table every ttl. Call before serving traffic and
// Close on shutdown.
//
// Accuracy is the same as the database path, plus two bounded gaps: spend
// recorded while a counter was being (re)seeded can be missed until the next
// seed, and increments are applied asynchronously (typically < 1ms behind).
func (e *Engine) EnableSharedCounter(c Counter, ttl time.Duration, log *slog.Logger) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if log != nil {
		e.log = log
	} else if e.log == nil {
		e.log = slog.Default()
	}
	e.counter = c
	e.counterTTL = ttl
	e.counterPrefix = "kr:bud:"
	e.deltas = make(chan Delta, 8192)
	e.counterDone = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	e.stopCounter = cancel
	c.SubscribeInvalidate(ctx, e.invalidateLocal)
	go e.runDeltas(ctx)
}

// Close stops the shared counter worker after applying queued increments.
func (e *Engine) Close() {
	if e.stopCounter == nil {
		return
	}
	e.stopCounter()
	<-e.counterDone
}

// SharedCounterStats reports shared counter health.
type SharedCounterStats struct {
	Enabled bool  `json:"enabled"`
	Healthy bool  `json:"healthy"`
	Dropped int64 `json:"dropped"`
	Errors  int64 `json:"errors"`
}

// Stats returns shared counter counters.
func (e *Engine) Stats() SharedCounterStats {
	return SharedCounterStats{
		Enabled: e.counter != nil,
		Healthy: e.counter != nil && !e.counterOpen(),
		Dropped: e.deltaDropped.Load(),
		Errors:  e.counterErrors.Load(),
	}
}

// spend returns current-period spend for each scope. With shared counters it
// reads them in one round trip, seeds the missing ones from the usage table,
// and falls back to the table entirely if redis fails.
func (e *Engine) spend(ctx context.Context, scopes []store.SpendScope) ([]store.SpendResult, error) {
	if e.counter == nil {
		res, err := e.usage.SpendAndTokensBatch(ctx, scopes)
		if err != nil {
			return nil, fmt.Errorf("budget: batch spend: %w", err)
		}
		return res, nil
	}

	keys := make([]string, len(scopes))
	for i, s := range scopes {
		keys[i] = counterKey(e.counterPrefix, s.Kind, s.ScopeID, s.Since)
	}
	var vals []CounterValue
	err := errCounterOpen
	if !e.counterOpen() {
		vals, err = e.counter.Load(ctx, keys)
		if err != nil {
			e.tripCounter(err)
		} else if e.counterOpenUntil.Load() != 0 && e.counterOpenUntil.Swap(0) != 0 {
			e.log.Info("budget: shared counters reachable again")
		}
	}
	if err != nil {
		res, err := e.usage.SpendAndTokensBatch(ctx, scopes)
		if err != nil {
			return nil, fmt.Errorf("budget: batch spend: %w", err)
		}
		return res, nil
	}

	out := make([]store.SpendResult, len(scopes))
	var missing []int
	for i, v := range vals {
		if !v.Found {
			missing = append(missing, i)
			continue
		}
		// Same rounding as the SQL path: nanos summed, then to micros.
		out[i] = store.SpendResult{CostMicros: (v.CostNanos + 500) / 1000, Tokens: v.Tokens}
	}
	if len(missing) == 0 {
		return out, nil
	}
	seedScopes := make([]store.SpendScope, len(missing))
	for j, i := range missing {
		seedScopes[j] = scopes[i]
	}
	seeded, err := e.usage.SpendAndTokensBatch(ctx, seedScopes)
	if err != nil {
		return nil, fmt.Errorf("budget: batch spend: %w", err)
	}
	for j, i := range missing {
		out[i] = seeded[j]
		v := CounterValue{CostNanos: seeded[j].CostMicros * 1000, Tokens: seeded[j].Tokens}
		if err := e.counter.Seed(ctx, keys[i], v, e.counterTTL); err != nil {
			e.counterErrors.Add(1)
		}
	}
	return out, nil
}

// counterBreakerCooldown is how long a counter failure bypasses redis.
const counterBreakerCooldown = 5 * time.Second

var errCounterOpen = errors.New("budget: shared counter circuit open")

func (e *Engine) counterOpen() bool {
	until := e.counterOpenUntil.Load()
	return until != 0 && time.Now().UnixNano() < until
}

func (e *Engine) tripCounter(err error) {
	e.counterErrors.Add(1)
	if e.counterOpenUntil.Swap(time.Now().Add(counterBreakerCooldown).UnixNano()) == 0 {
		e.log.Warn("budget: shared counters unavailable, checking spend in the usage table", "retry_in", counterBreakerCooldown, "err", err)
	}
}

// ObserveUsage adds a metered request's cost and tokens to the shared
// counters of every budget covering its key, project and tenant. It never
// blocks: when the queue is full the delta is dropped, and the counter's
// next re-seed picks the spend up from the usage table.
func (e *Engine) ObserveUsage(rec store.UsageRecord) {
	if e.counter == nil || rec.PricingBackfilled {
		return
	}
	tokens := rec.PromptTokens + rec.CompletionTokens
	if rec.CostNanos == 0 && tokens == 0 {
		return
	}
	now := rec.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	for _, sc := range []struct {
		kind store.BudgetScope
		id   string
	}{
		{store.ScopeAPIKey, rec.APIKeyID},
		{store.ScopeProject, rec.ProjectID},
		{store.ScopeTenant, rec.TenantID},
	} {
		if sc.id == "" {
			continue
		}
		budgets, err := e.cachedListByScope(context.Background(), sc.kind, sc.id)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, b := range budgets {
			key := counterKey(e.counterPrefix, b.ScopeKind, b.ScopeID, PeriodStart(b.Period, now))
			if seen[key] {
				continue
			}
			seen[key] = true
			select {
			case e.deltas <- Delta{Key: key, CostNanos: rec.CostNanos, Tokens: int64(tokens)}:
			default:
				e.deltaDropped.Add(1)
			}
		}
	}
}

// runDeltas applies queued increments. Under load it merges every delta that
// is already queued into one pipelined round trip, one write per counter, so
// redis traffic grows with the number of active budgets, not requests.
func (e *Engine) runDeltas(ctx context.Context) {
	defer close(e.counterDone)
	merged := map[string]*Delta{}
	flush := func() {
		if len(merged) == 0 {
			return
		}
		batch := make([]Delta, 0, len(merged))
		for _, d := range merged {
			batch = append(batch, *d)
		}
		clear(merged)
		if e.counterOpen() {
			// The counters re-seed from the usage table once redis is back.
			e.deltaDropped.Add(int64(len(batch)))
			return
		}
		if err := e.counter.Add(context.Background(), batch); err != nil {
			e.tripCounter(err)
		}
	}
	add := func(d Delta) {
		if m, ok := merged[d.Key]; ok {
			m.CostNanos += d.CostNanos
			m.Tokens += d.Tokens
			return
		}
		merged[d.Key] = &d
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case d := <-e.deltas:
					add(d)
				default:
					flush()
					return
				}
			}
		case d := <-e.deltas:
			add(d)
		drain:
			for n := 0; n < 4096 && len(merged) < 512; n++ {
				select {
				case d := <-e.deltas:
					add(d)
				default:
					break drain
				}
			}
			flush()
		}
	}
}
