import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ChevronRight, Plus, Search, X } from "lucide-react";
import { api, type Account, type HealthTimelineProvider, type Provider } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { Badge, Button, ErrorBanner, Field, Input, Modal, Select, Skeleton } from "../components/ui";
import { useToast } from "../components/Toast";


// Popularity ranking for default sort order (lower = more popular).
const POPULARITY: Record<string, number> = {
  // Tier 1 — mega platforms
  openai: 1,
  anthropic: 2,
  claude: 3,
  gemini: 4,
  deepseek: 5,
  // Tier 2 — major LLM providers
  xai: 10,
  mistral: 11,
  groq: 12,
  cohere: 13,
  perplexity: 14,
  together: 15,
  fireworks: 16,
  openrouter: 17,
  // Tier 3 — popular regional / coding
  qwen: 20,
  kimi: 21,
  glm: 22,
  minimax: 23,
  "volcengine-ark": 24,
  deepinfra: 25,
  cerebras: 26,
  sambanova: 27,
  nvidia: 28,
  "xiaomi-mimo": 29,
  "mimo-free": 30,
  "xiaomi-tokenplan": 31,
  // Tier 4 — coding tools / wrappers
  cursor: 30,
  codex: 31,
  github: 32,
  cline: 33,
  kiro: 34,
  "gemini-cli": 35,
  commandcode: 36,
  "kimi-coding": 37,
  qoder: 38,
  // Tier 5 — cloud platforms
  azure: 40,
  vertex: 41,
  "vertex-partner": 42,
  "cloudflare-ai": 43,
  "aws-polly": 44,
  "vercel-ai-gateway": 45,
  // Tier 6 — self-hosted / local
  ollama: 50,
  "ollama-local": 51,
  // Tier 7 — media / speech
  elevenlabs: 60,
  deepgram: 61,
  assemblyai: 62,
  cartesia: 63,
  "stability-ai": 64,
  "black-forest-labs": 65,
  runwayml: 66,
  // Tier 8 — search / embeddings / utility
  "voyage-ai": 70,
  "jina-ai": 71,
  "jina-reader": 72,
  tavily: 73,
  "brave-search": 74,
  exa: 75,
  serper: 76,
  firecrawl: 77,
  // Tier 9 — niche / smaller
  huggingface: 80,
  siliconflow: 81,
  hyperbolic: 82,
  nebius: 83,
  ai21: 84,
  reka: 85,
  baseten: 86,
  modal: 87,
  lepton: 88,
};
const DEFAULT_RANK = 999;

function sortByPopularity<T extends { id: string; pinned?: boolean }>(list: T[]): T[] {
  return [...list].sort((a, b) => {
    // Pinned providers always come first.
    if (a.pinned && !b.pinned) return -1;
    if (!a.pinned && b.pinned) return 1;
    const ra = POPULARITY[a.id] ?? DEFAULT_RANK;
    const rb = POPULARITY[b.id] ?? DEFAULT_RANK;
    return ra - rb;
  });
}

// kindFilters narrow the page by what a provider can serve.
const kindFilters = [
  { id: "all", label: "All" },
  { id: "llm", label: "Chat" },
  { id: "embedding", label: "Embeddings" },
  { id: "image", label: "Image" },
  { id: "stt", label: "Speech-to-text" },
  { id: "tts", label: "Text-to-speech" },
  { id: "search", label: "Search" },
  { id: "fetch", label: "Fetch" },
];

type CatalogGroup = "custom" | "subscription" | "api" | "free" | "media" | "retrieval";

// Catalog groups follow how people actually connect: their own endpoint, a
// subscription they sign in to, a paid API key, something free/local, or a
// non-chat capability.
const GROUPS: { id: CatalogGroup; title: string; hint: string }[] = [
  { id: "custom", title: "Custom endpoints", hint: "Any OpenAI- or Anthropic-compatible server, isolated per instance." },
  { id: "subscription", title: "Sign in with a plan", hint: "Use an existing subscription through OAuth or device login — no API key." },
  { id: "api", title: "Model APIs", hint: "Pay-as-you-go providers authenticated with an API key." },
  { id: "free", title: "Free & local", hint: "No credentials needed: local runtimes and free tiers." },
  { id: "media", title: "Media & speech", hint: "Image, video, speech-to-text and text-to-speech." },
  { id: "retrieval", title: "Search, fetch & embeddings", hint: "Web search, page fetching and vector embeddings." },
];

function groupOf(p: Provider): CatalogGroup {
  if (p.custom || p.id.startsWith("custom-")) return "custom";
  if (p.auth_kind === "none") return "free";
  if (p.auth_kind === "oauth") return "subscription";
  if (p.service_kinds.includes("llm")) return "api";
  if (p.service_kinds.some((k) => k === "image" || k === "tts" || k === "stt" || k === "video")) return "media";
  return "retrieval";
}

export function ProvidersPage() {
  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers() });
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts() });
  const timeline = useQuery({
    queryKey: ["health-timeline", "24h"],
    queryFn: () => api.healthTimeline("24h", 24),
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
  const [filter, setFilter] = useState("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [customOpen, setCustomOpen] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);

  // "/" focuses search, the convention in most developer dashboards.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      e.preventDefault();
      searchRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const accountsByProvider = useMemo(() => {
    const map = new Map<string, Account[]>();
    for (const a of accounts.data?.accounts ?? []) {
      const list = map.get(a.provider) ?? [];
      list.push(a);
      map.set(a.provider, list);
    }
    return map;
  }, [accounts.data]);

  const healthByProvider = useMemo(
    () => new Map((timeline.data?.providers ?? []).map((h) => [h.provider, h])),
    [timeline.data],
  );

  const visible = useMemo(() => {
    const all = providers.data?.providers ?? [];
    const q = searchQuery.trim().toLowerCase();
    return all
      .filter((p) => !p.hidden)
      .filter((p) => filter === "all" || p.service_kinds.includes(filter))
      .filter((p) => !q || p.display_name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q) || p.alias.toLowerCase().includes(q));
  }, [providers.data, filter, searchQuery]);

  const connected = sortByPopularity(visible.filter((p) => accountsByProvider.has(p.id)));
  const available = sortByPopularity(visible.filter((p) => !accountsByProvider.has(p.id)));
  const grouped = GROUPS.map((g) => ({ ...g, items: available.filter((p) => groupOf(p) === g.id) })).filter((g) => g.items.length > 0);
  const totalVisible = (providers.data?.providers ?? []).filter((p) => !p.hidden).length;

  return (
    <>
      <PageHeader
        title="Providers"
        description={
          providers.data
            ? `${accountsByProvider.size} connected · ${totalVisible - accountsByProvider.size} more available. Each provider can hold several accounts; KeiRouter rotates and fails over between them.`
            : "Connect the upstreams KeiRouter routes to."
        }
        action={
          <Button variant="secondary" onClick={() => setCustomOpen(true)}>
            <Plus />
            New custom provider
          </Button>
        }
      />

      <div className="mb-5 flex flex-col gap-3 lg:flex-row lg:items-center">
        <div className="relative lg:w-80">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
          <input
            ref={searchRef}
            type="search"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search 90+ providers"
            aria-label="Search providers"
            className="h-9 w-full rounded-lg border border-line bg-surface pl-9 pr-16 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
          />
          {searchQuery ? (
            <button
              type="button"
              onClick={() => setSearchQuery("")}
              className="absolute right-2 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg"
              aria-label="Clear search"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          ) : (
            <kbd className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 rounded border border-line bg-subtle px-1.5 font-mono text-[10.5px] text-fg-faint">/</kbd>
          )}
        </div>
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Filter by capability">
          {kindFilters.map((k) => {
            const active = filter === k.id;
            return (
              <button
                key={k.id}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setFilter(k.id)}
                className={cn(
                  "h-8 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                  active ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                )}
              >
                {k.label}
              </button>
            );
          })}
        </div>
      </div>

      {providers.isLoading ? (
        <div className="space-y-5">
          <Skeleton className="h-48 w-full rounded-2xl" />
          <Skeleton className="h-72 w-full rounded-2xl" />
        </div>
      ) : providers.isError ? (
        <ErrorBanner message="Couldn't load providers. Is the backend running?" />
      ) : (
        <div className="space-y-8">
          <section aria-labelledby="connected-heading">
            <div className="mb-2.5 flex items-baseline justify-between">
              <h2 id="connected-heading" className="text-[14px] font-semibold text-fg">Connected</h2>
              <span className="text-[12px] tabular-nums text-fg-faint">{connected.length}</span>
            </div>
            {connected.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-10 text-center">
                <p className="text-[13px] font-medium text-fg">{searchQuery || filter !== "all" ? "No connected provider matches" : "No providers connected yet"}</p>
                <p className="mt-1 text-[12.5px] text-fg-muted">
                  {searchQuery || filter !== "all" ? "Clear the search or filter to see all of them." : "Pick one below — most take an API key, some let you sign in with an existing plan."}
                </p>
              </div>
            ) : (
              <ConnectedTable providers={connected} accountsByProvider={accountsByProvider} healthByProvider={healthByProvider} />
            )}
          </section>

          {grouped.length === 0 ? (
            <div className="rounded-2xl border border-line bg-surface px-6 py-10 text-center text-[13px] text-fg-muted">
              No other providers match{searchQuery ? ` “${searchQuery}”` : " this capability"}.
            </div>
          ) : (
            grouped.map((g) => (
              <section key={g.id} aria-labelledby={`group-${g.id}`}>
                <div className="mb-2.5 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5">
                  <div className="flex items-baseline gap-2.5">
                    <h2 id={`group-${g.id}`} className="text-[14px] font-semibold text-fg">{g.title}</h2>
                    <span className="text-[12px] tabular-nums text-fg-faint">{g.items.length}</span>
                  </div>
                  <p className="text-[12.5px] text-fg-muted">{g.hint}</p>
                </div>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
                  {g.items.map((p) => (
                    <CatalogCard key={p.id} provider={p} />
                  ))}
                </div>
              </section>
            ))
          )}
        </div>
      )}

      <CreateCustomProviderModal open={customOpen} onClose={() => setCustomOpen(false)} />
    </>
  );
}

function ConnectedTable({
  providers,
  accountsByProvider,
  healthByProvider,
}: {
  providers: Provider[];
  accountsByProvider: Map<string, Account[]>;
  healthByProvider: Map<string, HealthTimelineProvider>;
}) {
  const navigate = useNavigate();
  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] text-[13px]">
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th className="px-4 py-2 font-medium">Provider</th>
              <th className="px-4 py-2 font-medium">Accounts</th>
              <th className="px-4 py-2 font-medium">Last 24 hours</th>
              <th className="px-4 py-2 text-right font-medium">Requests</th>
              <th className="px-4 py-2 text-right font-medium">Success</th>
              <th className="px-4 py-2 text-right font-medium">Worst p95</th>
              <th className="w-8 px-2 py-2" aria-hidden="true" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {providers.map((p) => {
              const accs = accountsByProvider.get(p.id) ?? [];
              const attention = accs.filter((a) => a.needs_reconnect).length;
              const paused = accs.filter((a) => a.disabled).length;
              const h = healthByProvider.get(p.id);
              return (
                <tr key={p.id} className="cursor-pointer transition-colors hover:bg-hover" onClick={() => navigate(`/providers/${p.id}`)}>
                  <td className="px-4 py-2.5">
                    <Link to={`/providers/${p.id}`} className="flex items-center gap-2.5 focus:outline-none focus-visible:underline" onClick={(e) => e.stopPropagation()}>
                      <ProviderLogo icon={p.icon} name={p.display_name} size={24} />
                      <span className="min-w-0">
                        <span className="block truncate font-medium text-fg">{p.display_name}</span>
                        <span className="block truncate font-mono text-[11.5px] text-fg-faint">{p.id}</span>
                      </span>
                    </Link>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5">
                    <span className="tabular-nums text-fg">{accs.length}</span>
                    {attention > 0 && <span className="ml-2"><Badge tone="warning">{attention} need{attention === 1 ? "s" : ""} reconnect</Badge></span>}
                    {attention === 0 && paused > 0 && <span className="ml-1.5 text-[12px] text-fg-faint">· {paused} paused</span>}
                  </td>
                  <td className="px-4 py-2.5">
                    {h ? (
                      <div className="flex w-44 gap-[2px]" role="img" aria-label={`${p.display_name} hourly status, last 24 hours`}>
                        {h.buckets.map((b) => (
                          <span key={b.start} className={cn("h-3.5 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")} />
                        ))}
                      </div>
                    ) : (
                      <span className="text-[12px] text-fg-faint">No traffic</span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 text-right text-fg">{h ? h.requests.toLocaleString("en-US") : "—"}</td>
                  <td className={cn("px-4 py-2.5 text-right", h && h.requests ? (h.success_rate >= 0.99 ? "text-fg" : h.success_rate >= 0.95 ? "text-warn" : "text-bad") : "text-fg-faint")}>
                    {h && h.requests ? `${(h.success_rate * 100).toFixed(1)}%` : "—"}
                  </td>
                  <td className="px-4 py-2.5 text-right text-fg-muted">{h && h.worst_p95_ms ? fmtLatency(h.worst_p95_ms) : "—"}</td>
                  <td className="px-2 py-2.5 text-fg-faint" aria-hidden="true">
                    <ChevronRight className="h-4 w-4" />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

const TICK_CLASS: Record<string, string> = { ok: "bg-ok/70", degraded: "bg-warn", down: "bg-bad", idle: "bg-track" };

function fmtLatency(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(2)} s`;
}

function CatalogCard({ provider: p }: { provider: Provider }) {
  const tag = p.deprecated
    ? { label: "Unofficial", tone: "warning" as const, title: p.notice || "Uses an unofficial client API; the account may be restricted." }
    : !p.drivable
      ? { label: "Coming soon", tone: "neutral" as const, title: "Listed for discovery; routing is not available yet." }
      : p.auth_kind === "none"
        ? { label: "Free", tone: "success" as const, title: "No credentials required." }
        : null;
  return (
    <Link
      to={`/providers/${p.id}`}
      aria-label={`Connect ${p.display_name}`}
      className="group flex items-center gap-3 rounded-2xl border border-line bg-surface px-3 py-2.5 transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
    >
      <ProviderLogo icon={p.icon} name={p.display_name} size={28} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-fg">{p.display_name}</span>
        <span className="block truncate font-mono text-[11.5px] text-fg-faint">{p.id}</span>
      </span>
      {tag && (
        <span title={tag.title}>
          <Badge tone={tag.tone}>
            {p.deprecated && <AlertTriangle className="h-3 w-3" />}
            {tag.label}
          </Badge>
        </span>
      )}
      <span className="hidden text-[12px] font-medium text-accent-500 group-hover:inline dark:text-accent-400">Connect</span>
    </Link>
  );
}

// CreateCustomProviderModal creates a new dynamic custom provider instance.
// Each instance gets a unique id so multiple OpenAI-/Anthropic-compatible
// endpoints stay fully isolated (own base URL, accounts, and models).
function CreateCustomProviderModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [name, setName] = useState("");
  const [dialect, setDialect] = useState("openai");
  const [baseURL, setBaseURL] = useState("");
  const [alias, setAlias] = useState("");
  const [error, setError] = useState("");

  const reset = () => {
    setName("");
    setDialect("openai");
    setBaseURL("");
    setAlias("");
    setError("");
  };

  const create = useMutation({
    mutationFn: () =>
      api.createCustomProvider({
        display_name: name.trim(),
        dialect,
        base_url: baseURL.trim(),
        alias: alias.trim() ? alias.trim() : undefined,
      }),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["providers"] });
      toast.success("Custom provider created", "Add an account and models to start routing.");
      reset();
      onClose();
      navigate(`/providers/${p.id}`);
    },
    onError: (e: Error) => setError(e.message),
  });

  const canSubmit = name.trim().length > 0 && baseURL.trim().length > 0 && !create.isPending;

  return (
    <Modal
      open={open}
      onClose={() => { reset(); onClose(); }}
      title="New custom provider"
      subtitle="A dedicated instance of an OpenAI- or Anthropic-compatible endpoint. Each instance is isolated with its own base URL, accounts, and models."
    >
      <form
        className="space-y-4 px-5 py-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSubmit) create.mutate();
        }}
      >
        <Field label="Name (required)">
          <Input
            value={name}
            onChange={(e) => { setName(e.target.value); setError(""); }}
            placeholder="e.g. Local vLLM or Acme Gateway"
            autoFocus
          />
        </Field>
        <Field label="Dialect">
          <Select value={dialect} onChange={(e) => setDialect(e.target.value)}>
            <option value="openai">OpenAI-compatible</option>
            <option value="anthropic">Anthropic-compatible</option>
          </Select>
        </Field>
        <Field label="Base URL (required)">
          <Input
            value={baseURL}
            onChange={(e) => { setBaseURL(e.target.value); setError(""); }}
            placeholder="https://llm.example.com/v1"
          />
        </Field>
        <Field label="Alias / prefix (optional)">
          <Input
            value={alias}
            onChange={(e) => {
              const slug = e.target.value
                .toLowerCase()
                .replace(/\s+/g, "-")
                .replace(/[^a-z0-9-]/g, "")
                .replace(/-+/g, "-");
              setAlias(slug);
              setError("");
            }}
            placeholder="e.g. kei-ai — models route as <alias>/<model>"
            pattern="[A-Za-z0-9-]*"
            maxLength={32}
          />
          <p className="text-xs text-[var(--text-muted)]">
            Letters, digits, hyphens only (max 32). Leave blank to derive from the name. Models route as <code>&lt;alias&gt;/&lt;model&gt;</code>.
          </p>
        </Field>
        <p className="text-xs text-[var(--text-muted)]">
          Tip: add two separate instances for two endpoints of the same type — they will never share models or credentials.
        </p>
        {error && <ErrorBanner message={error} />}
        <div className="flex items-center justify-end gap-2 pt-1">
          <Button type="button" variant="ghost" onClick={() => { reset(); onClose(); }}>
            Cancel
          </Button>
          <Button type="submit" disabled={!canSubmit}>
            <Plus className="h-4 w-4" />
            {create.isPending ? "Creating…" : "Create provider"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
