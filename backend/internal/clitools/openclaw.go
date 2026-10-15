package clitools

import "strings"

// OpenClawTool auto-configures OpenClaw (~/.openclaw/openclaw.json).
type OpenClawTool struct{}

func (t *OpenClawTool) ID() string                    { return "openclaw" }
func (t *OpenClawTool) Name() string                  { return "OpenClaw" }
func (t *OpenClawTool) Commands() []string            { return []string{"openclaw"} }
func (t *OpenClawTool) Extensions() []string          { return nil }
func (t *OpenClawTool) ModelRequired() bool           { return true }
func (t *OpenClawTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

func (t *OpenClawTool) configPath(homeDir string) string {
	return expandHome(homeDir, "~/.openclaw/openclaw.json")
}

const openClawProvider = "keirouter"

func (t *OpenClawTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadJSONObject(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	base := str(cfg, "models", "providers", openClawProvider, "baseUrl")
	if base == "" {
		return nil, nil
	}
	s := &Settings{BaseURL: base, APIKey: str(cfg, "models", "providers", openClawProvider, "apiKey")}
	if primary := str(cfg, "agents", "defaults", "model", "primary"); strings.HasPrefix(primary, openClawProvider+"/") {
		s.Model = strings.TrimPrefix(primary, openClawProvider+"/")
	} else if models, ok := lookup(cfg, "models", "providers", openClawProvider, "models").([]any); ok && len(models) > 0 {
		// Accept both the object form and the bare-string form older
		// KeiRouter versions wrote.
		switch m := models[0].(type) {
		case string:
			s.Model = m
		case map[string]any:
			s.Model, _ = m["id"].(string)
		}
	}
	return s, nil
}

func (t *OpenClawTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	// Merge into models.providers so the user's other providers survive.
	child(child(cfg, "models"), "providers")[openClawProvider] = map[string]any{
		"baseUrl": v1URL(s.BaseURL),
		"apiKey":  s.APIKey,
		"api":     "openai-completions",
		"models":  []any{map[string]any{"id": s.Model, "name": s.Model}},
	}
	child(child(child(cfg, "agents"), "defaults"), "model")["primary"] = openClawProvider + "/" + s.Model
	return saveJSON(path, cfg)
}

func (t *OpenClawTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadJSONObject(path)
	if err != nil || !exists {
		return err
	}
	if models, ok := cfg["models"].(map[string]any); ok {
		if providers, ok := models["providers"].(map[string]any); ok {
			delete(providers, openClawProvider)
			pruneEmpty(models, "providers")
		}
		pruneEmpty(cfg, "models")
	}
	if agents, ok := cfg["agents"].(map[string]any); ok {
		if defaults, ok := agents["defaults"].(map[string]any); ok {
			if model, ok := defaults["model"].(map[string]any); ok {
				if primary, _ := model["primary"].(string); strings.HasPrefix(primary, openClawProvider+"/") {
					delete(model, "primary")
				}
				pruneEmpty(defaults, "model")
			}
			pruneEmpty(agents, "defaults")
		}
		pruneEmpty(cfg, "agents")
	}
	return saveOrRemove(path, cfg, saveJSONObject)
}
