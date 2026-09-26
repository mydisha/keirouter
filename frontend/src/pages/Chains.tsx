import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { Check, ChevronRight, CircleAlert, Copy, Layers, Loader2, Pencil, Plus, Search, ShieldAlert, Trash2 } from "lucide-react";
import { api, type Chain, type HealthChainRow, type Provider } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Badge, Button, Card, EmptyState, ErrorCard, Input, Modal, Skeleton } from "../components/ui";
import { ChainRoutePreview } from "../components/chains/ChainRoutePreview";
import { strategyLabel } from "../components/chains/chainUtils";

type StrategyFilter = "all" | "priority" | "round_robin" | "latency" | "cost";
type HealthFilter = "all" | "healthy" | "degraded" | "unhealthy" | "unknown";

const healthTone = (status?: HealthChainRow["status"]) => {
  switch (status) {
    case "healthy": return "success" as const;
    case "degraded": return "warning" as const;
    case "unhealthy": return "danger" as const;
    default: return "neutral" as const;
  }
};

function ChainListSkeleton() {
  return <Card className="overflow-hidden">{[0, 1, 2].map((index) => <div key={index} className="flex items-center gap-4 border-b border-[var(--border)] px-5 py-4 last:border-b-0"><Skeleton className="h-10 w-10 shrink-0 rounded-xl" /><div className="min-w-0 flex-1 space-y-2"><Skeleton className="h-4 w-36" /><Skeleton className="h-3 w-64" /></div><Skeleton className="hidden h-8 w-20 rounded-lg sm:block" /></div>)}</Card>;
}

function ChainHealth({ health }: { health?: HealthChainRow }) {
  if (!health) return <span className="text-xs text-[var(--text-muted)]">No health data</span>;
  return <div className="flex min-w-0 items-center gap-2"><Badge tone={healthTone(health.status)} title={health.main_issue || undefined}>{health.status}</Badge><span className="hidden text-xs tabular-nums text-[var(--text-muted)] lg:inline">{health.requests.toLocaleString()} requests · {(health.fallback_rate * 100).toFixed(1)}% fallback</span></div>;
}

function ChainRow({ chain, providers, health, onDelete }: { chain: Chain; providers: Provider[]; health?: HealthChainRow; onDelete: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const hasIssue = health?.status === "degraded" || health?.status === "unhealthy";
  const copyTarget = async () => {
    try {
      await navigator.clipboard.writeText(`chain:${chain.name}`);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
      toast.success("Chain target copied", `Use chain:${chain.name} as the model.`);
    } catch {
      toast.error("Copy failed", "Your browser did not allow access to the clipboard.");
    }
  };
  return <article className="group grid gap-3 px-4 py-4 transition-colors hover:bg-[var(--bg-subtle)]/70 sm:grid-cols-[minmax(200px,0.8fr)_minmax(280px,1.55fr)_minmax(150px,0.55fr)_auto] sm:items-center sm:gap-5 sm:px-5">
    <div className="min-w-0"><div className="flex min-w-0 items-center gap-2"><div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent-100 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300"><Layers className="h-4.5 w-4.5" /></div><div className="min-w-0"><button type="button" onClick={() => navigate(dashboard(`/chains/${chain.id}/edit`))} className="block max-w-full truncate text-left text-sm font-semibold text-[var(--text)] hover:text-secondary-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50">{chain.name}</button><div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-[var(--text-muted)]"><span className="truncate font-mono">chain:{chain.name}</span><button type="button" onClick={copyTarget} className="rounded p-0.5 text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50" aria-label={`Copy chain:${chain.name}`}>{copied ? <Check className="h-3.5 w-3.5 text-[color:var(--color-success)]" /> : <Copy className="h-3.5 w-3.5" />}</button></div></div></div></div>
    <div className="min-w-0"><div className="mb-1.5 flex flex-wrap items-center gap-2"><Badge tone="accent">{strategyLabel(chain.strategy)}</Badge><span className="text-xs text-[var(--text-muted)]">{chain.steps.length} model{chain.steps.length === 1 ? "" : "s"}</span>{chain.fallback_provider && chain.fallback_model && <span className="inline-flex items-center gap-1 text-xs text-[color:var(--color-warning)]"><ShieldAlert className="h-3.5 w-3.5" />Final fallback</span>}</div><ChainRoutePreview chain={chain} providers={providers} compact /></div>
    <div className="min-w-0"><ChainHealth health={health} />{hasIssue && health?.main_issue && <p className="mt-1 truncate text-xs text-[color:var(--color-warning)]" title={health.main_issue}>{health.main_issue}</p>}</div>
    <div className="flex items-center justify-end gap-1 border-t border-[var(--border)] pt-3 sm:border-t-0 sm:pt-0"><button type="button" onClick={() => navigate(dashboard(`/chains/${chain.id}/edit`))} className="inline-flex h-9 items-center gap-1.5 rounded-lg px-2.5 text-xs font-medium text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"><Pencil className="h-3.5 w-3.5" />Edit</button><button type="button" onClick={onDelete} className="inline-flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[color:var(--color-danger)]/10 hover:text-[color:var(--color-danger)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--color-danger)]/40" aria-label={`Delete ${chain.name}`}><Trash2 className="h-4 w-4" /></button><button type="button" onClick={() => navigate(dashboard(`/chains/${chain.id}/edit`))} className="inline-flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-secondary-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50" aria-label={`Open details for ${chain.name}`} title="Open details"><ChevronRight className="h-4 w-4" aria-hidden="true" /></button></div>
  </article>;
}

export function ChainsPage() {
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const [strategy, setStrategy] = useState<StrategyFilter>("all");
  const [healthFilter, setHealthFilter] = useState<HealthFilter>("all");
  const [deleting, setDeleting] = useState<Chain | null>(null);
  const chainsQuery = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains() });
  const providersQuery = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const healthQuery = useQuery({ queryKey: ["health-chains", "24h"], queryFn: () => api.healthChains("24h"), staleTime: 30_000, retry: 1 });
  const deleteMutation = useMutation({ mutationFn: (id: string) => api.deleteChain(id), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["chains"] }); queryClient.invalidateQueries({ queryKey: ["health-chains"] }); toast.success("Chain deleted", "It will no longer resolve as a model target."); setDeleting(null); }, onError: (error: Error) => toast.error("Deletion failed", error.message) });
  const healthByID = useMemo(() => new Map((healthQuery.data?.chains ?? []).map((item) => [item.chain_id, item])), [healthQuery.data]);
  const chains = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    return (chainsQuery.data?.chains ?? []).filter((chain) => {
      const matchesQuery = !normalizedQuery || [chain.name, chain.strategy, ...chain.steps.map((step) => `${step.provider}/${step.model}`)].join(" ").toLowerCase().includes(normalizedQuery);
      const matchesStrategy = strategy === "all" || chain.strategy === strategy || (strategy === "round_robin" && chain.strategy === "round-robin");
      const matchesHealth = healthFilter === "all" || healthByID.get(chain.id)?.status === healthFilter;
      return matchesQuery && matchesStrategy && matchesHealth;
    });
  }, [chainsQuery.data, healthByID, healthFilter, query, strategy]);
  const filtersActive = Boolean(query || strategy !== "all" || healthFilter !== "all");
  return <>
    <PageHeader title="Chains" icon={Layers} description="Build named routing paths that keep requests moving when a model or provider cannot serve them." action={<Button onClick={() => navigate(dashboard("/chains/new"))}><Plus className="h-4 w-4" />Create chain</Button>} />
    <div className="space-y-4"><div className="flex flex-col gap-2 sm:flex-row sm:items-center"><div className="relative min-w-0 flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search chains, models, or providers…" className="pl-9" aria-label="Search chains" /></div><select value={strategy} onChange={(event) => setStrategy(event.target.value as StrategyFilter)} className="min-h-10 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 text-sm text-[var(--text)] focus:border-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/30"><option value="all">All strategies</option><option value="priority">Priority</option><option value="round_robin">Round robin</option><option value="latency">Latency</option><option value="cost">Cost</option></select><select value={healthFilter} onChange={(event) => setHealthFilter(event.target.value as HealthFilter)} className="min-h-10 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 text-sm text-[var(--text)] focus:border-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/30"><option value="all">All health</option><option value="healthy">Healthy</option><option value="degraded">Degraded</option><option value="unhealthy">Unhealthy</option><option value="unknown">Unknown</option></select></div>
      {healthQuery.isError && <div className="flex items-center gap-2 rounded-xl border border-[color:var(--color-warning)]/25 bg-[color:var(--color-warning)]/8 px-3.5 py-2.5 text-sm text-[color:var(--color-warning)]"><CircleAlert className="h-4 w-4 shrink-0" />Health data is temporarily unavailable. Chain configuration is still available.</div>}
      {chainsQuery.isLoading ? <ChainListSkeleton /> : chainsQuery.isError ? <ErrorCard message="Could not load chains. Please refresh and try again." /> : chains.length === 0 ? <Card><div className="space-y-4"><EmptyState title={filtersActive ? "No chains match these filters" : "No chains yet"} hint={filtersActive ? "Try clearing a filter or search term." : "Create a chain to add ordered fallback and resilience to a model target."} />{!filtersActive && <div className="flex justify-center"><Button onClick={() => navigate(dashboard("/chains/new"))}><Plus className="h-4 w-4" />Create your first chain</Button></div>}</div></Card> : <Card className="overflow-hidden p-0"><div className="hidden grid-cols-[minmax(200px,0.8fr)_minmax(280px,1.55fr)_minmax(150px,0.55fr)_auto] gap-5 border-b border-[var(--border)] bg-[var(--bg-subtle)]/60 px-5 py-2.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)] sm:grid"><span>Chain</span><span>Route</span><span>Health · 24h</span><span className="text-right">Actions</span></div>{chains.map((chain, index) => <div key={chain.id} className={index > 0 ? "border-t border-[var(--border)]" : ""}><ChainRow chain={chain} providers={providersQuery.data?.providers ?? []} health={healthByID.get(chain.id)} onDelete={() => setDeleting(chain)} /></div>)}</Card>}</div>
    <Modal open={Boolean(deleting)} onClose={() => !deleteMutation.isPending && setDeleting(null)} title="Delete chain" subtitle={deleting ? `This permanently removes chain:${deleting.name}. Existing requests using this target will no longer resolve.` : undefined}><div className="flex justify-end gap-2 px-6 py-4"><Button variant="ghost" onClick={() => setDeleting(null)} disabled={deleteMutation.isPending}>Cancel</Button><Button variant="danger" onClick={() => deleting && deleteMutation.mutate(deleting.id)} disabled={deleteMutation.isPending}>{deleteMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}Delete chain</Button></div></Modal>
  </>;
}
