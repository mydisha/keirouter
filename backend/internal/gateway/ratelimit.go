package gateway

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ipLimiter counts events per IP in a fixed window that starts at the IP's
// first event. The limit is live-tunable; zero or negative disables it. A nil
// *ipLimiter allows everything.
type ipLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    atomic.Int64  // max events per window; <= 0 disables
	window   time.Duration // time window
	now      func() time.Time
	stop     chan struct{} // signals cleanup to exit
	stopOnce sync.Once
}

type visitor struct {
	count       int64
	windowStart time.Time
}

// newIPLimiter creates a limiter that allows `limit` events per `window` per
// IP address.
func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	l := &ipLimiter{
		visitors: make(map[string]*visitor),
		window:   window,
		now:      time.Now,
		stop:     make(chan struct{}),
	}
	l.limit.Store(int64(limit))
	// Cleanup old entries periodically
	go l.cleanup()
	return l
}

// SetLimit changes the per-window limit; <= 0 disables the limiter.
func (l *ipLimiter) SetLimit(limit int) {
	if l == nil {
		return
	}
	l.limit.Store(int64(limit))
}

// Stop gracefully shuts down the cleanup goroutine.
func (l *ipLimiter) Stop() {
	l.stopOnce.Do(func() { close(l.stop) })
}

// Allow records one event for ip and reports whether it is within the limit.
func (l *ipLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	limit := l.limit.Load()
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.visitorLocked(ip)
	if v.count >= limit {
		return false
	}
	v.count++
	return true
}

// Exceeded reports whether ip has used up its limit, without recording an
// event, and how long until its window resets.
func (l *ipLimiter) Exceeded(ip string) (bool, time.Duration) {
	if l == nil {
		return false, 0
	}
	limit := l.limit.Load()
	if limit <= 0 {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.visitors[ip]
	if !ok {
		return false, 0
	}
	now := l.now()
	if now.Sub(v.windowStart) >= l.window || v.count < limit {
		return false, 0
	}
	return true, v.windowStart.Add(l.window).Sub(now)
}

// Record counts one event for ip without checking the limit.
func (l *ipLimiter) Record(ip string) {
	if l == nil {
		return
	}
	if l.limit.Load() <= 0 {
		return
	}
	l.mu.Lock()
	l.visitorLocked(ip).count++
	l.mu.Unlock()
}

// visitorLocked returns ip's entry, starting a fresh window when the previous
// one has elapsed.
func (l *ipLimiter) visitorLocked(ip string) *visitor {
	now := l.now()
	v, ok := l.visitors[ip]
	if !ok || now.Sub(v.windowStart) >= l.window {
		v = &visitor{windowStart: now}
		l.visitors[ip] = v
	}
	return v
}

// cleanup removes expired entries every minute.
func (l *ipLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := l.now()
			l.mu.Lock()
			for ip, v := range l.visitors {
				if now.Sub(v.windowStart) >= l.window {
					delete(l.visitors, ip)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

// extractIP extracts the client IP from the request.
func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginRateLimiter limits dashboard login attempts per IP
// (limits.login_attempts_per_minute, overridable from the dashboard).
func (s *Server) loginRateLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		if !s.loginLimiter.Allow(ip) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: too many login attempts, please try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the key per-IP limits count a client against: its address
// (see clientAddr), with IPv6 grouped per /64 since one host can rotate
// through a whole /64 at no cost.
func (s *Server) clientIP(r *http.Request) string {
	if addr, ok := s.clientAddr(r); ok {
		return limitKey(addr)
	}
	return extractIP(r)
}

// clientAddr is the client's address. Behind a reverse proxy on the same host
// or private network (the deploy/Caddyfile setup), RemoteAddr is the proxy
// for every visitor, so when forwarded headers are trusted it takes the
// right-most X-Forwarded-For entry: the one the proxy appended, which the
// client cannot forge.
func (s *Server) clientAddr(r *http.Request) (netip.Addr, bool) {
	peer, err := netip.ParseAddr(extractIP(r))
	if err != nil {
		return netip.Addr{}, false
	}
	peer = peer.Unmap()
	if s.cfg.Security.TrustForwardedHeaders && (peer.IsLoopback() || peer.IsPrivate()) {
		if fwd := rightmostForwardedIP(r); fwd.IsValid() {
			return fwd, true
		}
	}
	return peer, true
}

// rightmostForwardedIP returns the last valid address in X-Forwarded-For.
func rightmostForwardedIP(r *http.Request) netip.Addr {
	values := r.Header.Values("X-Forwarded-For")
	for i := len(values) - 1; i >= 0; i-- {
		parts := strings.Split(values[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			if addr, err := netip.ParseAddr(strings.TrimSpace(parts[j])); err == nil {
				return addr.Unmap()
			}
		}
	}
	return netip.Addr{}
}

// limitKey groups IPv6 addresses by /64 and leaves IPv4 as is.
func limitKey(addr netip.Addr) string {
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// authFailureIP returns the client that invalid API keys are counted
// against, or false for a genuinely local client (the operator's own tools).
// Clients behind a local reverse proxy are counted by their forwarded IP.
func (s *Server) authFailureIP(r *http.Request) (string, bool) {
	if addr, ok := s.clientAddr(r); ok && addr.IsLoopback() {
		return "", false
	}
	return s.clientIP(r), true
}

// recordAuthFailure counts an invalid API key against the client IP.
func (s *Server) recordAuthFailure(r *http.Request) {
	if ip, ok := s.authFailureIP(r); ok {
		s.authFailures.Record(ip)
	}
}

// authFailureBlocked rejects an invalid API key from an IP that has already
// sent too many (limits.auth_failures_per_minute), so a key-guessing client is
// slowed down. Callers check it only after a key fails verification; valid
// keys are never refused here. It reports whether the request was rejected.
func (s *Server) authFailureBlocked(w http.ResponseWriter, r *http.Request) bool {
	ip, ok := s.authFailureIP(r)
	if !ok {
		return false
	}
	blocked, retryAfter := s.authFailures.Exceeded(ip)
	if !blocked {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded: too many authentication failures")
	return true
}

// publicRateLimiter is a global per-IP budget for the unauthenticated public
// landing API. Generous enough for a page load (several sections), tight enough
// that the endpoint cannot be used as a free aggregate-scraping oracle.
func (s *Server) publicRateLimiter(next http.Handler) http.Handler {
	limiter := newIPLimiter(60, time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow(s.clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// concurrencyLimiter caps the number of in-flight API requests to protect
// the gateway from resource exhaustion under high concurrency. Each request
// holds argon2 goroutines, SQLite connection time, upstream HTTP connections,
// and buffer pool slots. When the semaphore is full, new requests get 503.
//
// maxConcurrent is tuned for typical AI gateway workloads: long-lived
// streaming connections that hold resources for seconds to minutes.
//
// onReject, when non-nil, is called for every request turned away.
func concurrencyLimiter(maxConcurrent int, onReject func(*http.Request)) func(http.Handler) http.Handler {
	sem := make(chan struct{}, maxConcurrent)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				if onReject != nil {
					onReject(r)
				}
				writeError(w, http.StatusServiceUnavailable,
					"server is at capacity, please retry shortly")
			}
		})
	}
}
