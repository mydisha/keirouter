package gateway

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// insightsCacheTTL bounds how stale the Usage/Quota dashboard payloads may be.
// Solo deployments refetch these endpoints rapidly (page open, period toggle,
// nav back-and-forth); a short TTL collapses those into one SQLite aggregation
// pass while keeping the numbers fresh enough for a live dashboard.
const insightsCacheTTL = 12 * time.Second

// ttlCache is a tiny thread-safe cache of pre-marshaled JSON bodies keyed by a
// caller-supplied string. Entries expire after ttl. It is intentionally minimal
// — no background eviction — because the key space is bounded (a handful of
// endpoint × period × tz combinations).
type ttlCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]ttlEntry
}

type ttlEntry struct {
	body    []byte
	expires time.Time
}

func newTTLCache(ttl time.Duration) *ttlCache {
	return &ttlCache{ttl: ttl, entries: make(map[string]ttlEntry)}
}

func (c *ttlCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.body, true
}

func (c *ttlCache) set(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = ttlEntry{body: body, expires: time.Now().Add(c.ttl)}
}

// del drops key so the next request rebuilds it. Safe on a nil cache.
func (c *ttlCache) del(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// cacheHit writes a cached body for key if one is live, returning true. Handlers
// call this at the top and return early on a hit.
func (s *Server) cacheHit(w http.ResponseWriter, key string) bool {
	body, ok := s.insightsCache.get(key)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return true
}

// writeJSONCached marshals v, stores it under key, and writes it. Mirrors
// writeJSON but persists the body so subsequent calls within the TTL skip the
// aggregation work entirely.
func writeJSONCached(w http.ResponseWriter, c *ttlCache, key string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.set(key, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
