package limits

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type Limiter interface {
	Acquire(ctx context.Context, req Request) (ReleaseFunc, Decision, error)
}

// ReleaseFunc frees the request's concurrency slot and settles its TPM
// reservation. Only the first call takes effect, so a deferred safety-net
// release after an explicit one is harmless.
//
// When token reconciliation is enabled, actualTokens > 0 replaces the
// reserved estimate with the usage the upstream reported, 0 keeps the
// estimate (usage unknown), and Refund drops the reservation because the
// request never consumed upstream tokens.
type ReleaseFunc func(actualTokens int64)

// Refund is passed to a ReleaseFunc when the request consumed no upstream
// tokens (rejected locally, served from cache, failed before generation).
const Refund int64 = -1

// DefaultConcurrencyRetryAfter is the Retry-After hint for a concurrency
// denial when Options leaves it unset.
const DefaultConcurrencyRetryAfter = time.Second

type Request struct {
	TenantID  string
	ProjectID string
	APIKeyID  string
	Provider  string
	Model     string
	// EstimatedTokens is the input-side token estimate.
	EstimatedTokens int64
	// Generative marks requests that produce output tokens (chat). Only
	// generative requests reserve an output budget.
	Generative bool
	// MaxOutputTokens is the client's requested output cap; 0 means unset.
	MaxOutputTokens int64
	Limits          EffectiveLimits
}

// Options are the operator-tunable limiter behaviours. They can be swapped at
// runtime with SetOptions; each Acquire uses the snapshot current at the time.
type Options struct {
	// ReconcileTokens settles each TPM reservation against the usage the
	// upstream reported: over-estimates are refunded, overruns are charged.
	ReconcileTokens bool
	// OutputTokenFloor is the output budget reserved for a generative request
	// that sets no max_tokens. Zero reserves nothing for output up front.
	OutputTokenFloor int64
	// OutputReserveCap caps the output part of a reservation, so clients that
	// always send a huge max_tokens do not exhaust TPM before generating.
	// Zero means no cap. Pair it with ReconcileTokens so real usage is still
	// charged.
	OutputReserveCap int64
	// ConcurrencyRetryAfter is the Retry-After hint on a concurrency denial.
	// Zero or negative uses DefaultConcurrencyRetryAfter.
	ConcurrencyRetryAfter time.Duration
}

// reserveTokens is the TPM amount charged up front for req.
func (o Options) reserveTokens(req Request) int64 {
	tokens := max(req.EstimatedTokens, 0)
	if req.Generative {
		output := req.MaxOutputTokens
		if output <= 0 {
			output = o.OutputTokenFloor
		}
		if o.OutputReserveCap > 0 && output > o.OutputReserveCap {
			output = o.OutputReserveCap
		}
		tokens += max(output, 0)
	}
	if tokens <= 0 {
		tokens = 1
	}
	return tokens
}

type EffectiveLimits struct {
	RPM         int64
	TPM         int64
	Concurrency int64
}

type Decision struct {
	Allowed    bool
	Reason     string
	RetryAfter time.Duration
	LimitKind  string
	Scope      string
	Limit      int64
	Remaining  int64
	Reset      time.Time
}

type Resolver struct {
	cfg   config.LimitsConfig
	plans *store.PlanRepo
}

func NewResolver(cfg config.LimitsConfig, plans *store.PlanRepo) *Resolver {
	return &Resolver{cfg: cfg, plans: plans}
}

func (r *Resolver) Resolve(ctx context.Context, key store.APIKey) (EffectiveLimits, error) {
	if key.PlanID != "" && r.plans != nil {
		plan, err := r.plans.Get(ctx, key.PlanID)
		if err != nil {
			return EffectiveLimits{}, err
		}
		return EffectiveLimits{
			RPM:         plan.RPMLimit,
			TPM:         plan.TPMLimit,
			Concurrency: plan.ConcurrencyLimit,
		}, nil
	}
	return EffectiveLimits{
		RPM:         r.cfg.DefaultRPM,
		TPM:         r.cfg.DefaultTPM,
		Concurrency: r.cfg.DefaultConcurrency,
	}, nil
}

type MemoryConfig struct {
	Enabled         bool
	Window          time.Duration
	CleanupInterval time.Duration
	Options         Options
	Now             func() time.Time
}

type bucket struct {
	count   int64
	expires time.Time
}

type Memory struct {
	enabled atomic.Bool
	opts    atomic.Pointer[Options]
	window  time.Duration
	now     func() time.Time

	mu          sync.Mutex
	buckets     map[string]bucket
	concurrency map[string]int64
}

func NewMemory(cfg MemoryConfig) *Memory {
	window := cfg.Window
	if window <= 0 {
		window = time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	m := &Memory{
		window:      window,
		now:         now,
		buckets:     make(map[string]bucket),
		concurrency: make(map[string]int64),
	}
	m.enabled.Store(cfg.Enabled)
	m.SetOptions(cfg.Options)
	if cfg.CleanupInterval > 0 {
		go m.cleanupLoop(cfg.CleanupInterval)
	}
	return m
}

func (m *Memory) SetEnabled(enabled bool) {
	m.enabled.Store(enabled)
}

func (m *Memory) Enabled() bool {
	return m.enabled.Load()
}

// SetOptions swaps the tunable behaviours; in-flight requests keep the
// snapshot they were admitted with.
func (m *Memory) SetOptions(opts Options) {
	m.opts.Store(&opts)
}

// Options returns the current tunable behaviours.
func (m *Memory) Options() Options {
	return *m.opts.Load()
}

func (m *Memory) Acquire(ctx context.Context, req Request) (ReleaseFunc, Decision, error) {
	if ctx.Err() != nil {
		return nil, Decision{}, ctx.Err()
	}
	if !m.enabled.Load() {
		return noopRelease, Decision{Allowed: true}, nil
	}
	if req.APIKeyID == "" {
		return noopRelease, Decision{Allowed: true}, nil
	}
	if req.EstimatedTokens < 0 {
		req.EstimatedTokens = 0
	}

	opts := m.Options()
	key := "key:" + req.APIKeyID
	concurrencyKey := key + ":concurrency"
	now := m.now()
	windowStart := now.Truncate(m.window)
	reset := windowStart.Add(m.window)
	var concurrencyInc bool

	m.mu.Lock()
	defer m.mu.Unlock()

	if req.Limits.Concurrency > 0 {
		current := m.concurrency[concurrencyKey]
		if current >= req.Limits.Concurrency {
			retryAfter := opts.ConcurrencyRetryAfter
			if retryAfter <= 0 {
				retryAfter = DefaultConcurrencyRetryAfter
			}
			return noopRelease, denied("concurrency", "key", req.Limits.Concurrency, 0, retryAfter, now.Add(retryAfter)), nil
		}
		m.concurrency[concurrencyKey] = current + 1
		concurrencyInc = true
	}

	if req.Limits.RPM > 0 {
		bucketKey := windowKey(key, "rpm", windowStart)
		b := m.bucket(bucketKey, reset, now)
		if b.count+1 > req.Limits.RPM {
			if concurrencyInc {
				m.decrementLocked(concurrencyKey)
			}
			remaining := req.Limits.RPM - b.count
			if remaining < 0 {
				remaining = 0
			}
			return noopRelease, denied("rpm", "key", req.Limits.RPM, remaining, reset.Sub(now), reset), nil
		}
		b.count++
		m.buckets[bucketKey] = b
	}

	var tpmKey string
	var reserved int64
	if req.Limits.TPM > 0 {
		tokens := opts.reserveTokens(req)
		bucketKey := windowKey(key, "tpm", windowStart)
		b := m.bucket(bucketKey, reset, now)
		if b.count+tokens > req.Limits.TPM {
			if concurrencyInc {
				m.decrementLocked(concurrencyKey)
			}
			remaining := req.Limits.TPM - b.count
			if remaining < 0 {
				remaining = 0
			}
			return noopRelease, denied("tpm", "key", req.Limits.TPM, remaining, reset.Sub(now), reset), nil
		}
		b.count += tokens
		m.buckets[bucketKey] = b
		if opts.ReconcileTokens {
			tpmKey = bucketKey
			reserved = tokens
		}
	}

	if !concurrencyInc && tpmKey == "" {
		return noopRelease, Decision{Allowed: true}, nil
	}
	var once sync.Once
	release := func(actualTokens int64) {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if concurrencyInc {
				m.decrementLocked(concurrencyKey)
			}
			if tpmKey != "" && actualTokens != 0 {
				m.settleTokensLocked(key, tpmKey, reserved, actualTokens)
			}
		})
	}
	return release, Decision{Allowed: true}, nil
}

// settleTokensLocked replaces a TPM reservation with the actual usage. When
// the reservation's window is still live the difference is applied to it;
// when the window has rolled over the reservation expired with it, so the
// real usage is charged to the current window instead.
func (m *Memory) settleTokensLocked(key, reservedKey string, reserved, actual int64) {
	if actual < 0 {
		actual = 0
	}
	now := m.now()
	if b, ok := m.buckets[reservedKey]; ok && b.expires.After(now) {
		b.count += actual - reserved
		if b.count < 0 {
			b.count = 0
		}
		m.buckets[reservedKey] = b
		return
	}
	if actual == 0 {
		return
	}
	windowStart := now.Truncate(m.window)
	bucketKey := windowKey(key, "tpm", windowStart)
	b := m.bucket(bucketKey, windowStart.Add(m.window), now)
	b.count += actual
	m.buckets[bucketKey] = b
}

func windowKey(key, kind string, windowStart time.Time) string {
	return key + ":" + kind + ":" + strconv.FormatInt(windowStart.Unix(), 10)
}

func (m *Memory) bucket(key string, reset time.Time, now time.Time) bucket {
	b := m.buckets[key]
	if b.expires.IsZero() || !b.expires.After(now) {
		return bucket{expires: reset}
	}
	return b
}

func (m *Memory) decrementLocked(key string) {
	current := m.concurrency[key]
	if current <= 1 {
		delete(m.concurrency, key)
		return
	}
	m.concurrency[key] = current - 1
}

func (m *Memory) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanup()
	}
}

func (m *Memory) cleanup() {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, b := range m.buckets {
		if !b.expires.After(now) {
			delete(m.buckets, k)
		}
	}
}

func denied(kind, scope string, limit, remaining int64, retryAfter time.Duration, reset time.Time) Decision {
	if retryAfter < 0 {
		retryAfter = 0
	}
	return Decision{
		Allowed:    false,
		Reason:     fmt.Sprintf("rate limit exceeded: %s %s limit", scope, kind),
		RetryAfter: retryAfter,
		LimitKind:  kind,
		Scope:      scope,
		Limit:      limit,
		Remaining:  remaining,
		Reset:      reset,
	}
}

func noopRelease(int64) {}
