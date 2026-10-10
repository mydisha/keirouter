import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ChevronDown,
  Loader2,
  MoreHorizontal,
  Plug,
  Power,
  PowerOff,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Trash2,
} from "lucide-react";
import { api, connectUsageStream, type QuotaAccount, type UpstreamQuota } from "../lib/api";
import { REPORT_PERIODS } from "../lib/periods";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorCard,
  Input,
  Select,
  Skeleton,
  TablePagination,
  Toggle,
  useClientPagination,
} from "../components/ui";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../components/ui/dropdown-menu";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";

const PERIODS = REPORT_PERIODS.map((p) => ({ ...p }));

const REFRESH_INTERVAL = 10_000;
const DEPLETED_THRESHOLD = 5;
// A window counts as "near limit" once this share of it has been used.
const NEAR_LIMIT_USED = 80;
const ACCOUNTS_PER_PAGE = 12;
// Windows shown inline per account; the rest live in the expandable details.
const INLINE_WINDOWS = 2;

type QuotaFilter = "all" | "reported" | "capable" | "usage_only";
type SortMode = "attention" | "reset" | "usage" | "provider";

const statusMeta: Record<string, { label: string; tone: "success" | "warning" | "danger" | "neutral" }> = {
  active: { label: "Active", tone: "success" },
  paused: { label: "Paused", tone: "neutral" },
  needs_attention: { label: "Needs attention", tone: "danger" },
};

const STATUS_CHIPS = [
  { value: "all", label: "All" },
  { value: "active", label: "Active" },
  { value: "paused", label: "Paused" },
  { value: "needs_attention", label: "Needs attention" },
];

const iconButton =
  "inline-flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-35";

const checkboxClass = "h-4 w-4 rounded border-input accent-accent-500";
// Pads the native checkbox to a 24×24 target (WCAG 2.5.8).
const checkboxTarget = "inline-flex h-6 w-6 cursor-pointer items-center justify-center rounded";

function SelectBox({ checked, onChange, label, className }: { checked: boolean; onChange: () => void; label: string; className?: string }) {
  return (
    <label className={cn(checkboxTarget, className)}>
      <input type="checkbox" checked={checked} onChange={onChange} aria-label={label} className={checkboxClass} />
    </label>
  );
}

export function QuotaPage() {
  const confirm = useConfirm();
  const navigate = useNavigate();
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
  const [filtersOpen, setFiltersOpen] = useState(false);
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

  const accounts = useMemo(() => quota.data?.accounts ?? [], [quota.data]);
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

  const totals = useMemo(() => {
    let nextReset: { at: number; account: QuotaAccount } | null = null;
    for (const account of accounts) {
      const at = earliestReset(account);
      if (at != null && at > Date.now() && (nextReset == null || at < nextReset.at)) nextReset = { at, account };
    }
    return {
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
      exhausted: accounts.filter(isDepleted).length,
      nearLimit: accounts.filter((account) => !isDepleted(account) && worstRemainingPercent(account) <= 100 - NEAR_LIMIT_USED).length,
      nextReset,
    };
  }, [accounts]);

  const activeFilterCount = [providerFilter !== "all", quotaFilter !== "all", sortMode !== "attention"].filter(Boolean).length;
  const resetFilters = () => {
    setProviderFilter("all");
    setQuotaFilter("all");
    setSortMode("attention");
  };

  const statusCounts: Record<string, number> = {
    all: accounts.length,
    active: totals.active,
    paused: totals.paused,
    needs_attention: totals.attention,
  };

  return (
    <>
      <PageHeader
        title="Quota tracker"
        description="Upstream limits and local usage for each provider account."
        action={
          <>
            <PeriodRadios value={period} onChange={setPeriod} />
            <button
              type="button"
              onClick={() => setAutoRefresh((current) => !current)}
              aria-pressed={autoRefresh}
              title={autoRefresh ? "Pause automatic refresh" : "Refresh every 10 seconds"}
              className={cn(
                "inline-flex h-8 items-center gap-2 rounded-lg border border-line bg-surface px-2.5 text-[12px] font-medium transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                autoRefresh ? "text-fg" : "text-fg-muted",
              )}
            >
              <span className={cn("h-1.5 w-1.5 rounded-full", autoRefresh ? "live-dot bg-ok" : "bg-fg-faint")} aria-hidden="true" />
              <span className="tabular-nums">
                Auto refresh
                {/* Ticks every second; keep it out of the accessible name (aria-pressed carries state). */}
                <span aria-hidden="true">{autoRefresh ? ` · ${countdown}s` : " off"}</span>
              </span>
            </button>
            <button
              type="button"
              onClick={handleRefresh}
              disabled={quota.isFetching}
              aria-label="Refresh quota data"
              title="Refresh quota data"
              className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-60"
            >
              <RefreshCw className={cn("h-3.5 w-3.5", quota.isFetching && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
            </button>
          </>
        }
      />

      {quota.isError ? (
        <ErrorCard message="Couldn't load quota data. Check the gateway is running, then refresh." />
      ) : quota.isLoading ? (
        <QuotaSkeleton />
      ) : accounts.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <h2 className="text-[14px] font-medium text-fg">No connected accounts</h2>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
            Connect a provider account to track its usage and limits.
          </p>
          <Button className="mt-4" onClick={() => navigate("/providers")}>
            <Plug aria-hidden="true" />
            Connect a provider
          </Button>
        </div>
      ) : (
        <div className="space-y-5 pb-12">
          {(depletedAccounts.length > 0 || resumableAccounts.length > 0) && (
            <CapacityActions
              depleted={depletedAccounts.length}
              resumable={resumableAccounts.length}
              onPauseDepleted={handlePauseDepleted}
              onResumeAvailable={handleResumeAvailable}
            />
          )}

          <SummaryStrip
            cells={[
              {
                label: "Needs attention",
                value: fmtInteger(totals.attention),
                hint: `of ${fmtInteger(accounts.length)} accounts`,
                tone: totals.attention > 0 ? "bad" : undefined,
              },
              {
                label: "Near limit",
                value: fmtInteger(totals.nearLimit),
                hint: `${NEAR_LIMIT_USED}%+ of a window used`,
                tone: totals.nearLimit > 0 ? "warn" : undefined,
              },
              {
                label: "Exhausted",
                value: fmtInteger(totals.exhausted),
                hint: depletedAccounts.length > 0 ? `${fmtInteger(depletedAccounts.length)} still routing` : undefined,
                tone: totals.exhausted > 0 ? "bad" : undefined,
              },
              {
                label: "Next reset",
                value: totals.nextReset ? formatCountdown(totals.nextReset.at).replace(/^in /, "") : "—",
                hint: totals.nextReset ? totals.nextReset.account.label || totals.nextReset.account.provider_name : undefined,
                title: totals.nextReset ? new Date(totals.nextReset.at).toLocaleString() : undefined,
              },
            ]}
          />

          <Card>
            <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
              <h2 className="text-[13px] font-semibold text-fg">Accounts</h2>
              <span role="status" className="shrink-0 text-[12px] tabular-nums text-fg-muted">
                {sorted.length === accounts.length
                  ? `${fmtInteger(accounts.length)} accounts`
                  : `${fmtInteger(sorted.length)} of ${fmtInteger(accounts.length)} accounts`}
              </span>
            </div>

            <div className="border-b border-line px-4 py-2.5">
              <div className="flex flex-col gap-2 lg:flex-row lg:items-center">
                <label className="relative min-w-0 lg:w-60">
                  <span className="sr-only">Search accounts</span>
                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
                  <Input
                    type="search"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder="Search accounts"
                    className="h-8 min-h-8 py-0 pl-8 text-[12.5px]"
                  />
                </label>
                <div className="flex flex-wrap gap-1" role="radiogroup" aria-label="Filter by status">
                  {STATUS_CHIPS.map((chip) => {
                    const active = statusFilter === chip.value;
                    return (
                      <button
                        key={chip.value}
                        type="button"
                        role="radio"
                        aria-checked={active}
                        onClick={() => setStatusFilter(chip.value)}
                        className={cn(
                          "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                          active ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:text-fg",
                        )}
                      >
                        {chip.label}
                        <span className={cn("tabular-nums", !active && "text-fg-faint")}>{statusCounts[chip.value] ?? 0}</span>
                      </button>
                    );
                  })}
                </div>
                <div className="flex items-center gap-1 lg:ml-auto">
                  {activeFilterCount > 0 && (
                    <button
                      type="button"
                      onClick={resetFilters}
                      className="inline-flex h-8 items-center rounded-lg px-2 text-[12px] text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                    >
                      Reset
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => setFiltersOpen((open) => !open)}
                    aria-expanded={filtersOpen}
                    aria-controls="quota-filters"
                    className={cn(
                      "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                      filtersOpen ? "border-line-strong bg-hover text-fg" : "border-line bg-surface text-fg-muted hover:text-fg",
                    )}
                  >
                    <SlidersHorizontal className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                    Filters
                    {activeFilterCount > 0 && (
                      <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg">
                        {activeFilterCount}
                        <span className="sr-only"> active</span>
                      </span>
                    )}
                  </button>
                </div>
              </div>
              {filtersOpen && (
                <div id="quota-filters" role="group" aria-label="Filters and sort" className="mt-2.5 grid grid-cols-1 gap-2 sm:grid-cols-3">
                  <FilterSelect value={providerFilter} onChange={setProviderFilter} label="Provider">
                    <option value="all">All providers</option>
                    {providers.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
                  </FilterSelect>
                  <FilterSelect value={quotaFilter} onChange={(value) => setQuotaFilter(value as QuotaFilter)} label="Quota">
                    <option value="all">All quota states</option>
                    <option value="reported">Limits reported</option>
                    <option value="capable">No current report</option>
                    <option value="usage_only">Usage only</option>
                  </FilterSelect>
                  <FilterSelect value={sortMode} onChange={(value) => setSortMode(value as SortMode)} label="Sort">
                    <option value="attention">Attention first</option>
                    <option value="reset">Reset soon</option>
                    <option value="usage">Highest usage</option>
                    <option value="provider">Provider name</option>
                  </FilterSelect>
                </div>
              )}
            </div>

            {selected.size > 0 && (
              <div className="flex flex-wrap items-center gap-2 border-b border-line bg-subtle px-4 py-2 text-[12.5px]">
                <span role="status" className="mr-1 font-medium tabular-nums text-fg">{selected.size} selected</span>
                {selectedCanEnable.length > 0 && (
                  <Button variant="secondary" className="h-8 min-h-8 text-[12.5px]" onClick={() => applyBulkState(selectedCanEnable, false)}>
                    <Power strokeWidth={1.75} aria-hidden="true" /> Enable
                  </Button>
                )}
                {selectedCanPause.length > 0 && (
                  <Button variant="secondary" className="h-8 min-h-8 text-[12.5px]" onClick={() => applyBulkState(selectedCanPause, true)}>
                    <PowerOff strokeWidth={1.75} aria-hidden="true" /> Pause
                  </Button>
                )}
                <Button variant="danger" className="h-8 min-h-8 text-[12.5px]" onClick={handleBulkDelete}>
                  <Trash2 strokeWidth={1.75} aria-hidden="true" /> Delete
                </Button>
                <button
                  type="button"
                  onClick={() => setSelected(new Set())}
                  className="inline-flex h-8 items-center rounded-lg px-2 text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                >
                  Clear selection
                </button>
              </div>
            )}

            {sorted.length === 0 ? (
              <EmptyState title="No accounts match these filters" hint="Clear a filter or search for another account." />
            ) : (
              <>
                <div className="divide-y divide-line md:hidden">
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
                  <table className="w-full min-w-[900px] text-[13px]">
                    <caption className="sr-only">Provider accounts</caption>
                    <thead>
                      <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                        <th scope="col" className="w-10 py-1.5 pl-3 pr-1 font-medium">
                          <SelectBox checked={allPageSelected} onChange={togglePageSelection} label="Select accounts on this page" />
                        </th>
                        <th scope="col" className="px-3 py-2 font-medium">Account</th>
                        <th scope="col" className="px-3 py-2 font-medium">Routing</th>
                        <th scope="col" className="w-[300px] px-3 py-2 font-medium">Quota windows</th>
                        <th scope="col" className="px-3 py-2 text-right font-medium">Period usage</th>
                        <th scope="col" className="px-3 py-2 text-right font-medium">Cost</th>
                        <th scope="col" className="px-4 py-2 text-right font-medium"><span className="sr-only">Actions</span></th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-line">
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

function PeriodRadios({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Usage period">
      {PERIODS.map((option) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(option.value)}
            className={cn(
              "h-full rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
            )}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

function QuotaSkeleton() {
  return (
    <div className="space-y-5" aria-busy="true" aria-label="Loading quota data">
      <div className="grid grid-cols-2 gap-px overflow-hidden rounded-2xl border border-line bg-line lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <div key={index} className="space-y-2 bg-surface px-4 py-3">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-6 w-14" />
            <Skeleton className="h-3 w-28" />
          </div>
        ))}
      </div>
      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="border-b border-line px-4 py-3">
          <Skeleton className="h-3.5 w-24" />
        </div>
        <div className="divide-y divide-line">
          {Array.from({ length: 6 }).map((_, index) => (
            <div key={index} className="flex items-center gap-4 px-4 py-3">
              <Skeleton className="h-6 w-6 rounded-md" />
              <div className="w-48 space-y-1.5">
                <Skeleton className="h-3.5 w-32" />
                <Skeleton className="h-3 w-40" />
              </div>
              <Skeleton className="hidden h-5 w-16 md:block" />
              <div className="flex-1 space-y-1.5">
                <Skeleton className="h-1.5 w-full max-w-xs" />
                <Skeleton className="h-1.5 w-full max-w-xs" />
              </div>
              <Skeleton className="hidden h-4 w-16 md:block" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function SummaryStrip({
  cells,
}: {
  cells: { label: string; value: string; hint?: string; tone?: "warn" | "bad"; title?: string }[];
}) {
  return (
    <section
      aria-label="Quota summary"
      className="grid grid-cols-2 gap-px overflow-hidden rounded-2xl border border-line bg-line shadow-[var(--shadow-card)] lg:grid-cols-4"
    >
      {cells.map((cell) => (
        <div key={cell.label} className="flex min-w-0 flex-col gap-1 bg-surface px-4 py-3" title={cell.title}>
          <span className="text-[12px] font-medium text-fg-muted">{cell.label}</span>
          <span
            className={cn(
              "text-[22px] font-semibold leading-tight tracking-[-0.02em] tabular-nums",
              cell.tone === "bad" ? "text-bad" : cell.tone === "warn" ? "text-warn" : "text-fg",
            )}
          >
            {cell.value}
          </span>
          {cell.hint && <span className="truncate text-[12px] tabular-nums text-fg-faint" title={cell.hint}>{cell.hint}</span>}
        </div>
      ))}
    </section>
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
    <section aria-label="Accounts needing attention" className="flex flex-col gap-3 rounded-2xl border border-warn/30 bg-warn/5 px-4 py-3 sm:flex-row sm:items-center">
      <AlertTriangle className="hidden h-4 w-4 shrink-0 text-warn sm:block" strokeWidth={1.75} aria-hidden="true" />
      <div className="min-w-0 flex-1 text-[13px] leading-5">
        <p role="status" className="font-medium text-fg">
          {depleted > 0 && `${depleted} active account${depleted === 1 ? " is" : "s are"} almost out of quota`}
          {depleted > 0 && resumable > 0 && " · "}
          {resumable > 0 && `${resumable} paused account${resumable === 1 ? " has" : "s have"} capacity again`}
        </p>
        <p className="text-[12px] text-fg-muted">Paused accounts stay out of routing until enabled.</p>
      </div>
      <div className="flex flex-wrap gap-2">
        {depleted > 0 && (
          <Button variant="danger" className="h-8 min-h-8 text-[12.5px]" onClick={onPauseDepleted}>
            <PowerOff strokeWidth={1.75} aria-hidden="true" /> Pause depleted ({depleted})
          </Button>
        )}
        {resumable > 0 && (
          <Button variant="secondary" className="h-8 min-h-8 text-[12.5px]" onClick={onResumeAvailable}>
            <Power strokeWidth={1.75} aria-hidden="true" /> Resume available ({resumable})
          </Button>
        )}
      </div>
    </section>
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
    <label className="flex min-w-0 items-center gap-2">
      <span className="w-14 shrink-0 text-[12px] font-medium text-fg-muted sm:w-auto">{label}</span>
      <Select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="h-8 min-h-8 py-0 pl-2.5 pr-7 text-[12.5px]"
      >
        {children}
      </Select>
    </label>
  );
}

// useQuotaRefresh asks the provider for fresh upstream limits for one account.
function useQuotaRefresh(account: QuotaAccount) {
  const queryClient = useQueryClient();
  const toast = useToast();
  return useMutation({
    mutationFn: () => api.accountQuota(account.id),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["quota"] });
      if (result.supported) toast.success("Quota refreshed", "The latest upstream limits are now available.");
      else toast.success("Usage-only account", "This provider does not expose upstream quota through KeiRouter.");
    },
    onError: (error: Error) => toast.error("Quota refresh failed", error.message),
  });
}

type RowProps = {
  account: QuotaAccount;
  selected: boolean;
  expanded: boolean;
  onSelect: () => void;
  onExpand: () => void;
  onToggle: () => void;
  onDelete: () => void;
};

function accountName(account: QuotaAccount): string {
  return account.label || account.provider_name;
}

function AccountIdentity({ account }: { account: QuotaAccount }) {
  const providerName = account.provider_name || account.provider;
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <ProviderLogo icon={`/providers/${account.provider}.png`} name={providerName} size={24} />
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="max-w-48 truncate font-medium text-fg" title={providerName}>{providerName}</span>
          {account.plan_name && <Badge tone="neutral">{account.plan_name}</Badge>}
        </div>
        <div className="max-w-64 truncate text-[12px] text-fg-faint" title={account.label || account.auth_kind}>
          {account.label || account.auth_kind} · {formatAuthKind(account.auth_kind)}
        </div>
      </div>
    </div>
  );
}

function RowActions({
  account,
  refreshQuota,
  onToggle,
  onDelete,
}: {
  account: QuotaAccount;
  refreshQuota: ReturnType<typeof useQuotaRefresh>;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const name = accountName(account);
  const paused = account.status === "paused";
  return (
    <div className="inline-flex items-center gap-1">
      {supportsQuota(account) && (
        <button
          type="button"
          onClick={() => refreshQuota.mutate()}
          disabled={refreshQuota.isPending || paused}
          aria-label={`Refresh quota for ${name}`}
          title={paused ? "Enable the account before refreshing quota" : "Refresh upstream quota"}
          className={iconButton}
        >
          {refreshQuota.isPending
            ? <Loader2 className="h-4 w-4 animate-spin" strokeWidth={1.75} aria-hidden="true" />
            : <RefreshCw className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />}
        </button>
      )}
      <span title={paused ? "Enable account" : "Pause account"} className="inline-flex px-1">
        <Toggle checked={!paused} onChange={() => onToggle()} label={`Route traffic to ${name}`} />
      </span>
      <DropdownMenu>
        <DropdownMenuTrigger aria-label={`Actions for ${name}`} className={iconButton}>
          <MoreHorizontal className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem tone="danger" onSelect={onDelete}>
            <Trash2 aria-hidden="true" />
            Delete account
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function QuotaAccountRow({ account, selected, expanded, onSelect, onExpand, onToggle, onDelete }: RowProps) {
  const quotas = account.upstream_quotas ?? [];
  const state = effectiveQuotaState(account);
  const status = statusMeta[account.status] ?? { label: account.status, tone: "neutral" as const };
  const refreshQuota = useQuotaRefresh(account);

  return (
    <>
      <tr className="align-top transition-colors hover:bg-hover">
        <td className="py-2.5 pl-3 pr-1">
          <SelectBox checked={selected} onChange={onSelect} label={`Select ${accountName(account)}`} className="mt-0.5" />
        </td>
        <td className="px-3 py-3">
          <AccountIdentity account={account} />
        </td>
        <td className="px-3 py-3">
          <Badge tone={status.tone}>{status.label}</Badge>
          <div className="mt-1 text-[12px] tabular-nums text-fg-faint">Priority {account.priority}</div>
        </td>
        <td className="px-3 py-3">
          <QuotaWindowsCell account={account} expanded={expanded} onExpand={onExpand} detailsId={`quota-details-${account.id}`} />
        </td>
        <td className="px-3 py-3 text-right tabular-nums">
          <div className="text-fg">{fmtInteger(account.total_requests)} req</div>
          <div className="text-[12px] text-fg-faint">{fmtCompact(account.prompt_tokens + account.completion_tokens)} tokens</div>
        </td>
        <td className="px-3 py-3 text-right tabular-nums text-fg">{fmtUSD(account.cost_usd)}</td>
        <td className="px-4 py-2.5 text-right">
          <RowActions account={account} refreshQuota={refreshQuota} onToggle={onToggle} onDelete={onDelete} />
          {state === "error" && <span className="sr-only">Quota refresh error</span>}
        </td>
      </tr>
      {expanded && quotas.length > 0 && (
        <tr id={`quota-details-${account.id}`}>
          <td colSpan={7} className="bg-subtle px-4 py-3">
            <QuotaDetails account={account} />
          </td>
        </tr>
      )}
    </>
  );
}

function QuotaAccountMobile({ account, selected, expanded, onSelect, onExpand, onToggle, onDelete }: RowProps) {
  const status = statusMeta[account.status] ?? { label: account.status, tone: "neutral" as const };
  const refreshQuota = useQuotaRefresh(account);

  return (
    <article className="px-4 py-3.5 text-[13px]" aria-label={accountName(account)}>
      <div className="flex items-start gap-2">
        <SelectBox checked={selected} onChange={onSelect} label={`Select ${accountName(account)}`} className="shrink-0" />
        <div className="min-w-0 flex-1">
          <AccountIdentity account={account} />
        </div>
        <Badge tone={status.tone}>{status.label}</Badge>
      </div>

      <div className="mt-3 pl-8">
        <QuotaWindowsCell account={account} expanded={expanded} onExpand={onExpand} detailsId={`quota-details-m-${account.id}`} />
      </div>

      <div className="mt-3 flex items-center justify-between gap-3 pl-8">
        <div className="min-w-0 text-[12px] tabular-nums text-fg-muted">
          <span className="text-fg">{fmtInteger(account.total_requests)} req</span>
          {" · "}{fmtCompact(account.prompt_tokens + account.completion_tokens)} tokens
          {" · "}{fmtUSD(account.cost_usd)}
          <span className="text-fg-faint"> · Priority {account.priority}</span>
        </div>
        <RowActions account={account} refreshQuota={refreshQuota} onToggle={onToggle} onDelete={onDelete} />
      </div>

      {expanded && hasReportedQuota(account) && (
        <div className="mt-3" id={`quota-details-m-${account.id}`}>
          <QuotaDetails account={account} />
        </div>
      )}
    </article>
  );
}

function QuotaWindowsCell({ account, expanded, onExpand, detailsId }: { account: QuotaAccount; expanded: boolean; onExpand: () => void; detailsId: string }) {
  const quotas = account.upstream_quotas ?? [];
  const state = effectiveQuotaState(account);

  if (quotas.length === 0) {
    // Only states with something actionable to say carry a second line.
    const content: Record<string, { label: string; detail?: string; dot: string }> = {
      usage_only: { label: "Usage only", dot: "bg-fg-faint" },
      paused: { label: "Paused · enable to fetch limits", dot: "bg-fg-faint" },
      error: { label: "Refresh failed", detail: account.message || "Retry the quota refresh.", dot: "bg-bad" },
      pending: { label: "Not yet reported", dot: "bg-warn" },
      unavailable: { label: "Not reported", detail: account.message || undefined, dot: "bg-warn" },
    };
    const item = content[state] ?? content.unavailable;
    return (
      <div className="flex min-w-0 items-start gap-2">
        <span className={cn("mt-[7px] h-1.5 w-1.5 shrink-0 rounded-full", item.dot)} aria-hidden="true" />
        <div className="min-w-0">
          <div className={cn("text-[13px]", state === "error" ? "text-bad" : "text-fg")}>{item.label}</div>
          {item.detail && <div className="max-w-72 truncate text-[12px] text-fg-faint" title={item.detail}>{item.detail}</div>}
        </div>
      </div>
    );
  }

  // Surface the most-consumed windows first; the rest are in the details panel.
  const ranked = [...quotas].sort((left, right) => windowUsedPercent(right) - windowUsedPercent(left));
  const inline = ranked.slice(0, INLINE_WINDOWS);
  const hidden = quotas.length - inline.length;

  return (
    <div className="min-w-0 space-y-2">
      {inline.map((window, index) => <WindowBar key={`${window.resource_type}-${index}`} quota={window} />)}
      <button
        type="button"
        onClick={onExpand}
        aria-expanded={expanded}
        aria-controls={expanded ? detailsId : undefined}
        className="inline-flex min-h-6 items-center gap-1 rounded text-[12px] text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        {expanded
          ? "Hide details"
          : hidden > 0
            ? `${hidden} more limit${hidden === 1 ? "" : "s"}`
            : "Details"}
        <span className="sr-only"> for {accountName(account)}</span>
        <ChevronDown className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-180")} strokeWidth={1.75} aria-hidden="true" />
      </button>
    </div>
  );
}

// WindowBar is one quota window as a thin bar: accent while healthy, warn once
// mostly used, bad when exhausted.
function WindowBar({ quota }: { quota: UpstreamQuota }) {
  const limited = quota.limit > 0;
  const used = windowUsedPercent(quota);
  const tone = windowTone(quota);
  return (
    <div className="min-w-0" title={quota.reset_at ? `Resets ${formatDateTime(quota.reset_at)}` : undefined}>
      <div className="grid grid-cols-[minmax(0,7.5rem)_minmax(0,1fr)_2.75rem] items-center gap-2">
        <span className="truncate text-[12px] text-fg-muted" title={humanize(quota.resource_type)}>{humanize(quota.resource_type)}</span>
        <UsageBar quota={quota} />
        <span className={cn("text-right text-[12px] tabular-nums", tone.text)}>{limited ? `${used}%` : "—"}</span>
      </div>
      <div className="mt-0.5 truncate text-[11.5px] tabular-nums text-fg-faint">
        {limited ? `${fmtCompact(quota.used)} of ${fmtCompact(quota.limit)}` : `${fmtCompact(quota.used)} used · unlimited`}
        {" · "}
        {quota.reset_at ? `resets ${formatCountdown(quota.reset_at)}` : "no reset reported"}
      </div>
    </div>
  );
}

function UsageBar({ quota }: { quota: UpstreamQuota }) {
  const used = windowUsedPercent(quota);
  const tone = windowTone(quota);
  return (
    <div
      className="h-1.5 overflow-hidden rounded-full bg-track"
      role="meter"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={used}
      aria-label={`${humanize(quota.resource_type)} used`}
    >
      <div className={cn("h-full rounded-full", tone.bar)} style={{ width: `${Math.max(quota.used > 0 && quota.limit > 0 ? 2 : 0, used)}%` }} />
    </div>
  );
}

function QuotaDetails({ account }: { account: QuotaAccount }) {
  const quotas = account.upstream_quotas ?? [];
  const { page, pages, paged, setPage, total } = useClientPagination(quotas, 6);

  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface">
      <div className="flex flex-col gap-1 border-b border-line px-4 py-2.5 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <h3 className="text-[13px] font-semibold text-fg">Upstream limits</h3>
          {account.message && <p className="text-[12px] text-fg-muted">{account.message}</p>}
        </div>
        <span className="shrink-0 text-[12px] text-fg-faint">Updated {relativeTime(account.updated_at)}</span>
      </div>
      <div className="divide-y divide-line sm:hidden">
        {paged.map((quota, index) => <QuotaDetailMobile key={`${quota.resource_type}-${index}`} quota={quota} />)}
      </div>
      <div className="hidden overflow-x-auto sm:block">
        <table className="w-full min-w-[640px] text-[13px]">
          <caption className="sr-only">Upstream limits for {accountName(account)}</caption>
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th scope="col" className="px-4 py-2 font-medium">Window</th>
              <th scope="col" className="w-[30%] px-3 py-2 font-medium">Consumption</th>
              <th scope="col" className="px-3 py-2 text-right font-medium">Used</th>
              <th scope="col" className="px-3 py-2 text-right font-medium">Remaining</th>
              <th scope="col" className="px-4 py-2 text-right font-medium">Resets</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {paged.map((quota, index) => <QuotaDetailRow key={`${quota.resource_type}-${index}`} quota={quota} />)}
          </tbody>
        </table>
      </div>
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </div>
  );
}

function QuotaDetailMobile({ quota }: { quota: UpstreamQuota }) {
  const remaining = windowRemainingPercent(quota);
  const tone = windowTone(quota);
  return (
    <div className="px-4 py-2.5">
      <div className="flex items-baseline justify-between gap-3">
        <span className="truncate text-[13px] text-fg">{humanize(quota.resource_type)}</span>
        <span className={cn("shrink-0 text-[12px] tabular-nums", tone.text)}>
          {quota.limit > 0 ? `${remaining}% left` : "Unlimited"}
        </span>
      </div>
      <div className="mt-1.5"><UsageBar quota={quota} /></div>
      <div className="mt-1 text-[12px] tabular-nums text-fg-faint">
        {fmtInteger(quota.used)} of {quota.limit > 0 ? fmtInteger(quota.limit) : "unlimited"}
        {" · "}
        {quota.reset_at ? `resets ${formatCountdown(quota.reset_at)}` : "no reset reported"}
      </div>
    </div>
  );
}

function QuotaDetailRow({ quota }: { quota: UpstreamQuota }) {
  const remaining = windowRemainingPercent(quota);
  const tone = windowTone(quota);
  return (
    <tr>
      <td className="px-4 py-2.5 text-fg">{humanize(quota.resource_type)}</td>
      <td className="px-3 py-2.5"><UsageBar quota={quota} /></td>
      <td className="px-3 py-2.5 text-right tabular-nums text-fg-muted">
        {fmtInteger(quota.used)} / {quota.limit > 0 ? fmtInteger(quota.limit) : "Unlimited"}
      </td>
      <td className={cn("px-3 py-2.5 text-right tabular-nums", tone.text)}>{quota.limit > 0 ? `${remaining}%` : "Unlimited"}</td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg-muted" title={quota.reset_at ? formatDateTime(quota.reset_at) : undefined}>
        {quota.reset_at ? formatCountdown(quota.reset_at) : "—"}
      </td>
    </tr>
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

function windowRemainingPercent(quota: UpstreamQuota): number {
  return quota.limit > 0 ? Math.max(0, Math.min(100, Math.round((quota.remaining / quota.limit) * 100))) : 100;
}

function windowUsedPercent(quota: UpstreamQuota): number {
  return quota.limit > 0 ? 100 - windowRemainingPercent(quota) : 0;
}

// windowTone colours a window by consumption: exhausted uses the same
// threshold as the depleted-account logic, near-limit starts at 80% used.
function windowTone(quota: UpstreamQuota): { bar: string; text: string } {
  if (quota.limit <= 0) return { bar: "bg-accent-500", text: "text-fg-faint" };
  if ((quota.remaining / quota.limit) * 100 < DEPLETED_THRESHOLD) return { bar: "bg-bad", text: "text-bad" };
  if (windowUsedPercent(quota) >= NEAR_LIMIT_USED) return { bar: "bg-warn", text: "text-warn" };
  return { bar: "bg-accent-500", text: "text-fg-muted" };
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
