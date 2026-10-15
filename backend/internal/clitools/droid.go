package clitools

import "strings"

// DroidTool auto-configures Factory Droid (~/.factory/settings.json
// "customModels").
type DroidTool struct{}

func (t *DroidTool) ID() string                    { return "droid" }
func (t *DroidTool) Name() string                  { return "Factory Droid" }
func (t *DroidTool) Commands() []string            { return []string{"droid"} }
func (t *DroidTool) Extensions() []string          { return nil }
func (t *DroidTool) ModelRequired() bool           { return true }
func (t *DroidTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

func (t *DroidTool) configPath(homeDir string) string {
	return expandHome(homeDir, "~/.factory/settings.json")
}

// droidIDPrefix marks custom-model entries KeiRouter owns.
const droidIDPrefix = "custom:KeiRouter"

func isDroidEntry(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	id, _ := m["id"].(string)
	return strings.HasPrefix(id, droidIDPrefix)
}

func (t *DroidTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadJSONObject(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	list, _ := cfg["customModels"].([]any)
	for _, v := range list {
		if !isDroidEntry(v) {
			continue
		}
		m := v.(map[string]any)
		base, _ := m["baseUrl"].(string)
		if base == "" {
			continue
		}
		key, _ := m["apiKey"].(string)
		model, _ := m["model"].(string)
		return &Settings{BaseURL: base, APIKey: key, Model: model}, nil
	}
	return nil, nil
}

func (t *DroidTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	cfg, _, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	// Keep the user's own custom models; replace only KeiRouter's entry.
	list, _ := cfg["customModels"].([]any)
	kept := make([]any, 0, len(list)+1)
	for _, v := range list {
		if !isDroidEntry(v) {
			kept = append(kept, v)
		}
	}
	kept = append(kept, map[string]any{
		"model":           s.Model,
		"id":              droidIDPrefix + "-0",
		"baseUrl":         v1URL(s.BaseURL),
		"apiKey":          s.APIKey,
		"displayName":     s.Model + " (KeiRouter)",
		"maxOutputTokens": 131072,
		"provider":        "openai",
	})
	cfg["customModels"] = kept
	cfg["activeModel"] = droidIDPrefix + "-0"
	return saveJSON(path, cfg)
}

func (t *DroidTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	cfg, exists, err := loadJSONObject(path)
	if err != nil || !exists {
		return err
	}
	if list, ok := cfg["customModels"].([]any); ok {
		kept := make([]any, 0, len(list))
		for _, v := range list {
			if !isDroidEntry(v) {
				kept = append(kept, v)
			}
		}
		if len(kept) == 0 {
			delete(cfg, "customModels")
		} else {
			cfg["customModels"] = kept
		}
	}
	if active, _ := cfg["activeModel"].(string); strings.HasPrefix(active, droidIDPrefix) {
		delete(cfg, "activeModel")
	}
	return saveOrRemove(path, cfg, saveJSONObject)
}
