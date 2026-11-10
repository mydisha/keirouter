package mcp

import (
	"sync"
	"time"
)

// Circuit breaker settings, matching the provider breaker in dispatch.
const (
	breakerThreshold = 3
	breakerBase      = 5 * time.Second
	breakerMax       = 2 * time.Minute
	breakerProbe     = 30 * time.Second
)

// breaker opens per server after consecutive network or 5xx failures, so a
// dead upstream fails fast instead of holding every caller for its timeout.
type breaker struct {
	mu    sync.Mutex
	state map[string]*circuit
	now   func() time.Time
}

type circuit struct {
	failures  int
	openUntil time.Time
	probing   time.Time
}

func newBreaker() *breaker {
	return &breaker{state: map[string]*circuit{}, now: time.Now}
}

// allow reports whether a call to the server may proceed. While open it
// refuses; once the open window ends it lets one probe through at a time.
func (b *breaker) allow(serverID string) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.state[serverID]
	if c == nil || c.failures < breakerThreshold {
		return true, 0
	}
	now := b.now()
	if now.Before(c.openUntil) {
		return false, c.openUntil.Sub(now)
	}
	if !c.probing.IsZero() && now.Sub(c.probing) < breakerProbe {
		return false, breakerProbe - now.Sub(c.probing)
	}
	c.probing = now
	return true, 0
}

func (b *breaker) success(serverID string) {
	b.mu.Lock()
	delete(b.state, serverID)
	b.mu.Unlock()
}

func (b *breaker) failure(serverID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.state[serverID]
	if c == nil {
		c = &circuit{}
		b.state[serverID] = c
	}
	c.failures++
	c.probing = time.Time{}
	if c.failures < breakerThreshold {
		return
	}
	wait := breakerBase << (c.failures - breakerThreshold)
	if wait > breakerMax || wait <= 0 {
		wait = breakerMax
	}
	c.openUntil = b.now().Add(wait)
}

func (b *breaker) forget(serverID string) {
	b.mu.Lock()
	delete(b.state, serverID)
	b.mu.Unlock()
}
