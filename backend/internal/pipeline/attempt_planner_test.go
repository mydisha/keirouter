package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
	"github.com/stretchr/testify/require"
)

type plannerConnectorSource struct{ conn core.Connector }

func (s plannerConnectorSource) Get(string) (core.Connector, error) { return s.conn, nil }

type plannerConnector struct{}

func (plannerConnector) ID() string            { return "openai" }
func (plannerConnector) Dialect() core.Dialect { return core.DialectOpenAI }
func (plannerConnector) Chat(context.Context, *core.ChatRequest, core.Credentials) (*core.ChatResponse, error) {
	return nil, nil
}
func (plannerConnector) Stream(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (<-chan core.StreamChunk, error) {
	ch := make(chan core.StreamChunk)
	close(ch)
	return ch, nil
}

func newPlannerDispatcher(t *testing.T) *dispatch.Dispatcher {
	return newPlannerDispatcherWithAccounts(t, "acc-1", "acc-2")
}

func newPlannerDispatcherWithAccounts(t *testing.T, accountIDs ...string) *dispatch.Dispatcher {
	return newPlannerDispatcherWithConnector(t, plannerConnector{}, accountIDs...)
}

func newPlannerDispatcherWithConnector(t *testing.T, conn core.Connector, accountIDs ...string) *dispatch.Dispatcher {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	key, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(key)
	require.NoError(t, err)
	secrets := vault.New(sealer)

	for i, id := range accountIDs {
		now := time.Now()
		account := store.Account{
			ID: id, TenantID: store.DefaultTenantID, Provider: "openai",
			Label: id, AuthKind: store.AuthAPIKey, Priority: (i + 1) * 10,
			Metadata: "{}", CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, secrets.Seal(&account, vault.NewSecret{APIKey: "sk-test"}))
		require.NoError(t, db.Accounts().Create(ctx, account))
	}

	d := dispatch.New(plannerConnectorSource{conn: conn}, db.Accounts(), secrets)
	d.SetRoutingSource(db.Routing())
	return d
}

func TestAttemptPlannerModelFailureKeepsAccountAvailableForNextModel(t *testing.T) {
	ctx := context.Background()
	d := newPlannerDispatcher(t)
	targets := []dispatch.Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "openai", Model: "gpt-5"},
	}
	initial, err := d.Plan(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet())
	require.NoError(t, err)
	planner := newAttemptPlanner(d, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{}, initial)

	first, ok := planner.Current()
	require.True(t, ok)
	require.Equal(t, "acc-1", first.Account.ID)
	require.Equal(t, "gpt-4o", first.Target.Model)

	second, ok := planner.AfterFailure(ctx, first, &core.ProviderError{Kind: core.ErrModelUnavailable})
	require.True(t, ok)
	require.Equal(t, "acc-2", second.Account.ID)
	require.Equal(t, "gpt-4o", second.Target.Model)

	third, ok := planner.AfterFailure(ctx, second, &core.ProviderError{Kind: core.ErrModelUnavailable})
	require.True(t, ok)
	require.Equal(t, "acc-1", third.Account.ID)
	require.Equal(t, "gpt-5", third.Target.Model)
}

func TestAttemptPlannerAccountFailureSkipsCredentialAcrossModels(t *testing.T) {
	ctx := context.Background()
	d := newPlannerDispatcher(t)
	targets := []dispatch.Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "openai", Model: "gpt-5"},
	}
	initial, err := d.Plan(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet())
	require.NoError(t, err)
	planner := newAttemptPlanner(d, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{}, initial)

	first, ok := planner.Current()
	require.True(t, ok)
	second, ok := planner.AfterFailure(ctx, first, &core.ProviderError{Kind: core.ErrAuth})
	require.True(t, ok)
	require.Equal(t, "acc-2", second.Account.ID)

	_, ok = planner.AfterFailure(ctx, second, &core.ProviderError{Kind: core.ErrAuth})
	require.False(t, ok)
}

func TestAttemptPlannerRepairPrefersAnotherAccount(t *testing.T) {
	ctx := context.Background()
	d := newPlannerDispatcher(t)
	targets := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}
	initial, err := d.Plan(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet())
	require.NoError(t, err)
	planner := newAttemptPlanner(d, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{}, initial)

	first, ok := planner.Current()
	require.True(t, ok)
	second, ok := planner.AfterRepair(ctx, first, &core.ProviderError{
		Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
	})
	require.True(t, ok)
	require.Equal(t, "acc-2", second.Account.ID)
}

func TestAttemptPlannerRepairRetriesOnlyAccountOnce(t *testing.T) {
	ctx := context.Background()
	d := newPlannerDispatcherWithAccounts(t, "acc-1")
	targets := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}
	initial, err := d.Plan(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet())
	require.NoError(t, err)
	planner := newAttemptPlanner(d, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{}, initial)

	first, ok := planner.Current()
	require.True(t, ok)
	retry, ok := planner.AfterRepair(ctx, first, &core.ProviderError{
		Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
	})
	require.True(t, ok)
	require.Equal(t, first.Key(), retry.Key())

	_, ok = planner.AfterFailure(ctx, retry, &core.ProviderError{
		Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
	})
	require.False(t, ok)
}

type repairPipelineConnector struct {
	mu       sync.Mutex
	systems  []string
	accounts []string
}

func (*repairPipelineConnector) ID() string            { return "openai" }
func (*repairPipelineConnector) Dialect() core.Dialect { return core.DialectOpenAI }
func (c *repairPipelineConnector) Chat(_ context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systems = append(c.systems, req.System)
	c.accounts = append(c.accounts, creds.AccountID)
	if len(c.systems) == 1 {
		return nil, &core.ProviderError{
			Kind:                   core.ErrUpstream,
			Scope:                  core.FailureScopeRequest,
			Message:                "incomplete response",
			RetrySystemInstruction: "complete the previous response",
			AttemptUsage: &core.Usage{
				PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6,
				Source: core.UsageSourceProvider,
			},
		}
	}
	return &core.ChatResponse{
		Model: req.Model,
		Message: core.Message{
			Role:    core.RoleAssistant,
			Content: []core.ContentPart{{Type: core.PartText, Text: "done"}},
		},
		FinishReason: core.FinishStop,
		Usage: core.Usage{
			PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8,
			Source: core.UsageSourceProvider,
		},
	}, nil
}
func (*repairPipelineConnector) Stream(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (<-chan core.StreamChunk, error) {
	ch := make(chan core.StreamChunk)
	close(ch)
	return ch, nil
}

func TestPipelineRepairReroutesAndAccountsForBothAttempts(t *testing.T) {
	conn := &repairPipelineConnector{}
	d := newPlannerDispatcherWithConnector(t, conn, "acc-1", "acc-2")
	p := New(Deps{Dispatcher: d})
	req := &core.ChatRequest{
		Model:  "gpt-4o",
		System: "original",
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}},
		}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID},
	}

	result, err := p.Chat(context.Background(), req, Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	require.Equal(t, "acc-2", result.AccountID)
	require.Equal(t, 11, result.Response.Usage.PromptTokens)
	require.Equal(t, 3, result.Response.Usage.CompletionTokens)
	require.Equal(t, 14, result.Response.Usage.TotalTokens)
	require.Equal(t, "original", req.System)

	conn.mu.Lock()
	defer conn.mu.Unlock()
	require.Equal(t, []string{"acc-1", "acc-2"}, conn.accounts)
	require.Equal(t, []string{
		"original",
		"original\n\ncomplete the previous response",
	}, conn.systems)
}

func TestAttemptPlannerRetriesSameAttemptWhenChainExhausted(t *testing.T) {
	ctx := context.Background()
	d := newPlannerDispatcherWithAccounts(t, "acc-only")
	targets := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}
	initial, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{})
	require.NoError(t, err)

	planner := newAttemptPlanner(d, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{}, initial)
	var slept []time.Duration
	planner.sleep = func(_ context.Context, d time.Duration) bool {
		slept = append(slept, d)
		return true
	}
	first, ok := planner.Current()
	require.True(t, ok)

	transient := &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeProvider, StatusCode: 502}
	retry, ok := planner.AfterFailure(ctx, first, transient)
	require.True(t, ok, "a lone transient fault on a single-account chain is retried")
	require.Equal(t, first.Account.ID, retry.Account.ID)

	retry, ok = planner.AfterFailure(ctx, retry, transient)
	require.True(t, ok)
	require.Equal(t, first.Account.ID, retry.Account.ID)

	_, ok = planner.AfterFailure(ctx, retry, transient)
	require.False(t, ok, "retries are bounded")

	require.Len(t, slept, maxSameAttemptRetries)
	require.GreaterOrEqual(t, slept[0], sameRetryInitialDelay)
	require.LessOrEqual(t, slept[0], sameRetryInitialDelay+sameRetryJitter)
	require.GreaterOrEqual(t, slept[1], 2*sameRetryInitialDelay)
}

func TestAttemptPlannerDoesNotRetrySameAttemptForNonTransientErrors(t *testing.T) {
	ctx := context.Background()
	d := newPlannerDispatcherWithAccounts(t, "acc-only")
	targets := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}
	initial, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{})
	require.NoError(t, err)

	for _, pe := range []*core.ProviderError{
		{Kind: core.ErrAuth},
		{Kind: core.ErrRateLimit},
		{Kind: core.ErrContextWindow},
		{Kind: core.ErrTimeout, Scope: core.FailureScopeRequest, Cause: context.DeadlineExceeded},
	} {
		planner := newAttemptPlanner(d, store.DefaultTenantID, targets, core.NewCapabilitySet(), dispatch.PlanOptions{}, initial)
		planner.sleep = func(context.Context, time.Duration) bool { t.Fatal("must not sleep"); return false }
		first, _ := planner.Current()
		_, ok := planner.AfterFailure(ctx, first, pe)
		require.False(t, ok, "kind %s", pe.Kind)
	}
}

func TestSameRetryDelayHonoursShortRetryAfter(t *testing.T) {
	d := sameRetryDelay(0, 3*time.Second)
	require.GreaterOrEqual(t, d, 3*time.Second)
	require.LessOrEqual(t, d, 3*time.Second+sameRetryJitter)

	d = sameRetryDelay(0, 10*time.Minute) // too long: fall back to exponential
	require.LessOrEqual(t, d, sameRetryInitialDelay+sameRetryJitter)

	d = sameRetryDelay(10, 0)
	require.LessOrEqual(t, d, sameRetryMaxDelay+sameRetryJitter)
}
