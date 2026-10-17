package market

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const modelsDevFixture = `{
  "deepseek": {"models": {
    "deepseek-v4-flash": {"id":"deepseek-v4-flash","canonical_model_id":"deepseek/deepseek-v4.1-flash",
      "cost":{"input":0.15,"output":0.6},"limit":{"context":1000000,"output":393216}}
  }},
  "reseller-a": {"models": {
    "deepseek/deepseek-v4.1-flash": {"id":"deepseek/deepseek-v4.1-flash","canonical_model_id":"deepseek/deepseek-v4.1-flash",
      "cost":{"input":0.3,"output":1.2},"limit":{"context":1048576}},
    "glm-x": {"id":"glm-x","cost":{"input":0.4,"output":2},"limit":{"context":131072}},
    "glm-x:free": {"id":"glm-x:free","cost":{"input":0,"output":0},"limit":{"context":131072}}
  }},
  "reseller-b": {"models": {
    "glm-x": {"id":"glm-x","cost":{"input":0.2,"output":1},"limit":{"context":200000}}
  }},
  "reseller-c": {"models": {
    "zai/glm-x": {"id":"zai/glm-x","cost":{"input":0.2,"output":1},"limit":{"context":200000}}
  }},
  "zhipuai": {"models": {
    "glm-5.3-flash": {"id":"glm-5.3-flash","canonical_model_id":"zhipuai/glm-5.3-flash","cost":{"input":0.15,"output":0.5},"limit":{"context":1000000}},
    "glm-5.3-flashx": {"id":"glm-5.3-flashx","canonical_model_id":"zhipuai/glm-5.3-flash","cost":{"input":0.37,"output":1.25},"limit":{"context":1000000}}
  }}
}`

func parseFixture(t *testing.T) *ReferenceCache {
	t.Helper()
	refs, err := ParseModelsDev(strings.NewReader(modelsDevFixture))
	require.NoError(t, err)
	c := NewReferenceCache()
	c.Replace(refs)
	return c
}

func TestModelsDevPrefersVendorListing(t *testing.T) {
	c := parseFixture(t)
	for _, name := range []string{"deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash", "DeepSeek-V4_1-Flash"} {
		ref, ok := c.Lookup(name)
		require.True(t, ok, name)
		require.Equal(t, Reference{InputPerM: 0.15, OutputPerM: 0.6, ContextTokens: 1000000, Provider: "deepseek"}, ref,
			"the vendor's own listing (matched by canonical id) wins over resellers")
	}
}

func TestModelsDevPrefersExactVendorID(t *testing.T) {
	ref, ok := parseFixture(t).Lookup("glm-5.3-flash")
	require.True(t, ok)
	require.Equal(t, 0.15, ref.InputPerM, "glm-5.3-flashx shares the canonical id but is a different SKU")
	require.Equal(t, "zhipuai", ref.Provider)
}

func TestModelsDevFallsBackToMostCommonPrice(t *testing.T) {
	ref, ok := parseFixture(t).Lookup("glm-x")
	require.True(t, ok)
	require.Equal(t, Reference{InputPerM: 0.2, OutputPerM: 1, ContextTokens: 200000}, ref,
		"no vendor listing: the price two providers share beats the outlier; the free variant is ignored")
}
