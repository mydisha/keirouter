import { useEffect, useState, useRef, useCallback, useMemo, useId, type KeyboardEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, Loader2, ArrowUpRight, ChevronRight, KeyRound } from "lucide-react";
import { api, type TailscaleEnableResult } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import {
  Card,
  CardHeader,
  Button,
  Input,
  Field,
  Badge,
  Skeleton,
} from "../components/ui";

// Polling intervals (ms).
const STATUS_POLL_FAST = 5000;
const STATUS_POLL_SLOW = 30000;
const PING_INTERVAL = 2000;
const PING_TIMEOUT = 5000;
const PING_MAX_MS = 300000;
const REACHABLE_MISS_THRESHOLD = 5;

const LINK_BUTTON =
  "inline-flex min-h-9 items-center justify-center gap-1.5 rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[13px] font-medium text-fg transition-colors duration-150 hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [&_svg]:size-4 [&_svg]:shrink-0";
const ICON_BUTTON =
  "inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-fg-faint transition-colors hover:bg-hover hover:text-fg disabled:cursor-not-allowed disabled:opacity-40 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

// ---------------------------------------------------------------------------
// Brand SVG logos (monochrome; colour is reserved for meaning)
// ---------------------------------------------------------------------------

function CloudflareLogo({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true" focusable="false">
      <path d="M16.5088 16.8447c.1475-.5068.0908-.9707-.1553-1.3154-.2246-.3164-.6045-.499-1.0615-.5205l-8.6592-.1123a.1559.1559 0 0 1-.1333-.0713c-.0283-.042-.0351-.0986-.021-.1553.0278-.084.1123-.1484.2036-.1562l8.7359-.1123c1.0351-.0489 2.1601-.8868 2.5537-1.9136l.499-1.3013c.0215-.0561.0293-.1128.0147-.168-.5625-2.5463-2.835-4.4453-5.5499-4.4453-2.5039 0-4.6284 1.6177-5.3876 3.8614-.4927-.3658-1.1187-.5625-1.794-.499-1.2026.119-2.1665 1.083-2.2861 2.2856-.0283.31-.0069.6128.0635.894C1.5683 13.171 0 14.7754 0 16.752c0 .1748.0142.3515.0352.5273.0141.083.0844.1475.1689.1475h15.9814c.0909 0 .1758-.0645.2032-.1553l.12-.4268zm2.7568-5.5634c-.0771 0-.1611 0-.2383.0112-.0566 0-.1054.0415-.127.0976l-.3378 1.1744c-.1475.5068-.0918.9707.1543 1.3164.2256.3164.6055.498 1.0625.5195l1.8437.1133c.0557 0 .1055.0263.1329.0703.0283.043.0351.1074.0214.1562-.0283.084-.1132.1485-.204.1553l-1.921.1123c-1.041.0488-2.1582.8867-2.5527 1.914l-.1406.3585c-.0283.0713.0215.1416.0986.1416h6.5977c.0771 0 .1474-.0489.169-.126.1122-.4082.1757-.837.1757-1.2803 0-2.6025-2.125-4.727-4.7344-4.727" />
    </svg>
  );
}

function TailscaleLogo({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true" focusable="false">
      <path d="M24 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0zm-9 9a3 3 0 1 1-6 0 3 3 0 0 1 6 0zm0-9a3 3 0 1 1-6 0 3 3 0 0 1 6 0zm6-6a3 3 0 1 1 0-6 3 3 0 0 1 0 6zm0-.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM3 24a3 3 0 1 1 0-6 3 3 0 0 1 0 6zm0-.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zm18 .5a3 3 0 1 1 0-6 3 3 0 0 1 0 6zm0-.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM6 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0zm9-9a3 3 0 1 1-6 0 3 3 0 0 1 6 0zm-3 2.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM6 3a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM3 5.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z" />
    </svg>
  );
}

// ---------------------------------------------------------------------------
// Clipboard
// ---------------------------------------------------------------------------

function useCopy() {
  const toast = useToast();
  return useCallback(
    (value: string, title: string, description?: string) =>
      navigator.clipboard.writeText(value).then(
        () => {
          toast.success(title, description);
          return true;
        },
        () => {
          toast.error("Copy failed", "Your browser blocked clipboard access.");
          return false;
        },
      ),
    [toast],
  );
}

function CopyIconButton({ value, title, label }: { value: string; title: string; label: string }) {
  const copy = useCopy();
  return (
    <button
      type="button"
      className={ICON_BUTTON}
      disabled={!value}
      aria-label={label}
      title={label}
      onClick={() => value && void copy(value, title, value.length < 120 ? value : undefined)}
    >
      <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
    </button>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

type Target = "local" | "public";

// Append the /v1 API path to a tunnel base URL so it mirrors the primary
// endpoint and can be used as a drop-in replacement.
function withApiPath(base: string): string {
  const trimmed = base.replace(/\/+$/, "");
  if (!trimmed) return "";
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

// The gateway origin, without the /v1 suffix. Catalogue paths are absolute.
function originOf(apiBase: string): string {
  return apiBase.replace(/\/+$/, "").replace(/\/v1$/, "");
}

export function EndpointsPage() {
  const access = useQuery({ queryKey: ["access-settings"], queryFn: () => api.accessSettings() });
  const status = useQuery({
    queryKey: ["tunnel-status"],
    queryFn: () => api.tunnelStatus(),
    refetchInterval: STATUS_POLL_SLOW,
  });

  const localUrl = access.data?.endpoint_url ?? "";

  const tunnel = status.data?.tunnel;
  const tunnelRunning = tunnel?.running ?? false;
  const tunnelBase = tunnel?.publicUrl || tunnel?.tunnelUrl || "";
  const tunnelUrl = tunnelRunning ? withApiPath(tunnelBase) : "";

  const [target, setTarget] = useState<Target>("local");
  const effectiveTarget: Target = target === "public" && tunnelUrl ? "public" : "local";
  const apiBase = effectiveTarget === "public" ? tunnelUrl : localUrl;

  // One target switch drives both the snippets and the copied reference URLs.
  const targetControl = tunnelUrl ? (
    <TargetRadios value={effectiveTarget} onChange={setTarget} />
  ) : null;

  return (
    <>
      <PageHeader
        title="Endpoints"
        description="One base URL and API key for every provider."
        action={
          <Link to="/keys" className={LINK_BUTTON}>
            <KeyRound strokeWidth={1.75} aria-hidden="true" />
            Manage keys
          </Link>
        }
      />
      <div className="space-y-5">
        <ConnectionCard localUrl={localUrl} loading={access.isLoading} tunnelUrl={tunnelUrl} />
        <QuickStartCard apiBase={apiBase} action={targetControl} />
        <CatalogueCard apiBase={apiBase} />
        <TunnelSection />
      </div>
    </>
  );
}

// TargetRadios switches snippets and reference URLs between the local and the
// public base URL. Radiogroup with roving focus and arrow-key selection.
function TargetRadios({ value, onChange }: { value: Target; onChange: (next: Target) => void }) {
  const options: { value: Target; label: string }[] = [
    { value: "local", label: "Local" },
    { value: "public", label: "Public" },
  ];
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const index = options.findIndex((option) => option.value === value);
    const next = event.key === "Home" ? 0
      : event.key === "End" ? options.length - 1
        : event.key === "ArrowRight" || event.key === "ArrowDown" ? (index + 1) % options.length
          : (index - 1 + options.length) % options.length;
    onChange(options[next].value);
    event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus();
  };
  return (
    <div role="radiogroup" aria-label="Base URL used in snippets and copied URLs" onKeyDown={onKeyDown} className="inline-flex rounded-xl border border-line bg-subtle p-0.5">
      {options.map((option) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(option.value)}
            className={`h-7 rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 ${
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg"
            }`}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Base URL
// ---------------------------------------------------------------------------

function ConnectionCard({
  localUrl,
  loading,
  tunnelUrl,
}: {
  localUrl: string;
  loading: boolean;
  tunnelUrl: string;
}) {
  return (
    <Card>
      <CardHeader title="Base URL" />
      <div className="divide-y divide-line">
        <EndpointRow label="Local" url={localUrl} loading={loading} primary />
        {tunnelUrl && (
          <EndpointRow
            label="Public · Cloudflare Tunnel"
            url={tunnelUrl}
            icon={<CloudflareLogo className="h-4 w-4 text-fg-faint" />}
          />
        )}
      </div>
      <p className="border-t border-line bg-subtle px-4 py-2.5 text-[12px] text-fg-muted sm:px-5">
        Authenticate with <code className="font-mono text-fg">Authorization: Bearer &lt;key&gt;</code> or{" "}
        <code className="font-mono text-fg">x-api-key</code>.
      </p>
    </Card>
  );
}

function EndpointRow({
  label,
  url,
  icon,
  loading,
  primary = false,
}: {
  label: string;
  url: string;
  icon?: ReactNode;
  loading?: boolean;
  primary?: boolean;
}) {
  const copy = useCopy();
  const [copied, setCopied] = useState(false);
  const labelId = useId();

  const onCopy = async () => {
    if (!url) return;
    if (await copy(url, "Endpoint copied", url)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    }
  };

  return (
    <div className="px-4 py-3.5 sm:px-5">
      <div className="flex items-center gap-2">
        {icon}
        <h3 id={labelId} className="text-[12px] font-medium text-fg-muted">{label}</h3>
      </div>
      <div className="mt-1.5 flex min-w-0 items-center gap-2 rounded-lg border border-line bg-subtle py-1 pl-3 pr-1" aria-busy={(loading && !url) || undefined}>
        {loading && !url ? (
          <Skeleton className="h-4 w-64 max-w-full flex-1" />
        ) : (
          <span className="min-w-0 flex-1 truncate font-mono text-[14px] text-fg" title={url}>
            {url || "Not available"}
          </span>
        )}
        <Button
          variant={primary ? "primary" : "secondary"}
          onClick={onCopy}
          disabled={!url}
          className="min-h-8 shrink-0"
          aria-label={`Copy ${label.toLowerCase()} base URL`}
        >
          {copied ? <Check strokeWidth={1.75} aria-hidden="true" /> : <Copy strokeWidth={1.75} aria-hidden="true" />}
          <span aria-hidden="true">{copied ? "Copied" : "Copy"}</span>
        </Button>
        <span className="sr-only" role="status">{copied ? "Copied" : ""}</span>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Quick start snippets
// ---------------------------------------------------------------------------

type SnippetId = "curl" | "python" | "node" | "claude" | "env";

const SNIPPET_TABS: { value: SnippetId; label: string }[] = [
  { value: "curl", label: "cURL" },
  { value: "python", label: "Python" },
  { value: "node", label: "Node.js" },
  { value: "claude", label: "Claude Code" },
  { value: "env", label: "Environment" },
];

function buildSnippet(id: SnippetId, apiBase: string): { code: string; note: string } {
  const base = apiBase || "http://localhost:PORT/v1";
  const origin = originOf(base);
  switch (id) {
    case "curl":
      return {
        note: "Use any model ID from GET /v1/models.",
        code: `curl ${base}/chat/completions \\
  -H "Authorization: Bearer $KEIROUTER_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "provider/model-id",
    "messages": [{"role": "user", "content": "Hello"}]
  }'`,
      };
    case "python":
      return {
        note: "Any OpenAI-compatible SDK takes the same base URL.",
        code: `import os
from openai import OpenAI

client = OpenAI(
    base_url="${base}",
    api_key=os.environ["KEIROUTER_API_KEY"],
)

resp = client.chat.completions.create(
    model="provider/model-id",
    messages=[{"role": "user", "content": "Hello"}],
)
print(resp.choices[0].message.content)`,
      };
    case "node":
      return {
        note: "Works in Node.js, Deno and Bun.",
        code: `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${base}",
  apiKey: process.env.KEIROUTER_API_KEY,
});

const resp = await client.chat.completions.create({
  model: "provider/model-id",
  messages: [{ role: "user", content: "Hello" }],
});
console.log(resp.choices[0].message.content);`,
      };
    case "claude":
      return {
        note: "Anthropic clients add /v1 themselves, so omit it.",
        code: `# ~/.claude/settings.json
{
  "env": {
    "ANTHROPIC_BASE_URL": "${origin}",
    "ANTHROPIC_AUTH_TOKEN": "<your KeiRouter key>"
  }
}`,
      };
    case "env":
      return {
        note: "OpenAI clients expect /v1; Anthropic clients don't.",
        code: `export KEIROUTER_API_KEY="<your KeiRouter key>"
export OPENAI_BASE_URL="${base}"
export OPENAI_API_KEY="$KEIROUTER_API_KEY"
export ANTHROPIC_BASE_URL="${origin}"
export ANTHROPIC_AUTH_TOKEN="$KEIROUTER_API_KEY"`,
      };
  }
}

function QuickStartCard({ apiBase, action }: { apiBase: string; action: ReactNode }) {
  const [tab, setTab] = useState<SnippetId>("curl");
  const snippet = useMemo(() => buildSnippet(tab, apiBase), [tab, apiBase]);
  const label = SNIPPET_TABS.find((t) => t.value === tab)?.label ?? tab;
  const uid = useId();
  const tabId = (id: SnippetId) => `${uid}-tab-${id}`;
  const panelId = `${uid}-panel`;

  // Arrow keys move between tabs (automatic activation), Home/End jump.
  const onTabKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const index = SNIPPET_TABS.findIndex((t) => t.value === tab);
    const next = event.key === "Home" ? 0
      : event.key === "End" ? SNIPPET_TABS.length - 1
        : (index + (event.key === "ArrowRight" ? 1 : -1) + SNIPPET_TABS.length) % SNIPPET_TABS.length;
    setTab(SNIPPET_TABS[next].value);
    document.getElementById(tabId(SNIPPET_TABS[next].value))?.focus();
  };

  return (
    <Card className="overflow-hidden">
      <CardHeader title="Quick start" action={action} />
      <div role="tablist" aria-label="Snippet language" onKeyDown={onTabKeyDown} className="flex gap-1 overflow-x-auto border-b border-line px-1 pb-px sm:px-2">
        {SNIPPET_TABS.map((t) => {
          const active = t.value === tab;
          return (
            <button
              key={t.value}
              id={tabId(t.value)}
              type="button"
              role="tab"
              aria-selected={active}
              aria-controls={panelId}
              tabIndex={active ? 0 : -1}
              onClick={() => setTab(t.value)}
              className={`relative whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500 ${
                active ? "text-fg" : "text-fg-muted hover:text-fg"
              }`}
            >
              {t.label}
              {active && <span className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-500" aria-hidden="true" />}
            </button>
          );
        })}
      </div>
      <div id={panelId} role="tabpanel" aria-labelledby={tabId(tab)} className="p-4 sm:p-5">
        <div className="relative rounded-lg border border-line bg-subtle">
          <div className="absolute right-1.5 top-1.5">
            <CopyIconButton value={snippet.code} title={`${label} snippet copied`} label={`Copy ${label} snippet`} />
          </div>
          <pre className="overflow-x-auto px-4 py-3 pr-11 font-mono text-[12.5px] leading-relaxed text-fg" tabIndex={0} aria-label={`${label} snippet`}>
            <code>{snippet.code}</code>
          </pre>
        </div>
        <div className="mt-2 flex flex-col gap-1 text-[12px] text-fg-muted sm:flex-row sm:items-center sm:justify-between">
          <p className="text-pretty">{snippet.note}</p>
          <Link
            to="/cli-tools"
            className="inline-flex min-h-6 shrink-0 items-center gap-1 rounded-md text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          >
            Set up other CLI tools
            <ArrowUpRight className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          </Link>
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Endpoint catalogue
// ---------------------------------------------------------------------------

type Format = "OpenAI" | "Anthropic" | "Gemini" | "KeiRouter";

interface CatalogueEntry {
  method: "GET" | "POST";
  path: string;
  purpose: string;
  format: Format;
}

const CATALOGUE: { group: string; note?: string; entries: CatalogueEntry[] }[] = [
  {
    group: "Text generation",
    entries: [
      { method: "POST", path: "/v1/chat/completions", purpose: "Chat completions", format: "OpenAI" },
      { method: "POST", path: "/v1/responses", purpose: "Responses API, used by Codex and Responses-native clients", format: "OpenAI" },
      { method: "POST", path: "/v1/messages", purpose: "Messages API, used by Claude Code", format: "Anthropic" },
      { method: "POST", path: "/v1/messages/count_tokens", purpose: "Count tokens before a turn, answered locally", format: "Anthropic" },
      { method: "POST", path: "/v1beta/models/{model}:generateContent", purpose: "Native generateContent for Google SDK clients", format: "Gemini" },
    ],
  },
  {
    group: "Media and tools",
    entries: [
      { method: "POST", path: "/v1/embeddings", purpose: "Text embeddings", format: "OpenAI" },
      { method: "POST", path: "/v1/images/generations", purpose: "Generate images from a prompt", format: "OpenAI" },
      { method: "POST", path: "/v1/images/understanding", purpose: "Describe or analyse an image", format: "KeiRouter" },
      { method: "POST", path: "/v1/videos/generations", purpose: "Start a video generation job", format: "KeiRouter" },
      { method: "GET", path: "/v1/videos/{id}", purpose: "Poll a video job for status and result", format: "KeiRouter" },
      { method: "POST", path: "/v1/audio/speech", purpose: "Text to speech", format: "OpenAI" },
      { method: "POST", path: "/v1/audio/transcriptions", purpose: "Speech to text", format: "OpenAI" },
      { method: "POST", path: "/v1/search", purpose: "Web search", format: "KeiRouter" },
      { method: "POST", path: "/v1/web/fetch", purpose: "Fetch a web page and extract its content", format: "KeiRouter" },
    ],
  },
  {
    group: "Models and usage",
    entries: [
      { method: "GET", path: "/v1/models", purpose: "List model IDs available to the calling key", format: "OpenAI" },
      { method: "GET", path: "/v1/models/info", purpose: "Metadata for one model, by ?id=provider/model", format: "KeiRouter" },
      { method: "GET", path: "/v1/models/{kind}", purpose: "List models of one kind, or routing chains with kind=chains", format: "KeiRouter" },
      { method: "GET", path: "/v1/keys/me/usage", purpose: "Token budget and spend for the calling key", format: "KeiRouter" },
    ],
  },
  {
    group: "Public",
    note: "No key required",
    entries: [
      { method: "GET", path: "/healthz", purpose: "Health check", format: "KeiRouter" },
      { method: "GET", path: "/v1", purpose: "Gateway name, version and endpoint list", format: "KeiRouter" },
    ],
  },
];

function CatalogueCard({ apiBase }: { apiBase: string }) {
  const origin = apiBase ? originOf(apiBase) : "";
  const total = CATALOGUE.reduce((n, g) => n + g.entries.length, 0);
  // Groups start collapsed except the first, so the page reads as a short list.
  const [openGroups, setOpenGroups] = useState<Set<string>>(() => new Set([CATALOGUE[0].group]));
  const uid = useId();
  const allOpen = openGroups.size === CATALOGUE.length;
  const toggleGroup = (group: string) =>
    setOpenGroups((current) => {
      const next = new Set(current);
      if (next.has(group)) next.delete(group);
      else next.add(group);
      return next;
    });

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3.5 sm:px-5">
        <h2 className="text-[14px] font-semibold tracking-[-0.005em]">
          API reference <span className="ml-1 text-[12px] font-normal tabular-nums text-fg-muted">{total} routes</span>
        </h2>
        <button
          type="button"
          onClick={() => setOpenGroups(allOpen ? new Set() : new Set(CATALOGUE.map((g) => g.group)))}
          className="min-h-6 shrink-0 rounded-md text-[12.5px] font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
        >
          {allOpen ? "Collapse all" : "Expand all"}
        </button>
      </div>
      <div className="divide-y divide-line">
        {CATALOGUE.map((g, gi) => {
          const expanded = openGroups.has(g.group);
          const regionId = `${uid}-group-${gi}`;
          return (
            <section key={g.group} aria-labelledby={`${regionId}-heading`}>
              <h3 id={`${regionId}-heading`} className="text-[13px]">
                <button
                  type="button"
                  aria-expanded={expanded}
                  aria-controls={regionId}
                  onClick={() => toggleGroup(g.group)}
                  className="flex w-full items-center gap-2 px-4 py-2.5 text-left transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500 sm:px-5"
                >
                  <ChevronRight
                    className={`h-4 w-4 shrink-0 text-fg-faint transition-transform ${expanded ? "rotate-90" : ""}`}
                    strokeWidth={1.75}
                    aria-hidden="true"
                  />
                  <span className="font-medium text-fg">{g.group}</span>
                  <span className="text-[12px] tabular-nums text-fg-muted">{g.entries.length}</span>
                  {g.note && <span className="ml-auto text-[12px] text-fg-muted">{g.note}</span>}
                </button>
              </h3>
              {expanded && (
                <ul id={regionId} className="divide-y divide-line border-t border-line">
                  {g.entries.map((e) => (
                    <li key={`${e.method} ${e.path}`} className="flex items-center gap-3 px-4 py-2 sm:pl-11 sm:pr-5">
                      <span className="w-10 shrink-0 font-mono text-[12px] font-medium text-fg-muted">{e.method}</span>
                      <span className="min-w-0 flex-1">
                        <span className="block break-all font-mono text-[12.5px] text-fg">{e.path}</span>
                        <span className="block text-[12px] text-fg-muted">{e.purpose}</span>
                      </span>
                      <span className="hidden sm:inline-flex">
                        <Badge tone="neutral">{e.format}</Badge>
                      </span>
                      <CopyIconButton
                        value={origin ? `${origin}${e.path}` : ""}
                        title="URL copied"
                        label={`Copy URL for ${e.method} ${e.path}`}
                      />
                    </li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Tunnel section — single card with list
// ---------------------------------------------------------------------------

function TunnelSection() {
  return (
    <Card>
      <CardHeader title="Tunnels" description="Optional access from outside this machine" />
      <div className="divide-y divide-line">
        <CloudflareTunnel />
        {/* Tailscale — temporarily disabled, under active development */}
        <TunnelRow
          name="Tailscale"
          description="Private network with HTTPS"
          logo={<TailscaleLogo className="h-4 w-4 text-fg-faint" />}
          loading={false}
          isRunning={false}
          displayUrl=""
          reachable={null}
          actionsDisabled
          onEnable={() => {}}
          onDisable={() => {}}
          enablePending={false}
          disablePending={false}
        />
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Cloudflare tunnel row
// ---------------------------------------------------------------------------

function CloudflareTunnel() {
  const qc = useQueryClient();
  const [loading, setLoading] = useState(false);
  const [reachable, setReachable] = useState<boolean | null>(null);
  const missRef = useRef(0);
  const pingTimerRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  const status = useQuery({
    queryKey: ["tunnel-status"],
    queryFn: () => api.tunnelStatus(),
    refetchInterval: STATUS_POLL_SLOW,
  });

  const tunnel = status.data?.tunnel;
  const download = status.data?.download;
  const tunnelUrl = tunnel?.tunnelUrl || "";
  const publicUrl = tunnel?.publicUrl || "";
  const isRunning = tunnel?.running ?? false;
  const displayUrl = publicUrl || tunnelUrl;

  const pingTunnel = useCallback(async () => {
    const url = publicUrl || tunnelUrl;
    if (!url) return;
    try {
      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(), PING_TIMEOUT);
      const res = await fetch(`${url}/healthz`, { mode: "cors", signal: ctrl.signal });
      clearTimeout(timer);
      if (res.ok) {
        setReachable(true);
        missRef.current = 0;
      } else {
        missRef.current++;
        if (missRef.current >= REACHABLE_MISS_THRESHOLD) setReachable(false);
      }
    } catch {
      missRef.current++;
      if (missRef.current >= REACHABLE_MISS_THRESHOLD) setReachable(false);
    }
  }, [publicUrl, tunnelUrl]);

  useEffect(() => {
    if (isRunning && (publicUrl || tunnelUrl)) {
      pingTunnel();
      pingTimerRef.current = setInterval(pingTunnel, PING_INTERVAL);
      const stopAt = Date.now() + PING_MAX_MS;
      const check = setInterval(() => {
        if (Date.now() > stopAt) {
          clearInterval(pingTimerRef.current);
          clearInterval(check);
        }
      }, 10000);
      return () => {
        clearInterval(pingTimerRef.current);
        clearInterval(check);
      };
    } else {
      setReachable(null);
      missRef.current = 0;
    }
  }, [isRunning, publicUrl, tunnelUrl, pingTunnel]);

  useEffect(() => {
    if (loading) {
      pollTimerRef.current = setInterval(
        () => qc.invalidateQueries({ queryKey: ["tunnel-status"] }),
        STATUS_POLL_FAST,
      );
      return () => clearInterval(pollTimerRef.current);
    }
  }, [loading, qc]);

  const enable = useMutation({
    mutationFn: () => api.tunnelEnable(),
    onMutate: () => setLoading(true),
    onSuccess: () => {
      setLoading(false);
      qc.invalidateQueries({ queryKey: ["tunnel-status"] });
      qc.invalidateQueries({ queryKey: ["access-settings"] });
    },
    onError: () => setLoading(false),
  });

  const disable = useMutation({
    mutationFn: () => api.tunnelDisable(),
    onSuccess: () => {
      setReachable(null);
      qc.invalidateQueries({ queryKey: ["tunnel-status"] });
      qc.invalidateQueries({ queryKey: ["access-settings"] });
    },
  });

  return (
    <TunnelRow
      name="Cloudflare Tunnel"
      description="Quick tunnel — no account needed"
      logo={<CloudflareLogo className="h-4 w-4 text-fg-faint" />}
      isRunning={isRunning}
      reachable={reachable}
      loading={loading}
      displayUrl={displayUrl}
      subText={download?.downloading ? `Downloading cloudflared… ${download.progress}%` : undefined}
      onEnable={() => enable.mutate()}
      onDisable={() => disable.mutate()}
      enablePending={enable.isPending}
      disablePending={disable.isPending}
    />
  );
}

// ---------------------------------------------------------------------------
// Tailscale tunnel row
// ---------------------------------------------------------------------------

export function TailscaleTunnel() {
  const qc = useQueryClient();
  const [loading, setLoading] = useState(false);
  const [reachable, setReachable] = useState<boolean | null>(null);
  const [sudoPassword, setSudoPassword] = useState("");
  const [installLog, setInstallLog] = useState<string[]>([]);
  const [installing, setInstalling] = useState(false);
  const [authUrl, setAuthUrl] = useState<string | null>(null);
  const [showInstall, setShowInstall] = useState(false);
  const missRef = useRef(0);
  const pingTimerRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  const status = useQuery({
    queryKey: ["tunnel-status"],
    queryFn: () => api.tunnelStatus(),
    refetchInterval: STATUS_POLL_SLOW,
  });

  const tsCheck = useQuery({
    queryKey: ["tailscale-check"],
    queryFn: () => api.tailscaleCheck(),
    refetchInterval: STATUS_POLL_SLOW,
  });

  const ts = status.data?.tailscale;
  const isRunning = ts?.running ?? false;
  const isLoggedIn = ts?.loggedIn ?? false;
  const isInstalled = tsCheck.data?.installed ?? false;
  const tunnelUrl = ts?.tunnelUrl || "";

  const pingTailscale = useCallback(async () => {
    if (!tunnelUrl) return;
    try {
      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(), PING_TIMEOUT);
      const res = await fetch(`${tunnelUrl}/healthz`, { mode: "cors", signal: ctrl.signal });
      clearTimeout(timer);
      if (res.ok) {
        setReachable(true);
        missRef.current = 0;
      } else {
        missRef.current++;
        if (missRef.current >= REACHABLE_MISS_THRESHOLD) setReachable(false);
      }
    } catch {
      missRef.current++;
      if (missRef.current >= REACHABLE_MISS_THRESHOLD) setReachable(false);
    }
  }, [tunnelUrl]);

  useEffect(() => {
    if (isRunning && tunnelUrl) {
      pingTailscale();
      pingTimerRef.current = setInterval(pingTailscale, PING_INTERVAL);
      return () => clearInterval(pingTimerRef.current);
    } else {
      setReachable(null);
    }
  }, [isRunning, tunnelUrl, pingTailscale]);

  const handleInstall = async () => {
    setInstalling(true);
    setInstallLog([]);
    try {
      const res = await fetch("/api/tunnel/tailscale-install", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sudoPassword }),
      });
      if (!res.ok || !res.body) {
        setInstallLog((prev) => [...prev, "Failed to start install"]);
        setInstalling(false);
        return;
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";
        for (const line of lines) {
          if (line.startsWith("event: ")) {
            const event = line.slice(7);
            const dataLine = lines[lines.indexOf(line) + 1];
            if (dataLine?.startsWith("data: ")) {
              try {
                const data = JSON.parse(dataLine.slice(6));
                if (event === "progress") {
                  setInstallLog((prev) => [...prev, data.message]);
                } else if (event === "done") {
                  setInstallLog((prev) => [...prev, "Installation complete!"]);
                  setInstalling(false);
                  qc.invalidateQueries({ queryKey: ["tailscale-check"] });
                } else if (event === "error") {
                  setInstallLog((prev) => [...prev, `Error: ${data.error}`]);
                  setInstalling(false);
                }
              } catch { /* ignore parse errors */ }
            }
          }
        }
      }
    } catch (e) {
      setInstallLog((prev) => [...prev, `Error: ${(e as Error).message}`]);
      setInstalling(false);
    }
  };

  const enable = useMutation({
    mutationFn: () => api.tailscaleEnable(sudoPassword || undefined),
    onMutate: () => setLoading(true),
    onSuccess: (data: TailscaleEnableResult) => {
      setLoading(false);
      if (data.needsLogin && data.authUrl) {
        setAuthUrl(data.authUrl);
        window.open(data.authUrl, "_blank", "width=600,height=700");
      } else if (data.funnelNotEnabled && data.enableUrl) {
        window.open(data.enableUrl, "_blank", "width=600,height=700");
      } else if (data.success) {
        setAuthUrl(null);
        qc.invalidateQueries({ queryKey: ["tunnel-status"] });
        qc.invalidateQueries({ queryKey: ["access-settings"] });
      }
    },
    onError: () => setLoading(false),
  });

  const disable = useMutation({
    mutationFn: () => api.tailscaleDisable(),
    onSuccess: () => {
      setReachable(null);
      qc.invalidateQueries({ queryKey: ["tunnel-status"] });
      qc.invalidateQueries({ queryKey: ["access-settings"] });
    },
  });

  return (
    <TunnelRow
      name="Tailscale"
      description="Private network with HTTPS"
      logo={<TailscaleLogo className="h-4 w-4 text-fg-faint" />}
      isRunning={isRunning}
      reachable={reachable}
      loading={loading}
      displayUrl={tunnelUrl}
      onEnable={() => {
        if (!isInstalled) setShowInstall(true);
        else enable.mutate();
      }}
      onDisable={() => disable.mutate()}
      enablePending={enable.isPending}
      disablePending={disable.isPending}
    >
      {!isInstalled && showInstall && (
        <div className="max-w-md space-y-3">
          <Field label="Sudo password (required to install)">
            <Input
              type="password"
              value={sudoPassword}
              onChange={(e) => setSudoPassword(e.target.value)}
              placeholder="Required for system install"
            />
          </Field>
          <div className="flex gap-2">
            <Button onClick={handleInstall} disabled={installing || !sudoPassword.trim()}>
              {installing ? <><Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />Installing…</> : "Install"}
            </Button>
            <Button variant="ghost" onClick={() => setShowInstall(false)}>
              Cancel
            </Button>
          </div>
          {installLog.length > 0 && (
            <div role="log" aria-live="polite" aria-label="Install log" className="max-h-36 overflow-y-auto rounded-lg border border-line bg-subtle px-3 py-2 font-mono text-[12px] leading-relaxed text-fg-muted">
              {installLog.map((line, i) => (
                <div key={i}>{line}</div>
              ))}
            </div>
          )}
        </div>
      )}

      {isInstalled && authUrl && (
        <p className="rounded-lg border border-line bg-subtle px-3 py-2 text-[12px] text-fg-muted">
          Login required —{" "}
          <a
            href={authUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-link hover:underline"
          >
            authenticate here
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        </p>
      )}

      {isInstalled && !isRunning && isLoggedIn && (
        <div className="max-w-sm">
          <Field label="Sudo password (optional, for TUN mode)">
            <Input
              type="password"
              value={sudoPassword}
              onChange={(e) => setSudoPassword(e.target.value)}
              placeholder="Leave empty for userspace"
            />
          </Field>
        </div>
      )}

      {isInstalled && !isLoggedIn && (
        <p className="text-[12px] text-fg-muted">Log in to your Tailscale account to enable the funnel.</p>
      )}
    </TunnelRow>
  );
}

// ---------------------------------------------------------------------------
// Shared tunnel row component
// ---------------------------------------------------------------------------

function TunnelRow({
  name,
  description,
  logo,
  isRunning,
  reachable,
  loading,
  displayUrl,
  subText,
  onEnable,
  onDisable,
  enablePending,
  disablePending,
  actionsDisabled,
  children,
}: {
  name: string;
  description: string;
  logo: ReactNode;
  isRunning: boolean;
  reachable: boolean | null;
  loading: boolean;
  displayUrl?: string;
  subText?: string;
  onEnable: () => void;
  onDisable: () => void;
  enablePending: boolean;
  disablePending: boolean;
  actionsDisabled?: boolean;
  children?: ReactNode;
}) {
  return (
    <div className="px-4 py-3.5 sm:px-5">
      <div className="flex items-center gap-3">
        <span className="shrink-0">{logo}</span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-[13px] font-medium text-fg">{name}</h3>
            {isRunning && <TunnelBadge reachable={reachable} />}
            {actionsDisabled && <Badge tone="neutral">Coming soon</Badge>}
          </div>
          <p className="mt-0.5 text-[12px] text-fg-muted">{description}</p>
          {isRunning && displayUrl && (
            <div className="mt-1 flex min-w-0 items-center gap-1.5">
              <TunnelDot running={isRunning} reachable={reachable} />
              <a
                href={displayUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="flex min-h-6 min-w-0 items-center gap-1 rounded-md font-mono text-[12px] text-fg-muted transition-colors hover:text-fg hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                <span className="truncate">{displayUrl}</span>
                <ArrowUpRight className="h-3 w-3 shrink-0" strokeWidth={1.75} aria-hidden="true" />
                <span className="sr-only"> (opens in a new tab)</span>
              </a>
            </div>
          )}
          {subText && <p className="mt-1 text-[12px] tabular-nums text-fg-muted" role="status">{subText}</p>}
        </div>
        <div className="flex shrink-0 items-center pl-2">
          {isRunning ? (
            <Button
              variant="danger"
              onClick={onDisable}
              disabled={actionsDisabled || disablePending}
              aria-label={disablePending ? `Disabling ${name}` : `Disable ${name}`}
            >
              {disablePending ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : "Disable"}
            </Button>
          ) : (
            <Button
              variant="secondary"
              onClick={onEnable}
              disabled={actionsDisabled || loading || enablePending}
              aria-label={loading ? `Enabling ${name}` : `Enable ${name}`}
            >
              {loading ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : "Enable"}
            </Button>
          )}
        </div>
      </div>
      {children && <div className="mt-3 border-t border-line pt-3 sm:ml-7">{children}</div>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared tunnel UI atoms
// ---------------------------------------------------------------------------

function TunnelDot({ running, reachable }: { running: boolean; reachable: boolean | null }) {
  const color = running
    ? reachable === true
      ? "bg-ok"
      : reachable === false
        ? "bg-bad"
        : "bg-warn animate-pulse"
    : "bg-fg-faint";
  // Decorative: the adjacent badge carries the status in text.
  return <span className={`block h-1.5 w-1.5 shrink-0 rounded-full ${color}`} aria-hidden="true" />;
}

function TunnelBadge({ reachable }: { reachable: boolean | null }) {
  const tone = reachable === true ? "success" : reachable === false ? "danger" : "neutral";
  const label = reachable === true ? "Reachable" : reachable === false ? "Unreachable" : "Checking…";
  return (
    <span role="status">
      <Badge tone={tone}>{label}</Badge>
    </span>
  );
}
