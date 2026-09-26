import { useState } from "react";
import { useParams, Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft, Copy, Check, Settings, Play, RotateCcw,
  CheckCircle2, XCircle, CircleDot, Loader2,
  ChevronDown, ChevronUp, TerminalSquare, KeyRound, Globe, Cpu,
} from "lucide-react";
import { api } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { brandColor } from "../lib/brand-colors";
import { useToast } from "../components/Toast";
import {
  Card, SectionHeader, Button, Input, Select, Field,
  Spinner, EmptyState,
} from "../components/ui";

// Tool metadata — descriptions, images, install commands. Colors from brand-colors.ts.
const toolMeta: Record<string, { description: string; image: string; installCmd?: string }> = {
  claude:       { description: "Anthropic's CLI coding agent", image: "/providers/claude.png", installCmd: "npm install -g @anthropic-ai/claude-code" },
  codex:        { description: "OpenAI Codex CLI", image: "/providers/codex.png", installCmd: "npm install -g @openai/codex" },
  cline:        { description: "VS Code AI coding assistant", image: "/providers/cline.png" },
  copilot:      { description: "GitHub Copilot Chat", image: "/providers/copilot.png" },
  droid:        { description: "Factory Droid CLI", image: "/providers/droid.png", installCmd: "curl -fsSL https://factory.ai/install.sh | sh" },
  openclaw:     { description: "OpenClaw agent framework", image: "/providers/openclaw.png", installCmd: "npm install -g openclaw" },
  opencode:     { description: "OpenCode multi-model agent", image: "/providers/opencode.png", installCmd: "npm install -g @opencode-ai/opencode" },
  kilo:         { description: "Kilo Code AI assistant", image: "/providers/kilocode.png", installCmd: "npm install -g kilo-code" },
  hermes:       { description: "Hermes Agent CLI", image: "/providers/hermes.png", installCmd: "npm install -g hermes-agent" },
  deepseek:     { description: "DeepSeek TUI", image: "/providers/deepseek-tui.png", installCmd: "npm install -g deepseek-tui" },
  jcode:        { description: "jcode coding agent", image: "/providers/jcode.png", installCmd: "npm install -g jcode" },
};

export function CLIToolDetailPage() {
  const { toolId } = useParams<{ toolId: string }>();
  const qc = useQueryClient();
  const toast = useToast();

  const tools = useQuery({
    queryKey: ["cli-tools"],
    queryFn: () => api.cliTools(),
  });

  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => api.listKeys(),
  });

  const tool = tools.data?.tools.find((t) => t.id === toolId);
  const meta = toolMeta[toolId ?? ""];

  // Form state
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [showInstall, setShowInstall] = useState(false);
  const [showSnippet, setShowSnippet] = useState(false);

  // Initialize base URL when data loads
  const initializedKey = `${tools.data?.base_url}-${toolId}`;
  const [initKey, setInitKey] = useState("");
  if (tools.data?.base_url && initKey !== initializedKey) {
    setInitKey(initializedKey);
    setBaseUrl(tools.data.base_url);
  }

  // Mutations
  const configureMut = useMutation({
    mutationFn: () =>
      api.cliToolConfigure(toolId!, {
        base_url: baseUrl,
        api_key: apiKey || "sk_keirouter",
        models: model ? [model] : undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cli-tools"] });
      toast.success("Tool configured", `${tool?.name} is now routing through KeiRouter. All requests will use the proxy endpoint.`);
    },
    onError: (e: Error) => toast.error("Tool configuration failed", e.message),
  });

  const removeMut = useMutation({
    mutationFn: () => api.cliToolRemove(toolId!),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cli-tools"] });
      toast.success("Config removed", `${tool?.name} has been disconnected from KeiRouter and will use its default endpoint.`);
    },
    onError: (e: Error) => toast.error("Config removal failed", e.message),
  });

  if (tools.isLoading) return <Spinner />;

  if (!tool) {
    return (
      <div className="space-y-4">
        <Link to={dashboard("/cli-tools")} className="flex items-center gap-1 text-sm text-[var(--text-muted)] hover:text-[var(--text)]">
          <ArrowLeft className="h-4 w-4" /> Back to CLI Tools
        </Link>
        <EmptyState title="Tool not found" />
      </div>
    );
  }

  const snippetWithVars = tool.snippet
    .replace(/http:\/\/localhost:\d+\/v1/g, baseUrl ? `${baseUrl.replace(/\/+$/, "")}/v1` : "http://localhost:20180/v1")
    .replace(/sk_keirouter/g, apiKey || "sk_keirouter");

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <Link to={dashboard("/cli-tools")} className="mb-3 flex items-center gap-1 text-sm text-[var(--text-muted)] hover:text-[var(--text)]">
          <ArrowLeft className="h-4 w-4" /> Back to CLI Tools
        </Link>
        <div className="flex items-center gap-3">
          <ToolIcon id={toolId!} />
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-xl font-semibold">{tool.name}</h1>
              <StatusBadge installed={tool.installed} configured={tool.configured} />
            </div>
            <p className="text-sm text-[var(--text-muted)]">
              {meta?.description ?? tool.instructions}
            </p>
          </div>
        </div>
      </div>

      {/* Not installed warning */}
      {!tool.installed && (
        <Card className="border-amber-500/30 bg-amber-500/5 dark:border-amber-500/20 dark:bg-amber-500/10">
          <div className="flex items-start gap-3 px-6 py-4">
            <XCircle className="mt-0.5 h-5 w-5 shrink-0 text-amber-500" />
            <div className="flex-1">
              <p className="text-sm font-medium text-amber-700 dark:text-amber-400">
                {tool.name} CLI not detected locally
              </p>
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                You can still copy the config snippet below, or install the CLI first.
              </p>
              {meta?.installCmd && (
                <div className="mt-2">
                  <button
                    onClick={() => setShowInstall(!showInstall)}
                    className="flex items-center gap-1 text-xs font-medium text-amber-600 hover:text-amber-700 dark:text-amber-400 dark:hover:text-amber-300"
                  >
                    {showInstall ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
                    How to install
                  </button>
                  {showInstall && (
                    <pre className="mt-2 rounded-lg bg-[var(--bg-subtle)] p-3 font-mono text-xs">
                      {meta.installCmd}
                    </pre>
                  )}
                </div>
              )}
            </div>
          </div>
        </Card>
      )}

      {/* Config form */}
      <Card>
        <SectionHeader
          title="Configuration"
          description="Set the endpoint, API key, and model for this tool."
          icon={Settings}
        />
        <div className="space-y-4 px-6 pb-6">
          {/* Endpoint */}
          <Field label="Endpoint URL">
            <div className="flex items-center gap-2">
              <Globe className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
              <Input
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="http://localhost:20180"
                className="flex-1 font-mono"
              />
            </div>
            <p className="mt-1 text-[10px] text-[var(--text-muted)]">
              Current: <span className="font-mono">{tools.data?.base_url ?? "—"}</span>
            </p>
          </Field>

          {/* API Key */}
          <Field label="API Key">
            <div className="flex items-center gap-2">
              <KeyRound className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
              {keys.data?.keys && keys.data.keys.length > 0 ? (
                <Select
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  className="flex-1"
                >
                  <option value="">sk_keirouter (default)</option>
                  {keys.data.keys.filter((k) => !k.disabled).map((k) => (
                    <option key={k.id} value={k.display}>
                      {k.name || k.display}
                    </option>
                  ))}
                  <option value="__custom__">Custom…</option>
                </Select>
              ) : (
                <Input
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder="sk_keirouter"
                  type="password"
                  className="flex-1"
                />
              )}
            </div>
            {apiKey === "__custom__" && (
              <Input
                value=""
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="Enter custom API key"
                type="password"
                className="mt-2 ml-6 max-w-md"
              />
            )}
          </Field>

          {/* Model */}
          <Field label="Default model (optional)">
            <div className="flex items-center gap-2">
              <Cpu className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
              <Input
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="provider/model-id or chain:my-chain"
                className="flex-1 font-mono"
              />
            </div>
          </Field>

          {/* Action buttons */}
          <div className="flex items-center gap-2 pt-2">
            <Button
              onClick={() => configureMut.mutate()}
              disabled={configureMut.isPending || !baseUrl}
            >
              {configureMut.isPending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Play className="h-4 w-4" />
              )}
              Apply
            </Button>
            {tool.configured && (
              <Button
                variant="ghost"
                onClick={() => removeMut.mutate()}
                disabled={removeMut.isPending}
              >
                {removeMut.isPending ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <RotateCcw className="h-4 w-4" />
                )}
                Reset
              </Button>
            )}
            <Button
              variant="ghost"
              onClick={() => setShowSnippet(!showSnippet)}
            >
              <TerminalSquare className="h-4 w-4" />
              {showSnippet ? "Hide" : "Show"} snippet
            </Button>
          </div>

          {/* Success/error feedback */}
          {configureMut.isSuccess && (
            <p className="text-xs text-emerald-600 dark:text-emerald-400">
              ✓ Configured successfully — restart {tool.name} to pick up changes.
            </p>
          )}
          {configureMut.isError && (
            <p className="text-xs text-[color:var(--color-danger)]">
              ✗ {(configureMut.error as Error)?.message}
            </p>
          )}
          {removeMut.isSuccess && (
            <p className="text-xs text-emerald-600 dark:text-emerald-400">
              ✓ Config removed.
            </p>
          )}
        </div>
      </Card>

      {/* Snippet preview */}
      {showSnippet && (
        <Card>
          <SectionHeader
            title="Config snippet"
            description={tool.instructions}
            icon={TerminalSquare}
            iconTone="neutral"
            action={<CopyButton text={snippetWithVars} />}
          />
          <div className="px-6 pb-6">
            <pre className="overflow-x-auto rounded-lg bg-[var(--bg-subtle)] p-4 font-mono text-xs leading-relaxed">
              {snippetWithVars}
            </pre>
          </div>
        </Card>
      )}

      {/* Config path info */}
      {tool.config_path && (
        <Card className="px-6 py-4">
          <div className="flex items-center gap-2 text-xs text-[var(--text-muted)]">
            <TerminalSquare className="h-3.5 w-3.5" />
            <span>Config path:</span>
            <span className="font-mono">{tool.config_path}</span>
          </div>
        </Card>
      )}
    </div>
  );
}

function ToolIcon({ id }: { id: string }) {
  const [errored, setErrored] = useState(false);
  const meta = toolMeta[id];
  if (errored || !meta?.image) {
    return (
      <div
        className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl text-lg font-bold text-white"
        style={{ backgroundColor: brandColor(id) }}
      >
        {id.slice(0, 2).toUpperCase()}
      </div>
    );
  }
  return (
    <img
      src={meta.image}
      alt={id}
      onError={() => setErrored(true)}
      className="h-12 w-12 shrink-0 rounded-xl object-contain"
    />
  );
}

function StatusBadge({ installed, configured }: { installed: boolean; configured: boolean }) {
  if (configured) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400">
        <CheckCircle2 className="h-3 w-3" />
        Connected
      </span>
    );
  }
  if (installed) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
        <XCircle className="h-3 w-3" />
        Not configured
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[var(--bg-subtle)] px-2 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
      <CircleDot className="h-3 w-3" />
      Not installed
    </span>
  );
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch { /* noop */ }
  };
  return (
    <Button variant="ghost" onClick={copy} className="px-2">
      {copied ? (
        <Check className="h-4 w-4 text-emerald-500" />
      ) : (
        <Copy className="h-4 w-4" />
      )}
      {copied ? "Copied!" : "Copy"}
    </Button>
  );
}
