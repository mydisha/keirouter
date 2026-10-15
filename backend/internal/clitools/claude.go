package clitools

import "path/filepath"

// ClaudeTool auto-configures Claude Code (~/.claude/settings.json "env").
type ClaudeTool struct{}

func (t *ClaudeTool) ID() string                    { return "claude" }
func (t *ClaudeTool) Name() string                  { return "Claude Code" }
func (t *ClaudeTool) Commands() []string            { return []string{"claude"} }
func (t *ClaudeTool) Extensions() []string          { return []string{"anthropic.claude-code"} }
func (t *ClaudeTool) ModelRequired() bool           { return false }
func (t *ClaudeTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

// configPath honors CLAUDE_CONFIG_DIR, which Claude Code uses in place of
// ~/.claude when set.
func (t *ClaudeTool) configPath(homeDir string) string {
	return filepath.Join(envDir(homeDir, "CLAUDE_CONFIG_DIR", "~/.claude"), "settings.json")
}

// claudeEnvKeys are the env entries KeiRouter owns. The DEFAULT_*_MODEL keys
// were written by earlier versions and are still cleaned up on removal.
var claudeEnvKeys = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

func (t *ClaudeTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadJSONObject(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	base := str(cfg, "env", "ANTHROPIC_BASE_URL")
	if base == "" {
		return nil, nil
	}
	key := str(cfg, "env", "ANTHROPIC_AUTH_TOKEN")
	if key == "" {
		key = str(cfg, "env", "ANTHROPIC_API_KEY")
	}
	return &Settings{BaseURL: base, APIKey: key, Model: str(cfg, "env", "ANTHROPIC_MODEL")}, nil
}

func (t *ClaudeTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	env := child(cfg, "env")
	// The Anthropic SDK appends /v1/messages itself, so the base URL is the
	// gateway root (a "/v1" here would produce /v1/v1/messages).
	env["ANTHROPIC_BASE_URL"] = s.BaseURL
	env["ANTHROPIC_AUTH_TOKEN"] = s.APIKey
	// ANTHROPIC_API_KEY makes Claude Code prompt about which credential to
	// use; the gateway key above replaces it.
	delete(env, "ANTHROPIC_API_KEY")
	for _, k := range claudeEnvKeys[3:] {
		delete(env, k)
	}
	if s.Model != "" {
		env["ANTHROPIC_MODEL"] = s.Model
	} else {
		delete(env, "ANTHROPIC_MODEL")
	}
	return saveJSON(path, cfg)
}

func (t *ClaudeTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadJSONObject(path)
	if err != nil || !exists {
		return err
	}
	if env, ok := cfg["env"].(map[string]any); ok {
		for _, k := range claudeEnvKeys {
			delete(env, k)
		}
		pruneEmpty(cfg, "env")
	}
	// Written by earlier KeiRouter versions; Claude Code keeps it in
	// ~/.claude.json, so it is meaningless here.
	delete(cfg, "hasCompletedOnboarding")
	return saveOrRemove(path, cfg, saveJSONObject)
}
