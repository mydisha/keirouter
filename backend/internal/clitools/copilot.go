package clitools

import (
	"path/filepath"
	"strings"
)

// CopilotTool auto-configures VS Code Copilot Chat (chatLanguageModels.json),
// registering KeiRouter as a custom "azure"-vendor model provider.
type CopilotTool struct{}

func (t *CopilotTool) ID() string                    { return "copilot" }
func (t *CopilotTool) Name() string                  { return "GitHub Copilot (VS Code)" }
func (t *CopilotTool) Commands() []string            { return nil }
func (t *CopilotTool) Extensions() []string          { return []string{"github.copilot-chat"} }
func (t *CopilotTool) ModelRequired() bool           { return true }
func (t *CopilotTool) ConfigFiles(h string) []string { return []string{t.configPath(h)} }

func (t *CopilotTool) configPath(homeDir string) string {
	return filepath.Join(vscodeUserDir(homeDir), "chatLanguageModels.json")
}

const (
	copilotEntryName = "KeiRouter"
	// copilotURLSuffix routes the azure vendor's request to a plain
	// OpenAI-compatible chat endpoint.
	copilotURLSuffix = "/chat/completions#models.ai.azure.com"
)

func (t *CopilotTool) load(path string) ([]map[string]any, bool, error) {
	var entries []map[string]any
	exists, err := loadJSON(path, &entries)
	return entries, exists, err
}

// partition splits entries into the KeiRouter entry and everything else.
func (t *CopilotTool) partition(entries []map[string]any) (ours map[string]any, others []map[string]any) {
	others = make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if name, _ := e["name"].(string); name == copilotEntryName {
			ours = e
			continue
		}
		others = append(others, e)
	}
	return ours, others
}

func (t *CopilotTool) Read(homeDir string) (*Settings, error) {
	entries, exists, err := t.load(t.configPath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	ours, _ := t.partition(entries)
	if ours == nil {
		return nil, nil
	}
	s := &Settings{}
	s.APIKey, _ = ours["apiKey"].(string)
	if models, ok := ours["models"].([]any); ok && len(models) > 0 {
		if m, ok := models[0].(map[string]any); ok {
			s.Model, _ = m["id"].(string)
			u, _ := m["url"].(string)
			s.BaseURL = strings.TrimSuffix(u, copilotURLSuffix)
		}
	}
	if s.BaseURL == "" {
		return nil, nil
	}
	return s, nil
}

func (t *CopilotTool) Configure(homeDir string, s Settings) error {
	path := t.configPath(homeDir)
	entries, _, err := t.load(path)
	if err != nil {
		return err
	}
	_, others := t.partition(entries)
	others = append(others, map[string]any{
		"name":   copilotEntryName,
		"vendor": "azure",
		"apiKey": s.APIKey,
		"models": []map[string]any{{
			"id":              s.Model,
			"name":            s.Model,
			"url":             v1URL(s.BaseURL) + copilotURLSuffix,
			"toolCalling":     true,
			"maxInputTokens":  128000,
			"maxOutputTokens": 16384,
		}},
	})
	return saveJSON(path, others)
}

func (t *CopilotTool) Remove(homeDir string) error {
	path := t.configPath(homeDir)
	entries, exists, err := t.load(path)
	if err != nil || !exists {
		return err
	}
	ours, others := t.partition(entries)
	if ours == nil {
		return nil
	}
	if len(others) == 0 {
		return removeFile(path)
	}
	return saveJSON(path, others)
}
