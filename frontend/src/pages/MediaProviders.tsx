import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ChevronRight, Search, X } from "lucide-react";
import { api, type Account, type Provider } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { Badge, ErrorBanner, Skeleton } from "../components/ui";

// Media service kinds — everything that isn't a plain chat/LLM provider.
const mediaKinds = [
  { id: "embedding", label: "Embeddings", description: "Text embedding models for search and RAG." },
  { id: "image", label: "Image", description: "Text-to-image generation providers." },
  { id: "tts", label: "Text-to-speech", description: "Voice synthesis providers." },
  { id: "stt", label: "Speech-to-text", description: "Audio transcription providers." },
  { id: "search", label: "Web search", description: "Web search API providers." },
  { id: "fetch", label: "Web fetch", description: "Web page content extraction." },
];

const kindLabels: Record<string, string> = {
  embedding: "Embed",
  image: "Image",
  tts: "TTS",
  stt: "STT",
  search: "Search",
  fetch: "Fetch",
};

function capabilityList(p: Provider): string {
  return p.service_kinds
    .filter((k) => k !== "llm")
    .map((k) => kindLabels[k] ?? k)
    .join(" · ");
}

export function MediaProvidersPage() {
  const { kind: urlKind } = useParams();
  const navigate = useNavigate();
  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers() });
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts() });
  const [filter, setFilter] = useState(urlKind || "embedding");
  const [searchQuery, setSearchQuery] = useState("");

  // The URL is the source of truth for the selected capability.
  const activeFilter = urlKind || filter;
  const setActiveFilter = (k: string) => {
    setFilter(k);
    navigate(`/media/${k}`, { replace: true });
  };

  const accountsByProvider = useMemo(() => {
    const map = new Map<string, Account[]>();
    for (const a of accounts.data?.accounts ?? []) {
      const list = map.get(a.provider) ?? [];
      list.push(a);
      map.set(a.provider, list);
    }
    return map;
  }, [accounts.data]);

  const list = useMemo(() => {
    const all = providers.data?.providers ?? [];
    return all
      .filter((p) => !p.hidden)
      .filter((p) => p.service_kinds.includes(activeFilter));
  }, [providers.data, activeFilter]);

  const visible = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return list;
    return list.filter((p) => p.display_name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
  }, [list, searchQuery]);

  const connected = visible.filter((p) => accountsByProvider.has(p.id));
  const available = visible.filter((p) => !accountsByProvider.has(p.id));
  const activeKind = mediaKinds.find((k) => k.id === activeFilter) ?? mediaKinds[0];

  return (
    <>
      <PageHeader
        title="Media providers"
        description="Connect providers for embeddings, image generation, speech, and web access. Each capability routes through the same OpenAI-compatible endpoints."
      />

      <div className="mb-3 flex flex-col gap-3 lg:flex-row lg:items-center">
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Filter by capability">
          {mediaKinds.map((k) => {
            const active = activeFilter === k.id;
            return (
              <button
                key={k.id}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setActiveFilter(k.id)}
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
        <div className="relative lg:ml-auto lg:w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
          <input
            type="search"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={`Search ${activeKind.label.toLowerCase()} providers`}
            aria-label="Search media providers"
            className="h-9 w-full rounded-lg border border-line bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery("")}
              className="absolute right-2 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg"
              aria-label="Clear search"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>
      <p className="mb-5 text-[12.5px] text-fg-muted">
        {activeKind.description}{" "}
        {providers.data && <span className="tabular-nums text-fg-faint">{list.length} available.</span>}
      </p>

      {providers.isLoading ? (
        <div className="space-y-5">
          <Skeleton className="h-32 w-full rounded-2xl" />
          <Skeleton className="h-56 w-full rounded-2xl" />
        </div>
      ) : providers.isError ? (
        <ErrorBanner message="Couldn't load providers. Is the backend running?" />
      ) : !list.length ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <p className="text-[13px] font-medium text-fg">No providers for this capability</p>
          <p className="mt-1 text-[12.5px] text-fg-muted">Pick another capability above, or add a provider account on the Providers page.</p>
          <Link to="/providers" className="mt-3 inline-block text-[13px] font-medium text-accent-500 hover:underline dark:text-accent-400">
            Go to providers
          </Link>
        </div>
      ) : (
        <div className="space-y-8">
          <section aria-labelledby="media-connected-heading">
            <div className="mb-2.5 flex items-baseline justify-between">
              <h2 id="media-connected-heading" className="text-[14px] font-semibold text-fg">Connected</h2>
              <span className="text-[12px] tabular-nums text-fg-faint">{connected.length}</span>
            </div>
            {connected.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-8 text-center">
                <p className="text-[13px] font-medium text-fg">{searchQuery ? "No connected provider matches" : `No ${activeKind.label.toLowerCase()} provider connected yet`}</p>
                <p className="mt-1 text-[12.5px] text-fg-muted">
                  {searchQuery ? "Clear the search to see all of them." : "Pick one below and add an account to start routing this capability."}
                </p>
              </div>
            ) : (
              <ConnectedTable providers={connected} kind={activeFilter} accountsByProvider={accountsByProvider} />
            )}
          </section>

          <section aria-labelledby="media-catalog-heading">
            <div className="mb-2.5 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5">
              <div className="flex items-baseline gap-2.5">
                <h2 id="media-catalog-heading" className="text-[14px] font-semibold text-fg">Catalog</h2>
                <span className="text-[12px] tabular-nums text-fg-faint">{available.length}</span>
              </div>
              <p className="text-[12.5px] text-fg-muted">Open a provider to add an account and try it in the playground.</p>
            </div>
            {available.length === 0 ? (
              <div className="rounded-2xl border border-line bg-surface px-6 py-10 text-center text-[13px] text-fg-muted">
                No other providers match{searchQuery ? ` “${searchQuery}”` : " this capability"}.
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
                {available.map((p) => (
                  <CatalogCard key={p.id} provider={p} kind={activeFilter} />
                ))}
              </div>
            )}
          </section>
        </div>
      )}
    </>
  );
}

function ConnectedTable({
  providers,
  kind,
  accountsByProvider,
}: {
  providers: Provider[];
  kind: string;
  accountsByProvider: Map<string, Account[]>;
}) {
  const navigate = useNavigate();
  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[560px] text-[13px]">
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th className="px-4 py-2 font-medium">Provider</th>
              <th className="px-4 py-2 font-medium">Accounts</th>
              <th className="px-4 py-2 font-medium">Capabilities</th>
              <th className="w-8 px-2 py-2" aria-hidden="true" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {providers.map((p) => {
              const accs = accountsByProvider.get(p.id) ?? [];
              const paused = accs.filter((a) => a.disabled).length;
              const href = `/media/${kind}/${p.id}`;
              return (
                <tr key={p.id} className="cursor-pointer transition-colors hover:bg-hover" onClick={() => navigate(href)}>
                  <td className="px-4 py-2.5">
                    <Link to={href} className="flex items-center gap-2.5 focus:outline-none focus-visible:underline" onClick={(e) => e.stopPropagation()}>
                      <ProviderLogo icon={p.icon} name={p.display_name} size={24} />
                      <span className="min-w-0">
                        <span className="flex items-center gap-1.5">
                          <span className="truncate font-medium text-fg">{p.display_name}</span>
                          {!p.drivable && <Badge tone="neutral">Coming soon</Badge>}
                        </span>
                        <span className="block truncate font-mono text-[11.5px] text-fg-faint">{p.id}</span>
                      </span>
                    </Link>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5">
                    <span className="tabular-nums text-fg">{accs.length}</span>
                    {paused > 0 && <span className="ml-1.5 text-[12px] text-fg-faint">· {paused} paused</span>}
                  </td>
                  <td className="px-4 py-2.5 text-[12.5px] text-fg-muted">{capabilityList(p)}</td>
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

function CatalogCard({ provider: p, kind }: { provider: Provider; kind: string }) {
  return (
    <Link
      to={`/media/${kind}/${p.id}`}
      aria-label={`Open ${p.display_name}`}
      className="group flex items-center gap-3 rounded-2xl border border-line bg-surface px-3 py-2.5 transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
    >
      <ProviderLogo icon={p.icon} name={p.display_name} size={28} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-fg">{p.display_name}</span>
        <span className="block truncate text-[11.5px] text-fg-faint">
          <span className="font-mono">{p.id}</span>
          {capabilityList(p) && <> · {capabilityList(p)}</>}
        </span>
      </span>
      {!p.drivable ? (
        <span title="Listed for discovery; routing is not available yet.">
          <Badge tone="neutral">Coming soon</Badge>
        </span>
      ) : (
        <span className="hidden text-[12px] font-medium text-accent-500 group-hover:inline dark:text-accent-400">Connect</span>
      )}
    </Link>
  );
}
