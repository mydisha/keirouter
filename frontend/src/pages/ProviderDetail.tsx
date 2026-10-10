import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertCircle,
  AlertTriangle,
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Check,
  ChevronDown,
  Clock3,
  Copy,
  Download,
  Info,
  Layers,
  Loader2,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Plug,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
  X,
} from "lucide-react";
import {
  api,
  type Account,
  type HealthTimelineProvider,
  type Provider,
  type ProviderModel,
  type ProviderRoutingSettings,
  type ProxyPool,
  type UpstreamQuota,
} from "../lib/api";
import { cn } from "@/lib/utils";
import { ModelCapabilityIcons } from "../components/ModelCapabilityIcons";
import { CustomModelsSection } from "../components/CustomModelsSection";
import { ProviderLogo } from "../components/ProviderLogo";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";
import { ConnectProviderDialog, connectOptions, type ConnectMode } from "../components/connect";
import { Badge, Button, Skeleton, TablePagination, Toggle, useClientPagination } from "../components/ui";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../components/ui/tooltip";

type Tab = "accounts" | "models" | "routing";
type TestResult = { status: "testing" | "ok" | "error"; message?: string };

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

// InfoTip holds an explanation that would otherwise be a line of hint text.
function InfoTip({ label, children }: { label: string; children: ReactNode }) {
  return (
    <TooltipProvider delayDuration={150}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            aria-label={label}
            className="inline-flex h-6 w-6 items-center justify-center rounded-md text-fg-faint transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          >
            <Info className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent>{children}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

const TABS: { value: Tab; label: string }[] = [
  { value: "accounts", label: "Accounts" },
  { value: "models", label: "Models" },
  { value: "routing", label: "Routing" },
];

export function ProviderDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab | null) ?? "accounts";
  const setTab = (t: Tab) =>
    setParams((p) => {
      if (t === "accounts") p.delete("tab");
      else p.set("tab", t);
      return p;
    }, { replace: true });
  const [connect, setConnect] = useState<ConnectMode | null>(null);

  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers() });
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts() });
  const oauthProviders = useQuery({ queryKey: ["oauth-providers"], queryFn: () => api.oauthProviders() });
  const models = useQuery({ queryKey: ["provider-models", id], queryFn: () => api.providerModels(id!), enabled: !!id, staleTime: 60_000 });
  const disabledModels = useQuery({ queryKey: ["disabled-models", id], queryFn: () => api.listDisabledModels(id!), enabled: !!id });
  const timeline = useQuery({ queryKey: ["health-timeline", "24h"], queryFn: () => api.healthTimeline("24h", 24), staleTime: 60_000 });

  const provider = providers.data?.providers.find((p) => p.id === id);
  const oauth = oauthProviders.data?.providers.find((p) => p.provider === id);
  const myAccounts = useMemo(
    () => (accounts.data?.accounts ?? []).filter((a) => a.provider === id).sort((a, b) => a.priority - b.priority),
    [accounts.data, id],
  );
  const disabledIds = useMemo(() => new Set(disabledModels.data?.ids ?? []), [disabledModels.data]);
  const modelList = models.data?.models ?? [];
  const enabledModels = Math.max(0, modelList.length - disabledIds.size);
  const health = timeline.data?.providers.find((p) => p.provider === id);

  const deleteProvider = useMutation({
    mutationFn: () => api.deleteCustomProvider(id!),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["providers"] });
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Provider deleted", data?.accounts_disabled ? `${data.accounts_disabled} bound account(s) were disabled.` : undefined);
      navigate("/providers");
    },
    onError: (e: Error) => toast.error("Couldn't delete provider", e.message),
  });

  if (providers.isLoading) {
    return (
      <div className="space-y-4" aria-busy="true" aria-label="Loading provider">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-20 w-full rounded-2xl" />
        <Skeleton className="h-72 w-full rounded-2xl" />
      </div>
    );
  }
  if (!provider) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <h1 className="text-[14px] font-semibold text-fg">Provider not found</h1>
        <p className="mt-1 text-[13px] text-fg-muted">It may have been deleted.</p>
        <Link to="/providers" className="mt-3 inline-block rounded-md text-[13px] font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
          Back to providers
        </Link>
      </div>
    );
  }

  const opts = connectOptions(provider, oauth);
  // A no-credentials provider is "enabled" by its first account; after that
  // the action just adds another.
  if (provider.auth_kind === "none" && myAccounts.length > 0) opts.primaryLabel = "Add account";
  const routePrefix = provider.alias || provider.id;
  // The empty accounts state carries its own connect button; showing the
  // header one too would put two primary actions on screen.
  const emptyAccountsShown = tab === "accounts" && !accounts.isLoading && myAccounts.length === 0;
  const hasMenu = opts.alsoApiKey || opts.bulk || !!provider.custom;

  const removeProvider = async () => {
    const ok = await confirm({
      title: `Delete ${provider.display_name}?`,
      description: "Its custom models are removed and every bound account is disabled, so nothing routes to it anymore. This cannot be undone.",
      confirmLabel: "Delete provider",
      tone: "danger",
    });
    if (ok) deleteProvider.mutate();
  };

  const onTabKeys = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(e.key)) return;
    const i = TABS.findIndex((t) => t.value === tab);
    e.preventDefault();
    const n = e.key === "Home" ? 0 : e.key === "End" ? TABS.length - 1 : (i + (e.key === "ArrowRight" ? 1 : -1) + TABS.length) % TABS.length;
    setTab(TABS[n].value);
    document.getElementById(`provider-tab-${TABS[n].value}`)?.focus();
  };
  const counts: Partial<Record<Tab, number>> = { accounts: myAccounts.length, models: enabledModels };

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 flex items-center gap-1.5 text-[13px] text-fg-muted">
        <Link to="/providers" className="inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Providers
        </Link>
        <span aria-hidden="true" className="text-fg-faint">/</span>
        <span className="truncate text-fg" aria-current="page">
          {provider.display_name}
        </span>
      </nav>

      <header className="mb-5 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-center gap-3">
          <ProviderLogo icon={provider.icon} name={provider.display_name} size={40} className="rounded-lg" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{provider.display_name}</h1>
              {provider.custom && <Badge tone="neutral">Custom</Badge>}
              {provider.deprecated && <Badge tone="warning">Unofficial</Badge>}
            </div>
            <p className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
              <span className="font-mono text-[12.5px]">{provider.id}</span>
              <Dot />
              <span>{provider.auth_kind === "none" ? "No credentials" : opts.primaryIsSignIn ? "Sign-in" : "API key"}</span>
              {provider.custom && provider.base_url && (
                <>
                  <Dot />
                  <span>{provider.dialect === "anthropic" ? "Anthropic-compatible" : "OpenAI-compatible"}</span>
                  <Dot />
                  <span className="max-w-[22rem] truncate font-mono text-[12.5px]" title={provider.base_url}>
                    {provider.base_url}
                  </span>
                </>
              )}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!emptyAccountsShown && (
            <Button onClick={() => setConnect("primary")}>
              <Plug aria-hidden="true" />
              {opts.primaryLabel}
            </Button>
          )}
          {hasMenu && (
            <DropdownMenu>
              <DropdownMenuTrigger
                aria-label="More provider actions"
                className="flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {opts.alsoApiKey && (
                  <DropdownMenuItem onSelect={() => setConnect("api_key")}>
                    <Plus />
                    Add API key
                  </DropdownMenuItem>
                )}
                {opts.bulk && (
                  <DropdownMenuItem onSelect={() => setConnect("bulk")}>
                    <Layers />
                    {provider.id === "qoder" ? "Import tokens" : "Import keys"}
                  </DropdownMenuItem>
                )}
                {provider.custom && (
                  <>
                    {(opts.alsoApiKey || opts.bulk) && <DropdownMenuSeparator />}
                    <DropdownMenuItem tone="danger" onSelect={removeProvider}>
                      <Trash2 />
                      Delete custom provider
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      </header>

      {provider.deprecated && provider.notice && (
        <div role="note" className="mb-5 flex items-start gap-2.5 rounded-2xl border border-warn/30 bg-warn/5 px-4 py-3 text-[13px] leading-5 text-fg">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" aria-hidden="true" />
          <span>{provider.notice}</span>
        </div>
      )}

      <ProviderSummary provider={provider} health={health} />

      <div className="mb-5 mt-6 flex gap-1 overflow-x-auto border-b border-line" role="tablist" aria-label={`${provider.display_name} sections`} onKeyDown={onTabKeys}>
        {TABS.map(({ value, label }) => {
          const selected = tab === value;
          const count = counts[value];
          return (
            <button
              key={value}
              id={`provider-tab-${value}`}
              type="button"
              role="tab"
              aria-selected={selected}
              aria-controls={`provider-panel-${value}`}
              tabIndex={selected ? 0 : -1}
              onClick={() => setTab(value)}
              className={cn(
                "relative -mb-px inline-flex items-center gap-1.5 whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                selected ? "text-fg" : "text-fg-muted hover:text-fg",
              )}
            >
              {label}
              {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{count}</span>}
              {selected && <span aria-hidden="true" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
            </button>
          );
        })}
      </div>

      <div role="tabpanel" id={`provider-panel-${tab}`} aria-labelledby={`provider-tab-${tab}`}>
        {tab === "accounts" && (
          <AccountsPanel provider={provider} accounts={myAccounts} loading={accounts.isLoading} onConnect={() => setConnect("primary")} connectLabel={opts.primaryLabel} />
        )}
        {tab === "models" && (
          <ModelsPanel provider={provider} models={modelList} loading={models.isLoading} disabledIds={disabledIds} routePrefix={routePrefix} />
        )}
        {tab === "routing" && <RoutingPanel providerId={provider.id} accountCount={myAccounts.length} />}
      </div>

      {connect && <ConnectProviderDialog provider={provider} oauth={oauth} mode={connect} onClose={() => setConnect(null)} />}
    </>
  );
}

function Dot() {
  return (
    <span aria-hidden="true" className="text-fg-faint">
      ·
    </span>
  );
}

// ── Summary strip ────────────────────────────────────────────────────────────

const TICK_CLASS: Record<string, string> = { ok: "bg-ok/70", degraded: "bg-warn", down: "bg-bad", idle: "bg-track" };

function ProviderSummary({ provider, health }: { provider: Provider; health?: HealthTimelineProvider }) {
  const cells = [
    {
      label: "Requests · 24h",
      value: health ? health.requests.toLocaleString("en-US") : "—",
      hint: health && health.fallbacks ? `${health.fallbacks.toLocaleString("en-US")} fell over` : undefined,
    },
    {
      label: "Success",
      value: health && health.requests ? `${(health.success_rate * 100).toFixed(1)}%` : "—",
      tone: health && health.requests ? (health.success_rate >= 0.99 ? undefined : health.success_rate >= 0.95 ? "warn" : "bad") : undefined,
    },
    { label: "Worst p95", value: health && health.worst_p95_ms ? fmtMs(health.worst_p95_ms) : "—" },
  ] as { label: string; value: string; hint?: string; tone?: "warn" | "bad" }[];
  const badHours = health ? health.buckets.filter((b) => b.status === "degraded" || b.status === "down").length : 0;

  return (
    <section aria-label="Last 24 hours" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="grid grid-cols-2 gap-px bg-line sm:grid-cols-3 lg:grid-cols-[repeat(3,minmax(0,1fr))_minmax(0,1.6fr)]">
        {cells.map((c) => (
          <div key={c.label} className="bg-surface px-4 py-3">
            <p className="text-[12px] font-medium text-fg-muted">{c.label}</p>
            <p className={cn("mt-1 text-[18px] font-semibold tracking-[-0.01em] tabular-nums", c.tone === "bad" ? "text-bad" : c.tone === "warn" ? "text-warn" : "text-fg")}>
              {c.value === "—" ? (
                <>
                  <span aria-hidden="true">—</span>
                  <span className="sr-only">No data</span>
                </>
              ) : (
                c.value
              )}
            </p>
            {c.hint && <p className="mt-0.5 text-[12px] tabular-nums text-warn">{c.hint}</p>}
          </div>
        ))}
        <div className="col-span-2 flex flex-col justify-center gap-2 bg-surface px-4 py-3 sm:col-span-3 lg:col-span-1">
          <div className="flex items-center justify-between gap-2">
            <p className="text-[12px] font-medium text-fg-muted">Hourly health</p>
            <Link
              to={`/provider-health/${encodeURIComponent(provider.id)}`}
              className="inline-flex min-h-6 items-center rounded-md text-[12px] font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              aria-label={`${provider.display_name} health details`}
            >
              Details
            </Link>
          </div>
          <div>
            {health ? (
              <div
                className="flex gap-[2px]"
                role="img"
                aria-label={badHours ? `${badHours} of ${health.buckets.length} hours had errors` : `No errors in ${health.buckets.length} hours`}
              >
                {health.buckets.map((b) => (
                  <span key={b.start} className={cn("h-4 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")} />
                ))}
              </div>
            ) : (
              <p className="text-[12.5px] text-fg-faint">No traffic yet</p>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

// ── Accounts ─────────────────────────────────────────────────────────────────

const checkboxClass = "h-4 w-4 shrink-0 rounded border-input accent-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

function AccountsPanel({
  provider,
  accounts,
  loading,
  onConnect,
  connectLabel,
}: {
  provider: Provider;
  accounts: Account[];
  loading: boolean;
  onConnect: () => void;
  connectLabel: string;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const pools = useQuery({ queryKey: ["proxy-pools"], queryFn: () => api.listProxyPools() });
  const [tests, setTests] = useState<Record<string, TestResult>>({});
  const [testingAll, setTestingAll] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const pagination = useClientPagination(accounts, 10);
  const { page, pages, paged, setPage, total } = pagination;
  const pageStart = (page - 1) * 10;

  useEffect(() => {
    setPage(1);
    setSelected(new Set());
  }, [provider.id, setPage]);

  const update = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: { label?: string; priority?: number; disabled?: boolean; proxy_pool_id?: string } }) => api.updateAccount(id, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts"] }),
    onError: (e: Error) => toast.error("Couldn't update the account", e.message),
  });
  const bulkUpdate = useMutation({
    mutationFn: async ({ ids, disabled }: { ids: string[]; disabled: boolean }) => {
      await Promise.all(ids.map((id) => api.updateAccount(id, { disabled })));
    },
    onSuccess: (_, { ids, disabled }) => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      setSelected(new Set());
      toast.success(`${ids.length} account${ids.length > 1 ? "s" : ""} ${disabled ? "paused" : "resumed"}`);
    },
    onError: (e: Error) => toast.error("Bulk update failed", e.message),
  });
  const removeMany = useMutation({
    mutationFn: async (ids: string[]) => {
      await Promise.all(ids.map((id) => api.deleteAccount(id)));
    },
    onSuccess: (_, ids) => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      setSelected(new Set());
      toast.success(`${ids.length} account${ids.length > 1 ? "s" : ""} removed`, "Their encrypted credentials were purged.");
    },
    onError: (e: Error) => toast.error("Couldn't remove accounts", e.message),
  });

  const runTest = async (accountId: string) => {
    setTests((t) => ({ ...t, [accountId]: { status: "testing" } }));
    try {
      const res = await api.testAccount(accountId);
      const ok = res.status === "ok";
      setTests((t) => ({ ...t, [accountId]: { status: ok ? "ok" : "error", message: res.message } }));
      if (!ok) qc.invalidateQueries({ queryKey: ["accounts"] });
      return ok;
    } catch (e) {
      setTests((t) => ({ ...t, [accountId]: { status: "error", message: (e as Error).message } }));
      qc.invalidateQueries({ queryKey: ["accounts"] });
      return false;
    }
  };

  // Accounts are tested one at a time so a provider's rate limit isn't hit.
  const testAll = async () => {
    if (testingAll || !accounts.length) return;
    setTestingAll(true);
    let failed = 0;
    for (const a of accounts) if (!(await runTest(a.id))) failed += 1;
    setTestingAll(false);
    if (failed === 0) toast.success("All accounts passed", `${accounts.length} credential${accounts.length === 1 ? "" : "s"} verified.`);
    else toast.error("Some accounts failed", `${accounts.length - failed} passed, ${failed} failed.`);
  };

  // Moving swaps priorities with the neighbour, optimistically.
  const move = (accId: string, dir: -1 | 1) => {
    const i = accounts.findIndex((a) => a.id === accId);
    const j = i + dir;
    if (i < 0 || j < 0 || j >= accounts.length) return;
    const a = accounts[i];
    const b = accounts[j];
    qc.setQueryData<{ accounts: Account[] }>(["accounts"], (old) =>
      old
        ? { accounts: old.accounts.map((x) => (x.id === a.id ? { ...x, priority: b.priority } : x.id === b.id ? { ...x, priority: a.priority } : x)) }
        : old,
    );
    update.mutate({ id: a.id, patch: { priority: b.priority } });
    update.mutate({ id: b.id, patch: { priority: a.priority } }, { onSettled: () => qc.invalidateQueries({ queryKey: ["accounts"] }) });
  };

  const removeOne = async (a: Account) => {
    const ok = await confirm({
      title: `Remove ${a.label || provider.display_name}?`,
      description: "The account stops receiving traffic and its encrypted credentials are purged. This cannot be undone.",
      confirmLabel: "Remove account",
      tone: "danger",
    });
    if (!ok) return;
    try {
      await api.deleteAccount(a.id);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Account removed");
    } catch (e) {
      toast.error("Couldn't remove the account", (e as Error).message);
    }
  };

  const selectedList = accounts.filter((a) => selected.has(a.id));
  const allOnPage = paged.length > 0 && paged.every((a) => selected.has(a.id));
  const someOnPage = paged.some((a) => selected.has(a.id));
  const busy = bulkUpdate.isPending || removeMany.isPending;
  const needsReconnect = accounts.filter((a) => a.needs_reconnect).length;
  const tested = Object.values(tests).filter((t) => t.status !== "testing").length;

  if (loading) return <Skeleton className="h-64 w-full rounded-2xl" />;

  if (accounts.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <h2 className="text-[14px] font-semibold text-fg">No accounts yet</h2>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">Add one to start routing to {provider.display_name}.</p>
        <Button className="mt-4" onClick={onConnect}>
          <Plug aria-hidden="true" />
          {connectLabel}
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {needsReconnect > 0 && (
        <div className="flex items-start gap-2.5 rounded-2xl border border-warn/30 bg-warn/5 px-4 py-3 text-[13px] leading-5 text-fg">
          <RefreshCw className="mt-0.5 h-4 w-4 shrink-0 text-warn" aria-hidden="true" />
          <span>
            {needsReconnect} account{needsReconnect === 1 ? "'s sign-in was" : "s' sign-ins were"} revoked. Remove {needsReconnect === 1 ? "it" : "them"} and connect again.
          </span>
        </div>
      )}

      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="flex min-h-12 flex-wrap items-center gap-2 border-b border-line px-4 py-2">
          <p className="text-[12.5px] text-fg-muted" role="status" aria-live="polite">
            {testingAll ? (
              <span className="text-fg">
                Testing {tested} of {accounts.length}…
              </span>
            ) : selectedList.length > 0 ? (
              <span className="font-medium text-fg">{selectedList.length} selected</span>
            ) : (
              <span className="tabular-nums">
                {accounts.length} account{accounts.length === 1 ? "" : "s"}
              </span>
            )}
          </p>
          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            {selectedList.length > 0 ? (
              <>
                <Button variant="ghost" disabled={busy || !selectedList.some((a) => a.disabled)} onClick={() => bulkUpdate.mutate({ ids: selectedList.filter((a) => a.disabled).map((a) => a.id), disabled: false })}>
                  <Play aria-hidden="true" />
                  Resume
                </Button>
                <Button variant="ghost" disabled={busy || !selectedList.some((a) => !a.disabled)} onClick={() => bulkUpdate.mutate({ ids: selectedList.filter((a) => !a.disabled).map((a) => a.id), disabled: true })}>
                  <Pause aria-hidden="true" />
                  Pause
                </Button>
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={async () => {
                    const ok = await confirm({
                      title: `Remove ${selectedList.length} account${selectedList.length > 1 ? "s" : ""}?`,
                      description: "They stop receiving traffic and their encrypted credentials are purged. This cannot be undone.",
                      confirmLabel: "Remove",
                      tone: "danger",
                    });
                    if (ok) removeMany.mutate(selectedList.map((a) => a.id));
                  }}
                >
                  <Trash2 aria-hidden="true" />
                  Remove
                </Button>
                <button
                  type="button"
                  onClick={() => setSelected(new Set())}
                  aria-label="Clear selection"
                  className="flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                >
                  <X className="h-4 w-4" aria-hidden="true" />
                </button>
              </>
            ) : (
              <Button variant="ghost" onClick={testAll} disabled={testingAll}>
                {testingAll ? <Loader2 className="animate-spin" aria-hidden="true" /> : <ShieldCheck aria-hidden="true" />}
                {testingAll ? "Testing…" : "Test all"}
              </Button>
            )}
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-[13px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th scope="col" className="w-10 py-2 pl-4 pr-1">
                  <input
                    type="checkbox"
                    aria-label="Select all accounts on this page"
                    className={checkboxClass}
                    checked={allOnPage}
                    ref={(el) => {
                      if (el) el.indeterminate = someOnPage && !allOnPage;
                    }}
                    onChange={() =>
                      setSelected((prev) => {
                        const next = new Set(prev);
                        if (allOnPage) paged.forEach((a) => next.delete(a.id));
                        else paged.forEach((a) => next.add(a.id));
                        return next;
                      })
                    }
                  />
                </th>
                <th scope="col" className="px-3 py-2 font-medium">Account</th>
                <th scope="col" className="px-3 py-2 font-medium">Status</th>
                <th scope="col" className="px-3 py-1 font-medium">
                  <span className="inline-flex items-center gap-0.5">
                    Priority
                    <InfoTip label="About priority">Lower numbers are tried first.</InfoTip>
                  </span>
                </th>
                <th scope="col" className="px-3 py-2 font-medium">Enabled</th>
                <th scope="col" className="w-20 px-3 py-2">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            {paged.map((a, i) => (
              <AccountRow
                key={a.id}
                account={a}
                index={pageStart + i}
                total={accounts.length}
                pools={pools.data?.pools ?? []}
                selected={selected.has(a.id)}
                test={tests[a.id]}
                batchTesting={testingAll}
                onToggleSelect={() =>
                  setSelected((prev) => {
                    const next = new Set(prev);
                    if (next.has(a.id)) next.delete(a.id);
                    else next.add(a.id);
                    return next;
                  })
                }
                onMove={(dir) => move(a.id, dir)}
                onPatch={(patch) => update.mutate({ id: a.id, patch })}
                onTest={() => runTest(a.id)}
                onRemove={() => removeOne(a)}
              />
            ))}
          </table>
        </div>
        {pages > 1 && <TablePagination page={page} pages={pages} total={total} onPage={setPage} />}
      </div>
    </div>
  );
}

function AccountRow({
  account: a,
  index,
  total,
  pools,
  selected,
  test,
  batchTesting,
  onToggleSelect,
  onMove,
  onPatch,
  onTest,
  onRemove,
}: {
  account: Account;
  index: number;
  total: number;
  pools: ProxyPool[];
  selected: boolean;
  test?: TestResult;
  batchTesting: boolean;
  onToggleSelect: () => void;
  onMove: (dir: -1 | 1) => void;
  onPatch: (patch: { label?: string; priority?: number; proxy_pool_id?: string; disabled?: boolean }) => void;
  onTest: () => void;
  onRemove: () => void;
}) {
  const [priority, setPriority] = useState(String(a.priority));
  const [renaming, setRenaming] = useState(false);
  const [label, setLabel] = useState(a.label);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const labelRef = useRef<HTMLInputElement>(null);
  const renameRequested = useRef(false);
  const detailsId = useId();
  const egressId = useId();
  useEffect(() => setPriority(String(a.priority)), [a.priority]);
  useEffect(() => setLabel(a.label), [a.label]);
  useEffect(() => {
    if (renaming) {
      labelRef.current?.focus();
      labelRef.current?.select();
    }
  }, [renaming]);

  const supportsQuota = a.provider === "kiro" || a.provider === "qoder";
  const quota = useQuery({
    queryKey: ["account-quota", a.id],
    queryFn: () => api.accountQuota(a.id),
    enabled: supportsQuota && detailsOpen && !a.disabled,
    staleTime: 60_000,
    retry: 1,
  });
  const name = a.label || a.provider;
  const pool = pools.find((p) => p.id === a.proxy_pool_id);
  const testing = test?.status === "testing";

  const commitPriority = () => {
    const v = parseInt(priority, 10);
    if (!Number.isNaN(v) && v >= 0 && v !== a.priority) onPatch({ priority: v });
    else setPriority(String(a.priority));
  };
  const commitLabel = () => {
    setRenaming(false);
    if (label.trim() !== a.label) onPatch({ label: label.trim() });
  };

  const status: { tone: "ok" | "warn" | "neutral"; text: string } = a.needs_reconnect
    ? { tone: "warn", text: "Reconnect needed" }
    : a.disabled
      ? { tone: "neutral", text: "Paused" }
      : { tone: "ok", text: "Active" };
  const rowTone = selected ? "bg-accent-500/5" : a.disabled ? "bg-subtle/60" : "";
  const cell = "px-3 py-2.5 align-middle";

  return (
    <tbody className={cn("border-b border-line last:border-b-0", rowTone)}>
      <tr className={cn("transition-colors", !selected && "hover:bg-hover/60")}>
        <td className="py-2.5 pl-4 pr-1 align-middle">
          <input type="checkbox" checked={selected} onChange={onToggleSelect} aria-label={`Select ${name}`} className={checkboxClass} />
        </td>
        <td className={cn(cell, "max-w-[280px]")}>
          {renaming ? (
            <input
              ref={labelRef}
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              onBlur={commitLabel}
              onKeyDown={(e) => {
                if (e.key === "Enter") commitLabel();
                if (e.key === "Escape") {
                  setLabel(a.label);
                  setRenaming(false);
                }
              }}
              aria-label={`Label for ${name}`}
              className="h-8 w-full max-w-56 rounded-lg border border-accent-500 bg-surface px-2 text-[13px] text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            />
          ) : (
            <span className="block truncate font-medium text-fg" title={name}>
              {name}
            </span>
          )}
          <span className="mt-0.5 block truncate text-[12px] text-fg-muted">
            {a.auth_kind === "oauth" ? "Signed in" : a.auth_kind === "none" ? "No credentials" : "API key"}
            {pool && (
              <>
                {" · via "}
                <span className={pool.test_status === "error" ? "text-bad" : undefined}>
                  {pool.name}
                  {pool.test_status === "error" ? " (failing)" : ""}
                </span>
              </>
            )}
          </span>
        </td>
        <td className={cn(cell, "whitespace-nowrap")}>
          <span className="inline-flex items-center gap-1.5 text-[12.5px]">
            <span aria-hidden="true" className={cn("h-1.5 w-1.5 rounded-full", status.tone === "warn" ? "bg-warn" : status.tone === "neutral" ? "bg-fg-faint" : "bg-ok")} />
            <span className={status.tone === "warn" ? "text-warn" : status.tone === "neutral" ? "text-fg-muted" : "text-fg"}>{status.text}</span>
          </span>
          <span className="block text-[12px]" role="status" aria-live="polite">
            {testing && (
              <span className="inline-flex items-center gap-1 text-fg-muted">
                <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
                Testing…
              </span>
            )}
            {test?.status === "ok" && (
              <span className="inline-flex items-center gap-1 text-ok">
                <Check className="h-3 w-3" aria-hidden="true" />
                Verified
              </span>
            )}
            {test?.status === "error" && <span className="text-bad">Test failed</span>}
          </span>
        </td>
        <td className={cell}>
          <div className="inline-flex h-8 items-center overflow-hidden rounded-lg border border-input bg-surface">
            <button
              type="button"
              onClick={() => onMove(-1)}
              disabled={index === 0}
              aria-label={`Move ${name} up`}
              className="flex h-full w-7 items-center justify-center text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500 disabled:opacity-30"
            >
              <ArrowUp className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
            <input
              value={priority}
              inputMode="numeric"
              onChange={(e) => setPriority(e.target.value.replace(/[^0-9]/g, ""))}
              onBlur={commitPriority}
              onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
              aria-label={`Priority for ${name}`}
              className="h-full w-11 border-x border-input bg-transparent text-center font-mono text-[12.5px] text-fg focus:bg-subtle focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
            />
            <button
              type="button"
              onClick={() => onMove(1)}
              disabled={index === total - 1}
              aria-label={`Move ${name} down`}
              className="flex h-full w-7 items-center justify-center text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500 disabled:opacity-30"
            >
              <ArrowDown className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </div>
        </td>
        <td className={cell}>
          <Toggle checked={!a.disabled} onChange={(on) => onPatch({ disabled: !on })} label={`Enable ${name}`} />
        </td>
        <td className={cn(cell, "text-right")}>
          <div className="inline-flex items-center gap-0.5">
            <button
              type="button"
              onClick={() => setDetailsOpen((o) => !o)}
              aria-expanded={detailsOpen}
              aria-controls={detailsId}
              aria-label={`Details for ${name}`}
              className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <ChevronDown className={cn("h-4 w-4 transition-transform", detailsOpen && "rotate-180")} aria-hidden="true" />
            </button>
            <DropdownMenu>
              <DropdownMenuTrigger
                aria-label={`Actions for ${name}`}
                className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="end"
                onCloseAutoFocus={(e) => {
                  // Keep focus in the rename field instead of the menu trigger.
                  if (renameRequested.current) {
                    e.preventDefault();
                    renameRequested.current = false;
                  }
                }}
              >
                <DropdownMenuItem onSelect={onTest} disabled={testing || batchTesting}>
                  <ShieldCheck />
                  Test connection
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => {
                    renameRequested.current = true;
                    setRenaming(true);
                  }}
                >
                  <Pencil />
                  Rename
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem tone="danger" onSelect={onRemove}>
                  <Trash2 />
                  Remove account
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </td>
      </tr>

      {test?.status === "error" && test.message && (
        <tr>
          <td />
          <td colSpan={5} className="px-3 pb-2.5 pt-0">
            <p className="flex items-start gap-1.5 break-words text-[12.5px] leading-5 text-bad">
              <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              {test.message}
            </p>
          </td>
        </tr>
      )}

      <tr id={detailsId} hidden={!detailsOpen}>
        <td />
        <td colSpan={5} className="px-3 pb-3 pt-0">
          {detailsOpen && (
            <div className="space-y-2.5">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                <label htmlFor={egressId} className="text-[12.5px] font-medium text-fg">
                  Egress
                </label>
                <select
                  id={egressId}
                  value={a.proxy_pool_id || ""}
                  onChange={(e) => onPatch({ proxy_pool_id: e.target.value })}
                  className={cn(
                    "h-8 w-56 rounded-lg border bg-surface px-2 text-[12.5px] text-fg focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                    pool?.test_status === "error" ? "border-bad" : "border-input",
                  )}
                >
                  <option value="">Direct connection</option>
                  {pools.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                      {!p.is_active ? " (inactive)" : ""}
                    </option>
                  ))}
                </select>
                {pool && (
                  <span className={cn("text-[12px]", pool.test_status === "error" ? "text-bad" : "text-fg-muted")}>
                    {pool.test_status === "active" ? "Proxy healthy" : pool.test_status === "error" ? "Proxy failing" : "Proxy untested"}
                  </span>
                )}
              </div>
              {supportsQuota && (
                <AccountQuotaPanel
                  loading={quota.isFetching}
                  error={quota.error instanceof Error ? quota.error.message : ""}
                  planName={quota.data?.plan_name}
                  message={quota.data?.message}
                  quotas={quota.data?.quotas ?? []}
                  disabled={a.disabled}
                  onRefresh={() => quota.refetch()}
                />
              )}
              {a.provider === "codex" && <CodexResetCreditsSection accountId={a.id} />}
            </div>
          )}
        </td>
      </tr>
    </tbody>
  );
}

function AccountQuotaPanel({
  loading,
  error,
  planName,
  message,
  quotas,
  disabled,
  onRefresh,
}: {
  loading: boolean;
  error: string;
  planName?: string;
  message?: string;
  quotas: UpstreamQuota[];
  disabled: boolean;
  onRefresh: () => void;
}) {
  return (
    <div className="rounded-xl border border-line bg-subtle p-3">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-[13px] font-medium text-fg">{planName || "Plan"}</h3>
        <button type="button" onClick={onRefresh} disabled={loading || disabled} aria-label="Refresh usage" className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-40">
          <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} aria-hidden="true" />
        </button>
      </div>
      {disabled ? (
        <p className="mt-2 text-[12.5px] text-fg-muted">Resume the account to refresh its usage.</p>
      ) : loading && quotas.length === 0 ? (
        <div className="mt-3 grid gap-2 sm:grid-cols-2" aria-busy="true">
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
        </div>
      ) : error ? (
        <p className="mt-2 text-[12.5px] text-bad">{error}</p>
      ) : quotas.length > 0 ? (
        <div className="mt-3 grid gap-2 sm:grid-cols-2">
          {quotas.map((q) => (
            <QuotaBar key={q.resource_type} quota={q} />
          ))}
        </div>
      ) : (
        <p className="mt-2 text-[12.5px] text-fg-muted">{message || "No allowance reported for this account."}</p>
      )}
      {message && quotas.length > 0 && <p className="mt-2 text-[12px] text-fg-muted">{message}</p>}
    </div>
  );
}

function QuotaBar({ quota: q }: { quota: UpstreamQuota }) {
  const used = q.limit > 0 ? Math.min(100, Math.round((q.used / q.limit) * 100)) : 0;
  const fill = used >= 70 ? (used >= 90 ? "bg-bad" : "bg-warn") : "bg-accent-500";
  const label = q.resource_type.toLowerCase().replace(/_/g, " ").replace(/^\w/, (c) => c.toUpperCase());
  const reset = q.reset_at ? (/^\d+$/.test(q.reset_at) ? new Date(Number(q.reset_at) * (Number(q.reset_at) > 10_000_000_000 ? 1 : 1000)) : new Date(q.reset_at)) : null;
  return (
    <div className="rounded-lg border border-line bg-surface p-2.5">
      <div className="flex items-baseline justify-between gap-2 text-[12.5px]">
        <span className="font-medium text-fg">{label}</span>
        <span className="tabular-nums text-fg">{q.remaining.toLocaleString()} left</span>
      </div>
      <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-track" role="progressbar" aria-label={`${label} used`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={used}>
        <div className={cn("h-full rounded-full", fill)} style={{ width: `${used}%` }} />
      </div>
      <div className="mt-1.5 flex flex-wrap justify-between gap-1 text-[11.5px] tabular-nums text-fg-faint">
        <span>
          {q.used.toLocaleString()} of {q.limit.toLocaleString()} used
        </span>
        {reset && !Number.isNaN(reset.getTime()) && (
          <span className="inline-flex items-center gap-1">
            <Clock3 className="h-3 w-3" aria-hidden="true" />
            Resets {reset.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
          </span>
        )}
      </div>
    </div>
  );
}

function CodexResetCreditsSection({ accountId }: { accountId: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const details = useQuery({ queryKey: ["codex-usage-details", accountId], queryFn: () => api.codexUsageDetails(accountId), staleTime: 30_000, retry: 1 });
  const consume = useMutation({
    mutationFn: (creditId?: string) => api.codexConsumeCredit(accountId, creditId),
    onSuccess: async (r) => {
      if (r.ok) {
        toast.success("Usage limits reset", "A reset credit was used.");
        await details.refetch();
        qc.invalidateQueries({ queryKey: ["quota"] });
      } else toast.error(r.no_credit ? "No credits available" : "Limits were not reset", r.message || undefined);
    },
    onError: (e: Error) => toast.error("Limits were not reset", e.message),
  });
  const data = details.data;
  const credits = data?.reset_credits?.credits.filter((c) => c.status === "available") ?? [];
  const available = data?.reset_credits?.available_count ?? data?.usage_data?.reset_credits_available ?? 0;
  const nextExpiry = credits.map((c) => c.expires_at).filter((v): v is string => !!v).sort()[0];

  return (
    <div className="rounded-xl border border-line bg-subtle p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <h3 className="text-[13px] font-medium text-fg">Codex limits</h3>
          {data?.usage_data?.plan_type && <Badge tone={data.usage_data.limit_reached ? "danger" : "neutral"}>{data.usage_data.plan_type}</Badge>}
        </div>
        <button type="button" onClick={() => details.refetch()} disabled={details.isFetching} aria-label="Refresh Codex usage" className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-40">
          <RefreshCw className={cn("h-4 w-4", details.isFetching && "animate-spin")} aria-hidden="true" />
        </button>
      </div>
      {details.isLoading ? (
        <div className="mt-3 grid gap-2 sm:grid-cols-2" aria-busy="true">
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </div>
      ) : details.error ? (
        <p className="mt-2 text-[12.5px] text-bad">{details.error instanceof Error ? details.error.message : "Couldn't load Codex usage."}</p>
      ) : data ? (
        <div className="mt-3 space-y-2">
          {data.error && <p className="text-[12.5px] text-warn">{data.error}</p>}
          {data.usage_data && (
            <div className="grid gap-2 sm:grid-cols-2">
              <LimitWindow label="5-hour window" used={data.usage_data.primary_used_percent} resetAt={data.usage_data.primary_reset_at} />
              <LimitWindow label="Weekly window" used={data.usage_data.secondary_used_percent} resetAt={data.usage_data.secondary_reset_at} />
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface px-3 py-2.5">
            <div className="flex flex-wrap gap-x-5 gap-y-1 text-[12.5px]">
              <span className="text-fg-muted">
                Credits <span className="font-medium tabular-nums text-fg">{data.usage_data?.unlimited ? "Unlimited" : data.usage_data?.credits_balance || "0"}</span>
              </span>
              <span className="text-fg-muted">
                Earned resets <span className="font-medium tabular-nums text-fg">{available}</span>
              </span>
              {nextExpiry && <span className="text-fg-faint">Next expires {new Date(nextExpiry).toLocaleDateString()}</span>}
            </div>
            {available > 0 && (
              <Button
                variant="secondary"
                disabled={consume.isPending}
                onClick={async () => {
                  if (await confirm({ title: "Use a reset credit?", description: "Both usage windows reset now. Earned credits are limited.", confirmLabel: "Reset limits" })) consume.mutate(credits[0]?.id);
                }}
              >
                {consume.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
                Reset limits
              </Button>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function LimitWindow({ label, used, resetAt }: { label: string; used: number; resetAt: number }) {
  const pct = Math.min(100, Math.max(0, used));
  const fill = pct >= 80 ? "bg-bad" : pct >= 50 ? "bg-warn" : "bg-accent-500";
  return (
    <div className="rounded-lg border border-line bg-surface p-2.5">
      <div className="flex items-baseline justify-between gap-2 text-[12.5px]">
        <span className="font-medium text-fg">{label}</span>
        <span className="tabular-nums text-fg">{100 - pct}% left</span>
      </div>
      <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-track" role="progressbar" aria-label={`${label} used`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
        <div className={cn("h-full rounded-full", fill)} style={{ width: `${pct}%` }} />
      </div>
      <p className="mt-1.5 text-[11.5px] text-fg-faint">{resetAt > 0 ? `Resets ${new Date(resetAt * 1000).toLocaleString()}` : "Reset time unavailable"}</p>
    </div>
  );
}

// ── Models ───────────────────────────────────────────────────────────────────

type ModelFilter = "all" | "enabled" | "disabled";

function ModelsPanel({
  provider,
  models,
  loading,
  disabledIds,
  routePrefix,
}: {
  provider: Provider;
  models: ProviderModel[];
  loading: boolean;
  disabledIds: Set<string>;
  routePrefix: string;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [view, setView] = useState<"catalog" | "custom">("catalog");
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<ModelFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const setDisabled = useMutation({
    mutationFn: ({ ids, disable }: { ids: string[]; disable: boolean }) => (disable ? api.disableModels(provider.id, ids) : api.enableModels(provider.id, ids)),
    onSuccess: (_, { ids, disable }) => {
      qc.invalidateQueries({ queryKey: ["disabled-models", provider.id] });
      if (ids.length > 1) toast.success(`${ids.length} models ${disable ? "disabled" : "enabled"}`);
    },
    onError: (e: Error) => toast.error("Couldn't update models", e.message),
  });
  const importModels = useMutation({
    mutationFn: () => api.importModels(provider.id),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["provider-models", provider.id] });
      qc.invalidateQueries({ queryKey: ["custom-models", provider.id] });
      toast.success(
        "Sync complete",
        res.imported > 0 ? `Imported ${res.imported} model${res.imported === 1 ? "" : "s"}${res.skipped ? ` (${res.skipped} already present)` : ""}.` : res.total > 0 ? `All ${res.total} models were already registered.` : "The endpoint returned no models.",
      );
    },
    onError: (e: Error) => toast.error("Couldn't sync models", e.message),
  });

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return models.filter((m) => {
      if (filter === "enabled" && disabledIds.has(m.id)) return false;
      if (filter === "disabled" && !disabledIds.has(m.id)) return false;
      return !q || m.id.toLowerCase().includes(q) || (m.name ?? "").toLowerCase().includes(q) || (m.kind ?? "").toLowerCase().includes(q);
    });
  }, [models, query, filter, disabledIds]);
  const { page, pages, paged, setPage, total } = useClientPagination(filtered, 25);
  useEffect(() => setPage(1), [query, filter, setPage]);

  const allFiltered = filtered.length > 0 && filtered.every((m) => selected.has(m.id));
  const someFiltered = filtered.some((m) => selected.has(m.id));

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Model source" onKeyDown={onRadioKeys}>
          {(["catalog", "custom"] as const).map((v) => (
            <button
              key={v}
              type="button"
              role="radio"
              aria-checked={view === v}
              tabIndex={view === v ? 0 : -1}
              onClick={() => setView(v)}
              className={cn(
                "h-full rounded-lg px-3 text-[12.5px] font-medium focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                view === v ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
              )}
            >
              {v === "catalog" ? `Catalog · ${models.length}` : "Custom models"}
            </button>
          ))}
        </div>
        {view === "catalog" && provider.custom && (
          <Button variant="secondary" onClick={() => importModels.mutate()} disabled={importModels.isPending}>
            {importModels.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Download aria-hidden="true" />}
            {importModels.isPending ? "Syncing…" : "Sync from /models"}
          </Button>
        )}
      </div>

      {view === "custom" ? (
        <CustomModelsSection provider={provider} />
      ) : loading ? (
        <Skeleton className="h-72 w-full rounded-2xl" />
      ) : models.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <h2 className="text-[14px] font-semibold text-fg">No models yet</h2>
          <p className="mt-1 text-[13px] text-fg-muted">
            {provider.custom ? "Sync the endpoint's /models list, or add them under Custom models." : "Add them under Custom models."}
          </p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
            <div className="relative w-full sm:w-72">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" aria-hidden="true" />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter models"
                aria-label="Filter models"
                className="h-8 w-full rounded-lg border border-input bg-surface pl-8 pr-3 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              />
            </div>
            <div className="flex gap-1" role="radiogroup" aria-label="Status" onKeyDown={onRadioKeys}>
              {(["all", "enabled", "disabled"] as ModelFilter[]).map((f) => (
                <button
                  key={f}
                  type="button"
                  role="radio"
                  aria-checked={filter === f}
                  tabIndex={filter === f ? 0 : -1}
                  onClick={() => setFilter(f)}
                  className={cn(
                    "h-8 rounded-lg border px-2.5 text-[12.5px] font-medium capitalize focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                    filter === f ? "border-transparent bg-primary text-primary-fg" : "border-line text-fg-muted hover:text-fg",
                  )}
                >
                  {f}
                </button>
              ))}
            </div>
            <div className="ml-auto flex items-center gap-1.5">
              {selected.size > 0 ? (
                <>
                  <span className="text-[12.5px] font-medium text-fg" role="status">
                    {selected.size} selected
                  </span>
                  <Button variant="ghost" disabled={setDisabled.isPending} onClick={() => setDisabled.mutate({ ids: [...selected], disable: false }, { onSuccess: () => setSelected(new Set()) })}>
                    Enable
                  </Button>
                  <Button variant="ghost" disabled={setDisabled.isPending} onClick={() => setDisabled.mutate({ ids: [...selected], disable: true }, { onSuccess: () => setSelected(new Set()) })}>
                    Disable
                  </Button>
                  <button type="button" onClick={() => setSelected(new Set())} aria-label="Clear selection" className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
                    <X className="h-4 w-4" aria-hidden="true" />
                  </button>
                </>
              ) : (
                <span className="text-[12.5px] tabular-nums text-fg-faint" role="status" aria-live="polite">
                  {filtered.length} shown
                </span>
              )}
            </div>
          </div>
          {filtered.length === 0 ? (
            <p className="px-6 py-10 text-center text-[13px] text-fg-muted">No models match.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[720px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="w-10 px-4 py-2">
                      <input
                        type="checkbox"
                        aria-label="Select all shown models"
                        className={checkboxClass}
                        checked={allFiltered}
                        ref={(el) => {
                          if (el) el.indeterminate = someFiltered && !allFiltered;
                        }}
                        onChange={() =>
                          setSelected((prev) => {
                            const next = new Set(prev);
                            if (allFiltered) filtered.forEach((m) => next.delete(m.id));
                            else filtered.forEach((m) => next.add(m.id));
                            return next;
                          })
                        }
                      />
                    </th>
                    <th scope="col" className="px-2 py-2 font-medium">Model</th>
                    <th scope="col" className="px-4 py-2 font-medium">Kind</th>
                    <th scope="col" className="px-4 py-2 font-medium">Route as</th>
                    <th scope="col" className="px-4 py-2 text-right font-medium">Enabled</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {paged.map((m) => (
                    <ModelRow
                      key={m.id}
                      model={m}
                      route={`${routePrefix}/${m.id}`}
                      disabled={disabledIds.has(m.id)}
                      selected={selected.has(m.id)}
                      onSelect={() =>
                        setSelected((prev) => {
                          const next = new Set(prev);
                          if (next.has(m.id)) next.delete(m.id);
                          else next.add(m.id);
                          return next;
                        })
                      }
                      onToggle={(enable) => setDisabled.mutate({ ids: [m.id], disable: !enable })}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {pages > 1 && <TablePagination page={page} pages={pages} total={total} onPage={setPage} />}
        </div>
      )}
    </div>
  );
}

function ModelRow({
  model: m,
  route,
  disabled,
  selected,
  onSelect,
  onToggle,
}: {
  model: ProviderModel;
  route: string;
  disabled: boolean;
  selected: boolean;
  onSelect: () => void;
  onToggle: (enable: boolean) => void;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(route);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1300);
    } catch {
      /* clipboard blocked */
    }
  };
  return (
    <tr className={cn("transition-colors", selected ? "bg-accent-500/5" : "hover:bg-hover/60", disabled && "text-fg-muted")}>
      <td className="px-4 py-2">
        <input type="checkbox" checked={selected} onChange={onSelect} aria-label={`Select ${m.name || m.id}`} className={checkboxClass} />
      </td>
      <td className="max-w-[340px] px-2 py-2">
        <div className="flex items-center gap-2">
          <span className={cn("truncate font-medium", disabled ? "text-fg-muted" : "text-fg")} title={m.name || m.id}>
            {m.name || m.id}
          </span>
          <ModelCapabilityIcons capabilities={m.capabilities} size={13} />
          {m.custom && <Badge tone="neutral">Custom</Badge>}
          {m.discovered && <Badge tone="neutral">Synced</Badge>}
        </div>
        {m.name && m.name !== m.id && <p className="truncate font-mono text-[11.5px] text-fg-faint">{m.id}</p>}
      </td>
      <td className="px-4 py-2 text-[12.5px] text-fg-muted">{m.kind || "chat"}</td>
      <td className="max-w-[300px] px-4 py-2">
        <button
          type="button"
          onClick={copy}
          aria-label={`Copy ${route}`}
          className="group inline-flex min-h-6 max-w-full items-center gap-1.5 rounded-md font-mono text-[12px] text-fg-muted hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
        >
          <span className="truncate">{route}</span>
          {copied ? (
            <Check className="h-3.5 w-3.5 shrink-0 text-ok" aria-hidden="true" />
          ) : (
            <Copy className="h-3.5 w-3.5 shrink-0 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100" aria-hidden="true" />
          )}
        </button>
        <span className="sr-only" role="status">
          {copied ? "Copied" : ""}
        </span>
      </td>
      <td className="px-4 py-2 text-right">
        <span className="inline-flex">
          <Toggle checked={!disabled} onChange={onToggle} label={`Enable ${m.name || m.id}`} />
        </span>
      </td>
    </tr>
  );
}

// ── Routing ──────────────────────────────────────────────────────────────────

const STRATEGIES: { value: string; label: string; body: string }[] = [
  { value: "inherit", label: "Router default", body: "Follows Settings › Routing" },
  { value: "fill-first", label: "Fill first", body: "Top healthy account until it's unavailable" },
  { value: "round-robin", label: "Round robin", body: "Rotate accounts every few requests" },
  { value: "smart-round-robin", label: "Smart round robin", body: "Rotate new sessions, keep conversations on one account" },
];

function RoutingPanel({ providerId, accountCount }: { providerId: string; accountCount: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const routing = useQuery({ queryKey: ["provider-routing", providerId], queryFn: () => api.providerRouting(providerId) });
  const save = useMutation({
    mutationFn: (patch: Partial<ProviderRoutingSettings>) => api.updateProviderRouting(providerId, patch),
    onSuccess: (data) => {
      qc.setQueryData(["provider-routing", providerId], data);
      toast.success("Routing saved");
    },
    onError: (e: Error) => toast.error("Couldn't save routing", e.message),
  });
  if (routing.isLoading || !routing.data) return <Skeleton className="h-72 w-full rounded-2xl" />;
  return <RoutingForm settings={routing.data} saving={save.isPending} onSave={(p) => save.mutate(p)} accountCount={accountCount} />;
}

function RoutingForm({
  settings,
  saving,
  onSave,
  accountCount,
}: {
  settings: ProviderRoutingSettings;
  saving: boolean;
  onSave: (patch: Partial<ProviderRoutingSettings>) => void;
  accountCount: number;
}) {
  const saved = {
    mode: settings.routing_strategy || "inherit",
    sticky: settings.sticky_limit || 3,
    ttl: Math.max(1, Math.round((settings.affinity_ttl_minutes || 1440) / 60)),
  };
  const [mode, setMode] = useState(saved.mode);
  const [sticky, setSticky] = useState(saved.sticky);
  const [ttl, setTtl] = useState(saved.ttl);
  useEffect(() => {
    if (!saving) {
      setMode(saved.mode);
      setSticky(saved.sticky);
      setTtl(saved.ttl);
    }
  }, [saved.mode, saved.sticky, saved.ttl, saving]);
  const dirty = mode !== saved.mode || sticky !== saved.sticky || ttl !== saved.ttl;
  const rotates = mode === "round-robin" || mode === "smart-round-robin";

  return (
    <section aria-labelledby="strategy-heading" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-5 py-3">
        <h2 id="strategy-heading" className="text-[13px] font-semibold text-fg">
          Account strategy
        </h2>
        {accountCount < 2 && <p className="mt-0.5 text-[12px] text-fg-muted">Takes effect with two or more accounts</p>}
      </div>
      <div className="grid gap-2 p-5 sm:grid-cols-2" role="radiogroup" aria-labelledby="strategy-heading" onKeyDown={onRadioKeys}>
        {STRATEGIES.map((s) => {
          const on = mode === s.value;
          return (
            <button
              key={s.value}
              type="button"
              role="radio"
              aria-checked={on}
              tabIndex={on ? 0 : -1}
              disabled={saving}
              onClick={() => setMode(s.value)}
              className={cn(
                "flex items-start gap-3 rounded-xl border px-3.5 py-3 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-60",
                on ? "border-accent-500 bg-accent-500/5" : "border-line hover:border-line-strong hover:bg-hover",
              )}
            >
              <span className={cn("mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border", on ? "border-accent-500" : "border-input")} aria-hidden="true">
                {on && <span className="h-2 w-2 rounded-full bg-accent-500" />}
              </span>
              <span>
                <span className="block text-[13px] font-medium text-fg">{s.label}</span>
                <span className="mt-0.5 block text-[12.5px] leading-5 text-fg-muted">{s.body}</span>
              </span>
            </button>
          );
        })}
      </div>
      {rotates && (
        <div className="grid gap-4 border-t border-line px-5 py-4 sm:grid-cols-2">
          <NumberSetting label="Requests before rotating" hint="Per account, before the next takes over" value={sticky} min={1} max={100} onChange={setSticky} disabled={saving} />
          {mode === "smart-round-robin" && (
            <NumberSetting label="Session affinity (hours)" hint="How long a conversation stays on its account" value={ttl} min={1} max={168} onChange={setTtl} disabled={saving} />
          )}
        </div>
      )}
      <div className="flex items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
        <span className="mr-auto text-[12.5px] text-fg-muted" role="status" aria-live="polite">
          {saving ? "Saving…" : dirty ? "Unsaved changes" : "Saved"}
        </span>
        {dirty && !saving && (
          <Button
            variant="ghost"
            onClick={() => {
              setMode(saved.mode);
              setSticky(saved.sticky);
              setTtl(saved.ttl);
            }}
          >
            Discard
          </Button>
        )}
        <Button disabled={!dirty || saving} onClick={() => onSave({ routing_strategy: mode, sticky_limit: sticky, affinity_ttl_minutes: ttl * 60 })}>
          {saving && <Loader2 className="animate-spin" aria-hidden="true" />}
          Save changes
        </Button>
      </div>
    </section>
  );
}

function NumberSetting({
  label,
  hint,
  value,
  min,
  max,
  onChange,
  disabled,
}: {
  label: string;
  hint: ReactNode;
  value: number;
  min: number;
  max: number;
  onChange: (v: number) => void;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="block text-[12.5px] font-medium text-fg">
        {label}
      </label>
      <input
        id={id}
        type="number"
        min={min}
        max={max}
        value={value}
        disabled={disabled}
        aria-describedby={`${id}-hint`}
        onChange={(e) => onChange(Math.min(max, Math.max(min, parseInt(e.target.value, 10) || min)))}
        className="h-9 w-32 rounded-lg border border-input bg-surface px-3 font-mono text-[13px] text-fg hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-60"
      />
      <p id={`${id}-hint`} className="text-[12px] text-fg-muted">
        {hint}
      </p>
    </div>
  );
}

function fmtMs(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(2)} s`;
}
