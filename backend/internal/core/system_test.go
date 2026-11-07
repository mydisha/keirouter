package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

var ephemeral = json.RawMessage(`{"type":"ephemeral"}`)

func TestSystemBlocks_SplitAtTheClientBreakpoints(t *testing.T) {
	req := ChatRequest{
		System:      "header" + "static rules" + "today is monday",
		SystemCache: []SystemBlock{{Text: "static rules", CacheControl: ephemeral}},
	}
	require.Equal(t, []SystemBlock{
		{Text: "header"},
		{Text: "static rules", CacheControl: ephemeral},
		{Text: "today is monday"},
	}, req.SystemBlocks())
}

func TestSystemBlocks_SurviveAPrependedHookAndDropSeparators(t *testing.T) {
	req := ChatRequest{
		System: "skill block\n\nfirst\n\nsecond",
		SystemCache: []SystemBlock{
			{Text: "first", CacheControl: ephemeral},
			{Text: "second", CacheControl: ephemeral},
		},
	}
	require.Equal(t, []SystemBlock{
		{Text: "skill block\n\n"},
		{Text: "first", CacheControl: ephemeral},
		{Text: "second", CacheControl: ephemeral},
	}, req.SystemBlocks())
}

func TestSystemBlocks_RewrittenPromptKeepsOneBreakpoint(t *testing.T) {
	req := ChatRequest{
		System:      "redacted by a guardrail",
		SystemCache: []SystemBlock{{Text: "original", CacheControl: ephemeral}},
	}
	require.Equal(t, []SystemBlock{{Text: "redacted by a guardrail", CacheControl: ephemeral}}, req.SystemBlocks())
}

func TestSystemBlocks_EmptyAndUncached(t *testing.T) {
	require.Nil(t, (&ChatRequest{System: "  "}).SystemBlocks())
	require.Equal(t, []SystemBlock{{Text: "plain"}}, (&ChatRequest{System: "plain"}).SystemBlocks())
}
