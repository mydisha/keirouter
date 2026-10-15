package skills

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestCompileSkipsDisabledAndBlank(t *testing.T) {
	set := Compile([]Skill{
		{Prompt: "  First rule.  ", Enabled: true},
		{Prompt: "Disabled rule.", Enabled: false},
		{Prompt: "   ", Enabled: true},
		{Prompt: "Second rule.", Enabled: true},
	})
	if set.Len() != 2 {
		t.Fatalf("Len = %d, want 2", set.Len())
	}
	if got, want := set.Block(Request{}), "First rule.\n\nSecond rule."; got != want {
		t.Fatalf("Block = %q, want %q", got, want)
	}
}

func TestBlockEmptySet(t *testing.T) {
	var nilSet *Set
	if got := nilSet.Block(Request{}); got != "" {
		t.Fatalf("nil set Block = %q", got)
	}
	if got := Compile(nil).Block(Request{KeyID: "k"}); got != "" {
		t.Fatalf("empty set Block = %q", got)
	}
}

func TestBlockScopes(t *testing.T) {
	set := Compile([]Skill{
		{Prompt: "global", Enabled: true},
		{Prompt: "key-a", Enabled: true, Scope: Scope{Keys: []string{"a"}}},
		{Prompt: "plan-pro", Enabled: true, Scope: Scope{Plans: []string{"pro"}}},
		{Prompt: "key-b-or-free", Enabled: true, Scope: Scope{Keys: []string{"b"}, Plans: []string{"free"}}},
		{Prompt: "claude", Enabled: true, Scope: Scope{Models: []string{"Claude-*"}}},
		{Prompt: "pro-on-gpt4o", Enabled: true, Scope: Scope{Plans: []string{"pro"}, Models: []string{"gpt-4o"}}},
	})

	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"anonymous", Request{}, "global"},
		{"key a", Request{KeyID: "a"}, "global\n\nkey-a"},
		{"plan pro, other model", Request{KeyID: "x", PlanID: "pro", Models: []string{"gemini-2.5"}}, "global\n\nplan-pro"},
		{"plan pro on gpt-4o", Request{PlanID: "pro", Models: []string{"openai/gpt-4o", "gpt-4o"}}, "global\n\nplan-pro\n\npro-on-gpt4o"},
		{"key b", Request{KeyID: "b"}, "global\n\nkey-b-or-free"},
		{"plan free", Request{PlanID: "free"}, "global\n\nkey-b-or-free"},
		{"claude model", Request{Models: []string{"kr/coding", "claude-sonnet-4.5"}}, "global\n\nclaude"},
		{"empty ids never match", Request{KeyID: "", PlanID: ""}, "global"},
	}
	for _, tc := range cases {
		if got := set.Block(tc.req); got != tc.want {
			t.Errorf("%s: Block = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBlockSkipOptional(t *testing.T) {
	set := Compile([]Skill{
		{Prompt: "mandatory", Enabled: true},
		{Prompt: "optional", Enabled: true, Optional: true},
	})
	if got := set.Block(Request{}); got != "mandatory\n\noptional" {
		t.Fatalf("Block = %q", got)
	}
	if got := set.Block(Request{SkipOptional: true}); got != "mandatory" {
		t.Fatalf("Block(skip) = %q", got)
	}
}

func TestMatchModel(t *testing.T) {
	if !MatchModel("Claude-Opus", []string{"claude-*"}) {
		t.Fatal("prefix wildcard should match case-insensitively")
	}
	if MatchModel("gpt-4o-mini", []string{"gpt-4o"}) {
		t.Fatal("exact pattern must not prefix-match")
	}
	if !MatchModel("anything", []string{"*"}) {
		t.Fatal("bare * matches everything")
	}
}

func TestApplyEmptyBlockIsNoop(t *testing.T) {
	req := &core.ChatRequest{System: "original"}
	Apply(req, Config{})
	if req.System != "original" {
		t.Fatalf("empty block must not change system, got %q", req.System)
	}
}

func TestApplyIntoEmptySystem(t *testing.T) {
	req := &core.ChatRequest{System: "  "}
	Apply(req, Config{Block: "Be precise."})
	if req.System != "Be precise." {
		t.Fatalf("System = %q", req.System)
	}
}

func TestApplyPrependsAndIsIdempotent(t *testing.T) {
	req := &core.ChatRequest{System: "client prompt"}
	cfg := Config{Block: "Be precise."}
	Apply(req, cfg)
	Apply(req, cfg)
	if want := "Be precise.\n\nclient prompt"; req.System != want {
		t.Fatalf("System = %q, want %q", req.System, want)
	}
}

func TestApplyNilRequest(t *testing.T) {
	Apply(nil, Config{Block: "x"})
}

func TestRenderFillsKnownPlaceholders(t *testing.T) {
	vars := map[string]string{"company": "Acme", "support.email": "help@acme.test", "blank": ""}
	got := Render("Hi from {{company}} ({{ company }}). Mail {{support.email}}. {{blank}} {{missing}} {{bad name}}", vars)
	want := "Hi from Acme (Acme). Mail help@acme.test. {{blank}} {{missing}} {{bad name}}"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	if got := Render("{{company}}", nil); got != "{{company}}" {
		t.Fatalf("Render without vars = %q", got)
	}
}

func TestCompileRendersVariables(t *testing.T) {
	set := Compile([]Skill{{Prompt: "You work for {{company}}.", Enabled: true, Variables: map[string]string{"company": "Acme"}}})
	if got := set.Block(Request{}); got != "You work for Acme." {
		t.Fatalf("Block = %q", got)
	}
}
