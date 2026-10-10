import { useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { AlertTriangle, ArrowRight, Check, Copy, Plus, RefreshCw } from "lucide-react";
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
  "24h": { label: "24h", buckets: 24, text: "Last 24 hours", vs: "compared with the 24 hours before" },
  "7d": { label: "7d", buckets: 42, text: "Last 7 days", vs: "compared with the 7 days before" },
  "30d": { label: "30d", buckets: 30, text: "Last 30 days", vs: "compared with the 30 days before" },
  "90d": { label: "90d", buckets: 45, text: "Last 90 days", vs: "compared with the 90 days before" },
};
const PERIOD_KEYS = Object.keys(PERIODS) as PeriodKey[];
const PERIOD_STORAGE_KEY = "kr.overview.period";

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

function readStoredPeriod(): PeriodKey {
  try {
    const raw = localStorage.getItem(PERIOD_STORAGE_KEY);
    if (raw && raw in PERIODS) return raw as PeriodKey;
  } catch {
    /* storage unavailable: fall through to the default */
  }
  return "7d";
}

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
        description={firstRun ? "Finish setup to start routing your tools." : `${cfg.text}, ${cfg.vs}`}
        action={
          <>
            <BaseUrlChip />
            {!firstRun && (
              <>
                {live && (
                  <span
                    role="status"
                    title="Updates as traffic arrives"
                    className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] font-medium text-fg-muted"
                  >
                    <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
                    Live
                  </span>
                )}
                <PeriodSelect value={period} onChange={setPeriod} />
                <button
                  type="button"
                  onClick={refreshAll}
                  aria-label="Refresh overview"
                  aria-busy={refreshing}
                  className={cn(
                    "inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg",
                    FOCUS_RING,
                  )}
                >
                  <RefreshCw className={cn("h-4 w-4", refreshing && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
                </button>
                <Link
                  to="/providers"
                  className={cn(
                    "inline-flex h-8 items-center gap-1.5 rounded-lg bg-primary px-3 text-[13px] font-medium text-primary-fg transition-opacity hover:opacity-85 focus-visible:ring-offset-2",
                    FOCUS_RING,
                  )}
                >
                  <Plus className="h-4 w-4" strokeWidth={2} aria-hidden="true" />
                  Connect provider
                </Link>
              </>
            )}
          </>
        }
      />

      <BudgetNotice budgets={budgets.data?.budgets ?? []} />

      {insights.isLoading ? (
        <OverviewSkeleton />
      ) : insights.isError || !data ? (
        <ErrorCard message="Couldn't load the overview. Check that the gateway is running, then refresh." />
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
            <TopModelsCard models={models.data?.models} loading={models.isLoading} className="xl:col-span-2" />
            <CostCard data={data} budgets={budgets.data?.budgets} budgetsLoading={budgets.isLoading} />
          </div>

          <div className="grid gap-5 xl:grid-cols-3">
            <RecentRequestsCard recent={data.recent} className="xl:col-span-2" />
            <ChainsCard chains={chains.data?.chains} loading={chains.isLoading} />
          </div>
        </div>
      )}
    </>
  );
}

// ── Header controls ──────────────────────────────────────────────────────────

function PeriodSelect({ value, onChange }: { value: PeriodKey; onChange: (p: PeriodKey) => void }) {
  return (
    <div
      className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5"
      role="radiogroup"
      aria-label="Time range"
      onKeyDown={(e) => onGroupArrowKeys(e, "radio")}
    >
      {PERIOD_KEYS.map((key) => {
        const active = key === value;
        return (
          <button
            key={key}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(key)}
            className={cn(
              "h-full min-w-8 rounded-lg px-2.5 font-mono text-[12px] font-medium transition-colors",
              FOCUS_RING,
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
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
      toast.error("Couldn't copy", "Your browser blocked clipboard access. Select the URL and copy it manually.");
    }
  };
  return (
    <div className="hidden h-8 items-center overflow-hidden rounded-lg border border-line bg-surface md:inline-flex">
      <span className="flex h-full items-center border-r border-line px-2.5 text-[12px] text-fg-muted">Base URL</span>
      <span className="px-2.5 font-mono text-[12px] text-fg">{url}</span>
      <button
        type="button"
        onClick={copy}
        aria-label="Copy base URL"
        className="flex h-full w-8 items-center justify-center border-l border-line bg-subtle text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      >
        <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
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
      <AlertTriangle className={cn("h-4 w-4 shrink-0", blocked ? "text-bad" : "text-warn")} strokeWidth={1.75} aria-hidden="true" />
      <p className="min-w-0 flex-1 truncate">
        <span className="font-medium text-fg">{blocked ? "Budget limit reached, requests are blocked." : "Budget alert threshold reached."}</span>{" "}
        <span className="text-fg-muted" title={list}>{list}</span>
      </p>
      <Link to="/plans" className={cn("inline-flex min-h-6 shrink-0 items-center rounded-md text-[12px] font-medium text-link hover:underline", FOCUS_RING)}>
        Review budgets
      </Link>
    </div>
  );
}

// ── Shared card chrome ───────────────────────────────────────────────────────

function Panel({
  title,
  meta,
  action,
  className,
  children,
}: {
  title: string;
  meta?: ReactNode;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={cn("flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 border-b border-line px-4 py-3">
        <div className="flex min-w-0 items-baseline gap-2">
          <h2 id={id} className="text-[13px] font-semibold text-fg">{title}</h2>
          {meta && <span className="text-[12px] text-fg-muted">{meta}</span>}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function PanelLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className={cn("inline-flex min-h-6 items-center gap-1 rounded-md text-[12px] font-medium text-link hover:underline hover:underline-offset-2", FOCUS_RING)}>
      {children}
      <ArrowRight className="h-3 w-3" aria-hidden="true" />
    </Link>
  );
}

function PanelEmpty({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-1 px-6 py-10 text-center">
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {hint && <p className="max-w-sm text-[12px] text-fg-muted">{hint}</p>}
      {action && <div className="mt-3">{action}</div>}
    </div>
  );
}

const TH = "px-4 py-2 font-medium";
const THEAD = "border-b border-line bg-subtle text-left text-[12px] text-fg-faint";

// ── KPI strip ────────────────────────────────────────────────────────────────

type DeltaTone = "good" | "bad" | "flat";

function DeltaText({ delta, className }: { delta: { text: string; tone: DeltaTone }; className?: string }) {
  return (
    <span className={cn("whitespace-nowrap text-[12px] font-medium tabular-nums", deltaClass(delta.tone), className)}>
      {delta.text}
      <span className="sr-only">
        {" "}
        vs previous period{delta.tone === "good" ? ", better" : delta.tone === "bad" ? ", worse" : ""}
      </span>
    </span>
  );
}

function KpiStrip({ data }: { data: UsageInsights }) {
  const s = data.summary;
  const p = data.previous;
  const series = data.series;
  const headingId = useId();

  const successSeries = series.filter((pt) => pt.requests > 0).map((pt) => 1 - pt.failures / pt.requests);
  // Four numbers answer "is it healthy and what does it cost". Optimizer
  // savings live in the Savings and budgets card (and as the Spend hint).
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
      sub: data.savings.usd_saved > 0 ? `${fmtUSD(data.savings.usd_saved)} saved` : undefined,
      delta: relDelta(s.cost_usd, p?.cost_usd, "flat"),
      spark: series.map((pt) => pt.cost_usd),
    },
  ];

  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-line shadow-[var(--shadow-card)]">
      <h2 id={headingId} className="sr-only">Key metrics</h2>
      {/* gap-px over a line-coloured ground draws every divider. */}
      <dl className="grid grid-cols-1 gap-px sm:grid-cols-2 xl:grid-cols-4">
        {items.map((item) => (
          <div key={item.label} className="flex min-w-0 flex-col gap-1.5 bg-surface px-4 pb-3 pt-3.5">
            <dt className="text-[12px] font-medium text-fg-muted">{item.label}</dt>
            <dd className="flex min-w-0 items-baseline gap-2">
              <span className="whitespace-nowrap text-[24px] font-semibold leading-none tracking-[-0.02em] tabular-nums text-fg">{item.value}</span>
              {item.sub && <span className="truncate text-[12px] tabular-nums text-fg-muted">{item.sub}</span>}
            </dd>
            <dd className="flex min-h-6 items-center justify-between gap-2">
              {item.delta ? <DeltaText delta={item.delta} /> : <span className="text-[12px] text-fg-muted">No prior data</span>}
              <Sparkline values={item.spark} />
            </dd>
          </div>
        ))}
      </dl>
    </section>
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
        className="stroke-accent-500"
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
    <Panel title="Provider health" meta="24h" action={<PanelLink to="/provider-health">All providers</PanelLink>}>
      {loading ? (
        <div className="space-y-3 p-4" aria-busy="true">
          <span className="sr-only">Loading provider health</span>
          {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-11 w-full" />)}
        </div>
      ) : rows.length === 0 ? (
        <PanelEmpty title="No provider traffic in the last 24 hours" />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((p) => {
            const state = providerState(p);
            return (
              <li key={p.provider}>
                <Link
                  to={`/provider-health/${encodeURIComponent(p.provider)}`}
                  className="block px-4 py-2.5 transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
                >
                  <div className="flex items-center gap-2.5">
                    <ProviderLogo icon={p.icon} name={p.display_name} />
                    <p className="min-w-0 flex-1 truncate text-[13px] font-medium text-fg">{p.display_name}</p>
                    <div className="text-right">
                      <p className={cn("inline-flex items-center gap-1.5 text-[12px] font-medium", toneText(state.tone))}>
                        <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />
                        {state.label}
                      </p>
                      <p className="text-[11.5px] tabular-nums text-fg-muted">
                        {p.requests ? `${fmtPct(p.success_rate, 1)} ok` : "—"} · p95 {fmtMs(p.worst_p95_ms)}
                      </p>
                    </div>
                  </div>
                  {/* Hourly strip is a visual summary; the status label above carries the meaning. */}
                  <div className="mt-2 flex gap-[2px]" aria-hidden="true">
                    {p.buckets.map((b) => (
                      <span
                        key={b.start}
                        className={cn("h-4 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")}
                        title={`${formatBucket(b.start, false)} · ${b.status}${b.requests ? ` · ${b.requests} req` : ""}`}
                      />
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

function TopModelsCard({ models, loading, className }: { models?: ModelUsage[]; loading: boolean; className?: string }) {
  const rows = (models ?? []).slice(0, 6);
  const total = (models ?? []).reduce((sum, m) => sum + m.total_requests, 0);
  return (
    <Panel title="Top models" action={<PanelLink to="/usage?tab=models">All models</PanelLink>} className={className}>
      {loading ? (
        <div className="space-y-2 p-4" aria-busy="true">
          <span className="sr-only">Loading models</span>
          {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-8 w-full" />)}
        </div>
      ) : rows.length === 0 ? (
        <PanelEmpty title="No model traffic in this period" />
      ) : (
        <div className="overflow-x-auto focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500" tabIndex={0} role="region" aria-label="Top models table">
          <table className="w-full min-w-[560px] text-[12.5px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Model</th>
                <th scope="col" className={cn(TH, "text-right")}>Requests</th>
                <th scope="col" className={cn(TH, "text-right")}>Tokens</th>
                <th scope="col" className={cn(TH, "text-right")}>Cost</th>
                <th scope="col" className={cn(TH, "w-40")}>Share</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((m) => {
                const share = total ? (m.total_requests / total) * 100 : 0;
                return (
                  <tr key={`${m.provider}/${m.model}`} className="transition-colors hover:bg-hover">
                    <td className="max-w-[280px] px-4 py-2">
                      <span className="flex min-w-0 items-center gap-2.5">
                        <ProviderLogo icon={m.provider_icon} name={m.provider_name || m.provider} size={18} />
                        <span className="min-w-0">
                          <span className="block truncate font-mono text-fg" title={m.model}>{m.model || "unknown"}</span>
                          <span className="block truncate font-mono text-[11.5px] text-fg-muted">{m.provider}</span>
                        </span>
                      </span>
                    </td>
                    <td className="px-4 py-2 text-right text-fg">{fmtInt(m.total_requests)}</td>
                    <td className="px-4 py-2 text-right text-fg-muted">{fmtCompact(m.total_tokens)}</td>
                    <td className="px-4 py-2 text-right text-fg">{m.cost_usd > 0 ? fmtUSD(m.cost_usd) : m.pricing_status === "free" ? "Free" : "—"}</td>
                    <td className="px-4 py-2">
                      <div className="flex items-center gap-2">
                        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-track" aria-hidden="true">
                          <div className="h-full rounded-full bg-accent-500" style={{ width: `${share}%` }} />
                        </div>
                        <span className="w-10 text-right text-[11.5px] tabular-nums text-fg-muted">{share.toFixed(share < 10 ? 1 : 0)}%</span>
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

// ── Savings and budgets ──────────────────────────────────────────────────────

function CostCard({ data, budgets, budgetsLoading }: { data: UsageInsights; budgets?: BudgetStatus[]; budgetsLoading: boolean }) {
  const sv = data.savings;
  const gross = data.summary.cost_usd + sv.usd_saved;
  const compressionTokens = sv.slim_tokens_saved + sv.headroom_tokens_saved;
  const shaped = sv.caveman_requests + sv.terse_requests + sv.ponytail_requests;
  const usdRows = [
    { name: "Semantic cache", detail: `${fmtInt(data.summary.cache_hits)} cache hits`, usd: sv.avoided_cost_usd, opacity: 1 },
    { name: "Input compression", detail: `${fmtCompact(compressionTokens)} tokens removed`, usd: sv.saved_cost_usd, opacity: 0.45 },
  ];
  const usdTotal = usdRows.reduce((sum, r) => sum + r.usd, 0);
  const savedDelta = absUSDDelta(sv.usd_saved, data.previous?.usd_saved);
  const budgetRows = [...(budgets ?? [])].sort((a, b) => usedPct(b) - usedPct(a)).slice(0, 3);
  const savingsId = useId();
  const budgetsId = useId();

  return (
    <Panel title="Savings and budgets">
      <section aria-labelledby={savingsId} className="px-4 pb-2 pt-3.5">
        <div className="flex items-center justify-between gap-3">
          <h3 id={savingsId} className="text-[12.5px] font-medium text-fg-muted">Saved by optimizers</h3>
          <PanelLink to="/settings">
            Configure<span className="sr-only"> token savings</span>
          </PanelLink>
        </div>
        <p className="mt-1.5 flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="text-[22px] font-semibold leading-none tracking-[-0.02em] tabular-nums text-fg">{fmtUSD(sv.usd_saved)}</span>
          {gross > 0 && <span className="text-[12px] tabular-nums text-fg-muted">{fmtPct(sv.usd_saved / gross, 1)} of gross</span>}
          {sv.usd_saved_estimate && <span className="text-[12px] text-fg-muted">· estimated</span>}
          {savedDelta && <DeltaText delta={savedDelta} className="ml-auto" />}
        </p>
        {usdTotal > 0 && (
          <div className="mt-3 flex h-2 gap-0.5 overflow-hidden rounded-full" aria-hidden="true">
            {usdRows.map((r) => r.usd > 0 && <span key={r.name} className="bg-accent-500" style={{ width: `${(r.usd / usdTotal) * 100}%`, opacity: r.opacity }} />)}
          </div>
        )}
        <ul className="mt-1.5">
          {usdRows.map((r) => (
            <li key={r.name} className="flex items-center gap-2.5 py-1.5">
              <span className="h-2 w-2 shrink-0 rounded-[2px] bg-accent-500" style={{ opacity: r.opacity }} aria-hidden="true" />
              <p className="min-w-0 flex-1 truncate text-[12.5px]">
                <span className="font-medium text-fg">{r.name}</span> <span className="text-fg-muted">· {r.detail}</span>
              </p>
              <span className="text-[12.5px] font-medium tabular-nums text-fg">{fmtUSD(r.usd)}</span>
            </li>
          ))}
          <li className="flex items-center gap-2.5 py-1.5">
            <span className="h-2 w-2 shrink-0 rounded-[2px] bg-track" aria-hidden="true" />
            <p className="min-w-0 flex-1 truncate text-[12.5px]">
              <span className="font-medium text-fg">Output shaping</span> <span className="text-fg-muted">· not priced</span>
            </p>
            <span className="text-[12.5px] tabular-nums text-fg-muted">{fmtInt(shaped)} req</span>
          </li>
        </ul>
      </section>

      <section aria-labelledby={budgetsId} className="flex-1 border-t border-line px-4 pb-3 pt-3">
        <div className="flex items-center justify-between gap-3">
          <h3 id={budgetsId} className="text-[12.5px] font-medium text-fg-muted">Budgets</h3>
          <PanelLink to="/plans">
            {budgetRows.length > 0 ? "Manage" : "Set a budget"}
            {budgetRows.length > 0 && <span className="sr-only"> budgets</span>}
          </PanelLink>
        </div>
        {budgetsLoading ? (
          <div className="mt-2 space-y-2" aria-busy="true">
            <span className="sr-only">Loading budgets</span>
            {[0, 1].map((i) => <Skeleton key={i} className="h-9 w-full" />)}
          </div>
        ) : budgetRows.length === 0 ? (
          <p className="mt-1.5 text-[12.5px] text-fg-muted">No budgets set.</p>
        ) : (
          <ul className="mt-1 divide-y divide-line">
            {budgetRows.map((b) => {
              const pct = usedPct(b);
              const level = pct >= 100 ? "over" : pct >= b.alert_pct ? "near" : "ok";
              const fill = level === "over" ? "bg-bad" : level === "near" ? "bg-warn" : "bg-accent-500";
              const byTokens = b.limit_tokens > 0 && b.tokens_pct_used >= b.pct_used;
              const detail = byTokens
                ? `${fmtCompact(b.spent_tokens)} of ${fmtCompact(b.limit_tokens)} tokens`
                : `${fmtUSD(b.spent_micros / 1e6)} of ${fmtUSD(b.limit_micros / 1e6)}`;
              return (
                <li key={b.id} className="flex flex-col gap-1.5 py-2.5">
                  <div className="flex items-baseline gap-2">
                    <span className="truncate font-mono text-[12.5px] font-medium text-fg">{b.scope_name}</span>
                    <span className={cn("ml-auto text-[12px] font-medium tabular-nums", level === "over" ? "text-bad" : level === "near" ? "text-warn" : "text-fg")}>
                      {Math.round(pct)}%
                      <span className="sr-only">{level === "over" ? " used, limit reached" : level === "near" ? " used, near limit" : " used"}</span>
                    </span>
                  </div>
                  <div className="h-1.5 overflow-hidden rounded-full bg-track" aria-hidden="true">
                    <div className={cn("h-full rounded-full", fill)} style={{ width: `${Math.min(100, pct)}%` }} />
                  </div>
                  <div className="flex justify-between gap-2 text-[11.5px] tabular-nums text-fg-muted">
                    <span className="truncate">{detail} · {b.period}</span>
                    <span className="shrink-0">{b.resets_at ? `Resets ${relativeFuture(b.resets_at)}` : "No reset"}</span>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </Panel>
  );
}

// ── Routing chains ───────────────────────────────────────────────────────────

const STEP_OPACITY = [1, 0.6, 0.38, 0.24, 0.16];

function ChainsCard({ chains, loading, className }: { chains?: ChainUsage[]; loading: boolean; className?: string }) {
  const rows = (chains ?? []).slice(0, 4);
  return (
    <Panel title="Routing chains" action={rows.length > 0 ? <PanelLink to="/chains">Manage chains</PanelLink> : undefined} className={className}>
      {loading ? (
        <div className="space-y-3 p-4" aria-busy="true">
          <span className="sr-only">Loading chains</span>
          {[0, 1, 2].map((i) => <Skeleton key={i} className="h-14 w-full" />)}
        </div>
      ) : rows.length === 0 ? (
        // The page's single next-step callout.
        <PanelEmpty
          title="No fallback chains yet"
          hint="A chain tries models in order, so one rate-limited provider never stops your tools."
          action={
            <Link
              to="/chains/new"
              className={cn("inline-flex h-8 items-center rounded-lg border border-line-strong bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:bg-hover", FOCUS_RING)}
            >
              Create chain
            </Link>
          }
        />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((c) => {
            const rotating = /round|random/.test(c.strategy);
            const sep = rotating ? "·" : "→";
            return (
              <li key={c.chain_id}>
                <Link
                  to={`/chains/${c.chain_id}/edit`}
                  className="flex flex-col gap-2 px-4 py-3 transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
                >
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="font-mono text-[13px] font-medium text-fg">{c.name}</span>
                    <span className="inline-flex h-5 items-center rounded-md border border-line px-1.5 text-[11.5px] text-fg-muted">{humanStrategy(c.strategy)}</span>
                    <span className="ml-auto text-[12px] tabular-nums text-fg-muted">
                      {c.requests > 0 ? (
                        <>
                          {fmtInt(c.requests)} req ·{" "}
                          <span className={cn(c.fallback_rate >= 0.1 ? "text-warn" : "text-fg")}>{fmtPct(c.fallback_rate, 1)}</span> fell back
                          <span className="sr-only"> ({fmtInt(c.fallback_requests)} requests)</span>
                        </>
                      ) : (
                        "No traffic"
                      )}
                    </span>
                  </div>
                  {c.requests > 0 && (
                    <div className="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-track" aria-hidden="true">
                      {c.steps.map((st, i) =>
                        st.requests > 0 ? (
                          <span key={`${st.provider}/${st.model}`} className="bg-accent-500" style={{ width: `${st.share * 100}%`, opacity: STEP_OPACITY[Math.min(i, STEP_OPACITY.length - 1)] }} />
                        ) : null,
                      )}
                    </div>
                  )}
                  <div className="flex flex-wrap items-center gap-1.5 text-[12px]">
                    {c.steps.map((st, i) => (
                      <span key={`${st.provider}/${st.model}/${i}`} className="inline-flex min-w-0 items-center gap-1.5">
                        {i > 0 && <span className="text-fg-faint" aria-hidden="true">{st.is_fallback ? "⤳" : sep}</span>}
                        <span
                          className="inline-flex h-6 min-w-0 items-center gap-1.5 rounded-md border border-line bg-subtle px-2"
                          title={`${st.provider}/${st.model}${st.is_fallback ? " · last-resort fallback" : ""}`}
                        >
                          <span className="h-1.5 w-1.5 shrink-0 rounded-[2px] bg-accent-500" style={{ opacity: STEP_OPACITY[Math.min(i, STEP_OPACITY.length - 1)] }} aria-hidden="true" />
                          <span className="truncate font-mono text-fg">{st.model}</span>
                          {st.is_fallback && <span className="sr-only">(last-resort fallback)</span>}
                          {c.requests > 0 && <span className="tabular-nums text-fg-muted">{fmtPct(st.share, st.share < 0.1 ? 1 : 0)}</span>}
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

// ── Budgets ──────────────────────────────────────────────────────────────────

function usedPct(b: BudgetStatus): number {
  return Math.max(b.limit_micros > 0 ? b.pct_used : 0, b.limit_tokens > 0 ? b.tokens_pct_used : 0);
}

// ── Recent requests ──────────────────────────────────────────────────────────

const STATUS_STYLE: Record<string, { label: string; className: string }> = {
  success: { label: "OK", className: "border-line text-fg-muted [&>i]:bg-ok" },
  cache_hit: { label: "Cache hit", className: "border-line text-fg-muted [&>i]:bg-accent-500" },
  failed: { label: "Failed", className: "border-transparent bg-bad/10 text-bad [&>i]:bg-bad" },
  blocked: { label: "Blocked", className: "border-transparent bg-warn/12 text-warn [&>i]:bg-warn" },
  cancelled: { label: "Cancelled", className: "border-line text-fg-muted [&>i]:bg-fg-faint" },
};

function RecentRequestsCard({ recent, className }: { recent: RecentActivity[]; className?: string }) {
  return (
    <Panel title="Recent requests" action={<PanelLink to="/usage?tab=requests">All requests</PanelLink>} className={className}>
      {recent.length === 0 ? (
        <PanelEmpty title="No requests in this period" />
      ) : (
        <div className="overflow-x-auto focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500" tabIndex={0} role="region" aria-label="Recent requests table">
          <table className="w-full min-w-[600px] text-[12.5px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Time</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={TH}>Model</th>
                <th scope="col" className={cn(TH, "text-right")}>Latency</th>
                <th scope="col" className={cn(TH, "text-right")}>Tokens in → out</th>
                <th scope="col" className={cn(TH, "text-right")}>Cost</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {recent.map((r) => {
                const st = STATUS_STYLE[r.status] ?? STATUS_STYLE.cancelled;
                const caller = [r.api_key_name, r.client].filter(Boolean).join(" · ");
                return (
                  <tr key={r.id} className="transition-colors hover:bg-hover">
                    <td className="whitespace-nowrap px-4 py-2 font-mono text-[12px] text-fg-muted" title={new Date(r.created_at).toLocaleString()}>
                      {formatClock(r.created_at)}
                    </td>
                    <td className="px-4 py-2">
                      <span className={cn("inline-flex h-5 items-center gap-1.5 whitespace-nowrap rounded-md border px-1.5 text-[11.5px] font-medium", st.className)} title={r.error_kind || undefined}>
                        <i className="h-1.5 w-1.5 rounded-full" aria-hidden="true" />
                        {r.status === "failed" && r.error_kind ? humanError(r.error_kind) : st.label}
                      </span>
                    </td>
                    <td className="max-w-[300px] px-4 py-2">
                      <span className="flex min-w-0 items-center gap-2.5">
                        <ProviderLogo icon={r.provider_icon} name={r.provider_name || r.provider} size={18} />
                        <span className="min-w-0">
                          <span className="block truncate font-mono text-fg" title={`${r.provider}/${r.model}`}>{r.model || "unknown"}</span>
                          {caller && <span className="block truncate font-mono text-[11.5px] text-fg-muted">{caller}</span>}
                        </span>
                      </span>
                    </td>
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
  const setupId = useId();
  const gatewayId = useId();
  const snippetBase = useId();

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
  const snippetKeys = Object.keys(snippets) as SnippetKey[];

  const copySnippet = async () => {
    try {
      await navigator.clipboard.writeText(snippets[snippet].code);
      toast.success("Snippet copied");
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access. Select the snippet and copy it manually.");
    }
  };

  return (
    <div className="grid gap-5 xl:grid-cols-3">
      <section aria-labelledby={setupId} className="min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)] xl:col-span-2">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
          <div className="flex items-baseline gap-2">
            <h2 id={setupId} className="text-[13px] font-semibold text-fg">Finish setting up</h2>
            <span className="text-[12px] tabular-nums text-fg-muted">{done} of 4 done</span>
          </div>
          <div
            className="h-1.5 w-40 overflow-hidden rounded-full bg-track"
            role="progressbar"
            aria-valuenow={done * 25}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuetext={`${done} of 4 steps done`}
            aria-label="Setup progress"
          >
            <div className="h-full bg-accent-500 transition-[width]" style={{ width: `${done * 25}%` }} />
          </div>
        </div>
        <ol className="divide-y divide-line">
          <SetupStep
            index={0}
            current={current}
            done={steps[0]}
            title="Connect a provider"
            body={steps[0] ? `${accountCount} account${accountCount === 1 ? "" : "s"} connected.` : "An API key or OAuth sign-in, any of 90+ providers."}
            action={<SetupAction to="/providers" primary={current === 0}>{steps[0] ? "Manage providers" : "Connect provider"}</SetupAction>}
          />
          <SetupStep
            index={1}
            current={current}
            done={steps[1]}
            title="Create an API key"
            body="Your tools use it instead of provider credentials."
            action={<SetupAction to="/keys" primary={current === 1}>{steps[1] ? "Manage keys" : "Create key"}</SetupAction>}
          />
          <SetupStep
            index={2}
            current={current}
            done={steps[2]}
            title="Point a tool at KeiRouter"
            body="Paste a snippet, or let KeiRouter write the config."
            action={<SetupAction to="/cli-tools" primary={current === 2}>Auto-configure tools</SetupAction>}
          >
            <div className="mt-3 overflow-hidden rounded-xl border border-line">
              <div className="flex items-center gap-1 border-b border-line bg-subtle p-1.5">
                <div className="flex items-center gap-1" role="tablist" aria-label="Snippet" onKeyDown={(e) => onGroupArrowKeys(e, "tab")}>
                  {snippetKeys.map((k) => (
                    <button
                      key={k}
                      id={`${snippetBase}-tab-${k}`}
                      type="button"
                      role="tab"
                      aria-selected={snippet === k}
                      aria-controls={`${snippetBase}-panel`}
                      tabIndex={snippet === k ? 0 : -1}
                      onClick={() => setSnippet(k)}
                      className={cn(
                        "h-7 rounded-lg px-2.5 text-[12px] font-medium transition-colors",
                        FOCUS_RING,
                        snippet === k ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
                      )}
                    >
                      {snippets[k].label}
                    </button>
                  ))}
                </div>
                <button
                  type="button"
                  onClick={copySnippet}
                  aria-label={`Copy ${snippets[snippet].label} snippet`}
                  className={cn("ml-auto flex h-7 w-7 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
                >
                  <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                </button>
              </div>
              <pre
                id={`${snippetBase}-panel`}
                role="tabpanel"
                aria-labelledby={`${snippetBase}-tab-${snippet}`}
                tabIndex={0}
                className={cn("overflow-x-auto bg-canvas px-3.5 py-3 font-mono text-[12px] leading-relaxed text-fg", FOCUS_RING)}
              >
                {snippets[snippet].code}
              </pre>
            </div>
          </SetupStep>
          <SetupStep
            index={3}
            current={current}
            done={false}
            title="Send your first request"
            body="This page fills in as soon as traffic arrives."
            action={
              <span className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] text-fg-muted">
                <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
                Listening on <span className="font-mono">/v1</span>
              </span>
            }
          />
        </ol>
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line bg-subtle px-4 py-2.5 text-[12px] text-fg-muted">
          <span>Then:</span>
          <Link to="/chains/new" className={cn("inline-flex min-h-6 items-center rounded-md font-medium text-link hover:underline", FOCUS_RING)}>Add a fallback chain</Link>
          <Link to="/settings" className={cn("inline-flex min-h-6 items-center rounded-md font-medium text-link hover:underline", FOCUS_RING)}>Turn on token savings</Link>
          <Link to="/plans" className={cn("inline-flex min-h-6 items-center rounded-md font-medium text-link hover:underline", FOCUS_RING)}>Set a budget</Link>
        </p>
      </section>

      <section aria-labelledby={gatewayId} className="min-w-0 self-start overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
          <h2 id={gatewayId} className="text-[13px] font-semibold text-fg">Gateway</h2>
          <span className="inline-flex h-5 items-center gap-1.5 rounded-md border border-line px-1.5 text-[11.5px] font-medium text-fg-muted">
            <span className="h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
            Running
          </span>
        </div>
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
            <Link to="/endpoints" className={cn("inline-flex min-h-6 items-center rounded-md text-[12px] font-medium text-link hover:underline", FOCUS_RING)}>
              Set up tunnel
            </Link>
          </dd>
        </dl>
      </section>
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
    <li className={cn("flex gap-3.5 px-4 py-4", isCurrent && "bg-subtle")} aria-current={isCurrent ? "step" : undefined}>
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
          <div className="min-w-[min(100%,220px)] flex-1">
            <h3 className={cn("text-[13px]", done ? "font-medium text-fg-muted line-through decoration-line-strong" : isCurrent ? "font-semibold text-fg" : "font-medium text-fg")}>
              <span className="sr-only">Step {index + 1}: </span>
              {title}
              <span className="sr-only">{done ? " (done)" : isCurrent ? " (current step)" : ""}</span>
            </h3>
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
        "inline-flex h-8 shrink-0 items-center rounded-lg px-3 text-[13px] font-medium transition-colors focus-visible:ring-offset-2",
        FOCUS_RING,
        primary ? "bg-primary text-primary-fg hover:opacity-85" : "border border-line-strong bg-surface text-fg hover:bg-hover",
      )}
    >
      {children}
    </Link>
  );
}

// ── Skeleton ─────────────────────────────────────────────────────────────────

function OverviewSkeleton() {
  return (
    <div className="space-y-5" aria-busy="true">
      <span className="sr-only" role="status">Loading overview</span>
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
  return tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : tone === "bad" ? "text-bad" : "text-fg-muted";
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
