package topics

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"time"
)

// verdictCache memoizes LLM verdicts: a conversation is resent every turn, so
// an unchanged newest message must not cost another model call.
type verdictCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	limit int
	ll    *list.List
	items map[[32]byte]*list.Element
}

type verdictEntry struct {
	key [32]byte
	val llmVerdict
	at  time.Time
}

func newVerdictCache(limit int, ttl time.Duration) *verdictCache {
	return &verdictCache{ttl: ttl, limit: limit, ll: list.New(), items: map[[32]byte]*list.Element{}}
}

func (c *verdictCache) get(key string) (llmVerdict, bool) {
	k := sha256.Sum256([]byte(key))
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return llmVerdict{}, false
	}
	e := el.Value.(*verdictEntry)
	if time.Since(e.at) > c.ttl {
		c.ll.Remove(el)
		delete(c.items, k)
		return llmVerdict{}, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *verdictCache) put(key string, v llmVerdict) {
	k := sha256.Sum256([]byte(key))
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value = &verdictEntry{key: k, val: v, at: time.Now()}
		c.ll.MoveToFront(el)
		return
	}
	c.items[k] = c.ll.PushFront(&verdictEntry{key: k, val: v, at: time.Now()})
	for c.ll.Len() > c.limit {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*verdictEntry).key)
	}
}
