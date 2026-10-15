package clitools

// ClineTool auto-configures Cline (~/.cline/data/globalState.json +
// secrets.json), the shared state the Cline CLI reads.
type ClineTool struct{}

func (t *ClineTool) ID() string          { return "cline" }
func (t *ClineTool) Name() string        { return "Cline / Roo" }
func (t *ClineTool) Commands() []string  { return []string{"cline"} }
func (t *ClineTool) ModelRequired() bool { return true }
func (t *ClineTool) Extensions() []string {
	return []string{"saoudrizwan.claude-dev", "rooveterinaryinc.roo-cline"}
}
func (t *ClineTool) ConfigFiles(h string) []string {
	return []string{t.globalStatePath(h), t.secretsPath(h)}
}

func (t *ClineTool) globalStatePath(homeDir string) string {
	return expandHome(homeDir, "~/.cline/data/globalState.json")
}
func (t *ClineTool) secretsPath(homeDir string) string {
	return expandHome(homeDir, "~/.cline/data/secrets.json")
}

// clineStateKeys are the globalState entries KeiRouter owns.
var clineStateKeys = []string{
	"actModeApiProvider", "planModeApiProvider",
	"openAiBaseUrl", "openAiModelId", "planModeOpenAiModelId",
}

func (t *ClineTool) Read(homeDir string) (*Settings, error) {
	cfg, exists, err := loadJSONObject(t.globalStatePath(homeDir))
	if err != nil || !exists {
		return nil, err
	}
	// The OpenAI-compatible base URL only matters while that provider is the
	// active one.
	if str(cfg, "actModeApiProvider") != "openai" {
		return nil, nil
	}
	base := str(cfg, "openAiBaseUrl")
	if base == "" {
		return nil, nil
	}
	sec, _, err := loadJSONObject(t.secretsPath(homeDir))
	if err != nil {
		return nil, err
	}
	return &Settings{BaseURL: base, APIKey: str(sec, "openAiApiKey"), Model: str(cfg, "openAiModelId")}, nil
}

func (t *ClineTool) Configure(homeDir string, s Settings) error {
	gsPath, secPath := t.globalStatePath(homeDir), t.secretsPath(homeDir)
	cfg, _, err := loadJSONObject(gsPath)
	if err != nil {
		return err
	}
	sec, _, err := loadJSONObject(secPath)
	if err != nil {
		return err
	}

	cfg["actModeApiProvider"] = "openai"
	cfg["planModeApiProvider"] = "openai"
	// Cline's OpenAI-compatible provider hands this to the OpenAI SDK, which
	// appends /chat/completions — so it must include /v1.
	cfg["openAiBaseUrl"] = v1URL(s.BaseURL)
	cfg["openAiModelId"] = s.Model
	cfg["planModeOpenAiModelId"] = s.Model
	if err := saveJSON(gsPath, cfg); err != nil {
		return err
	}
	sec["openAiApiKey"] = s.APIKey
	return saveJSON(secPath, sec)
}

func (t *ClineTool) Remove(homeDir string) error {
	gsPath, secPath := t.globalStatePath(homeDir), t.secretsPath(homeDir)
	cfg, cfgExists, err := loadJSONObject(gsPath)
	if err != nil {
		return err
	}
	sec, secExists, err := loadJSONObject(secPath)
	if err != nil {
		return err
	}
	if cfgExists {
		for _, k := range clineStateKeys {
			delete(cfg, k)
		}
		if err := saveOrRemove(gsPath, cfg, saveJSONObject); err != nil {
			return err
		}
	}
	if secExists {
		delete(sec, "openAiApiKey")
		return saveOrRemove(secPath, sec, saveJSONObject)
	}
	return nil
}
