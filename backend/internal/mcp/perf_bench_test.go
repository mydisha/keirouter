package mcp

// Benchmarks for the per-request MCP path: building tools/list for a key,
// decoding an inbound tools/call, rewriting params for the upstream, and the
// access check. Compare allocs/op across changes; ns/op is noisy.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/fastjson"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func benchSnapshot(b *testing.B, servers, tools int) *Snapshot {
	b.Helper()
	var srvs []store.MCPServer
	var defs []store.MCPServerTool
	for s := 0; s < servers; s++ {
		id := fmt.Sprintf("srv-%02d", s)
		srvs = append(srvs, store.MCPServer{ID: id, Slug: fmt.Sprintf("s%02d", s), Enabled: true})
		for t := 0; t < tools; t++ {
			name := fmt.Sprintf("tool_%02d", t)
			def := fmt.Sprintf(`{"name":%q,"description":"Does thing %d with a reasonably long description of its behaviour.","inputSchema":{"type":"object","properties":{"q":{"type":"string","description":"query"},"limit":{"type":"integer"}},"required":["q"]}}`, name, t)
			defs = append(defs, store.MCPServerTool{ServerID: id, Name: name, Definition: def, Enabled: true})
		}
	}
	snap, err := buildSnapshot(srvs, defs)
	if err != nil {
		b.Fatal(err)
	}
	return snap
}

func BenchmarkListTools(b *testing.B) {
	snap := benchSnapshot(b, 10, 30)
	selected := &Access{Mode: store.MCPModeSelected, grants: map[string]grant{}}
	for s := 0; s < 3; s++ {
		g := grant{tools: map[string]struct{}{}}
		for t := 0; t < 5; t++ {
			g.tools[fmt.Sprintf("tool_%02d", t)] = struct{}{}
		}
		selected.grants[fmt.Sprintf("srv-%02d", s)] = g
	}
	for _, tc := range []struct {
		name   string
		access *Access
	}{{"all", AllAccess}, {"selected", selected}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				buf := getBuffer()
				appendTools(buf, snap, tc.access, "")
				putBuffer(buf)
			}
		})
	}
}

func benchCallBody(argBytes int) []byte {
	return []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"s01__tool_03","arguments":{"q":"` +
		strings.Repeat("x", argBytes) + `","limit":5},"_meta":{"progressToken":"p1","io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
}

func BenchmarkCallEnvelope(b *testing.B) {
	for _, size := range []int{2 << 10, 200 << 10} {
		body := benchCallBody(size)
		b.Run(fmt.Sprintf("decode/%dKB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				buf := append([]byte(nil), body...)
				var req inbound
				var p inboundParams
				if err := fastjson.UnmarshalNoCopy(buf, &req); err != nil {
					b.Fatal(err)
				}
				if err := fastjson.UnmarshalNoCopy(req.Params, &p); err != nil {
					b.Fatal(err)
				}
			}
		})
		var req inbound
		_ = json.Unmarshal(body, &req)
		b.Run(fmt.Sprintf("upstream-params/%dKB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(req.Params)))
			for b.Loop() {
				out, err := withMeta(req.Params, true, testInfo, "tool_03")
				if err != nil || !bytes.Contains(out, []byte(`"tool_03"`)) {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCanCall(b *testing.B) {
	acc := NewAccess(store.MCPKeyAccess{Mode: store.MCPModeSelected, Grants: []store.MCPKeyGrant{
		{ServerID: "srv-01", ToolMode: store.MCPModeSelected, Tools: []string{"a", "b", "c"}},
	}})
	b.ReportAllocs()
	for b.Loop() {
		if !acc.CanCall("srv-01", "b") {
			b.Fatal("denied")
		}
	}
}

// TestCanCallAllocations keeps the access check allocation-free.
func TestCanCallAllocations(t *testing.T) {
	acc := NewAccess(store.MCPKeyAccess{Mode: store.MCPModeSelected, Grants: []store.MCPKeyGrant{
		{ServerID: "srv-01", ToolMode: store.MCPModeSelected, Tools: []string{"a", "b"}},
	}})
	if n := testing.AllocsPerRun(100, func() { _ = acc.CanCall("srv-01", "b") }); n != 0 {
		t.Fatalf("CanCall allocates %v times", n)
	}
}

// TestAppendToolsAllModeAllocations: a key in all mode gets the precomputed
// list; writing it into a warm buffer allocates nothing.
func TestAppendToolsAllModeAllocations(t *testing.T) {
	snap, err := buildSnapshot(
		[]store.MCPServer{{ID: "a", Slug: "a", Enabled: true}},
		[]store.MCPServerTool{{ServerID: "a", Name: "t", Definition: `{"name":"t","inputSchema":{"type":"object"}}`, Enabled: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.Grow(1 << 12)
	if n := testing.AllocsPerRun(100, func() { buf.Reset(); appendTools(&buf, snap, AllAccess, "") }); n != 0 {
		t.Fatalf("appendTools allocates %v times", n)
	}
}
