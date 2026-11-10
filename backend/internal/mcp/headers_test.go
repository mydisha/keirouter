package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseParamHeaders(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		want    []ParamHeader
		wantErr string
	}{
		{name: "none", schema: `{"type":"object","properties":{"q":{"type":"string"}}}`},
		{name: "empty schema", schema: ``},
		{
			name:   "top level and nested",
			schema: `{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"},"opts":{"type":"object","properties":{"dry":{"type":"boolean","x-mcp-header":"Dry-Run"}}}}}`,
			want:   []ParamHeader{{Name: "Dry-Run", Path: []string{"opts", "dry"}}, {Name: "Region", Path: []string{"region"}}},
		},
		{name: "number type", schema: `{"properties":{"r":{"type":"number","x-mcp-header":"R"}}}`, wantErr: "number"},
		{name: "missing type", schema: `{"properties":{"r":{"x-mcp-header":"R"}}}`, wantErr: "type"},
		{name: "under items", schema: `{"properties":{"l":{"type":"array","items":{"type":"string","x-mcp-header":"L"}}}}`, wantErr: "reachable"},
		{name: "under oneOf", schema: `{"oneOf":[{"properties":{"a":{"type":"string","x-mcp-header":"A"}}}]}`, wantErr: "reachable"},
		{name: "on root", schema: `{"type":"string","x-mcp-header":"Root"}`, wantErr: "reachable"},
		{name: "duplicate ignoring case", schema: `{"properties":{"a":{"type":"string","x-mcp-header":"Id"},"b":{"type":"string","x-mcp-header":"ID"}}}`, wantErr: "more than once"},
		{name: "not a token", schema: `{"properties":{"a":{"type":"string","x-mcp-header":"bad name"}}}`, wantErr: "token"},
		{name: "empty value", schema: `{"properties":{"a":{"type":"string","x-mcp-header":""}}}`, wantErr: "non-empty"},
		{name: "inside default is data", schema: `{"properties":{"a":{"type":"object","default":{"x-mcp-header":1}}}}`},
		{name: "property named like the keyword", schema: `{"properties":{"x-mcp-header":{"type":"string"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseParamHeaders(json.RawMessage(tc.schema))
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestParamHeaderValue(t *testing.T) {
	args := json.RawMessage(`{"region":"us-west1","n":42,"f":42.0,"big":9007199254740993,"frac":1.5,"ok":true,"none":null,"uni":"Hello, 世界","pad":" x ","opts":{"dry":false},"obj":{"a":1}}`)
	cases := []struct {
		path    []string
		want    string
		present bool
		err     bool
	}{
		{path: []string{"region"}, want: "us-west1", present: true},
		{path: []string{"n"}, want: "42", present: true},
		{path: []string{"f"}, want: "42", present: true},
		{path: []string{"ok"}, want: "true", present: true},
		{path: []string{"opts", "dry"}, want: "false", present: true},
		{path: []string{"uni"}, want: "=?base64?SGVsbG8sIOS4lueVjA==?=", present: true},
		{path: []string{"pad"}, want: "=?base64?IHgg?=", present: true},
		{path: []string{"none"}},
		{path: []string{"missing"}},
		{path: []string{"opts", "missing"}},
		{path: []string{"big"}, err: true},
		{path: []string{"frac"}, err: true},
		{path: []string{"obj"}, err: true},
	}
	for _, tc := range cases {
		got, ok, err := ParamHeaderValue(args, ParamHeader{Name: "X", Path: tc.path})
		if tc.err {
			require.Error(t, err, tc.path)
			continue
		}
		require.NoError(t, err, tc.path)
		require.Equal(t, tc.present, ok, tc.path)
		require.Equal(t, tc.want, got, tc.path)
	}
}

func TestHeaderValueEncoding(t *testing.T) {
	for _, v := range []string{"plain", "Hello, 世界", " lead", "line1\nline2", "=?base64?literal?=", ""} {
		enc := EncodeHeaderValue(v)
		dec, ok := DecodeHeaderValue(enc)
		require.True(t, ok, v)
		require.Equal(t, v, dec)
	}
	require.Equal(t, "plain", EncodeHeaderValue("plain"))
	_, ok := DecodeHeaderValue("=?base64?!!!?=")
	require.False(t, ok)
	_, ok = DecodeHeaderValue("bad\x01value")
	require.False(t, ok)
}

func TestSplitToolName(t *testing.T) {
	slug, tool, ok := SplitToolName("github__create_issue")
	require.True(t, ok)
	require.Equal(t, "github", slug)
	require.Equal(t, "create_issue", tool)

	slug, tool, ok = SplitToolName("my-srv__tool__with__underscores")
	require.True(t, ok)
	require.Equal(t, "my-srv", slug)
	require.Equal(t, "tool__with__underscores", tool)

	for _, bad := range []string{"noseparator", "__tool", "slug__", ""} {
		_, _, ok := SplitToolName(bad)
		require.False(t, ok, bad)
	}
}
