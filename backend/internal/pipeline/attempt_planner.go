package pipeline

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
)

// Same-attempt retry policy, applied only when no alternative account or
// target remains. With other healthy deployments a
// failure moves to the next one immediately; with a single deployment the
// same one is retried after min(8s, 0.5s·2^n) + jitter, honouring a short
// Retry-After. Without this, a lone transient 502 on a single-account chain
// surfaced straight to the client.
const (
	maxSameAttemptRetries  = 2
	sameRetryInitialDelay  = 500 * time.Millisecond
	sameRetryMaxDelay      = 8 * time.Second
	sameRetryJitter        = 750 * time.Millisecond
	sameRetryMaxRetryAfter = 60 * time.Second
)

// attemptPlanner refreshes routing state after each failed credential while
// preserving the target order chosen for the request's first plan.
type attemptPlanner struct {
	dispatcher *dispatch.Dispatcher
	tenantID   string
	targets    []dispatch.Target
	required   core.CapabilitySet
	options    dispatch.PlanOptions
	affinity   dispatch.PlanOptions
	excluded   map[string]struct{}
	attempted  map[dispatch.AttemptKey]struct{}
	current    dispatch.Attempt
	remaining  bool
	// sameRetries counts last-resort retries of the current attempt.
	sameRetries int
	// sleep is swapped by tests.
	sleep func(ctx context.Context, d time.Duration) bool
}

func newAttemptPlanner(
	dispatcher *dispatch.Dispatcher,
	tenantID string,
	originalTargets []dispatch.Target,
	required core.CapabilitySet,
	options dispatch.PlanOptions,
	initial []dispatch.Attempt,
) *attemptPlanner {
	excluded := make(map[string]struct{}, len(options.ExcludedAccountIDs))
	for id := range options.ExcludedAccountIDs {
		excluded[id] = struct{}{}
	}

	retryOptions := options
	retryOptions.Strategy = dispatch.StrategyFallback
	retryOptions.ChainID = ""
	retryOptions.AccountStrategy = dispatch.StrategyFallback
	retryOptions.AccountAffinityKey = ""
	retryOptions.ExcludedAccountIDs = excluded
	attempted := make(map[dispatch.AttemptKey]struct{}, len(options.ExcludedAttempts))
	for key := range options.ExcludedAttempts {
		attempted[key] = struct{}{}
	}
	retryOptions.ExcludedAttempts = attempted
	// One attempt is consumed per plan, so never prepare credentials for
	// more than one candidate.
	retryOptions.Limit = 1
	if len(options.ProviderAccountStrategies) > 0 {
		retryOptions.ProviderAccountStrategies = make(map[string]dispatch.AccountRoutingOptions, len(options.ProviderAccountStrategies))
		for provider, providerOptions := range options.ProviderAccountStrategies {
			providerOptions.Strategy = dispatch.StrategyFallback
			providerOptions.AffinityKey = ""
			retryOptions.ProviderAccountStrategies[provider] = providerOptions
		}
	}

	planner := &attemptPlanner{
		dispatcher: dispatcher,
		tenantID:   tenantID,
		targets:    stableTargetOrder(initial, originalTargets),
		required:   required,
		options:    retryOptions,
		affinity:   options,
		excluded:   excluded,
		attempted:  attempted,
		sleep:      sleepCtx,
	}
	if len(initial) > 0 {
		planner.current = initial[0]
		planner.remaining = true
	}
	return planner
}

func (p *attemptPlanner) Current() (dispatch.Attempt, bool) {
	return p.current, p.remaining
}

func (p *attemptPlanner) AfterFailure(ctx context.Context, failed dispatch.Attempt, pe *core.ProviderError) (dispatch.Attempt, bool) {
	if !p.remaining || failed.Account.ID == "" {
		p.remaining = false
		return dispatch.Attempt{}, false
	}
	p.attempted[failed.Key()] = struct{}{}
	p.options.ExcludedAttempts = p.attempted
	if pe != nil && pe.EffectiveScope() == core.FailureScopeAccount {
		p.excluded[failed.Account.ID] = struct{}{}
	}
	p.options.ExcludedAccountIDs = p.excluded
	p.dispatcher.EvictAccountAffinity(ctx, p.tenantID, failed.Target, p.affinity, failed.Account.ID)

	attempts, err := p.dispatcher.PlanWith(ctx, p.tenantID, p.targets, p.required, p.options)
	if err != nil || len(attempts) == 0 {
		if p.retrySame(ctx, failed, pe) {
			return failed, true
		}
		p.remaining = false
		return dispatch.Attempt{}, false
	}
	p.sameRetries = 0
	p.current = attempts[0]
	return p.current, true
}

// retrySame decides whether the exhausted chain should re-run the failed
// attempt after a backoff. Only transient upstream faults qualify; request
// errors, auth/quota failures and self-imposed deadlines never do.
func (p *attemptPlanner) retrySame(ctx context.Context, failed dispatch.Attempt, pe *core.ProviderError) bool {
	if pe == nil || p.sameRetries >= maxSameAttemptRetries || ctx.Err() != nil {
		return false
	}
	if !isTransientForSameRetry(pe) {
		return false
	}
	delay := sameRetryDelay(p.sameRetries, pe.RetryAfter)
	if !p.sleep(ctx, delay) {
		return false
	}
	p.sameRetries++
	p.current = failed
	p.remaining = true
	return true
}

func isTransientForSameRetry(pe *core.ProviderError) bool {
	switch pe.Kind {
	case core.ErrUpstream, core.ErrTimeout:
	default:
		return false
	}
	switch pe.EffectiveScope() {
	case core.FailureScopeProvider, core.FailureScopeNetwork:
	default:
		return false
	}
	// Our own deadline fired: retrying the same slow call is pointless.
	if pe.Kind == core.ErrTimeout && errors.Is(pe.Cause, context.DeadlineExceeded) {
		return false
	}
	return true
}

// sameRetryDelay computes the backoff before a same-attempt retry: a Retry-After of at most
// 60s wins; otherwise 0.5s·2^n capped at 8s. Both get up to 750ms of jitter.
func sameRetryDelay(n int, retryAfter time.Duration) time.Duration {
	jitter := time.Duration(rand.Int64N(int64(sameRetryJitter) + 1))
	if retryAfter > 0 && retryAfter <= sameRetryMaxRetryAfter {
		return retryAfter + jitter
	}
	delay := sameRetryInitialDelay << uint(n)
	if delay > sameRetryMaxDelay {
		delay = sameRetryMaxDelay
	}
	return delay + jitter
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *attemptPlanner) AfterRepair(ctx context.Context, failed dispatch.Attempt, pe *core.ProviderError) (dispatch.Attempt, bool) {
	if next, ok := p.AfterFailure(ctx, failed, pe); ok {
		return next, true
	}
	p.current = failed
	p.remaining = true
	return failed, true
}

func stableTargetOrder(initial []dispatch.Attempt, original []dispatch.Target) []dispatch.Target {
	out := make([]dispatch.Target, 0, len(original))
	seen := make(map[dispatch.Target]struct{}, len(original))
	for _, attempt := range initial {
		if _, ok := seen[attempt.Target]; ok {
			continue
		}
		seen[attempt.Target] = struct{}{}
		out = append(out, attempt.Target)
	}
	for _, target := range original {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	return out
}
