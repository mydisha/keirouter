// Package inflight counts upstream attempts currently running, per provider.
package inflight

import "sync"

// Tracker is a concurrency-safe per-provider counter. A nil *Tracker is a
// valid no-op tracker.
type Tracker struct {
	mu     sync.Mutex
	counts map[string]int64
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{counts: make(map[string]int64)}
}

// Inc marks one attempt against provider as running and returns the func
// that ends it. done is safe to call any number of times; only the first
// call decrements.
func (t *Tracker) Inc(provider string) (done func()) {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	t.counts[provider]++
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			if t.counts[provider]--; t.counts[provider] <= 0 {
				delete(t.counts, provider)
			}
			t.mu.Unlock()
		})
	}
}

// Snapshot returns a copy of the current counts with zero entries omitted.
func (t *Tracker) Snapshot() map[string]int64 {
	out := make(map[string]int64)
	if t == nil {
		return out
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.counts {
		if v > 0 {
			out[k] = v
		}
	}
	return out
}
