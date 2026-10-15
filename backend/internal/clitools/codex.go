package clitools

import "path/filepath"

// CodexTool auto-configures Codex CLI (~/.codex/config.toml).
//
// The key is stored as the provider's experimental_bearer_token rather than in
// auth.json: auth.json holds the user's own OpenAI / ChatGPT login, and
// overwriting it would sign them out of Codex.
type CodexTool struct{}

func (t *CodexTool) ID() string                    { return "codex" }
func (t *CodexTool) Name() string                  { return "Codex CLI" }
func (t *CodexTool) Commands() []string            { return []string{"codex"} }
func (t *CodexTool) Extensions() []string          { return []string{"openai.chatgpt"} }
func (t *CodexTool) ModelRequired() bool           { return false }
func (t *CodexTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

// configPath honors CODEX_HOME, Codex's override for ~/.codex.
func (t *CodexTool) configPath(homeDir string) string {
	return filepath.Join(envDir(homeDir, "CODEX_HOME", "~/.codex"), "config.toml")
}

const codexProviderID = "keirouter"

func (t *CodexTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadTOML(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	// The provider block is inert unless it is the selected provider.
	if str(cfg, "model_provider") != codexProviderID {
		return nil, nil
	}
	base := str(cfg, "model_providers", codexProviderID, "base_url")
	if base == "" {
		return nil, nil
	}
	return &Settings{
		BaseURL: base,
		APIKey:  str(cfg, "model_providers", codexProviderID, "experimental_bearer_token"),
		Model:   str(cfg, "model"),
	}, nil
}

func (t *CodexTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadTOML(path)
	if err != nil {
		return err
	}
	if s.Model != "" {
		cfg["model"] = s.Model
	}
	cfg["model_provider"] = codexProviderID
	child(cfg, "model_providers")[codexProviderID] = map[string]any{
		"name":                      "KeiRouter",
		"base_url":                  v1URL(s.BaseURL),
		"wire_api":                  "responses",
		"experimental_bearer_token": s.APIKey,
	}
	return saveTOML(path, cfg)
}

func (t *CodexTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadTOML(path)
	if err != nil || !exists {
		return err
	}
	if str(cfg, "model_provider") == codexProviderID {
		delete(cfg, "model_provider")
		// The model was chosen for the gateway; Codex falls back to its own
		// default without it.
		delete(cfg, "model")
	}
	if providers, ok := cfg["model_providers"].(map[string]any); ok {
		delete(providers, codexProviderID)
		pruneEmpty(cfg, "model_providers")
	}
	return saveOrRemove(path, cfg, saveTOML)
}
