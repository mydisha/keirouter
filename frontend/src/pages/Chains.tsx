import { useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { AlertTriangle, Ban, Copy, MoreHorizontal, Pencil, Plus, RotateCcw, Search, Trash2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type Chain, type HealthChainRow, type Provider } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Badge, Button, EmptyState, ErrorBanner, SectionTitle, Skeleton } from "../components/ui";
import { ICONS } from "../lib/icons";
import { useConfirm } from "../components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";
import { ChainRoutePreview } from "../components/chains/ChainRoutePreview";
import { strategyIcon, strategyLabel } from "../components/chains/chainUtils";
import { fmtRate } from "../components/ModelCatalog";

type StrategyFilter = "all" | "priority" | "round_robin" | "latency" | "cost";
type HealthFilter = "all" | "healthy" | "degraded" | "unhealthy" | "unknown";

const STRATEGY_FILTERS: { value: StrategyFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "priority", label: "Priority" },
  { value: "round_robin", label: "Round robin" },
  { value: "latency", label: "Latency" },
  { value: "cost", label: "Cost" },
];

const HEALTH_FILTERS: { value: HealthFilter; label: string }[] = [
  { value: "all", label: "Any health" },
  { value: "healthy", label: "Healthy" },
  { value: "degraded", label: "Degraded" },
  { value: "unhealthy", label: "Unhealthy" },
  { value: "unknown", label: "Unknown" },
];

const matchesStrategyFilter = (chain: Chain, strategy: StrategyFilter) =>
  strategy === "all" || chain.strategy === strategy || (strategy === "round_robin" && chain.strategy === "round-robin");

const statusDot = (status?: HealthChainRow["status"]) => {
  switch (status) {
    case "healthy": return "bg-ok";
    case "degraded": return "bg-warn";
    case "unhealthy": return "bg-bad";
    default: return "bg-fg-faint";
  }
};

const statusBadge = (status?: HealthChainRow["status"]) => {
  switch (status) {
    case "healthy": return "success" as const;
    case "degraded": return "warning" as const;
    case "unhealthy": return "danger" as const;
    default: return "neutral" as const;
  }
};

const statusLabel = (status?: HealthChainRow["status"]) =>
  status ? status.charAt(0).toUpperCase() + status.slice(1) : "No data";

// FilterGroup is a compact segmented radiogroup with roving focus: Tab enters
// the selected option, arrow keys move and select.
function FilterGroup<T extends string>({ label, value, onChange, options }: {
  label: string;
  value: T;
  onChange: (next: T) => void;
  options: { value: T; label: string; count?: number; dot?: string }[];
}) {
  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
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
    // Below sm the group is one horizontally scrollable row that bleeds to the
    // page edges instead of wrapping into a tall block.
    <div className="-mx-4 overflow-x-auto px-4 [scrollbar-width:none] sm:mx-0 sm:overflow-visible sm:px-0">
      <div role="radiogroup" aria-label={label} onKeyDown={onKeyDown} className="inline-flex flex-nowrap rounded-xl border border-line bg-subtle p-0.5 sm:max-w-full sm:flex-wrap">
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
              className={cn(
                "inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2.5 text-[12px] font-medium sm:h-7 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
              )}
            >
              {option.dot && <span className={cn("h-1.5 w-1.5 rounded-full", option.dot)} aria-hidden="true" />}
              {option.label}
              {option.count !== undefined && (
                <span className="tabular-nums text-fg-muted">
                  <span className="sr-only">, </span>{option.count}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function ChainListSkeleton() {
  return (
    <div className="space-y-3" aria-busy="true" aria-label="Loading chains">
      <Skeleton className="h-9 w-full max-w-2xl" />
      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="h-8 border-b border-line bg-subtle" />
        <div className="divide-y divide-line">
          {[0, 1, 2, 3].map((index) => (
            <div key={index} className="flex items-center gap-6 px-4 py-3">
              <div className="w-56 space-y-1.5"><Skeleton className="h-3.5 w-32" /><Skeleton className="h-3 w-44" /></div>
              <Skeleton className="h-5 w-16 rounded-md" />
              <Skeleton className="h-5 w-28 rounded-md" />
              <Skeleton className="ml-auto h-3.5 w-24" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

type ChainRowProps = {
  chain: Chain;
  providers: Provider[];
  health?: HealthChainRow;
  onOpen: () => void;
  onCopy: () => void;
  onDelete: () => void;
  onToggleSale: () => void;
};

// ChainActionsMenu is the row menu shared by the desktop table row and the
// mobile card, so both expose exactly the same actions.
function ChainActionsMenu({ chain, onOpen, onCopy, onDelete, onToggleSale, className }: Pick<ChainRowProps, "chain" | "onOpen" | "onCopy" | "onDelete" | "onToggleSale"> & { className?: string }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={`Actions for ${chain.name}`}
        className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500", className)}
      >
        <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onOpen}>
          <Pencil aria-hidden="true" />
          Edit chain
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onCopy}>
          <Copy aria-hidden="true" />
          Copy model target
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onToggleSale}>
          {chain.disabled ? <RotateCcw aria-hidden="true" /> : <Ban aria-hidden="true" />}
          {chain.disabled ? "Put back on sale" : "Mark sold out"}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem tone="danger" onSelect={onDelete}>
          <Trash2 aria-hidden="true" />
          Delete chain
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function HealthBadge({ health }: { health?: HealthChainRow }) {
  return (
    <span className="inline-flex shrink-0" title={health?.main_issue || undefined}>
      <Badge tone={statusBadge(health?.status)}>
        <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", statusDot(health?.status))} aria-hidden="true" />
        {statusLabel(health?.status)}
      </Badge>
    </span>
  );
}

// ChainPricing shows the chain's billed $/M rates, or a Sold out badge (with
// the last stored price) when the operator took it off sale or no market
// offer currently clears its floor.
function ChainPricing({ chain }: { chain: Chain }) {
  if (chain.sold_out || chain.disabled) {
    return (
      <div className="flex flex-col items-start gap-1">
        <span
          className="inline-flex"
          title={chain.disabled
            ? "Marked sold out by an admin; requests are refused with 503 until it is put back on sale."
            : "No market offer currently meets this chain's price floor; requests are refused with 503 until one does."}
        >
          <Badge tone="danger">Sold out</Badge>
        </span>
        <span className="text-[12px] tabular-nums text-fg-muted">
          {chain.disabled ? "Set manually · " : ""}Last price {fmtRate(chain.input_per_m)} / {fmtRate(chain.output_per_m)}
        </span>
      </div>
    );
  }
  const hasPricing = chain.input_per_m > 0 || chain.output_per_m > 0 || chain.cache_write_per_m > 0 || chain.cache_read_per_m > 0;
  if (!hasPricing) return <><span className="text-fg-muted" aria-hidden="true">—</span><span className="sr-only">No price set</span></>;
  return (
    <div className="grid grid-cols-2 gap-x-4 gap-y-0.5 text-[12px] tabular-nums text-fg-muted">
      <span className="truncate">In <span className="font-medium text-fg">{fmtRate(chain.input_per_m)}</span></span>
      <span className="truncate">Out <span className="font-medium text-fg">{fmtRate(chain.output_per_m)}</span></span>
      <span className="truncate">CW <span className="font-medium text-fg">{fmtRate(chain.cache_write_per_m)}</span></span>
      <span className="truncate">CR <span className="font-medium text-fg">{fmtRate(chain.cache_read_per_m)}</span></span>
    </div>
  );
}

// ChainCard is the below-md rendering of a chain row: identity + health on
// the first line, the route on the second, key facts and the menu last.
function ChainCard({ chain, providers, health, onOpen, onCopy, onDelete, onToggleSale }: ChainRowProps) {
  const hasIssue = health?.status === "degraded" || health?.status === "unhealthy";
  const facts = [
    strategyLabel(chain.strategy),
    `${chain.steps.length} step${chain.steps.length === 1 ? "" : "s"}`,
    health ? `${health.requests.toLocaleString()} requests` : null,
    health ? `${health.fallback_rate.toFixed(1)}% fallback` : null,
  ].filter(Boolean).join(" · ");
  return (
    <li className="space-y-2 px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <Link
            to={dashboard(`/chains/${chain.id}/edit`)}
            className="block truncate rounded-sm text-[13px] leading-6 font-medium text-fg hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            title={chain.name}
          >
            {chain.name}
          </Link>
          <span className="block truncate font-mono text-[12px] text-fg-muted" title={`chain:${chain.name}`}>chain:{chain.name}</span>
        </div>
        <HealthBadge health={health} />
      </div>
      <ChainRoutePreview chain={chain} providers={providers} compact />
      <div>
        <p className="mb-0.5 text-[11.5px] font-medium text-fg-muted">Pricing $/M</p>
        <ChainPricing chain={chain} />
      </div>
      {hasIssue && health?.main_issue && <p className="line-clamp-2 text-[12px] text-fg-muted">{health.main_issue}</p>}
      <div className="flex items-center justify-between gap-3">
        <p className="min-w-0 text-[12px] tabular-nums text-fg-muted">{facts}</p>
        <ChainActionsMenu chain={chain} onOpen={onOpen} onCopy={onCopy} onDelete={onDelete} onToggleSale={onToggleSale} className="h-9 w-9" />
      </div>
    </li>
  );
}

function ChainRow({ chain, providers, health, onOpen, onCopy, onDelete, onToggleSale }: ChainRowProps) {
  const hasIssue = health?.status === "degraded" || health?.status === "unhealthy";
  const StrategyIcon = strategyIcon(chain.strategy);
  const stop = (event: MouseEvent) => event.stopPropagation();
  return (
    <tr className="cursor-pointer transition-colors hover:bg-hover" onClick={onOpen}>
      <td className="max-w-[260px] px-4 py-2.5">
        <Link
          to={dashboard(`/chains/${chain.id}/edit`)}
          onClick={stop}
          className="block truncate rounded-sm leading-6 font-medium text-fg hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          title={chain.name}
        >
          {chain.name}
        </Link>
        <button
          type="button"
          onClick={(event) => { stop(event); onCopy(); }}
          className="group mt-0.5 inline-flex min-h-6 max-w-full items-center gap-1.5 rounded-md font-mono text-[12px] text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          title="Copy model target"
          aria-label={`Copy model target chain:${chain.name}`}
        >
          <span className="truncate">chain:{chain.name}</span>
          <Copy className="h-3.5 w-3.5 shrink-0 text-fg-faint transition-opacity [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:group-focus-visible:opacity-100" strokeWidth={1.75} aria-hidden="true" />
        </button>
      </td>
      <td className="px-4 py-2.5">
        <Badge tone="secondary">
          <StrategyIcon className="h-3 w-3 shrink-0" strokeWidth={1.75} aria-hidden="true" />
          {strategyLabel(chain.strategy)}
        </Badge>
        <span className="mt-1 block text-[12px] tabular-nums text-fg-muted">{chain.steps.length} step{chain.steps.length === 1 ? "" : "s"}</span>
      </td>
      <td className="px-4 py-2.5"><ChainRoutePreview chain={chain} providers={providers} compact /></td>
      <td className="px-4 py-2.5"><ChainPricing chain={chain} /></td>
      <td className="max-w-[220px] px-4 py-2.5">
        <HealthBadge health={health} />
        {hasIssue && health?.main_issue && <p className="mt-0.5 truncate text-[12px] text-fg-muted" title={health.main_issue}>{health.main_issue}</p>}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg">{health ? health.requests.toLocaleString() : <><span className="text-fg-muted" aria-hidden="true">—</span><span className="sr-only">No data</span></>}</td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg">{health ? `${health.fallback_rate.toFixed(1)}%` : <><span className="text-fg-muted" aria-hidden="true">—</span><span className="sr-only">No data</span></>}</td>
      <td className="px-2 py-2.5" onClick={stop}>
        <ChainActionsMenu chain={chain} onOpen={onOpen} onCopy={onCopy} onDelete={onDelete} onToggleSale={onToggleSale} />
      </td>
    </tr>
  );
}

export function ChainsPage() {
  const navigate = useNavigate();
  const toast = useToast();
  const confirmAction = useConfirm();
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const [strategy, setStrategy] = useState<StrategyFilter>("all");
  const [healthFilter, setHealthFilter] = useState<HealthFilter>("all");
  const chainsQuery = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains() });
  const providersQuery = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const healthQuery = useQuery({ queryKey: ["health-chains", "24h"], queryFn: () => api.healthChains("24h"), staleTime: 30_000, retry: 1 });
  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteChain(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["chains"] });
      queryClient.invalidateQueries({ queryKey: ["health-chains"] });
      toast.success("Chain deleted", "It will no longer resolve as a model target.");
    },
    onError: (error: Error) => toast.error("Deletion failed", error.message),
  });
  const saleMutation = useMutation({
    mutationFn: ({ id, disabled }: { id: string; disabled: boolean }) => api.updateChain(id, { disabled }),
    onSuccess: (_data, { disabled }) => {
      queryClient.invalidateQueries({ queryKey: ["chains"] });
      toast.success(
        disabled ? "Chain marked sold out" : "Chain back on sale",
        disabled ? "Requests to it now get a sold-out error." : "It accepts requests again.",
      );
    },
    onError: (error: Error) => toast.error("Couldn't update the chain", error.message),
  });
  const healthByID = useMemo(() => new Map((healthQuery.data?.chains ?? []).map((item) => [item.chain_id, item])), [healthQuery.data]);
  const allChains = chainsQuery.data?.chains ?? [];
  const chains = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    return (chainsQuery.data?.chains ?? []).filter((chain) => {
      const matchesQuery = !normalizedQuery || [chain.name, chain.strategy, ...chain.steps.map((step) => `${step.provider}/${step.model}`)].join(" ").toLowerCase().includes(normalizedQuery);
      const matchesStrategy = matchesStrategyFilter(chain, strategy);
      const matchesHealth = healthFilter === "all" || healthByID.get(chain.id)?.status === healthFilter;
      return matchesQuery && matchesStrategy && matchesHealth;
    });
  }, [chainsQuery.data, healthByID, healthFilter, query, strategy]);
  const strategyCounts = useMemo(() => {
    const list = chainsQuery.data?.chains ?? [];
    return Object.fromEntries(STRATEGY_FILTERS.map((f) => [f.value, list.filter((chain) => matchesStrategyFilter(chain, f.value)).length])) as Record<StrategyFilter, number>;
  }, [chainsQuery.data]);
  const healthCounts = useMemo(() => {
    const list = chainsQuery.data?.chains ?? [];
    return Object.fromEntries(HEALTH_FILTERS.map((f) => [f.value, f.value === "all" ? list.length : list.filter((chain) => healthByID.get(chain.id)?.status === f.value).length])) as Record<HealthFilter, number>;
  }, [chainsQuery.data, healthByID]);
  const filtersActive = Boolean(query || strategy !== "all" || healthFilter !== "all");
  const providers = providersQuery.data?.providers ?? [];
  const isEmpty = !chainsQuery.isLoading && !chainsQuery.isError && allChains.length === 0;

  const copyTarget = async (chain: Chain) => {
    try {
      await navigator.clipboard.writeText(`chain:${chain.name}`);
      toast.success("Chain target copied", `Use chain:${chain.name} as the model.`);
    } catch {
      toast.error("Copy failed", "Your browser did not allow access to the clipboard.");
    }
  };

  const deleteChain = async (chain: Chain) => {
    const ok = await confirmAction({
      title: `Delete ${chain.name}?`,
      description: <>This permanently removes <span className="font-mono text-fg">chain:{chain.name}</span>. Requests that use this target stop resolving immediately.</>,
      confirmLabel: "Delete chain",
      tone: "danger",
    });
    if (ok) deleteMutation.mutate(chain.id);
  };

  const toggleSale = async (chain: Chain) => {
    if (chain.disabled) {
      saleMutation.mutate({ id: chain.id, disabled: false });
      return;
    }
    const ok = await confirmAction({
      title: `Mark ${chain.name} sold out?`,
      description: <>The catalog shows it as sold out and calls to <span className="font-mono text-fg">chain:{chain.name}</span> are refused right away. You can put it back on sale from this menu.</>,
      confirmLabel: "Mark sold out",
      tone: "danger",
    });
    if (ok) saleMutation.mutate({ id: chain.id, disabled: true });
  };

  const clearFilters = () => {
    setQuery("");
    setStrategy("all");
    setHealthFilter("all");
  };

  return (
    <>
      <PageHeader
        title="Chains"
        description="Fallback routes you call as chain:<name>."
        action={isEmpty ? undefined : <Button onClick={() => navigate(dashboard("/chains/new"))}><Plus aria-hidden="true" />New chain</Button>}
      />

      {chainsQuery.isLoading ? (
        <ChainListSkeleton />
      ) : chainsQuery.isError ? (
        <ErrorBanner message="Couldn't load chains. Refresh the page to try again." />
      ) : allChains.length === 0 ? (
        <EmptyState
          variant="card"
          icon={ICONS.chains}
          title="No chains yet"
          hint="A chain tries models in order, so one failing provider doesn't fail the request."
          action={(
            <Button onClick={() => navigate(dashboard("/chains/new"))}>
              <Plus aria-hidden="true" />
              Create chain
            </Button>
          )}
        />
      ) : (
        <div className="space-y-4">
          <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search chains or models"
                aria-label="Search chains, models or providers"
                className="h-9 w-full rounded-lg border border-input bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              />
              {query && (
                <button
                  type="button"
                  onClick={() => setQuery("")}
                  className="absolute right-1.5 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                  aria-label="Clear search"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </div>
            <FilterGroup
              label="Filter by strategy"
              value={strategy}
              onChange={setStrategy}
              options={STRATEGY_FILTERS.map((f) => ({ ...f, count: strategyCounts[f.value] }))}
            />
            <FilterGroup
              label="Filter by health, last 24 hours"
              value={healthFilter}
              onChange={setHealthFilter}
              options={HEALTH_FILTERS.map((f) => ({ ...f, count: f.value === "all" ? undefined : healthCounts[f.value], dot: f.value === "all" ? undefined : statusDot(f.value) }))}
            />
          </div>

          {healthQuery.isError && (
            <div role="status" className="flex items-start gap-2.5 rounded-lg border border-warn/30 bg-warn/5 px-3 py-2 text-[12.5px] leading-5 text-fg">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
              Health data is unavailable right now. Chains still route and can be edited.
            </div>
          )}

          <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
            <div className="border-b border-line px-4 py-3">
              <SectionTitle
                icon={ICONS.chains}
                title={(
                  <>
                    All chains
                    <span className="ml-2 text-[12px] font-normal tabular-nums text-fg-muted">
                      {filtersActive ? `${chains.length} of ${allChains.length}` : allChains.length}
                    </span>
                  </>
                )}
                action={filtersActive ? (
                  <button type="button" onClick={clearFilters} className="min-h-6 rounded-md text-[12.5px] font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
                    Clear filters
                  </button>
                ) : undefined}
              />
              <span className="sr-only" role="status">{filtersActive ? `${chains.length} of ${allChains.length} chains shown` : ""}</span>
            </div>
            {chains.length === 0 ? (
              <EmptyState icon={Search} title="No chains match" hint="Clear the search or a filter to see every chain." />
            ) : (
              <>
                <ul className="divide-y divide-line md:hidden" aria-label="Chains">
                  {chains.map((chain) => (
                    <ChainCard
                      key={chain.id}
                      chain={chain}
                      providers={providers}
                      health={healthByID.get(chain.id)}
                      onOpen={() => navigate(dashboard(`/chains/${chain.id}/edit`))}
                      onCopy={() => copyTarget(chain)}
                      onDelete={() => deleteChain(chain)}
                          onToggleSale={() => toggleSale(chain)}
                    />
                  ))}
                </ul>
                <div className="hidden overflow-x-auto md:block">
                  <table className="w-full min-w-[980px] text-[13px]">
                    <caption className="sr-only">Chains, with health for the last 24 hours</caption>
                    <thead>
                      <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-muted">
                        <th scope="col" className="px-4 py-2 font-medium">Chain</th>
                        <th scope="col" className="px-4 py-2 font-medium">Strategy</th>
                        <th scope="col" className="px-4 py-2 font-medium">Route</th>
                        <th scope="col" className="px-4 py-2 font-medium">Pricing $/M</th>
                        <th scope="col" className="px-4 py-2 font-medium">Health (24h)</th>
                        <th scope="col" className="px-4 py-2 text-right font-medium">Requests</th>
                        <th scope="col" className="px-4 py-2 text-right font-medium">Fallback rate</th>
                        <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-line">
                      {chains.map((chain) => (
                        <ChainRow
                          key={chain.id}
                          chain={chain}
                          providers={providers}
                          health={healthByID.get(chain.id)}
                          onOpen={() => navigate(dashboard(`/chains/${chain.id}/edit`))}
                          onCopy={() => copyTarget(chain)}
                          onDelete={() => deleteChain(chain)}
                          onToggleSale={() => toggleSale(chain)}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </>
  );
}
