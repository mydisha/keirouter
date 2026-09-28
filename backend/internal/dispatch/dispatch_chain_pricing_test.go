package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestTargetsFromChainSpreadsChainRateToEveryStep(t *testing.T) {
	chain := store.Chain{
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		Steps: []store.ChainStep{
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "anthropic", Model: "claude-3-5-sonnet"},
		},
	}
	targets := TargetsFromChain(chain)
	require.Len(t, targets, 2)
	for i, tgt := range targets {
		require.Equal(t, 2.5, tgt.InputPerM, "target %d input", i)
		require.Equal(t, 10.0, tgt.OutputPerM, "target %d output", i)
		require.Equal(t, 3.125, tgt.CacheWritePerM, "target %d cache write", i)
		require.Equal(t, 0.25, tgt.CacheReadPerM, "target %d cache read", i)
	}
	require.Equal(t, "openai", targets[0].Provider)
	require.Equal(t, "anthropic", targets[1].Provider)
}
