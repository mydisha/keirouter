package pipeline

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
)

// Only surplus-trusted steps are marketplace-routed; the flag must not leak
// onto direct provider steps, and the scope is left alone so same-attempt
// retries of transient faults still apply.
func TestProviderErrorForAttemptMarksMarketRoutes(t *testing.T) {
	upstream := &core.ProviderError{Kind: core.ErrUpstream, StatusCode: 503}

	trusted := dispatch.Attempt{Target: dispatch.Target{Provider: "custom-openai-surplus", Model: "glm-5.3-flash", MarketSlug: "surplus-trusted:glm-5.3-flash"}}
	pe := providerErrorForAttempt(upstream, trusted)
	require.True(t, pe.MarketRouted)
	require.Equal(t, core.FailureScopeProvider, pe.EffectiveScope())
	require.False(t, upstream.MarketRouted, "the shared upstream error must not be mutated")

	direct := dispatch.Attempt{Target: dispatch.Target{Provider: "custom-openai-inferhub", Model: "ali/deepseek-v4.1-flash", MarketSlug: "ali/deepseek-v4.1-flash"}}
	require.False(t, providerErrorForAttempt(upstream, direct).MarketRouted)
	require.False(t, providerErrorForAttempt(errors.New("boom"), dispatch.Attempt{}).MarketRouted)
}
