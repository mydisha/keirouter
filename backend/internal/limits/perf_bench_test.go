package limits

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Hot-path cost of one admitted request (acquire + release) with all three
// limits set. miniredis runs in-process and interprets the Lua scripts in Go,
// so the redis numbers include the server's own work; they are only useful
// for spotting client-side regressions between runs.
var benchLimits = EffectiveLimits{RPM: 1 << 40, TPM: 1 << 50, Concurrency: 1 << 20}

func BenchmarkMemoryAcquireRelease(b *testing.B) {
	m := NewMemory(MemoryConfig{Enabled: true, Options: Options{ReconcileTokens: true}})
	req := Request{APIKeyID: "k1", EstimatedTokens: 500, Limits: benchLimits}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		rel, _, _ := m.Acquire(ctx, req)
		rel(420)
	}
}

func BenchmarkRedisAcquireRelease(b *testing.B) {
	mr := miniredis.RunT(b)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	r := NewRedis(RedisConfig{Client: client, Enabled: true, Timeout: time.Second, Options: Options{ReconcileTokens: true}})
	defer r.Close()
	req := Request{APIKeyID: "k1", EstimatedTokens: 500, Limits: benchLimits}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		rel, _, _ := r.Acquire(ctx, req)
		rel(420)
	}
}
