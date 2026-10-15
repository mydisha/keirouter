package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type staticGlobalProxy struct{ url, noProxy string }

func (p staticGlobalProxy) ProxyURL() string { return p.url }
func (p staticGlobalProxy) NoProxy() string  { return p.noProxy }

// recordingRefresher captures the proxy carried by the refresh context.
type recordingRefresher struct {
	seen []core.Credentials
}

func (r *recordingRefresher) EnsureFresh(ctx context.Context, acc store.Account) (store.Account, error) {
	creds, _ := core.ProxyFromContext(ctx)
	r.seen = append(r.seen, creds)
	return acc, nil
}

func (r *recordingRefresher) ForceRefresh(ctx context.Context, acc store.Account) (store.Account, error) {
	return r.EnsureFresh(ctx, acc)
}

func TestPlanWith_RefreshUsesGlobalProxy(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-1", 10))
	refresher := &recordingRefresher{}
	d.SetTokenRefresher(refresher)
	d.SetGlobalProxy(staticGlobalProxy{url: "http://proxy.example.com:8080", noProxy: "localhost"})

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, []Target{{Provider: "openai", Model: "gpt-4o"}}, core.NewCapabilitySet(), PlanOptions{})
	require.NoError(t, err)
	require.Len(t, refresher.seen, 1)
	require.Equal(t, "http://proxy.example.com:8080", refresher.seen[0].ProxyURL)
	require.Equal(t, "localhost", refresher.seen[0].NoProxy)
	require.Equal(t, "http://proxy.example.com:8080", attempts[0].Creds.ProxyURL)
	require.Equal(t, "sk-test", attempts[0].Creds.APIKey)
}

func TestPlanWith_RoundRobinKeepsPinnedFallbackLast(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-1", 10))
	now := time.Now()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "chain-1", TenantID: store.DefaultTenantID, Name: "combo",
		Strategy: string(StrategyRoundRobin), CreatedAt: now, UpdatedAt: now,
	}))

	targets := []Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "openai", Model: "gpt-5"},
		{Provider: "openai", Model: "gpt-4o-mini"}, // last-resort fallback
	}
	opts := PlanOptions{Strategy: StrategyRoundRobin, ChainID: "chain-1", PinnedTail: 1}

	var first, last []string
	for i := 0; i < 4; i++ {
		attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
		require.NoError(t, err)
		require.Len(t, attempts, 3)
		first = append(first, attempts[0].Target.Model)
		last = append(last, attempts[2].Target.Model)
	}
	require.Equal(t, []string{"gpt-4o", "gpt-5", "gpt-4o", "gpt-5"}, first)
	require.Equal(t, []string{"gpt-4o-mini", "gpt-4o-mini", "gpt-4o-mini", "gpt-4o-mini"}, last)
}
