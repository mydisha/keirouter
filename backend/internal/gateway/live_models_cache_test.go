package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/connectors"
)

func TestLiveModelCacheMemoizesAndCollapses(t *testing.T) {
	c := newLiveModelCache()
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) ([]connectors.ModelSpec, error) {
		calls.Add(1)
		<-release
		return []connectors.ModelSpec{{ID: "m1"}}, nil
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, _ := c.list(context.Background(), "p", false, 0, fetch); len(got) != 1 {
				t.Errorf("got %d models, want 1", len(got))
			}
		}()
	}
	close(release)
	wg.Wait()
	c.list(context.Background(), "p", false, 0, fetch)
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetch called %d times, want 1", n)
	}

	c.list(context.Background(), "p", true, 0, fetch)
	if n := calls.Load(); n != 2 {
		t.Fatalf("refresh should bypass cache: fetch called %d times, want 2", n)
	}
}

func TestLiveModelCacheDetachesCallerCancel(t *testing.T) {
	c := newLiveModelCache()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sawCancel := make(chan bool, 1)
	// The canceled caller may give up, but the shared fetch must not see its
	// cancellation, and its result must still land in the cache.
	c.list(ctx, "p", false, 0, func(ctx context.Context) ([]connectors.ModelSpec, error) {
		sawCancel <- ctx.Err() != nil
		return []connectors.ModelSpec{{ID: "m1"}}, nil
	})
	if <-sawCancel {
		t.Fatal("fetch saw the caller's cancellation")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		e, ok := c.entries["p"]
		c.mu.Unlock()
		if ok && len(e.models) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("result never landed in the cache")
}

func TestLiveModelCacheNilAndFailure(t *testing.T) {
	var nilCache *liveModelCache
	if got, _ := nilCache.list(context.Background(), "p", false, 0, func(context.Context) ([]connectors.ModelSpec, error) {
		return []connectors.ModelSpec{{ID: "m1"}}, nil
	}); len(got) != 1 {
		t.Fatalf("nil cache should fetch directly")
	}
	c := newLiveModelCache()
	if got, _ := c.list(context.Background(), "p", false, 0, func(context.Context) ([]connectors.ModelSpec, error) {
		return nil, errors.New("upstream down")
	}); got != nil {
		t.Fatalf("failed fetch should yield nil, got %v", got)
	}
}

func TestLiveModelCacheWaitReturnsPendingThenCaches(t *testing.T) {
	c := newLiveModelCache()
	release := make(chan struct{})
	fetch := func(context.Context) ([]connectors.ModelSpec, error) {
		<-release
		return []connectors.ModelSpec{{ID: "m1"}}, nil
	}
	got, done := c.list(context.Background(), "p", false, 20*time.Millisecond, fetch)
	if done || got != nil {
		t.Fatalf("slow lookup should report pending, got done=%v models=%v", done, got)
	}
	close(release)
	// The detached fetch finishes in the background and fills the cache.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, done = c.list(context.Background(), "p", false, time.Millisecond, fetch); done && len(got) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("background fetch never landed in the cache")
}
