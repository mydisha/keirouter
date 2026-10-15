package clitools

import (
	"path/filepath"
	"strings"
)

// OpenCodeTool auto-configures OpenCode (~/.config/opencode/opencode.json or
// opencode.jsonc).
type OpenCodeTool struct{}

func (t *OpenCodeTool) ID() string                    { return "opencode" }
func (t *OpenCodeTool) Name() string                  { return "OpenCode" }
func (t *OpenCodeTool) Commands() []string            { return []string{"opencode"} }
func (t *OpenCodeTool) Extensions() []string          { return []string{"sst-dev.opencode"} }
func (t *OpenCodeTool) ModelRequired() bool           { return true }
func (t *OpenCodeTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

// configPath resolves the OpenCode global config. OpenCode honors
// XDG_CONFIG_HOME and accepts either opencode.json or opencode.jsonc; an
// existing .jsonc wins so KeiRouter edits the file OpenCode actually reads.
func (t *OpenCodeTool) configPath(homeDir string) string {
	dir := filepath.Join(envDir(homeDir, "XDG_CONFIG_HOME", "~/.config"), "opencode")
	if jsonc := filepath.Join(dir, "opencode.jsonc"); fileExists(jsonc) && !fileExists(filepath.Join(dir, "opencode.json")) {
		return jsonc
	}
	return filepath.Join(dir, "opencode.json")
}

const openCodeProvider = "keirouter"

func (t *OpenCodeTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadJSONObject(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	base := str(cfg, "provider", openCodeProvider, "options", "baseURL")
	if base == "" {
		return nil, nil
	}
	s := &Settings{BaseURL: base, APIKey: str(cfg, "provider", openCodeProvider, "options", "apiKey")}
	if model := str(cfg, "model"); strings.HasPrefix(model, openCodeProvider+"/") {
		s.Model = strings.TrimPrefix(model, openCodeProvider+"/")
	}
	return s, nil
}

func (t *OpenCodeTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	if _, ok := cfg["$schema"]; !ok {
		cfg["$schema"] = "https://opencode.ai/config.json"
	}
	provider := child(child(cfg, "provider"), openCodeProvider)
	provider["npm"] = "@ai-sdk/openai-compatible"
	provider["name"] = "KeiRouter"
	options := child(provider, "options")
	options["baseURL"] = v1URL(s.BaseURL)
	options["apiKey"] = s.APIKey
	// Keep models added earlier (or by hand) so switching the default does
	// not drop them from OpenCode's model picker.
	models := child(provider, "models")
	if _, ok := models[s.Model]; !ok {
		models[s.Model] = map[string]any{"name": s.Model}
	}
	cfg["model"] = openCodeProvider + "/" + s.Model
	return saveJSON(path, cfg)
}

func (t *OpenCodeTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadJSONObject(path)
	if err != nil || !exists {
		return err
	}
	if provider, ok := cfg["provider"].(map[string]any); ok {
		delete(provider, openCodeProvider)
		pruneEmpty(cfg, "provider")
	}
	for _, k := range []string{"model", "small_model"} {
		if v, _ := cfg[k].(string); strings.HasPrefix(v, openCodeProvider+"/") {
			delete(cfg, k)
		}
	}
	// Only "$schema" left means the file holds nothing of the user's.
	if len(cfg) == 1 && cfg["$schema"] != nil {
		delete(cfg, "$schema")
	}
	return saveOrRemove(path, cfg, saveJSONObject)
}
