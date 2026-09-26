import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Database,
  DollarSign,
  Layers3,
  RefreshCw,
  ShieldCheck,
  Timer,
  TrendingUp,
  Wallet,
  type LucideIcon,
} from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import {
  api,
  type ProviderUsage,
  type RecentActivity,
  type SeriesPoint,
  type UsageInsights,
  type UsageTerminalStatus,
} from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { microsToUSD } from "../lib/format";
import { PageHeader } from "../components/Layout";
import {
  Badge,
  Card,
  EmptyState,
  ErrorCard,
  SegmentedControl,
  Skeleton,
  TablePagination,
  useClientPagination,
} from "../components/ui";

const PERIODS = [
  { value: "today", label: "Today" },
  { value: "week", label: "7D" },
  { value: "month", label: "30D" },
];

export function OverviewPage() {
  const [period, setPeriod] = useState("week");
  const insights = useQuery({
    queryKey: ["usage-insights", period],
    queryFn: () => api.usageInsights(period),
    staleTime: 30_000,
    placeholderData: (previous) => previous,
  });
  const budgets = useQuery({
    queryKey: ["budget-status"],
    queryFn: () => api.budgetStatus(),
    staleTime: 30_000,
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  });

  const alerts = (budgets.data?.budgets ?? []).filter((budget) => budget.pct_used >= budget.alert_pct);
  const blocked = alerts.filter((budget) => budget.pct_used >= 100 && budget.hard_cutoff);
  const warnings = alerts.filter((budget) => budget.pct_used < 100 || !budget.hard_cutoff);
  const isRefreshing = insights.isFetching && !insights.isLoading;

  return (
    <>
      <PageHeader
        title="Overview"
        icon={Activity}
        description="A concise view of traffic, spend, and routing performance."
        action={
          <div className="flex items-center gap-2">
            <SegmentedControl value={period} onChange={setPeriod} options={PERIODS} />
            <button
              type="button"
              onClick={() => insights.refetch()}
              disabled={insights.isFetching}
              aria-label="Refresh overview"
              title="Refresh overview"
              className="inline-flex h-10 w-10 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] shadow-sm transition-colors hover:border-[var(--border-strong)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-60"
            >
              <RefreshCw className={`h-4 w-4 ${isRefreshing ? "animate-spin" : ""}`} />
            </button>
          </div>
        }
      />

      <BudgetNotice blocked={blocked} warnings={warnings} />

      {insights.isLoading ? (
        <OverviewSkeleton />
      ) : insights.isError ? (
        <ErrorCard message="Failed to load overview data. Is the backend running?" />
      ) : insights.data ? (
        <InsightsDashboard data={insights.data} />
      ) : null}
    </>
  );
}

function BudgetNotice({
  blocked,
  warnings,
}: {
  blocked: Array<{ scope_name: string; limit_micros: number; period: string }>;
  warnings: Array<{ scope_name: string; pct_used: number }>;
}) {
  if (blocked.length === 0 && warnings.length === 0) return null;
  const isBlocked = blocked.length > 0;
  const items = isBlocked
    ? blocked.map((budget) => `${budget.scope_name} · ${microsToUSD(budget.limit_micros)} ${budget.period}`)
    : warnings.map((budget) => `${budget.scope_name} · ${budget.pct_used.toFixed(0)}% used`);

  return (
    <div className={`mb-6 flex flex-col gap-3 rounded-xl border px-4 py-3 sm:flex-row sm:items-center ${
      isBlocked
        ? "border-red-300/70 bg-red-50/60 dark:border-red-700/50 dark:bg-red-950/20"
        : "border-amber-300/70 bg-amber-50/60 dark:border-amber-700/50 dark:bg-amber-950/20"
    }`}>
      {isBlocked
        ? <AlertTriangle className="h-4 w-4 shrink-0 text-red-600 dark:text-red-400" />
        : <Wallet className="h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />}
      <div className="min-w-0 flex-1">
        <p className="text-sm font-semibold text-[var(--text)]">
          {isBlocked ? "Plan limit reached" : "Plan threshold reached"}
        </p>
        <p className="mt-0.5 truncate text-xs text-[var(--text-muted)]" title={items.join(", ")}>
          {items.join(" · ")}
        </p>
      </div>
      <Link
              to={dashboard("/plans")}
        className="inline-flex shrink-0 items-center gap-1 text-xs font-semibold text-[var(--text)] hover:text-accent-600"
      >
        Manage plans <ArrowUpRight className="h-3.5 w-3.5" />
      </Link>
    </div>
  );
}

function OverviewSkeleton() {
  return (
    <div className="space-y-6 pb-12">
      <div className="grid gap-4 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, index) => (
          <Card key={index} className="p-5">
            <Skeleton className="h-8 w-28" />
            <Skeleton className="mt-4 h-9 w-32" />
            <div className="mt-4 grid grid-cols-3 gap-3 border-t border-[var(--border)] pt-3">
              {Array.from({ length: 3 }).map((__, item) => <Skeleton key={item} className="h-8 w-full" />)}
            </div>
          </Card>
        ))}
      </div>
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.7fr)_minmax(320px,1fr)]">
        <Card className="p-6"><Skeleton className="h-72 w-full" /></Card>
        <Card className="p-6"><Skeleton className="h-72 w-full" /></Card>
      </div>
      <Card className="p-6"><Skeleton className="h-28 w-full" /></Card>
      <Card className="p-6"><Skeleton className="h-96 w-full" /></Card>
    </div>
  );
}

function InsightsDashboard({ data }: { data: UsageInsights }) {
  const { summary, savings, providers, recent, series } = data;

  return (
    <div className="space-y-6 pb-12">
      <div className="grid gap-4 lg:grid-cols-3">
        <SummaryGroupCard
          icon={Activity}
          title="Traffic"
          primary={fmtCompact(summary.total_requests)}
          primaryLabel="requests"
          items={[
            { label: "Input", value: fmtCompact(summary.prompt_tokens) },
            { label: "Output", value: fmtCompact(summary.completion_tokens) },
            { label: "Cache read", value: fmtCompact(summary.cached_tokens) },
          ]}
        />
        <SummaryGroupCard
          icon={DollarSign}
          title="Spend & value"
          primary={fmtUSD(summary.cost_usd)}
          primaryLabel="tracked cost"
          tone={summary.unpriced_requests > 0 ? "warning" : "accent"}
          items={[
            { label: "Value saved", value: fmtUSD(savings.usd_saved), tone: "good" },
            { label: "Cost / request", value: fmtUSD(summary.cost_per_request_usd) },
            { label: "Pricing coverage", value: fmtCoverage(summary.pricing_request_coverage) },
          ]}
        />
        <SummaryGroupCard
          icon={ShieldCheck}
          title="Reliability"
          primary={fmtPercent(summary.success_rate)}
          primaryLabel="successful"
          tone={summary.success_rate < 0.95 && summary.total_requests > 0 ? "warning" : "success"}
          items={[
            { label: "Failed", value: fmtCompact(summary.failed_requests), tone: summary.failed_requests > 0 ? "danger" : undefined },
            { label: "Avg latency", value: fmtMs(summary.avg_latency_ms) },
            { label: "TTFT", value: fmtMs(summary.avg_ttft_ms) },
          ]}
        />
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.7fr)_minmax(320px,1fr)]">
        <TrendCard series={series} busiest={data.busiest} />
        <ProviderPerformance providers={providers} />
      </div>

      <TokenComposition data={data} />
      <RecentActivityTable recent={recent} providers={providers} />
    </div>
  );
}

type SummaryTone = "accent" | "success" | "warning";

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
      icon: "text-emerald-600 dark:text-emerald-300",
      background: "bg-emerald-50 ring-emerald-200/70 dark:bg-emerald-950/30 dark:ring-emerald-900/60",
    },
    warning: {
      icon: "text-amber-700 dark:text-amber-300",
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
        <span className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--text-muted)]">{title}</span>
      </div>
      <div className="mt-4 flex items-baseline gap-2">
        <span className="text-3xl font-semibold tracking-tight tabular-nums text-[var(--text)]">{primary}</span>
        <span className="text-xs text-[var(--text-muted)]">{primaryLabel}</span>
      </div>
      <div className="mt-4 grid grid-cols-3 gap-3 border-t border-[var(--border)] pt-3">
        {items.map((item) => (
          <div key={item.label} className="min-w-0">
            <div className={`truncate text-xs font-semibold tabular-nums sm:text-sm ${
              item.tone === "good"
                ? "text-emerald-600 dark:text-emerald-300"
                : item.tone === "danger"
                  ? "text-red-600 dark:text-red-300"
                  : "text-[var(--text)]"
            }`} title={item.value}>{item.value}</div>
            <div className="mt-0.5 text-[9px] font-medium uppercase tracking-wider text-[var(--text-muted)]">{item.label}</div>
          </div>
        ))}
      </div>
    </Card>
  );
}

type TrendMetric = "requests" | "tokens" | "cost" | "failures";
type TrendPoint = SeriesPoint & { total_tokens: number };

const TREND_OPTIONS = [
  { value: "requests", label: "Requests" },
  { value: "tokens", label: "Tokens" },
  { value: "cost", label: "Cost" },
  { value: "failures", label: "Failures" },
];

const TREND_CONFIG: Record<TrendMetric, { key: keyof TrendPoint; label: string; color: string }> = {
  requests: { key: "requests", label: "Requests", color: "var(--color-chart-1)" },
  tokens: { key: "total_tokens", label: "Tokens", color: "var(--color-chart-2)" },
  cost: { key: "cost_usd", label: "Cost", color: "var(--color-accent-500)" },
  failures: { key: "failures", label: "Failures", color: "var(--color-danger)" },
};

function TrendCard({ series, busiest }: { series: SeriesPoint[]; busiest: string }) {
  const [metric, setMetric] = useState<TrendMetric>("requests");
  const points = useMemo<TrendPoint[]>(
    () => series.map((point) => ({
      ...point,
      requests: point.requests || point.count,
      total_tokens: point.prompt_tokens + point.completion_tokens,
    })),
    [series],
  );
  const config = TREND_CONFIG[metric];

  return (
    <Card className="flex min-h-[370px] flex-col">
      <div className="flex flex-col gap-3 border-b border-[var(--border)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-start gap-2.5">
          <TrendingUp className="mt-0.5 h-4 w-4 text-[var(--text-muted)]" />
          <div>
            <h2 className="text-sm font-semibold">Usage trend</h2>
            <p className="mt-1 text-xs text-[var(--text-muted)]">
              {busiest ? `Busiest request bucket: ${busiest}` : "Volume across the selected period."}
            </p>
          </div>
        </div>
        <SegmentedControl value={metric} onChange={(value) => setMetric(value as TrendMetric)} options={TREND_OPTIONS} />
      </div>
      {points.length === 0 ? (
        <EmptyState title="No activity in this period." />
      ) : (
        <div className="min-h-0 flex-1 px-3 pb-4 pt-5 sm:px-5">
          <ResponsiveContainer width="100%" height="100%" minHeight={260}>
            <AreaChart data={points} margin={{ top: 4, right: 8, left: -8, bottom: 0 }}>
              <defs>
                <linearGradient id={`overview-${metric}`} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={config.color} stopOpacity={0.22} />
                  <stop offset="95%" stopColor={config.color} stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="label" axisLine={false} tickLine={false} tick={{ fontSize: 10, fill: "var(--text-muted)" }} minTickGap={28} />
              <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 10, fill: "var(--text-muted)" }} width={46} tickFormatter={(value) => fmtAxis(Number(value), metric)} />
              <Tooltip
                cursor={{ stroke: "var(--border-strong)", strokeWidth: 1 }}
                contentStyle={{
                  background: "var(--bg-elevated)",
                  border: "1px solid var(--border)",
                  borderRadius: "10px",
                  fontSize: "12px",
                  boxShadow: "var(--shadow-card)",
                }}
                formatter={(value) => [fmtTrendValue(Number(value), metric), config.label]}
              />
              <Area
                type="monotone"
                dataKey={config.key}
                name={config.label}
                stroke={config.color}
                strokeWidth={2}
                fill={`url(#overview-${metric})`}
                animationDuration={350}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}
    </Card>
  );
}

function ProviderPerformance({ providers }: { providers: ProviderUsage[] }) {
  const { page, pages, paged, setPage, total } = useClientPagination(providers, 5);

  return (
    <Card className="flex min-h-[370px] flex-col">
      <div className="flex items-start justify-between gap-3 border-b border-[var(--border)] px-5 py-4">
        <div className="flex items-start gap-2.5">
          <Database className="mt-0.5 h-4 w-4 text-[var(--text-muted)]" />
          <div>
            <h2 className="text-sm font-semibold">Provider mix</h2>
            <p className="mt-1 text-xs text-[var(--text-muted)]">Traffic share and delivery quality.</p>
          </div>
        </div>
        <span className="text-xs tabular-nums text-[var(--text-muted)]">{providers.length} providers</span>
      </div>
      {providers.length === 0 ? (
        <EmptyState title="No provider usage yet." />
      ) : (
        <>
          <div className="flex-1 divide-y divide-[var(--border)] px-5">
            {paged.map((provider) => (
              <div key={provider.provider} className="py-3">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex min-w-0 items-center gap-2.5">
                    <SmallProviderIcon provider={provider} className="h-6 w-6" />
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold" title={provider.display_name}>{provider.display_name}</div>
                      <div className="mt-0.5 text-[10px] text-[var(--text-muted)]">
                        {fmtPercent(provider.success_rate)} success · {fmtMs(provider.avg_latency_ms)} avg
                      </div>
                    </div>
                  </div>
                  <div className="shrink-0 text-right">
                    <div className="text-xs font-semibold tabular-nums">{fmtCompact(provider.total_requests)} req</div>
                    <div className="mt-0.5 text-[10px] tabular-nums text-[var(--text-muted)]">{fmtUSD(provider.cost_usd)}</div>
                  </div>
                </div>
                <div className="mt-2 flex items-center gap-2">
                  <div className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                    <div className="h-full rounded-full bg-secondary-500" style={{ width: `${Math.max(0, Math.min(100, provider.share_pct))}%` }} />
                  </div>
                  <span className="w-11 text-right text-[10px] font-medium tabular-nums text-[var(--text-muted)]">{provider.share_pct.toFixed(1)}%</span>
                </div>
              </div>
            ))}
          </div>
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
        </>
      )}
    </Card>
  );
}

function TokenComposition({ data }: { data: UsageInsights }) {
  const { summary } = data;
  const regularInput = Math.max(0, summary.prompt_tokens - summary.cached_tokens - summary.cache_write_tokens);
  const total = summary.prompt_tokens + summary.completion_tokens;
  const rows = [
    { label: "Regular input", value: regularInput, color: "bg-secondary-500" },
    { label: "Cache read", value: summary.cached_tokens, color: "bg-accent-500" },
    { label: "Cache write", value: summary.cache_write_tokens, color: "bg-amber-500" },
    {
      label: "Output",
      value: summary.completion_tokens,
      color: "bg-[var(--color-chart-4)]",
      note: summary.reasoning_tokens > 0
        ? `${((summary.completion_tokens / Math.max(1, total)) * 100).toFixed(1)}% · ${fmtCompact(summary.reasoning_tokens)} reasoning`
        : undefined,
    },
  ];

  return (
    <Card>
      <div className="grid gap-5 px-5 py-5 lg:grid-cols-[minmax(180px,0.55fr)_minmax(0,1.45fr)] lg:items-center lg:px-6">
        <div className="flex items-start gap-3">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-[var(--bg-subtle)] text-[var(--text-muted)]">
            <Layers3 className="h-4 w-4" />
          </span>
          <div>
            <h2 className="text-sm font-semibold">Token composition</h2>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-2xl font-semibold tracking-tight tabular-nums">{fmtCompact(total)}</span>
              <span className="text-xs text-[var(--text-muted)]">tokens</span>
            </div>
            <p className="mt-1 text-[10px] text-[var(--text-muted)]">{fmtCompact(summary.cache_hits)} request cache hits</p>
          </div>
        </div>
        {total === 0 ? (
          <p className="text-sm text-[var(--text-muted)]">No token usage recorded in this period.</p>
        ) : (
          <div className="min-w-0">
            <div className="flex h-2.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]" aria-label="Token composition">
              {rows.filter((row) => row.value > 0).map((row) => (
                <div
                  key={row.label}
                  className={row.color}
                  style={{ width: `${(row.value / total) * 100}%` }}
                  title={`${row.label}: ${fmtInteger(row.value)}`}
                />
              ))}
            </div>
            <div className="mt-4 grid grid-cols-2 gap-x-5 gap-y-3 sm:grid-cols-4">
              {rows.map((row) => (
                <div key={row.label} className="min-w-0">
                  <div className="flex items-center gap-1.5">
                    <span className={`h-2 w-2 shrink-0 rounded-full ${row.color}`} />
                    <span className="truncate text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">{row.label}</span>
                  </div>
                  <div className="mt-1 text-sm font-semibold tabular-nums">{fmtCompact(row.value)}</div>
                  <div className="mt-0.5 truncate text-[10px] text-[var(--text-muted)]">
                    {row.note || `${((row.value / total) * 100).toFixed(1)}% of total`}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

function RecentActivityTable({ recent, providers }: { recent: RecentActivity[]; providers: ProviderUsage[] }) {
  const { page, pages, paged, setPage, total } = useClientPagination(recent, 10);
  const providerMap = useMemo(() => new Map(providers.map((provider) => [provider.provider, provider])), [providers]);

  return (
    <Card>
      <div className="flex items-start justify-between gap-4 border-b border-[var(--border)] px-5 py-4">
        <div className="flex items-start gap-2.5">
          <Timer className="mt-0.5 h-4 w-4 text-[var(--text-muted)]" />
          <div>
            <h2 className="text-sm font-semibold">Recent requests</h2>
            <p className="mt-1 text-xs text-[var(--text-muted)]">Terminal request outcomes from the selected period.</p>
          </div>
        </div>
        <Link to={dashboard("/usage")} className="inline-flex shrink-0 items-center gap-1 text-xs font-semibold text-[var(--text-muted)] hover:text-[var(--text)]">
          Full accounting <ArrowUpRight className="h-3.5 w-3.5" />
        </Link>
      </div>
      {recent.length === 0 ? (
        <EmptyState title="No recent requests." hint="Make a request through the proxy to see it here." />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[900px] text-xs">
              <thead>
                <tr className="border-b border-[var(--border)] text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  <th className="px-5 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Provider / model</th>
                  <th className="px-4 py-3 text-right">Tokens</th>
                  <th className="px-4 py-3 text-right">Cost</th>
                  <th className="px-4 py-3 text-right">Latency</th>
                  <th className="px-5 py-3 text-right">Time</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border)]">
                {paged.map((row) => {
                  const provider = providerMap.get(row.provider);
                  return (
                    <tr key={row.id} className="transition-colors hover:bg-[var(--bg-subtle)]/60">
                      <td className="px-5 py-3"><RequestStatusBadge status={row.status} /></td>
                      <td className="px-4 py-3">
                        <div className="flex min-w-0 items-center gap-2.5">
                          <SmallProviderIcon provider={provider} className="h-7 w-7" />
                          <div className="min-w-0">
                            <div className="max-w-md truncate font-mono text-[11px] font-semibold" title={row.model}>{row.model || "Unknown model"}</div>
                            <div className="mt-0.5 truncate text-[10px] text-[var(--text-muted)]">{provider?.display_name || row.provider}</div>
                          </div>
                        </div>
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        <div className="font-semibold">{fmtInteger(row.prompt_tokens + row.completion_tokens)}</div>
                        {(row.cached_tokens > 0 || row.reasoning_tokens > 0) && (
                          <div className="mt-0.5 text-[10px] text-[var(--text-muted)]">
                            {row.cached_tokens > 0 ? `${fmtCompact(row.cached_tokens)} cached` : `${fmtCompact(row.reasoning_tokens)} reasoning`}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-3 text-right font-medium tabular-nums">
                        {row.pricing_status === "missing" ? "Unpriced" : fmtUSD(row.cost_usd)}
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums text-[var(--text-muted)]">
                        {fmtMs(row.end_to_end_latency_ms || row.latency_ms)}
                      </td>
                      <td className="whitespace-nowrap px-5 py-3 text-right text-[var(--text-muted)]" title={formatDateTime(row.created_at)}>
                        {relativeTime(row.created_at)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
        </>
      )}
    </Card>
  );
}

function RequestStatusBadge({ status }: { status: UsageTerminalStatus }) {
  const config: Record<UsageTerminalStatus, { label: string; tone: "success" | "accent" | "warning" | "danger" | "neutral" }> = {
    success: { label: "Success", tone: "success" },
    cache_hit: { label: "Cache hit", tone: "accent" },
    blocked: { label: "Blocked", tone: "warning" },
    failed: { label: "Failed", tone: "danger" },
    cancelled: { label: "Cancelled", tone: "neutral" },
  };
  const item = config[status] ?? { label: String(status), tone: "neutral" as const };
  return <Badge tone={item.tone}>{item.label}</Badge>;
}

function SmallProviderIcon({ provider, className = "h-5 w-5" }: { provider?: ProviderUsage; className?: string }) {
  const [errored, setErrored] = useState(false);
  if (!provider || errored || !provider.icon) {
    const label = provider?.display_name || "?";
    return (
      <div
        className={`flex shrink-0 items-center justify-center rounded-md text-[9px] font-bold text-white ${className}`}
        style={{ backgroundColor: provider?.color || "var(--color-ink-400)" }}
        aria-hidden="true"
      >
        {label.slice(0, 1).toUpperCase()}
      </div>
    );
  }
  return (
    <img
      src={provider.icon}
      alt=""
      onError={() => setErrored(true)}
      className={`shrink-0 rounded-md object-contain ${className}`}
    />
  );
}

function fmtCompact(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return value.toLocaleString();
}

function fmtInteger(value: number): string {
  return Math.round(value).toLocaleString();
}

function fmtUSD(value: number): string {
  if (value > 0 && value < 0.0001) return "<$0.0001";
  if (value < 1) return `$${value.toFixed(4)}`;
  return `$${value.toFixed(2)}`;
}

function fmtMs(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "—";
  if (value < 1000) return `${Math.round(value)}ms`;
  return `${(value / 1000).toFixed(2)}s`;
}

function fmtPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

function fmtCoverage(value: number | null): string {
  return value == null ? "—" : `${(value * 100).toFixed(1)}%`;
}

function fmtAxis(value: number, metric: TrendMetric): string {
  if (metric === "cost") return value >= 1 ? `$${value.toFixed(0)}` : `$${value.toFixed(2)}`;
  return fmtCompact(value);
}

function fmtTrendValue(value: number, metric: TrendMetric): string {
  if (metric === "cost") return fmtUSD(value);
  return fmtInteger(value);
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

function relativeTime(value: string): string {
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return "—";
  const delta = Date.now() - timestamp;
  if (delta < 60_000) return "just now";
  if (delta < 3_600_000) return `${Math.floor(delta / 60_000)}m ago`;
  if (delta < 86_400_000) return `${Math.floor(delta / 3_600_000)}h ago`;
  return `${Math.floor(delta / 86_400_000)}d ago`;
}
