package connectors

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKiroModelPrices_Coverage verifies that every Kiro model exposed in the
// catalog has a matching price entry, so usage statistics can report a
// non-zero cost rather than silently defaulting to free.
func TestKiroModelPrices_Coverage(t *testing.T) {
	for _, mdl := range providerModels["kiro"] {
		mp, ok := ModelPriceByProviderModel("kiro", mdl.ID)
		require.Truef(t, ok, "missing price entry for kiro/%s", mdl.ID)
		require.Greaterf(t, mp.InputPerM, 0.0, "input price must be > 0 for kiro/%s", mdl.ID)
		require.Greaterf(t, mp.OutputPerM, 0.0, "output price must be > 0 for kiro/%s", mdl.ID)
	}
}

// TestKiroModelPrices_NewClaudeVersions verifies the Sonnet/Opus 4.5–4.8
// variants (and their synthetic suffixes) resolve to the expected retail rates.
func TestKiroModelPrices_NewClaudeVersions(t *testing.T) {
	cases := []struct {
		model              string
		wantInput, wantOut float64
	}{
		{"claude-sonnet-5", 2, 10},
		{"claude-sonnet-5-thinking-agentic", 2, 10},
		{"claude-sonnet-4.6", 3, 15},
		{"claude-sonnet-4.6-thinking-agentic", 3, 15},
		{"claude-sonnet-4.7", 3, 15},
		{"claude-sonnet-4.8", 3, 15},
		{"claude-opus-4.5", 5, 25},
		{"claude-opus-4.5-thinking", 5, 25},
		{"claude-opus-4.6", 5, 25},
		{"claude-opus-4.7-thinking", 5, 25},
		{"claude-opus-4.8-thinking-agentic", 5, 25},
		{"gpt-5.6-sol-thinking", 4, 20},
	}
	for _, tc := range cases {
		mp, ok := ModelPriceByProviderModel("kiro", tc.model)
		require.Truef(t, ok, "missing price for kiro/%s", tc.model)
		require.Equalf(t, tc.wantInput, mp.InputPerM, "input rate for kiro/%s", tc.model)
		require.Equalf(t, tc.wantOut, mp.OutputPerM, "output rate for kiro/%s", tc.model)
	}
}
