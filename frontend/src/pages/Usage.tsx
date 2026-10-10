import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, Info, RefreshCw, Search, X } from "lucide-react";
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
import { Badge, ErrorBanner, ErrorCard, Skeleton, TablePagination, useClientPagination } from "../components/ui";
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
const PERIOD_TEXT: Record<string, string> = { today: "today", "24h": "last 24 hours", "7d": "last 7 days", "30d": "last 30 days", "90d": "last 90 days" };
const USAGE_REFRESH_DEBOUNCE_MS = 8_000;

type Tab = "overview" | "providers" | "models" | "requests";
const TABS: { value: Tab; label: string }[] = [
  { value: "overview", label: "Overview" },
  { value: "providers", label: "Providers" },
  { value: "models", label: "Models" },
  { value: "requests", label: "Requests" },
];

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
    toast.success("Refreshing usage", "Accounting, pricing and health are being re-fetched.");
  };
  const refreshing = insights.isFetching || modelUsage.isFetching || health.isFetching;
  const data = insights.data;

  return (
    <>
      <PageHeader
        title="Usage"
        description={`Every request KeiRouter routed — what it cost, how fast it was, and where it went · ${PERIOD_TEXT[period] ?? period}`}
        action={
          <>
            <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Period">
              {REPORT_PERIODS.map((p) => (
                <button
                  key={p.value}
                  type="button"
                  role="radio"
                  aria-checked={period === p.value}
                  onClick={() => setParam("period", p.value, "today")}
                  className={cn(
                    "h-full rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                    period === p.value ? "bg-surface text-fg shadow-[0_0_0_1px_var(--border-strong)]" : "text-fg-muted hover:text-fg",
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
              className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />
            </button>
          </>
        }
      />

      {insights.isLoading ? (
        <div className="space-y-5">
          <Skeleton className="h-[86px] w-full rounded-2xl" />
          <Skeleton className="h-[360px] w-full rounded-2xl" />
        </div>
      ) : insights.isError || !data ? (
        <ErrorCard message="Couldn't load usage analytics. Is the backend running?" />
      ) : (
        <div className="space-y-5">
          <KpiStrip data={data} />
          <PricingCoverageNotice summary={data.summary} />

          <div className="flex gap-1 border-b border-line" role="tablist" aria-label="Usage views">
            {TABS.map((t) => {
              const count =
                t.value === "providers"
                  ? data.providers.filter((p) => p.total_requests > 0).length
                  : t.value === "models"
                    ? (modelUsage.data?.models.length ?? null)
                    : t.value === "requests"
                      ? data.recent.length
                      : null;
              return (
                <button
                  key={t.value}
                  type="button"
                  role="tab"
                  aria-selected={tab === t.value}
                  onClick={() => setParam("tab", t.value, "overview")}
                  className={cn(
                    "relative -mb-px inline-flex items-center gap-1.5 px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                    tab === t.value ? "text-fg" : "text-fg-muted hover:text-fg",
                  )}
                >
                  {t.label}
                  {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{count}</span>}
                  {tab === t.value && <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
                </button>
              );
            })}
          </div>

          {tab === "overview" && (
            <div className="space-y-5">
              <div className="grid gap-5 xl:grid-cols-3">
                <TrafficCard data={data} className="xl:col-span-2" />
                <ProviderDistribution providers={data.providers} totalRequests={data.summary.total_requests} />
              </div>
              <TokenSavingsBreakdown savings={data.savings} totalRequests={data.summary.total_requests} insights={data} period={period} />
            </div>
          )}
          {tab === "providers" && <ProviderAccounting providers={data.providers} health={health.data} healthLoading={health.isLoading} healthError={health.isError} />}
          {tab === "models" && <ModelUsageTable models={modelUsage.data?.models ?? []} loading={modelUsage.isLoading} error={modelUsage.isError} />}
          {tab === "requests" && <RecentRequests records={data.recent} />}
        </div>
      )}
    </>
  );
}

// ── KPI strip ────────────────────────────────────────────────────────────────

function KpiStrip({ data }: { data: UsageInsights }) {
  const s = data.summary;
  const cells: { label: string; value: string; hint: string; tone?: "warn" }[] = [
    { label: "Requests", value: fmtInteger(s.total_requests), hint: `${fmtInteger(s.successful_requests)} ok · ${fmtInteger(s.failed_requests)} failed` },
    { label: "Success rate", value: s.total_requests ? fmtRatio(s.success_rate) : "—", hint: `${fmtInteger(s.cache_hits)} served from cache`, tone: s.total_requests > 0 && s.success_rate < 0.95 ? "warn" : undefined },
    { label: "Tokens", value: fmtCompact(s.total_tokens), hint: `${fmtCompact(s.prompt_tokens)} in · ${fmtCompact(s.completion_tokens)} out` },
    { label: "Spend", value: fmtUSD(s.cost_usd), hint: `${fmtUSD(s.cost_per_request_usd)} per request`, tone: s.unpriced_requests > 0 ? "warn" : undefined },
    { label: "Saved", value: fmtUSD(data.savings.usd_saved), hint: `${fmtCompact(data.savings.total_tokens_saved)} tokens avoided` },
    { label: "Latency p50", value: fmtMs(s.p50_latency_ms), hint: `p95 ${fmtMs(s.p95_latency_ms)} · TTFT ${fmtMs(s.avg_ttft_ms)}` },
  ];
  return (
    <section aria-label="Key metrics" className="grid grid-cols-2 gap-px overflow-hidden rounded-2xl border border-line bg-line shadow-[var(--shadow-card)] md:grid-cols-3 xl:grid-cols-6">
      {cells.map((c) => (
        <div key={c.label} className="min-w-0 bg-surface px-4 py-3">
          <p className="text-[12px] font-medium text-fg-muted">{c.label}</p>
          <p className={cn("mt-1 truncate text-[20px] font-semibold tracking-[-0.02em] tabular-nums", c.tone === "warn" ? "text-warn" : "text-fg")}>{c.value}</p>
          <p className="mt-0.5 truncate text-[12px] tabular-nums text-fg-faint" title={c.hint}>
            {c.hint}
          </p>
        </div>
      ))}
    </section>
  );
}

// ── Accounting quality ───────────────────────────────────────────────────────

function PricingCoverageNotice({ summary }: { summary: UsageInsights["summary"] }) {
  const notices = [
    summary.unpriced_requests > 0 && {
      label: "Missing pricing",
      detail: `${fmtInteger(summary.unpriced_requests)} pricing-eligible requests and ${fmtInteger(summary.unpriced_tokens)} tokens have no deterministic rate. Tracked cost excludes them rather than treating them as free.`,
    },
    summary.estimated_requests > 0 && {
      label: "Pricing estimate",
      detail: `${fmtInteger(summary.estimated_requests)} requests use an alias, catalog fallback, or retail-equivalent rate. This estimates price, independently of whether usage was measured.`,
    },
    summary.estimated_usage_requests > 0 && {
      label: "Usage estimate",
      detail: `${fmtInteger(summary.estimated_usage_requests)} requests and ${fmtInteger(summary.estimated_usage_tokens)} tokens use estimated usage counters. Their cost can still use an exact or estimated rate.`,
    },
    summary.legacy_usage_requests > 0 && {
      label: "Legacy usage",
      detail: `${fmtInteger(summary.legacy_usage_requests)} requests and ${fmtInteger(summary.legacy_usage_tokens)} tokens predate complete provenance. Available totals are retained without inventing component breakdowns.`,
    },
    summary.backfilled_requests > 0 && {
      label: "Historical backfill",
      detail: `${fmtInteger(summary.backfilled_requests)} requests use current rates applied later as historical estimates, not original request-time pricing snapshots.`,
    },
  ].filter((n): n is { label: string; detail: string } => Boolean(n));
  if (notices.length === 0) return null;
  const caution = summary.unpriced_requests > 0 || summary.legacy_usage_requests > 0 || summary.backfilled_requests > 0;

  return (
    <details className={cn("group overflow-hidden rounded-2xl border bg-surface", caution ? "border-warn/30" : "border-line")}>
      <summary className="flex cursor-pointer list-none flex-wrap items-center gap-3 px-4 py-2.5 [&::-webkit-details-marker]:hidden">
        {caution ? <AlertTriangle className="h-4 w-4 shrink-0 text-warn" /> : <Info className="h-4 w-4 shrink-0 text-fg-faint" />}
        <p className="min-w-[180px] flex-1 text-[13px]">
          <span className="font-medium text-fg">Accounting quality</span>{" "}
          <span className="text-fg-muted">
            · {notices.length} note{notices.length === 1 ? "" : "s"} on how this period's cost was measured
          </span>
        </p>
        <span className="text-[12px] tabular-nums text-fg-muted">
          Priced: <span className="text-fg">{fmtRatio(summary.pricing_request_coverage)}</span> of requests · <span className="text-fg">{fmtRatio(summary.pricing_token_coverage)}</span> of tokens
        </span>
        <ChevronDown className="h-4 w-4 text-fg-faint transition-transform group-open:rotate-180" />
      </summary>
      <div className="border-t border-line px-4 py-3">
        <ul className="grid gap-3 text-[12.5px] leading-5 text-fg-muted lg:grid-cols-2">
          {notices.map((n) => (
            <li key={n.label}>
              <span className="font-medium text-fg">{n.label}.</span> {n.detail}
            </li>
          ))}
        </ul>
        <p className="mt-3 border-t border-line pt-2 text-[12px] text-fg-faint">
          Coverage counts {fmtInteger(summary.pricing_eligible_requests)} token-bearing, pricing-eligible request{summary.pricing_eligible_requests === 1 ? "" : "s"}.
        </p>
      </div>
    </details>
  );
}

// ── Shared chrome ────────────────────────────────────────────────────────────

function Panel({ title, subtitle, action, children, className }: { title: string; subtitle?: ReactNode; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section aria-label={title} className={cn("min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-[13px] font-semibold text-fg">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function Empty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="px-6 py-10 text-center">
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {hint && <p className="mt-1 text-[12.5px] text-fg-muted">{hint}</p>}
    </div>
  );
}

const TH = "px-4 py-2 font-medium";
const THEAD = "border-b border-line bg-subtle text-left text-[12px] text-fg-faint";

// ── Provider distribution ────────────────────────────────────────────────────

function ProviderDistribution({ providers, totalRequests }: { providers: ProviderUsage[]; totalRequests: number }) {
  const active = providers.filter((p) => p.total_requests > 0).sort((a, b) => b.total_requests - a.total_requests);
  return (
    <Panel title="Where requests went" subtitle={`${fmtInteger(totalRequests)} requests across ${active.length} provider${active.length === 1 ? "" : "s"}`}>
      {active.length === 0 ? (
        <Empty title="No provider traffic in this period" />
      ) : (
        <ul className="space-y-3 px-4 py-4">
          {active.slice(0, 8).map((p) => (
            <li key={p.provider} className="flex items-center gap-3">
              <ProviderLogo icon={p.icon} name={p.display_name || p.provider} size={22} />
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline justify-between gap-3 text-[13px]">
                  <span className="truncate font-medium text-fg">{p.display_name || p.provider}</span>
                  <span className="tabular-nums text-fg">{p.share_pct.toFixed(1)}%</span>
                </div>
                <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-track">
                  <div className="h-full rounded-full bg-accent-500" style={{ width: `${p.share_pct}%` }} />
                </div>
                <p className="mt-1 text-[11.5px] tabular-nums text-fg-faint">
                  {fmtInteger(p.total_requests)} req · {p.token_share_pct.toFixed(1)}% of tokens · {fmtUSD(p.cost_usd)}
                </p>
              </div>
            </li>
          ))}
          {active.length > 8 && <li className="text-[12px] text-fg-faint">+ {active.length - 8} more in the Providers tab</li>}
        </ul>
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
      {healthError && <ErrorBanner message="Usage loaded, but provider-health telemetry is unavailable right now." />}
      {dropped > 0 && (
        <p role="status" className="flex items-start gap-2 rounded-2xl border border-warn/30 bg-warn/5 px-4 py-2.5 text-[12.5px] text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" />
          {fmtInteger(dropped)} health events were dropped, so provider health may be incomplete. Usage accounting is unaffected.
        </p>
      )}
      <Panel
        title="Provider accounting"
        subtitle="Requests, tokens, cost, latency and pricing coverage by provider, with current health"
        action={
          <Link to="/provider-health" className="text-[12px] font-medium text-accent-500 hover:underline dark:text-accent-400">
            Health dashboard
          </Link>
        }
      >
        {rows.length === 0 ? (
          <Empty title="No provider usage in this period" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[1040px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <th className={TH}>Provider</th>
                  <th className={TH}>Health</th>
                  <th className={cn(TH, "text-right")}>Requests</th>
                  <th className={cn(TH, "text-right")}>Input</th>
                  <th className={cn(TH, "text-right")}>Output</th>
                  <th className={cn(TH, "text-right")}>Cost</th>
                  <th className={cn(TH, "text-right")}>Latency</th>
                  <th className={cn(TH, "text-right")}>Priced</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {rows.map((p) => {
                  const h = healthBy.get(p.provider);
                  return (
                    <tr key={p.provider} className="transition-colors hover:bg-hover">
                      <td className="px-4 py-2.5">
                        <Link to={`/providers/${p.provider}`} className="flex items-center gap-2.5 hover:underline">
                          <ProviderLogo icon={p.icon} name={p.display_name || p.provider} size={22} />
                          <span className="min-w-0">
                            <span className="block truncate font-medium text-fg">{p.display_name || p.provider}</span>
                            <span className="block truncate font-mono text-[11.5px] text-fg-faint">{p.provider}</span>
                          </span>
                        </Link>
                      </td>
                      <td className="px-4 py-2.5">
                        {h ? <HealthStatusBadge status={h.status} issue={h.main_issue} /> : <span className="text-[12px] text-fg-faint">{healthLoading ? "…" : "—"}</span>}
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
                        <div className="text-[11.5px] text-fg-faint">
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

function Cell({ main, sub }: { main: string; sub?: string }) {
  return (
    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
      <div className="text-fg">{main}</div>
      {sub && <div className="text-[11.5px] text-fg-faint">{sub}</div>}
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
      subtitle="Usage, cost and the immutable pricing snapshot each model was billed with"
      action={
        <div className="relative w-full sm:w-64">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" />
          <input
            aria-label="Filter models by provider or name"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(1);
            }}
            placeholder="Filter provider or model"
            className="h-8 w-full rounded-lg border border-line bg-surface pl-8 pr-3 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
          />
        </div>
      }
    >
      {loading ? (
        <div className="space-y-2 p-4">{[0, 1, 2, 3, 4].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
      ) : error ? (
        <div className="p-4">
          <ErrorBanner message="Couldn't load model-level usage." />
        </div>
      ) : filtered.length === 0 ? (
        <Empty title={models.length === 0 ? "No model usage in this period" : "No models match"} />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[1040px] text-[13px]">
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
                          <div className="max-w-sm truncate font-mono text-[12.5px] text-fg" title={m.model}>
                            {m.model}
                          </div>
                          <div className="truncate text-[11.5px] text-fg-faint">{m.provider_name || m.provider}</div>
                        </div>
                      </div>
                    </td>
                    <Cell main={fmtInteger(m.total_requests)} sub={`${fmtRatio(m.success_rate)} ok`} />
                    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums" title={`${fmtCompact(m.cached_tokens)} cached · ${fmtCompact(m.reasoning_tokens)} reasoning`}>
                      <div className="text-fg">{fmtCompact(m.total_tokens)}</div>
                      <div className="text-[11.5px] text-fg-faint">
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
                      <div className="mt-1 whitespace-nowrap text-[11.5px] tabular-nums text-fg-faint">
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
    <th aria-sort={active === k ? (dir === "asc" ? "ascending" : "descending") : "none"} className={cn(TH, right && "text-right")}>
      <button type="button" onClick={() => onSort(k)} className={cn("inline-flex items-center gap-1 hover:text-fg", right && "flex-row-reverse", active === k && "text-fg")}>
        {label}
        <Icon className={cn("h-3 w-3", active === k ? "opacity-100" : "opacity-40")} />
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

  return (
    <>
      <Panel
        title="Recent requests"
        subtitle={`Newest ${records.length} terminal requests in this period · open one to audit tokens, cost and pricing`}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-56">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" />
              <input
                aria-label="Filter requests"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Model, key, client or ID"
                className="h-8 w-full rounded-lg border border-line bg-surface pl-8 pr-7 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
              />
              {query && (
                <button type="button" onClick={() => setQuery("")} aria-label="Clear filter" className="absolute right-1.5 top-1/2 flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-fg-faint hover:text-fg">
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
            <div className="flex gap-1" role="radiogroup" aria-label="Status">
              {STATUS_FILTERS.map((f) => (
                <button
                  key={f.value}
                  type="button"
                  role="radio"
                  aria-checked={status === f.value}
                  onClick={() => setStatus(f.value)}
                  className={cn(
                    "h-8 rounded-lg border px-2.5 text-[12px] font-medium transition-colors",
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
        {filtered.length === 0 ? (
          <Empty title={records.length === 0 ? "No requests in this period" : "No requests match"} />
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[1080px] text-[13px]">
                <thead>
                  <tr className={THEAD}>
                    <th className={TH}>Time</th>
                    <th className={TH}>Status</th>
                    <th className={TH}>Model</th>
                    <th className={TH}>Key · client</th>
                    <th className={cn(TH, "text-right")}>Input</th>
                    <th className={cn(TH, "text-right")}>Output</th>
                    <th className={cn(TH, "text-right")}>Cost</th>
                    <th className={cn(TH, "text-right")}>Latency</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {paged.map((r) => (
                    <tr
                      key={r.id}
                      className="cursor-pointer transition-colors hover:bg-hover"
                      onClick={() => setSelected(r)}
                      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), setSelected(r))}
                      tabIndex={0}
                      aria-label={`Open request ${r.request_id || r.id}`}
                    >
                      <td className="whitespace-nowrap px-4 py-2.5 font-mono text-[12px] text-fg-muted" title={formatDateTime(r.created_at)}>
                        {relativeTime(r.created_at)}
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
                          <span className="truncate font-mono text-[12.5px] text-fg" title={`${r.provider}/${r.model}`}>
                            {r.model || "—"}
                          </span>
                        </div>
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-[12.5px]">
                        <span className="font-mono text-fg">{r.api_key_name || "—"}</span>
                        {r.client && <span className="text-fg-faint"> · {prettyClient(r.client)}</span>}
                      </td>
                      <Cell main={fmtInteger(r.prompt_tokens)} sub={r.cached_tokens ? `${fmtCompact(r.cached_tokens)} cached` : undefined} />
                      <Cell main={fmtInteger(r.completion_tokens)} sub={r.reasoning_tokens ? `${fmtCompact(r.reasoning_tokens)} reasoning` : undefined} />
                      <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                        <div className={cn(r.pricing_status === "missing" ? "text-warn" : "text-fg")}>{r.pricing_status === "missing" ? "Unpriced" : r.cache_hit ? "$0.00" : fmtUSD(r.cost_usd)}</div>
                        {r.pricing_status !== "priced" && r.pricing_status !== "missing" && <div className="text-[11.5px] text-fg-faint">{humanize(r.pricing_status)}</div>}
                      </td>
                      <Cell main={fmtMs(r.end_to_end_latency_ms)} sub={`upstream ${fmtMs(r.upstream_latency_ms)}`} />
                    </tr>
                  ))}
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

function RequestDetail({ record, onClose }: { record: RecentActivity; onClose: () => void }) {
  const [tab, setTab] = useState<"overview" | "pricing">("overview");
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
  if (pricingUnavailable) notices.push({ title: "Pricing unavailable", message: "No catalog or custom rate matched. Tokens are retained and cost remains unpriced.", tone: "warning", pricing: true });
  if (record.usage_source === "estimated") notices.push({ title: "Estimated usage", message: "Token counts were estimated because authoritative provider usage was unavailable.", tone: "info" });
  if (legacyBreakdownUnavailable)
    notices.push({ title: "Legacy accounting", message: `The historical total ${fmtUSD(record.cost_usd)} was retained, but its component and rate split was not stored.`, tone: "warning", pricing: true });
  if (record.pricing_backfilled) notices.push({ title: "Historical estimate", message: "Current rates were applied later during backfill; this is not the original request-time snapshot.", tone: "warning", pricing: true });
  if (record.pricing_status === "partial") notices.push({ title: "Partial pricing", message: "Only matched components are included in the charged total.", tone: "warning", pricing: true });
  if (cacheHit) notices.push({ title: "Served from cache", message: "Component costs show avoided provider spend; the charged total is zero.", tone: "info", pricing: true });
  const shown = tab === "overview" ? notices : notices.filter((n) => n.pricing);

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
          <button type="button" onClick={onClose} aria-label="Close" className="-mr-1.5 flex h-7 w-7 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="mt-3 flex gap-1" role="tablist">
          {(["overview", "pricing"] as const).map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              aria-selected={tab === t}
              onClick={() => setTab(t)}
              className={cn("relative -mb-px px-2.5 py-2 text-[13px] font-medium", tab === t ? "text-fg" : "text-fg-muted hover:text-fg")}
            >
              {t === "overview" ? "Overview" : "Pricing & audit"}
              {tab === t && <span className="absolute inset-x-1.5 bottom-0 h-0.5 rounded-full bg-accent-500" />}
            </button>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
        <NoticeList notices={shown} />
        {tab === "overview" ? (
          <>
            <div className="grid grid-cols-3 gap-px overflow-hidden rounded-xl border border-line bg-line">
              <Metric label="Total tokens" value={fmtInteger(totalTokens)} hint={`${fmtInteger(record.prompt_tokens)} in · ${fmtInteger(record.completion_tokens)} out`} />
              <Metric label="Charged" value={charged} hint={cacheHit ? "Served from cache" : humanize(record.pricing_status)} />
              <Metric label="End to end" value={fmtMs(record.end_to_end_latency_ms)} hint={`TTFT ${fmtMs(record.ttft_ms)}`} />
            </div>

            <Section title="Tokens" hint="Cache and reasoning are included in their totals.">
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
            </Section>

            <Section
              title={cacheHit ? "Avoided provider cost" : "Cost"}
              hint={
                legacyBreakdownUnavailable
                  ? "Only the retained aggregate total is available."
                  : pricingUnavailable
                    ? "A rate is required before component cost can be calculated."
                    : cacheHit
                      ? "Counterfactual components, not customer charges."
                      : record.pricing_backfilled
                        ? "Calculated with the later backfill rate."
                        : "Per-request USD snapshot."
              }
            >
              {legacyBreakdownUnavailable ? (
                <Unavailable label="Legacy total" value={fmtUSD(record.cost_usd)} message="A reconcilable component split was not retained for this row." />
              ) : pricingUnavailable ? (
                <Unavailable label="Charged total" value="Unpriced" message="Zero-valued components are hidden so missing pricing isn't mistaken for free usage." />
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
            </Section>

            <Section title="Latency" hint="End to end includes routing, fallback and transforms.">
              <Rows
                rows={[
                  ["Upstream", fmtMs(record.upstream_latency_ms)],
                  ["End to end", fmtMs(record.end_to_end_latency_ms)],
                  ["Time to first token", fmtMs(record.ttft_ms)],
                ]}
              />
            </Section>

            <Section title="Optimizations">
              {!hasOptimization ? (
                <p className="text-[12.5px] text-fg-muted">No optimization ran on this request.</p>
              ) : (
                <div className="space-y-2.5">
                  {flags.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {flags.map((f) => (
                        <Badge key={f} tone="neutral">
                          {f}
                        </Badge>
                      ))}
                    </div>
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
            </Section>
          </>
        ) : (
          <>
            <Section title="Pricing provenance" hint="The source and match retained with this request.">
              <Rows
                rows={[
                  ["Status", <PricingBadge key="s" status={record.pricing_status} />],
                  ["Source", record.pricing_source || "—"],
                  ["Match", humanize(record.pricing_match_kind) || "—"],
                  ["Resolved key", <span key="k" className="break-all font-mono text-[12px]">{record.pricing_key || "—"}</span>],
                  [
                    "Source URL",
                    record.pricing_source_url ? (
                      sourceURL ? (
                        <a key="u" href={sourceURL} target="_blank" rel="noreferrer" title={record.pricing_source_url} className="text-accent-500 hover:underline dark:text-accent-400">
                          Open pricing source
                        </a>
                      ) : (
                        <span key="u" className="break-all">{record.pricing_source_url}</span>
                      )
                    ) : (
                      "—"
                    ),
                  ],
                  ["Pricing as of", record.pricing_as_of ? formatDateTime(record.pricing_as_of) : "—"],
                  ["Backfilled", record.pricing_backfilled ? <Badge key="b" tone="warning">Historical estimate</Badge> : "No"],
                ]}
              />
            </Section>
            <Section title="Rates per 1M tokens" hint="USD rates used for each component.">
              {legacyBreakdownUnavailable || pricingUnavailable ? (
                <Unavailable
                  label="Rate snapshot"
                  value="Unavailable"
                  message={legacyBreakdownUnavailable ? "No immutable component-rate snapshot was stored for this legacy total." : "No matching price was found for this request."}
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
            </Section>
            <Section title="Identifiers" hint="Stable references for logs and reconciliation.">
              <Rows
                rows={[
                  ["Request ID", <span key="r" className="break-all font-mono text-[12px]">{record.request_id || "—"}</span>],
                  ["Usage row", <span key="u" className="break-all font-mono text-[12px]">{record.id}</span>],
                  ["API key", <span key="k" className="font-mono text-[12px]">{record.api_key_name || record.api_key_id || "—"}</span>],
                  ["Account", record.account_label || <span key="a" className="font-mono text-[12px]">{record.account_id || "—"}</span>],
                  ["Client", record.client ? prettyClient(record.client) : "—"],
                  ["Provider", <span key="p" className="font-mono text-[12px]">{record.provider || "—"}</span>],
                  ["Recorded at", formatDateTime(record.created_at)],
                ]}
              />
            </Section>
          </>
        )}
      </div>
    </div>
  );
}

function NoticeList({ notices }: { notices: RequestNotice[] }) {
  if (!notices.length) return null;
  const tone = notices.some((n) => n.tone === "danger") ? "danger" : notices.some((n) => n.tone === "warning") ? "warning" : "info";
  return (
    <div className={cn("flex items-start gap-2.5 rounded-xl border px-3.5 py-3", tone === "danger" ? "border-bad/30 bg-bad/5" : tone === "warning" ? "border-warn/30 bg-warn/5" : "border-line bg-subtle")}>
      {tone === "info" ? <Info className="mt-0.5 h-4 w-4 shrink-0 text-fg-faint" /> : <AlertTriangle className={cn("mt-0.5 h-4 w-4 shrink-0", tone === "danger" ? "text-bad" : "text-warn")} />}
      <div className="min-w-0 space-y-1 text-[12.5px] leading-5 text-fg-muted">
        {notices.map((n) => (
          <p key={`${n.title}-${n.message}`}>
            <span className="font-medium text-fg">{n.title}.</span> {n.message}
          </p>
        ))}
      </div>
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="rounded-xl border border-line">
      <div className="border-b border-line px-3.5 py-2.5">
        <h3 className="text-[13px] font-semibold text-fg">{title}</h3>
        {hint && <p className="text-[12px] text-fg-muted">{hint}</p>}
      </div>
      <div className="px-3.5 py-3">{children}</div>
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
      <p className="text-[12px] font-medium text-fg-muted">{label}</p>
      <p className="mt-0.5 truncate text-[16px] font-semibold tabular-nums text-fg">{value}</p>
      <p className="truncate text-[11.5px] text-fg-faint" title={hint}>
        {hint}
      </p>
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
