package market

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOpenRouterReferences(t *testing.T) {
	refs, err := ParseOpenRouter(strings.NewReader(`{"data":[
	  {"id":"deepseek/deepseek-v4.1-flash","pricing":{"prompt":"0.00000036","completion":"0.00000684"}},
	  {"id":"deepseek/deepseek-v4.1-flash:free","pricing":{"prompt":"0","completion":"0"}},
	  {"id":"openrouter/auto","pricing":{"prompt":"-1","completion":"-1"}},
	  {"id":"a/shared","pricing":{"prompt":"0.000001","completion":"0.000002"}},
	  {"id":"b/shared","pricing":{"prompt":"0.000003","completion":"0.000004"}}
	]}`))
	require.NoError(t, err)

	c := NewReferenceCache()
	c.Replace(refs)
	for _, name := range []string{"deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash", "DeepSeek-V4_1-Flash"} {
		ref, ok := c.Lookup(name)
		require.True(t, ok, name)
		require.InDelta(t, 0.36, ref.InputPerM, 1e-9)
		require.InDelta(t, 6.84, ref.OutputPerM, 1e-9)
	}
	_, ok := c.Lookup("auto")
	require.False(t, ok, "variable-price router models are skipped")

	// A bare name priced differently by two vendors is ambiguous; the full
	// ids still resolve.
	_, ok = c.Lookup("shared")
	require.False(t, ok)
	ref, ok := c.Lookup("b/shared")
	require.True(t, ok)
	require.InDelta(t, 3.0, ref.InputPerM, 1e-9)
}

func TestParseSurplusDirectPrice(t *testing.T) {
	models, err := ParseSurplus(strings.NewReader(`{"markets":[
	  {"model":"m","best_input_per_1m":144,"best_output_per_1m":2735,"direct_input_per_1m":36000,"direct_output_per_1m":684000}
	]}`))
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.InDelta(t, 0.036, models[0].DirectIn, 1e-12)
	require.InDelta(t, 0.684, models[0].DirectOut, 1e-12)
}
