import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { AlertTriangle, ArrowRight, Check, ChevronRight, Copy, Plus, RefreshCw } from "lucide-react";
import {
  api,
  connectUsageStream,
  type BudgetStatus,
  type ChainUsage,
  type HealthTimelineProvider,
  type ModelUsage,
  type RecentActivity,
  type UsageInsights,
} from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { TrafficCard } from "../components/charts/TrafficChart";
import { ErrorCard, Skeleton } from "../components/ui";
import { useToast } from "../components/Toast";

// ── Period model ─────────────────────────────────────────────────────────────

type PeriodKey = "24h" | "7d" | "30d" | "90d";

const PERIODS: Record<PeriodKey, { label: string; buckets: number; text: string; vs: string }> = {
  "24h": { label: "24h", buckets: 24, text: "last 24 hours", vs: "compared with the 24 hours before" },
  "7d": { label: "7d", buckets: 42, text: "last 7 days", vs: "compared with the 7 days before" },
  "30d": { label: "30d", buckets: 30, text: "last 30 days", vs: "compared with the 30 days before" },
  "90d": { label: "90d", buckets: 45, text: "last 90 days", vs: "compared with the 90 days before" },
};
const PERIOD_KEYS = Object.keys(PERIODS) as PeriodKey[];
const PERIOD_STORAGE_KEY = "kr.overview.period";

function readStoredPeriod(): PeriodKey {
  try {
    const raw = localStorage.getItem(PERIOD_STORAGE_KEY);
    if (raw && raw in PERIODS) return raw as PeriodKey;
  } catch {
    /* storage unavailable: fall through to the default */
  }
  return "7d";
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function OverviewPage() {
  const [period, setPeriodState] = useState<PeriodKey>(readStoredPeriod);
  const setPeriod = (p: PeriodKey) => {
    setPeriodState(p);
    try {
      localStorage.setItem(PERIOD_STORAGE_KEY, p);
    } catch {
      /* non-essential */
    }
  };
  const cfg = PERIODS[period];
  const qc = useQueryClient();

  const insightsKey = ["usage-insights", period, "overview"] as const;
  const insights = useQuery({
    queryKey: insightsKey,
    queryFn: () => api.usageInsights(period, { limit: 8, buckets: cfg.buckets }),
    staleTime: 15_000,
    placeholderData: (previous) => previous,
  });
  const models = useQuery({
    queryKey: ["model-usage", period],
    queryFn: () => api.modelUsage(period),
    staleTime: 30_000,
    placeholderData: (previous) => previous,
  });
  const timeline = useQuery({
    queryKey: ["health-timeline", "24h"],
    queryFn: () => api.healthTimeline("24h", 24),
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
  const chains = useQuery({
    queryKey: ["chain-usage", period],
    queryFn: () => api.chainUsage(period),
    staleTime: 30_000,
    placeholderData: (previous) => previous,
  });
  const budgets = useQuery({
    queryKey: ["budget-status"],
    queryFn: () => api.budgetStatus(),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });

  // Live: the usage stream is an invalidation signal. Coalesce bursts so a
  // busy gateway refreshes the page at most every few seconds.
  const [live, setLive] = useState(false);
  const lastRefresh = useRef(0);
  useEffect(() => {
    let timer: number | undefined;
    const stop = connectUsageStream(() => {
      setLive(true);
      const wait = Math.max(0, 4_000 - (Date.now() - lastRefresh.current));
      if (timer !== undefined) return;
      timer = window.setTimeout(() => {
        timer = undefined;
        lastRefresh.current = Date.now();
        qc.invalidateQueries({ queryKey: ["usage-insights"] });
        qc.invalidateQueries({ queryKey: ["model-usage"] });
        qc.invalidateQueries({ queryKey: ["chain-usage"] });
      }, wait);
    });
    return () => {
      stop();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [qc]);

  const data = insights.data;
  const firstRun = !!data && data.summary.total_requests === 0 && (data.previous?.total_requests ?? 0) === 0;
  const refreshing = insights.isFetching && !insights.isLoading;

  const refreshAll = () => {
    qc.invalidateQueries({ queryKey: ["usage-insights"] });
    qc.invalidateQueries({ queryKey: ["model-usage"] });
    qc.invalidateQueries({ queryKey: ["health-timeline"] });
    qc.invalidateQueries({ queryKey: ["chain-usage"] });
    qc.invalidateQueries({ queryKey: ["budget-status"] });
  };

  return (
    <>
      <PageHeader
        title={firstRun ? "Welcome to KeiRouter" : "Overview"}
        description={
          firstRun
            ? "Your gateway is running. A few more steps and your tools start routing through it."
            : `Traffic, spend and routing health across every provider · ${cfg.text}, ${cfg.vs}`
        }
        action={
          <>
            <BaseUrlChip />
            {!firstRun && (
              <>
                {live && (
                  <span className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] font-medium text-fg-muted">
                    <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
                    Live
                  </span>
                )}
                <PeriodSelect value={period} onChange={setPeriod} />
                <button
                  type="button"
                  onClick={refreshAll}
                  aria-label="Refresh overview"
                  className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
                >
                  <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />
                </button>
              </>
            )}
            <Link
              to="/providers"
              className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-primary px-3 text-[13px] font-medium text-primary-fg transition-opacity hover:opacity-85 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2"
            >
              <Plus className="h-3.5 w-3.5" strokeWidth={2} />
              Connect provider
            </Link>
          </>
        }
      />

      <BudgetNotice budgets={budgets.data?.budgets ?? []} />

      {insights.isLoading ? (
        <OverviewSkeleton />
      ) : insights.isError || !data ? (
        <ErrorCard message="Couldn't load the overview. Is the backend running?" />
      ) : firstRun ? (
        <FirstRun />
      ) : (
        <div className="space-y-5">
          <KpiStrip data={data} />

          <div className="grid gap-5 xl:grid-cols-3">
            <TrafficCard data={data} className="xl:col-span-2" />
            <ProviderHealthCard providers={timeline.data?.providers} loading={timeline.isLoading} />
          </div>

          <div className="grid gap-5 xl:grid-cols-3">
            <TopModelsCard models={models.data?.models} loading={models.isLoading} periodText={cfg.text} className="xl:col-span-2" />
            <SavingsCard data={data} />
          </div>

          <div className="grid gap-5 xl:grid-cols-3">
            <ChainsCard chains={chains.data?.chains} loading={chains.isLoading} periodText={cfg.text} className="xl:col-span-2" />
            <LimitsCard budgets={budgets.data?.budgets} loading={budgets.isLoading} />
          </div>

          <RecentRequestsCard recent={data.recent} />
        </div>
      )}
    </>
  );
}

// ── Header controls ──────────────────────────────────────────────────────────

function PeriodSelect({ value, onChange }: { value: PeriodKey; onChange: (p: PeriodKey) => void }) {
  return (
    <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Time range">
      {PERIOD_KEYS.map((key) => {
        const active = key === value;
        return (
          <button
            key={key}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(key)}
            className={cn(
              "h-full rounded-lg px-2.5 font-mono text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
              active ? "bg-surface text-fg shadow-[0_0_0_1px_var(--border-strong)]" : "text-fg-muted hover:text-fg",
            )}
          >
            {PERIODS[key].label}
          </button>
        );
      })}
    </div>
  );
}

// The base URL is the single most-copied value in the app. In production the
// dashboard is served by the gateway itself and in development Vite proxies
// /v1, so the page origin is always the correct client-facing endpoint.
function BaseUrlChip() {
  const toast = useToast();
  const url = `${window.location.origin}/v1`;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      toast.success("Base URL copied", url);
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access.");
    }
  };
  return (
    <div className="hidden h-8 items-center overflow-hidden rounded-lg border border-line bg-surface md:inline-flex">
      <span className="flex h-full items-center border-r border-line px-2.5 text-[12px] text-fg-faint">Base URL</span>
      <span className="px-2.5 font-mono text-[12px] text-fg">{url}</span>
      <button
        type="button"
        onClick={copy}
        aria-label="Copy base URL"
        className="flex h-full w-8 items-center justify-center border-l border-line bg-subtle text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500/40"
      >
        <Copy className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

function BudgetNotice({ budgets }: { budgets: BudgetStatus[] }) {
  const over = budgets.filter((b) => usedPct(b) >= 100 && b.hard_cutoff);
  const near = budgets.filter((b) => usedPct(b) >= b.alert_pct && !(usedPct(b) >= 100 && b.hard_cutoff));
  if (over.length === 0 && near.length === 0) return null;
  const blocked = over.length > 0;
  const list = (blocked ? over : near).map((b) => `${b.scope_name} · ${Math.round(usedPct(b))}%`).join("  ·  ");
  return (
    <div
      role="status"
      className={cn(
        "mb-5 flex items-center gap-3 rounded-2xl border px-4 py-2.5 text-[13px]",
        blocked ? "border-bad/30 bg-bad/5" : "border-warn/30 bg-warn/5",
      )}
    >
      <AlertTriangle className={cn("h-4 w-4 shrink-0", blocked ? "text-bad" : "text-warn")} />
      <p className="min-w-0 flex-1 truncate">
        <span className="font-medium text-fg">{blocked ? "Budget limit reached — requests are being blocked." : "Budget alert threshold reached."}</span>{" "}
        <span className="text-fg-muted" title={list}>{list}</span>
      </p>
      <Link to="/plans" className="shrink-0 text-[12px] font-medium text-accent-500 hover:underline">
        Review budgets
      </Link>
    </div>
  );
}

// ── Shared card chrome ───────────────────────────────────────────────────────

function Panel({ className, children, label }: { className?: string; children: ReactNode; label: string }) {
  return (
    <section aria-label={label} className={cn("min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      {children}
    </section>
  );
}

function PanelHeader({ title, subtitle, action }: { title: ReactNode; subtitle?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
      <div className="min-w-0">
        <h2 className="text-[13px] font-semibold tracking-[-0.005em] text-fg">{title}</h2>
        {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
      </div>
      {action}
    </div>
  );
}

function PanelLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className="inline-flex items-center gap-1 text-[12px] font-medium text-accent-500 hover:underline hover:underline-offset-2 dark:text-accent-400">
      {children}
      <ArrowRight className="h-3 w-3" />
    </Link>
  );
}

function PanelEmpty({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-1 px-6 py-10 text-center">
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {hint && <p className="max-w-sm text-[12px] text-fg-muted">{hint}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

// ── KPI strip ────────────────────────────────────────────────────────────────

type DeltaTone = "good" | "bad" | "flat";

function KpiStrip({ data }: { data: UsageInsights }) {
  const s = data.summary;
  const p = data.previous;
  const series = data.series;
  const gross = s.cost_usd + data.savings.usd_saved;

  const successSeries = series.filter((pt) => pt.requests > 0).map((pt) => 1 - pt.failures / pt.requests);
  const items: {
    label: string;
    value: string;
    sub?: string;
    delta: { text: string; tone: DeltaTone } | null;
    spark: number[];
  }[] = [
    {
      label: "Requests",
      value: fmtInt(s.total_requests),
      delta: relDelta(s.total_requests, p?.total_requests, "flat"),
      spark: series.map((pt) => pt.requests),
    },
    {
      label: "Success rate",
      value: s.total_requests ? fmtPct(s.success_rate, 2) : "—",
      delta: p && p.total_requests && s.total_requests ? ptsDelta(s.success_rate, p.success_rate) : null,
      spark: successSeries,
    },
    {
      label: "Latency p50",
      value: fmtMs(s.p50_latency_ms),
      sub: s.p95_latency_ms ? `p95 ${fmtMs(s.p95_latency_ms)}` : undefined,
      delta: relDelta(s.p50_latency_ms, p?.p50_latency_ms, "lower-better"),
      spark: series.filter((pt) => pt.avg_latency_ms > 0).map((pt) => pt.avg_latency_ms),
    },
    {
      label: "Spend",
      value: fmtUSD(s.cost_usd),
      delta: relDelta(s.cost_usd, p?.cost_usd, "flat"),
      spark: series.map((pt) => pt.cost_usd),
    },
    {
      label: "Saved by optimizers",
      value: fmtUSD(data.savings.usd_saved),
      sub: gross > 0 ? `${fmtPct(data.savings.usd_saved / gross, 1)} of gross` : undefined,
      delta: absUSDDelta(data.savings.usd_saved, p?.usd_saved),
      spark: series.map((pt) => pt.saved_usd ?? 0),
    },
  ];

  return (
    // gap-px over a line-coloured ground draws every divider for any column
    // count; the last cell spans the spare column so no empty slot shows.
    <Panel label="Key metrics" className="grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-5">
      {items.map((item) => (
        <div key={item.label} className="flex min-w-0 flex-col gap-1.5 bg-surface px-4 pb-3 pt-3.5 sm:last:col-span-2 xl:last:col-span-1">
          <span className="text-[12px] font-medium text-fg-muted">{item.label}</span>
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="whitespace-nowrap text-[24px] font-semibold leading-none tracking-[-0.02em] tabular-nums text-fg">{item.value}</span>
            {item.sub && <span className="truncate text-[12px] tabular-nums text-fg-muted">{item.sub}</span>}
          </div>
          <div className="flex min-h-6 items-center justify-between gap-2">
            {item.delta ? (
              <span className={cn("whitespace-nowrap text-[12px] font-medium tabular-nums", deltaClass(item.delta.tone))}>{item.delta.text}</span>
            ) : (
              <span className="text-[12px] text-fg-faint">No prior data</span>
            )}
            <Sparkline values={item.spark} />
          </div>
        </div>
      ))}
    </Panel>
  );
}

function Sparkline({ values }: { values: number[] }) {
  if (values.length < 2 || values.every((v) => v === values[0])) return <span className="h-6 w-[72px]" aria-hidden="true" />;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const points = values
    .map((v, i) => `${((i / (values.length - 1)) * 100).toFixed(2)},${(25 - ((v - min) / span) * 21).toFixed(2)}`)
    .join(" ");
  return (
    <svg width="72" height="24" viewBox="0 0 100 28" preserveAspectRatio="none" aria-hidden="true" className="shrink-0">
      <polyline
        points={points}
        fill="none"
        stroke="var(--color-accent-500)"
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}

// ── Provider health ──────────────────────────────────────────────────────────

const TICK_CLASS: Record<string, string> = {
  ok: "bg-ok/70",
  degraded: "bg-warn",
  down: "bg-bad",
  idle: "bg-track",
};

function providerState(p: HealthTimelineProvider): { label: string; tone: "ok" | "warn" | "bad" | "idle" } {
  if (p.requests === 0) return { label: "Idle", tone: "idle" };
  const recent = p.buckets.slice(-3).map((b) => b.status);
  if (recent.includes("down")) return { label: "Down", tone: "bad" };
  if (recent.includes("degraded")) return { label: p.rate_limited > 0 ? "Rate limited" : "Degraded", tone: "warn" };
  return { label: "Operational", tone: "ok" };
}

function ProviderHealthCard({ providers, loading }: { providers?: HealthTimelineProvider[]; loading: boolean }) {
  const rows = (providers ?? []).slice(0, 6);
  return (
    <Panel label="Provider health" className="flex flex-col">
      <PanelHeader title="Provider health" subtitle="Success rate and worst p95 · last 24 hours" action={<PanelLink to="/provider-health">All providers</PanelLink>} />
      {loading ? (
        <div className="space-y-3 p-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-11 w-full" />)}</div>
      ) : rows.length === 0 ? (
        <PanelEmpty title="No provider traffic in the last 24 hours" hint="Health strips appear once requests flow through a provider." />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((p) => {
            const state = providerState(p);
            return (
              <li key={p.provider}>
                <Link to={`/provider-health/${encodeURIComponent(p.provider)}`} className="block px-4 py-2.5 transition-colors hover:bg-hover">
                  <div className="flex items-center gap-2.5">
                    <ProviderLogo icon={p.icon} name={p.display_name} />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-[13px] font-medium text-fg">{p.display_name}</p>
                      <p className="truncate font-mono text-[11.5px] text-fg-faint">{p.provider}</p>
                    </div>
                    <div className="text-right">
                      <p className={cn("inline-flex items-center gap-1.5 text-[12px] font-medium", toneText(state.tone))}>
                        <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />
                        {state.label}
                      </p>
                      <p className="text-[11.5px] tabular-nums text-fg-muted">
                        {p.requests ? fmtPct(p.success_rate, 1) : "—"} · {fmtMs(p.worst_p95_ms)}
                      </p>
                    </div>
                  </div>
                  <div className="mt-2 flex gap-[2px]" role="img" aria-label={`${p.display_name} hourly status, last 24 hours`}>
                    {p.buckets.map((b) => (
                      <span key={b.start} className={cn("h-4 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")} title={`${formatBucket(b.start, false)} · ${b.status}${b.requests ? ` · ${b.requests} req` : ""}`} />
                    ))}
                  </div>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

// ── Top models ───────────────────────────────────────────────────────────────

function TopModelsCard({ models, loading, periodText, className }: { models?: ModelUsage[]; loading: boolean; periodText: string; className?: string }) {
  const rows = (models ?? []).slice(0, 6);
  const total = (models ?? []).reduce((sum, m) => sum + m.total_requests, 0);
  return (
    <Panel label="Top models" className={className}>
      <PanelHeader title="Top models" subtitle={`Ranked by requests · ${periodText}`} action={<PanelLink to="/usage">Open usage</PanelLink>} />
      {loading ? (
        <div className="space-y-2 p-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
      ) : rows.length === 0 ? (
        <PanelEmpty title="No model traffic in this period" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[620px] text-[12.5px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th className="px-4 py-2 font-medium">Model</th>
                <th className="px-4 py-2 font-medium">Provider</th>
                <th className="px-4 py-2 text-right font-medium">Requests</th>
                <th className="px-4 py-2 text-right font-medium">Tokens</th>
                <th className="px-4 py-2 text-right font-medium">Cost</th>
                <th className="w-40 px-4 py-2 font-medium">Share</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((m) => {
                const share = total ? (m.total_requests / total) * 100 : 0;
                return (
                  <tr key={`${m.provider}/${m.model}`} className="transition-colors hover:bg-hover">
                    <td className="max-w-[260px] truncate px-4 py-2 font-mono text-fg" title={m.model}>{m.model || "unknown"}</td>
                    <td className="px-4 py-2">
                      <span className="inline-flex items-center gap-2">
                        <ProviderLogo icon={m.provider_icon} name={m.provider_name || m.provider} size={18} />
                        <span className="font-mono text-[12px] text-fg-muted">{m.provider}</span>
                      </span>
                    </td>
                    <td className="px-4 py-2 text-right text-fg">{fmtInt(m.total_requests)}</td>
                    <td className="px-4 py-2 text-right text-fg-muted">{fmtCompact(m.total_tokens)}</td>
                    <td className="px-4 py-2 text-right text-fg">{m.cost_usd > 0 ? fmtUSD(m.cost_usd) : m.pricing_status === "free" ? "Free" : "—"}</td>
                    <td className="px-4 py-2">
                      <div className="flex items-center gap-2">
                        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-track">
                          <div className="h-full rounded-full bg-accent-500" style={{ width: `${share}%` }} />
                        </div>
                        <span className="w-9 text-right text-[11.5px] tabular-nums text-fg-muted">{share.toFixed(share < 10 ? 1 : 0)}%</span>
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
  );
}

// ── Savings ──────────────────────────────────────────────────────────────────

function SavingsCard({ data }: { data: UsageInsights }) {
  const sv = data.savings;
  const gross = data.summary.cost_usd + sv.usd_saved;
  const compressionTokens = sv.slim_tokens_saved + sv.headroom_tokens_saved;
  const shaped = sv.caveman_requests + sv.terse_requests + sv.ponytail_requests;
  const usdRows = [
    { name: "Semantic cache", detail: `${fmtInt(data.summary.cache_hits)} requests served from cache`, usd: sv.avoided_cost_usd, opacity: 1 },
    { name: "Input compression", detail: `${fmtCompact(compressionTokens)} prompt tokens removed (RTK + Headroom)`, usd: sv.saved_cost_usd, opacity: 0.45 },
  ];
  const usdTotal = usdRows.reduce((sum, r) => sum + r.usd, 0);

  return (
    <Panel label="Token savings" className="flex flex-col">
      <PanelHeader title="Token savings" subtitle="Cache and compression before the bill" action={<PanelLink to="/settings">Configure</PanelLink>} />
      <div className="flex items-baseline gap-2 px-4 pb-1 pt-4">
        <span className="text-[24px] font-semibold leading-none tracking-[-0.02em] tabular-nums text-fg">{fmtUSD(sv.usd_saved)}</span>
        <span className="text-[12px] text-fg-muted">
          saved{gross > 0 ? ` · ${fmtPct(sv.usd_saved / gross, 1)} of gross spend` : ""}
          {sv.usd_saved_estimate ? " · estimated" : ""}
        </span>
      </div>
      {usdTotal > 0 && (
        <div className="mx-4 mt-3 flex h-2 gap-0.5 overflow-hidden rounded-full" role="img" aria-label="Savings split by optimizer">
          {usdRows.map((r) => r.usd > 0 && <span key={r.name} className="bg-accent-500" style={{ width: `${(r.usd / usdTotal) * 100}%`, opacity: r.opacity }} />)}
        </div>
      )}
      <ul className="mt-2 flex-1 pb-2">
        {usdRows.map((r) => (
          <li key={r.name} className="flex items-center gap-2.5 px-4 py-2">
            <span className="h-2 w-2 shrink-0 rounded-[2px] bg-accent-500" style={{ opacity: r.opacity }} aria-hidden="true" />
            <div className="min-w-0 flex-1">
              <p className="text-[13px] font-medium text-fg">{r.name}</p>
              <p className="truncate text-[11.5px] text-fg-faint">{r.detail}</p>
            </div>
            <span className="text-[13px] font-medium tabular-nums text-fg">{fmtUSD(r.usd)}</span>
          </li>
        ))}
        <li className="flex items-center gap-2.5 px-4 py-2">
          <span className="h-2 w-2 shrink-0 rounded-[2px] bg-track" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium text-fg">Output shaping</p>
            <p className="truncate text-[11.5px] text-fg-faint">Caveman, Terse and Ponytail · not priced individually</p>
          </div>
          <span className="text-[13px] tabular-nums text-fg-muted">{fmtInt(shaped)} req</span>
        </li>
      </ul>
    </Panel>
  );
}

// ── Routing chains ───────────────────────────────────────────────────────────

const STEP_OPACITY = [1, 0.6, 0.38, 0.24, 0.16];

function ChainsCard({ chains, loading, periodText, className }: { chains?: ChainUsage[]; loading: boolean; periodText: string; className?: string }) {
  const rows = (chains ?? []).slice(0, 4);
  return (
    <Panel label="Routing chains" className={className}>
      <PanelHeader title="Routing chains" subtitle={`Where each chain's traffic actually landed · ${periodText}`} action={<PanelLink to="/chains">Manage chains</PanelLink>} />
      {loading ? (
        <div className="space-y-3 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-14 w-full" />)}</div>
      ) : rows.length === 0 ? (
        <PanelEmpty
          title="No chains yet"
          hint="A chain tries models in order, so a rate-limited provider never stops your tools."
          action={<Link to="/chains/new" className="text-[12px] font-medium text-accent-500 hover:underline">Create a fallback chain</Link>}
        />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((c) => {
            const rotating = /round|random/.test(c.strategy);
            const sep = rotating ? "·" : "→";
            return (
              <li key={c.chain_id}>
                <Link to={`/chains/${c.chain_id}/edit`} className="flex flex-col gap-2.5 px-4 py-3 transition-colors hover:bg-hover">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-[13px] font-medium text-fg">{c.name}</span>
                    <span className="inline-flex h-5 items-center rounded-md border border-line px-1.5 text-[11.5px] text-fg-muted">{humanStrategy(c.strategy)}</span>
                    <span className="ml-auto text-[12px] tabular-nums text-fg-muted">
                      {c.requests > 0 ? (
                        <>
                          {fmtInt(c.requests)} req ·{" "}
                          <span className={cn(c.fallback_rate >= 0.1 ? "text-warn" : "text-fg")}>{fmtInt(c.fallback_requests)}</span> fell back ({fmtPct(c.fallback_rate, 1)})
                        </>
                      ) : (
                        "No traffic in this period"
                      )}
                    </span>
                  </div>
                  {c.requests > 0 && (
                    <div className="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-track" role="img" aria-label={`${c.name} traffic by step`}>
                      {c.steps.map((st, i) =>
                        st.requests > 0 ? (
                          <span key={`${st.provider}/${st.model}`} className="bg-accent-500" style={{ width: `${st.share * 100}%`, opacity: STEP_OPACITY[Math.min(i, STEP_OPACITY.length - 1)] }} />
                        ) : null,
                      )}
                    </div>
                  )}
                  <div className="flex flex-wrap items-center gap-1.5 text-[12px]">
                    {c.steps.map((st, i) => (
                      <span key={`${st.provider}/${st.model}/${i}`} className="inline-flex items-center gap-1.5">
                        {i > 0 && <span className="text-fg-faint" aria-hidden="true">{st.is_fallback ? "⤳" : sep}</span>}
                        <span
                          className="inline-flex h-6 items-center gap-1.5 rounded-md border border-line bg-subtle px-2"
                          title={`${st.provider}/${st.model}${st.is_fallback ? " · last-resort fallback" : ""}`}
                        >
                          <span className="h-1.5 w-1.5 rounded-[2px] bg-accent-500" style={{ opacity: STEP_OPACITY[Math.min(i, STEP_OPACITY.length - 1)] }} aria-hidden="true" />
                          <span className="font-mono text-fg">{st.model}</span>
                          {c.requests > 0 && <span className="tabular-nums text-fg-faint">{fmtPct(st.share, st.share < 0.1 ? 1 : 0)}</span>}
                        </span>
                      </span>
                    ))}
                  </div>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

function humanStrategy(s: string): string {
  const map: Record<string, string> = { priority: "Priority", fallback: "Fallback", round_robin: "Round robin", "round-robin": "Round robin", random: "Random", latency: "Lowest latency", cost: "Lowest cost" };
  return map[s] ?? (s ? s.charAt(0).toUpperCase() + s.slice(1).replace(/[_-]/g, " ") : "Priority");
}

// ── Budgets / limits ─────────────────────────────────────────────────────────

function usedPct(b: BudgetStatus): number {
  return Math.max(b.limit_micros > 0 ? b.pct_used : 0, b.limit_tokens > 0 ? b.tokens_pct_used : 0);
}

function LimitsCard({ budgets, loading }: { budgets?: BudgetStatus[]; loading: boolean }) {
  const rows = [...(budgets ?? [])].sort((a, b) => usedPct(b) - usedPct(a)).slice(0, 4);
  return (
    <Panel label="Budgets" className="flex flex-col">
      <PanelHeader title="Budgets" subtitle="Closest to their limit first" action={<PanelLink to="/plans">All budgets</PanelLink>} />
      {loading ? (
        <div className="space-y-3 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-10 w-full" />)}</div>
      ) : rows.length === 0 ? (
        <PanelEmpty
          title="No budgets set"
          hint="Cap spend or tokens per key with an automatic cutoff."
          action={<Link to="/plans" className="text-[12px] font-medium text-accent-500 hover:underline">Set a budget</Link>}
        />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((b) => {
            const pct = usedPct(b);
            const fill = pct >= 100 ? "bg-bad" : pct >= b.alert_pct ? "bg-warn" : "bg-accent-500";
            const byTokens = b.limit_tokens > 0 && b.tokens_pct_used >= b.pct_used;
            const detail = byTokens
              ? `${fmtCompact(b.spent_tokens)} of ${fmtCompact(b.limit_tokens)} tokens`
              : `${fmtUSD(b.spent_micros / 1e6)} of ${fmtUSD(b.limit_micros / 1e6)}`;
            return (
              <li key={b.id} className="flex flex-col gap-1.5 px-4 py-3">
                <div className="flex items-baseline gap-2">
                  <span className="truncate font-mono text-[13px] font-medium text-fg">{b.scope_name}</span>
                  <span className={cn("ml-auto text-[12px] font-medium tabular-nums", pct >= 100 ? "text-bad" : pct >= b.alert_pct ? "text-warn" : "text-fg")}>
                    {Math.round(pct)}%
                  </span>
                </div>
                <div className="h-1.5 overflow-hidden rounded-full bg-track">
                  <div className={cn("h-full rounded-full", fill)} style={{ width: `${Math.min(100, pct)}%` }} />
                </div>
                <div className="flex justify-between gap-2 text-[11.5px] tabular-nums text-fg-faint">
                  <span className="truncate">{detail} · {b.period}</span>
                  <span className="shrink-0">{b.resets_at ? `Resets ${relativeFuture(b.resets_at)}` : "No reset"}</span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

// ── Recent requests ──────────────────────────────────────────────────────────

const STATUS_STYLE: Record<string, { label: string; className: string }> = {
  success: { label: "OK", className: "border-line text-fg-muted [&>i]:bg-ok" },
  cache_hit: { label: "Cache hit", className: "border-line text-fg-muted [&>i]:bg-accent-500" },
  failed: { label: "Failed", className: "border-transparent bg-bad/10 text-bad [&>i]:bg-bad" },
  blocked: { label: "Blocked", className: "border-transparent bg-warn/12 text-warn [&>i]:bg-warn" },
  cancelled: { label: "Cancelled", className: "border-line text-fg-faint [&>i]:bg-fg-faint" },
};

function RecentRequestsCard({ recent }: { recent: RecentActivity[] }) {
  return (
    <Panel label="Recent requests">
      <PanelHeader title="Recent requests" subtitle="Newest first · refreshes as traffic arrives" action={<PanelLink to="/usage">Open usage</PanelLink>} />
      {recent.length === 0 ? (
        <PanelEmpty title="No requests in this period" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[920px] text-[12.5px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th className="px-4 py-2 font-medium">Time</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">Model</th>
                <th className="px-4 py-2 font-medium">Key</th>
                <th className="px-4 py-2 font-medium">Client</th>
                <th className="px-4 py-2 text-right font-medium">Latency</th>
                <th className="px-4 py-2 text-right font-medium">Tokens in → out</th>
                <th className="px-4 py-2 text-right font-medium">Cost</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {recent.map((r) => {
                const st = STATUS_STYLE[r.status] ?? STATUS_STYLE.cancelled;
                return (
                  <tr key={r.id} className="transition-colors hover:bg-hover">
                    <td className="whitespace-nowrap px-4 py-2 font-mono text-[12px] text-fg-muted" title={new Date(r.created_at).toLocaleString()}>
                      {formatClock(r.created_at)}
                    </td>
                    <td className="px-4 py-2">
                      <span className={cn("inline-flex h-5 items-center gap-1.5 rounded-md border px-1.5 text-[11.5px] font-medium", st.className)} title={r.error_kind || undefined}>
                        <i className="h-1.5 w-1.5 rounded-full" aria-hidden="true" />
                        {r.status === "failed" && r.error_kind ? humanError(r.error_kind) : st.label}
                      </span>
                    </td>
                    <td className="px-4 py-2">
                      <span className="inline-flex max-w-[300px] items-center gap-2">
                        <ProviderLogo icon={r.provider_icon} name={r.provider_name || r.provider} size={18} />
                        <span className="truncate font-mono text-fg" title={`${r.provider}/${r.model}`}>{r.model || "unknown"}</span>
                      </span>
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 font-mono text-[12px] text-fg">{r.api_key_name || <span className="text-fg-faint">—</span>}</td>
                    <td className="whitespace-nowrap px-4 py-2 text-[12px] text-fg-muted">{r.client || "—"}</td>
                    <td className="whitespace-nowrap px-4 py-2 text-right text-fg">{fmtMs(r.latency_ms)}</td>
                    <td className="whitespace-nowrap px-4 py-2 text-right text-fg-muted">
                      {r.prompt_tokens || r.completion_tokens ? `${fmtCompact(r.prompt_tokens)} → ${fmtCompact(r.completion_tokens)}` : "—"}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 text-right text-fg">{r.cache_hit ? "$0" : r.cost_usd > 0 ? fmtUSD(r.cost_usd) : "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

function humanError(kind: string): string {
  const map: Record<string, string> = {
    rate_limited: "Rate limited",
    timeout: "Timeout",
    auth: "Auth error",
    auth_error: "Auth error",
    quota_exceeded: "Quota exceeded",
    provider_5xx: "Upstream 5xx",
    bad_request: "Bad request",
    network: "Network error",
    network_error: "Network error",
  };
  return map[kind] ?? kind.replace(/_/g, " ");
}

// ── First run ────────────────────────────────────────────────────────────────

type SnippetKey = "claude" | "openai" | "curl";

function FirstRun() {
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts(), staleTime: 30_000 });
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys(), staleTime: 30_000 });
  const info = useQuery({ queryKey: ["gateway-info"], queryFn: () => api.gatewayInfo(), staleTime: 60_000 });
  const [snippet, setSnippet] = useState<SnippetKey>("claude");
  const toast = useToast();

  const accountCount = accounts.data?.accounts.length ?? 0;
  const keyList = keys.data?.keys ?? [];
  const keyUsed = keyList.some((k) => !!k.last_used_at);
  const steps = [accountCount > 0, keyList.length > 0, keyUsed, false];
  const done = steps.filter(Boolean).length;
  const current = steps.findIndex((s) => !s);

  const origin = window.location.origin;
  const snippets: Record<SnippetKey, { label: string; code: string }> = {
    claude: {
      label: "Claude Code",
      code: `export ANTHROPIC_BASE_URL=${origin}\nexport ANTHROPIC_AUTH_TOKEN=<your KeiRouter key>\nclaude`,
    },
    openai: {
      label: "OpenAI SDK",
      code: `from openai import OpenAI\n\nclient = OpenAI(base_url="${origin}/v1", api_key="<your KeiRouter key>")\nclient.chat.completions.create(model="<model or chain>", messages=[{"role": "user", "content": "ping"}])`,
    },
    curl: {
      label: "curl",
      code: `curl ${origin}/v1/chat/completions \\\n  -H "Authorization: Bearer <your KeiRouter key>" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model": "<model or chain>", "messages": [{"role": "user", "content": "ping"}]}'`,
    },
  };

  const copySnippet = async () => {
    try {
      await navigator.clipboard.writeText(snippets[snippet].code);
      toast.success("Snippet copied");
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access.");
    }
  };

  return (
    <div className="space-y-5">
      <KpiPlaceholder />
      <div className="grid gap-5 xl:grid-cols-3">
        <Panel label="Setup" className="xl:col-span-2">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
            <div>
              <h2 className="text-[13px] font-semibold text-fg">Finish setting up</h2>
              <p className="mt-0.5 text-[12px] text-fg-muted">{done} of 4 complete</p>
            </div>
            <div className="h-1.5 w-40 overflow-hidden rounded-full bg-track" role="progressbar" aria-valuenow={done * 25} aria-valuemin={0} aria-valuemax={100} aria-label="Setup progress">
              <div className="h-full bg-accent-500 transition-[width]" style={{ width: `${done * 25}%` }} />
            </div>
          </div>
          <ol className="divide-y divide-line">
            <SetupStep
              index={0}
              current={current}
              done={steps[0]}
              title="Connect a provider"
              body={steps[0] ? `${accountCount} account${accountCount === 1 ? "" : "s"} connected.` : "Add an API key or sign in with OAuth to any of 90+ providers."}
              action={<SetupAction to="/providers" primary={current === 0}>{steps[0] ? "Manage" : "Connect provider"}</SetupAction>}
            />
            <SetupStep
              index={1}
              current={current}
              done={steps[1]}
              title="Create an API key"
              body="Each key carries its own budget, rate limit and model access. Your tools use it instead of provider credentials."
              action={<SetupAction to="/keys" primary={current === 1}>{steps[1] ? "Manage keys" : "Create key"}</SetupAction>}
            />
            <SetupStep
              index={2}
              current={current}
              done={steps[2]}
              title="Point a tool at KeiRouter"
              body="Paste a snippet, or let KeiRouter write the config for Claude Code, Codex, Cursor and others."
              action={<SetupAction to="/cli-tools" primary={current === 2}>Auto-configure CLI tools</SetupAction>}
            >
              <div className="mt-3 overflow-hidden rounded-xl border border-line">
                <div className="flex items-center gap-1 border-b border-line bg-subtle p-1.5" role="tablist" aria-label="Snippet">
                  {(Object.keys(snippets) as SnippetKey[]).map((k) => (
                    <button
                      key={k}
                      type="button"
                      role="tab"
                      aria-selected={snippet === k}
                      onClick={() => setSnippet(k)}
                      className={cn(
                        "h-7 rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                        snippet === k ? "bg-surface text-fg shadow-[0_0_0_1px_var(--border-strong)]" : "text-fg-muted hover:text-fg",
                      )}
                    >
                      {snippets[k].label}
                    </button>
                  ))}
                  <button
                    type="button"
                    onClick={copySnippet}
                    aria-label="Copy snippet"
                    className="ml-auto flex h-7 w-7 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </button>
                </div>
                <pre className="overflow-x-auto bg-canvas px-3.5 py-3 font-mono text-[12px] leading-relaxed text-fg">{snippets[snippet].code}</pre>
              </div>
            </SetupStep>
            <SetupStep
              index={3}
              current={current}
              done={false}
              title="Send your first request"
              body="This page fills in the moment traffic arrives — no refresh needed."
              action={
                <span className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] text-fg-muted">
                  <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
                  Listening on <span className="font-mono">/v1</span>
                </span>
              }
            />
          </ol>
        </Panel>

        <div className="flex min-w-0 flex-col gap-5">
          <Panel label="Gateway">
            <PanelHeader
              title="Gateway"
              subtitle="This instance"
              action={
                <span className="inline-flex h-5 items-center gap-1.5 rounded-md border border-line px-1.5 text-[11.5px] font-medium text-fg-muted">
                  <span className="h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
                  Running
                </span>
              }
            />
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2.5 px-4 py-3.5 text-[12.5px]">
              <dt className="text-fg-muted">Endpoint</dt>
              <dd className="truncate text-right font-mono text-fg">{origin}/v1</dd>
              <dt className="text-fg-muted">Listening on</dt>
              <dd className="text-right font-mono text-fg">{info.data?.listen_addr ?? "—"}</dd>
              <dt className="text-fg-muted">Storage</dt>
              <dd className="text-right text-fg">{info.data ? (info.data.dialect === "postgres" ? "Postgres" : "SQLite") : "—"}</dd>
              <dt className="text-fg-muted">Version</dt>
              <dd className="text-right font-mono text-fg">{info.data?.version ?? "—"}</dd>
              <dt className="text-fg-muted">Remote access</dt>
              <dd className="text-right">
                <Link to="/endpoints" className="text-[12px] font-medium text-accent-500 hover:underline">Cloudflare or Tailscale</Link>
              </dd>
            </dl>
          </Panel>
          <Panel label="Make it reliable">
            <PanelHeader title="Make it reliable" subtitle="Worth doing before you depend on it" />
            <ul className="divide-y divide-line">
              {[
                { to: "/chains/new", title: "Create a fallback chain", body: "Keep working when a provider rate-limits." },
                { to: "/settings", title: "Turn on token savings", body: "Cache and compress prompts before they cost you." },
                { to: "/plans", title: "Set a budget", body: "Cap spend per key with an automatic cutoff." },
              ].map((item) => (
                <li key={item.to}>
                  <Link to={item.to} className="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-hover">
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium text-fg">{item.title}</p>
                      <p className="text-[12px] text-fg-muted">{item.body}</p>
                    </div>
                    <ChevronRight className="h-4 w-4 shrink-0 text-fg-faint" />
                  </Link>
                </li>
              ))}
            </ul>
          </Panel>
        </div>
      </div>
    </div>
  );
}

function SetupStep({
  index,
  current,
  done,
  title,
  body,
  action,
  children,
}: {
  index: number;
  current: number;
  done: boolean;
  title: string;
  body: string;
  action?: ReactNode;
  children?: ReactNode;
}) {
  const isCurrent = index === current;
  return (
    <li className={cn("flex gap-3.5 px-4 py-4", isCurrent && "bg-subtle")}>
      <span
        aria-hidden="true"
        className={cn(
          "flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[12px] font-semibold tabular-nums",
          done ? "bg-ok text-white" : isCurrent ? "border-[1.5px] border-accent-500 text-fg" : "border-[1.5px] border-line-strong text-fg-muted",
        )}
      >
        {done ? <Check className="h-3.5 w-3.5" strokeWidth={2.5} /> : index + 1}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-[220px] flex-1">
            <p className={cn("text-[13px]", done ? "text-fg-muted line-through decoration-line-strong" : isCurrent ? "font-semibold text-fg" : "font-medium text-fg")}>
              {title}
              <span className="sr-only">{done ? " (done)" : isCurrent ? " (current step)" : ""}</span>
            </p>
            <p className="mt-0.5 max-w-xl text-[12px] text-fg-muted">{body}</p>
          </div>
          {action}
        </div>
        {children}
      </div>
    </li>
  );
}

function SetupAction({ to, primary, children }: { to: string; primary?: boolean; children: ReactNode }) {
  return (
    <Link
      to={to}
      className={cn(
        "inline-flex h-8 shrink-0 items-center rounded-lg px-3 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
        primary ? "bg-primary text-primary-fg hover:opacity-85" : "border border-line-strong bg-surface text-fg hover:bg-hover",
      )}
    >
      {children}
    </Link>
  );
}

function KpiPlaceholder() {
  return (
    <Panel label="Key metrics" className="grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-5">
      {["Requests", "Success rate", "Latency p50", "Spend", "Saved by optimizers"].map((label) => (
        <div key={label} className="flex flex-col gap-1.5 bg-surface px-4 py-3.5 sm:last:col-span-2 xl:last:col-span-1">
          <span className="text-[12px] font-medium text-fg-muted">{label}</span>
          <span className="text-[24px] font-semibold leading-none text-fg-faint">—</span>
          <span className="text-[12px] text-fg-faint">Appears after the first request</span>
        </div>
      ))}
    </Panel>
  );
}

// ── Skeleton ─────────────────────────────────────────────────────────────────

function OverviewSkeleton() {
  return (
    <div className="space-y-5" aria-busy="true" aria-label="Loading overview">
      <Skeleton className="h-[104px] w-full rounded-2xl" />
      <div className="grid gap-5 xl:grid-cols-3">
        <Skeleton className="h-[340px] rounded-2xl xl:col-span-2" />
        <Skeleton className="h-[340px] rounded-2xl" />
      </div>
      <div className="grid gap-5 xl:grid-cols-3">
        <Skeleton className="h-[280px] rounded-2xl xl:col-span-2" />
        <Skeleton className="h-[280px] rounded-2xl" />
      </div>
    </div>
  );
}

// ── Formatting & maths ───────────────────────────────────────────────────────

function fmtInt(v: number): string {
  return Math.round(v || 0).toLocaleString("en-US");
}

function fmtCompact(v: number): string {
  const n = v || 0;
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(n >= 1e4 ? 0 : 1)}k`;
  return Math.round(n).toString();
}

// Two decimals for anything a person would read as money; more precision only
// for sub-cent values, so a quiet day shows "$0.42", not "$0.4213".
function fmtUSD(v: number): string {
  const n = v || 0;
  if (n === 0) return "$0.00";
  if (n < 0.01) return `$${n.toPrecision(2)}`;
  return `$${n.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function fmtMs(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "—";
  if (v < 1000) return `${Math.round(v)} ms`;
  return `${(v / 1000).toFixed(v < 10_000 ? 2 : 1)} s`;
}

function fmtPct(ratio: number, digits: number): string {
  return `${(ratio * 100).toFixed(digits)}%`;
}

function relDelta(cur: number, prev: number | undefined, mode: "flat" | "lower-better" | "higher-better"): { text: string; tone: DeltaTone } | null {
  if (!prev || !Number.isFinite(prev) || prev <= 0 || !cur) return null;
  const change = (cur - prev) / prev;
  const text = `${change >= 0 ? "+" : "−"}${Math.abs(change * 100).toFixed(1)}%`;
  if (Math.abs(change) < 0.005 || mode === "flat") return { text, tone: "flat" };
  const better = mode === "lower-better" ? change < 0 : change > 0;
  return { text, tone: better ? "good" : "bad" };
}

function ptsDelta(cur: number, prev: number): { text: string; tone: DeltaTone } {
  const diff = (cur - prev) * 100;
  const text = `${diff >= 0 ? "+" : "−"}${Math.abs(diff).toFixed(2)} pts`;
  if (Math.abs(diff) < 0.01) return { text, tone: "flat" };
  return { text, tone: diff > 0 ? "good" : "bad" };
}

function absUSDDelta(cur: number, prev: number | undefined): { text: string; tone: DeltaTone } | null {
  if (prev === undefined || (!prev && !cur)) return null;
  const diff = cur - prev;
  const text = `${diff >= 0 ? "+" : "−"}${fmtUSD(Math.abs(diff))}`;
  return { text, tone: Math.abs(diff) < 0.005 ? "flat" : diff > 0 ? "good" : "bad" };
}

function deltaClass(tone: DeltaTone): string {
  return tone === "good" ? "text-ok" : tone === "bad" ? "text-bad" : "text-fg-muted";
}

function toneText(tone: "ok" | "warn" | "bad" | "idle"): string {
  return tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : tone === "bad" ? "text-bad" : "text-fg-faint";
}


function formatBucket(iso: string, withDate: boolean): string {
  const d = new Date(iso);
  return withDate
    ? d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
    : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

function formatClock(iso: string): string {
  const d = new Date(iso);
  const sameDay = d.toDateString() === new Date().toDateString();
  return sameDay
    ? d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })
    : d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });
}

function relativeFuture(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "now";
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  if (h >= 48) return `in ${Math.round(h / 24)}d`;
  if (h > 0) return `in ${h}h ${m}m`;
  return `in ${Math.max(1, m)}m`;
}
