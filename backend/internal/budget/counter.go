package budget

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// CounterValue is one shared spend counter.
type CounterValue struct {
	Found     bool
	CostNanos int64
	Tokens    int64
}

// Delta is spend to add to one counter.
type Delta struct {
	Key       string
	CostNanos int64
	Tokens    int64
}

// Counter holds per-scope, per-period spend shared by every replica, so a
// budget check is a key lookup instead of a SUM over the usage table, and
// all replicas agree on the spend.
//
// Counters are a cache of the usage table: a missing counter is seeded from
// it, and each counter expires after a TTL and is seeded again, which bounds
// any drift from increments lost to a crash or a full queue.
type Counter interface {
	Load(ctx context.Context, keys []string) ([]CounterValue, error)
	// Seed creates a counter unless one already exists.
	Seed(ctx context.Context, key string, v CounterValue, ttl time.Duration) error
	// Add increments counters that exist; deltas for missing ones are dropped
	// because the next seed reads them from the usage table.
	Add(ctx context.Context, deltas []Delta) error
	// PublishInvalidate tells every replica that budget definitions changed.
	PublishInvalidate(ctx context.Context) error
	// SubscribeInvalidate calls fn whenever a replica publishes, until ctx ends.
	SubscribeInvalidate(ctx context.Context, fn func())
}

// counterKey names the counter of one scope for the period starting at
// since. The {scope} hash tag keeps a scope's counters on one cluster slot.
func counterKey(prefix string, kind store.BudgetScope, scopeID string, since time.Time) string {
	period := "total"
	if !since.IsZero() {
		period = strconv.FormatInt(since.Unix(), 10)
	}
	return prefix + "{" + string(kind) + ":" + scopeID + "}:" + period
}

var seedScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 0
end
redis.call('HSET', KEYS[1], 'c', ARGV[1], 't', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

var addScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 0
end
redis.call('HINCRBY', KEYS[1], 'c', ARGV[1])
redis.call('HINCRBY', KEYS[1], 't', ARGV[2])
return 1
`)

// RedisCounter is a Counter on redis hashes ("c" = cost nanos, "t" = tokens).
type RedisCounter struct {
	client  redis.UniversalClient
	timeout time.Duration
	channel string
	log     *slog.Logger
}

// NewRedisCounter builds a RedisCounter. timeout bounds each call on the
// request path.
func NewRedisCounter(client redis.UniversalClient, timeout time.Duration, log *slog.Logger) *RedisCounter {
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	if log == nil {
		log = slog.Default()
	}
	return &RedisCounter{client: client, timeout: timeout, channel: "kr:bud:invalidate", log: log}
}

// Load reads every key in one pipelined round trip.
func (c *RedisCounter) Load(ctx context.Context, keys []string) ([]CounterValue, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmds := make([]*redis.SliceCmd, len(keys))
	_, err := c.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, k := range keys {
			cmds[i] = p.HMGet(ctx, k, "c", "t")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]CounterValue, len(keys))
	for i, cmd := range cmds {
		vals := cmd.Val()
		if len(vals) != 2 || vals[0] == nil {
			continue
		}
		out[i].Found = true
		out[i].CostNanos = parseRedisInt(vals[0])
		out[i].Tokens = parseRedisInt(vals[1])
	}
	return out, nil
}

func parseRedisInt(v any) int64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// Seed implements Counter. The TTL gets ±10% jitter so counters seeded
// together do not all expire, and hit the database, at the same moment.
func (c *RedisCounter) Seed(ctx context.Context, key string, v CounterValue, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	jitter := time.Duration(rand.Int64N(int64(ttl)/5+1)) - ttl/10
	return seedScript.Run(ctx, c.client, []string{key}, v.CostNanos, v.Tokens, (ttl + jitter).Milliseconds()).Err()
}

// Add implements Counter in one pipelined round trip.
func (c *RedisCounter) Add(ctx context.Context, deltas []Delta) error {
	if len(deltas) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	// Make sure the script is cached so the pipelined EVALSHAs succeed.
	if err := addScript.Load(ctx, c.client).Err(); err != nil {
		return err
	}
	_, err := c.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, d := range deltas {
			addScript.EvalSha(ctx, p, []string{d.Key}, d.CostNanos, d.Tokens)
		}
		return nil
	})
	return err
}

// PublishInvalidate implements Counter.
func (c *RedisCounter) PublishInvalidate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.Publish(ctx, c.channel, "1").Err()
}

// SubscribeInvalidate implements Counter. go-redis resubscribes after a
// dropped connection; a replica that misses a message still converges when
// its 10s definition cache expires.
func (c *RedisCounter) SubscribeInvalidate(ctx context.Context, fn func()) {
	sub := c.client.Subscribe(ctx, c.channel)
	go func() {
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				fn()
			}
		}
	}()
}
