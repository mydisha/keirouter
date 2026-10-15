package budget

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// fakeStores stands in for the budget and usage repos shared by replicas.
type fakeStores struct {
	mu         sync.Mutex
	budgets    []store.Budget
	spent      int64 // micros in the "usage table"
	tokens     int64
	listCalls  atomic.Int32
	spendCalls atomic.Int32
}

func (f *fakeStores) ListByScope(_ context.Context, kind store.BudgetScope, id string) ([]store.Budget, error) {
	f.listCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Budget
	for _, b := range f.budgets {
		if b.ScopeKind == kind && b.ScopeID == id {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeStores) SpendAndTokensBatch(_ context.Context, scopes []store.SpendScope) ([]store.SpendResult, error) {
	f.spendCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.SpendResult, len(scopes))
	for i := range scopes {
		out[i] = store.SpendResult{CostMicros: f.spent, Tokens: f.tokens}
	}
	return out, nil
}

func sharedEngine(t *testing.T, mr *miniredis.Miniredis, fs *fakeStores) *Engine {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	e := &Engine{budgets: fs, usage: fs, reservations: map[string]*reservation{},
		budgetCache: map[string]*budgetCacheEntry{}}
	e.EnableSharedCounter(NewRedisCounter(client, 200*time.Millisecond, nil), time.Minute, nil)
	t.Cleanup(e.Close)
	return e
}

var keyScope = Scope{TenantID: "t1", APIKeyID: "k1"}

func monthlyBudget(limitMicros int64) store.Budget {
	return store.Budget{ID: "b1", TenantID: "t1", ScopeKind: store.ScopeAPIKey, ScopeID: "k1",
		LimitMicros: limitMicros, Period: "monthly", HardCutoff: true}
}

func TestSharedCounterAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	fs := &fakeStores{budgets: []store.Budget{monthlyBudget(1_000_000)}, spent: 400_000}
	a, b := sharedEngine(t, mr, fs), sharedEngine(t, mr, fs)

	dec, err := a.Check(context.Background(), keyScope)
	require.NoError(t, err)
	require.True(t, dec.Allowed)
	require.Equal(t, int64(400_000), dec.BudgetUsage[0].CostMicros)
	require.EqualValues(t, 1, fs.spendCalls.Load(), "first check seeds from the usage table")

	for i := 0; i < 5; i++ {
		dec, err = b.Check(context.Background(), keyScope)
		require.NoError(t, err)
		require.True(t, dec.Allowed)
	}
	require.EqualValues(t, 1, fs.spendCalls.Load(), "other replicas read the shared counter, no SUM")

	// Replica A meters a $0.65 request; replica B must see it and block.
	a.ObserveUsage(store.UsageRecord{TenantID: "t1", APIKeyID: "k1", CostNanos: 650_000_000,
		PromptTokens: 100, CompletionTokens: 50, CreatedAt: time.Now()})
	require.Eventually(t, func() bool {
		dec, err := b.Check(context.Background(), keyScope)
		return err == nil && !dec.Allowed
	}, 2*time.Second, 5*time.Millisecond)

	dec, err = b.Check(context.Background(), keyScope)
	require.NoError(t, err)
	require.Equal(t, int64(1_050_000), dec.BudgetUsage[0].CostMicros)
	require.Equal(t, "b1", dec.Blocking.ID)
}

func TestSharedCounterReseedsAfterTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	fs := &fakeStores{budgets: []store.Budget{monthlyBudget(1_000_000)}, spent: 100}
	e := sharedEngine(t, mr, fs)

	_, err := e.Check(context.Background(), keyScope)
	require.NoError(t, err)
	fs.mu.Lock()
	fs.spent = 900 // usage table moved on (e.g. an increment was lost)
	fs.mu.Unlock()

	dec, _ := e.Check(context.Background(), keyScope)
	require.Equal(t, int64(100), dec.BudgetUsage[0].CostMicros, "served from the counter")

	mr.FastForward(2 * time.Minute)
	dec, _ = e.Check(context.Background(), keyScope)
	require.Equal(t, int64(900), dec.BudgetUsage[0].CostMicros, "re-seeded from the usage table")
	require.EqualValues(t, 2, fs.spendCalls.Load())
}

func TestSharedCounterFallsBackWhenRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	fs := &fakeStores{budgets: []store.Budget{monthlyBudget(1_000)}, spent: 2_000}
	e := sharedEngine(t, mr, fs)
	mr.Close()

	for i := 0; i < 3; i++ {
		dec, err := e.Check(context.Background(), keyScope)
		require.NoError(t, err)
		require.False(t, dec.Allowed, "enforced from the usage table while redis is down")
	}
	st := e.Stats()
	require.EqualValues(t, 1, st.Errors, "the breaker skips redis after the first failure")
	require.False(t, st.Healthy)
	require.EqualValues(t, 3, fs.spendCalls.Load())

	e.ObserveUsage(store.UsageRecord{TenantID: "t1", APIKeyID: "k1", CostNanos: 1})
}

func TestSharedCounterBroadcastsInvalidation(t *testing.T) {
	mr := miniredis.RunT(t)
	fs := &fakeStores{budgets: []store.Budget{monthlyBudget(1_000_000)}}
	a, b := sharedEngine(t, mr, fs), sharedEngine(t, mr, fs)

	_, err := b.Check(context.Background(), keyScope)
	require.NoError(t, err)
	listed := fs.listCalls.Load()
	_, _ = b.Check(context.Background(), keyScope)
	require.Equal(t, listed, fs.listCalls.Load(), "definitions cached")

	// An admin edit lands on replica A.
	fs.mu.Lock()
	fs.budgets[0].LimitMicros = 1
	fs.mu.Unlock()
	a.InvalidateBudgetCache()

	require.Eventually(t, func() bool {
		dec, err := b.Check(context.Background(), keyScope)
		return err == nil && len(dec.BudgetUsage) == 1 && dec.BudgetUsage[0].Budget.LimitMicros == 1
	}, 2*time.Second, 5*time.Millisecond, "replica B dropped its cached definition")
}

func TestObserveUsageMergesDeltas(t *testing.T) {
	mr := miniredis.RunT(t)
	fs := &fakeStores{budgets: []store.Budget{monthlyBudget(1 << 50)}}
	e := sharedEngine(t, mr, fs)
	_, err := e.Check(context.Background(), keyScope)
	require.NoError(t, err)

	for i := 0; i < 200; i++ {
		e.ObserveUsage(store.UsageRecord{TenantID: "t1", APIKeyID: "k1", CostNanos: 1000, PromptTokens: 1, CreatedAt: time.Now()})
	}
	require.Eventually(t, func() bool {
		dec, _ := e.Check(context.Background(), keyScope)
		return dec.BudgetUsage[0].CostMicros == 200 && dec.BudgetUsage[0].TokenCount == 200
	}, 2*time.Second, 5*time.Millisecond)
	require.Zero(t, e.Stats().Dropped)
}
