package capability

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolve_Opus4And41OutputLimit(t *testing.T) {
	for _, model := range []string{"claude-opus-4-1-20250805", "claude-opus-4-20250514", "claude-opus-4-1", "claude-opus-4", "us.anthropic.claude-opus-4-1-20250805-v1:0"} {
		require.Equal(t, 32000, ResolveProfile("anthropic", model).MaxOutput, model)
	}
	require.Equal(t, 64000, ResolveProfile("anthropic", "claude-opus-4-5").MaxOutput)
	require.Equal(t, 128000, ResolveProfile("anthropic", "claude-opus-4-6").MaxOutput)
}
