import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, FileSearch, Gift, LogIn, Plug, Plus, Search, SearchX, X, type LucideIcon } from "lucide-react";
import { api, type Account, type HealthTimelineProvider, type Provider } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { Badge, Button, ErrorBanner, IconTile, SectionTitle, Select, Skeleton } from "../components/ui";
import { ICONS } from "../lib/icons";
import { useToast } from "../components/Toast";
import { ConnectDialog, FormError, PrimaryAction, SecondaryAction, SelectField, TextField } from "../components/connect/ConnectKit";


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
const GROUPS: { id: CatalogGroup; title: string; icon: LucideIcon }[] = [
  { id: "custom", title: "Custom endpoints", icon: ICONS.server },
  { id: "subscription", title: "Sign in with a plan", icon: LogIn },
  { id: "api", title: "Model APIs", icon: ICONS.model },
  { id: "free", title: "Free and local", icon: Gift },
  { id: "media", title: "Media and speech", icon: ICONS.media },
  { id: "retrieval", title: "Search, fetch and embeddings", icon: FileSearch },
];

function groupOf(p: Provider): CatalogGroup {
  if (p.custom || p.id.startsWith("custom-")) return "custom";
  if (p.auth_kind === "none") return "free";
  if (p.auth_kind === "oauth") return "subscription";
  if (p.service_kinds.includes("llm")) return "api";
  if (p.service_kinds.some((k) => k === "image" || k === "tts" || k === "stt" || k === "video")) return "media";
  return "retrieval";
}

function matches(p: Provider, filter: string, q: string) {
  if (filter !== "all" && !p.service_kinds.includes(filter)) return false;
  return !q || p.display_name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q) || p.alias.toLowerCase().includes(q);
}

// onRadioKeys gives a role="radiogroup" the arrow-key behaviour of native radios.
function onRadioKeys(e: ReactKeyboardEvent<HTMLElement>) {
  if (!["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp", "Home", "End"].includes(e.key)) return;
  const radios = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]:not([disabled])'));
  const i = radios.indexOf(document.activeElement as HTMLElement);
  if (i < 0) return;
  e.preventDefault();
  const fwd = e.key === "ArrowRight" || e.key === "ArrowDown";
  const n = e.key === "Home" ? 0 : e.key === "End" ? radios.length - 1 : (i + (fwd ? 1 : -1) + radios.length) % radios.length;
  radios[n].focus();
  radios[n].click();
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
  const [pickerOpen, setPickerOpen] = useState(false);
  const [customOpen, setCustomOpen] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);

  // "/" focuses search, the convention in most developer dashboards.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (!searchRef.current) return;
      e.preventDefault();
      searchRef.current.focus();
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

  const listed = useMemo(() => (providers.data?.providers ?? []).filter((p) => !p.hidden), [providers.data]);
  const allConnected = useMemo(() => sortByPopularity(listed.filter((p) => accountsByProvider.has(p.id))), [listed, accountsByProvider]);
  const available = useMemo(() => sortByPopularity(listed.filter((p) => !accountsByProvider.has(p.id))), [listed, accountsByProvider]);
  const q = searchQuery.trim().toLowerCase();
  const connected = allConnected.filter((p) => matches(p, filter, q));
  const filtering = !!q || filter !== "all";
  const noneConnected = !providers.isLoading && !providers.isError && allConnected.length === 0;

  return (
    <>
      <PageHeader
        title="Providers"
        description={providers.data && allConnected.length > 0 ? `${allConnected.length} connected · ${available.length} available` : undefined}
        action={
          noneConnected ? undefined : (
            <Button onClick={() => setPickerOpen(true)}>
              <Plug aria-hidden="true" />
              Connect provider
            </Button>
          )
        }
      />

      {providers.isLoading ? (
        <div className="space-y-3" aria-busy="true" aria-label="Loading providers">
          <Skeleton className="h-10 w-full rounded-2xl" />
          <Skeleton className="h-72 w-full rounded-2xl" />
        </div>
      ) : providers.isError ? (
        <ErrorBanner message="Couldn't load providers. Check that the gateway is running, then reload." />
      ) : noneConnected ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <IconTile icon={ICONS.providers} size="lg" className="mx-auto mb-3" />
          <h2 className="text-[14px] font-semibold text-fg">No providers connected</h2>
          <p className="mx-auto mt-1 max-w-sm text-[13px] text-fg-muted">Connect one to start routing requests.</p>
          <Button className="mt-4" onClick={() => setPickerOpen(true)}>
            <Plug aria-hidden="true" />
            Connect provider
          </Button>
        </div>
      ) : (
        <section aria-labelledby="connected-heading" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
            <div className="mr-auto">
              <SectionTitle id="connected-heading" icon={ICONS.providers} title="Connected" />
            </div>
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                ref={searchRef}
                type="search"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search"
                aria-label="Search connected providers"
                aria-keyshortcuts="/"
                className="h-9 w-full rounded-lg border border-input bg-surface pl-8 pr-9 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              />
              {searchQuery ? (
                <button
                  type="button"
                  onClick={() => setSearchQuery("")}
                  className="absolute right-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                  aria-label="Clear search"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              ) : (
                <kbd aria-hidden="true" className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 rounded border border-line bg-subtle px-1.5 font-mono text-[11px] text-fg-faint">
                  /
                </kbd>
              )}
            </div>
            <Select value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Capability" className="w-full sm:w-44">
              {kindFilters.map((k) => (
                <option key={k.id} value={k.id}>
                  {k.id === "all" ? "All capabilities" : k.label}
                </option>
              ))}
            </Select>
          </div>
          <p className="sr-only" role="status" aria-live="polite">
            {filtering ? `${connected.length} of ${allConnected.length} connected providers shown` : ""}
          </p>
          {connected.length === 0 ? (
            <div className="px-6 py-10 text-center">
              <IconTile icon={SearchX} size="md" className="mx-auto mb-2.5" />
              <p className="text-[13px] font-medium text-fg">No connected provider matches</p>
              <button
                type="button"
                onClick={() => {
                  setSearchQuery("");
                  setFilter("all");
                }}
                className="mt-1 rounded-md text-[13px] font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                Clear filters
              </button>
            </div>
          ) : (
            <ConnectedTable providers={connected} accountsByProvider={accountsByProvider} healthByProvider={healthByProvider} />
          )}
        </section>
      )}

      {pickerOpen && (
        <ProviderPicker
          providers={available}
          onClose={() => setPickerOpen(false)}
          onCustom={() => {
            setPickerOpen(false);
            setCustomOpen(true);
          }}
        />
      )}
      {customOpen && <CreateCustomProviderModal onClose={() => setCustomOpen(false)} />}
    </>
  );
}

// ── Connected providers ─────────────────────────────────────────────────────

type Tone = "ok" | "warn" | "bad" | "neutral";
const BADGE_TONE = { ok: "success", warn: "warning", bad: "danger", neutral: "neutral" } as const;

// providerStatus answers "is it OK?" in one word: account problems first,
// then the last 24 hours of traffic.
function providerStatus(accs: Account[], h?: HealthTimelineProvider): { tone: Tone; label: string } {
  const attention = accs.filter((a) => a.needs_reconnect).length;
  if (attention > 0) return { tone: "warn", label: "Reconnect needed" };
  if (accs.length > 0 && accs.every((a) => a.disabled)) return { tone: "neutral", label: "Paused" };
  if (!h || !h.requests) return { tone: "neutral", label: "No traffic" };
  if (h.success_rate >= 0.99) return { tone: "ok", label: "Healthy" };
  if (h.success_rate >= 0.95) return { tone: "warn", label: "Degraded" };
  return { tone: "bad", label: "Failing" };
}

function NoData() {
  return (
    <>
      <span aria-hidden="true">—</span>
      <span className="sr-only">No data</span>
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
    <div className="overflow-x-auto">
      <table className="w-full min-w-[820px] text-[13px]">
        <thead>
          <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
            <th scope="col" className="px-4 py-2 font-medium">Provider</th>
            <th scope="col" className="px-4 py-2 font-medium">Status</th>
            <th scope="col" className="px-4 py-2 text-right font-medium">Accounts</th>
            <th scope="col" className="px-4 py-2 font-medium">Last 24 hours</th>
            <th scope="col" className="px-4 py-2 text-right font-medium">Requests</th>
            <th scope="col" className="px-4 py-2 text-right font-medium">Success</th>
            <th scope="col" className="px-4 py-2 text-right font-medium">Worst p95</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {providers.map((p) => {
            const accs = accountsByProvider.get(p.id) ?? [];
            const paused = accs.filter((a) => a.disabled).length;
            const h = healthByProvider.get(p.id);
            const status = providerStatus(accs, h);
            const badHours = h ? h.buckets.filter((b) => b.status === "degraded" || b.status === "down").length : 0;
            return (
              <tr key={p.id} className="cursor-pointer transition-colors hover:bg-hover" onClick={() => navigate(`/providers/${p.id}`)}>
                <td className="px-4 py-2">
                  <Link
                    to={`/providers/${p.id}`}
                    className="flex items-center gap-2.5 rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <ProviderLogo icon={p.icon} name={p.display_name} size={24} />
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-fg">{p.display_name}</span>
                      <span className="block truncate font-mono text-[11.5px] text-fg-faint">{p.id}</span>
                    </span>
                  </Link>
                </td>
                <td className="whitespace-nowrap px-4 py-2">
                  <Badge tone={BADGE_TONE[status.tone]}>
                    <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-current" />
                    {status.label}
                  </Badge>
                </td>
                <td className="whitespace-nowrap px-4 py-2 text-right">
                  <span className="tabular-nums text-fg">{accs.length}</span>
                  {paused > 0 && paused < accs.length && <span className="block text-[11.5px] text-fg-faint">{paused} paused</span>}
                </td>
                <td className="px-4 py-2">
                  {h ? (
                    <div
                      className="flex w-40 gap-[2px]"
                      role="img"
                      aria-label={badHours ? `${badHours} of ${h.buckets.length} hours had errors` : `No errors in ${h.buckets.length} hours`}
                    >
                      {h.buckets.map((b) => (
                        <span key={b.start} className={cn("h-3.5 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")} />
                      ))}
                    </div>
                  ) : (
                    <span className="text-[12px] text-fg-faint">No traffic</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right text-fg">{h ? h.requests.toLocaleString("en-US") : <NoData />}</td>
                <td className={cn("px-4 py-2 text-right", h && h.requests ? (h.success_rate >= 0.99 ? "text-fg" : h.success_rate >= 0.95 ? "text-warn" : "text-bad") : "text-fg-faint")}>
                  {h && h.requests ? `${(h.success_rate * 100).toFixed(1)}%` : <NoData />}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">{h && h.worst_p95_ms ? fmtLatency(h.worst_p95_ms) : <NoData />}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

const TICK_CLASS: Record<string, string> = { ok: "bg-ok/70", degraded: "bg-warn", down: "bg-bad", idle: "bg-track" };

function fmtLatency(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(2)} s`;
}

// ── Catalog picker ──────────────────────────────────────────────────────────

// ProviderPicker is the "Connect provider" dialog: search, filter by
// capability, then pick a provider to open its page and connect it there.
function ProviderPicker({ providers, onClose, onCustom }: { providers: Provider[]; onClose: () => void; onCustom: () => void }) {
  const [filter, setFilter] = useState("all");
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const visible = providers.filter((p) => matches(p, filter, q));
  const grouped = GROUPS.map((g) => ({ ...g, items: visible.filter((p) => groupOf(p) === g.id) })).filter((g) => g.items.length > 0);

  return (
    <ConnectDialog
      title="Connect a provider"
      width="xl"
      onClose={onClose}
      toolbar={
        <div className="space-y-2.5">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={`Search ${providers.length} providers`}
              aria-label="Search providers"
              className="h-9 w-full rounded-lg border border-input bg-surface pl-8 pr-3 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            />
          </div>
          <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Capability" onKeyDown={onRadioKeys}>
            {kindFilters.map((k) => {
              const active = filter === k.id;
              return (
                <button
                  key={k.id}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  tabIndex={active ? 0 : -1}
                  onClick={() => setFilter(k.id)}
                  className={cn(
                    "h-7 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                    active ? "border-accent-500/30 bg-accent-500/10 text-link" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                  )}
                >
                  {k.label}
                </button>
              );
            })}
          </div>
        </div>
      }
      footer={
        <>
          <span className="mr-auto text-[12.5px] text-fg-muted">Not listed?</span>
          <SecondaryAction onClick={onCustom}>
            <Plus aria-hidden="true" />
            New custom provider
          </SecondaryAction>
        </>
      }
    >
      <p className="sr-only" role="status" aria-live="polite">
        {visible.length} provider{visible.length === 1 ? "" : "s"} shown
      </p>
      {grouped.length === 0 ? (
        <div className="py-8 text-center">
          <IconTile icon={SearchX} size="md" className="mx-auto mb-2.5" />
          <p className="text-[13px] text-fg-muted">No providers match{q ? ` “${query.trim()}”` : " this capability"}.</p>
        </div>
      ) : (
        <div className="space-y-5">
          {grouped.map((g) => (
            <section key={g.id} aria-labelledby={`group-${g.id}`}>
              <div className="mb-1.5 px-2">
                <SectionTitle
                  as="h3"
                  id={`group-${g.id}`}
                  icon={g.icon}
                  title={
                    <>
                      {g.title}
                      <span className="ml-2 text-[12px] font-normal tabular-nums text-fg-faint">{g.items.length}</span>
                    </>
                  }
                />
              </div>
              <ul className="grid grid-cols-1 gap-x-2 sm:grid-cols-2">
                {g.items.map((p) => (
                  <li key={p.id}>
                    <CatalogItem provider={p} />
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </ConnectDialog>
  );
}

function CatalogItem({ provider: p }: { provider: Provider }) {
  const tag = p.deprecated
    ? { label: "Unofficial", tone: "warning" as const }
    : !p.drivable
      ? { label: "Coming soon", tone: "neutral" as const }
      : p.auth_kind === "none"
        ? { label: "Free", tone: "success" as const }
        : null;
  return (
    <Link
      to={`/providers/${p.id}`}
      className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    >
      <ProviderLogo icon={p.icon} name={p.display_name} size={24} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-fg">{p.display_name}</span>
        <span className="block truncate font-mono text-[11.5px] text-fg-faint">{p.id}</span>
      </span>
      {tag && (
        <Badge tone={tag.tone}>
          {p.deprecated && <AlertTriangle className="h-3 w-3" aria-hidden="true" />}
          {tag.label}
        </Badge>
      )}
    </Link>
  );
}

// ── Custom provider ─────────────────────────────────────────────────────────

// CreateCustomProviderModal creates a new dynamic custom provider instance.
// Each instance gets a unique id so multiple OpenAI-/Anthropic-compatible
// endpoints stay fully isolated (own base URL, accounts, and models).
function CreateCustomProviderModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const formId = useId();
  const [name, setName] = useState("");
  const [dialect, setDialect] = useState("openai");
  const [baseURL, setBaseURL] = useState("");
  const [alias, setAlias] = useState("");
  const [error, setError] = useState("");
  const [showErrors, setShowErrors] = useState(false);

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
      onClose();
      navigate(`/providers/${p.id}`);
    },
    onError: (e: Error) => setError(e.message),
  });

  const nameMissing = name.trim().length === 0;
  const urlMissing = baseURL.trim().length === 0;
  const canSubmit = !nameMissing && !urlMissing && !create.isPending;

  return (
    <ConnectDialog
      title="New custom provider"
      description="Any OpenAI- or Anthropic-compatible endpoint"
      onClose={onClose}
      footer={
        <>
          <SecondaryAction onClick={onClose}>Cancel</SecondaryAction>
          <PrimaryAction type="submit" form={formId} busy={create.isPending}>
            {!create.isPending && <Plus aria-hidden="true" />}
            {create.isPending ? "Creating…" : "Create provider"}
          </PrimaryAction>
        </>
      }
    >
      <form
        id={formId}
        className="space-y-3.5"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (canSubmit) {
            create.mutate();
            return;
          }
          if (create.isPending) return;
          const formEl = e.currentTarget;
          setShowErrors(true);
          window.requestAnimationFrame(() => formEl.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
        }}
      >
        <TextField
          label="Name"
          required
          value={name}
          onChange={(e) => {
            setName(e.target.value);
            setError("");
          }}
          placeholder="Local vLLM"
          error={showErrors && nameMissing ? "Enter a name." : undefined}
        />
        <SelectField label="Dialect" value={dialect} onChange={(e) => setDialect(e.target.value)}>
          <option value="openai">OpenAI-compatible</option>
          <option value="anthropic">Anthropic-compatible</option>
        </SelectField>
        <TextField
          label="Base URL"
          required
          value={baseURL}
          onChange={(e) => {
            setBaseURL(e.target.value);
            setError("");
          }}
          placeholder="https://llm.example.com/v1"
          className="font-mono"
          error={showErrors && urlMissing ? "Enter the endpoint's base URL." : undefined}
        />
        <TextField
          label="Route prefix"
          optional
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
          placeholder="kei-ai"
          pattern="[A-Za-z0-9-]*"
          maxLength={32}
          className="font-mono"
          hint={
            <>
              Models route as <code className="font-mono text-fg">{alias || "<prefix>"}/&lt;model&gt;</code>. Defaults to the name.
            </>
          }
        />
        <FormError message={error} />
      </form>
    </ConnectDialog>
  );
}
