import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, Copy, Info, RefreshCw, Search, X } from "lucide-react";
import {
  api,
  connectUsageStream,
  type HealthOverview,
  type HealthProviderRow,
  type ModelUsage,
  type PricingStatus,
  type ProviderUsage,
  type RecentActivity,
  type UsageInsights,
  type UsageSource,
  type UsageTerminalStatus,
} from "../lib/api";
import { REPORT_PERIODS } from "../lib/periods";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { Badge, Button, ErrorBanner, ErrorCard, Skeleton, TablePagination, useClientPagination } from "../components/ui";
import { HealthStatusBadge } from "../components/HealthBadge";
import { ProviderLogo } from "../components/ProviderLogo";
import { TrafficCard } from "../components/charts/TrafficChart";
import { TokenSavingsBreakdown, prettyClient } from "../components/SavingsBreakdown";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "../components/ui/sheet";
import { useToast } from "../components/Toast";

// Health telemetry is windowed differently from usage; pick the closest range.
const HEALTH_RANGE: Record<string, string> = { today: "24h", "24h": "24h", "7d": "7d", "30d": "30d", "90d": "30d" };
// Trend granularity per period, matching the Overview chart.
const TREND_BUCKETS: Record<string, number> = { today: 24, "24h": 24, "7d": 42, "30d": 30, "90d": 45 };
const USAGE_REFRESH_DEBOUNCE_MS = 8_000;

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";
const SEARCH_INPUT =
  "h-8 w-full rounded-lg border border-input bg-surface pl-8 text-[13px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

type Tab = "overview" | "providers" | "models" | "requests";
const TABS: { value: Tab; label: string }[] = [
  { value: "overview", label: "Overview" },
  { value: "providers", label: "Providers" },
  { value: "models", label: "Models" },
  { value: "requests", label: "Requests" },
];

// Arrow / Home / End move between the radios or tabs of a group and select
// the one they land on (roving tabindex, as the ARIA patterns expect).
function onGroupArrowKeys(event: ReactKeyboardEvent<HTMLElement>, role: "radio" | "tab") {
  if (!["ArrowRight", "ArrowLeft", "ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>(`[role="${role}"]`));
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  if (current < 0 || items.length === 0) return;
  event.preventDefault();
  const forward = event.key === "ArrowRight" || event.key === "ArrowDown";
  const next =
    event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (current + (forward ? 1 : -1) + items.length) % items.length;
  items[next]?.focus();
  items[next]?.click();
}

export function UsagePage() {
  const [params, setParams] = useSearchParams();
  const period = params.get("period") ?? "today";
  const tab = (params.get("tab") as Tab | null) ?? "overview";
  const setParam = (key: string, value: string, fallback: string) =>
    setParams((p) => {
      if (value === fallback) p.delete(key);
      else p.set(key, value);
      return p;
    }, { replace: true });

  const queryClient = useQueryClient();
  const toast = useToast();
  const refreshTimer = useRef<number | null>(null);
  const healthRange = HEALTH_RANGE[period] ?? "24h";
  const tabsId = useId();

  const insights = useQuery({
    queryKey: ["usage-insights", period],
    queryFn: () => api.usageInsights(period, { limit: 200, buckets: TREND_BUCKETS[period] ?? 24 }),
    staleTime: 12_000,
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  });
  const modelUsage = useQuery({
    queryKey: ["usage-models", period],
    queryFn: () => api.modelUsage(period),
    staleTime: 12_000,
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  });
  const health = useQuery({
    queryKey: ["health-overview", healthRange],
    queryFn: () => api.healthOverview(healthRange),
    staleTime: 15_000,
    refetchInterval: 30_000,
    retry: 1,
  });

  useEffect(() => {
    const scheduleRefresh = () => {
      if (refreshTimer.current != null) return;
      refreshTimer.current = window.setTimeout(() => {
        refreshTimer.current = null;
        queryClient.invalidateQueries({ queryKey: ["usage-insights", period] });
        queryClient.invalidateQueries({ queryKey: ["usage-models", period] });
      }, USAGE_REFRESH_DEBOUNCE_MS);
    };
    return connectUsageStream(scheduleRefresh);
  }, [period, queryClient]);

  useEffect(
    () => () => {
      if (refreshTimer.current != null) window.clearTimeout(refreshTimer.current);
    },
    [],
  );

  const handleRefresh = () => {
    queryClient.invalidateQueries({ queryKey: ["usage-insights", period] });
    queryClient.invalidateQueries({ queryKey: ["usage-models", period] });
    queryClient.invalidateQueries({ queryKey: ["health-overview", healthRange] });
    toast.success("Refreshing usage");
  };
  const refreshing = insights.isFetching || modelUsage.isFetching || health.isFetching;
  const data = insights.data;
  const setTab = (t: Tab) => setParam("tab", t, "overview");

  return (
    <>
      <PageHeader
        title="Usage"
        description="Cost, speed and routing for every request."
        action={
          <>
            <div
              className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5"
              role="radiogroup"
              aria-label="Period"
              onKeyDown={(e) => onGroupArrowKeys(e, "radio")}
            >
              {REPORT_PERIODS.map((p) => (
                <button
                  key={p.value}
                  type="button"
                  role="radio"
                  aria-checked={period === p.value}
                  tabIndex={period === p.value ? 0 : -1}
                  onClick={() => setParam("period", p.value, "today")}
                  className={cn(
                    "h-full min-w-8 rounded-lg px-2.5 text-[12px] font-medium transition-colors",
                    FOCUS_RING,
                    period === p.value ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
                    p.value !== "today" && "font-mono",
                  )}
                >
                  {p.label}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={handleRefresh}
              aria-label="Refresh usage"
              aria-busy={refreshing}
              className={cn("inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
            >
              <RefreshCw className={cn("h-4 w-4", refreshing && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
            </button>
          </>
        }
      />

      {insights.isLoading ? (
        <div className="space-y-5" aria-busy="true">
          <span className="sr-only" role="status">Loading usage</span>
          <Skeleton className="h-[86px] w-full rounded-2xl" />
          <Skeleton className="h-[360px] w-full rounded-2xl" />
        </div>
      ) : insights.isError || !data ? (
        <ErrorCard message="Couldn't load usage analytics. Check that the gateway is running, then refresh." />
      ) : (
        <div className="space-y-5">
          <KpiStrip data={data} />
          <PricingCoverageNotice summary={data.summary} />

          <div className="flex gap-1 overflow-x-auto border-b border-line" role="tablist" aria-label="Usage views" onKeyDown={(e) => onGroupArrowKeys(e, "tab")}>
            {TABS.map((t) => {
              const count =
                t.value === "providers"
                  ? data.providers.filter((p) => p.total_requests > 0).length
                  : t.value === "models"
                    ? (modelUsage.data?.models.length ?? null)
                    : t.value === "requests"
                      ? data.recent.length
                      : null;
              const active = tab === t.value;
              return (
                <button
                  key={t.value}
                  id={`${tabsId}-tab-${t.value}`}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  aria-controls={`${tabsId}-panel`}
                  tabIndex={active ? 0 : -1}
                  onClick={() => setTab(t.value)}
                  className={cn(
                    "relative inline-flex items-center gap-1.5 whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors",
                    FOCUS_RING,
                    active ? "text-fg" : "text-fg-muted hover:text-fg",
                  )}
                >
                  {t.label}
                  {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{count}</span>}
                  {active && <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" aria-hidden="true" />}
                </button>
              );
            })}
          </div>

          <div id={`${tabsId}-panel`} role="tabpanel" aria-labelledby={`${tabsId}-tab-${tab}`}>
            {tab === "overview" && (
              <div className="space-y-5">
                <div className="grid gap-5 xl:grid-cols-3">
                  <TrafficCard data={data} className="xl:col-span-2" />
                  <ProviderDistribution providers={data.providers} onShowAll={() => setTab("providers")} />
                </div>
                <TokenSavingsBreakdown savings={data.savings} totalRequests={data.summary.total_requests} insights={data} period={period} />
              </div>
            )}
            {tab === "providers" && <ProviderAccounting providers={data.providers} health={health.data} healthLoading={health.isLoading} healthError={health.isError} />}
            {tab === "models" && <ModelUsageTable models={modelUsage.data?.models ?? []} loading={modelUsage.isLoading} error={modelUsage.isError} />}
            {tab === "requests" && <RecentRequests records={data.recent} />}
          </div>
        </div>
      )}
    </>
  );
}

// ── KPI strip ────────────────────────────────────────────────────────────────

function KpiStrip({ data }: { data: UsageInsights }) {
  const s = data.summary;
  const headingId = useId();
  const lowSuccess = s.total_requests > 0 && s.success_rate < 0.95;
  const cells: { label: string; value: string; hint: ReactNode }[] = [
    {
      label: "Requests",
      value: fmtInteger(s.total_requests),
      hint: (
        <>
          <span className={cn(lowSuccess && "text-warn")}>{s.total_requests ? fmtRatio(s.success_rate) : "—"} success</span> · {fmtInteger(s.failed_requests)} failed
        </>
      ),
    },
    {
      label: "Spend",
      value: fmtUSD(s.cost_usd),
      hint: (
        <>
          {fmtUSD(s.cost_per_request_usd)} per request
          {s.unpriced_requests > 0 && <span className="text-warn"> · {fmtInteger(s.unpriced_requests)} unpriced</span>}
        </>
      ),
    },
    { label: "Tokens", value: fmtCompact(s.total_tokens), hint: `${fmtCompact(s.prompt_tokens)} in · ${fmtCompact(s.completion_tokens)} out` },
    { label: "Latency p50", value: fmtMs(s.p50_latency_ms), hint: `p95 ${fmtMs(s.p95_latency_ms)} · TTFT ${fmtMs(s.avg_ttft_ms)}` },
  ];
  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-line shadow-[var(--shadow-card)]">
      <h2 id={headingId} className="sr-only">Key metrics</h2>
      <dl className="grid grid-cols-1 gap-px sm:grid-cols-2 xl:grid-cols-4">
        {cells.map((c) => (
          <div key={c.label} className="min-w-0 bg-surface px-4 py-3">
            <dt className="text-[12px] font-medium text-fg-muted">{c.label}</dt>
            <dd className="mt-1 truncate text-[22px] font-semibold tracking-[-0.02em] tabular-nums text-fg">{c.value}</dd>
            <dd className="mt-0.5 truncate text-[12px] tabular-nums text-fg-muted">{c.hint}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

// ── Accounting quality ───────────────────────────────────────────────────────

function PricingCoverageNotice({ summary }: { summary: UsageInsights["summary"] }) {
  const notices = [
    summary.unpriced_requests > 0 && {
      label: "Missing pricing",
      detail: `${fmtInteger(summary.unpriced_requests)} requests (${fmtInteger(summary.unpriced_tokens)} tokens) have no rate. Cost excludes them instead of counting them as free.`,
    },
    summary.estimated_requests > 0 && {
      label: "Pricing estimate",
      detail: `${fmtInteger(summary.estimated_requests)} requests use an alias, catalog fallback or retail-equivalent rate.`,
    },
    summary.estimated_usage_requests > 0 && {
      label: "Usage estimate",
      detail: `${fmtInteger(summary.estimated_usage_requests)} requests (${fmtInteger(summary.estimated_usage_tokens)} tokens) use estimated token counts.`,
    },
    summary.legacy_usage_requests > 0 && {
      label: "Legacy usage",
      detail: `${fmtInteger(summary.legacy_usage_requests)} requests (${fmtInteger(summary.legacy_usage_tokens)} tokens) predate full provenance. Totals are kept, without a breakdown.`,
    },
    summary.backfilled_requests > 0 && {
      label: "Historical backfill",
      detail: `${fmtInteger(summary.backfilled_requests)} requests were priced later with current rates, not request-time snapshots.`,
    },
  ].filter((n): n is { label: string; detail: string } => Boolean(n));
  if (notices.length === 0) return null;
  const caution = summary.unpriced_requests > 0 || summary.legacy_usage_requests > 0 || summary.backfilled_requests > 0;

  return (
    <details className={cn("group overflow-hidden rounded-2xl border bg-surface", caution ? "border-warn/30" : "border-line")}>
      <summary
        className={cn(
          "flex min-h-10 cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-1 rounded-2xl px-4 py-2 [&::-webkit-details-marker]:hidden",
          "focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500",
        )}
      >
        {caution ? (
          <AlertTriangle className="h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
        ) : (
          <Info className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
        )}
        <span className="flex-1 text-[13px] font-medium text-fg">
          Accounting notes <span className="ml-1 rounded-md bg-subtle px-1.5 text-[11.5px] font-normal tabular-nums text-fg-muted">{notices.length}</span>
        </span>
        <span className="text-[12px] tabular-nums text-fg-muted">
          Priced <span className="text-fg">{fmtRatio(summary.pricing_request_coverage)}</span> of requests · <span className="text-fg">{fmtRatio(summary.pricing_token_coverage)}</span> of tokens
        </span>
        <ChevronDown className="h-4 w-4 text-fg-faint transition-transform group-open:rotate-180" aria-hidden="true" />
      </summary>
      <div className="border-t border-line px-4 py-3">
        <ul className="grid gap-2.5 text-[12.5px] leading-5 text-fg-muted lg:grid-cols-2">
          {notices.map((n) => (
            <li key={n.label}>
              <span className="font-medium text-fg">{n.label}.</span> {n.detail}
            </li>
          ))}
        </ul>
        <p className="mt-3 border-t border-line pt-2 text-[12px] text-fg-muted">
          Coverage counts {fmtInteger(summary.pricing_eligible_requests)} pricing-eligible request{summary.pricing_eligible_requests === 1 ? "" : "s"}.
        </p>
      </div>
    </details>
  );
}

// ── Shared chrome ────────────────────────────────────────────────────────────

function Panel({ title, action, children, className }: { title: string; action?: ReactNode; children: ReactNode; className?: string }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={cn("min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
        <h2 id={id} className="text-[13px] font-semibold text-fg">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}

function Empty({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="px-6 py-10 text-center">
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {action && <div className="mt-3">{action}</div>}
    </div>
  );
}

const TH = "px-4 py-2 font-medium";
const THEAD = "border-b border-line bg-subtle text-left text-[12px] text-fg-faint";

// ── Provider distribution ────────────────────────────────────────────────────

function ProviderDistribution({ providers, onShowAll }: { providers: ProviderUsage[]; onShowAll: () => void }) {
  const active = providers.filter((p) => p.total_requests > 0).sort((a, b) => b.total_requests - a.total_requests);
  return (
    <Panel title="Where requests went">
      {active.length === 0 ? (
        <Empty title="No provider traffic in this period" />
      ) : (
        <>
          <ul className="space-y-3 px-4 py-4">
            {active.slice(0, 8).map((p) => (
              <li key={p.provider} className="flex items-center gap-3">
                <ProviderLogo icon={p.icon} name={p.display_name || p.provider} size={22} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline justify-between gap-3 text-[13px]">
                    <span className="truncate font-medium text-fg">{p.display_name || p.provider}</span>
                    <span className="tabular-nums text-fg">{p.share_pct.toFixed(1)}%</span>
                  </div>
                  <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-track" aria-hidden="true">
                    <div className="h-full rounded-full bg-accent-500" style={{ width: `${p.share_pct}%` }} />
                  </div>
                  <p className="mt-1 text-[11.5px] tabular-nums text-fg-muted">
                    {fmtInteger(p.total_requests)} req · {p.token_share_pct.toFixed(1)}% of tokens · {fmtUSD(p.cost_usd)}
                  </p>
                </div>
              </li>
            ))}
          </ul>
          {active.length > 8 && (
            <div className="border-t border-line px-4 py-2">
              <button type="button" onClick={onShowAll} className={cn("inline-flex min-h-6 items-center rounded-md text-[12px] font-medium text-link hover:underline", FOCUS_RING)}>
                Show all {active.length} providers
              </button>
            </div>
          )}
        </>
      )}
    </Panel>
  );
}

// ── Provider accounting (with health) ────────────────────────────────────────

function ProviderAccounting({
  providers,
  health,
  healthLoading,
  healthError,
}: {
  providers: ProviderUsage[];
  health?: HealthOverview;
  healthLoading: boolean;
  healthError: boolean;
}) {
  const rows = providers.filter((p) => p.total_requests > 0).sort((a, b) => b.total_requests - a.total_requests);
  const healthBy = new Map<string, HealthProviderRow>((health?.providers ?? []).map((h) => [h.provider, h]));
  const dropped = health?.summary.telemetry_dropped ?? 0;
  return (
    <div className="space-y-3">
      {healthError && <ErrorBanner message="Health data is unavailable right now. Usage figures below are complete." />}
      {dropped > 0 && (
        <p role="status" className="flex items-start gap-2 rounded-2xl border border-warn/30 bg-warn/5 px-4 py-2.5 text-[12.5px] text-fg">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
          {fmtInteger(dropped)} health events were dropped, so health may be incomplete. Usage figures are unaffected.
        </p>
      )}
      <Panel
        title="Provider accounting"
        action={
          <Link to="/provider-health" className={cn("inline-flex min-h-6 items-center rounded-md text-[12px] font-medium text-link hover:underline", FOCUS_RING)}>
            Provider health
          </Link>
        }
      >
        {rows.length === 0 ? (
          <Empty title="No provider usage in this period" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <th scope="col" className={TH}>Provider</th>
                  <th scope="col" className={cn(TH, "text-right")}>Requests</th>
                  <th scope="col" className={cn(TH, "text-right")}>Input</th>
                  <th scope="col" className={cn(TH, "text-right")}>Output</th>
                  <th scope="col" className={cn(TH, "text-right")}>Cost</th>
                  <th scope="col" className={cn(TH, "text-right")}>Latency</th>
                  <th scope="col" className={cn(TH, "text-right")}>Priced</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {rows.map((p) => {
                  const h = healthBy.get(p.provider);
                  return (
                    <tr key={p.provider} className="transition-colors hover:bg-hover">
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2.5">
                          <ProviderLogo icon={p.icon} name={p.display_name || p.provider} size={22} />
                          <div className="min-w-0">
                            <Link to={`/providers/${p.provider}`} className={cn("block truncate rounded-sm font-medium text-fg hover:underline", FOCUS_RING)}>
                              {p.display_name || p.provider}
                            </Link>
                            <div className="mt-0.5">
                              {h ? (
                                <HealthStatusBadge status={h.status} issue={h.main_issue} />
                              ) : (
                                <span className="text-[11.5px] text-fg-muted">{healthLoading ? "Checking health…" : "No health data"}</span>
                              )}
                            </div>
                          </div>
                        </div>
                      </td>
                      <Cell main={fmtInteger(p.total_requests)} sub={`${fmtRatio(p.success_rate)} ok · ${fmtInteger(p.failed_requests)} failed`} />
                      <Cell main={fmtCompact(p.prompt_tokens)} sub={`${fmtCompact(p.cached_tokens)} cache read · ${fmtCompact(p.cache_write_tokens)} write`} />
                      <Cell main={fmtCompact(p.completion_tokens)} sub={`${fmtCompact(p.reasoning_tokens)} reasoning`} />
                      <Cell main={fmtUSD(p.cost_usd)} sub={`${fmtUSD(p.saved_cost_usd + p.avoided_cost_usd)} saved`} />
                      <Cell main={fmtMs(p.avg_latency_ms)} sub={`TTFT ${fmtMs(p.avg_ttft_ms)}${h?.latency_p95_ms ? ` · p95 ${fmtMs(h.latency_p95_ms)}` : ""}`} />
                      <td
                        className="px-4 py-2.5 text-right tabular-nums"
                        title={`${fmtInteger(p.estimated_requests)} pricing estimates · ${fmtInteger(p.estimated_usage_requests)} usage estimates · ${fmtInteger(p.legacy_usage_requests)} legacy · ${fmtInteger(p.backfilled_requests)} backfilled`}
                      >
                        <div className={cn(p.pricing_request_coverage != null && p.pricing_request_coverage < 1 ? "text-warn" : "text-fg")}>{fmtRatio(p.pricing_request_coverage)}</div>
                        <div className="text-[11.5px] text-fg-muted">
                          {p.pricing_eligible_requests > 0 ? `${fmtInteger(Math.max(0, p.pricing_eligible_requests - p.unpriced_requests))} / ${fmtInteger(p.pricing_eligible_requests)}` : "Nothing to price"}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

function Cell({ main, sub }: { main: ReactNode; sub?: string }) {
  return (
    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
      <div className="text-fg">{main}</div>
      {sub && <div className="text-[11.5px] text-fg-muted">{sub}</div>}
    </td>
  );
}

// ── Models ───────────────────────────────────────────────────────────────────

type ModelSortKey = "model" | "requests" | "tokens" | "cost" | "latency" | "coverage";

function ModelUsageTable({ models, loading, error }: { models: ModelUsage[]; loading: boolean; error: boolean }) {
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<ModelSortKey>("cost");
  const [dir, setDir] = useState<"asc" | "desc">("desc");

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    const rows = q ? models.filter((m) => `${m.provider} ${m.provider_name} ${m.model}`.toLowerCase().includes(q)) : models;
    return rows.slice().sort((a, b) => {
      const d = dir === "asc" ? 1 : -1;
      switch (sortKey) {
        case "model":
          return d * `${a.provider}/${a.model}`.localeCompare(`${b.provider}/${b.model}`);
        case "requests":
          return d * (a.total_requests - b.total_requests);
        case "tokens":
          return d * (a.total_tokens - b.total_tokens);
        case "cost":
          return d * (a.cost_usd - b.cost_usd);
        case "latency":
          return d * (a.avg_latency_ms - b.avg_latency_ms);
        case "coverage":
          if (a.pricing_request_coverage == null && b.pricing_request_coverage == null) return 0;
          if (a.pricing_request_coverage == null) return 1;
          if (b.pricing_request_coverage == null) return -1;
          return d * (a.pricing_request_coverage - b.pricing_request_coverage);
      }
    });
  }, [models, search, dir, sortKey]);
  const { page, pages, paged, setPage, total } = useClientPagination(filtered, 15);

  const sort = (key: ModelSortKey) => {
    if (sortKey === key) setDir((c) => (c === "asc" ? "desc" : "asc"));
    else {
      setSortKey(key);
      setDir(key === "model" ? "asc" : "desc");
    }
  };

  return (
    <Panel
      title="Model accounting"
      action={
        <div className="relative w-full sm:w-64">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          <input
            type="search"
            aria-label="Filter models by provider or name"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(1);
            }}
            placeholder="Filter provider or model"
            className={cn(SEARCH_INPUT, "pr-3")}
          />
        </div>
      }
    >
      {!loading && !error && (
        <p role="status" className="sr-only">
          {search.trim() ? `${filtered.length} of ${models.length} models match` : `${models.length} models`}
        </p>
      )}
      {loading ? (
        <div className="space-y-2 p-4" aria-busy="true">
          <span className="sr-only">Loading models</span>
          {[0, 1, 2, 3, 4].map((i) => <Skeleton key={i} className="h-9 w-full" />)}
        </div>
      ) : error ? (
        <div className="p-4">
          <ErrorBanner message="Couldn't load model usage. Refresh to try again." />
        </div>
      ) : filtered.length === 0 ? (
        <Empty
          title={models.length === 0 ? "No model usage in this period" : "No models match"}
          action={models.length > 0 ? <Button variant="secondary" onClick={() => setSearch("")}>Clear filter</Button> : undefined}
        />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[880px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <SortableHeader label="Model" k="model" active={sortKey} dir={dir} onSort={sort} />
                  <SortableHeader label="Requests" k="requests" active={sortKey} dir={dir} onSort={sort} right />
                  <SortableHeader label="Tokens" k="tokens" active={sortKey} dir={dir} onSort={sort} right />
                  <SortableHeader label="Cost" k="cost" active={sortKey} dir={dir} onSort={sort} right />
                  <SortableHeader label="Latency" k="latency" active={sortKey} dir={dir} onSort={sort} right />
                  <SortableHeader label="Pricing" k="coverage" active={sortKey} dir={dir} onSort={sort} right />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {paged.map((m) => (
                  <tr key={`${m.provider}/${m.model}`} className="transition-colors hover:bg-hover">
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-2.5">
                        <ProviderLogo icon={m.provider_icon} name={m.provider_name || m.provider} size={22} />
                        <div className="min-w-0">
                          <div className="max-w-xs truncate font-mono text-[12.5px] text-fg" title={m.model}>
                            {m.model}
                          </div>
                          <div className="truncate text-[11.5px] text-fg-muted">{m.provider_name || m.provider}</div>
                        </div>
                      </div>
                    </td>
                    <Cell main={fmtInteger(m.total_requests)} sub={`${fmtRatio(m.success_rate)} ok`} />
                    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums" title={`${fmtCompact(m.cached_tokens)} cached · ${fmtCompact(m.reasoning_tokens)} reasoning`}>
                      <div className="text-fg">{fmtCompact(m.total_tokens)}</div>
                      <div className="text-[11.5px] text-fg-muted">
                        {fmtCompact(m.prompt_tokens)} in · {fmtCompact(m.completion_tokens)} out
                      </div>
                    </td>
                    <Cell main={m.pricing_status === "missing" ? "Unpriced" : fmtUSD(m.cost_usd)} sub={`${fmtUSD(m.saved_cost_usd + m.avoided_cost_usd)} saved`} />
                    <Cell main={fmtMs(m.avg_latency_ms)} sub={`TTFT ${fmtMs(m.avg_ttft_ms)}`} />
                    <td
                      className="px-4 py-2.5 text-right"
                      title={[
                        m.pricing_key ? `Key ${m.pricing_key}` : "No pricing key",
                        `${fmtInteger(m.estimated_requests)} pricing estimates · ${fmtInteger(m.estimated_usage_requests)} usage estimates`,
                        `${fmtInteger(m.legacy_usage_requests)} legacy · ${fmtInteger(m.backfilled_requests)} backfilled`,
                      ].join("\n")}
                    >
                      <div className="flex justify-end">
                        <PricingBadge status={m.pricing_status} />
                      </div>
                      <div className="mt-1 whitespace-nowrap text-[11.5px] tabular-nums text-fg-muted">
                        {m.pricing_mixed ? <span className="text-warn">Several pricing snapshots</span> : formatRates(m)}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {pages > 1 && <TablePagination page={page} pages={pages} total={total} onPage={setPage} />}
        </>
      )}
    </Panel>
  );
}

function SortableHeader({ label, k, active, dir, onSort, right }: { label: string; k: ModelSortKey; active: ModelSortKey; dir: "asc" | "desc"; onSort: (k: ModelSortKey) => void; right?: boolean }) {
  const Icon = active !== k ? ArrowUpDown : dir === "asc" ? ArrowUp : ArrowDown;
  return (
    <th scope="col" aria-sort={active === k ? (dir === "asc" ? "ascending" : "descending") : "none"} className={cn(TH, right && "text-right")}>
      <button
        type="button"
        onClick={() => onSort(k)}
        className={cn("inline-flex min-h-6 items-center gap-1 rounded-md hover:text-fg", FOCUS_RING, right && "flex-row-reverse", active === k && "text-fg")}
      >
        {label}
        <Icon className={cn("h-3 w-3", active === k ? "opacity-100" : "opacity-40")} aria-hidden="true" />
      </button>
    </th>
  );
}

function formatRates(m: ModelUsage) {
  if (m.pricing_status === "legacy") return "Legacy total";
  if (m.pricing_status === "missing" || m.pricing_status === "none") return `${fmtRatio(m.pricing_request_coverage)} covered`;
  return `${fmtRate(m.input_per_m)} in · ${fmtRate(m.output_per_m)} out /M`;
}

// ── Requests ─────────────────────────────────────────────────────────────────

type StatusFilter = "all" | "success" | "cache_hit" | "failed" | "blocked";
const STATUS_FILTERS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "success", label: "Succeeded" },
  { value: "cache_hit", label: "Cache hits" },
  { value: "failed", label: "Failed" },
  { value: "blocked", label: "Blocked" },
];

function RecentRequests({ records }: { records: RecentActivity[] }) {
  const [selected, setSelected] = useState<RecentActivity | null>(null);
  const [status, setStatus] = useState<StatusFilter>("all");
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return records.filter((r) => {
      if (status !== "all" && r.status !== status) return false;
      if (!q) return true;
      return `${r.model} ${r.provider} ${r.api_key_name} ${r.client} ${r.request_id}`.toLowerCase().includes(q);
    });
  }, [records, status, query]);
  const { page, pages, paged, setPage, total } = useClientPagination(filtered, 20);
  useEffect(() => setPage(1), [status, query, setPage]);
  const filtering = status !== "all" || query.trim() !== "";
  const clearFilters = () => {
    setStatus("all");
    setQuery("");
  };

  return (
    <>
      <Panel
        title="Recent requests"
        action={
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-full sm:w-56">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                type="search"
                aria-label="Filter requests by model, key, client or request ID"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Model, key, client or ID"
                className={cn(SEARCH_INPUT, "pr-8 [&::-webkit-search-cancel-button]:hidden")}
              />
              {query && (
                <button
                  type="button"
                  onClick={() => setQuery("")}
                  aria-label="Clear search"
                  className={cn("absolute right-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-muted hover:text-fg", FOCUS_RING)}
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </div>
            <div className="flex flex-wrap gap-1" role="radiogroup" aria-label="Status" onKeyDown={(e) => onGroupArrowKeys(e, "radio")}>
              {STATUS_FILTERS.map((f) => (
                <button
                  key={f.value}
                  type="button"
                  role="radio"
                  aria-checked={status === f.value}
                  tabIndex={status === f.value ? 0 : -1}
                  onClick={() => setStatus(f.value)}
                  className={cn(
                    "h-8 rounded-lg border px-2.5 text-[12px] font-medium transition-colors",
                    FOCUS_RING,
                    status === f.value ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:text-fg",
                  )}
                >
                  {f.label}
                </button>
              ))}
            </div>
          </div>
        }
      >
        <p role="status" className="sr-only">
          {filtering ? `${filtered.length} of ${records.length} requests match` : `${records.length} requests`}
        </p>
        {filtered.length === 0 ? (
          <Empty
            title={records.length === 0 ? "No requests in this period" : "No requests match"}
            action={records.length > 0 ? <Button variant="secondary" onClick={clearFilters}>Clear filters</Button> : undefined}
          />
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[820px] text-[13px]">
                <thead>
                  <tr className={THEAD}>
                    <th scope="col" className={TH}>Time</th>
                    <th scope="col" className={TH}>Status</th>
                    <th scope="col" className={TH}>Model</th>
                    <th scope="col" className={cn(TH, "text-right")}>Tokens in → out</th>
                    <th scope="col" className={cn(TH, "text-right")}>Cost</th>
                    <th scope="col" className={cn(TH, "text-right")}>Latency</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {paged.map((r) => {
                    const tokenNotes = [
                      r.cached_tokens ? `${fmtCompact(r.cached_tokens)} cached` : "",
                      r.reasoning_tokens ? `${fmtCompact(r.reasoning_tokens)} reasoning` : "",
                    ]
                      .filter(Boolean)
                      .join(" · ");
                    return (
                      // The row is a mouse convenience; the time button is the keyboard / AT path.
                      <tr key={r.id} className="cursor-pointer transition-colors hover:bg-hover" onClick={() => setSelected(r)}>
                        <td className="whitespace-nowrap px-4 py-2.5">
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              setSelected(r);
                            }}
                            title={formatDateTime(r.created_at)}
                            className={cn("inline-flex min-h-6 items-center rounded-md font-mono text-[12px] text-fg-muted hover:text-fg hover:underline", FOCUS_RING)}
                          >
                            {relativeTime(r.created_at)}
                            <span className="sr-only">, {r.model || "unknown model"}: open request details</span>
                          </button>
                        </td>
                        <td className="px-4 py-2.5">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <StatusBadge status={r.status} />
                            {r.usage_source !== "provider" && <UsageSourceBadge source={r.usage_source} />}
                          </div>
                        </td>
                        <td className="max-w-[300px] px-4 py-2.5">
                          <div className="flex items-center gap-2.5">
                            <ProviderLogo icon={r.provider_icon} name={r.provider_name || r.provider} size={20} />
                            <div className="min-w-0">
                              <div className="truncate font-mono text-[12.5px] text-fg" title={`${r.provider}/${r.model}`}>
                                {r.model || "—"}
                              </div>
                              {(r.api_key_name || r.client) && (
                                <div className="truncate text-[11.5px] text-fg-muted">
                                  {r.api_key_name && <span className="font-mono">{r.api_key_name}</span>}
                                  {r.api_key_name && r.client && " · "}
                                  {r.client && prettyClient(r.client)}
                                </div>
                              )}
                            </div>
                          </div>
                        </td>
                        <Cell main={`${fmtInteger(r.prompt_tokens)} → ${fmtInteger(r.completion_tokens)}`} sub={tokenNotes || undefined} />
                        <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                          <div className={cn(r.pricing_status === "missing" ? "text-warn" : "text-fg")}>{r.pricing_status === "missing" ? "Unpriced" : r.cache_hit ? "$0.00" : fmtUSD(r.cost_usd)}</div>
                          {r.pricing_status !== "priced" && r.pricing_status !== "missing" && <div className="text-[11.5px] text-fg-muted">{humanize(r.pricing_status)}</div>}
                        </td>
                        <Cell main={fmtMs(r.end_to_end_latency_ms)} sub={`upstream ${fmtMs(r.upstream_latency_ms)}`} />
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {pages > 1 && <TablePagination page={page} pages={pages} total={total} onPage={setPage} />}
          </>
        )}
      </Panel>

      <Sheet open={selected != null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent side="right" className="w-full max-w-[640px]">
          {selected && <RequestDetail record={selected} onClose={() => setSelected(null)} />}
        </SheetContent>
      </Sheet>
    </>
  );
}

// ── Request detail drawer ────────────────────────────────────────────────────

type RequestNotice = { title: string; message: string; tone: "info" | "warning" | "danger"; pricing?: boolean };
type DetailTab = "overview" | "pricing";
const DETAIL_TABS: { value: DetailTab; label: string }[] = [
  { value: "overview", label: "Overview" },
  { value: "pricing", label: "Pricing & audit" },
];

function RequestDetail({ record, onClose }: { record: RecentActivity; onClose: () => void }) {
  const [tab, setTab] = useState<DetailTab>("overview");
  const tabsId = useId();
  const flags = [
    record.slim_active && "RTK",
    record.caveman_active && "Caveman",
    record.terse_active && "Terse",
    record.headroom_active && "Headroom",
    record.ponytail_active && "Ponytail",
  ].filter((v): v is string => Boolean(v));
  const legacyBreakdownUnavailable = record.pricing_status === "legacy" && !record.pricing_backfilled;
  const cacheHit = record.cache_hit || record.status === "cache_hit" || record.usage_source === "cache";
  const sourceURL = safeExternalURL(record.pricing_source_url);
  const totalTokens = record.prompt_tokens + record.completion_tokens;
  const regularInput = Math.max(0, record.prompt_tokens - record.cached_tokens - record.cache_write_tokens);
  const regularOutput = Math.max(0, record.completion_tokens - record.reasoning_tokens);
  const pricingUnavailable = record.pricing_status === "missing";
  const hasOptimization =
    flags.length > 0 || record.slim_tokens_saved > 0 || record.slim_bytes_saved > 0 || record.headroom_tokens_saved > 0 || record.headroom_bytes_saved > 0 || Boolean(record.slim_rules);
  const charged = pricingUnavailable ? "Unpriced" : cacheHit ? "$0.00" : fmtUSD(record.cost_usd);

  const notices: RequestNotice[] = [];
  if (record.error_kind) notices.push({ title: "Terminal error", message: humanize(record.error_kind), tone: "danger" });
  if (pricingUnavailable) notices.push({ title: "Pricing unavailable", message: "No catalog or custom rate matched, so cost stays unpriced.", tone: "warning", pricing: true });
  if (record.usage_source === "estimated") notices.push({ title: "Estimated usage", message: "The provider didn't report usage, so token counts are estimated.", tone: "info" });
  if (legacyBreakdownUnavailable)
    notices.push({ title: "Legacy accounting", message: `The total ${fmtUSD(record.cost_usd)} was kept; its component and rate split wasn't stored.`, tone: "warning", pricing: true });
  if (record.pricing_backfilled) notices.push({ title: "Historical estimate", message: "Priced later with backfill rates, not the request-time snapshot.", tone: "warning", pricing: true });
  if (record.pricing_status === "partial") notices.push({ title: "Partial pricing", message: "Only matched components are in the charged total.", tone: "warning", pricing: true });
  if (cacheHit) notices.push({ title: "Served from cache", message: "Components show avoided provider spend; the charged total is zero.", tone: "info", pricing: true });
  const shown = tab === "overview" ? notices : notices.filter((n) => n.pricing);

  const mono = (value: ReactNode, key: string) => (
    <span key={key} className="break-all font-mono text-[12px]">
      {value}
    </span>
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b border-line px-5 pb-0 pt-4">
        <div className="flex items-start gap-3">
          <ProviderLogo icon={record.provider_icon} name={record.provider_name || record.provider} size={32} className="mt-0.5" />
          <div className="min-w-0 flex-1">
            <SheetTitle className="truncate font-mono text-[14px] font-medium text-fg" title={record.model}>
              {record.model || "Unknown model"}
            </SheetTitle>
            <SheetDescription className="mt-0.5 text-[12.5px] text-fg-muted">
              {record.provider_name || record.provider} · {formatDateTime(record.created_at)}
            </SheetDescription>
            <div className="mt-2 flex flex-wrap items-center gap-1.5">
              <StatusBadge status={record.status} />
              <UsageSourceBadge source={record.usage_source} />
              <PricingBadge status={record.pricing_status} />
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close request details"
            className={cn("-mr-1.5 flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg", FOCUS_RING)}
          >
            <X className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
        <div className="mt-3 flex gap-1" role="tablist" aria-label="Request details" onKeyDown={(e) => onGroupArrowKeys(e, "tab")}>
          {DETAIL_TABS.map((t) => {
            const active = tab === t.value;
            return (
              <button
                key={t.value}
                id={`${tabsId}-tab-${t.value}`}
                type="button"
                role="tab"
                aria-selected={active}
                aria-controls={`${tabsId}-panel`}
                tabIndex={active ? 0 : -1}
                onClick={() => setTab(t.value)}
                className={cn("relative px-2.5 py-2 text-[13px] font-medium", FOCUS_RING, active ? "text-fg" : "text-fg-muted hover:text-fg")}
              >
                {t.label}
                {active && <span className="absolute inset-x-1.5 bottom-0 h-0.5 rounded-full bg-accent-500" aria-hidden="true" />}
              </button>
            );
          })}
        </div>
      </div>

      <div
        id={`${tabsId}-panel`}
        role="tabpanel"
        aria-labelledby={`${tabsId}-tab-${tab}`}
        tabIndex={0}
        className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      >
        <NoticeList notices={shown} />
        {tab === "overview" ? (
          <>
            <dl className="grid grid-cols-3 gap-px overflow-hidden rounded-xl border border-line bg-line">
              <Metric label="Total tokens" value={fmtInteger(totalTokens)} hint={`${fmtInteger(record.prompt_tokens)} in · ${fmtInteger(record.completion_tokens)} out`} />
              <Metric label="Charged" value={charged} hint={cacheHit ? "Served from cache" : humanize(record.pricing_status)} />
              <Metric label="End to end" value={fmtMs(record.end_to_end_latency_ms)} hint={`TTFT ${fmtMs(record.ttft_ms)}`} />
            </dl>

            <Group title="Request">
              <Rows
                rows={[
                  [
                    "Request ID",
                    record.request_id ? (
                      <span key="r" className="inline-flex items-center justify-end gap-1">
                        {mono(record.request_id, "rid")}
                        <CopyButton value={record.request_id} label="Copy request ID" />
                      </span>
                    ) : (
                      "—"
                    ),
                  ],
                  ["API key", mono(record.api_key_name || record.api_key_id || "—", "k")],
                  ["Client", record.client ? prettyClient(record.client) : "—"],
                  ["Account", record.account_label || mono(record.account_id || "—", "a")],
                ]}
              />
            </Group>

            <Group title="Tokens" hint="Totals include cache and reasoning">
              <div className="grid gap-4 sm:grid-cols-2">
                <Rows
                  head={["Input", fmtInteger(record.prompt_tokens)]}
                  rows={[
                    ["Regular", fmtInteger(regularInput)],
                    ["Cache read", fmtInteger(record.cached_tokens)],
                    ["Cache write", fmtInteger(record.cache_write_tokens)],
                  ]}
                />
                <Rows
                  head={["Output", fmtInteger(record.completion_tokens)]}
                  rows={[
                    ["Regular", fmtInteger(regularOutput)],
                    ["Reasoning", fmtInteger(record.reasoning_tokens)],
                  ]}
                />
              </div>
            </Group>

            <Group
              title={cacheHit ? "Avoided provider cost" : "Cost"}
              hint={
                legacyBreakdownUnavailable
                  ? "Retained total only"
                  : pricingUnavailable
                    ? "Needs a rate to calculate"
                    : cacheHit
                      ? "Avoided, not charged"
                      : record.pricing_backfilled
                        ? "Later backfill rate"
                        : "USD snapshot"
              }
            >
              {legacyBreakdownUnavailable ? (
                <Unavailable label="Legacy total" value={fmtUSD(record.cost_usd)} message="No component split was retained for this row." />
              ) : pricingUnavailable ? (
                <Unavailable label="Charged total" value="Unpriced" message="Components are hidden so missing pricing isn't read as free usage." />
              ) : (
                <>
                  <Rows
                    rows={[
                      ["Regular input", fmtUSD(record.input_cost_usd)],
                      ["Cached input", fmtUSD(record.cached_cost_usd)],
                      ["Cache write", fmtUSD(record.cache_write_cost_usd)],
                      ["Regular output", fmtUSD(record.output_cost_usd)],
                      ["Reasoning", fmtUSD(record.reasoning_cost_usd)],
                    ]}
                  />
                  {(record.saved_cost_usd > 0 || record.avoided_cost_usd > 0) && (
                    <p className="mt-2 text-[12.5px] text-fg-muted">
                      Saved <span className="tabular-nums text-ok">{fmtUSD(record.saved_cost_usd)}</span> by compression · avoided{" "}
                      <span className="tabular-nums text-ok">{fmtUSD(record.avoided_cost_usd)}</span> by cache
                    </p>
                  )}
                </>
              )}
            </Group>

            <Group title="Latency">
              <Rows
                rows={[
                  ["Upstream", fmtMs(record.upstream_latency_ms)],
                  ["End to end", fmtMs(record.end_to_end_latency_ms)],
                  ["Time to first token", fmtMs(record.ttft_ms)],
                ]}
              />
            </Group>

            <Group title="Optimizations">
              {!hasOptimization ? (
                <p className="text-[12.5px] text-fg-muted">None ran on this request.</p>
              ) : (
                <div className="space-y-2.5">
                  {flags.length > 0 && (
                    <ul className="flex flex-wrap gap-1.5" aria-label="Active optimizers">
                      {flags.map((f) => (
                        <li key={f}>
                          <Badge tone="neutral">{f}</Badge>
                        </li>
                      ))}
                    </ul>
                  )}
                  <Rows
                    rows={[
                      ...((record.slim_active || record.slim_tokens_saved > 0 || record.slim_bytes_saved > 0) ? [["RTK saved", `${fmtInteger(record.slim_tokens_saved)} tokens · ${fmtBytes(record.slim_bytes_saved)}`] as [string, string]] : []),
                      ...((record.headroom_active || record.headroom_tokens_saved > 0 || record.headroom_bytes_saved > 0)
                        ? [["Headroom saved", `${fmtInteger(record.headroom_tokens_saved)} tokens · ${fmtBytes(record.headroom_bytes_saved)}`] as [string, string]]
                        : []),
                    ]}
                  />
                  {record.slim_rules && (
                    <p className="rounded-lg bg-subtle px-3 py-2 text-[12px] leading-5">
                      <span className="font-medium text-fg">Rules applied:</span> <span className="break-words font-mono text-fg-muted">{record.slim_rules}</span>
                    </p>
                  )}
                </div>
              )}
            </Group>
          </>
        ) : (
          <>
            <Group title="Pricing provenance">
              <Rows
                rows={[
                  ["Status", <PricingBadge key="s" status={record.pricing_status} />],
                  ["Source", record.pricing_source || "—"],
                  ["Match", humanize(record.pricing_match_kind) || "—"],
                  ["Resolved key", mono(record.pricing_key || "—", "k")],
                  [
                    "Source URL",
                    record.pricing_source_url ? (
                      sourceURL ? (
                        <a key="u" href={sourceURL} target="_blank" rel="noreferrer" title={record.pricing_source_url} className={cn("rounded-sm text-link hover:underline", FOCUS_RING)}>
                          Open pricing source<span className="sr-only"> (opens in a new tab)</span>
                        </a>
                      ) : (
                        <span key="u" className="break-all font-mono text-[12px]">{record.pricing_source_url}</span>
                      )
                    ) : (
                      "—"
                    ),
                  ],
                  ["Pricing as of", record.pricing_as_of ? formatDateTime(record.pricing_as_of) : "—"],
                  ["Backfilled", record.pricing_backfilled ? <Badge key="b" tone="warning">Historical estimate</Badge> : "No"],
                ]}
              />
            </Group>
            <Group title="Rates per 1M tokens" hint="USD">
              {legacyBreakdownUnavailable || pricingUnavailable ? (
                <Unavailable
                  label="Rate snapshot"
                  value="Unavailable"
                  message={legacyBreakdownUnavailable ? "No rate snapshot was stored for this legacy total." : "No matching price was found for this request."}
                />
              ) : (
                <Rows
                  rows={[
                    ["Regular input", fmtRate(record.input_rate_per_m)],
                    ["Cached input", fmtRate(record.cached_rate_per_m)],
                    ["Cache write", fmtRate(record.cache_write_rate_per_m)],
                    ["Regular output", fmtRate(record.output_rate_per_m)],
                    ["Reasoning output", fmtRate(record.reasoning_rate_per_m)],
                  ]}
                />
              )}
            </Group>
            <Group title="Record" hint="For logs and reconciliation">
              <Rows
                rows={[
                  ["Usage row", mono(record.id, "u")],
                  ["Provider", mono(record.provider || "—", "p")],
                  ["Recorded at", formatDateTime(record.created_at)],
                ]}
              />
            </Group>
          </>
        )}
      </div>
    </div>
  );
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const toast = useToast();
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success("Copied", value);
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access. Select the text and copy it manually.");
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label}
      className={cn("flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
    >
      <Copy className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
    </button>
  );
}

function NoticeList({ notices }: { notices: RequestNotice[] }) {
  if (!notices.length) return null;
  const tone = notices.some((n) => n.tone === "danger") ? "danger" : notices.some((n) => n.tone === "warning") ? "warning" : "info";
  return (
    <div className={cn("flex items-start gap-2.5 rounded-xl border px-3.5 py-3", tone === "danger" ? "border-bad/30 bg-bad/5" : tone === "warning" ? "border-warn/30 bg-warn/5" : "border-line bg-subtle")}>
      {tone === "info" ? (
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
      ) : (
        <AlertTriangle className={cn("mt-0.5 h-4 w-4 shrink-0", tone === "danger" ? "text-bad" : "text-warn")} strokeWidth={1.75} aria-hidden="true" />
      )}
      <ul className="min-w-0 space-y-1 text-[12.5px] leading-5 text-fg-muted">
        {notices.map((n) => (
          <li key={`${n.title}-${n.message}`}>
            <span className="font-medium text-fg">{n.title}.</span> {n.message}
          </li>
        ))}
      </ul>
    </div>
  );
}

// Group is a heading + content block inside the drawer: no box, just a hairline
// above, so the drawer reads as one scannable column.
function Group({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="border-t border-line pt-4">
      <div className="mb-2.5 flex items-baseline justify-between gap-3">
        <h3 id={id} className="text-[13px] font-semibold text-fg">{title}</h3>
        {hint && <p className="text-[12px] text-fg-muted">{hint}</p>}
      </div>
      {children}
    </section>
  );
}

function Rows({ head, rows }: { head?: [string, string]; rows: [string, ReactNode][] }) {
  return (
    <div>
      {head && (
        <div className="mb-1.5 flex items-baseline justify-between gap-3">
          <span className="text-[12px] font-medium text-fg-muted">{head[0]}</span>
          <span className="text-[16px] font-semibold tabular-nums text-fg">{head[1]}</span>
        </div>
      )}
      <dl className="divide-y divide-line">
        {rows.map(([label, value]) => (
          <div key={label} className="flex items-start justify-between gap-4 py-1.5 text-[12.5px] first:pt-0 last:pb-0">
            <dt className="shrink-0 text-fg-muted">{label}</dt>
            <dd className="min-w-0 text-right tabular-nums text-fg">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function Metric({ label, value, hint }: { label: string; value: string; hint: string }) {
  return (
    <div className="min-w-0 bg-surface px-3.5 py-2.5">
      <dt className="text-[12px] font-medium text-fg-muted">{label}</dt>
      <dd className="mt-0.5 truncate text-[16px] font-semibold tabular-nums text-fg">{value}</dd>
      <dd className="truncate text-[11.5px] text-fg-muted" title={hint}>
        {hint}
      </dd>
    </div>
  );
}

function Unavailable({ label, value, message }: { label: string; value: string; message: string }) {
  return (
    <div className="rounded-lg bg-subtle px-3.5 py-3">
      <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
        <span className="font-medium text-fg-muted">{label}</span>
        <span className="font-semibold tabular-nums text-fg">{value}</span>
      </div>
      <p className="mt-1.5 text-[12px] leading-5 text-fg-muted">{message}</p>
    </div>
  );
}

// ── Badges ───────────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: UsageTerminalStatus }) {
  const config: Record<UsageTerminalStatus, { label: string; tone: "success" | "accent" | "warning" | "danger" | "neutral" }> = {
    success: { label: "Success", tone: "success" },
    cache_hit: { label: "Cache hit", tone: "accent" },
    blocked: { label: "Blocked", tone: "warning" },
    failed: { label: "Failed", tone: "danger" },
    cancelled: { label: "Cancelled", tone: "neutral" },
  };
  const item = config[status] ?? { label: humanize(String(status)) || "Unknown status", tone: "neutral" as const };
  return <Badge tone={item.tone}>{item.label}</Badge>;
}

function PricingBadge({ status }: { status: PricingStatus }) {
  const config: Record<PricingStatus, { label: string; tone: "success" | "accent" | "warning" | "danger" | "neutral" }> = {
    priced: { label: "Priced", tone: "neutral" },
    estimated: { label: "Pricing estimate", tone: "warning" },
    free: { label: "Free", tone: "neutral" },
    missing: { label: "Missing price", tone: "danger" },
    partial: { label: "Partial", tone: "warning" },
    legacy: { label: "Legacy total", tone: "neutral" },
    none: { label: "No billable usage", tone: "neutral" },
    mixed: { label: "Mixed snapshots", tone: "warning" },
  };
  const item = config[status] ?? { label: humanize(String(status)) || "Unknown pricing", tone: "neutral" as const };
  return <Badge tone={item.tone}>{item.label}</Badge>;
}

function UsageSourceBadge({ source }: { source: UsageSource }) {
  const config: Record<UsageSource, { label: string; tone: "success" | "warning" | "accent" | "neutral" }> = {
    provider: { label: "Provider usage", tone: "neutral" },
    estimated: { label: "Usage estimate", tone: "warning" },
    cache: { label: "Cache replay", tone: "accent" },
    legacy: { label: "Legacy usage", tone: "neutral" },
    none: { label: "No usage reported", tone: "neutral" },
  };
  const item = config[source] ?? { label: humanize(String(source)) || "Unknown usage", tone: "neutral" as const };
  return <Badge tone={item.tone}>{item.label}</Badge>;
}

// ── Formatting ───────────────────────────────────────────────────────────────

function safeExternalURL(value: string) {
  if (!value) return null;
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.toString() : null;
  } catch {
    return null;
  }
}

function fmtInteger(value: number) {
  if (!Number.isFinite(value)) return "—";
  return Math.round(value).toLocaleString("en-US");
}

function fmtCompact(value: number) {
  if (!Number.isFinite(value)) return "—";
  const a = Math.abs(value);
  if (a >= 1e9) return `${(value / 1e9).toFixed(1)}B`;
  if (a >= 1e6) return `${(value / 1e6).toFixed(1)}M`;
  if (a >= 1e3) return `${(value / 1e3).toFixed(1)}k`;
  return Math.round(value).toLocaleString("en-US");
}

// Money reads with two decimals; sub-cent values keep enough precision to be
// non-zero (a cheap request is "$0.0013", not "$0.00").
function fmtUSD(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (value === 0) return "$0.00";
  const a = Math.abs(value);
  if (a < 0.0001) return value > 0 ? "<$0.0001" : ">-$0.0001";
  if (a < 0.01) return `$${value.toFixed(4)}`;
  return `$${value.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function fmtRate(value: number) {
  if (!Number.isFinite(value)) return "—";
  return `$${value.toLocaleString("en-US", { maximumFractionDigits: 6 })}`;
}

function fmtRatio(value?: number | null) {
  if (value == null || !Number.isFinite(value)) return "—";
  return `${(value * 100).toFixed(1)}%`;
}

function fmtMs(value?: number) {
  if (value == null || !Number.isFinite(value) || value <= 0) return "—";
  if (value < 1) return "<1 ms";
  if (value < 1_000) return `${Math.round(value)} ms`;
  return `${(value / 1_000).toFixed(2)} s`;
}

function fmtBytes(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (value >= 1_048_576) return `${(value / 1_048_576).toFixed(1)} MB`;
  if (value >= 1_024) return `${(value / 1_024).toFixed(1)} KB`;
  return `${fmtInteger(value)} B`;
}

function formatDateTime(value: string) {
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString();
}

function relativeTime(value: string) {
  const t = new Date(value).getTime();
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, Math.floor((Date.now() - t) / 1_000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function humanize(value?: string) {
  if (!value) return "";
  return value.replace(/[_-]+/g, " ").replace(/^\w/, (c) => c.toUpperCase());
}
