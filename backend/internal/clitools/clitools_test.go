package clitools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points every env override the tools honor at a temp home and
// empties PATH so the host's real CLIs and configs cannot leak into a test.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "HERMES_HOME", "APPDATA", "LOCALAPPDATA",
		"NVM_DIR", "PNPM_HOME", "VOLTA_HOME", "BUN_INSTALL", "CARGO_HOME", "GOBIN", "NPM_CONFIG_PREFIX"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", "")
	prev := systemBinDirs
	systemBinDirs = false
	t.Cleanup(func() { systemBinDirs = prev })
	return home
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var testSettings = Settings{BaseURL: "http://localhost:20180", APIKey: "kr_test_key_123456789", Model: "openai/gpt-5"}

// TestToolsRoundTrip configures every tool, reads the settings back, removes
// them and checks that nothing of KeiRouter's is left behind.
func TestToolsRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, tool := range NewRegistry().All() {
		t.Run(tool.ID(), func(t *testing.T) {
			home := isolate(t)
			r := NewRegistry()

			st := r.Detect(ctx, tool, home)
			if st.Configured || st.Installed {
				t.Fatalf("fresh home: configured=%v installed=%v", st.Configured, st.Installed)
			}

			st, err := r.Configure(ctx, tool, home, Settings{BaseURL: testSettings.BaseURL + "/v1/", APIKey: testSettings.APIKey, Model: testSettings.Model})
			if err != nil {
				t.Fatalf("configure: %v", err)
			}
			if !st.Configured || st.Settings == nil {
				t.Fatalf("not configured after write: %+v", st)
			}
			if st.Error != "" {
				t.Fatalf("detection error: %s", st.Error)
			}
			got := *st.Settings
			if got.BaseURL != testSettings.BaseURL {
				t.Errorf("base URL = %q, want %q", got.BaseURL, testSettings.BaseURL)
			}
			if got.APIKey != testSettings.APIKey {
				t.Errorf("api key = %q, want %q", got.APIKey, testSettings.APIKey)
			}
			if got.Model != testSettings.Model {
				t.Errorf("model = %q, want %q", got.Model, testSettings.Model)
			}
			for _, p := range st.ConfigFiles {
				if raw, err := os.ReadFile(p); err == nil && strings.Contains(string(raw), "/v1/v1") {
					t.Errorf("%s contains a doubled /v1", p)
				}
			}

			st, err = r.Remove(ctx, tool, home)
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if st.Configured {
				t.Fatalf("still configured after remove: %+v", st.Settings)
			}
			for _, p := range st.ConfigFiles {
				if raw, err := os.ReadFile(p); err == nil && strings.Contains(string(raw), testSettings.APIKey) {
					t.Errorf("%s still holds the key after remove:\n%s", p, raw)
				}
			}
		})
	}
}

// TestConfigurePreservesUserSettings checks the merge-not-overwrite contract on
// representative files, including JSONC comments and other providers.
func TestConfigurePreservesUserSettings(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		tool  Tool
		path  string
		seed  string
		keeps []string
	}{
		{&ClaudeTool{}, "~/.claude/settings.json", `{
  // user comment
  "permissions": {"allow": ["Bash(ls:*)"]},
  "env": {"DISABLE_TELEMETRY": "1", "URL": "http://example.com//x",},
}`, []string{"permissions", "DISABLE_TELEMETRY", "http://example.com//x"}},
		{&CodexTool{}, "~/.codex/config.toml", "approval_policy = \"on-request\"\n[model_providers.other]\nname = \"Other\"\nbase_url = \"https://other/v1\"\n",
			[]string{"approval_policy", "model_providers.other"}},
		{&DroidTool{}, "~/.factory/settings.json", `{"customModels":[{"id":"custom:Mine-0","model":"mine"}],"theme":"dark"}`,
			[]string{"custom:Mine-0", "theme"}},
		{&OpenClawTool{}, "~/.openclaw/openclaw.json", `{"models":{"providers":{"ollama":{"baseUrl":"http://x"}}}}`,
			[]string{"ollama"}},
		{&DeepSeekTool{}, "~/.deepseek/config.toml", "theme = \"dark\"\n[providers.deepseek]\napi_key = \"sk-own\"\n",
			[]string{"theme", "sk-own"}},
		{&HermesTool{}, "~/.hermes/config.yaml", "terminal:\n  backend: local\n", []string{"terminal"}},
	}
	for _, c := range cases {
		t.Run(c.tool.ID(), func(t *testing.T) {
			home := isolate(t)
			path := expandHome(home, c.path)
			writeTestFile(t, path, c.seed)
			r := NewRegistry()
			if _, err := r.Configure(ctx, c.tool, home, testSettings); err != nil {
				t.Fatalf("configure: %v", err)
			}
			out := readTestFile(t, path)
			for _, k := range c.keeps {
				if !strings.Contains(out, k) && !strings.Contains(out, strings.ReplaceAll(k, ".", "]\n[")) {
					// TOML renders nested tables as [model_providers.other].
					if !strings.Contains(out, "["+k+"]") {
						t.Errorf("configure dropped %q:\n%s", k, out)
					}
				}
			}
			if got := readTestFile(t, backupPath(path)); got != c.seed {
				t.Errorf("backup = %q, want original %q", got, c.seed)
			}

			if _, err := r.Remove(ctx, c.tool, home); err != nil {
				t.Fatalf("remove: %v", err)
			}
			out = readTestFile(t, path)
			for _, k := range c.keeps {
				if !strings.Contains(out, k) && !strings.Contains(out, "["+k+"]") {
					t.Errorf("remove dropped %q:\n%s", k, out)
				}
			}
		})
	}
}

// TestConfigureRefusesUnparseableConfig: a broken file must be left untouched
// rather than replaced by a fresh document.
func TestConfigureRefusesUnparseableConfig(t *testing.T) {
	ctx := context.Background()
	home := isolate(t)
	path := expandHome(home, "~/.claude/settings.json")
	const broken = `{"permissions": {"allow": [}`
	writeTestFile(t, path, broken)

	r := NewRegistry()
	tool := r.Get("claude")
	_, err := r.Configure(ctx, tool, home, testSettings)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("configure error = %v, want *ParseError", err)
	}
	if got := readTestFile(t, path); got != broken {
		t.Fatalf("broken config was rewritten: %q", got)
	}
	if _, err := r.Remove(ctx, tool, home); !errors.As(err, &pe) {
		t.Fatalf("remove error = %v, want *ParseError", err)
	}
	st := r.Detect(ctx, tool, home)
	if st.Error == "" || st.Configured || !st.Installed {
		t.Fatalf("detect on broken config: %+v", st)
	}
}

// TestCodexLeavesAuthJSONAlone: Codex's auth.json holds the user's own login.
func TestCodexLeavesAuthJSONAlone(t *testing.T) {
	ctx := context.Background()
	home := isolate(t)
	auth := expandHome(home, "~/.codex/auth.json")
	const login = `{"tokens":{"access_token":"chatgpt"}}`
	writeTestFile(t, auth, login)

	r := NewRegistry()
	tool := r.Get("codex")
	if _, err := r.Configure(ctx, tool, home, testSettings); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Remove(ctx, tool, home); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, auth); got != login {
		t.Fatalf("auth.json changed: %q", got)
	}
}

// TestEnvFileMerge: env files keep the user's other variables.
func TestEnvFileMerge(t *testing.T) {
	ctx := context.Background()
	home := isolate(t)
	envP := expandHome(home, "~/.hermes/.env")
	writeTestFile(t, envP, "# keys\nANTHROPIC_API_KEY=sk-ant\nexport OPENAI_API_KEY=\"old\"\n")

	r := NewRegistry()
	tool := r.Get("hermes")
	if _, err := r.Configure(ctx, tool, home, testSettings); err != nil {
		t.Fatal(err)
	}
	out := readTestFile(t, envP)
	if !strings.Contains(out, "ANTHROPIC_API_KEY=sk-ant") || !strings.Contains(out, "OPENAI_API_KEY="+testSettings.APIKey) || strings.Contains(out, "old") {
		t.Fatalf("env after configure:\n%s", out)
	}
	if _, err := r.Remove(ctx, tool, home); err != nil {
		t.Fatal(err)
	}
	out = readTestFile(t, envP)
	if !strings.Contains(out, "ANTHROPIC_API_KEY=sk-ant") || strings.Contains(out, "OPENAI_API_KEY") {
		t.Fatalf("env after remove:\n%s", out)
	}
}

func TestConfigureValidation(t *testing.T) {
	ctx := context.Background()
	home := isolate(t)
	r := NewRegistry()
	if _, err := r.Configure(ctx, r.Get("opencode"), home, Settings{BaseURL: "http://x", APIKey: "k"}); err == nil {
		t.Error("opencode without a model should fail")
	}
	if _, err := r.Configure(ctx, r.Get("claude"), home, Settings{BaseURL: "http://x", APIKey: "k"}); err != nil {
		t.Errorf("claude without a model: %v", err)
	}
	if _, err := r.Configure(ctx, r.Get("claude"), home, Settings{BaseURL: "http://x"}); err == nil {
		t.Error("missing key should fail")
	}
}

func TestStripJSONC(t *testing.T) {
	in := "\ufeff{\n  // line\n  \"a\": \"http://x//y\", /* block, } */\n  \"b\": [1, 2,],\n  \"c\": \"q\\\"//,\",\n}"
	var v map[string]any
	if err := json.Unmarshal(stripJSONC([]byte(in)), &v); err != nil {
		t.Fatalf("parse: %v\n%s", err, stripJSONC([]byte(in)))
	}
	if v["a"] != "http://x//y" || v["c"] != `q"//,` || len(v["b"].([]any)) != 2 {
		t.Fatalf("got %#v", v)
	}
}

func TestDetectBinaryAndExtension(t *testing.T) {
	ctx := context.Background()
	home := isolate(t)
	bin := filepath.Join(home, ".local", "bin", "claude")
	writeTestFile(t, bin, "#!/bin/sh\necho '2.1.0 (Claude Code)'\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor", "extensions", "github.copilot-chat-0.31.2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".vscode", "extensions", "github.copilot-chat-0.29.0"), 0o700); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	byID := map[string]Status{}
	for _, st := range r.DetectAll(ctx, home) {
		byID[st.ID] = st
	}
	claude := byID["claude"]
	if !claude.Installed || claude.BinaryPath != bin || claude.DetectedVia[0] != "binary" {
		t.Errorf("claude: %+v", claude)
	}
	if claude.Version != "2.1.0" {
		t.Errorf("claude version = %q", claude.Version)
	}
	copilot := byID["copilot"]
	if !copilot.Installed || copilot.Version != "0.31.2" || !strings.Contains(copilot.Extension, "Cursor") {
		t.Errorf("copilot: %+v", copilot)
	}
	if byID["codex"].Installed {
		t.Errorf("codex should not be installed: %+v", byID["codex"])
	}
}

func TestRootURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:20180":       "http://localhost:20180",
		" http://localhost:20180/v1/ ": "http://localhost:20180",
		"https://gw.example.com/kr/v1": "https://gw.example.com/kr",
	} {
		if got := RootURL(in); got != want {
			t.Errorf("RootURL(%q) = %q, want %q", in, got, want)
		}
	}
}
