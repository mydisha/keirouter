package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// TestAttemptForTargetsKeepsChainRates proves the cache-hit/local-terminal path
// builds the attempt by copying the source target wholesale, so the four chain
// rates survive (previously only Provider/Model were copied, dropping them).
func TestAttemptForTargetsKeepsChainRates(t *testing.T) {
	targets := []dispatch.Target{{
		Provider: "openai", Model: "gpt-4o",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
	}}
	att := attemptForTargets(targets)
	require.Equal(t, targets[0], att.Target)
	require.Equal(t, float64(2.5), att.Target.InputPerM)
	require.Equal(t, float64(10), att.Target.OutputPerM)
	require.Equal(t, float64(3.125), att.Target.CacheWritePerM)
	require.Equal(t, float64(0.25), att.Target.CacheReadPerM)
	require.Equal(t, dispatch.Target{}, attemptForTargets(nil).Target)
}

// TestPipelineForwardsChainStepPrice proves that the winning attempt's chain
// rates are forwarded through recordOutcomeWithTTFT into the meter and
// persisted as a chain-priced UsageRecord. The meter is built with no catalog
// prices, so only the forwarded rates can price the request.
func TestPipelineForwardsChainStepPrice(t *testing.T) {
	ctx := context.Background()

	db := newPipelineTestDB(t)
	m := meter.New(db.Usage(), nil, nil)
	p := New(Deps{Meter: m})

	att := dispatch.Attempt{
		Target: dispatch.Target{
			Provider:       "openai",
			Model:          "gpt-4o",
			InputPerM:      2.5,
			OutputPerM:     10,
			CacheWritePerM: 3.125,
			CacheReadPerM:  0.25,
		},
		Account: store.Account{ID: "acc-1"},
	}
	meta := core.RequestMetadata{TenantID: store.DefaultTenantID, ChainID: "c1"}

	p.recordOutcomeWithTTFT(ctx, meta, att,
		core.Usage{PromptTokens: 1000, CompletionTokens: 500}, "success", "", false,
		5*time.Millisecond, 6*time.Millisecond, 0, nil)

	// RecentRecord (db.Usage().Recent) does not expose these columns, so read
	// the persisted row directly to prove what the pipeline wrote to the store.
	var (
		rows      int
		pricing   string
		chainID   string
		costNanos int64
	)
	err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM usage_records WHERE tenant_id = ?`, store.DefaultTenantID).Scan(&rows)
	require.NoError(t, err)
	require.Equal(t, 1, rows, "exactly one usage record should be persisted")

	err = db.SQL().QueryRowContext(ctx,
		`SELECT pricing_source, chain_id, cost_nanos FROM usage_records WHERE tenant_id = ?`,
		store.DefaultTenantID).Scan(&pricing, &chainID, &costNanos)
	require.NoError(t, err)

	require.Equal(t, "chain", pricing)
	require.Equal(t, "c1", chainID)
	require.Equal(t, int64(7_500_000), costNanos)
}
