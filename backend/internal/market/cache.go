package market

import (
	"sync"
	"time"
)

// SnapshotCache holds the latest market snapshot in memory. Replace swaps the
// whole map atomically so readers never observe a partially built snapshot.
type SnapshotCache struct {
	mu      sync.RWMutex
	bySlug  map[string]Model
	fetched time.Time
}

func NewSnapshotCache() *SnapshotCache {
	return &SnapshotCache{bySlug: map[string]Model{}}
}

// Replace installs a new snapshot.
func (c *SnapshotCache) Replace(models []Model) {
	next := make(map[string]Model, len(models))
	for _, m := range models {
		next[m.Slug] = m
	}
	c.mu.Lock()
	c.bySlug = next
	c.fetched = time.Now()
	c.mu.Unlock()
}

// Rate returns the raw market ask for slug. ok is false when the slug is absent
// or its asks are not strictly positive finite values.
func (c *SnapshotCache) Rate(slug string) (Rate, bool) {
	c.mu.RLock()
	m, ok := c.bySlug[slug]
	c.mu.RUnlock()
	if !ok || m.MinAskIn <= 0 || m.MinAskOut <= 0 {
		return Rate{}, false
	}
	if !validRate(m.MinAskIn) || !validRate(m.MinAskOut) {
		return Rate{}, false
	}
	return Rate{InputPerM: m.MinAskIn, OutputPerM: m.MinAskOut}, true
}

// Age reports time since the last successful Replace. A never-populated cache
// returns a very large duration so callers can treat it as stale.
func (c *SnapshotCache) Age() time.Duration {
	c.mu.RLock()
	f := c.fetched
	c.mu.RUnlock()
	if f.IsZero() {
		return time.Duration(1<<62 - 1)
	}
	return time.Since(f)
}

// Lookup returns the full snapshot model for slug, including source-specific
// fields (failover cap, discount ceiling) that Rate does not expose.
func (c *SnapshotCache) Lookup(slug string) (Model, bool) {
	c.mu.RLock()
	m, ok := c.bySlug[slug]
	c.mu.RUnlock()
	return m, ok
}
