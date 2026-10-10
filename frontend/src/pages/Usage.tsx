import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
	ArrowUpDown,
	ChevronDown,
	Database,
	DollarSign,
	Info,
  Layers3,
  RefreshCw,
  Search,
	Server,
	ShieldCheck,
	Timer,
  TrendingUp,
  Zap,
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
  connectUsageStream,
  type HealthOverview,
  type HealthProviderRow,
  type ModelUsage,
  type PricingStatus,
  type ProviderUsage,
  type RecentActivity,
  type SeriesPoint,
  type UsageInsights,
  type UsageSource,
  type UsageTerminalStatus,
} from "../lib/api";
import { REPORT_PERIODS } from "../lib/periods";
import { PageHeader } from "../components/Layout";
import {
  Badge,
  Card,
  EmptyState,
  ErrorBanner,
  ErrorCard,
  Modal,
  SegmentedControl,
  Spinner,
  TabBar,
  TablePagination,
  useClientPagination,
} from "../components/ui";
import { HealthStatusBadge } from "../components/HealthBadge";
import { useToast } from "../components/Toast";
import { TokenSavingsBreakdown } from "../components/SavingsBreakdown";

const PERIODS = REPORT_PERIODS.map((p) => ({ ...p }));

const HEALTH_RANGE: Record<string, string> = {
  today: "24h",
  "24h": "24h",
  week: "7d",
  month: "30d",
};

const USAGE_REFRESH_DEBOUNCE_MS = 8_000;

export function UsagePage() {
  const [period, setPeriod] = useState<string>("today");
  const queryClient = useQueryClient();
  const toast = useToast();
  const refreshTimer = useRef<number | null>(null);
  const healthRange = HEALTH_RANGE[period] ?? "24h";

  const insights = useQuery({
    queryKey: ["usage-insights", period],
    queryFn: () => api.usageInsights(period),
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

  useEffect(() => () => {
    if (refreshTimer.current != null) window.clearTimeout(refreshTimer.current);
  }, []);

  const handleRefresh = () => {
    queryClient.invalidateQueries({ queryKey: ["usage-insights", period] });
    queryClient.invalidateQueries({ queryKey: ["usage-models", period] });
    queryClient.invalidateQueries({ queryKey: ["health-overview", healthRange] });
    toast.success("Usage data refreshed", "Accounting, pricing, and health metrics are being re-fetched.");
  };

  const isRefreshing = insights.isFetching || modelUsage.isFetching || health.isFetching;

  return (
    <>
		<PageHeader
			title="Usage"
			icon={Activity}
			description="Requests, spend, optimization, and provider performance."
        action={
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedControl value={period} onChange={setPeriod} options={PERIODS} />
            <button
              type="button"
              onClick={handleRefresh}
              aria-label="Refresh usage analytics"
              title="Refresh usage analytics"
              className="inline-flex h-10 w-10 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] shadow-sm transition-colors hover:border-[var(--border-strong)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
            >
              <RefreshCw className={`h-4 w-4 ${isRefreshing ? "animate-spin" : ""}`} />
            </button>
          </div>
        }
      />

      {insights.isLoading ? (
        <Spinner />
      ) : insights.isError ? (
        <ErrorCard message="Failed to load usage analytics. Is the backend running?" />
      ) : insights.data ? (
        <UsageContent
          data={insights.data}
          models={modelUsage.data?.models ?? []}
          modelsLoading={modelUsage.isLoading}
          modelsError={modelUsage.isError}
          health={health.data}
          healthLoading={health.isLoading}
          healthError={health.isError}
          period={period}
        />
      ) : null}
    </>
  );
}

function UsageContent({
  data,
  models,
  modelsLoading,
  modelsError,
  health,
  healthLoading,
  healthError,
  period,
}: {
  data: UsageInsights;
  models: ModelUsage[];
  modelsLoading: boolean;
  modelsError: boolean;
  health?: HealthOverview;
  healthLoading: boolean;
  healthError: boolean;
  period: string;
}) {
  const { summary, savings, providers, recent, series } = data;

	return (
		<div className="space-y-6 pb-12">
			<div className="grid gap-4 lg:grid-cols-3">
				<SummaryCard
					icon={Activity}
					title="Traffic"
					primary={fmtCompact(summary.total_requests)}
					primaryLabel="requests"
					items={[
						{ label: "Tokens", value: fmtCompact(summary.total_tokens) },
						{ label: "Success / failed", value: `${fmtCompact(summary.successful_requests)} / ${fmtCompact(summary.failed_requests)}` },
						{ label: "Input / output", value: `${fmtCompact(summary.prompt_tokens)} / ${fmtCompact(summary.completion_tokens)}` },
					]}
				/>
				<SummaryCard
					icon={DollarSign}
					title="Spend & savings"
					primary={fmtUSD(summary.cost_usd)}
					primaryLabel="tracked cost"
					items={[
						{ label: "Value saved", value: fmtUSD(savings.usd_saved), tone: "good" },
						{ label: "Cost / request", value: fmtUSD(summary.cost_per_request_usd) },
						{ label: "Tokens saved", value: fmtCompact(savings.total_tokens_saved) },
					]}
					tone={summary.unpriced_requests > 0 ? "warning" : "accent"}
				/>
				<SummaryCard
					icon={ShieldCheck}
					title="Performance"
					primary={fmtRatio(summary.success_rate)}
					primaryLabel="successful"
					items={[
						{ label: "Avg latency", value: fmtMs(summary.avg_latency_ms) },
						{ label: "TTFT", value: fmtMs(summary.avg_ttft_ms) },
						{ label: "Tokens / request", value: fmtCompact(summary.tokens_per_request) },
					]}
					tone={summary.success_rate < 0.95 && summary.total_requests > 0 ? "warning" : "success"}
				/>
			</div>

			<PricingCoverageNotice summary={summary} />

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.7fr)_minmax(320px,1fr)]">
        <UsageTrendCard series={series} busiest={data.busiest} />
        <ProviderDistribution providers={providers} totalRequests={summary.total_requests} />
      </div>

      <TokenSavingsBreakdown
        savings={savings}
        totalRequests={summary.total_requests}
        insights={data}
        period={period}
      />

      <ProviderHealthOverview
        data={health}
        loading={healthLoading}
        error={healthError}
      />

      <ProviderBreakdown providers={providers} />
      <ModelUsageTable models={models} loading={modelsLoading} error={modelsError} />
      <RecentRequests records={recent} />
    </div>
  );
}

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
  ].filter((notice): notice is { label: string; detail: string } => Boolean(notice));

  if (notices.length === 0) return null;

  const hasCaution = summary.unpriced_requests > 0 || summary.legacy_usage_requests > 0 || summary.backfilled_requests > 0;
	return (
		<details
			className={`group overflow-hidden rounded-xl border ${
				hasCaution
					? "border-amber-300/60 bg-amber-50/40 dark:border-amber-500/30 dark:bg-amber-950/10"
					: "border-[var(--border)] bg-[var(--bg-elevated)]"
			}`}
		>
			<summary className="flex cursor-pointer list-none flex-wrap items-center gap-3 px-4 py-3 [&::-webkit-details-marker]:hidden">
				{hasCaution ? (
					<AlertTriangle className="h-4 w-4 shrink-0 text-warn" />
				) : (
					<Info className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
				)}
				<div className="min-w-[180px] flex-1">
					<p className="text-sm font-semibold text-[var(--text)]">Accounting quality</p>
					<p className="text-xs text-[var(--text-muted)]">
						{notices.length} audit note{notices.length === 1 ? "" : "s"} · expand for provenance
					</p>
				</div>
				<div className="ml-auto flex shrink-0 items-center gap-2">
				<CoveragePill label="Requests" ratio={summary.pricing_request_coverage} />
				<CoveragePill label="Tokens" ratio={summary.pricing_token_coverage} />
					<ChevronDown className="ml-1 h-4 w-4 text-[var(--text-muted)] transition-transform group-open:rotate-180" />
				</div>
			</summary>
			<div className="border-t border-[var(--border)] px-4 py-3">
				<ul className="grid gap-3 text-xs leading-5 text-[var(--text-muted)] lg:grid-cols-2">
					{notices.map((notice) => (
						<li key={notice.label}>
							<span className="font-semibold text-[var(--text)]">{notice.label}</span>
							<p>{notice.detail}</p>
						</li>
					))}
				</ul>
				<p className="mt-3 border-t border-[var(--border)] pt-2 text-[10px] text-[var(--text-muted)]">
					Coverage denominator: {fmtInteger(summary.pricing_eligible_requests)} token-bearing, pricing-eligible request{summary.pricing_eligible_requests === 1 ? "" : "s"}.
				</p>
			</div>
		</details>
	);
}

function CoveragePill({ label, ratio }: { label: string; ratio: number | null }) {
	return (
		<div className="min-w-16 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2.5 py-1 text-right">
			<div className="text-[9px] font-medium text-[var(--text-muted)]">{label}</div>
			<div className="text-xs font-semibold tabular-nums">{fmtRatio(ratio)}</div>
		</div>
	);
}

type MetricTone = "accent" | "success" | "warning";

function SummaryCard({
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
	items: Array<{ label: string; value: string; tone?: "good" }>;
	tone?: MetricTone;
}) {
	// The icon is a quiet glyph; only a warning tone keeps colour because it
	// carries meaning.
	const iconClass = tone === "warning" ? "text-warn" : "text-fg-faint";

	return (
		<Card className="px-4 py-3.5">
			<div className="flex items-center gap-1.5">
				<Icon className={`h-3.5 w-3.5 ${iconClass}`} strokeWidth={1.75} aria-hidden="true" />
				<span className="text-[12px] font-medium text-fg-muted">{title}</span>
			</div>
			<div className="mt-2 flex items-baseline gap-2">
				<span className="text-[24px] font-semibold leading-none tracking-[-0.02em] tabular-nums text-[var(--text)]">{primary}</span>
				<span className="text-xs text-[var(--text-muted)]">{primaryLabel}</span>
			</div>
			<div className="mt-4 grid grid-cols-3 gap-3 border-t border-[var(--border)] pt-3">
				{items.map((item) => (
					<div key={item.label} className="min-w-0">
						<div className={`truncate text-xs font-semibold tabular-nums sm:text-sm ${item.tone === "good" ? "text-ok" : "text-[var(--text)]"}`}>{item.value}</div>
						<div className="mt-0.5 text-[9px] font-medium text-[var(--text-muted)]">{item.label}</div>
					</div>
				))}
			</div>
		</Card>
	);
}

type TrendMetric = "requests" | "tokens" | "cost" | "failures";

type ChartPoint = SeriesPoint & { total_tokens: number };

const TREND_OPTIONS: { value: TrendMetric; label: string }[] = [
  { value: "requests", label: "Requests" },
  { value: "tokens", label: "Tokens" },
  { value: "cost", label: "Cost" },
  { value: "failures", label: "Failures" },
];

const TREND_CONFIG: Record<TrendMetric, { key: keyof ChartPoint; label: string; color: string }> = {
  requests: { key: "requests", label: "Requests", color: "var(--color-chart-1)" },
  tokens: { key: "total_tokens", label: "Tokens", color: "var(--color-chart-2)" },
  cost: { key: "cost_usd", label: "Cost", color: "var(--color-accent-500)" },
  failures: { key: "failures", label: "Failures", color: "var(--color-danger)" },
};

function UsageTrendCard({ series, busiest }: { series: SeriesPoint[]; busiest: string }) {
  const [metric, setMetric] = useState<TrendMetric>("requests");
  const points = useMemo<ChartPoint[]>(
    () => series.map((point) => ({ ...point, total_tokens: point.prompt_tokens + point.completion_tokens })),
    [series],
  );
  const config = TREND_CONFIG[metric];

  return (
    <Card className="flex min-h-[390px] flex-col">
      <div className="flex flex-col gap-3 border-b border-[var(--border)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <TrendingUp className="h-4 w-4 text-[var(--text-muted)]" />
            <h2 className="text-sm font-semibold">Usage trend</h2>
          </div>
          <p className="mt-1 text-xs text-[var(--text-muted)]">
            {busiest ? `Busiest request bucket: ${busiest}` : "No active request bucket in this period"}
          </p>
        </div>
        <SegmentedControl value={metric} onChange={setMetric} options={TREND_OPTIONS} />
      </div>
      <div className="min-h-0 flex-1 px-2 pb-4 pt-5">
        {points.length === 0 ? (
          <EmptyState title="No trend data for this period." />
        ) : (
          <ResponsiveContainer width="100%" height="100%" minHeight={280}>
            <AreaChart data={points} margin={{ top: 8, right: 18, left: 2, bottom: 2 }}>
              <defs>
                <linearGradient id="usageTrendFill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={config.color} stopOpacity={0.22} />
                  <stop offset="95%" stopColor={config.color} stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.45} />
              <XAxis
                dataKey="label"
                tick={{ fontSize: 10, fill: "var(--text-muted)", fontWeight: 500 }}
                tickLine={false}
                axisLine={false}
                dy={10}
              />
              <YAxis
                tick={{ fontSize: 10, fill: "var(--text-muted)", fontWeight: 500 }}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value: number) => formatTrendValue(metric, value, true)}
                width={66}
              />
              <Tooltip
                contentStyle={{
                  fontSize: 12,
                  background: "var(--bg-elevated)",
                  border: "1px solid var(--border)",
                  borderRadius: 10,
                  boxShadow: "var(--shadow-card)",
                }}
                formatter={(value) => [formatTrendValue(metric, Number(value)), config.label]}
                labelStyle={{ color: "var(--text-muted)", marginBottom: 4 }}
              />
              <Area
                type="monotone"
                dataKey={config.key}
                stroke={config.color}
                strokeWidth={2}
                fill="url(#usageTrendFill)"
                activeDot={{ r: 4, strokeWidth: 0, fill: config.color }}
              />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </div>
    </Card>
  );
}

function formatTrendValue(metric: TrendMetric, value: number, compact = false) {
  if (metric === "cost") return compact ? fmtUSDCompact(value) : fmtUSD(value);
  return compact ? fmtCompact(value) : fmtInteger(value);
}

function ProviderDistribution({ providers, totalRequests }: { providers: ProviderUsage[]; totalRequests: number }) {
  const active = providers
    .filter((provider) => provider.total_requests > 0)
    .slice()
    .sort((a, b) => b.total_requests - a.total_requests);

  return (
    <Card className="flex min-h-[390px] flex-col">
      <div className="border-b border-[var(--border)] px-5 py-4">
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-[var(--text-muted)]" />
          <h2 className="text-sm font-semibold">Provider distribution</h2>
        </div>
        <p className="mt-1 text-xs text-[var(--text-muted)]">Request and token shares from recorded terminal requests.</p>
      </div>
      {active.length === 0 ? (
        <EmptyState title="No provider activity." hint="Provider shares appear after requests are recorded." />
      ) : (
        <div className="flex flex-1 flex-col p-5">
          <div className="flex h-3 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
            {active.map((provider) => (
              <div
                key={provider.provider}
                style={{ width: `${provider.share_pct}%`, backgroundColor: provider.color || "var(--color-chart-1)" }}
                title={`${provider.display_name}: ${provider.share_pct.toFixed(1)}% of requests`}
              />
            ))}
          </div>
          <div className="mt-5 space-y-3">
            {active.slice(0, 6).map((provider) => (
              <div key={provider.provider} className="flex items-center gap-3">
                <ProviderIcon provider={provider.provider} src={provider.icon} color={provider.color} className="h-8 w-8" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate text-xs font-semibold">{provider.display_name || provider.provider}</span>
                    <span className="text-xs font-semibold tabular-nums">{provider.share_pct.toFixed(1)}%</span>
                  </div>
                  <div className="mt-0.5 flex items-center justify-between gap-3 text-[10px] text-[var(--text-muted)]">
                    <span>{fmtInteger(provider.total_requests)} requests</span>
                    <span>{provider.token_share_pct.toFixed(1)}% tokens</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
          <div className="mt-auto border-t border-[var(--border)] pt-3 text-[11px] text-[var(--text-muted)]">
            {fmtInteger(totalRequests)} terminal requests across {active.length} active provider{active.length === 1 ? "" : "s"}.
          </div>
        </div>
      )}
    </Card>
  );
}

function ProviderHealthOverview({
  data,
  loading,
  error,
}: {
  data?: HealthOverview;
  loading: boolean;
  error: boolean;
}) {
  const rows = useMemo(
    () => (data?.providers ?? []).slice().sort((a, b) => healthSeverity(a) - healthSeverity(b) || a.provider.localeCompare(b.provider)),
    [data?.providers],
  );
  const collectorWindow = formatDurationSeconds(data?.window.duration_seconds);

  return (
    <Card>
      <div className="flex flex-col gap-3 border-b border-[var(--border)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-[var(--text-muted)]" />
            <h2 className="text-sm font-semibold">Provider health</h2>
          </div>
			<p className="mt-1 text-xs text-[var(--text-muted)]">
				{collectorWindow
					? `Rolling ${collectorWindow} attempt telemetry.`
					: "Current rolling attempt telemetry."}
          </p>
        </div>
        <Link to="/provider-health" className="text-xs font-semibold text-accent-600 hover:text-accent-700 dark:text-accent-300">
          Open full health dashboard →
        </Link>
      </div>
      {loading ? (
        <Spinner />
      ) : error ? (
        <div className="p-4"><ErrorBanner message="Usage loaded, but provider-health telemetry is currently unavailable." /></div>
      ) : !data ? null : (
        <>
			{(data.summary.telemetry_dropped ?? 0) > 0 && (
				<div className="border-b border-amber-300/40 bg-amber-50/60 px-5 py-2.5 text-xs text-amber-800 dark:border-amber-500/20 dark:bg-amber-950/20 dark:text-amber-300">
					<strong>{fmtInteger(data.summary.telemetry_dropped ?? 0)} health events dropped.</strong> Provider rates may be incomplete; usage accounting is unaffected.
				</div>
			)}
			<div className="flex flex-wrap items-center divide-x divide-[var(--border)] border-b border-[var(--border)] px-2 py-3">
            <HealthSummaryItem label="Healthy" value={data.summary.healthy} tone="good" />
            <HealthSummaryItem label="Degraded" value={data.summary.degraded} tone="warn" />
            <HealthSummaryItem label="Unhealthy" value={data.summary.unhealthy} tone="bad" />
            <HealthSummaryItem label="Unknown" value={data.summary.unknown} />
            <HealthSummaryItem label="Fallbacks" value={data.summary.fallbacks} tone={data.summary.fallbacks > 0 ? "warn" : "muted"} />
            <HealthSummaryItem label="Avg p95" value={fmtMs(data.summary.avg_p95_latency_ms)} />
          </div>
          {rows.length === 0 ? (
            <EmptyState title="No provider health data yet." hint="Send traffic or run a provider probe to populate telemetry." />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[860px] text-xs">
                <thead>
                  <tr className="border-b border-[var(--border)] text-left text-[11.5px] font-medium text-[var(--text-muted)]">
                    <th className="px-4 py-2.5">Provider</th>
                    <th className="px-4 py-2.5">Status</th>
                    <th className="px-4 py-2.5 text-right">Success</th>
                    <th className="px-4 py-2.5 text-right">Errors</th>
                    <th className="px-4 py-2.5 text-right">p95 / TTFT</th>
                    <th className="px-4 py-2.5 text-right">Fallbacks</th>
                    <th className="px-4 py-2.5">Main issue</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {rows.map((row) => (
                    <tr key={row.provider} className="hover:bg-[var(--bg-subtle)]">
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2.5">
                          <ProviderIcon provider={row.provider} className="h-7 w-7" />
                          <span className="font-semibold">{row.provider}</span>
                        </div>
                      </td>
                      <td className="px-4 py-3"><HealthStatusBadge status={row.status} issue={row.main_issue} /></td>
                      <td className="px-4 py-3 text-right font-medium tabular-nums">{fmtHealthPercent(row.success_rate)}</td>
                      <td className="px-4 py-3 text-right tabular-nums text-[var(--text-muted)]">{fmtHealthPercent(row.error_rate)}</td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        <div>{fmtMs(row.latency_p95_ms)}</div>
                        <div className="text-[10px] text-[var(--text-muted)]">TTFT {fmtMs(row.ttft_p95_ms)}</div>
                      </td>
                      <td className="px-4 py-3 text-right font-medium tabular-nums">{fmtInteger(row.fallback_count)}</td>
                      <td className="max-w-xs px-4 py-3 text-[var(--text-muted)]">{humanize(row.main_issue) || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </Card>
  );
}

function healthSeverity(row: HealthProviderRow) {
  const rank = { unhealthy: 0, degraded: 1, unknown: 2, disabled: 3, healthy: 4 };
  return rank[row.status] ?? 5;
}

function HealthSummaryItem({
  label,
  value,
  tone = "muted",
}: {
  label: string;
  value: string | number;
  tone?: "muted" | "good" | "warn" | "bad";
}) {
  const color = tone === "good"
    ? "text-ok"
    : tone === "warn"
      ? "text-warn"
      : tone === "bad"
        ? "text-bad"
        : "text-[var(--text)]";
	return (
		<div className="min-w-24 px-3 py-1">
			<div className="text-[9px] font-medium text-[var(--text-muted)]">{label}</div>
			<div className={`mt-0.5 text-base font-semibold tabular-nums ${color}`}>{typeof value === "number" ? fmtInteger(value) : value}</div>
    </div>
  );
}

function ProviderBreakdown({ providers }: { providers: ProviderUsage[] }) {
  const rows = providers.filter((provider) => provider.total_requests > 0).slice().sort((a, b) => b.total_requests - a.total_requests);

  return (
    <Card>
      <div className="border-b border-[var(--border)] px-5 py-4">
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-[var(--text-muted)]" />
          <h2 className="text-sm font-semibold">Provider accounting</h2>
        </div>
		<p className="mt-1 text-xs text-[var(--text-muted)]">Requests, tokens, cost, latency, and pricing by provider.</p>
      </div>
      {rows.length === 0 ? (
        <EmptyState title="No provider usage for this period." />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1080px] text-xs">
            <thead>
              <tr className="border-b border-[var(--border)] text-[11.5px] font-medium text-[var(--text-muted)]">
                <th className="px-4 py-3 text-left">Provider</th>
                <th className="px-4 py-3 text-right">Requests</th>
                <th className="px-4 py-3 text-right">Input classes</th>
                <th className="px-4 py-3 text-right">Output classes</th>
                <th className="px-4 py-3 text-right">Cost / savings</th>
                <th className="px-4 py-3 text-right">Latency</th>
                <th className="px-4 py-3 text-right">Pricing</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {rows.map((provider) => (
                <tr key={provider.provider} className="hover:bg-[var(--bg-subtle)]">
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-3">
                      <ProviderIcon provider={provider.provider} src={provider.icon} color={provider.color} className="h-8 w-8" />
                      <div className="min-w-0">
                        <div className="truncate font-semibold">{provider.display_name || provider.provider}</div>
                        <div className="truncate font-mono text-[9px] text-[var(--text-muted)]">{provider.provider}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">
                    <div className="font-semibold">{fmtInteger(provider.total_requests)}</div>
                    <div className="text-[10px] text-[var(--text-muted)]">{fmtInteger(provider.failed_requests)} failed · {fmtRatio(provider.success_rate)}</div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">
                    <div>{fmtCompact(provider.prompt_tokens)} input</div>
                    <div className="text-[10px] text-[var(--text-muted)]">{fmtCompact(provider.cached_tokens)} cache read · {fmtCompact(provider.cache_write_tokens)} write</div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">
                    <div>{fmtCompact(provider.completion_tokens)} output</div>
                    <div className="text-[10px] text-[var(--text-muted)]">{fmtCompact(provider.reasoning_tokens)} reasoning subset</div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">
                    <div className="font-semibold">{fmtUSD(provider.cost_usd)}</div>
                    <div className="text-[10px] text-ok">{fmtUSD(provider.saved_cost_usd + provider.avoided_cost_usd)} saved</div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">
                    <div>{fmtMs(provider.avg_latency_ms)}</div>
                    <div className="text-[10px] text-[var(--text-muted)]">TTFT {fmtMs(provider.avg_ttft_ms)}</div>
                  </td>
					<td className="px-4 py-3 text-right">
						<div className="font-semibold tabular-nums">{fmtRatio(provider.pricing_request_coverage)}</div>
						<div className="text-[10px] tabular-nums text-[var(--text-muted)]">
							{provider.pricing_eligible_requests > 0
								? `${fmtInteger(Math.max(0, provider.pricing_eligible_requests - provider.unpriced_requests))} / ${fmtInteger(provider.pricing_eligible_requests)} eligible covered`
								: "No pricing-eligible usage"}
                    </div>
                    <div className="mt-0.5 text-[9px] tabular-nums text-[var(--text-muted)]">
                      {fmtInteger(provider.estimated_requests)} pricing est. · {fmtInteger(provider.estimated_usage_requests)} usage est.
                    </div>
                    <div className="text-[9px] tabular-nums text-[var(--text-muted)]">
                      {fmtInteger(provider.legacy_usage_requests)} legacy · {fmtInteger(provider.backfilled_requests)} backfilled
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

type ModelSortKey = "model" | "requests" | "tokens" | "cost" | "latency" | "coverage";

function ModelUsageTable({ models, loading, error }: { models: ModelUsage[]; loading: boolean; error: boolean }) {
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<ModelSortKey>("cost");
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("desc");

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase();
    const rows = query
      ? models.filter((model) => `${model.provider} ${model.provider_name} ${model.model}`.toLowerCase().includes(query))
      : models;
    return rows.slice().sort((a, b) => {
      const direction = sortDirection === "asc" ? 1 : -1;
      switch (sortKey) {
        case "model": return direction * `${a.provider}/${a.model}`.localeCompare(`${b.provider}/${b.model}`);
        case "requests": return direction * (a.total_requests - b.total_requests);
        case "tokens": return direction * (a.total_tokens - b.total_tokens);
        case "cost": return direction * (a.cost_usd - b.cost_usd);
        case "latency": return direction * (a.avg_latency_ms - b.avg_latency_ms);
			case "coverage":
				if (a.pricing_request_coverage == null && b.pricing_request_coverage == null) return 0;
				if (a.pricing_request_coverage == null) return 1;
				if (b.pricing_request_coverage == null) return -1;
				return direction * (a.pricing_request_coverage - b.pricing_request_coverage);
      }
    });
  }, [models, search, sortDirection, sortKey]);

  const { page, pages, paged, setPage, total } = useClientPagination(filtered, 10);

  const toggleSort = (key: ModelSortKey) => {
    if (sortKey === key) setSortDirection((current) => current === "asc" ? "desc" : "asc");
    else {
      setSortKey(key);
      setSortDirection(key === "model" ? "asc" : "desc");
    }
  };

  return (
    <Card>
      <div className="flex flex-col gap-3 border-b border-[var(--border)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <Layers3 className="h-4 w-4 text-[var(--text-muted)]" />
            <h2 className="text-sm font-semibold">Model accounting</h2>
          </div>
			<p className="mt-1 text-xs text-[var(--text-muted)]">Usage, cost, and immutable pricing snapshots by model.</p>
        </div>
        <div className="relative w-full sm:w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--text-muted)]" />
          <input
            aria-label="Search model accounting by provider or model"
            value={search}
            onChange={(event) => { setSearch(event.target.value); setPage(1); }}
            placeholder="Search provider or model…"
            className="h-9 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] pl-9 pr-3 text-xs outline-none transition-colors placeholder:text-[var(--text-muted)] hover:border-[var(--border-strong)] focus:border-accent-400"
          />
        </div>
      </div>
      {loading ? (
        <Spinner />
      ) : error ? (
        <div className="p-4"><ErrorBanner message="Failed to load model-level usage." /></div>
      ) : filtered.length === 0 ? (
        <EmptyState title={models.length === 0 ? "No model usage for this period." : "No models match your search."} />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[1120px] text-xs">
              <thead>
                <tr className="border-b border-[var(--border)] text-[11.5px] font-medium text-[var(--text-muted)]">
                  <SortableHeader label="Provider / model" sortKey="model" active={sortKey} direction={sortDirection} onSort={toggleSort} />
                  <SortableHeader label="Requests" sortKey="requests" active={sortKey} direction={sortDirection} onSort={toggleSort} align="right" />
                  <SortableHeader label="Tokens" sortKey="tokens" active={sortKey} direction={sortDirection} onSort={toggleSort} align="right" />
                  <SortableHeader label="Cost" sortKey="cost" active={sortKey} direction={sortDirection} onSort={toggleSort} align="right" />
                  <SortableHeader label="Latency" sortKey="latency" active={sortKey} direction={sortDirection} onSort={toggleSort} align="right" />
                  <SortableHeader label="Pricing" sortKey="coverage" active={sortKey} direction={sortDirection} onSort={toggleSort} align="right" />
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border)]">
                {paged.map((model) => (
                  <tr key={`${model.provider}/${model.model}`} className="hover:bg-[var(--bg-subtle)]">
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-3">
                        <ProviderIcon provider={model.provider} src={model.provider_icon} color={model.provider_color} className="h-8 w-8" />
                        <div className="min-w-0">
                          <div className="max-w-sm truncate font-mono text-[11px] font-semibold" title={model.model}>{model.model}</div>
                          <div className="truncate text-[9px] text-[var(--text-muted)]">{model.provider_name || model.provider}</div>
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums">
                      <div className="font-semibold">{fmtInteger(model.total_requests)}</div>
                      <div className="text-[10px] text-[var(--text-muted)]">{fmtRatio(model.success_rate)} success</div>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums">
                      <div className="font-semibold">{fmtCompact(model.total_tokens)}</div>
                      <div className="text-[10px] text-[var(--text-muted)]">{fmtCompact(model.prompt_tokens)} in · {fmtCompact(model.completion_tokens)} out</div>
                      <div className="text-[9px] text-[var(--text-muted)]">{fmtCompact(model.cached_tokens)} cached · {fmtCompact(model.reasoning_tokens)} reasoning</div>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums">
                      <div className="font-semibold">{model.pricing_status === "missing" ? "Unpriced" : fmtUSD(model.cost_usd)}</div>
                      <div className="text-[10px] text-ok">{fmtUSD(model.saved_cost_usd + model.avoided_cost_usd)} saved</div>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums">
                      <div>{fmtMs(model.avg_latency_ms)}</div>
                      <div className="text-[10px] text-[var(--text-muted)]">TTFT {fmtMs(model.avg_ttft_ms)}</div>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex justify-end"><PricingBadge status={model.pricing_status} /></div>
						<div className="mt-1 text-[10px] font-medium tabular-nums text-[var(--text-muted)]">{fmtRatio(model.pricing_request_coverage)} covered</div>
						<div className="text-[9px] tabular-nums text-[var(--text-muted)]">
							{model.pricing_eligible_requests > 0
								? `${fmtInteger(Math.max(0, model.pricing_eligible_requests - model.unpriced_requests))} / ${fmtInteger(model.pricing_eligible_requests)} pricing-eligible`
								: "No pricing-eligible usage"}
                      </div>
                      <div className="mt-0.5 text-[9px] tabular-nums text-[var(--text-muted)]">
                        {fmtInteger(model.estimated_requests)} pricing est. · {fmtInteger(model.estimated_usage_requests)} usage est.
                      </div>
                      <div className="text-[9px] tabular-nums text-[var(--text-muted)]">
                        {fmtInteger(model.legacy_usage_requests)} legacy · {fmtInteger(model.backfilled_requests)} backfilled
                      </div>
                      {model.pricing_mixed ? (
                        <div className="mt-1 text-[9px] font-semibold text-warn">Multiple immutable snapshots</div>
                      ) : (
                        <>
                          <div className="mt-1 max-w-xs truncate font-mono text-[9px] text-[var(--text-muted)]" title={model.pricing_key || undefined}>{model.pricing_key || "No pricing key"}</div>
                          <div className="mt-0.5 text-[9px] text-[var(--text-muted)]">{formatRates(model)}</div>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
        </>
      )}
    </Card>
  );
}

function SortableHeader({
  label,
  sortKey,
  active,
  direction,
  onSort,
  align = "left",
}: {
  label: string;
  sortKey: ModelSortKey;
  active: ModelSortKey;
  direction: "asc" | "desc";
  onSort: (key: ModelSortKey) => void;
  align?: "left" | "right";
}) {
  const Icon = active !== sortKey ? ArrowUpDown : direction === "asc" ? ArrowUp : ArrowDown;
  const ariaSort = active === sortKey ? (direction === "asc" ? "ascending" : "descending") : "none";
  return (
    <th
      aria-sort={ariaSort}
      className={`px-4 py-3 ${align === "right" ? "text-right" : "text-left"}`}
    >
      <button
        type="button"
        onClick={() => onSort(sortKey)}
        className={`inline-flex items-center gap-1 transition-colors hover:text-[var(--text)] ${align === "right" ? "flex-row-reverse" : ""}`}
      >
        {label}<Icon className={`h-3 w-3 ${active === sortKey ? "opacity-100" : "opacity-30"}`} />
      </button>
    </th>
  );
}

function formatRates(model: ModelUsage) {
  if (model.pricing_mixed) return "Multiple immutable snapshots";
  if (model.pricing_status === "legacy") return "Legacy rate breakdown unavailable";
  if (model.pricing_status === "missing" || model.pricing_status === "none") return "Rates unavailable";
  return `${fmtRate(model.input_per_m)} in · ${fmtRate(model.cached_input_per_m)} cache · ${fmtRate(model.output_per_m)} out`;
}

function RecentRequests({ records }: { records: RecentActivity[] }) {
  const [selected, setSelected] = useState<RecentActivity | null>(null);
  const { page, pages, paged, setPage, total } = useClientPagination(records, 12);

  return (
    <>
      <Card>
        <div className="border-b border-[var(--border)] px-5 py-4">
          <div className="flex items-center gap-2">
            <Activity className="h-4 w-4 text-[var(--text-muted)]" />
            <h2 className="text-sm font-semibold">Recent terminal requests</h2>
          </div>
		<p className="mt-1 text-xs text-[var(--text-muted)]">Open a request to audit tokens, cost, pricing, and latency.</p>
        </div>
        {records.length === 0 ? (
          <EmptyState title="No requests in this period." />
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[1060px] text-xs">
                <thead>
                  <tr className="border-b border-[var(--border)] text-[11.5px] font-medium text-[var(--text-muted)]">
                    <th className="px-4 py-3 text-left">Status</th>
                    <th className="px-4 py-3 text-left">Provider / model</th>
                    <th className="px-4 py-3 text-right">Input</th>
                    <th className="px-4 py-3 text-right">Output</th>
                    <th className="px-4 py-3 text-right">Cost</th>
                    <th className="px-4 py-3 text-right">Latency</th>
                    <th className="px-4 py-3 text-right">Time</th>
                    <th className="px-4 py-3 text-right">Detail</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {paged.map((record) => (
                    <tr
                      key={record.id}
                      className="transition-colors hover:bg-[var(--bg-subtle)]"
                    >
                      <td className="px-4 py-3">
                        <div className="flex flex-col items-start gap-1.5">
                          <StatusBadge status={record.status} />
                          <UsageSourceBadge source={record.usage_source} />
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-3">
                          <ProviderIcon provider={record.provider} src={record.provider_icon} color={record.provider_color} className="h-8 w-8" />
                          <div className="min-w-0">
                            <div className="max-w-sm truncate font-mono text-[11px] font-semibold" title={record.model}>{record.model || "—"}</div>
                            <div className="truncate text-[9px] text-[var(--text-muted)]">{record.provider_name || record.provider}</div>
                          </div>
                        </div>
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        <div className="font-semibold">{fmtInteger(record.prompt_tokens)}</div>
                        <div className="text-[10px] text-[var(--text-muted)]">{fmtInteger(record.cached_tokens)} read · {fmtInteger(record.cache_write_tokens)} write</div>
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        <div className="font-semibold">{fmtInteger(record.completion_tokens)}</div>
                        <div className="text-[10px] text-[var(--text-muted)]">{fmtInteger(record.reasoning_tokens)} reasoning</div>
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        <div className="font-semibold">{record.pricing_status === "missing" ? "Unpriced" : fmtUSD(record.cost_usd)}</div>
                        <div className="mt-1 flex justify-end"><PricingBadge status={record.pricing_status} /></div>
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        <div>{fmtMs(record.end_to_end_latency_ms)}</div>
                        <div className="text-[10px] text-[var(--text-muted)]">upstream {fmtMs(record.upstream_latency_ms)}</div>
                      </td>
                      <td className="px-4 py-3 text-right whitespace-nowrap text-[var(--text-muted)]" title={formatDateTime(record.created_at)}>{relativeTime(record.created_at)}</td>
                      <td className="px-4 py-3 text-right">
                        <button
                          type="button"
                          onClick={() => setSelected(record)}
                          aria-label={`View accounting detail for request ${record.request_id || record.id}`}
                          className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2.5 py-1.5 text-[10px] font-semibold text-[var(--text-muted)] transition-colors hover:border-[var(--border-strong)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-400"
                        >
                          <Info className="h-3 w-3" aria-hidden="true" /> Details
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
          </>
        )}
      </Card>

      <Modal
        open={selected != null}
        onClose={() => setSelected(null)}
        title="Request details"
        subtitle="Accounting, performance, and pricing audit"
        maxWidth="max-w-4xl"
      >
        {selected && <RequestDetail record={selected} />}
      </Modal>
    </>
  );
}

type RequestDetailTab = "overview" | "pricing";

type RequestNotice = {
  title: string;
  message: string;
  tone: "info" | "warning" | "danger";
  pricing?: boolean;
};

const REQUEST_DETAIL_TABS: { value: RequestDetailTab; label: string; icon: LucideIcon }[] = [
  { value: "overview", label: "Overview", icon: Activity },
  { value: "pricing", label: "Pricing & audit", icon: Database },
];

function RequestDetail({ record }: { record: RecentActivity }) {
  const [tab, setTab] = useState<RequestDetailTab>("overview");
  const optimizationFlags = [
    record.slim_active && "RTK",
    record.caveman_active && "Caveman",
    record.terse_active && "Terse",
    record.headroom_active && "Headroom",
    record.ponytail_active && "Ponytail",
  ].filter((value): value is string => Boolean(value));
  const legacyBreakdownUnavailable = record.pricing_status === "legacy" && !record.pricing_backfilled;
  const cacheHit = record.cache_hit || record.status === "cache_hit" || record.usage_source === "cache";
  const sourceURL = safeExternalURL(record.pricing_source_url);
  const totalTokens = record.prompt_tokens + record.completion_tokens;
  const regularInputTokens = Math.max(0, record.prompt_tokens - record.cached_tokens - record.cache_write_tokens);
  const regularOutputTokens = Math.max(0, record.completion_tokens - record.reasoning_tokens);
  const pricingUnavailable = record.pricing_status === "missing";
  const hasOptimizationDetail = optimizationFlags.length > 0
    || record.slim_tokens_saved > 0
    || record.slim_bytes_saved > 0
    || record.headroom_tokens_saved > 0
    || record.headroom_bytes_saved > 0
    || Boolean(record.slim_rules);
  const chargedTotal = pricingUnavailable
    ? "Unpriced"
    : cacheHit
      ? "$0.00"
      : fmtUSD(record.cost_usd);
  const notices: RequestNotice[] = [];

  if (record.error_kind) {
    notices.push({
      title: "Terminal error",
      message: humanize(record.error_kind),
      tone: "danger",
    });
  }
  if (pricingUnavailable) {
    notices.push({
      title: "Pricing unavailable",
      message: "No catalog or custom rate matched. Tokens are retained and cost remains unpriced.",
      tone: "warning",
      pricing: true,
    });
  }
  if (record.usage_source === "estimated") {
    notices.push({
      title: "Estimated usage",
      message: "Token counts were estimated because authoritative provider usage was unavailable.",
      tone: "info",
    });
  }
  if (legacyBreakdownUnavailable) {
    notices.push({
      title: "Legacy accounting",
      message: `The historical total ${fmtUSD(record.cost_usd)} was retained, but its component and rate split was not stored.`,
      tone: "warning",
      pricing: true,
    });
  }
  if (record.pricing_backfilled) {
    notices.push({
      title: "Historical estimate",
      message: "Current rates were applied later during backfill; this is not the original request-time snapshot.",
      tone: "warning",
      pricing: true,
    });
  }
  if (record.pricing_status === "partial") {
    notices.push({
      title: "Partial pricing",
      message: "Only matched components are included in the charged total.",
      tone: "warning",
      pricing: true,
    });
  }
  if (cacheHit) {
    notices.push({
      title: "Served from cache",
      message: "Component costs show avoided provider spend; the charged total is zero.",
      tone: "info",
      pricing: true,
    });
  }

  const pricingNotices = notices.filter((notice) => notice.pricing);

  return (
    <div className="flex h-[76vh] max-h-[720px] min-h-0 flex-col overflow-hidden">
      <div className="shrink-0 px-5 pt-4 sm:px-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-w-0 items-center gap-3">
            <ProviderIcon provider={record.provider} src={record.provider_icon} color={record.provider_color} className="h-10 w-10" />
            <div className="min-w-0">
              <div className="truncate font-mono text-sm font-semibold" title={record.model}>{record.model || "Unknown model"}</div>
              <div className="mt-0.5 truncate text-xs text-[var(--text-muted)]">
                {record.provider_name || record.provider} · {formatDateTime(record.created_at)}
              </div>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={record.status} />
            <UsageSourceBadge source={record.usage_source} />
            <PricingBadge status={record.pricing_status} />
          </div>
        </div>
        <div className="mt-4">
          <TabBar tabs={REQUEST_DETAIL_TABS} active={tab} onChange={setTab} />
        </div>
      </div>

      {tab === "overview" ? (
        <div
          role="tabpanel"
          aria-label="Request overview"
          className="min-h-0 flex-1 overflow-y-auto px-5 py-5 sm:px-6"
        >
          <div className="space-y-4">
            <DetailSummaryStrip
              items={[
                {
                  label: "Total tokens",
                  value: fmtInteger(totalTokens),
                  hint: `${fmtInteger(record.prompt_tokens)} input · ${fmtInteger(record.completion_tokens)} output`,
                },
                {
                  label: "Charged cost",
                  value: chargedTotal,
                  hint: cacheHit ? "Cache served" : humanize(record.pricing_status),
                },
                {
                  label: "End-to-end latency",
                  value: fmtMs(record.end_to_end_latency_ms),
                  hint: `TTFT ${fmtMs(record.ttft_ms)}`,
                },
              ]}
            />

            <RequestNoticeList notices={notices} />

            <div className="grid gap-4 lg:grid-cols-2">
              <DetailPanel
                icon={Layers3}
                title="Token usage"
                description="Cache and reasoning are included in their respective totals."
              >
                <div className="grid gap-5 sm:grid-cols-2 sm:gap-0 sm:divide-x sm:divide-[var(--border)]">
                  <TokenBreakdown
                    className="sm:pr-4"
                    label="Input total"
                    value={fmtInteger(record.prompt_tokens)}
                    rows={[
                      { label: "Regular", value: fmtInteger(regularInputTokens) },
                      { label: "Cache read", value: fmtInteger(record.cached_tokens) },
                      { label: "Cache write", value: fmtInteger(record.cache_write_tokens) },
                    ]}
                  />
                  <TokenBreakdown
                    className="sm:pl-4"
                    label="Output total"
                    value={fmtInteger(record.completion_tokens)}
                    rows={[
                      { label: "Regular", value: fmtInteger(regularOutputTokens) },
                      { label: "Reasoning", value: fmtInteger(record.reasoning_tokens) },
                    ]}
                  />
                </div>
              </DetailPanel>

              <DetailPanel
                icon={DollarSign}
                title={cacheHit ? "Avoided provider cost" : "Cost breakdown"}
                description={
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
                  <BreakdownUnavailable
                    label="Legacy total"
                    value={fmtUSD(record.cost_usd)}
                    message="A reconcilable component split was not retained for this row."
                  />
                ) : pricingUnavailable ? (
                  <BreakdownUnavailable
                    label="Charged total"
                    value="Unpriced"
                    message="Zero-valued components are hidden so missing pricing is not mistaken for free usage."
                  />
                ) : (
                  <>
                    <div className="divide-y divide-[var(--border)]">
                      <DetailValueRow label="Regular input" value={fmtUSD(record.input_cost_usd)} />
                      <DetailValueRow label="Cached input" value={fmtUSD(record.cached_cost_usd)} />
                      <DetailValueRow label="Cache write" value={fmtUSD(record.cache_write_cost_usd)} />
                      <DetailValueRow label="Regular output" value={fmtUSD(record.output_cost_usd)} />
                      <DetailValueRow label="Reasoning" value={fmtUSD(record.reasoning_cost_usd)} />
                    </div>
                    {(record.saved_cost_usd > 0 || record.avoided_cost_usd > 0) && (
                      <div className="mt-3 grid grid-cols-2 gap-px overflow-hidden rounded-lg bg-[var(--border)]">
                        <DetailMiniMetric label="Saved" value={fmtUSD(record.saved_cost_usd)} tone="good" />
                        <DetailMiniMetric label="Avoided" value={fmtUSD(record.avoided_cost_usd)} tone="good" />
                      </div>
                    )}
                  </>
                )}
              </DetailPanel>
            </div>

            <div className="grid gap-4 lg:grid-cols-[minmax(0,0.85fr)_minmax(0,1.15fr)]">
              <DetailPanel
                icon={Timer}
                title="Latency"
                description="Router completion includes upstream time."
              >
                <div className="grid grid-cols-3 gap-px overflow-hidden rounded-lg bg-[var(--border)]">
                  <DetailMiniMetric label="Upstream" value={fmtMs(record.upstream_latency_ms)} />
                  <DetailMiniMetric label="End to end" value={fmtMs(record.end_to_end_latency_ms)} />
                  <DetailMiniMetric label="TTFT" value={fmtMs(record.ttft_ms)} />
                </div>
              </DetailPanel>

              <DetailPanel icon={Zap} title="Optimizations">
                {!hasOptimizationDetail ? (
                  <p className="text-xs text-[var(--text-muted)]">No optimization was recorded for this request.</p>
                ) : (
                  <div className="space-y-3">
                    {optimizationFlags.length > 0 && (
                      <div className="flex flex-wrap gap-2">
                        {optimizationFlags.map((flag) => <Badge key={flag} tone="accent">{flag}</Badge>)}
                      </div>
                    )}
                    <div className="divide-y divide-[var(--border)]">
                      {(record.slim_active || record.slim_tokens_saved > 0 || record.slim_bytes_saved > 0) && (
                        <DetailValueRow
                          label="Slim saved"
                          value={`${fmtInteger(record.slim_tokens_saved)} tokens · ${fmtBytes(record.slim_bytes_saved)}`}
                        />
                      )}
                      {(record.headroom_active || record.headroom_tokens_saved > 0 || record.headroom_bytes_saved > 0) && (
                        <DetailValueRow
                          label="Headroom saved"
                          value={`${fmtInteger(record.headroom_tokens_saved)} tokens · ${fmtBytes(record.headroom_bytes_saved)}`}
                        />
                      )}
                    </div>
                    {record.slim_rules && (
                      <div className="rounded-lg bg-[var(--bg-subtle)] px-3 py-2 text-[11px] leading-4">
                        <span className="font-semibold text-[var(--text)]">Applied rules:</span>{" "}
                        <span className="break-words font-mono text-[var(--text-muted)]">{record.slim_rules}</span>
                      </div>
                    )}
                  </div>
                )}
              </DetailPanel>
            </div>
          </div>
        </div>
      ) : (
        <div
          role="tabpanel"
          aria-label="Pricing and audit details"
          className="min-h-0 flex-1 overflow-y-auto px-5 py-5 sm:px-6"
        >
          <div className="space-y-4">
            <RequestNoticeList notices={pricingNotices} />

            <div className="grid gap-4 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)]">
              <DetailPanel
                icon={Database}
                title="Pricing provenance"
                description="The source and match retained with this request."
              >
                <div className="divide-y divide-[var(--border)]">
                  <KeyValue label="Status" value={<PricingBadge status={record.pricing_status} />} />
                  <KeyValue label="Source" value={record.pricing_source || "—"} />
                  <KeyValue label="Match" value={humanize(record.pricing_match_kind) || "—"} />
                  <KeyValue label="Resolved key" value={<span className="break-all font-mono text-[11px]">{record.pricing_key || "—"}</span>} />
                  <KeyValue
                    label="Source URL"
                    value={record.pricing_source_url ? (
                      sourceURL ? (
                        <a
                          href={sourceURL}
                          target="_blank"
                          rel="noreferrer"
                          title={record.pricing_source_url}
                          className="text-accent-600 underline decoration-accent-400/50 underline-offset-2 hover:text-accent-700 dark:text-accent-300"
                        >
                          Open pricing source
                        </a>
                      ) : (
                        <span className="break-all">{record.pricing_source_url}</span>
                      )
                    ) : "—"}
                  />
                  <KeyValue label="Pricing as of" value={record.pricing_as_of ? formatDateTime(record.pricing_as_of) : "—"} />
                  <KeyValue
                    label="Backfilled"
                    value={record.pricing_backfilled ? <Badge tone="warning">Historical estimate</Badge> : <Badge tone="neutral">No</Badge>}
                  />
                </div>
              </DetailPanel>

              <DetailPanel
                icon={DollarSign}
                title="Rates per 1M tokens"
                description="USD rates used for each component."
              >
                {legacyBreakdownUnavailable || pricingUnavailable ? (
                  <BreakdownUnavailable
                    label="Rate snapshot"
                    value="Unavailable"
                    message={legacyBreakdownUnavailable
                      ? "No immutable component-rate snapshot was stored for this legacy total."
                      : "No matching price was found for this request."}
                  />
                ) : (
                  <div className="divide-y divide-[var(--border)]">
                    <DetailValueRow label="Regular input" value={fmtRate(record.input_rate_per_m)} />
                    <DetailValueRow label="Cached input" value={fmtRate(record.cached_rate_per_m)} />
                    <DetailValueRow label="Cache write" value={fmtRate(record.cache_write_rate_per_m)} />
                    <DetailValueRow label="Regular output" value={fmtRate(record.output_rate_per_m)} />
                    <DetailValueRow label="Reasoning output" value={fmtRate(record.reasoning_rate_per_m)} />
                  </div>
                )}
              </DetailPanel>
            </div>

            <DetailPanel
              icon={Info}
              title="Audit identifiers"
              description="Stable references for logs and reconciliation."
            >
              <div className="divide-y divide-[var(--border)]">
                <KeyValue label="Request ID" value={<span className="break-all font-mono text-[11px]">{record.request_id || "—"}</span>} />
                <KeyValue label="Usage row" value={<span className="break-all font-mono text-[11px]">{record.id}</span>} />
                <KeyValue label="Recorded at" value={formatDateTime(record.created_at)} />
                <KeyValue label="Provider key" value={<span className="font-mono text-[11px]">{record.provider || "—"}</span>} />
              </div>
            </DetailPanel>
          </div>
        </div>
      )}
    </div>
  );
}

function DetailSummaryStrip({ items }: { items: { label: string; value: string; hint: string }[] }) {
  return (
    <div className="grid overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--border)] sm:grid-cols-3 sm:gap-px">
      {items.map((item) => (
        <div key={item.label} className="border-b border-[var(--border)] bg-[var(--bg-elevated)] px-4 py-3 last:border-b-0 sm:border-b-0">
          <div className="text-[9px] font-medium text-[var(--text-muted)]">{item.label}</div>
          <div className="mt-1 text-lg font-semibold tabular-nums tracking-tight text-[var(--text)]">{item.value}</div>
          <div className="mt-0.5 truncate text-[10px] text-[var(--text-muted)]" title={item.hint}>{item.hint}</div>
        </div>
      ))}
    </div>
  );
}

function RequestNoticeList({ notices }: { notices: RequestNotice[] }) {
  if (notices.length === 0) return null;
  const tone = notices.some((notice) => notice.tone === "danger")
    ? "danger"
    : notices.some((notice) => notice.tone === "warning")
      ? "warning"
      : "info";
  const toneClasses = tone === "danger"
    ? "border-red-300/50 bg-red-50/60 text-red-800 dark:border-red-500/30 dark:bg-red-950/20 dark:text-red-300"
    : tone === "warning"
      ? "border-amber-300/50 bg-amber-50/60 text-amber-800 dark:border-amber-500/30 dark:bg-amber-950/20 dark:text-amber-300"
      : "border-blue-300/50 bg-blue-50/60 text-blue-800 dark:border-blue-500/30 dark:bg-blue-950/20 dark:text-blue-300";
  const Icon = tone === "info" ? Info : AlertTriangle;

  return (
    <div className={`flex items-start gap-3 rounded-xl border px-4 py-3 ${toneClasses}`}>
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="min-w-0 space-y-1.5 text-[11px] leading-4">
        {notices.map((notice) => (
          <p key={`${notice.title}-${notice.message}`}>
            <span className="font-semibold">{notice.title}:</span> {notice.message}
          </p>
        ))}
      </div>
    </div>
  );
}

function DetailPanel({
  icon: Icon,
  title,
  description,
  children,
}: {
  icon: LucideIcon;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)]">
      <div className="flex items-start gap-2.5 border-b border-[var(--border)] px-4 py-3">
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-[var(--bg-subtle)] text-[var(--text-muted)]">
          <Icon className="h-3.5 w-3.5" aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <h3 className="text-xs font-semibold text-[var(--text)]">{title}</h3>
          {description && <p className="mt-0.5 text-[10px] leading-4 text-[var(--text-muted)]">{description}</p>}
        </div>
      </div>
      <div className="p-4">{children}</div>
    </section>
  );
}

function TokenBreakdown({
  className = "",
  label,
  value,
  rows,
}: {
  className?: string;
  label: string;
  value: string;
  rows: { label: string; value: string }[];
}) {
  return (
    <div className={className}>
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[11.5px] font-medium text-[var(--text-muted)]">{label}</span>
        <span className="text-lg font-semibold tabular-nums text-[var(--text)]">{value}</span>
      </div>
      <div className="mt-2 divide-y divide-[var(--border)]">
        {rows.map((row) => <DetailValueRow key={row.label} label={row.label} value={row.value} />)}
      </div>
    </div>
  );
}

function DetailValueRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 py-2 text-xs first:pt-0 last:pb-0">
      <span className="text-[var(--text-muted)]">{label}</span>
      <span className="text-right font-semibold tabular-nums text-[var(--text)]">{value}</span>
    </div>
  );
}

function DetailMiniMetric({ label, value, tone = "normal" }: { label: string; value: string; tone?: "normal" | "good" }) {
  return (
    <div className="min-w-0 bg-[var(--bg-subtle)] px-3 py-2.5">
      <div className="truncate text-[9px] font-medium text-[var(--text-muted)]" title={label}>{label}</div>
      <div className={`mt-1 truncate text-xs font-semibold tabular-nums ${tone === "good" ? "text-ok" : "text-[var(--text)]"}`} title={value}>{value}</div>
    </div>
  );
}

function BreakdownUnavailable({ label, value, message }: { label: string; value: string; message: string }) {
  return (
    <div className="rounded-lg bg-[var(--bg-subtle)] px-4 py-3">
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[11.5px] font-medium text-[var(--text-muted)]">{label}</span>
        <span className="text-sm font-semibold tabular-nums text-[var(--text)]">{value}</span>
      </div>
      <p className="mt-2 text-[10px] leading-4 text-[var(--text-muted)]">{message}</p>
    </div>
  );
}

function KeyValue({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-[var(--border)] py-2 text-xs first:pt-0 last:border-0 last:pb-0">
      <span className="shrink-0 text-[var(--text-muted)]">{label}</span>
      <span className="min-w-0 text-right font-medium">{value}</span>
    </div>
  );
}

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
    priced: { label: "Priced", tone: "success" },
    estimated: { label: "Pricing estimate", tone: "warning" },
    free: { label: "Explicit free", tone: "accent" },
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
    provider: { label: "Provider usage", tone: "success" },
    estimated: { label: "Usage estimate", tone: "warning" },
    cache: { label: "Cache replay", tone: "accent" },
    legacy: { label: "Legacy usage", tone: "neutral" },
    none: { label: "No usage reported", tone: "neutral" },
  };
  const item = config[source] ?? { label: humanize(String(source)) || "Unknown usage", tone: "neutral" as const };
  return <Badge tone={item.tone}>{item.label}</Badge>;
}

function ProviderIcon({
  provider,
  src,
  color,
  className = "h-8 w-8",
}: {
  provider: string;
  src?: string;
  color?: string;
  className?: string;
}) {
  const candidates = useMemo(() => providerIconCandidates(provider, src), [provider, src]);
  const [index, setIndex] = useState(0);

  useEffect(() => setIndex(0), [provider, src]);

  const current = candidates[index];
  if (!current) {
    const initials = provider.replace(/^custom-/, "").split(/[-_]/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase() || "AI";
    return (
      <span
        className={`flex shrink-0 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] text-[9px] font-bold text-[var(--text-muted)] ${className}`}
        style={color ? { boxShadow: `inset 3px 0 0 ${color}` } : undefined}
        aria-label={provider}
      >
        {initials}
      </span>
    );
  }

  return (
    <span
      className={`flex shrink-0 items-center justify-center rounded-lg border border-[var(--border)] bg-white p-1 shadow-sm dark:bg-black/20 ${className}`}
      style={color ? { boxShadow: `inset 3px 0 0 ${color}` } : undefined}
    >
      <img
        src={current}
        alt={provider}
        className="h-full w-full object-contain"
        onError={() => setIndex((currentIndex) => currentIndex + 1)}
      />
    </span>
  );
}

function providerIconCandidates(provider: string, source?: string) {
  const values: string[] = [];
  const add = (value?: string) => {
    if (value && !values.includes(value)) values.push(value);
  };
  add(source);
  add(`/providers/${provider}.png`);
  if (provider.startsWith("custom-openai")) add("/providers/custom-openai.png");
  if (provider.startsWith("custom-anthropic")) add("/providers/custom-anthropic.png");
  if (provider === "openai-codex") add("/providers/codex.png");
  if (provider === "workers-ai") add("/providers/cloudflare-ai.png");
  return values;
}

function safeExternalURL(value: string) {
  if (!value) return null;
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.toString() : null;
  } catch {
    return null;
  }
}

function formatDurationSeconds(value?: number) {
  if (value == null || !Number.isFinite(value) || value <= 0) return "";
  const units = [
    { seconds: 86_400, suffix: "d" },
    { seconds: 3_600, suffix: "h" },
    { seconds: 60, suffix: "m" },
  ];
  for (const unit of units) {
    if (value >= unit.seconds) {
      const amount = value / unit.seconds;
      return `${Number.isInteger(amount) ? amount : Number(amount.toFixed(1))}${unit.suffix}`;
    }
  }
  return `${Math.round(value)}s`;
}

function fmtInteger(value: number) {
  if (!Number.isFinite(value)) return "—";
  return Math.round(value).toLocaleString();
}

function fmtCompact(value: number) {
  if (!Number.isFinite(value)) return "—";
  const absolute = Math.abs(value);
  if (absolute >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (absolute >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (absolute >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return Math.round(value).toLocaleString();
}

function fmtUSD(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (value === 0) return "$0.00";
  const absolute = Math.abs(value);
  if (absolute < 0.0001) return value > 0 ? "<$0.0001" : ">-$0.0001";
  if (absolute < 1) return `$${value.toFixed(4)}`;
  return `$${value.toFixed(2)}`;
}

function fmtUSDCompact(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (Math.abs(value) >= 1_000) return `$${(value / 1_000).toFixed(1)}K`;
  if (Math.abs(value) >= 1) return `$${value.toFixed(1)}`;
  return `$${value.toFixed(2)}`;
}

function fmtRate(value: number) {
  if (!Number.isFinite(value)) return "—";
  return `$${value.toLocaleString(undefined, { maximumFractionDigits: 6 })}`;
}

function fmtRatio(value?: number | null) {
	if (value == null || !Number.isFinite(value)) return "—";
  return `${(value * 100).toFixed(1)}%`;
}

function fmtHealthPercent(value?: number) {
  if (value == null || !Number.isFinite(value)) return "—";
  return `${value.toFixed(1)}%`;
}

function fmtMs(value?: number) {
  if (value == null || !Number.isFinite(value) || value <= 0) return "—";
  if (value < 1) return "<1ms";
  if (value < 1_000) return `${Math.round(value)}ms`;
  return `${(value / 1_000).toFixed(2)}s`;
}

function fmtBytes(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (value >= 1_048_576) return `${(value / 1_048_576).toFixed(1)} MB`;
  if (value >= 1_024) return `${(value / 1_024).toFixed(1)} KB`;
  return `${fmtInteger(value)} B`;
}

function formatDateTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString();
}

function relativeTime(value: string) {
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return "—";
  const seconds = Math.max(0, Math.floor((Date.now() - time) / 1_000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function humanize(value?: string) {
  if (!value) return "";
  return value.replace(/[_-]+/g, " ").replace(/\b\w/g, (character) => character.toUpperCase());
}
