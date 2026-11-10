package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinPresetsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range builtinPresets() {
		require.NoError(t, p.Validate(), p.ID)
		require.False(t, seen[p.ID], "duplicate id %s", p.ID)
		seen[p.ID] = true
		require.NotEmpty(t, p.DocsURL, "%s needs the doc it was checked against", p.ID)
		require.Empty(t, p.StaticHeaders, "%s: presets never carry credentials", p.ID)
	}
	require.Len(t, seen, 13)
}

func TestPresetMatchesURL(t *testing.T) {
	c, err := NewCatalog("", false)
	require.NoError(t, err)

	gh, _ := c.Get("github")
	require.True(t, gh.MatchesURL("https://api.githubcopilot.com/mcp/readonly"))
	require.True(t, gh.MatchesURL("https://api.githubcopilot.com/mcp/"))
	require.False(t, gh.MatchesURL("https://api.githubcopilot.com/mcp/x-other"))
	require.False(t, gh.MatchesURL("https://evil.example/mcp/"))

	sb, _ := c.Get("supabase")
	require.True(t, sb.MatchesURL("https://mcp.supabase.com/mcp?project_ref=abc123&read_only=true"))
	require.False(t, sb.MatchesURL("https://mcp.supabase.com/mcp?project_ref=abc/../x&read_only=true"))
	require.False(t, sb.MatchesURL("https://mcp.supabase.com/mcp?project_ref=abc&read_only=maybe"))

	dd, _ := c.Get("datadog")
	require.True(t, dd.MatchesURL("https://mcp.datadoghq.eu/v1/mcp"))
	require.False(t, dd.MatchesURL("https://mcp.attacker.com/v1/mcp"))
}

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "catalog.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestCompanyCatalog(t *testing.T) {
	path := writeCatalog(t, `
presets:
  - id: internal-docs
    name: Internal docs
    url: https://docs.internal.example/{region}/mcp
    auth_types: [bearer]
    vars:
      - key: region
        label: Region
        type: select
        options: [{value: eu, label: EU}, {value: us, label: US}]
  - id: github
    name: GitHub Enterprise
    url: https://copilot-api.acme.ghe.com/mcp
    auth_types: [bearer]
`)
	c, err := NewCatalog(path, true)
	require.NoError(t, err)
	require.True(t, c.CatalogOnly())
	presets := c.Presets()
	require.Equal(t, "internal-docs", presets[0].ID, "company presets come first")
	require.Equal(t, SourceCompany, presets[0].Source)
	gh, _ := c.Get("github")
	require.Equal(t, "GitHub Enterprise", gh.Name, "a company preset replaces the built-in one with the same id")
	require.Equal(t, 13+1, len(presets))
}

func TestCompanyCatalogRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"unknown field":       "presets:\n  - id: a\n    name: A\n    url: https://a.example/mcp\n    auth_types: [bearer]\n    colour: red\n",
		"bad id":              "presets:\n  - id: Bad_ID\n    name: A\n    url: https://a.example/mcp\n    auth_types: [bearer]\n",
		"bad auth type":       "presets:\n  - id: a\n    name: A\n    url: https://a.example/mcp\n    auth_types: [oauth]\n",
		"undeclared var":      "presets:\n  - id: a\n    name: A\n    url: https://{host}/mcp\n    auth_types: [none]\n",
		"not http":            "presets:\n  - id: a\n    name: A\n    url: ftp://a.example/mcp\n    auth_types: [none]\n",
		"duplicate id":        "presets:\n  - id: a\n    name: A\n    url: https://a.example/mcp\n    auth_types: [none]\n  - id: a\n    name: B\n    url: https://b.example/mcp\n    auth_types: [none]\n",
		"reserved header":     "presets:\n  - id: a\n    name: A\n    url: https://a.example/mcp\n    auth_types: [none]\n    static_headers: {Authorization: x}\n",
		"header without name": "presets:\n  - id: a\n    name: A\n    url: https://a.example/mcp\n    auth_types: [header]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewCatalog(writeCatalog(t, body), false)
			require.Error(t, err)
		})
	}
	_, err := NewCatalog(filepath.Join(t.TempDir(), "missing.yaml"), false)
	require.Error(t, err)
}
