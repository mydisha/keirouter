package app

import (
	"context"
	"time"

	"github.com/mydisha/keirouter/backend/internal/market"
)

const (
	referenceSyncInterval = 6 * time.Hour
	referenceRetryDelay   = 10 * time.Minute
)

// runReferenceSync keeps the official list prices from OpenRouter fresh. They
// only feed the public discount badge, so a slow cadence is enough; a failed
// fetch keeps the previous set and retries sooner.
func (a *App) runReferenceSync(ctx context.Context) {
	if a.references == nil {
		return
	}
	delay := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		refs, err := market.FetchOpenRouter(ctx, "")
		if err != nil || len(refs) == 0 {
			a.log.Debug("openrouter reference fetch failed", "err", err)
			delay = referenceRetryDelay
			continue
		}
		a.references.Replace(refs)
		delay = referenceSyncInterval
	}
}
