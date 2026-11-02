package store

import (
	"strings"
	"sync"
	"time"
)

// readCache holds recent results of a hot read for a short TTL. Writes made
// through this DB handle invalidate it at once; the TTL bounds how stale a
// result can be when another process (a second replica on the same Postgres)
// writes. gen advances on every invalidation, so a read that raced a write
// cannot store what it read before the write landed.
type readCache[V any] struct {
	ttl        time.Duration
	maxEntries int

	mu      sync.RWMutex
	gen     uint64
	entries map[string]readCacheEntry[V]
}

type readCacheEntry[V any] struct {
	value   V
	expires time.Time
}

// get returns a fresh cached value, and the generation to pass to put when
// there is none.
func (c *readCache[V]) get(key string, now time.Time) (value V, hit bool, gen uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if ok && now.Before(e.expires) {
		return e.value, true, c.gen
	}
	return value, false, c.gen
}

// put stores a value read at generation gen, unless a write happened since.
// Reaching maxEntries means something unusual, and the cache just resets.
func (c *readCache[V]) put(key string, value V, gen uint64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	if c.entries == nil || len(c.entries) >= c.maxEntries {
		c.entries = make(map[string]readCacheEntry[V])
	}
	c.entries[key] = readCacheEntry[V]{value: value, expires: now.Add(c.ttl)}
}

// invalidate drops one key and fences out reads that started before the write.
func (c *readCache[V]) invalidate(key string) {
	c.mu.Lock()
	c.gen++
	delete(c.entries, key)
	c.mu.Unlock()
}

// invalidateAll drops every entry.
func (c *readCache[V]) invalidateAll() {
	c.mu.Lock()
	c.gen++
	c.entries = nil
	c.mu.Unlock()
}

// settingValue is a cached SettingsRepo.Get result; misses are cached too.
type settingValue struct {
	value string
	found bool
}

// Settings are read several times per proxied request, so even a short TTL
// removes nearly all of those queries. The settings keys are a small, mostly
// fixed set.
func newSettingsCache() *readCache[settingValue] {
	return &readCache[settingValue]{ttl: 2 * time.Second, maxEntries: 1024}
}

// Model cooldowns and health verdicts are read for every target of every
// request and written only on failures and health probes. A second is short
// next to any cooldown or probe interval; the key is the model plus the
// account set, which is a handful of combinations per deployment.
func newRoutingCache[V any]() *readCache[V] {
	return &readCache[V]{ttl: time.Second, maxEntries: 4096}
}

// routingCacheKey keys a per-target routing read by model and account set.
func routingCacheKey(model string, accountIDs []string) string {
	return model + "\x00" + strings.Join(accountIDs, ",")
}

// InvalidateSettingsCache drops cached settings. Code that writes the settings
// table with raw SQL instead of SettingsRepo must call it afterwards.
func (db *DB) InvalidateSettingsCache() { db.settingsCache.invalidateAll() }
