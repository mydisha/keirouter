package pipeline

import (
	"context"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// AttemptObserver receives per-request routing events the caller cannot see
// from the final result alone: a target that failed before the pipeline moved
// on to the next one, and waits for rate-limited accounts to cool down. The
// gateway uses it to show a request's full timeline on the dashboard console.
// Implementations must be cheap and must not block.
type AttemptObserver interface {
	AttemptFailed(provider, model string, err *core.ProviderError)
	RateLimitWait(wait time.Duration, retry, max int)
}

type observerCtxKey struct{}

// WithObserver attaches an AttemptObserver to ctx for the pipeline to notify.
func WithObserver(ctx context.Context, o AttemptObserver) context.Context {
	return context.WithValue(ctx, observerCtxKey{}, o)
}

// ObserverFrom returns the AttemptObserver attached to ctx, or nil.
func ObserverFrom(ctx context.Context) AttemptObserver {
	o, _ := ctx.Value(observerCtxKey{}).(AttemptObserver)
	return o
}

func noteAttemptFailed(ctx context.Context, provider, model string, pe *core.ProviderError) {
	if o := ObserverFrom(ctx); o != nil {
		o.AttemptFailed(provider, model, pe)
	}
}

func noteRateLimitWait(ctx context.Context, wait time.Duration, retry, max int) {
	if o := ObserverFrom(ctx); o != nil {
		o.RateLimitWait(wait, retry, max)
	}
}
