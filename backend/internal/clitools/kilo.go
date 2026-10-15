package clitools

// KiloTool auto-configures Kilo Code (~/.local/share/kilo/auth.json).
type KiloTool struct{}

func (t *KiloTool) ID() string                    { return "kilo" }
func (t *KiloTool) Name() string                  { return "Kilo Code" }
func (t *KiloTool) Commands() []string            { return []string{"kilocode", "kilo"} }
func (t *KiloTool) Extensions() []string          { return []string{"kilocode.kilo-code"} }
func (t *KiloTool) ModelRequired() bool           { return true }
func (t *KiloTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

func (t *KiloTool) configPath(homeDir string) string {
	return expandHome(homeDir, "~/.local/share/kilo/auth.json")
}

const kiloProvider = "openai-compatible"

func (t *KiloTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadJSONObject(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	base := str(cfg, kiloProvider, "baseUrl")
	if base == "" {
		return nil, nil
	}
	return &Settings{BaseURL: base, APIKey: str(cfg, kiloProvider, "apiKey"), Model: str(cfg, kiloProvider, "model")}, nil
}

func (t *KiloTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	cfg[kiloProvider] = map[string]any{
		"type":    "api-key",
		"apiKey":  s.APIKey,
		"baseUrl": v1URL(s.BaseURL),
		"model":   s.Model,
	}
	return saveJSON(path, cfg)
}

func (t *KiloTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadJSONObject(path)
	if err != nil || !exists {
		return err
	}
	delete(cfg, kiloProvider)
	return saveOrRemove(path, cfg, saveJSONObject)
}
