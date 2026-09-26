package gateway

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// ipLimiter tracks per-IP request counts for rate limiting.
type ipLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // max requests per window
	window   time.Duration // time window
	stop     chan struct{} // signals cleanup to exit
}

type visitor struct {
	count    int
	lastSeen time.Time
}

// newIPLimiter creates a rate limiter that allows `rate` requests per `window`
// per IP address.
func newIPLimiter(rate int, window time.Duration) *ipLimiter {
	l := &ipLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
		stop:     make(chan struct{}),
	}
	// Cleanup old entries periodically
	go l.cleanup()
	return l
}

// Stop gracefully shuts down the cleanup goroutine.
func (l *ipLimiter) Stop() {
	close(l.stop)
}

// Allow checks if the request from the given IP should be allowed.
func (l *ipLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	v, exists := l.visitors[ip]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > l.window {
		l.visitors[ip] = &visitor{count: 1, lastSeen: now}
		return true
	}

	if v.count >= l.rate {
		return false
	}

	v.count++
	v.lastSeen = now
	return true
}

// cleanup removes expired entries every minute.
func (l *ipLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			for ip, v := range l.visitors {
				if time.Since(v.lastSeen) > l.window*2 {
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

// loginRateLimiter creates a rate limiting middleware for the login endpoint.
// Allows 5 attempts per minute per IP.
func (s *Server) loginRateLimiter(next http.Handler) http.Handler {
	limiter := newIPLimiter(5, time.Minute)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !limiter.Allow(ip) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: too many login attempts, please try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiKeyRateLimiter creates a rate limiting middleware for API key auth failures.
// Allows 20 failed attempts per minute per IP.
func (s *Server) apiKeyRateLimiter(next http.Handler) http.Handler {
	limiter := newIPLimiter(20, time.Minute)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !limiter.Allow(ip) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: too many authentication failures")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// publicRateLimiter is a global per-IP budget for the unauthenticated public
// landing API. Generous enough for a page load (several sections), tight enough
// that the endpoint cannot be used as a free aggregate-scraping oracle.
func (s *Server) publicRateLimiter(next http.Handler) http.Handler {
	limiter := newIPLimiter(60, time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow(extractIP(r)) {
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
func concurrencyLimiter(maxConcurrent int) func(http.Handler) http.Handler {
	sem := make(chan struct{}, maxConcurrent)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				writeError(w, http.StatusServiceUnavailable,
					"server is at capacity, please retry shortly")
			}
		})
	}
}
