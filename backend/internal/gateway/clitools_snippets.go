package gateway

import (
	"fmt"
	"strings"
)

// cliToolSnippet holds the display metadata and generated config snippet for
// one CLI tool. The fields map 1-to-1 to the frontend CLITool interface.
type cliToolSnippet struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Dialect      string `json:"dialect"`
	Instructions string `json:"instructions"`
	Snippet      string `json:"snippet"`
}

// cliKeyPlaceholder stands in for the API key in manual-setup snippets; the
// dashboard swaps it for the key the user picks.
const cliKeyPlaceholder = "kr_YOUR_API_KEY"

// snippetsByID indexes generateSnippets by tool id.
func snippetsByID(baseURL, model string) map[string]cliToolSnippet {
	list := generateSnippets(baseURL, model, "")
	out := make(map[string]cliToolSnippet, len(list))
	for _, sn := range list {
		out[sn.ID] = sn
	}
	return out
}

// generateSnippets returns snippets for every registered CLI tool, using the
// given base URL and model. The baseURL is the public KeiRouter endpoint (with
// /v1 appended); model may be empty (the user can edit the snippet).
func generateSnippets(baseURL, model, apiKey string) []cliToolSnippet {
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}
	if apiKey == "" {
		apiKey = cliKeyPlaceholder
	}
	modelStr := model
	if modelStr == "" {
		modelStr = "provider/model-id"
	}

	return []cliToolSnippet{
		snippetClaude(baseURL, apiKey, modelStr),
		snippetCodex(baseURL, apiKey, modelStr),
		snippetCline(baseURL, apiKey, modelStr),
		snippetCopilot(baseURL, apiKey, modelStr),
		snippetDroid(baseURL, apiKey, modelStr),
		snippetOpenClaw(baseURL, apiKey, modelStr),
		snippetOpenCode(baseURL, apiKey, modelStr),
		snippetKilo(baseURL, apiKey, modelStr),
		snippetHermes(baseURL, apiKey, modelStr),
		snippetDeepSeek(baseURL, apiKey, modelStr),
		snippetJcode(baseURL, apiKey, modelStr),
	}
}

// ---- individual tool snippets -----------------------------------------------

func snippetClaude(baseURL, apiKey, model string) cliToolSnippet {
	// The Anthropic SDK appends /v1/messages to ANTHROPIC_BASE_URL, so we must
	// strip the /v1 suffix to avoid double /v1/v1/messages.
	claudeBaseURL := strings.TrimSuffix(baseURL, "/v1")
	snippet := fmt.Sprintf(`# ~/.claude/settings.json
{
  "env": {
    "ANTHROPIC_BASE_URL": "%s",
    "ANTHROPIC_AUTH_TOKEN": "%s",
    "ANTHROPIC_MODEL": "%s"
  }
}`, claudeBaseURL, apiKey, model)
	return cliToolSnippet{
		ID:           "claude",
		Name:         "Claude Code",
		Dialect:      "json",
		Instructions: "Paste into ~/.claude/settings.json — Claude Code reads env vars from here.",
		Snippet:      snippet,
	}
}

func snippetCodex(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.codex/config.toml
model = "%s"
model_provider = "keirouter"

[model_providers.keirouter]
name = "KeiRouter"
base_url = "%s"
wire_api = "responses"
experimental_bearer_token = "%s"`, model, baseURL, apiKey)
	return cliToolSnippet{
		ID:           "codex",
		Name:         "Codex CLI",
		Dialect:      "toml",
		Instructions: "Merge into ~/.codex/config.toml — the key lives in the provider block, so your Codex login in auth.json is left alone.",
		Snippet:      snippet,
	}
}

func snippetCline(baseURL, apiKey, model string) cliToolSnippet {
	// Cline hands the base URL to the OpenAI SDK, which appends
	// /chat/completions — so it keeps the /v1 suffix.
	snippet := fmt.Sprintf(`# ~/.cline/data/globalState.json (partial)
{
  "actModeApiProvider": "openai",
  "planModeApiProvider": "openai",
  "openAiBaseUrl": "%s",
  "openAiModelId": "%s",
  "planModeOpenAiModelId": "%s"
}

# ~/.cline/data/secrets.json
{
  "openAiApiKey": "%s"
}`, baseURL, model, model, apiKey)
	return cliToolSnippet{
		ID:           "cline",
		Name:         "Cline / Roo",
		Dialect:      "json",
		Instructions: "Merge into ~/.cline/data/ JSON files — Cline stores API settings in globalState.json and secrets.json.",
		Snippet:      snippet,
	}
}

func snippetCopilot(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/Library/Application Support/Code/User/chatLanguageModels.json
# (or ~/.config/Code/User/chatLanguageModels.json on Linux)
[
  {
    "name": "KeiRouter",
    "vendor": "azure",
    "apiKey": "%s",
    "models": [
      {
        "id": "%s",
        "name": "%s",
        "url": "%s/chat/completions#models.ai.azure.com",
        "toolCalling": true,
        "vision": false,
        "maxInputTokens": 128000,
        "maxOutputTokens": 16000
      }
    ]
  }
]`, apiKey, model, model, baseURL)
	return cliToolSnippet{
		ID:           "copilot",
		Name:         "GitHub Copilot",
		Dialect:      "json",
		Instructions: "Write to VS Code's chatLanguageModels.json — Copilot Chat reads custom model providers from here.",
		Snippet:      snippet,
	}
}

func snippetDroid(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.factory/settings.json
{
  "customModels": [
    {
      "model": "%s",
      "id": "custom:KeiRouter-0",
      "baseUrl": "%s",
      "apiKey": "%s",
      "displayName": "%s (KeiRouter)",
      "maxOutputTokens": 131072,
      "provider": "openai"
    }
  ],
  "activeModel": "custom:KeiRouter-0"
}`, model, baseURL, apiKey, model)
	return cliToolSnippet{
		ID:           "droid",
		Name:         "Factory Droid",
		Dialect:      "json",
		Instructions: "Write to ~/.factory/settings.json — Droid loads custom models from this file.",
		Snippet:      snippet,
	}
}

func snippetOpenClaw(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.openclaw/openclaw.json
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "keirouter/%s"
      }
    }
  },
  "models": {
    "providers": {
      "keirouter": {
        "baseUrl": "%s",
        "apiKey": "%s",
        "api": "openai-completions",
        "models": [{ "id": "%s", "name": "%s" }]
      }
    }
  }
}`, model, baseURL, apiKey, model, model)
	return cliToolSnippet{
		ID:           "openclaw",
		Name:         "OpenClaw",
		Dialect:      "json",
		Instructions: "Write to ~/.openclaw/openclaw.json — OpenClaw reads provider and model settings from here.",
		Snippet:      snippet,
	}
}

func snippetOpenCode(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.config/opencode/opencode.json
{
  "provider": {
    "keirouter": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "KeiRouter",
      "options": {
        "baseURL": "%s",
        "apiKey": "%s"
      },
      "models": {
        "%s": {
          "name": "%s"
        }
      }
    }
  },
  "model": "keirouter/%s"
}`, baseURL, apiKey, model, model, model)
	return cliToolSnippet{
		ID:           "opencode",
		Name:         "OpenCode",
		Dialect:      "json",
		Instructions: "Write to ~/.config/opencode/opencode.json — OpenCode loads custom providers from this file.",
		Snippet:      snippet,
	}
}

func snippetKilo(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.local/share/kilo/auth.json
{
  "openai-compatible": {
    "type": "api-key",
    "apiKey": "%s",
    "baseUrl": "%s",
    "model": "%s"
  }
}`, apiKey, baseURL, model)
	return cliToolSnippet{
		ID:           "kilo",
		Name:         "Kilo Code",
		Dialect:      "json",
		Instructions: "Write to ~/.local/share/kilo/auth.json — Kilo reads OpenAI-compatible provider config from here.",
		Snippet:      snippet,
	}
}

func snippetHermes(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.hermes/config.yaml
model:
  default: "%s"
  provider: "custom"
  base_url: "%s"

# ~/.hermes/.env
OPENAI_API_KEY=%s`, model, baseURL, apiKey)
	return cliToolSnippet{
		ID:           "hermes",
		Name:         "Hermes Agent",
		Dialect:      "yaml",
		Instructions: "Write config.yaml and .env to ~/.hermes/ — Hermes reads provider settings from YAML and env files.",
		Snippet:      snippet,
	}
}

func snippetDeepSeek(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.deepseek/config.toml
provider = "openai"

[providers.openai]
base_url = "%s"
api_key = "%s"
model = "%s"`, baseURL, apiKey, model)
	return cliToolSnippet{
		ID:           "deepseek",
		Name:         "DeepSeek TUI",
		Dialect:      "toml",
		Instructions: "Write to ~/.deepseek/config.toml — DeepSeek TUI reads provider settings from TOML.",
		Snippet:      snippet,
	}
}

func snippetJcode(baseURL, apiKey, model string) cliToolSnippet {
	snippet := fmt.Sprintf(`# ~/.jcode/config.toml
[providers.keirouter]
type = "openai-compatible"
base_url = "%s"
auth = "bearer"
api_key_env = "JCODE_KEIROUTER_API_KEY"
env_file = "~/.config/jcode/provider-keirouter.env"
default_model = "%s"
requires_api_key = true

[[providers.keirouter.models]]
id = "%s"
name = "%s"

# ~/.config/jcode/provider-keirouter.env
JCODE_KEIROUTER_API_KEY="%s"`, baseURL, model, model, model, apiKey)
	return cliToolSnippet{
		ID:           "jcode",
		Name:         "jcode",
		Dialect:      "toml",
		Instructions: "Write config.toml and env file to ~/.jcode/ — jcode reads provider definitions from TOML.",
		Snippet:      snippet,
	}
}
