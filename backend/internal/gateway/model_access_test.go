package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// newModelAccessTestServer returns a server backed by an in-memory DB with a
// single API key whose allowed-model patterns are `allowed`.
func newModelAccessTestServer(t *testing.T, allowed []string) (*Server, string) {
	t.Helper()
	s, db := newCustomProviderTestServer(t)
	s.identity = identity.New(db.APIKeys())

	keyID := "key-model-access"
	require.NoError(t, db.APIKeys().Create(context.Background(), store.APIKey{
		ID: keyID, TenantID: store.DefaultTenantID, Name: "test", CreatedAt: time.Now(),
	}))
	require.NoError(t, db.APIKeys().SetAllowedModels(context.Background(), keyID, allowed))
	return s, keyID
}

// TestFilterAllowedTargetsGrantsWholeChain proves that allowing a chain by name
// permits every step the chain resolves to, rather than requiring each step's
// model id to be listed. Without this, allow-listing a chain (the only handle a
// user sees) would 403 on the resolved provider models.
func TestFilterAllowedTargetsGrantsWholeChain(t *testing.T) {
	s, keyID := newModelAccessTestServer(t, []string{"my-combo"})
	targets := []dispatch.Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}

	got, err := s.filterAllowedTargets(context.Background(), keyID, "my-combo", true, targets)
	require.NoError(t, err)
	require.Len(t, got, 2, "bare chain name grants every resolved step")

	got, err = s.filterAllowedTargets(context.Background(), keyID, "chain:my-combo", true, targets)
	require.NoError(t, err)
	require.Len(t, got, 2, "chain: prefix grants every resolved step")
}

// TestFilterAllowedTargetsChainNameDoesNotLeakToProviderModels proves the chain
// grant is scoped to chain-resolved requests: the same allowed name must not
// unlock a direct "provider/model" call.
func TestFilterAllowedTargetsChainNameDoesNotLeakToProviderModels(t *testing.T) {
	s, keyID := newModelAccessTestServer(t, []string{"my-combo"})
	targets := []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}

	got, err := s.filterAllowedTargets(context.Background(), keyID, "openai/gpt-4o", false, targets)
	require.NoError(t, err)
	require.Empty(t, got, "non-chain request does not inherit a chain-name grant")
}

// TestFilterAllowedTargetsStepModelStillWorks covers the pre-existing path: a
// directly allowed provider model passes on its own for a direct request.
func TestFilterAllowedTargetsStepModelStillWorks(t *testing.T) {
	s, keyID := newModelAccessTestServer(t, []string{"gpt-4o"})
	targets := []dispatch.Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}

	got, err := s.filterAllowedTargets(context.Background(), keyID, "openai/gpt-4o", false, targets)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "gpt-4o", got[0].Model)
}

// TestFilterAllowedTargetsWildcardOnStepsStillWorks covers wildcard patterns
// applied to the resolved step models of a chain.
func TestFilterAllowedTargetsWildcardOnStepsStillWorks(t *testing.T) {
	s, keyID := newModelAccessTestServer(t, []string{"claude-*"})
	targets := []dispatch.Target{
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		{Provider: "openai", Model: "gpt-4o"},
	}

	got, err := s.filterAllowedTargets(context.Background(), keyID, "some-chain", true, targets)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "claude-sonnet-4-6", got[0].Model)
}
