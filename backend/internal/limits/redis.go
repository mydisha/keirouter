package limits

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Fail modes for the redis limiter while redis is unreachable.
const (
	FailLocal  = "local"
	FailOpen   = "open"
	FailClosed = "closed"
)

// acquireScript checks and charges concurrency, RPM and TPM in one atomic
// round trip. It checks every limit before charging any, so a denial leaves
// no partial reservation behind.
//
// KEYS: 1 concurrency lease zset, 2 RPM window counter, 3 TPM window counter
// ARGV: 1 now (ms), 2 lease expiry (ms), 3 lease id, 4 concurrency limit,
// 5 RPM limit, 6 TPM limit, 7 tokens to reserve, 8 window counter TTL (ms),
// 9 lease zset TTL (ms)
//
// Returns {1} when admitted, else {0, kind, remaining} with kind 1 =
// concurrency, 2 = RPM, 3 = TPM.
var acquireScript = redis.NewScript(`
local conc_limit = tonumber(ARGV[4])
local rpm_limit = tonumber(ARGV[5])
local tpm_limit = tonumber(ARGV[6])
local tokens = tonumber(ARGV[7])
if conc_limit > 0 then
  redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
  if redis.call('ZCARD', KEYS[1]) >= conc_limit then
    return {0, 1, 0}
  end
end
if rpm_limit > 0 then
  local used = tonumber(redis.call('GET', KEYS[2]) or '0')
  if used + 1 > rpm_limit then
    return {0, 2, rpm_limit - used}
  end
end
if tpm_limit > 0 then
  local used = tonumber(redis.call('GET', KEYS[3]) or '0')
  if used + tokens > tpm_limit then
    return {0, 3, tpm_limit - used}
  end
end
if conc_limit > 0 then
  redis.call('ZADD', KEYS[1], ARGV[2], ARGV[3])
  redis.call('PEXPIRE', KEYS[1], ARGV[9])
end
if rpm_limit > 0 then
  redis.call('INCR', KEYS[2])
  redis.call('PEXPIRE', KEYS[2], ARGV[8])
end
if tpm_limit > 0 then
  redis.call('INCRBY', KEYS[3], tokens)
  redis.call('PEXPIRE', KEYS[3], ARGV[8])
end
return {1}
`)

// releaseScript frees a concurrency lease and settles a TPM reservation,
// mirroring Memory.settleTokensLocked: a live reservation window absorbs the
// difference (floored at zero); once it has rolled over, the actual usage is
// charged to the current window.
//
// KEYS: 1 concurrency lease zset, 2 reserved TPM window, 3 current TPM window
// ARGV: 1 lease id ("" for none), 2 reserved tokens, 3 actual tokens,
// 4 current window TTL (ms), 5 settle flag (1 = settle)
var releaseScript = redis.NewScript(`
if ARGV[1] ~= '' then
  redis.call('ZREM', KEYS[1], ARGV[1])
end
if ARGV[5] == '1' then
  local reserved = tonumber(ARGV[2])
  local actual = tonumber(ARGV[3])
  if redis.call('PTTL', KEYS[2]) > 0 then
    local v = redis.call('INCRBY', KEYS[2], actual - reserved)
    if v < 0 then
      redis.call('INCRBY', KEYS[2], -v)
    end
  elseif actual > 0 then
    redis.call('INCRBY', KEYS[3], actual)
    redis.call('PEXPIRE', KEYS[3], ARGV[4])
  end
end
return 1
`)

// RedisConfig configures a Redis limiter.
type RedisConfig struct {
	Client  redis.UniversalClient
	Enabled bool
	// Window is the fixed RPM/TPM counting window (default one minute).
	Window  time.Duration
	Options Options
	// LeaseTTL is how long a concurrency slot lives without a heartbeat. The
	// limiter renews its own leases every LeaseTTL/3, so only a crashed
	// replica's slots expire.
	LeaseTTL time.Duration
	// Timeout bounds each redis round trip on the request path.
	Timeout time.Duration
	// FailMode decides requests while redis is unreachable (FailLocal,
	// FailOpen or FailClosed).
	FailMode string
	// BreakerCooldown is how long a redis failure routes requests to the
	// fail mode before redis is tried again (default 5s), so an outage costs
	// one timeout per cooldown instead of one per request.
	BreakerCooldown time.Duration
	// KeyPrefix namespaces the limiter's keys (default "kr:rl:").
	KeyPrefix string
	Now       func() time.Time
	Logger    *slog.Logger
}

// Redis is a Limiter whose counters live in redis, so every replica behind a
// load balancer enforces one shared RPM/TPM window and one concurrency pool
// per API key.
//
// The request path costs one round trip to acquire (a Lua script that checks
// and charges every limit atomically) and one to release; keys without
// limits never touch redis. Keys share a {api key} hash tag, so the scripts
// also run on Redis Cluster.
//
// Concurrency slots are leases in a sorted set scored by expiry. A replica
// that crashes mid-request stops renewing its leases and the slots free
// themselves after LeaseTTL, instead of leaking like a bare counter would.
type Redis struct {
	client   redis.UniversalClient
	enabled  atomic.Bool
	opts     atomic.Pointer[Options]
	window   time.Duration
	leaseTTL time.Duration
	timeout  time.Duration
	cooldown time.Duration
	failMode string
	fallback *Memory
	prefix   string
	now      func() time.Time
	log      *slog.Logger

	// openUntil is the unix-nano time the circuit breaker closes again.
	openUntil atomic.Int64
	tripped   atomic.Bool

	leaseMu     sync.Mutex
	leases      map[string]string // lease id -> lease zset key
	leasePrefix string
	leaseSeq    atomic.Uint64

	redisErrors      atomic.Int64
	degradedRequests atomic.Int64

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewRedis builds a Redis limiter and starts its lease heartbeat. Call Close
// on shutdown.
func NewRedis(cfg RedisConfig) *Redis {
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 100 * time.Millisecond
	}
	if cfg.BreakerCooldown <= 0 {
		cfg.BreakerCooldown = 5 * time.Second
	}
	if cfg.FailMode == "" {
		cfg.FailMode = FailLocal
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "kr:rl:"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	r := &Redis{
		client:      cfg.Client,
		window:      cfg.Window,
		leaseTTL:    cfg.LeaseTTL,
		timeout:     cfg.Timeout,
		cooldown:    cfg.BreakerCooldown,
		failMode:    cfg.FailMode,
		prefix:      cfg.KeyPrefix,
		now:         cfg.Now,
		log:         cfg.Logger,
		leases:      make(map[string]string),
		leasePrefix: hex.EncodeToString(b[:]) + ":",
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
	// The local fallback runs with the same limits per process.
	r.fallback = NewMemory(MemoryConfig{
		Enabled:         cfg.Enabled,
		Window:          cfg.Window,
		CleanupInterval: time.Minute,
		Options:         cfg.Options,
		Now:             cfg.Now,
	})
	r.enabled.Store(cfg.Enabled)
	r.opts.Store(&cfg.Options)
	go r.heartbeat()
	return r
}

// SetEnabled turns enforcement on or off.
func (r *Redis) SetEnabled(enabled bool) {
	r.enabled.Store(enabled)
	r.fallback.SetEnabled(enabled)
}

// SetOptions swaps the tunable behaviours.
func (r *Redis) SetOptions(opts Options) {
	r.opts.Store(&opts)
	r.fallback.SetOptions(opts)
}

// Options returns the current tunable behaviours.
func (r *Redis) Options() Options { return *r.opts.Load() }

// Close stops the lease heartbeat. Leases still held expire after LeaseTTL.
func (r *Redis) Close() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.doneCh
}

// Acquire implements Limiter.
func (r *Redis) Acquire(ctx context.Context, req Request) (ReleaseFunc, Decision, error) {
	if ctx.Err() != nil {
		return nil, Decision{}, ctx.Err()
	}
	lim := req.Limits
	if !r.enabled.Load() || req.APIKeyID == "" || (lim.RPM <= 0 && lim.TPM <= 0 && lim.Concurrency <= 0) {
		return noopRelease, Decision{Allowed: true}, nil
	}
	if r.breakerOpen() {
		return r.degraded(ctx, req)
	}
	if req.EstimatedTokens < 0 {
		req.EstimatedTokens = 0
	}

	opts := r.Options()
	now := r.now()
	windowStart := now.Truncate(r.window)
	reset := windowStart.Add(r.window)
	base := r.prefix + "{" + req.APIKeyID + "}"
	concKey := base + ":conc"
	tpmKey := windowKey(base, "tpm", windowStart)

	var tokens int64
	if lim.TPM > 0 {
		tokens = opts.reserveTokens(req)
	}
	var leaseID string
	if lim.Concurrency > 0 {
		leaseID = r.leasePrefix + strconv.FormatUint(r.leaseSeq.Add(1), 36)
	}

	cctx, cancel := context.WithTimeout(ctx, r.timeout)
	res, err := acquireScript.Run(cctx, r.client,
		[]string{concKey, windowKey(base, "rpm", windowStart), tpmKey},
		now.UnixMilli(), now.Add(r.leaseTTL).UnixMilli(), leaseID,
		lim.Concurrency, lim.RPM, lim.TPM, tokens,
		(reset.Sub(now) + time.Second).Milliseconds(), (2 * r.leaseTTL).Milliseconds(),
	).Int64Slice()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, Decision{}, ctx.Err()
		}
		r.trip(err)
		return r.degraded(ctx, req)
	}
	r.recovered()

	if len(res) >= 3 && res[0] == 0 {
		remaining := max(res[2], 0)
		switch res[1] {
		case 1:
			retryAfter := opts.ConcurrencyRetryAfter
			if retryAfter <= 0 {
				retryAfter = DefaultConcurrencyRetryAfter
			}
			return noopRelease, denied("concurrency", "key", lim.Concurrency, 0, retryAfter, now.Add(retryAfter)), nil
		case 2:
			return noopRelease, denied("rpm", "key", lim.RPM, remaining, reset.Sub(now), reset), nil
		default:
			return noopRelease, denied("tpm", "key", lim.TPM, remaining, reset.Sub(now), reset), nil
		}
	}

	settleTPM := lim.TPM > 0 && opts.ReconcileTokens
	if leaseID == "" && !settleTPM {
		return noopRelease, Decision{Allowed: true}, nil
	}
	if leaseID != "" {
		r.leaseMu.Lock()
		r.leases[leaseID] = concKey
		r.leaseMu.Unlock()
	}
	var once sync.Once
	release := func(actualTokens int64) {
		once.Do(func() {
			if leaseID != "" {
				r.leaseMu.Lock()
				delete(r.leases, leaseID)
				r.leaseMu.Unlock()
			}
			settle := settleTPM && actualTokens != 0
			if leaseID == "" && !settle {
				return
			}
			now := r.now()
			curStart := now.Truncate(r.window)
			settleFlag := "0"
			if settle {
				settleFlag = "1"
			}
			// A cancelled request still has to give its slot back.
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			err := releaseScript.Run(ctx, r.client,
				[]string{concKey, tpmKey, windowKey(base, "tpm", curStart)},
				leaseID, tokens, max(actualTokens, 0),
				(curStart.Add(r.window).Sub(now) + time.Second).Milliseconds(), settleFlag,
			).Err()
			if err != nil {
				// The lease is no longer renewed, so it expires on its own.
				r.redisErrors.Add(1)
				r.log.Debug("rate limiter release failed", "err", err)
			}
		})
	}
	return release, Decision{Allowed: true}, nil
}

// degraded decides a request while redis is unavailable.
func (r *Redis) degraded(ctx context.Context, req Request) (ReleaseFunc, Decision, error) {
	r.degradedRequests.Add(1)
	switch r.failMode {
	case FailOpen:
		return noopRelease, Decision{Allowed: true}, nil
	case FailClosed:
		return noopRelease, Decision{
			Allowed: false, Reason: "rate limiter unavailable", RetryAfter: r.cooldown,
			LimitKind: "unavailable", Scope: "key", Reset: r.now().Add(r.cooldown),
		}, nil
	default:
		return r.fallback.Acquire(ctx, req)
	}
}

func (r *Redis) breakerOpen() bool {
	until := r.openUntil.Load()
	return until != 0 && r.now().UnixNano() < until
}

func (r *Redis) trip(err error) {
	r.redisErrors.Add(1)
	r.openUntil.Store(r.now().Add(r.cooldown).UnixNano())
	if !r.tripped.Swap(true) {
		r.log.Warn("rate limiter: redis unavailable, using fail mode",
			"fail_mode", r.failMode, "retry_in", r.cooldown, "err", err)
	}
}

func (r *Redis) recovered() {
	if r.tripped.Load() && r.tripped.Swap(false) {
		r.openUntil.Store(0)
		r.log.Info("rate limiter: redis reachable again")
	}
}

// heartbeat extends this replica's live leases every LeaseTTL/3. ZADD XX only
// touches members that still exist, so a lease released concurrently is not
// resurrected.
func (r *Redis) heartbeat() {
	defer close(r.doneCh)
	t := time.NewTicker(max(r.leaseTTL/3, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-t.C:
			r.renewLeases()
		}
	}
}

func (r *Redis) renewLeases() {
	r.leaseMu.Lock()
	if len(r.leases) == 0 {
		r.leaseMu.Unlock()
		return
	}
	byKey := make(map[string][]string)
	for id, key := range r.leases {
		byKey[key] = append(byKey[key], id)
	}
	r.leaseMu.Unlock()

	expiry := float64(r.now().Add(r.leaseTTL).UnixMilli())
	ctx, cancel := context.WithTimeout(context.Background(), r.leaseTTL/3)
	defer cancel()
	_, err := r.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for key, ids := range byKey {
			members := make([]redis.Z, len(ids))
			for i, id := range ids {
				members[i] = redis.Z{Score: expiry, Member: id}
			}
			p.ZAddXX(ctx, key, members...)
			p.PExpire(ctx, key, 2*r.leaseTTL)
		}
		return nil
	})
	if err != nil {
		r.redisErrors.Add(1)
		r.log.Debug("rate limiter lease heartbeat failed", "err", err)
	}
}

// RedisStats reports limiter health for the dashboard.
type RedisStats struct {
	Backend          string `json:"backend"`
	Healthy          bool   `json:"healthy"`
	FailMode         string `json:"fail_mode"`
	ActiveLeases     int    `json:"active_leases"`
	RedisErrors      int64  `json:"redis_errors"`
	DegradedRequests int64  `json:"degraded_requests"`
}

// Stats returns a snapshot of the limiter's health counters.
func (r *Redis) Stats() RedisStats {
	r.leaseMu.Lock()
	n := len(r.leases)
	r.leaseMu.Unlock()
	return RedisStats{
		Backend:          "redis",
		Healthy:          !r.breakerOpen(),
		FailMode:         r.failMode,
		ActiveLeases:     n,
		RedisErrors:      r.redisErrors.Load(),
		DegradedRequests: r.degradedRequests.Load(),
	}
}
