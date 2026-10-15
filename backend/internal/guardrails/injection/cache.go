package injection

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"time"
)

// resultCache memoizes model verdicts by content hash. Chat clients resend
// the whole conversation every turn, so without it every earlier message
// would be classified again on each request.
type resultCache[V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	limit int
	ll    *list.List
	items map[[32]byte]*list.Element
}

type cacheEntry[V any] struct {
	key [32]byte
	val V
	at  time.Time
}

func newResultCache[V any](limit int, ttl time.Duration) *resultCache[V] {
	return &resultCache[V]{ttl: ttl, limit: limit, ll: list.New(), items: map[[32]byte]*list.Element{}}
}

func cacheKey(parts ...string) [32]byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	var k [32]byte
	copy(k[:], h.Sum(nil))
	return k
}

func (c *resultCache[V]) get(k [32]byte) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[k]
	if !ok {
		return zero, false
	}
	e := el.Value.(*cacheEntry[V])
	if time.Since(e.at) > c.ttl {
		c.ll.Remove(el)
		delete(c.items, k)
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *resultCache[V]) put(k [32]byte, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value = &cacheEntry[V]{key: k, val: v, at: time.Now()}
		c.ll.MoveToFront(el)
		return
	}
	c.items[k] = c.ll.PushFront(&cacheEntry[V]{key: k, val: v, at: time.Now()})
	for c.ll.Len() > c.limit {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*cacheEntry[V]).key)
	}
}
