package limits

import (
	"context"
	"testing"
	"time"
)

func TestMemoryRPMAllowUnderLimit(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", Limits: EffectiveLimits{RPM: 2}}
	for i := 0; i < 2; i++ {
		release, decision, err := lim.Acquire(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allowed {
			t.Fatalf("request %d denied: %+v", i, decision)
		}
		release(0)
	}
}

func TestMemoryRPMDenyOverLimit(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", Limits: EffectiveLimits{RPM: 1}}
	release, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("first denied: %+v", decision)
	}
	release(0)

	_, decision, err = lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.LimitKind != "rpm" {
		t.Fatalf("expected rpm deny, got %+v", decision)
	}
}

func TestMemoryTPMDenyOverLimit(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", EstimatedTokens: 11, Limits: EffectiveLimits{TPM: 10}}
	_, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.LimitKind != "tpm" {
		t.Fatalf("expected tpm deny, got %+v", decision)
	}
}

func TestMemoryConcurrencyDenyAndRelease(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", Limits: EffectiveLimits{Concurrency: 1}}
	release, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("first denied: %+v", decision)
	}

	_, decision, err = lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.LimitKind != "concurrency" {
		t.Fatalf("expected concurrency deny, got %+v", decision)
	}

	release(0)
	release(0) // idempotent

	release2, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allow after release, got %+v", decision)
	}
	release2(0)
}

func TestMemoryExpiredWindowResets(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", Limits: EffectiveLimits{RPM: 1}}
	release, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("first denied: %+v", decision)
	}
	release(0)

	now = now.Add(time.Minute)

	release, decision, err = lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allow after window reset, got %+v", decision)
	}
	release(0)
}

func TestMemoryUnlimitedZeroMeansNoDeny(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", EstimatedTokens: 1_000_000, Limits: EffectiveLimits{}}
	for i := 0; i < 100; i++ {
		release, decision, err := lim.Acquire(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allowed {
			t.Fatalf("request %d denied: %+v", i, decision)
		}
		release(0)
	}
}

func TestMemoryDeniedTPMRollsBackConcurrency(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Now: func() time.Time { return now }})

	req := Request{APIKeyID: "key1", EstimatedTokens: 20, Limits: EffectiveLimits{Concurrency: 1, TPM: 10}}
	_, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.LimitKind != "tpm" {
		t.Fatalf("expected tpm deny, got %+v", decision)
	}

	req.EstimatedTokens = 1
	release, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("concurrency was not rolled back: %+v", decision)
	}
	release(0)
}

func newTestMemory(now *time.Time, opts Options) *Memory {
	return NewMemory(MemoryConfig{Enabled: true, Window: time.Minute, Options: opts, Now: func() time.Time { return *now }})
}

func mustAcquire(t *testing.T, lim *Memory, req Request) (ReleaseFunc, Decision) {
	t.Helper()
	release, decision, err := lim.Acquire(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return release, decision
}

func TestMemoryTPMReconcileRefundsOverEstimate(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := newTestMemory(&now, Options{ReconcileTokens: true})
	req := Request{APIKeyID: "key1", EstimatedTokens: 80, Limits: EffectiveLimits{TPM: 100}}

	release, decision := mustAcquire(t, lim, req)
	if !decision.Allowed {
		t.Fatalf("first denied: %+v", decision)
	}
	release(10)

	if _, decision = mustAcquire(t, lim, req); !decision.Allowed {
		t.Fatalf("over-estimate was not refunded: %+v", decision)
	}
}

func TestMemoryTPMWithoutReconcileKeepsEstimate(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := newTestMemory(&now, Options{})
	req := Request{APIKeyID: "key1", EstimatedTokens: 80, Limits: EffectiveLimits{TPM: 100}}

	release, _ := mustAcquire(t, lim, req)
	release(10)

	if _, decision := mustAcquire(t, lim, req); decision.Allowed || decision.LimitKind != "tpm" {
		t.Fatalf("expected tpm deny with reconciliation off, got %+v", decision)
	}
}

func TestMemoryTPMReleaseZeroKeepsEstimate(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := newTestMemory(&now, Options{ReconcileTokens: true})
	req := Request{APIKeyID: "key1", EstimatedTokens: 80, Limits: EffectiveLimits{TPM: 100}}

	release, _ := mustAcquire(t, lim, req)
	release(0)

	if _, decision := mustAcquire(t, lim, req); decision.Allowed {
		t.Fatalf("unknown usage must keep the estimate, got %+v", decision)
	}
}

func TestMemoryTPMRefundDropsReservation(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := newTestMemory(&now, Options{ReconcileTokens: true})
	req := Request{APIKeyID: "key1", EstimatedTokens: 100, Limits: EffectiveLimits{TPM: 100}}

	release, _ := mustAcquire(t, lim, req)
	release(Refund)
	release(500) // only the first release counts

	if _, decision := mustAcquire(t, lim, req); !decision.Allowed {
		t.Fatalf("refund did not free the reservation: %+v", decision)
	}
}

func TestMemoryTPMReconcileChargesOverrun(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	lim := newTestMemory(&now, Options{ReconcileTokens: true})

	release, _ := mustAcquire(t, lim, Request{APIKeyID: "key1", EstimatedTokens: 10, Limits: EffectiveLimits{TPM: 100}})
	release(60)

	if _, decision := mustAcquire(t, lim, Request{APIKeyID: "key1", EstimatedTokens: 41, Limits: EffectiveLimits{TPM: 100}}); decision.Allowed {
		t.Fatalf("overrun was not charged: %+v", decision)
	}
	if _, decision := mustAcquire(t, lim, Request{APIKeyID: "key1", EstimatedTokens: 40, Limits: EffectiveLimits{TPM: 100}}); !decision.Allowed {
		t.Fatalf("expected remaining budget of 40, got %+v", decision)
	}
}

func TestMemoryTPMReconcileAfterWindowRollover(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 30, 0, time.UTC)
	lim := newTestMemory(&now, Options{ReconcileTokens: true})

	release, _ := mustAcquire(t, lim, Request{APIKeyID: "key1", EstimatedTokens: 90, Limits: EffectiveLimits{TPM: 100}})
	now = now.Add(time.Minute) // long stream finishes in the next window
	release(30)

	if _, decision := mustAcquire(t, lim, Request{APIKeyID: "key1", EstimatedTokens: 71, Limits: EffectiveLimits{TPM: 100}}); decision.Allowed {
		t.Fatalf("usage was not charged to the current window: %+v", decision)
	}
	if _, decision := mustAcquire(t, lim, Request{APIKeyID: "key1", EstimatedTokens: 70, Limits: EffectiveLimits{TPM: 100}}); !decision.Allowed {
		t.Fatalf("expected remaining budget of 70, got %+v", decision)
	}
}

func TestOptionsReserveTokens(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		req  Request
		want int64
	}{
		{"max tokens counted", Options{}, Request{EstimatedTokens: 10, Generative: true, MaxOutputTokens: 100}, 110},
		{"no max tokens no floor", Options{}, Request{EstimatedTokens: 10, Generative: true}, 10},
		{"floor when max tokens unset", Options{OutputTokenFloor: 50}, Request{EstimatedTokens: 10, Generative: true}, 60},
		{"floor ignored when max tokens set", Options{OutputTokenFloor: 50}, Request{EstimatedTokens: 10, Generative: true, MaxOutputTokens: 20}, 30},
		{"cap clamps max tokens", Options{OutputReserveCap: 100}, Request{EstimatedTokens: 10, Generative: true, MaxOutputTokens: 64000}, 110},
		{"cap clamps floor", Options{OutputTokenFloor: 500, OutputReserveCap: 100}, Request{EstimatedTokens: 10, Generative: true}, 110},
		{"non-generative ignores output", Options{OutputTokenFloor: 50}, Request{EstimatedTokens: 10, MaxOutputTokens: 100}, 10},
		{"minimum one token", Options{}, Request{}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.reserveTokens(tt.req); got != tt.want {
				t.Fatalf("reserveTokens = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMemoryConcurrencyRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 10, 0, time.UTC)
	req := Request{APIKeyID: "key1", Limits: EffectiveLimits{Concurrency: 1}}

	lim := newTestMemory(&now, Options{})
	mustAcquire(t, lim, req)
	if _, decision := mustAcquire(t, lim, req); decision.RetryAfter != DefaultConcurrencyRetryAfter {
		t.Fatalf("default retry-after = %v, want %v", decision.RetryAfter, DefaultConcurrencyRetryAfter)
	}

	lim.SetOptions(Options{ConcurrencyRetryAfter: 3 * time.Second})
	_, decision := mustAcquire(t, lim, req)
	if decision.RetryAfter != 3*time.Second || !decision.Reset.Equal(now.Add(3*time.Second)) {
		t.Fatalf("configured retry-after not applied: %+v", decision)
	}
}
