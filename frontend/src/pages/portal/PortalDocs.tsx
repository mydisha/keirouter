import { useMemo, useState } from "react";
import { BookOpen, Terminal } from "lucide-react";
import { Card } from "../../components/ui";
import { CopyButton, PageHeader, SectionTitle } from "./components";

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
    <div className="space-y-8">
      <PageHeader
        title="Docs"
        subtitle="Connect your coding agents and CLI tools to this endpoint."
      />

      <Card className="p-6 md:p-7">
        <SectionTitle title="Endpoint" icon={<Terminal size={17} />} />
        <div className="mt-4 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60 p-3">
          <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-[var(--text)]">{baseURL}</code>
          <CopyButton value={baseURL} />
        </div>
        <p className="mt-3 text-sm text-[var(--text-muted)]">
          Use your API key (from the{" "}
          <a href="/portal/key" className="font-medium text-accent-600 hover:underline dark:text-accent-400">API Key</a>{" "}
          page) as the bearer token. Replace <code className="font-mono">provider/model-id</code> with a model from the{" "}
          <a href="/portal/models" className="font-medium text-accent-600 hover:underline dark:text-accent-400">Models</a> page.
        </p>
      </Card>

      <div className="flex flex-wrap gap-2">
        {guides.map((g) => (
          <button
            key={g.id}
            type="button"
            onClick={() => setActive(g.id)}
            className={`rounded-xl border px-4 py-2 text-sm font-medium transition-colors ${
              active === g.id
                ? "border-accent-500/40 bg-accent-500/10 text-accent-600 dark:text-accent-400"
                : "border-[var(--border)] text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
            }`}
          >
            {g.name}
          </button>
        ))}
      </div>

      <Card className="p-6 md:p-7">
        <SectionTitle title={guide.name} icon={<BookOpen size={17} />} />
        <p className="mt-3 text-sm text-[var(--text-muted)]">{guide.blurb}</p>

        <div className="mt-5 space-y-2">
          <div className="flex items-center justify-between text-xs">
            <span className="font-semibold uppercase tracking-widest text-[var(--text-muted)]">Config file</span>
            <code className="font-mono text-[12px] text-[var(--text)]">{guide.configPath}</code>
          </div>
          <div className="relative rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60">
            <div className="absolute right-2 top-2">
              <CopyButton value={guide.snippet} />
            </div>
            <pre className="max-h-[420px] overflow-auto p-4 pr-12 font-mono text-[12.5px] leading-relaxed text-[var(--text)]">
              <code>{guide.snippet}</code>
            </pre>
          </div>
        </div>
      </Card>
    </div>
  );
}