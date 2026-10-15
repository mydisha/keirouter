package clitools

// JcodeTool auto-configures jcode (~/.jcode/config.toml + an env file holding
// the key).
type JcodeTool struct{}

func (t *JcodeTool) ID() string           { return "jcode" }
func (t *JcodeTool) Name() string         { return "jcode" }
func (t *JcodeTool) Commands() []string   { return []string{"jcode"} }
func (t *JcodeTool) Extensions() []string { return nil }
func (t *JcodeTool) ModelRequired() bool  { return true }
func (t *JcodeTool) ConfigFiles(h string) []string {
	return []string{t.configPath(h), t.envPath(h)}
}

func (t *JcodeTool) configPath(homeDir string) string {
	return expandHome(homeDir, "~/.jcode/config.toml")
}
func (t *JcodeTool) envPath(homeDir string) string {
	return expandHome(homeDir, "~/.config/jcode/provider-keirouter.env")
}

const (
	jcodeProvider = "keirouter"
	jcodeKeyVar   = "JCODE_KEIROUTER_API_KEY"
)

func (t *JcodeTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadTOML(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	base := str(cfg, "providers", jcodeProvider, "base_url")
	if base == "" {
		return nil, nil
	}
	envFile := str(cfg, "providers", jcodeProvider, "env_file")
	if envFile == "" {
		envFile = t.envPath(homeDir)
	}
	key, err := readEnvValue(expandHome(homeDir, envFile), jcodeKeyVar)
	if err != nil {
		return nil, err
	}
	return &Settings{BaseURL: base, APIKey: key, Model: str(cfg, "providers", jcodeProvider, "default_model")}, nil
}

func (t *JcodeTool) Configure(homeDir string, s Settings) error {
	path, envP := t.configPath(homeDir), t.envPath(homeDir)
	cfg, _, err := loadTOML(path)
	if err != nil {
		return err
	}
	child(cfg, "providers")[jcodeProvider] = map[string]any{
		"type":          "openai-compatible",
		"base_url":      v1URL(s.BaseURL),
		"auth":          "bearer",
		"api_key_env":   jcodeKeyVar,
		"env_file":      envP,
		"default_model": s.Model,
	}
	if err := saveTOML(path, cfg); err != nil {
		return err
	}
	return setEnvValue(envP, jcodeKeyVar, s.APIKey)
}

func (t *JcodeTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadTOML(path)
	if err != nil {
		return err
	}
	if exists {
		if providers, ok := cfg["providers"].(map[string]any); ok {
			delete(providers, jcodeProvider)
			pruneEmpty(cfg, "providers")
		}
		if err := saveOrRemove(path, cfg, saveTOML); err != nil {
			return err
		}
	}
	return setEnvValue(t.envPath(homeDir), jcodeKeyVar, "")
}
