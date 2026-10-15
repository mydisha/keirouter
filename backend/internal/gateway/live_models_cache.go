package gateway

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/connectors"
)

// Live model discovery hits the upstream /models endpoint and can take up to
// its 10s timeout. The dashboard's model pickers fetch every provider's list at
// once, so without a cache each open re-probes every connected upstream.
const (
	liveModelsTTL     = 5 * time.Minute
	liveModelsMissTTL = time.Minute // empty / failed lookups retry sooner
)

// liveModelCache memoizes live discovery results per provider × account and
// collapses concurrent lookups for the same key into one upstream call.
type liveModelCache struct {
	mu      sync.Mutex
	entries map[string]liveModelEntry
	group   singleflight.Group
}

type liveModelEntry struct {
	models  []connectors.ModelSpec
	expires time.Time
}

func newLiveModelCache() *liveModelCache {
	return &liveModelCache{entries: make(map[string]liveModelEntry)}
}

// list returns the cached models for key, calling fetch on a miss. fetch runs
// detached from the caller's cancellation so one aborted request cannot fail
// the lookup for every caller sharing it. wait > 0 bounds how long list blocks
// on a miss: past it, list returns done=false while the fetch carries on and
// fills the cache for a later call. A nil cache always fetches inline.
func (c *liveModelCache) list(ctx context.Context, key string, refresh bool, wait time.Duration, fetch func(context.Context) ([]connectors.ModelSpec, error)) (models []connectors.ModelSpec, done bool) {
	if c == nil {
		models, _ = fetch(ctx)
		return models, true
	}
	cached := func() ([]connectors.ModelSpec, bool) {
		if refresh {
			return nil, false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		e, ok := c.entries[key]
		return e.models, ok && time.Now().Before(e.expires)
	}
	if models, ok := cached(); ok {
		return models, true
	}
	ch := c.group.DoChan(key, func() (any, error) {
		// Re-check: a lookup that finished between the miss above and this
		// call has already filled the cache.
		if models, ok := cached(); ok {
			return models, nil
		}
		models, err := fetch(context.WithoutCancel(ctx))
		ttl := liveModelsTTL
		if err != nil || len(models) == 0 {
			models, ttl = nil, liveModelsMissTTL
		}
		c.mu.Lock()
		c.entries[key] = liveModelEntry{models: models, expires: time.Now().Add(ttl)}
		c.mu.Unlock()
		return models, nil
	})
	var timeout <-chan time.Time
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case res := <-ch:
		models, _ = res.Val.([]connectors.ModelSpec)
		return models, true
	case <-timeout:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}
