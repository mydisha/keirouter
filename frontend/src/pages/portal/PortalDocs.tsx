import { useMemo, useState } from "react";
import { BookOpen, Terminal } from "lucide-react";
import { Link } from "react-router-dom";
import { SegmentedControl } from "../../components/ui";
import { portal } from "../../lib/portalRoutes";
import { CopyButton, IdField, PageHeader, PanelCard } from "./components";

type Tool = "opencode" | "claude" | "codex" | "hermes";

interface Guide {
  id: Tool;
  name: string;
  blurb: string;
  configPath: string;
  snippet: string;
}

// Snippets mirror the CLI-tool config the backend generates
// (backend/internal/gateway/clitools_snippets.go), with the base URL taken
// from the browser origin and placeholder key/model.
function buildGuides(baseURL: string): Guide[] {
  const claudeBase = baseURL.replace(/\/v1$/, "");
  return [
    {
      id: "opencode",
      name: "OpenCode",
      blurb: "Add Tokenizer as an OpenAI-compatible provider in opencode.json.",
      configPath: "~/.config/opencode/opencode.json",
      snippet: `{
  "provider": {
    "tokenizer": {
      "npm": "@opencode-ai/provider-openai-compatible",
      "name": "Tokenizer",
      "options": {
        "baseURL": "${baseURL}",
        "apiKey": "<YOUR_API_KEY>"
      },
      "models": {
        "provider/model-id": {
          "name": "provider/model-id",
          "modalities": { "input": ["text"], "output": ["text"] }
        }
      }
    }
  },
  "model": "tokenizer/provider/model-id"
}`,
    },
    {
      id: "claude",
      name: "Claude Code",
      blurb: "Claude Code reads env vars from settings.json. No /v1 suffix here — the SDK appends it.",
      configPath: "~/.claude/settings.json",
      snippet: `{
  "hasCompletedOnboarding": true,
  "env": {
    "ANTHROPIC_BASE_URL": "${claudeBase}",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>"
  }
}`,
    },
    {
      id: "codex",
      name: "Codex CLI",
      blurb: "Write config.toml and auth.json to ~/.codex/.",
      configPath: "~/.codex/config.toml",
      snippet: `model = "provider/model-id"
model_provider = "tokenizer"

[model_providers.tokenizer]
name = "Tokenizer"
base_url = "${baseURL}"
wire_api = "chat"

# ~/.codex/auth.json
# {
#   "auth_mode": "apikey",
#   "OPENAI_API_KEY": "<YOUR_API_KEY>"
# }`,
    },
    {
      id: "hermes",
      name: "Hermes Agent",
      blurb: "Write config.yaml and .env to ~/.hermes/.",
      configPath: "~/.hermes/config.yaml",
      snippet: `model:
  default: "provider/model-id"
  provider: "custom"
  base_url: "${baseURL}"

# ~/.hermes/.env
OPENAI_API_KEY=<YOUR_API_KEY>`,
    },
  ];
}

export function PortalDocsPage() {
  const baseURL = useMemo(() => `${window.location.origin}/v1`, []);
  const guides = useMemo(() => buildGuides(baseURL), [baseURL]);
  const [active, setActive] = useState<Tool>("opencode");
  const guide = guides.find((g) => g.id === active) ?? guides[0];

  return (
    <div className="space-y-6">
      <PageHeader
        icon={BookOpen}
        title="Docs"
        subtitle="Connect your coding agents and CLI tools to this endpoint."
      />

      <PanelCard icon={Terminal} title="Endpoint">
        <div className="space-y-3 p-4 sm:p-5">
          <IdField value={baseURL} />
          <p className="text-[13px] text-fg-muted">
            Use your API key (from the{" "}
            <Link to={portal("/key")} className="font-medium text-link hover:underline">API Key</Link>{" "}
            page) as the bearer token. Replace <code className="font-mono text-[12.5px] text-fg">provider/model-id</code> with a model from the{" "}
            <Link to={portal("/models")} className="font-medium text-link hover:underline">Models</Link> page.
          </p>
        </div>
      </PanelCard>

      <PanelCard
        icon={BookOpen}
        title={guide.name}
        subtitle={guide.blurb}
        action={
          <SegmentedControl<Tool>
            label="Tool"
            value={active}
            onChange={setActive}
            options={guides.map((g) => ({ value: g.id, label: g.name }))}
          />
        }
      >
        <div className="space-y-2 p-4 sm:p-5">
          <div className="flex flex-wrap items-center justify-between gap-2 text-[12.5px]">
            <span className="text-fg-muted">Config file</span>
            <code className="font-mono text-[12.5px] text-fg">{guide.configPath}</code>
          </div>
          <div className="relative rounded-lg border border-line bg-subtle">
            <div className="absolute right-1.5 top-1.5">
              <CopyButton value={guide.snippet} label="Copy snippet" />
            </div>
            <pre className="max-h-[420px] overflow-auto p-4 pr-12 font-mono text-[12.5px] leading-relaxed text-fg">
              <code>{guide.snippet}</code>
            </pre>
          </div>
        </div>
      </PanelCard>
    </div>
  );
}
