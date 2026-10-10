package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

type fakeConnectorSource struct {
	conn core.Connector
}

func (s fakeConnectorSource) Get(provider string) (core.Connector, error) {
	return s.conn, nil
}

type fakeConnector struct{}

func (fakeConnector) ID() string            { return "openai" }
func (fakeConnector) Dialect() core.Dialect { return core.DialectOpenAI }
func (fakeConnector) Chat(context.Context, *core.ChatRequest, core.Credentials) (*core.ChatResponse, error) {
	return nil, nil
}
func (fakeConnector) Stream(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (<-chan core.StreamChunk, error) {
	ch := make(chan core.StreamChunk)
	close(ch)
	return ch, nil
}

func newDispatchTest(t *testing.T, accounts ...store.Account) (*Dispatcher, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	v := vault.New(sealer)

	for i := range accounts {
		require.NoError(t, v.Seal(&accounts[i], vault.NewSecret{APIKey: "sk-test"}))
		require.NoError(t, db.Accounts().Create(ctx, accounts[i]))
	}

	d := New(fakeConnectorSource{conn: fakeConnector{}}, db.Accounts(), v)
	d.SetRoutingSource(db.Routing())
	return d, db
}

func testAccount(id string, priority int) store.Account {
	now := time.Now()
	return store.Account{
		ID:        id,
		TenantID:  store.DefaultTenantID,
		Provider:  "openai",
		Label:     id,
		AuthKind:  store.AuthAPIKey,
		Priority:  priority,
		Metadata:  "{}",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestPlanWith_AccountRoundRobinRotatesProviderAccounts(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t,
		testAccount("acc-1", 10),
		testAccount("acc-2", 20),
		testAccount("acc-3", 30),
	)

	targets := []Target{{Provider: "openai", Model: "gpt-4o"}}
	opts := PlanOptions{AccountStrategy: StrategyRoundRobin}

	var got []string
	for i := 0; i < 4; i++ {
		attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
		require.NoError(t, err)
		require.NotEmpty(t, attempts)
		got = append(got, attempts[0].Account.ID)
	}

	require.Equal(t, []string{"acc-1", "acc-2", "acc-3", "acc-1"}, got)
}

func TestPlanWith_AccountRoundRobinHonorsStickyLimit(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t,
		testAccount("acc-1", 10),
		testAccount("acc-2", 20),
	)

	targets := []Target{{Provider: "openai", Model: "gpt-4o"}}
	opts := PlanOptions{AccountStrategy: StrategyRoundRobin, AccountStickyLimit: 2}

	var got []string
	for i := 0; i < 5; i++ {
		attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
		require.NoError(t, err)
		require.NotEmpty(t, attempts)
		got = append(got, attempts[0].Account.ID)
	}

	require.Equal(t, []string{"acc-1", "acc-1", "acc-2", "acc-2", "acc-1"}, got)
}

func TestPlanWith_SmartRoundRobinPinsAffinityKey(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t,
		testAccount("acc-1", 10),
		testAccount("acc-2", 20),
		testAccount("acc-3", 30),
	)

	targets := []Target{{Provider: "openai", Model: "gpt-4o"}}
	opts := PlanOptions{AccountStrategy: StrategySmartRoundRobin, AccountAffinityKey: "thread-a"}

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
	require.NoError(t, err)
	require.Equal(t, "acc-1", attempts[0].Account.ID)

	attempts, err = d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
	require.NoError(t, err)
	require.Equal(t, "acc-1", attempts[0].Account.ID)

	opts.AccountAffinityKey = "thread-b"
	attempts, err = d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
	require.NoError(t, err)
	require.Equal(t, "acc-2", attempts[0].Account.ID)

	opts.AccountAffinityKey = "thread-a"
	attempts, err = d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
	require.NoError(t, err)
	require.Equal(t, "acc-1", attempts[0].Account.ID)
}

func TestPlanWith_ProviderAccountStrategyOverride(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t,
		testAccount("acc-1", 10),
		testAccount("acc-2", 20),
	)

	targets := []Target{{Provider: "openai", Model: "gpt-4o"}}
	opts := PlanOptions{
		AccountStrategy: StrategyFallback,
		ProviderAccountStrategies: map[string]AccountRoutingOptions{
			"openai": {Strategy: StrategyRoundRobin},
		},
	}

	var got []string
	for i := 0; i < 3; i++ {
		attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
		require.NoError(t, err)
		got = append(got, attempts[0].Account.ID)
	}

	require.Equal(t, []string{"acc-1", "acc-2", "acc-1"}, got)
}

func TestPlanWith_TargetRoundRobinRotatesComboTargets(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-1", 10))
	now := time.Now()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID:        "chain-1",
		TenantID:  store.DefaultTenantID,
		Name:      "combo",
		Strategy:  string(StrategyRoundRobin),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	targets := []Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "openai", Model: "gpt-5"},
	}
	opts := PlanOptions{Strategy: StrategyRoundRobin, ChainID: "chain-1"}

	var got []string
	for i := 0; i < 3; i++ {
		attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), opts)
		require.NoError(t, err)
		got = append(got, attempts[0].Target.Model)
	}

	require.Equal(t, []string{"gpt-4o", "gpt-5", "gpt-4o"}, got)
}

func TestPlanWith_CustomOpenAISkipsCapabilityGuard(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccountWithProvider("acc-custom", "custom-openai", 10))

	// The built-in "custom-openai" generic gateway has unknown upstream
	// capabilities, so the guard must be relaxed for it too.
	required := core.NewCapabilitySet(core.CapVision)
	targets := []Target{{Provider: "custom-openai", Model: "glm-5.2"}}

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, required, PlanOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, attempts)
	require.Equal(t, "custom-openai", attempts[0].Target.Provider)
}

func TestPlanWith_CustomProviderSkipsCapabilityGuard(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccountWithProvider("acc-custom", "custom-openai-bandel", 10))

	// glm-5.2 on a custom provider lacks vision in its resolved profile.
	// A vision-required request would normally be rejected, but custom
	// providers bypass the guard because their upstream capabilities are
	// unknown — the pipeline soft-degrades modalities instead.
	required := core.NewCapabilitySet(core.CapVision)
	targets := []Target{{Provider: "custom-openai-bandel", Model: "glm-5.2"}}

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, required, PlanOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, attempts)
	require.Equal(t, "custom-openai-bandel", attempts[0].Target.Provider)
	require.Equal(t, "glm-5.2", attempts[0].Target.Model)
}

func TestPlanWith_BuiltInProviderEnforcesHardCapabilityGuard(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccountWithProvider("acc-2", "openai", 10))

	// gpt-image-1 resolves to a profile with NoTools. A tool-calling
	// requirement is non-strippable (hard), so the guard must reject it.
	required := core.NewCapabilitySet(core.CapToolCalling)
	targets := []Target{{Provider: "openai", Model: "gpt-image-1"}}

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, required, PlanOptions{})
	require.Error(t, err)
	require.Empty(t, attempts)
	require.Contains(t, err.Error(), "lacks required capabilities")
}

func TestPlanWith_BuiltInProviderVisionStrippableSkipsGuard(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccountWithProvider("acc-3", "openai", 10))

	// glm-5.2 resolves to a profile without vision. Vision is strippable
	// (soft-degraded by the pipeline), so the dispatch guard must NOT reject
	// it — the pipeline will strip images instead.
	required := core.NewCapabilitySet(core.CapVision)
	targets := []Target{{Provider: "openai", Model: "glm-5.2"}}

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, required, PlanOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, attempts)
}

func TestNoteFailureHonorsRateLimitRetryAfter(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-rate-limited", 10))
	started := time.Now()

	d.NoteFailure(ctx, "acc-rate-limited", &core.ProviderError{
		Kind:       core.ErrRateLimit,
		RetryAfter: 4 * time.Minute,
	})

	account, err := db.Accounts().Get(ctx, "acc-rate-limited")
	require.NoError(t, err)
	require.NotNil(t, account.CooldownUntil)
	require.WithinDuration(t, started.Add(4*time.Minute), *account.CooldownUntil, 2*time.Second)
}

func TestNoteFailureDedupsConcurrentFailures(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-burst", 10))

	// A burst of parallel requests failing on the same account must advance
	// the backoff level once, not once per request.
	for range 5 {
		d.NoteFailure(ctx, "acc-burst", &core.ProviderError{Kind: core.ErrRateLimit})
	}

	account, err := db.Accounts().Get(ctx, "acc-burst")
	require.NoError(t, err)
	require.Equal(t, 1, account.BackoffLevel)
	require.NotNil(t, account.CooldownUntil)
}

func TestNoteFailureDedupSuppressedStillReportsRetryAfter(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-hint", 10))

	first := &core.ProviderError{Kind: core.ErrRateLimit, RetryAfter: 4 * time.Minute}
	d.NoteFailure(ctx, "acc-hint", first)

	// The suppressed failure reuses the cooldown set by the first one so the
	// caller still surfaces an accurate retry hint.
	second := &core.ProviderError{Kind: core.ErrRateLimit}
	d.NoteFailure(ctx, "acc-hint", second)
	require.Greater(t, second.RetryAfter, 3*time.Minute)
}

func TestNoteSuccessResetsFailureDedup(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-recover", 10))

	d.NoteFailure(ctx, "acc-recover", &core.ProviderError{Kind: core.ErrRateLimit})
	d.NoteSuccess(ctx, "openai", "acc-recover", "gpt-4o")

	// After a success the next failure is a fresh incident: it must apply a
	// cooldown again instead of being swallowed by the dedup window.
	d.NoteFailure(ctx, "acc-recover", &core.ProviderError{Kind: core.ErrRateLimit})

	account, err := db.Accounts().Get(ctx, "acc-recover")
	require.NoError(t, err)
	require.Equal(t, 1, account.BackoffLevel)
	require.NotNil(t, account.CooldownUntil)
	require.True(t, account.CooldownUntil.After(time.Now()))
}

func TestNonCoolingErrorDoesNotConsumeDedupWindow(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-mixed", 10))

	// A client cancellation never cools down and must not suppress the real
	// rate-limit failure that follows it.
	d.NoteFailure(ctx, "acc-mixed", &core.ProviderError{Kind: core.ErrClientCanceled})
	d.NoteFailure(ctx, "acc-mixed", &core.ProviderError{Kind: core.ErrRateLimit})

	account, err := db.Accounts().Get(ctx, "acc-mixed")
	require.NoError(t, err)
	require.NotNil(t, account.CooldownUntil)
}

func TestNoteFailureCreditsExhaustedParksAccount(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-dry", 10))

	d.NoteFailure(ctx, "acc-dry", &core.ProviderError{
		Kind:             core.ErrQuotaExhausted,
		CreditsExhausted: true,
	})

	account, err := db.Accounts().Get(ctx, "acc-dry")
	require.NoError(t, err)
	require.True(t, account.CreditsExhausted, "a dry balance must flag the account")
	require.NotNil(t, account.CooldownUntil)
	require.WithinDuration(t, time.Now().Add(CreditsExhaustedCooldown), *account.CooldownUntil, time.Minute,
		"a dry balance must park the account for the full credits cooldown")

	// A later success (the user topped up) restores the account fully.
	d.NoteSuccess(ctx, "openai", "acc-dry", "gpt-4o")
	account, err = db.Accounts().Get(ctx, "acc-dry")
	require.NoError(t, err)
	require.False(t, account.CreditsExhausted)
	require.Nil(t, account.CooldownUntil)
}

func TestNoteFailureCreditsExhaustedBypassesDedup(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-dry-burst", 10))

	// A rate-limit failure consumes the dedup window first; the terminal
	// credits failure arriving inside the window must still park and flag
	// the account instead of being collapsed into the short cooldown.
	d.NoteFailure(ctx, "acc-dry-burst", &core.ProviderError{Kind: core.ErrRateLimit})
	d.NoteFailure(ctx, "acc-dry-burst", &core.ProviderError{
		Kind:             core.ErrQuotaExhausted,
		CreditsExhausted: true,
	})

	account, err := db.Accounts().Get(ctx, "acc-dry-burst")
	require.NoError(t, err)
	require.True(t, account.CreditsExhausted, "the terminal credits flag must not be deduped away")
	require.NotNil(t, account.CooldownUntil)
	require.WithinDuration(t, time.Now().Add(CreditsExhaustedCooldown), *account.CooldownUntil, time.Minute,
		"the 24h park must replace the short rate-limit cooldown")
}

func TestNoteFailurePlainQuotaDoesNotFlagCredits(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-quota", 10))

	// A calendar-window quota (daily/monthly) is not a dry balance: cooldown
	// applies but the terminal credits flag must stay clear.
	d.NoteFailure(ctx, "acc-quota", &core.ProviderError{Kind: core.ErrQuotaExhausted})

	account, err := db.Accounts().Get(ctx, "acc-quota")
	require.NoError(t, err)
	require.False(t, account.CreditsExhausted)
	require.NotNil(t, account.CooldownUntil)
}

func TestWithJitterStaysWithinBounds(t *testing.T) {
	base := 100 * time.Second
	limit := base + time.Duration(float64(base)*BackoffJitterFraction)
	for range 100 {
		got := withJitter(base)
		require.GreaterOrEqual(t, got, base)
		require.LessOrEqual(t, got, limit)
	}
	require.Equal(t, time.Duration(0), withJitter(0))
}

func TestPlanWith_AllowedAccountPinsCredential(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t,
		testAccount("acc-1", 10),
		testAccount("acc-2", 20),
	)

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID,
		[]Target{{Provider: "openai", Model: "gpt-4o"}},
		core.NewCapabilitySet(),
		PlanOptions{AllowedAccountIDs: map[string]struct{}{"acc-2": {}}},
	)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, "acc-2", attempts[0].Account.ID)
}

func TestPlanWith_UnknownAllowedAccountIsRequestError(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-1", 10))

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID,
		[]Target{{Provider: "openai", Model: "gpt-4o"}},
		core.NewCapabilitySet(),
		PlanOptions{AllowedAccountIDs: map[string]struct{}{"missing": {}}},
	)
	require.Empty(t, attempts)
	pe := core.AsProviderError(err)
	require.Equal(t, core.ErrBadRequest, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.EffectiveScope())
}

func TestPlanWith_ExcludedAttemptStillAllowsOtherModelOnSameAccount(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-1", 10))
	targets := []Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "openai", Model: "gpt-5"},
	}

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(),
		PlanOptions{ExcludedAttempts: map[AttemptKey]struct{}{
			{Provider: "openai", Model: "gpt-4o", AccountID: "acc-1"}: {},
		}},
	)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, "gpt-5", attempts[0].Target.Model)
	require.Equal(t, "acc-1", attempts[0].Account.ID)
}

func TestPlanWith_AllCandidatesCoolingReturnsTypedRetry(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-1", 10))
	d.NoteFailure(ctx, "acc-1", &core.ProviderError{
		Kind:       core.ErrRateLimit,
		Provider:   "openai",
		Model:      "gpt-4o",
		RetryAfter: 2 * time.Second,
	})

	attempts, err := d.PlanWith(ctx, store.DefaultTenantID,
		[]Target{{Provider: "openai", Model: "gpt-4o"}},
		core.NewCapabilitySet(), PlanOptions{})
	require.Empty(t, attempts)
	pe := core.AsProviderError(err)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, core.FailureScopeAccount, pe.EffectiveScope())
	require.Greater(t, pe.RetryAfter, time.Duration(0))
	// The upstream Retry-After is a lower bound; the local cooldown may extend
	// it by up to BackoffJitterFraction.
	maxRetry := time.Duration(float64(2*time.Second) * (1 + BackoffJitterFraction))
	require.LessOrEqual(t, pe.RetryAfter, maxRetry)
}

func TestProviderCircuitOpensAndSuccessClosesIt(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-1", 10))
	for i := 0; i < ProviderCircuitFailureThreshold; i++ {
		d.NoteFailure(ctx, "acc-1", &core.ProviderError{
			Kind:     core.ErrUpstream,
			Scope:    core.FailureScopeProvider,
			Provider: "openai",
		})
	}

	require.Greater(t, d.providerCircuitRemaining("openai", time.Now()), time.Duration(0))
	d.NoteSuccess(ctx, "openai", "acc-1", "gpt-4o")
	require.Zero(t, d.providerCircuitRemaining("openai", time.Now()))
}

func testAccountWithProvider(id, provider string, priority int) store.Account {
	now := time.Now()
	return store.Account{
		ID:        id,
		TenantID:  store.DefaultTenantID,
		Provider:  provider,
		Label:     id,
		AuthKind:  store.AuthAPIKey,
		Priority:  priority,
		Metadata:  "{}",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestAdvanceRotationStateHonorsStickyLimit(t *testing.T) {
	cursor, nextCursor, hits := advanceRotationState(3, 0, 0, 2)
	require.Equal(t, 0, cursor)
	require.Equal(t, 0, nextCursor)
	require.Equal(t, 1, hits)

	cursor, nextCursor, hits = advanceRotationState(3, nextCursor, hits, 2)
	require.Equal(t, 0, cursor)
	require.Equal(t, 1, nextCursor)
	require.Equal(t, 0, hits)
}

func TestNoteFailureTransientNeedsRepeatedStrikes(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-blip", 10))

	upstream := func() *core.ProviderError {
		return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeProvider, StatusCode: 502}
	}
	// Two isolated 502s are a blip: the only account must stay in rotation.
	for range TransientFailureThreshold - 1 {
		d.NoteFailure(ctx, "acc-blip", upstream())
		account, err := db.Accounts().Get(ctx, "acc-blip")
		require.NoError(t, err)
		require.Nil(t, account.CooldownUntil)
	}
	// The third strike inside the window benches it.
	d.NoteFailure(ctx, "acc-blip", upstream())
	account, err := db.Accounts().Get(ctx, "acc-blip")
	require.NoError(t, err)
	require.NotNil(t, account.CooldownUntil)

	// A 429 still cools down immediately: the provider told us to back off.
	d2, db2 := newDispatchTest(t, testAccount("acc-429", 10))
	d2.NoteFailure(ctx, "acc-429", &core.ProviderError{Kind: core.ErrRateLimit})
	account, err = db2.Accounts().Get(ctx, "acc-429")
	require.NoError(t, err)
	require.NotNil(t, account.CooldownUntil)
}

func TestNoteSuccessKeepsCooldownSetAfterAttemptStarted(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-race", 10))

	attemptStarted := time.Now().Add(-time.Second)
	// A concurrent request hit a rate limit after our attempt began.
	d.NoteFailure(ctx, "acc-race", &core.ProviderError{Kind: core.ErrRateLimit, RetryAfter: time.Minute})
	account, err := db.Accounts().Get(ctx, "acc-race")
	require.NoError(t, err)
	require.NotNil(t, account.CooldownUntil)

	// Our older in-flight request completing must not erase that cooldown.
	d.NoteSuccessAt(ctx, "openai", "acc-race", "gpt-4o", attemptStarted)
	account, err = db.Accounts().Get(ctx, "acc-race")
	require.NoError(t, err)
	require.NotNil(t, account.CooldownUntil, "success that predates the failure must not clear it")

	// A request that started after the failure proves recovery.
	d.NoteSuccessAt(ctx, "openai", "acc-race", "gpt-4o", time.Now())
	account, err = db.Accounts().Get(ctx, "acc-race")
	require.NoError(t, err)
	require.Nil(t, account.CooldownUntil)
}

func TestNoteFailureContextWindowNeverCoolsDown(t *testing.T) {
	ctx := context.Background()
	d, db := newDispatchTest(t, testAccount("acc-ctx", 10))
	d.NoteFailure(ctx, "acc-ctx", &core.ProviderError{Kind: core.ErrContextWindow, Model: "gpt-4o"})
	account, err := db.Accounts().Get(ctx, "acc-ctx")
	require.NoError(t, err)
	require.Nil(t, account.CooldownUntil)
	active, err := db.Routing().IsModelCooldownActive(ctx, "acc-ctx", "gpt-4o")
	require.NoError(t, err)
	require.False(t, active)
}

func TestPlanWithLimitStopsAfterFirstHealthyAttempt(t *testing.T) {
	ctx := context.Background()
	d, _ := newDispatchTest(t, testAccount("acc-1", 10), testAccount("acc-2", 20), testAccount("acc-3", 30))
	targets := []Target{{Provider: "openai", Model: "gpt-4o"}}
	attempts, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), PlanOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, "acc-1", attempts[0].Account.ID)

	all, err := d.PlanWith(ctx, store.DefaultTenantID, targets, core.NewCapabilitySet(), PlanOptions{})
	require.NoError(t, err)
	require.Len(t, all, 3)
}

func TestProviderCircuitHalfOpenAdmitsSingleProbe(t *testing.T) {
	d, _ := newDispatchTest(t, testAccount("acc-1", 10))
	now := time.Now()
	d.clock = func() time.Time { return now }
	for range ProviderCircuitFailureThreshold {
		d.recordProviderFailure("openai")
	}
	require.Greater(t, d.providerCircuitRemaining("openai", now), time.Duration(0), "circuit is open")

	// Once the open interval elapses exactly one caller is admitted as the
	// probe; the next caller still waits.
	later := now.Add(ProviderCircuitBaseCooldown + time.Second)
	require.Equal(t, time.Duration(0), d.providerCircuitRemaining("openai", later))
	require.Greater(t, d.providerCircuitRemaining("openai", later), time.Duration(0))

	// A failed probe re-opens the circuit immediately, for longer.
	now = later
	d.recordProviderFailure("openai")
	require.Greater(t, d.providerCircuitRemaining("openai", later.Add(ProviderCircuitBaseCooldown+time.Second)), time.Duration(0))

	// A successful probe closes it for everyone.
	d.recordProviderSuccess("openai")
	require.Equal(t, time.Duration(0), d.providerCircuitRemaining("openai", later.Add(time.Hour)))
}
