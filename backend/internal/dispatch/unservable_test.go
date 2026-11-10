package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// knownOnlyConnectorSource mimics the real registry: providers it does not
// know resolve to an error instead of a connector.
type knownOnlyConnectorSource struct{ known map[string]bool }

func (s knownOnlyConnectorSource) Get(provider string) (core.Connector, error) {
	if !s.known[provider] {
		return nil, fmt.Errorf("connectors: no connector for provider %q", provider)
	}
	return fakeConnector{}, nil
}

func newUnservableTest(t *testing.T, accounts ...store.Account) *Dispatcher {
	t.Helper()
	d, db := newDispatchTest(t, accounts...)
	d.conns = knownOnlyConnectorSource{known: map[string]bool{"openai": true, "mistral": true}}
	_ = db
	return d
}

func planErr(t *testing.T, d *Dispatcher, required core.CapabilitySet, targets ...Target) *core.ProviderError {
	t.Helper()
	attempts, err := d.PlanWith(context.Background(), store.DefaultTenantID, targets, required, PlanOptions{})
	require.Error(t, err)
	require.Empty(t, attempts)
	return core.AsProviderError(err)
}

func TestPlanWith_UnknownProviderIsModelUnavailable(t *testing.T) {
	d := newUnservableTest(t, testAccount("acc-1", 10))

	pe := planErr(t, d, core.NewCapabilitySet(), Target{Provider: "nope", Model: "nonexistent"})

	require.Equal(t, core.ErrModelUnavailable, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.EffectiveScope())
	require.Contains(t, pe.Message, "no connector")
}

func TestPlanWith_ProviderWithoutAccountsIsModelUnavailable(t *testing.T) {
	d := newUnservableTest(t, testAccount("acc-1", 10))

	pe := planErr(t, d, core.NewCapabilitySet(), Target{Provider: "mistral", Model: "mistral-large"})

	require.Equal(t, core.ErrModelUnavailable, pe.Kind)
	require.Contains(t, pe.Message, "no accounts configured")
}

func TestPlanWith_ChainOfOnlyUnservableTargetsIsModelUnavailable(t *testing.T) {
	d := newUnservableTest(t, testAccount("acc-1", 10))

	pe := planErr(t, d, core.NewCapabilitySet(core.CapToolCalling),
		Target{Provider: "nope", Model: "x"},
		Target{Provider: "mistral", Model: "mistral-large"},
		Target{Provider: "openai", Model: "gpt-image-1"},
	)

	require.Equal(t, core.ErrModelUnavailable, pe.Kind)
}

func TestPlanWith_OnlyIncapableTargetsIsCapabilityError(t *testing.T) {
	d := newUnservableTest(t, testAccountWithProvider("acc-2", "openai", 10))

	pe := planErr(t, d, core.NewCapabilitySet(core.CapToolCalling), Target{Provider: "openai", Model: "gpt-image-1"})

	require.Equal(t, core.ErrCapability, pe.Kind)
	require.Contains(t, pe.Message, "lacks required capabilities")
}

func TestPlanWith_UnservableTargetDoesNotMaskCooldown(t *testing.T) {
	acc := testAccount("acc-1", 10)
	until := time.Now().Add(time.Minute)
	acc.CooldownUntil = &until
	d := newUnservableTest(t, acc)

	pe := planErr(t, d, core.NewCapabilitySet(),
		Target{Provider: "nope", Model: "x"},
		Target{Provider: "openai", Model: "gpt-4o"},
	)

	require.Equal(t, core.ErrRateLimit, pe.Kind, "a configured target on cooldown must still surface as a retryable rate limit")
	require.Positive(t, pe.RetryAfter)
}

func TestPlanWith_ExhaustedConfiguredTargetStaysInternal(t *testing.T) {
	d := newUnservableTest(t, testAccount("acc-1", 10))
	target := Target{Provider: "openai", Model: "gpt-4o"}

	attempts, err := d.PlanWith(context.Background(), store.DefaultTenantID,
		[]Target{{Provider: "nope", Model: "x"}, target},
		core.NewCapabilitySet(),
		PlanOptions{ExcludedAttempts: map[AttemptKey]struct{}{{Provider: "openai", Model: "gpt-4o", AccountID: "acc-1"}: {}}},
	)

	require.Empty(t, attempts)
	require.Equal(t, core.ErrInternal, core.AsProviderError(err).Kind,
		"a chain whose servable targets were already attempted is not a client error")
}
