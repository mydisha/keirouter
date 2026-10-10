import { useId, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useParams, Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft, Copy, Check, ChevronRight, Loader2, RotateCcw, FileCog, AlertTriangle,
} from "lucide-react";
import { api } from "../lib/api";
import { useToast } from "../components/Toast";
import { ProviderLogo } from "../components/ProviderLogo";
import { useConfirm } from "../components/ui/confirm-dialog";
import { Badge, Button, IconTile, Input, Select, Skeleton } from "../components/ui";
import { ICONS } from "../lib/icons";
import { cn } from "@/lib/utils";

// Tool metadata — descriptions, logos, install commands.
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

const CUSTOM_KEY = "__custom__";

export function CLIToolDetailPage() {
  const { toolId } = useParams<{ toolId: string }>();
  const qc = useQueryClient();
  const toast = useToast();
  const askConfirm = useConfirm();

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
  const [customKey, setCustomKey] = useState("");
  const [model, setModel] = useState("");

  // The key that is written / shown in the snippet. "Custom…" in the key
  // picker switches to a free-text field without losing the selection.
  const effectiveKey = apiKey === CUSTOM_KEY ? customKey : apiKey;

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
        api_key: effectiveKey || "sk_keirouter",
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

  const snippetWithVars = (tool?.snippet ?? "")
    .replace(/http:\/\/localhost:\d+\/v1/g, baseUrl ? `${baseUrl.replace(/\/+$/, "")}/v1` : "http://localhost:20180/v1")
    .replace(/sk_keirouter/g, effectiveKey || "sk_keirouter");
  const files = useMemo(() => splitSnippet(snippetWithVars), [snippetWithVars]);
  const envVars = useMemo(() => extractEnvVars(files), [files]);

  if (tools.isLoading) {
    return (
      <div className="space-y-4" aria-busy="true" aria-label="Loading CLI tool">
        <Skeleton className="h-5 w-40" />
        <div className="flex items-center gap-3">
          <Skeleton className="h-10 w-10 rounded-lg" />
          <div className="space-y-2">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-3.5 w-72" />
          </div>
        </div>
        <Skeleton className="h-96 w-full rounded-2xl" />
      </div>
    );
  }

  if (!tool) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <IconTile icon={ICONS.cliTools} size="lg" className="mx-auto mb-3" />
        <h1 className="text-[13px] font-semibold text-fg">This CLI tool doesn&apos;t exist</h1>
        <Link to="/cli-tools" className="mt-2 inline-block text-[13px] font-medium text-link hover:underline">
          Back to CLI tools
        </Link>
      </div>
    );
  }

  const configTarget = tool.config_path || "its config file";

  const applyConfig = async () => {
    const ok = await askConfirm({
      title: `Write ${tool.name} config?`,
      description: (
        <>
          KeiRouter writes the endpoint, key{model ? " and model" : ""} into{" "}
          <span className="font-mono text-[12px] text-fg">{configTarget}</span>. Existing provider settings in that file are
          replaced. Restart {tool.name} afterwards to pick up the change.
        </>
      ),
      confirmLabel: "Write config",
    });
    if (ok) configureMut.mutate();
  };

  const resetConfig = async () => {
    const ok = await askConfirm({
      title: `Disconnect ${tool.name}?`,
      description: `The KeiRouter settings are removed from ${configTarget}, so ${tool.name} goes back to its default endpoint. Requests stop routing through KeiRouter.`,
      confirmLabel: "Remove config",
      tone: "danger",
    });
    if (ok) removeMut.mutate();
  };

  const status = tool.configured
    ? { tone: "success" as const, label: "Connected" }
    : tool.installed
      ? { tone: "warning" as const, label: "Not configured" }
      : { tone: "neutral" as const, label: "Not installed" };

  const keyList = keys.data?.keys ?? [];

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 text-[13px] text-fg-muted">
        <ol className="flex items-center gap-1.5">
          <li>
            <Link
              to="/cli-tools"
              className="inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
              CLI tools
            </Link>
          </li>
          <li aria-hidden="true" className="text-fg-faint">
            /
          </li>
          <li className="truncate text-fg" aria-current="page">
            {tool.name}
          </li>
        </ol>
      </nav>

      <header className="mb-5 flex min-w-0 items-center gap-3">
        <ProviderLogo icon={meta?.image} name={tool.name} size={40} className="rounded-lg" />
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{tool.name}</h1>
            <Badge tone={status.tone}>{status.label}</Badge>
          </div>
          <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
            <span>{meta?.description ?? tool.dialect}</span>
            {meta?.description && (
              <>
                <span aria-hidden="true" className="text-fg-faint">
                  ·
                </span>
                <span className="font-mono text-[12.5px]">{tool.dialect}</span>
              </>
            )}
          </p>
        </div>
      </header>

      <ol className="max-w-3xl divide-y divide-line overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        {/* 1. Install */}
        <Step n={1} done={tool.installed} title={`Install ${tool.name}`}>
          {tool.installed ? (
            <p className="flex items-center gap-1.5 text-[13px] text-fg-muted">
              <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" />
              Detected on this machine.
            </p>
          ) : (
            <div className="space-y-3">
              <p className="flex items-start gap-1.5 text-[13px] leading-5 text-fg">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
                <span>Not detected here. Install it, or set it up by hand in step 4.</span>
              </p>
              {meta?.installCmd && <CodeWell code={meta.installCmd} label="Shell" copyLabel="install command" />}
            </div>
          )}
        </Step>

        {/* 2. Connection settings */}
        <Step n={2} title="Choose endpoint and key">
          <div className="max-w-xl space-y-4">
            <FieldRow
              id="cli-endpoint"
              label="Endpoint URL"
              required
              hint={
                <>
                  Gateway: <span className="font-mono">{tools.data?.base_url ?? "—"}</span>
                </>
              }
            >
              <Input
                id="cli-endpoint"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="http://localhost:20180"
                className="font-mono"
                aria-required="true"
                aria-describedby="cli-endpoint-hint"
              />
            </FieldRow>

            <FieldRow id="cli-key" label="API key" hint="Use a dedicated key so you can revoke it on its own.">
              {keyList.length > 0 ? (
                <Select id="cli-key" value={apiKey} onChange={(e) => setApiKey(e.target.value)} aria-describedby="cli-key-hint">
                  <option value="">sk_keirouter (default)</option>
                  {keyList.filter((k) => !k.disabled).map((k) => (
                    <option key={k.id} value={k.display}>
                      {k.name || k.display}
                    </option>
                  ))}
                  <option value={CUSTOM_KEY}>Custom…</option>
                </Select>
              ) : (
                <Input
                  id="cli-key"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder="sk_keirouter"
                  type="password"
                  className="font-mono"
                  aria-describedby="cli-key-hint"
                />
              )}
              {apiKey === CUSTOM_KEY && (
                <Input
                  value={customKey}
                  onChange={(e) => setCustomKey(e.target.value)}
                  placeholder="Enter custom API key"
                  aria-label="Custom API key"
                  type="password"
                  className="mt-2 font-mono"
                  autoFocus
                />
              )}
            </FieldRow>

            <FieldRow id="cli-model" label="Default model" optional hint="Leave empty to pick a model inside the tool.">
              <Input
                id="cli-model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="provider/model-id or chain:my-chain"
                className="font-mono"
                aria-describedby="cli-model-hint"
              />
            </FieldRow>
          </div>
        </Step>

        {/* 3. Apply */}
        <Step
          n={3}
          done={tool.configured}
          title="Write the config"
          description={
            tool.config_path ? (
              <>
                Updates <span className="break-all font-mono text-[12px] text-fg">{tool.config_path}</span>
              </>
            ) : (
              "Updates the tool's config file on this machine."
            )
          }
        >
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button onClick={applyConfig} disabled={configureMut.isPending || !baseUrl}>
              {configureMut.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <FileCog aria-hidden="true" />}
              {tool.configured ? "Rewrite config" : "Write config"}
            </Button>
            {tool.configured && (
              <Button variant="danger" onClick={resetConfig} disabled={removeMut.isPending}>
                {removeMut.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RotateCcw aria-hidden="true" />}
                Remove config
              </Button>
            )}
          </div>

          <div role="status" className="empty:hidden">
            {configureMut.isSuccess && (
              <p className="mt-3 flex items-center gap-1.5 text-[12.5px] text-ok">
                <Check className="h-3.5 w-3.5" aria-hidden="true" />
                Configured. Restart {tool.name} to pick up the change.
              </p>
            )}
            {removeMut.isSuccess && (
              <p className="mt-3 flex items-center gap-1.5 text-[12.5px] text-ok">
                <Check className="h-3.5 w-3.5" aria-hidden="true" />
                Config removed.
              </p>
            )}
          </div>
          {configureMut.isError && (
            <p role="alert" className="mt-3 flex items-start gap-1.5 text-[12.5px] text-bad">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              <span className="break-words">{(configureMut.error as Error)?.message}</span>
            </p>
          )}
        </Step>

        {/* 4. Manual */}
        <li className="px-4 py-4 sm:px-5">
          <details className="group" open={!tool.installed}>
            <summary className="flex cursor-pointer list-none items-center gap-3 rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [&::-webkit-details-marker]:hidden">
              <StepNumber n={4} />
              <h2 className="flex min-w-0 flex-1 items-center gap-1.5 text-[13px] font-semibold leading-5 text-fg">
                <span className="sr-only">Step 4: </span>
                Or set it up by hand
                <ChevronRight
                  className="h-3.5 w-3.5 text-fg-faint transition-transform group-open:rotate-90"
                  strokeWidth={1.75}
                  aria-hidden="true"
                />
              </h2>
            </summary>
            <div className="mt-3 space-y-4 sm:pl-10">
              {tool.instructions && <p className="text-[12.5px] leading-5 text-fg-muted">{tool.instructions}</p>}
              <SnippetWell files={files} fullText={snippetWithVars} />
              {envVars.length > 0 && <EnvTable vars={envVars} />}
            </div>
          </details>
        </li>
      </ol>
    </>
  );
}

// ── Layout pieces ────────────────────────────────────────────────────────────

// StepNumber is a 28px tone tile with the step number; a finished step
// (tool installed / config written) turns into an "ok" tile.
function StepNumber({ n, done }: { n: number; done?: boolean }) {
  return (
    <span
      className={cn(
        "flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-[12px] font-semibold tabular-nums ring-1 ring-inset",
        done ? "bg-ok/10 text-ok ring-ok/20" : "bg-tone-soft text-tone ring-tone-ring",
      )}
      aria-hidden="true"
    >
      {n}
    </span>
  );
}

function Step({
  n,
  title,
  description,
  done,
  children,
}: {
  n: number;
  title: string;
  description?: ReactNode;
  done?: boolean;
  children: ReactNode;
}) {
  return (
    <li className="flex gap-3 px-4 py-4 sm:px-5">
      <StepNumber n={n} done={done} />
      <div className="min-w-0 flex-1">
        <h2 className="mt-1 text-[13px] font-semibold leading-5 text-fg">
          <span className="sr-only">
            Step {n}
            {done ? " (done)" : ""}:{" "}
          </span>
          {title}
        </h2>
        {description && <p className="mt-0.5 text-[12.5px] leading-5 text-fg-muted">{description}</p>}
        <div className="mt-3">{children}</div>
      </div>
    </li>
  );
}

function FieldRow({
  id,
  label,
  hint,
  optional,
  required,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  required?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
        {required && <span className="text-[12px] font-normal text-fg-faint">Required</span>}
      </label>
      {children}
      {hint && (
        <p id={`${id}-hint`} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

// ── Snippets ─────────────────────────────────────────────────────────────────

interface SnippetFile {
  path: string;
  notes: string[];
  body: string;
}

// splitSnippet turns the backend's multi-file snippet ("# ~/path" header lines
// followed by file contents) into one entry per file. A fully commented file
// (e.g. Codex's optional auth.json) is shown uncommented so it can be pasted,
// and leading comment lines become notes above the code.
function splitSnippet(snippet: string): SnippetFile[] {
  const sections: { path: string; lines: string[] }[] = [];
  let current: { path: string; lines: string[] } | null = null;
  for (const line of snippet.split("\n")) {
    const header = /^#\s+((?:~|\/)\S.*)$/.exec(line);
    if (header) {
      current = { path: header[1].trim(), lines: [] };
      sections.push(current);
      continue;
    }
    if (!current) {
      current = { path: "", lines: [] };
      sections.push(current);
    }
    current.lines.push(line);
  }

  return sections
    .map(({ path, lines }) => {
      while (lines.length && !lines[lines.length - 1].trim()) lines.pop();
      while (lines.length && !lines[0].trim()) lines.shift();
      const nonEmpty = lines.filter((l) => l.trim());
      let body = lines;
      const notes: string[] = [];
      if (nonEmpty.length > 0 && nonEmpty.every((l) => /^#( |$)/.test(l))) {
        body = lines.map((l) => l.replace(/^# ?/, ""));
      } else {
        while (body.length && /^#\s/.test(body[0])) {
          notes.push(body[0].replace(/^#\s+/, "").replace(/^\((.*)\)$/, "$1"));
          body = body.slice(1);
        }
      }
      return { path, notes, body: body.join("\n") };
    })
    .filter((s) => s.body.trim() || s.path);
}

interface EnvVar {
  name: string;
  value: string;
  file: string;
}

// extractEnvVars finds environment-variable style keys (UPPER_SNAKE) in the
// snippet files, whether set in a JSON "env" block or a KEY=value env file.
function extractEnvVars(files: SnippetFile[]): EnvVar[] {
  const out: EnvVar[] = [];
  const seen = new Set<string>();
  for (const f of files) {
    for (const line of f.body.split("\n")) {
      const m =
        /^\s*"([A-Z][A-Z0-9_]{2,})"\s*:\s*"([^"]*)"/.exec(line) ??
        /^\s*(?:export\s+)?([A-Z][A-Z0-9_]{2,})=(.*)$/.exec(line);
      if (!m) continue;
      const name = m[1];
      const value = m[2].trim().replace(/^"(.*)"$/, "$1");
      const id = `${f.path}:${name}`;
      if (seen.has(id)) continue;
      seen.add(id);
      out.push({ name, value, file: f.path });
    }
  }
  return out;
}

function basename(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}

function SnippetWell({ files, fullText }: { files: SnippetFile[]; fullText: string }) {
  const [active, setActive] = useState(0);
  const baseId = useId();
  const idx = Math.min(active, Math.max(0, files.length - 1));
  const file = files[idx];
  if (!file) return null;
  const multi = files.length > 1;
  const tabId = (i: number) => `${baseId}-tab-${i}`;
  const panelId = `${baseId}-panel`;

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const n = files.length;
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? n - 1
          : (idx + (event.key === "ArrowRight" ? 1 : -1) + n) % n;
    setActive(next);
    document.getElementById(tabId(next))?.focus();
  };

  return (
    <div className="overflow-hidden rounded-xl border border-line bg-subtle">
      <div className="flex items-center justify-between gap-2 border-b border-line pl-1 pr-1.5">
        {multi ? (
          <div className="flex min-w-0 gap-1 overflow-x-auto" role="tablist" aria-label="Config files" onKeyDown={onKeyDown}>
            {files.map((f, i) => {
              const on = i === idx;
              return (
                <button
                  key={`${f.path}-${i}`}
                  id={tabId(i)}
                  type="button"
                  role="tab"
                  aria-selected={on}
                  aria-controls={panelId}
                  tabIndex={on ? 0 : -1}
                  title={f.path}
                  onClick={() => setActive(i)}
                  className={cn(
                    "relative whitespace-nowrap px-2.5 py-2 font-mono text-[12px] transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                    on ? "text-fg" : "text-fg-muted hover:text-fg",
                  )}
                >
                  {f.path ? basename(f.path) : "Snippet"}
                  {on && <span aria-hidden="true" className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-500" />}
                </button>
              );
            })}
          </div>
        ) : (
          <span className="truncate px-2.5 py-2 font-mono text-[12px] text-fg-muted">{file.path || "Snippet"}</span>
        )}
        <div className="flex shrink-0 items-center gap-1">
          {multi && <CopyButton text={fullText} label="Copy all" toastLabel="config files" />}
          <CopyButton text={file.body} label="Copy" toastLabel={file.path ? basename(file.path) : "snippet"} />
        </div>
      </div>
      <div
        id={panelId}
        role={multi ? "tabpanel" : "region"}
        aria-labelledby={multi ? tabId(idx) : undefined}
        aria-label={multi ? undefined : file.path || "Config snippet"}
        tabIndex={0}
        className="focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      >
        {multi && file.path && (
          <p className="border-b border-line px-3.5 py-1.5 font-mono text-[11.5px] text-fg-muted">{file.path}</p>
        )}
        {file.notes.length > 0 && (
          <p className="border-b border-line px-3.5 py-1.5 text-[12px] text-fg-muted">{file.notes.join(" ")}</p>
        )}
        <pre className="max-h-[420px] overflow-auto px-3.5 py-3 font-mono text-[12px] leading-5 text-fg">{file.body}</pre>
      </div>
    </div>
  );
}

function CodeWell({ code, label, copyLabel }: { code: string; label: string; copyLabel: string }) {
  return (
    <div className="overflow-hidden rounded-xl border border-line bg-subtle">
      <div className="flex items-center justify-between gap-2 border-b border-line pl-3.5 pr-1.5">
        <span className="py-1.5 text-[12px] text-fg-muted">{label}</span>
        <CopyButton text={code} label="Copy" toastLabel={copyLabel} />
      </div>
      <pre className="overflow-x-auto px-3.5 py-2.5 font-mono text-[12px] leading-5 text-fg">{code}</pre>
    </div>
  );
}

function EnvTable({ vars }: { vars: EnvVar[] }) {
  return (
    <div className="overflow-hidden rounded-xl border border-line">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[520px] text-[13px]">
          <caption className="sr-only">Environment variables in the config</caption>
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th scope="col" className="px-3.5 py-2 font-medium">Environment variable</th>
              <th scope="col" className="px-3.5 py-2 font-medium">Value</th>
              <th scope="col" className="px-3.5 py-2 font-medium">File</th>
              <th scope="col" className="w-10 px-2 py-2">
                <span className="sr-only">Copy</span>
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {vars.map((v) => (
              <tr key={`${v.file}:${v.name}`} className="transition-colors hover:bg-hover">
                <td className="whitespace-nowrap px-3.5 py-2 font-mono text-[12px] text-fg">{v.name}</td>
                <td className="max-w-[260px] truncate px-3.5 py-2 font-mono text-[12px] text-fg-muted" title={v.value}>{v.value}</td>
                <td className="whitespace-nowrap px-3.5 py-2 font-mono text-[12px] text-fg-muted">{v.file ? basename(v.file) : "—"}</td>
                <td className="px-2 py-1.5 text-right">
                  <CopyButton text={v.value} toastLabel={`${v.name} value`} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function CopyButton({ text, label, toastLabel }: { text: string; label?: string; toastLabel: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success("Copied", toastLabel);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("Copy failed", "The browser blocked clipboard access.");
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label ? `${label} ${toastLabel}` : `Copy ${toastLabel}`}
      title={label ? undefined : `Copy ${toastLabel}`}
      className="inline-flex h-7 min-w-7 items-center justify-center gap-1.5 rounded-lg px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" />
      ) : (
        <Copy className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
      )}
      {label && (copied ? "Copied" : label)}
    </button>
  );
}
