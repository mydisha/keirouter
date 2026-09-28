package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestTargetsFromChainCarriesPricing(t *testing.T) {
	chain := store.Chain{Steps: []store.ChainStep{
		{Provider: "openai", Model: "gpt-4o", InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25},
	}}
	targets := TargetsFromChain(chain)
	require.Len(t, targets, 1)
	require.Equal(t, 2.5, targets[0].InputPerM)
	require.Equal(t, 10.0, targets[0].OutputPerM)
	require.Equal(t, 3.125, targets[0].CacheWritePerM)
	require.Equal(t, 0.25, targets[0].CacheReadPerM)
}
