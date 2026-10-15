package limits

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	// Mid-window, so a test never straddles a window boundary by accident.
	c.ns.Store(time.Date(2026, 10, 11, 10, 0, 30, 0, time.UTC).UnixNano())
	return c
}
func (c *fakeClock) Now() time.Time         { return time.Unix(0, c.ns.Load()) }
func (c *fakeClock) Add(d time.Duration)    { c.ns.Add(int64(d)) }
func (c *fakeClock) Set(t time.Time)        { c.ns.Store(t.UnixNano()) }
func (c *fakeClock) WindowStart() time.Time { return c.Now().Truncate(time.Minute) }
func (c *fakeClock) AdvanceToNextWindow()   { c.Set(c.WindowStart().Add(time.Minute + time.Second)) }

// replica builds one limiter instance sharing mr, as one KeiRouter process
// behind a load balancer would.
func replica(t *testing.T, mr *miniredis.Miniredis, clock *fakeClock, mut ...func(*RedisConfig)) *Redis {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cfg := RedisConfig{
		Client: client, Enabled: true, Window: time.Minute, LeaseTTL: time.Minute,
		Timeout: 200 * time.Millisecond, Options: Options{ReconcileTokens: true},
	}
	if clock != nil {
		cfg.Now = clock.Now
	}
	for _, m := range mut {
		m(&cfg)
	}
	r := NewRedis(cfg)
	t.Cleanup(r.Close)
	return r
}

func acquire(t *testing.T, l Limiter, lim EffectiveLimits, tokens int64) (ReleaseFunc, Decision) {
	t.Helper()
	rel, dec, err := l.Acquire(context.Background(), Request{APIKeyID: "k1", EstimatedTokens: tokens, Limits: lim})
	require.NoError(t, err)
	return rel, dec
}

func TestRedisRPMSharedAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	a, b := replica(t, mr, clock), replica(t, mr, clock)
	lim := EffectiveLimits{RPM: 3}

	for i, l := range []*Redis{a, b, a} {
		_, dec := acquire(t, l, lim, 1)
		require.True(t, dec.Allowed, "request %d", i)
	}
	_, dec := acquire(t, b, lim, 1)
	require.False(t, dec.Allowed, "the fourth request is over the shared limit")
	require.Equal(t, "rpm", dec.LimitKind)
	require.Equal(t, int64(0), dec.Remaining)
	require.Equal(t, 30*time.Second, dec.RetryAfter)

	clock.AdvanceToNextWindow()
	_, dec = acquire(t, b, lim, 1)
	require.True(t, dec.Allowed, "a new window starts fresh")
}

func TestRedisConcurrencySharedAndReleased(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	a, b := replica(t, mr, clock), replica(t, mr, clock)
	lim := EffectiveLimits{Concurrency: 2}

	relA, dec := acquire(t, a, lim, 1)
	require.True(t, dec.Allowed)
	_, dec = acquire(t, b, lim, 1)
	require.True(t, dec.Allowed)
	_, dec = acquire(t, a, lim, 1)
	require.False(t, dec.Allowed)
	require.Equal(t, "concurrency", dec.LimitKind)

	relA(Refund)
	relA(Refund) // second release is a no-op
	_, dec = acquire(t, b, lim, 1)
	require.True(t, dec.Allowed, "the released slot is reusable from another replica")
}

func TestRedisCrashedReplicaLeasesExpire(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	lim := EffectiveLimits{Concurrency: 1}
	a := replica(t, mr, clock)
	b := replica(t, mr, clock)

	_, dec := acquire(t, a, lim, 1)
	require.True(t, dec.Allowed)
	a.Close() // "crash": the slot is never released and no longer renewed

	_, dec = acquire(t, b, lim, 1)
	require.False(t, dec.Allowed, "slot still held within its lease")

	clock.Add(time.Minute + time.Second)
	_, dec = acquire(t, b, lim, 1)
	require.True(t, dec.Allowed, "an expired lease frees the slot")
}

func TestRedisHeartbeatKeepsLongRequestAlive(t *testing.T) {
	mr := miniredis.RunT(t)
	lim := EffectiveLimits{Concurrency: 1}
	ttl := 150 * time.Millisecond
	withTTL := func(c *RedisConfig) { c.LeaseTTL = ttl; c.Now = time.Now }
	a := replica(t, mr, nil, withTTL)
	b := replica(t, mr, nil, withTTL)

	rel, dec := acquire(t, a, lim, 1)
	require.True(t, dec.Allowed)
	time.Sleep(3 * ttl) // several heartbeats

	_, dec = acquire(t, b, lim, 1)
	require.False(t, dec.Allowed, "a renewed lease outlives its TTL")
	rel(Refund)
	_, dec = acquire(t, b, lim, 1)
	require.True(t, dec.Allowed)
}

func TestRedisTPMReconcile(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	a := replica(t, mr, clock)
	lim := EffectiveLimits{TPM: 100}

	rel, dec := acquire(t, a, lim, 80)
	require.True(t, dec.Allowed)
	_, dec = acquire(t, a, lim, 30)
	require.False(t, dec.Allowed, "80 reserved + 30 > 100")
	require.Equal(t, int64(20), dec.Remaining)

	rel(30) // the upstream reported 30 tokens: refund 50
	_, dec = acquire(t, a, lim, 60)
	require.True(t, dec.Allowed, "30 + 60 <= 100")

	key := "kr:rl:{k1}:tpm:" + itoa(clock.WindowStart().Unix())
	v, err := mr.Get(key)
	require.NoError(t, err)
	require.Equal(t, "90", v)
}

func TestRedisSettleAfterWindowRolledOver(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	a := replica(t, mr, clock)
	lim := EffectiveLimits{TPM: 1000}

	rel, _ := acquire(t, a, lim, 100)
	oldKey := "kr:rl:{k1}:tpm:" + itoa(clock.WindowStart().Unix())
	clock.AdvanceToNextWindow()
	mr.FastForward(2 * time.Minute) // the old window's counter expired
	rel(40)

	require.False(t, mr.Exists(oldKey))
	v, err := mr.Get("kr:rl:{k1}:tpm:" + itoa(clock.WindowStart().Unix()))
	require.NoError(t, err)
	require.Equal(t, "40", v, "actual usage lands in the current window")
}

func TestRedisFailModes(t *testing.T) {
	lim := EffectiveLimits{RPM: 1}
	for _, tc := range []struct {
		mode    string
		allowed []bool
	}{
		{FailLocal, []bool{true, false}}, // per-process limit still applies
		{FailOpen, []bool{true, true}},
		{FailClosed, []bool{false, false}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			mr := miniredis.RunT(t)
			clock := newFakeClock()
			l := replica(t, mr, clock, func(c *RedisConfig) { c.FailMode = tc.mode })
			mr.Close()
			for i, want := range tc.allowed {
				_, dec := acquire(t, l, lim, 1)
				require.Equal(t, want, dec.Allowed, "request %d", i)
				if tc.mode == FailClosed {
					require.Equal(t, "unavailable", dec.LimitKind)
				}
			}
			st := l.Stats()
			require.False(t, st.Healthy)
			require.EqualValues(t, 1, st.RedisErrors, "the breaker skips redis after the first failure")
			require.EqualValues(t, 2, st.DegradedRequests)
		})
	}
}

func TestRedisBreakerRecovers(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	l := replica(t, mr, clock, func(c *RedisConfig) { c.FailMode = FailClosed })
	lim := EffectiveLimits{RPM: 10}

	mr.Close()
	_, dec := acquire(t, l, lim, 1)
	require.False(t, dec.Allowed)

	require.NoError(t, mr.Restart())
	_, dec = acquire(t, l, lim, 1)
	require.False(t, dec.Allowed, "still inside the cooldown")

	clock.Add(6 * time.Second)
	_, dec = acquire(t, l, lim, 1)
	require.True(t, dec.Allowed, "redis is retried after the cooldown")
	require.True(t, l.Stats().Healthy)
}

func TestRedisUnlimitedKeyNeverTouchesRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	l := replica(t, mr, newFakeClock(), func(c *RedisConfig) { c.FailMode = FailClosed })
	mr.Close()
	_, dec := acquire(t, l, EffectiveLimits{}, 1)
	require.True(t, dec.Allowed)
	require.EqualValues(t, 0, l.Stats().RedisErrors)
}

func TestRedisConcurrentAcquireIsAtomic(t *testing.T) {
	mr := miniredis.RunT(t)
	clock := newFakeClock()
	replicas := []*Redis{replica(t, mr, clock), replica(t, mr, clock), replica(t, mr, clock)}
	lim := EffectiveLimits{RPM: 25}

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 90; i++ {
		wg.Add(1)
		go func(l *Redis) {
			defer wg.Done()
			_, dec, err := l.Acquire(context.Background(), Request{APIKeyID: "k1", Limits: lim})
			if err == nil && dec.Allowed {
				allowed.Add(1)
			}
		}(replicas[i%3])
	}
	wg.Wait()
	require.EqualValues(t, 25, allowed.Load(), "never over-admits under contention")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
