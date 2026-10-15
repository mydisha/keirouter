package clitools

// HermesTool auto-configures Hermes Agent (~/.hermes/config.yaml + .env).
type HermesTool struct{}

func (t *HermesTool) ID() string           { return "hermes" }
func (t *HermesTool) Name() string         { return "Hermes Agent" }
func (t *HermesTool) Commands() []string   { return []string{"hermes"} }
func (t *HermesTool) Extensions() []string { return nil }
func (t *HermesTool) ModelRequired() bool  { return true }
func (t *HermesTool) ConfigFiles(h string) []string {
	return []string{t.configPath(h), t.envPath(h)}
}

func (t *HermesTool) configPath(homeDir string) string {
	return envDirFile(homeDir, "HERMES_HOME", "~/.hermes", "config.yaml")
}
func (t *HermesTool) envPath(homeDir string) string {
	return envDirFile(homeDir, "HERMES_HOME", "~/.hermes", ".env")
}

const hermesKeyVar = "OPENAI_API_KEY"

func (t *HermesTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadYAML(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	if str(cfg, "model", "provider") != "custom" {
		return nil, nil
	}
	base := str(cfg, "model", "base_url")
	if base == "" {
		return nil, nil
	}
	key, err := readEnvValue(t.envPath(homeDir), hermesKeyVar)
	if err != nil {
		return nil, err
	}
	return &Settings{BaseURL: base, APIKey: key, Model: str(cfg, "model", "default")}, nil
}

func (t *HermesTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadYAML(path)
	if err != nil {
		return err
	}
	model := child(cfg, "model")
	model["default"] = s.Model
	model["provider"] = "custom"
	model["base_url"] = v1URL(s.BaseURL)
	if err := saveYAML(path, cfg); err != nil {
		return err
	}
	// Merge into .env: it may hold the user's other provider keys.
	return setEnvValue(t.envPath(homeDir), hermesKeyVar, s.APIKey)
}

func (t *HermesTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadYAML(path)
	if err != nil {
		return err
	}
	if !exists || str(cfg, "model", "provider") != "custom" {
		return nil
	}
	delete(cfg, "model")
	if err := saveOrRemove(path, cfg, saveYAML); err != nil {
		return err
	}
	return setEnvValue(t.envPath(homeDir), hermesKeyVar, "")
}
