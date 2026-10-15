package clitools

// DeepSeekTool auto-configures DeepSeek TUI (~/.deepseek/config.toml), using
// its OpenAI-compatible provider slot.
type DeepSeekTool struct{}

func (t *DeepSeekTool) ID() string                    { return "deepseek" }
func (t *DeepSeekTool) Name() string                  { return "DeepSeek TUI" }
func (t *DeepSeekTool) Commands() []string            { return []string{"deepseek", "deepseek-tui"} }
func (t *DeepSeekTool) Extensions() []string          { return nil }
func (t *DeepSeekTool) ModelRequired() bool           { return true }
func (t *DeepSeekTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

func (t *DeepSeekTool) configPath(homeDir string) string {
	return expandHome(homeDir, "~/.deepseek/config.toml")
}

const deepSeekSlot = "openai"

func (t *DeepSeekTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadTOML(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	if str(cfg, "provider") != deepSeekSlot {
		return nil, nil
	}
	base := str(cfg, "providers", deepSeekSlot, "base_url")
	if base == "" {
		return nil, nil
	}
	return &Settings{
		BaseURL: base,
		APIKey:  str(cfg, "providers", deepSeekSlot, "api_key"),
		Model:   str(cfg, "providers", deepSeekSlot, "model"),
	}, nil
}

func (t *DeepSeekTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadTOML(path)
	if err != nil {
		return err
	}
	cfg["provider"] = deepSeekSlot
	child(cfg, "providers")[deepSeekSlot] = map[string]any{
		"base_url": v1URL(s.BaseURL),
		"api_key":  s.APIKey,
		"model":    s.Model,
	}
	return saveTOML(path, cfg)
}

func (t *DeepSeekTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadTOML(path)
	if err != nil || !exists {
		return err
	}
	if providers, ok := cfg["providers"].(map[string]any); ok {
		delete(providers, deepSeekSlot)
		pruneEmpty(cfg, "providers")
	}
	// Without an explicit provider DeepSeek TUI falls back to its native API.
	if str(cfg, "provider") == deepSeekSlot {
		delete(cfg, "provider")
	}
	return saveOrRemove(path, cfg, saveTOML)
}
