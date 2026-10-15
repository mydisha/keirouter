package gateway

import (
	"testing"
	"time"
)

func TestGuardrailTestRateLimiter_BurstThenRetryAfter(t *testing.T) {
	l := newGuardrailTestRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("sess:a"); !ok {
			t.Fatalf("run %d refused within burst", i+1)
		}
	}
	ok, wait := l.allow("sess:a")
	if ok {
		t.Fatal("run beyond burst allowed")
	}
	// One token refills every 20s at 3 per minute.
	if wait <= 0 || wait > 20*time.Second {
		t.Errorf("retry-after = %v, want (0, 20s]", wait)
	}
	if ok, _ := l.allow("sess:b"); !ok {
		t.Error("buckets must be per session")
	}
}
