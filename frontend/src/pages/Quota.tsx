import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  ChevronDown,
  Gauge,
  Loader2,
  Power,
  PowerOff,
  RefreshCw,
  Search,
  Server,
  Trash2,
  type LucideIcon,
} from "lucide-react";
import { api, connectUsageStream, type QuotaAccount, type UpstreamQuota } from "../lib/api";
import { REPORT_PERIODS } from "../lib/periods";
import { PageHeader } from "../components/Layout";
import {
  Badge,
  Card,
  EmptyState,
  ErrorCard,
  SegmentedControl,
  Spinner,
  TablePagination,
  useClientPagination,
} from "../components/ui";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";

const PERIODS = REPORT_PERIODS.map((p) => ({ ...p }));

const REFRESH_INTERVAL = 10_000;
const DEPLETED_THRESHOLD = 5;
const ACCOUNTS_PER_PAGE = 12;

type QuotaFilter = "all" | "reported" | "capable" | "usage_only";
type SortMode = "attention" | "reset" | "usage" | "provider";
type SummaryTone = "accent" | "success" | "warning";

const statusMeta: Record<string, { label: string; tone: "success" | "warning" | "danger" | "neutral" }> = {
  active: { label: "Active", tone: "success" },
  paused: { label: "Paused", tone: "neutral" },
  needs_attention: { label: "Needs attention", tone: "danger" },
};

export function QuotaPage() {
  const confirm = useConfirm();
  const [period, setPeriod] = useState<string>("30d");
  const [search, setSearch] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [quotaFilter, setQuotaFilter] = useState<QuotaFilter>("all");
  const [sortMode, setSortMode] = useState<SortMode>("attention");
  const [autoRefresh, setAutoRefresh] = useState(() => localStorage.getItem("quotaAutoRefresh") !== "false");
  const [countdown, setCountdown] = useState(REFRESH_INTERVAL / 1000);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const countdownRef = useRef(REFRESH_INTERVAL / 1000);
  const queryClient = useQueryClient();
  const toast = useToast();

  const quota = useQuery({
    queryKey: ["quota", period],
    queryFn: () => api.quota(period),
    refetchInterval: autoRefresh ? REFRESH_INTERVAL : false,
    placeholderData: (previous) => previous,
  });

  useEffect(() => connectUsageStream(() => {
    queryClient.invalidateQueries({ queryKey: ["quota"] });
  }), [queryClient]);

  useEffect(() => {
    if (!autoRefresh) return;
    countdownRef.current = REFRESH_INTERVAL / 1000;
    setCountdown(REFRESH_INTERVAL / 1000);
    const interval = window.setInterval(() => {
      countdownRef.current = countdownRef.current <= 1
        ? REFRESH_INTERVAL / 1000
        : countdownRef.current - 1;
      setCountdown(countdownRef.current);
    }, 1000);
    return () => window.clearInterval(interval);
  }, [autoRefresh, quota.dataUpdatedAt]);

  useEffect(() => {
    localStorage.setItem("quotaAutoRefresh", String(autoRefresh));
  }, [autoRefresh]);

  useEffect(() => {
    if (!autoRefresh) return;
    const handleVisibility = () => {
      if (document.hidden) queryClient.cancelQueries({ queryKey: ["quota"] });
    };
    document.addEventListener("visibilitychange", handleVisibility);
    return () => document.removeEventListener("visibilitychange", handleVisibility);
  }, [autoRefresh, queryClient]);

  const accounts = quota.data?.accounts ?? [];
  const providers = useMemo(
    () => [...new Map(accounts.map((account) => [account.provider, account.provider_name || account.provider])).entries()]
      .sort((left, right) => left[1].localeCompare(right[1])),
    [accounts],
  );

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase();
    return accounts.filter((account) => {
      if (providerFilter !== "all" && account.provider !== providerFilter) return false;
      if (statusFilter !== "all" && account.status !== statusFilter) return false;
      if (quotaFilter === "reported" && !hasReportedQuota(account)) return false;
      if (quotaFilter === "capable" && (!supportsQuota(account) || hasReportedQuota(account))) return false;
      if (quotaFilter === "usage_only" && supportsQuota(account)) return false;
      if (query) {
        const haystack = [account.provider, account.provider_name, account.label, account.auth_kind, account.plan_name]
          .filter(Boolean)
          .join(" ")
          .toLowerCase();
        if (!haystack.includes(query)) return false;
      }
      return true;
    });
  }, [accounts, providerFilter, quotaFilter, search, statusFilter]);

  const sorted = useMemo(() => [...filtered].sort((left, right) => {
    if (sortMode === "provider") {
      return (left.provider_name || left.provider).localeCompare(right.provider_name || right.provider)
        || (left.label || left.auth_kind).localeCompare(right.label || right.auth_kind);
    }
    if (sortMode === "usage") return right.total_requests - left.total_requests;
    if (sortMode === "reset") return compareNullableTime(earliestReset(left), earliestReset(right));

    const scoreDelta = accountAttentionScore(left) - accountAttentionScore(right);
    if (scoreDelta !== 0) return scoreDelta;
    const resetDelta = compareNullableTime(earliestReset(left), earliestReset(right));
    if (resetDelta !== 0) return resetDelta;
    return right.total_requests - left.total_requests;
  }), [filtered, sortMode]);

  const pagination = useClientPagination(sorted, ACCOUNTS_PER_PAGE);

  useEffect(() => {
    pagination.setPage(1);
    setSelected(new Set());
  }, [providerFilter, quotaFilter, search, sortMode, statusFilter]);

  const toggleAccount = useMutation({
    mutationFn: ({ id, disabled }: { id: string; disabled: boolean }) => api.updateAccount(id, { disabled }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["quota"] }),
    onError: (error: Error) => toast.error("Account update failed", error.message),
  });

  const deleteAccount = useMutation({
    mutationFn: (id: string) => api.deleteAccount(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["quota"] }),
    onError: (error: Error) => toast.error("Account removal failed", error.message),
  });

  const toggleSelection = (id: string) => {
    setSelected((previous) => {
      const next = new Set(previous);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const allPageSelected = pagination.paged.length > 0 && pagination.paged.every((account) => selected.has(account.id));
  const togglePageSelection = () => {
    setSelected((previous) => {
      const next = new Set(previous);
      for (const account of pagination.paged) {
        if (allPageSelected) next.delete(account.id);
        else next.add(account.id);
      }
      return next;
    });
  };

  const toggleExpanded = (id: string) => {
    setExpanded((previous) => {
      const next = new Set(previous);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectedAccounts = accounts.filter((account) => selected.has(account.id));
  const selectedCanEnable = selectedAccounts.filter((account) => account.status === "paused");
  const selectedCanPause = selectedAccounts.filter((account) => account.status !== "paused");

  const applyBulkState = (targets: QuotaAccount[], disabled: boolean) => {
    targets.forEach((account) => toggleAccount.mutate({ id: account.id, disabled }));
    toast.success(
      disabled ? "Accounts paused" : "Accounts enabled",
      `${targets.length} account${targets.length === 1 ? "" : "s"} updated.`,
    );
    setSelected(new Set());
  };

  const handleBulkDelete = async () => {
    if (selectedAccounts.length === 0) return;
    if (!(await confirm({ title: `Delete ${selectedAccounts.length} account${selectedAccounts.length === 1 ? "" : "s"}?`, description: "Their stored credentials are removed too. This cannot be undone.", tone: "danger" }))) return;
    selectedAccounts.forEach((account) => deleteAccount.mutate(account.id));
    toast.success("Accounts removed", `${selectedAccounts.length} account${selectedAccounts.length === 1 ? "" : "s"} deleted.`);
    setSelected(new Set());
  };

  const handleDeleteAccount = async (account: QuotaAccount) => {
    if (!(await confirm({ title: `Delete ${account.label || account.provider_name}?`, description: "The account and its stored credentials are removed. This cannot be undone.", tone: "danger" }))) return;
    deleteAccount.mutate(account.id, {
      onSuccess: () => toast.success("Account removed", "The provider account and its stored secrets were deleted."),
    });
  };

  const depletedAccounts = accounts.filter((account) => account.status === "active" && isDepleted(account));
  const resumableAccounts = accounts.filter((account) => account.status === "paused" && hasReportedQuota(account) && !isDepleted(account));

  const handlePauseDepleted = () => applyBulkState(depletedAccounts, true);
  const handleResumeAvailable = () => applyBulkState(resumableAccounts, false);

  const handleRefresh = async () => {
    countdownRef.current = REFRESH_INTERVAL / 1000;
    setCountdown(REFRESH_INTERVAL / 1000);
    const result = await quota.refetch();
    if (result.isError) toast.error("Quota refresh failed", "The latest account data could not be loaded.");
  };

  const totals = useMemo(() => ({
    requests: accounts.reduce((sum, account) => sum + account.total_requests, 0),
    input: accounts.reduce((sum, account) => sum + account.prompt_tokens, 0),
    output: accounts.reduce((sum, account) => sum + account.completion_tokens, 0),
    cost: accounts.reduce((sum, account) => sum + account.cost_usd, 0),
    active: accounts.filter((account) => account.status === "active").length,
    paused: accounts.filter((account) => account.status === "paused").length,
    attention: accounts.filter((account) => account.status === "needs_attention").length,
    reported: accounts.filter(hasReportedQuota).length,
    capable: accounts.filter(supportsQuota).length,
    usageOnly: accounts.filter((account) => !supportsQuota(account)).length,
    notReported: accounts.filter((account) => supportsQuota(account) && !hasReportedQuota(account)).length,
  }), [accounts]);

  return (
    <>
      <PageHeader
        title="Quota Tracker"
        icon={Gauge}
        description="Monitor account capacity, reported upstream limits, and period usage."
        action={
          <div className="flex items-center gap-2">
            <SegmentedControl value={period} onChange={setPeriod} options={PERIODS} />
            <button
              type="button"
              onClick={handleRefresh}
              disabled={quota.isFetching}
              aria-label="Refresh quota data"
              title="Refresh quota data"
              className="inline-flex h-10 w-10 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] shadow-sm transition-colors hover:border-[var(--border-strong)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-60"
            >
              <RefreshCw className={`h-4 w-4 ${quota.isFetching ? "animate-spin" : ""}`} />
            </button>
          </div>
        }
      />

      {quota.isError ? (
        <ErrorCard message="Failed to load quota and account usage data." />
      ) : (
        <div className="space-y-5 pb-12">
          {!quota.isLoading && accounts.length > 0 && (
            <div className="grid gap-4 lg:grid-cols-3">
              <SummaryGroupCard
                icon={Server}
                title="Routing accounts"
                primary={fmtInteger(totals.active)}
                primaryLabel={`of ${fmtInteger(accounts.length)} active`}
                tone={totals.attention > 0 || depletedAccounts.length > 0 ? "warning" : "success"}
                items={[
                  { label: "Paused", value: fmtInteger(totals.paused) },
                  { label: "Attention", value: fmtInteger(totals.attention), tone: totals.attention > 0 ? "danger" : undefined },
                  { label: "Depleted", value: fmtInteger(depletedAccounts.length), tone: depletedAccounts.length > 0 ? "danger" : undefined },
                ]}
              />
              <SummaryGroupCard
                icon={Activity}
                title="Period usage"
                primary={fmtCompact(totals.requests)}
                primaryLabel="requests"
                items={[
                  { label: "Input", value: fmtCompact(totals.input) },
                  { label: "Output", value: fmtCompact(totals.output) },
                  { label: "Attributed cost", value: fmtUSD(totals.cost) },
                ]}
              />
              <SummaryGroupCard
                icon={Gauge}
                title="Quota visibility"
                primary={fmtInteger(totals.reported)}
                primaryLabel="accounts reporting"
                tone={totals.notReported > 0 ? "warning" : "accent"}
                items={[
                  { label: "Quota-capable", value: fmtInteger(totals.capable) },
                  { label: "Usage only", value: fmtInteger(totals.usageOnly) },
                  { label: "Not reported", value: fmtInteger(totals.notReported) },
                ]}
              />
            </div>
          )}

          {(depletedAccounts.length > 0 || resumableAccounts.length > 0) && (
            <CapacityActions
              depleted={depletedAccounts.length}
              resumable={resumableAccounts.length}
              onPauseDepleted={handlePauseDepleted}
              onResumeAvailable={handleResumeAvailable}
            />
          )}

          <Card>
            <div className="flex flex-col gap-3 border-b border-[var(--border)] px-5 py-4 lg:flex-row lg:items-center lg:justify-between">
              <div>
                <h2 className="text-sm font-semibold">Account capacity</h2>
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  Upstream limits are shown only when the provider reports them; every account still shows local period usage.
                </p>
              </div>
              <button
                type="button"
                onClick={() => setAutoRefresh((current) => !current)}
                aria-pressed={autoRefresh}
                className={`inline-flex h-8 shrink-0 items-center gap-2 self-start rounded-lg border px-3 text-xs font-medium transition-colors lg:self-auto ${
                  autoRefresh
                    ? "border-emerald-300/70 bg-emerald-50 text-emerald-700 dark:border-emerald-700/50 dark:bg-emerald-950/20 dark:text-emerald-300"
                    : "border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)]"
                }`}
              >
                <span className={`h-1.5 w-1.5 rounded-full ${autoRefresh ? "bg-ok" : "bg-[var(--text-muted)]"}`} />
                {autoRefresh ? `Auto refresh · ${countdown}s` : "Auto refresh off"}
              </button>
            </div>

            <div className="flex flex-col gap-3 border-b border-[var(--border)] bg-[var(--bg-subtle)]/30 px-4 py-3 xl:flex-row xl:items-center">
              <label className="relative min-w-0 flex-1 xl:max-w-xs">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--text-muted)]" />
                <span className="sr-only">Search accounts</span>
                <input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search provider or account…"
                  className="h-9 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] pl-9 pr-3 text-xs outline-none transition-colors placeholder:text-[var(--text-muted)] focus:border-[var(--border-strong)] focus:ring-2 focus:ring-accent-400/20"
                />
              </label>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 xl:flex xl:items-center">
                <FilterSelect value={providerFilter} onChange={setProviderFilter} label="Provider">
                  <option value="all">All providers</option>
                  {providers.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
                </FilterSelect>
                <FilterSelect value={statusFilter} onChange={setStatusFilter} label="Status">
                  <option value="all">All statuses</option>
                  <option value="active">Active</option>
                  <option value="paused">Paused</option>
                  <option value="needs_attention">Needs attention</option>
                </FilterSelect>
                <FilterSelect value={quotaFilter} onChange={(value) => setQuotaFilter(value as QuotaFilter)} label="Quota visibility">
                  <option value="all">All quota states</option>
                  <option value="reported">Limits reported</option>
                  <option value="capable">No current report</option>
                  <option value="usage_only">Usage only</option>
                </FilterSelect>
                <FilterSelect value={sortMode} onChange={(value) => setSortMode(value as SortMode)} label="Sort accounts">
                  <option value="attention">Attention first</option>
                  <option value="reset">Reset soon</option>
                  <option value="usage">Highest usage</option>
                  <option value="provider">Provider name</option>
                </FilterSelect>
              </div>
              <span className="shrink-0 text-xs tabular-nums text-[var(--text-muted)]">{fmtInteger(sorted.length)} accounts</span>
            </div>

            {selected.size > 0 && (
              <div className="flex flex-wrap items-center gap-2 border-b border-[var(--border)] bg-accent-50/50 px-4 py-2.5 text-xs dark:bg-accent-950/20">
                <span className="mr-1 font-semibold">{selected.size} selected</span>
                {selectedCanEnable.length > 0 && (
                  <button type="button" onClick={() => applyBulkState(selectedCanEnable, false)} className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2.5 py-1.5 font-medium hover:bg-[var(--bg-subtle)]">Enable</button>
                )}
                {selectedCanPause.length > 0 && (
                  <button type="button" onClick={() => applyBulkState(selectedCanPause, true)} className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2.5 py-1.5 font-medium hover:bg-[var(--bg-subtle)]">Pause</button>
                )}
                <button type="button" onClick={handleBulkDelete} className="rounded-lg border border-red-300/60 bg-[var(--bg-elevated)] px-2.5 py-1.5 font-medium text-bad hover:bg-red-50 dark:border-red-700/50 dark:text-red-300 dark:hover:bg-red-950/20">Delete</button>
                <button type="button" onClick={() => setSelected(new Set())} className="px-2 py-1.5 text-[var(--text-muted)] hover:text-[var(--text)]">Clear</button>
              </div>
            )}

            {quota.isLoading ? (
              <div className="flex min-h-64 items-center justify-center"><Spinner /></div>
            ) : accounts.length === 0 ? (
              <EmptyState title="No connected accounts" hint="Add a provider account to begin tracking usage and quota visibility." />
            ) : sorted.length === 0 ? (
              <EmptyState title="No accounts match these filters." hint="Clear a filter or search for another account." />
            ) : (
              <>
                <div className="divide-y divide-[var(--border)] md:hidden">
                  {pagination.paged.map((account) => (
                    <QuotaAccountMobile
                      key={account.id}
                      account={account}
                      selected={selected.has(account.id)}
                      expanded={expanded.has(account.id)}
                      onSelect={() => toggleSelection(account.id)}
                      onExpand={() => toggleExpanded(account.id)}
                      onToggle={() => toggleAccount.mutate({ id: account.id, disabled: account.status !== "paused" })}
                      onDelete={() => handleDeleteAccount(account)}
                    />
                  ))}
                </div>
                <div className="hidden overflow-x-auto md:block">
                  <table className="w-full min-w-[1120px] text-xs">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-[11.5px] font-medium text-[var(--text-muted)]">
                        <th className="w-12 px-4 py-3 text-left">
                          <input
                            type="checkbox"
                            checked={allPageSelected}
                            onChange={togglePageSelection}
                            aria-label="Select accounts on this page"
                            className="h-3.5 w-3.5 rounded border-[var(--border)] accent-accent-600"
                          />
                        </th>
                        <th className="px-3 py-3 text-left">Provider / account</th>
                        <th className="px-3 py-3 text-left">Routing</th>
                        <th className="w-[290px] px-3 py-3 text-left">Quota visibility</th>
                        <th className="px-3 py-3 text-right">Period usage</th>
                        <th className="px-3 py-3 text-right">Attributed cost</th>
                        <th className="px-4 py-3 text-right">Actions</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[var(--border)]">
                      {pagination.paged.map((account) => (
                        <QuotaAccountRow
                          key={account.id}
                          account={account}
                          selected={selected.has(account.id)}
                          expanded={expanded.has(account.id)}
                          onSelect={() => toggleSelection(account.id)}
                          onExpand={() => toggleExpanded(account.id)}
                          onToggle={() => toggleAccount.mutate({ id: account.id, disabled: account.status !== "paused" })}
                          onDelete={() => handleDeleteAccount(account)}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
                <TablePagination
                  page={pagination.page}
                  pages={pagination.pages}
                  total={pagination.total}
                  onPage={pagination.setPage}
                />
              </>
            )}
          </Card>
        </div>
      )}
    </>
  );
}

function CapacityActions({
  depleted,
  resumable,
  onPauseDepleted,
  onResumeAvailable,
}: {
  depleted: number;
  resumable: number;
  onPauseDepleted: () => void;
  onResumeAvailable: () => void;
}) {
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-amber-300/60 bg-amber-50/40 px-4 py-3 dark:border-amber-700/40 dark:bg-amber-950/10 lg:flex-row lg:items-center">
      <AlertTriangle className="h-4 w-4 shrink-0 text-warn" />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-semibold">Capacity actions available</p>
        <p className="mt-0.5 text-xs text-[var(--text-muted)]">
          Recommendations use only the upstream limits currently reported by providers.
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        {depleted > 0 && (
          <button type="button" onClick={onPauseDepleted} className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-red-300/70 bg-[var(--bg-elevated)] px-3 text-xs font-medium text-bad hover:bg-red-50 dark:border-red-700/50 dark:text-red-300 dark:hover:bg-red-950/20">
            <PowerOff className="h-3.5 w-3.5" /> Pause depleted ({depleted})
          </button>
        )}
        {resumable > 0 && (
          <button type="button" onClick={onResumeAvailable} className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-emerald-300/70 bg-[var(--bg-elevated)] px-3 text-xs font-medium text-emerald-700 hover:bg-emerald-50 dark:border-emerald-700/50 dark:text-emerald-300 dark:hover:bg-emerald-950/20">
            <Power className="h-3.5 w-3.5" /> Resume available ({resumable})
          </button>
        )}
      </div>
    </div>
  );
}

function SummaryGroupCard({
  icon: Icon,
  title,
  primary,
  primaryLabel,
  items,
  tone = "accent",
}: {
  icon: LucideIcon;
  title: string;
  primary: string;
  primaryLabel: string;
  items: Array<{ label: string; value: string; tone?: "good" | "danger" }>;
  tone?: SummaryTone;
}) {
  const tones: Record<SummaryTone, { icon: string; background: string }> = {
    accent: {
      icon: "text-secondary-600 dark:text-secondary-300",
      background: "bg-secondary-50 ring-secondary-200/70 dark:bg-secondary-950/30 dark:ring-secondary-900/60",
    },
    success: {
      icon: "text-ok",
      background: "bg-emerald-50 ring-emerald-200/70 dark:bg-emerald-950/30 dark:ring-emerald-900/60",
    },
    warning: {
      icon: "text-warn",
      background: "bg-amber-50 ring-amber-200/70 dark:bg-amber-950/30 dark:ring-amber-900/60",
    },
  };
  const colors = tones[tone];

  return (
    <Card className="p-5">
      <div className="flex items-center gap-2">
        <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ring-1 ${colors.background}`}>
          <Icon className={`h-4 w-4 ${colors.icon}`} />
        </span>
        <span className="text-xs font-medium text-[var(--text-muted)]">{title}</span>
      </div>
      <div className="mt-4 flex items-baseline gap-2">
        <span className="text-3xl font-semibold tracking-tight tabular-nums">{primary}</span>
        <span className="text-xs text-[var(--text-muted)]">{primaryLabel}</span>
      </div>
      <div className="mt-4 grid grid-cols-3 gap-3 border-t border-[var(--border)] pt-3">
        {items.map((item) => (
          <div key={item.label} className="min-w-0">
            <div className={`truncate text-xs font-semibold tabular-nums sm:text-sm ${
              item.tone === "good"
                ? "text-ok"
                : item.tone === "danger"
                  ? "text-bad"
                  : "text-[var(--text)]"
            }`} title={item.value}>{item.value}</div>
            <div className="mt-0.5 text-[9px] font-medium text-[var(--text-muted)]">{item.label}</div>
          </div>
        ))}
      </div>
    </Card>
  );
}

function FilterSelect({
  value,
  onChange,
  label,
  children,
}: {
  value: string;
  onChange: (value: string) => void;
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="min-w-0">
      <span className="sr-only">{label}</span>
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="h-9 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2.5 text-xs outline-none focus:border-[var(--border-strong)] focus:ring-2 focus:ring-accent-400/20 xl:w-auto"
      >
        {children}
      </select>
    </label>
  );
}

function QuotaAccountRow({
  account,
  selected,
  expanded,
  onSelect,
  onExpand,
  onToggle,
  onDelete,
}: {
  account: QuotaAccount;
  selected: boolean;
  expanded: boolean;
  onSelect: () => void;
  onExpand: () => void;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const quotas = account.upstream_quotas ?? [];
  const canReportQuota = supportsQuota(account);
  const state = effectiveQuotaState(account);
  const status = statusMeta[account.status] ?? { label: account.status, tone: "neutral" as const };
  const refreshQuota = useMutation({
    mutationFn: () => api.accountQuota(account.id),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["quota"] });
      if (result.supported) toast.success("Quota refreshed", "The latest upstream limits are now available.");
      else toast.success("Usage-only account", "This provider does not expose upstream quota through KeiRouter.");
    },
    onError: (error: Error) => toast.error("Quota refresh failed", error.message),
  });

  return (
    <>
      <tr className={`transition-colors hover:bg-[var(--bg-subtle)]/60 ${account.status === "paused" ? "opacity-65" : ""}`}>
        <td className="px-4 py-3 align-middle">
          <input
            type="checkbox"
            checked={selected}
            onChange={onSelect}
            aria-label={`Select ${account.label || account.provider_name}`}
            className="h-3.5 w-3.5 rounded border-[var(--border)] accent-accent-600"
          />
        </td>
        <td className="px-3 py-3 align-middle">
          <div className="flex min-w-0 items-center gap-3">
            <ProviderIcon provider={account.provider} label={account.provider_name} />
            <div className="min-w-0">
              <div className="flex min-w-0 items-center gap-2">
                <span className="max-w-48 truncate text-sm font-semibold" title={account.provider_name}>{account.provider_name || account.provider}</span>
                {account.plan_name && <Badge tone="accent">{account.plan_name}</Badge>}
              </div>
              <div className="mt-0.5 max-w-64 truncate text-[10px] text-[var(--text-muted)]" title={account.label || account.auth_kind}>
                {account.label || account.auth_kind} · {formatAuthKind(account.auth_kind)}
              </div>
            </div>
          </div>
        </td>
        <td className="px-3 py-3 align-middle">
          <span className="whitespace-nowrap"><Badge tone={status.tone}>{status.label}</Badge></span>
          <div className="mt-1 text-[10px] text-[var(--text-muted)]">Priority {account.priority}</div>
        </td>
        <td className="px-3 py-3 align-middle">
          <QuotaVisibilityCell account={account} expanded={expanded} onExpand={onExpand} />
        </td>
        <td className="px-3 py-3 text-right align-middle tabular-nums">
          <div className="font-semibold">{fmtInteger(account.total_requests)} req</div>
          <div className="mt-1 text-[10px] text-[var(--text-muted)]">{fmtCompact(account.prompt_tokens + account.completion_tokens)} tokens</div>
        </td>
        <td className="px-3 py-3 text-right align-middle font-semibold tabular-nums">{fmtUSD(account.cost_usd)}</td>
        <td className="px-4 py-3 text-right align-middle">
          <div className="inline-flex items-center gap-1">
            {canReportQuota && (
              <button
                type="button"
                onClick={() => refreshQuota.mutate()}
                disabled={refreshQuota.isPending || account.status === "paused"}
                aria-label={`Refresh quota for ${account.label || account.provider_name}`}
                title={account.status === "paused" ? "Enable the account before refreshing quota" : "Refresh upstream quota"}
                className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-35"
              >
                {refreshQuota.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
              </button>
            )}
            <button
              type="button"
              onClick={onToggle}
              aria-label={account.status === "paused" ? `Enable ${account.label || account.provider_name}` : `Pause ${account.label || account.provider_name}`}
              title={account.status === "paused" ? "Enable account" : "Pause account"}
              className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
            >
              {account.status === "paused" ? <Power className="h-3.5 w-3.5" /> : <PowerOff className="h-3.5 w-3.5" />}
            </button>
            <button
              type="button"
              onClick={onDelete}
              aria-label={`Delete ${account.label || account.provider_name}`}
              title="Delete account"
              className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/20 dark:hover:text-red-300"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          </div>
          {state === "error" && <span className="sr-only">Quota refresh error</span>}
        </td>
      </tr>
      {expanded && quotas.length > 0 && (
        <tr>
          <td colSpan={7} className="border-t border-[var(--border)] bg-[var(--bg-subtle)]/35 px-5 py-4">
            <QuotaDetails account={account} />
          </td>
        </tr>
      )}
    </>
  );
}

function QuotaAccountMobile({
  account,
  selected,
  expanded,
  onSelect,
  onExpand,
  onToggle,
  onDelete,
}: {
  account: QuotaAccount;
  selected: boolean;
  expanded: boolean;
  onSelect: () => void;
  onExpand: () => void;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const status = statusMeta[account.status] ?? { label: account.status, tone: "neutral" as const };
  const canReportQuota = supportsQuota(account);
  const refreshQuota = useMutation({
    mutationFn: () => api.accountQuota(account.id),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["quota"] });
      if (result.supported) toast.success("Quota refreshed", "The latest upstream limits are now available.");
      else toast.success("Usage-only account", "This provider does not expose upstream quota through KeiRouter.");
    },
    onError: (error: Error) => toast.error("Quota refresh failed", error.message),
  });

  return (
    <article className={`px-4 py-4 ${account.status === "paused" ? "opacity-65" : ""}`}>
      <div className="flex items-start gap-3">
        <input
          type="checkbox"
          checked={selected}
          onChange={onSelect}
          aria-label={`Select ${account.label || account.provider_name}`}
          className="mt-2 h-3.5 w-3.5 shrink-0 rounded border-[var(--border)] accent-accent-600"
        />
        <ProviderIcon provider={account.provider} label={account.provider_name} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <h3 className="truncate text-sm font-semibold">{account.provider_name || account.provider}</h3>
            {account.plan_name && <Badge tone="accent">{account.plan_name}</Badge>}
          </div>
          <p className="mt-0.5 truncate text-[10px] text-[var(--text-muted)]">
            {account.label || account.auth_kind} · {formatAuthKind(account.auth_kind)}
          </p>
        </div>
        <span className="shrink-0 whitespace-nowrap"><Badge tone={status.tone}>{status.label}</Badge></span>
      </div>

      <div className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)]/35 px-3 py-2.5">
        <QuotaVisibilityCell account={account} expanded={expanded} onExpand={onExpand} />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-3 border-t border-[var(--border)] pt-3">
        <div>
          <div className="text-[9px] font-medium text-[var(--text-muted)]">Period usage</div>
          <div className="mt-1 text-xs font-semibold tabular-nums">{fmtInteger(account.total_requests)} requests</div>
          <div className="mt-0.5 text-[10px] text-[var(--text-muted)]">{fmtCompact(account.prompt_tokens + account.completion_tokens)} tokens</div>
        </div>
        <div className="text-right">
          <div className="text-[9px] font-medium text-[var(--text-muted)]">Attributed cost</div>
          <div className="mt-1 text-xs font-semibold tabular-nums">{fmtUSD(account.cost_usd)}</div>
          <div className="mt-0.5 text-[10px] text-[var(--text-muted)]">Priority {account.priority}</div>
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-2">
        {canReportQuota && (
          <button
            type="button"
            onClick={() => refreshQuota.mutate()}
            disabled={refreshQuota.isPending || account.status === "paused"}
            className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-[var(--border)] px-2.5 text-[10px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-35"
          >
            {refreshQuota.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            Refresh quota
          </button>
        )}
        <button type="button" onClick={onToggle} className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-[var(--border)] px-2.5 text-[10px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]">
          {account.status === "paused" ? <Power className="h-3.5 w-3.5" /> : <PowerOff className="h-3.5 w-3.5" />}
          {account.status === "paused" ? "Enable" : "Pause"}
        </button>
        <button type="button" onClick={onDelete} className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-red-300/50 px-2.5 text-[10px] font-medium text-bad hover:bg-red-50 dark:border-red-700/40 dark:text-red-300 dark:hover:bg-red-950/20">
          <Trash2 className="h-3.5 w-3.5" /> Delete
        </button>
      </div>

      {expanded && hasReportedQuota(account) && (
        <div className="mt-4">
          <QuotaDetails account={account} />
        </div>
      )}
    </article>
  );
}

function QuotaVisibilityCell({ account, expanded, onExpand }: { account: QuotaAccount; expanded: boolean; onExpand: () => void }) {
  const quotas = account.upstream_quotas ?? [];
  const state = effectiveQuotaState(account);

  if (quotas.length === 0) {
    const content: Record<string, { label: string; detail: string; tone: string }> = {
      usage_only: {
        label: "Usage only",
        detail: "Provider does not expose upstream limits.",
        tone: "bg-[var(--text-muted)]",
      },
      paused: {
        label: "Quota refresh paused",
        detail: "Enable the account to fetch limits.",
        tone: "bg-[var(--text-muted)]",
      },
      error: {
        label: "Refresh failed",
        detail: account.message || "Retry the upstream quota request.",
        tone: "bg-bad",
      },
      pending: {
        label: "Not yet reported",
        detail: "The provider supports upstream limits.",
        tone: "bg-warn",
      },
      unavailable: {
        label: "Not reported",
        detail: account.message || "The provider returned no limit buckets.",
        tone: "bg-warn",
      },
    };
    const item = content[state] ?? content.unavailable;
    return (
      <div className="flex min-w-0 items-start gap-2">
        <span className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${item.tone}`} />
        <div className="min-w-0">
          <div className="text-xs font-semibold">{item.label}</div>
          <div className="mt-0.5 max-w-64 truncate text-[10px] text-[var(--text-muted)]" title={item.detail}>{item.detail}</div>
        </div>
      </div>
    );
  }

  const remaining = worstRemainingPercent(account);
  const resetAt = earliestReset(account);
  const color = quotaColor(remaining);
  return (
    <div className="min-w-0">
      <div className="flex items-center justify-between gap-3">
        <span className={`text-xs font-semibold tabular-nums ${color.text}`}>{remaining}% remaining</span>
        <button
          type="button"
          onClick={onExpand}
          aria-expanded={expanded}
          className="inline-flex items-center gap-1 text-[10px] font-medium text-[var(--text-muted)] hover:text-[var(--text)]"
        >
          {quotas.length} limit{quotas.length === 1 ? "" : "s"}
          <ChevronDown className={`h-3 w-3 transition-transform ${expanded ? "rotate-180" : ""}`} />
        </button>
      </div>
      <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className={`h-full rounded-full ${color.bar}`} style={{ width: `${Math.max(2, 100 - remaining)}%` }} />
      </div>
      <div className="mt-1 text-[10px] text-[var(--text-muted)]">
        {resetAt ? `Next reset ${formatCountdown(resetAt)}` : "No reset time reported"}
      </div>
    </div>
  );
}

function QuotaDetails({ account }: { account: QuotaAccount }) {
  const quotas = account.upstream_quotas ?? [];
  const { page, pages, paged, setPage, total } = useClientPagination(quotas, 6);

  return (
    <div className="mx-auto max-w-5xl overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)]">
      <div className="flex flex-col gap-2 border-b border-[var(--border)] px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h3 className="text-xs font-semibold">Reported upstream limits</h3>
          <p className="mt-0.5 text-[10px] text-[var(--text-muted)]">
            {account.message || "Values are fetched directly from the provider and may use provider-specific units."}
          </p>
        </div>
        <span className="text-[10px] text-[var(--text-muted)]">Account updated {relativeTime(account.updated_at)}</span>
      </div>
      <div className="divide-y divide-[var(--border)] sm:hidden">
        {paged.map((quota, index) => <QuotaDetailMobile key={`${quota.resource_type}-${index}`} quota={quota} />)}
      </div>
      <div className="hidden overflow-x-auto sm:block">
        <table className="w-full min-w-[680px] text-xs">
          <thead>
            <tr className="border-b border-[var(--border)] text-[9px] font-medium text-[var(--text-muted)]">
              <th className="px-4 py-2.5 text-left">Resource</th>
              <th className="px-3 py-2.5 text-left">Consumption</th>
              <th className="px-3 py-2.5 text-right">Used</th>
              <th className="px-3 py-2.5 text-right">Remaining</th>
              <th className="px-4 py-2.5 text-right">Reset</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--border)]">
            {paged.map((quota, index) => <QuotaDetailRow key={`${quota.resource_type}-${index}`} quota={quota} />)}
          </tbody>
        </table>
      </div>
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </div>
  );
}

function QuotaDetailMobile({ quota }: { quota: UpstreamQuota }) {
  const remaining = quota.limit > 0 ? Math.max(0, Math.min(100, Math.round((quota.remaining / quota.limit) * 100))) : 100;
  const used = quota.limit > 0 ? 100 - remaining : 0;
  const color = quotaColor(remaining);
  return (
    <div className="px-3 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-xs font-semibold">{humanize(quota.resource_type)}</div>
          <div className="mt-0.5 text-[10px] text-[var(--text-muted)]">
            {fmtInteger(quota.used)} used of {quota.limit > 0 ? fmtInteger(quota.limit) : "unlimited"}
          </div>
        </div>
        <div className={`shrink-0 text-right text-xs font-semibold tabular-nums ${color.text}`}>
          {quota.limit > 0 ? `${remaining}% left` : "Unlimited"}
        </div>
      </div>
      <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className={`h-full rounded-full ${color.bar}`} style={{ width: `${Math.max(quota.used > 0 ? 2 : 0, used)}%` }} />
      </div>
      <div className="mt-1.5 text-[10px] text-[var(--text-muted)]">
        {quota.reset_at ? `Resets ${formatCountdown(quota.reset_at)}` : "No reset time reported"}
      </div>
    </div>
  );
}

function QuotaDetailRow({ quota }: { quota: UpstreamQuota }) {
  const remaining = quota.limit > 0 ? Math.max(0, Math.min(100, Math.round((quota.remaining / quota.limit) * 100))) : 100;
  const used = quota.limit > 0 ? 100 - remaining : 0;
  const color = quotaColor(remaining);
  return (
    <tr>
      <td className="px-4 py-3 font-medium">{humanize(quota.resource_type)}</td>
      <td className="px-3 py-3">
        <div className="h-1.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
          <div className={`h-full rounded-full ${color.bar}`} style={{ width: `${Math.max(quota.used > 0 ? 2 : 0, used)}%` }} />
        </div>
      </td>
      <td className="px-3 py-3 text-right tabular-nums">{fmtInteger(quota.used)} / {quota.limit > 0 ? fmtInteger(quota.limit) : "Unlimited"}</td>
      <td className={`px-3 py-3 text-right font-semibold tabular-nums ${color.text}`}>{quota.limit > 0 ? `${remaining}%` : "Unlimited"}</td>
      <td className="whitespace-nowrap px-4 py-3 text-right text-[var(--text-muted)]" title={quota.reset_at ? formatDateTime(quota.reset_at) : undefined}>
        {quota.reset_at ? formatCountdown(quota.reset_at) : "—"}
      </td>
    </tr>
  );
}

function ProviderIcon({ provider, label }: { provider: string; label: string }) {
  const [errored, setErrored] = useState(false);
  if (errored) {
    return (
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-[var(--bg-subtle)] text-[10px] font-bold text-[var(--text-muted)]">
        {(label || provider).slice(0, 2).toUpperCase()}
      </div>
    );
  }
  return (
    <img
      src={`/providers/${provider}.png`}
      alt=""
      onError={() => setErrored(true)}
      className="h-8 w-8 shrink-0 rounded-lg object-contain"
    />
  );
}

function supportsQuota(account: QuotaAccount): boolean {
  return account.quota_supported ?? account.usage_type === "credit";
}

function hasReportedQuota(account: QuotaAccount): boolean {
  return (account.upstream_quotas?.length ?? 0) > 0;
}

function effectiveQuotaState(account: QuotaAccount): string {
  if (hasReportedQuota(account)) return "reported";
  if (account.quota_state) return account.quota_state;
  if (!supportsQuota(account)) return "usage_only";
  if (account.status === "paused") return "paused";
  return "unavailable";
}

function isDepleted(account: QuotaAccount): boolean {
  return (account.upstream_quotas ?? []).some((quota) => quota.limit > 0 && (quota.remaining / quota.limit) * 100 < DEPLETED_THRESHOLD);
}

function worstRemainingPercent(account: QuotaAccount): number {
  const percentages = (account.upstream_quotas ?? [])
    .filter((quota) => quota.limit > 0)
    .map((quota) => Math.max(0, Math.min(100, Math.round((quota.remaining / quota.limit) * 100))));
  return percentages.length > 0 ? Math.min(...percentages) : 100;
}

function earliestReset(account: QuotaAccount): number | null {
  let earliest: number | null = null;
  for (const quota of account.upstream_quotas ?? []) {
    if (!quota.reset_at) continue;
    const timestamp = new Date(quota.reset_at).getTime();
    if (Number.isFinite(timestamp) && (earliest == null || timestamp < earliest)) earliest = timestamp;
  }
  return earliest;
}

function compareNullableTime(left: number | null, right: number | null): number {
  if (left == null && right == null) return 0;
  if (left == null) return 1;
  if (right == null) return -1;
  return left - right;
}

function accountAttentionScore(account: QuotaAccount): number {
  if (account.status === "active" && isDepleted(account)) return 0;
  if (account.status === "needs_attention") return 1;
  if (effectiveQuotaState(account) === "error") return 2;
  if (account.status === "active") return 3;
  return 4;
}

function quotaColor(remaining: number): { bar: string; text: string } {
  if (remaining > 70) return { bar: "bg-ok", text: "text-ok" };
  if (remaining >= 30) return { bar: "bg-warn", text: "text-warn" };
  return { bar: "bg-bad", text: "text-bad" };
}

function formatCountdown(value: string | number): string {
  const timestamp = typeof value === "number" ? value : new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return "—";
  const difference = timestamp - Date.now();
  if (difference <= 0) return "now";
  const days = Math.floor(difference / 86_400_000);
  const hours = Math.floor((difference % 86_400_000) / 3_600_000);
  const minutes = Math.floor((difference % 3_600_000) / 60_000);
  if (days > 0) return `in ${days}d ${hours}h`;
  if (hours > 0) return `in ${hours}h ${minutes}m`;
  return `in ${Math.max(1, minutes)}m`;
}

function relativeTime(value: string): string {
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return "unknown";
  const difference = Date.now() - timestamp;
  if (difference < 60_000) return "just now";
  if (difference < 3_600_000) return `${Math.floor(difference / 60_000)}m ago`;
  if (difference < 86_400_000) return `${Math.floor(difference / 3_600_000)}h ago`;
  return `${Math.floor(difference / 86_400_000)}d ago`;
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

function humanize(value: string): string {
  return value.replace(/[_-]+/g, " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

function formatAuthKind(value: string): string {
  const known: Record<string, string> = {
    oauth: "OAuth",
    api_key: "API key",
    bearer: "Bearer token",
  };
  return known[value.toLowerCase()] || humanize(value);
}

function fmtInteger(value: number): string {
  return Math.round(value).toLocaleString();
}

function fmtCompact(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return value.toLocaleString();
}

function fmtUSD(value: number): string {
  if (value > 0 && value < 0.0001) return "<$0.0001";
  if (value < 1) return `$${value.toFixed(4)}`;
  return `$${value.toFixed(2)}`;
}
