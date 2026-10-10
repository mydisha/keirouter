import { useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { ArrowLeft, ChevronRight, CircleAlert, CircleX, CornerDownRight, Lightbulb, Play, RefreshCw, X, type LucideIcon } from "lucide-react";
import {
  api,
  type HealthStatus,
  type HealthSummary,
  type HealthProviderRow,
  type HealthProviderDetail,
  type HealthModelRow,
  type HealthChainRow,
  type HealthProbeRow,
  type HealthTimelineProvider,
  type Provider,
} from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { Badge, Button, ErrorCard, IconTile, Kpi, KpiGrid, SectionTitle, Skeleton, TablePagination, useClientPagination } from "../components/ui";
import { ICONS } from "../lib/icons";
import { HealthStatusBadge, HealthScoreRing, fmtIssue } from "../components/HealthBadge";
import { ErrorTypeBreakdown, HealthTrends } from "../components/HealthCharts";
import { useToast } from "../components/Toast";

// "Live" is the telemetry service's rolling window; every longer range is
// answered from persisted snapshots, so it survives restarts.
const RANGES = [
  { value: "15m", label: "Live" },
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

const RANGE_LABEL: Record<string, string> = {
  "15m": "Live, last 15 minutes",
  "1h": "Last hour",
  "6h": "Last 6 hours",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
};

// Bucket counts keep each uptime strip between 15 and 36 ticks.
const TIMELINE_BUCKETS: Record<string, number> = { "15m": 15, "1h": 30, "6h": 36, "24h": 24, "7d": 28, "30d": 30 };

const STATUS_FILTERS = [
  { value: "", label: "All" },
  { value: "healthy", label: "Healthy" },
  { value: "degraded", label: "Degraded" },
  { value: "unhealthy", label: "Unhealthy" },
];

// Problems first: the API orders by request volume, which stays the tie-breaker.
const STATUS_RANK: Record<string, number> = { unhealthy: 0, degraded: 1, healthy: 2, unknown: 3, disabled: 4 };

function fmtMs(ms?: number) {
  if (ms == null || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

function fmtPct(v?: number) {
  if (v == null) return "—";
  return `${v.toFixed(1)}%`;
}

function fmtTime(t?: string) {
  if (!t) return "—";
  const d = new Date(t);
  if (isNaN(d.getTime())) return "—";
  const diff = Date.now() - d.getTime();
  if (diff < 60_000) return "just now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return d.toLocaleString();
}

const FOCUS = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

// Arrow / Home / End move focus and selection inside a radiogroup or tablist.
function rovingKeyDown(role: "radio" | "tab") {
  return (e: ReactKeyboardEvent<HTMLElement>) => {
    const fwd = ["ArrowRight", "ArrowDown"];
    const back = ["ArrowLeft", "ArrowUp"];
    if (![...fwd, ...back, "Home", "End"].includes(e.key)) return;
    const items = Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>(`[role="${role}"]`));
    const cur = items.indexOf(document.activeElement as HTMLButtonElement);
    if (cur < 0 || items.length === 0) return;
    e.preventDefault();
    const next =
      e.key === "Home" ? 0 : e.key === "End" ? items.length - 1 : (cur + (fwd.includes(e.key) ? 1 : -1) + items.length) % items.length;
    items[next]?.focus();
    items[next]?.click();
  };
}

const onRadioKeyDown = rovingKeyDown("radio");
const onTabKeyDown = rovingKeyDown("tab");

function RangeSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Time range" onKeyDown={onRadioKeyDown}>
      {RANGES.map((r) => {
        const active = value === r.value;
        return (
          <button
            key={r.value}
            type="button"
            role="radio"
            aria-checked={active}
            title={RANGE_LABEL[r.value]}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(r.value)}
            className={cn(
              "inline-flex h-full min-w-[30px] items-center justify-center gap-1.5 rounded-lg px-2.5 text-[12px] font-medium transition-colors",
              FOCUS,
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
              r.value !== "15m" && "font-mono",
            )}
          >
            {r.value === "15m" && <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />}
            {r.label}
          </button>
        );
      })}
    </div>
  );
}

export function ProviderHealthPage() {
  const { provider } = useParams();
  if (provider) return <ProviderDetail provider={provider} />;
  return <Overview />;
}

// ---- Overview ---------------------------------------------------------------

type Tab = "providers" | "models" | "chains" | "probes";

const TABS: { value: Tab; label: string }[] = [
  { value: "providers", label: "Providers" },
  { value: "models", label: "Models" },
  { value: "chains", label: "Chains" },
  { value: "probes", label: "Probes" },
];

function Overview() {
  const [params, setParams] = useSearchParams();
  const range = params.get("range") ?? "24h";
  const status = params.get("status") ?? "";
  const tab = (params.get("tab") as Tab | null) ?? "providers";
  const qc = useQueryClient();

  const setParam = (key: string, value: string) =>
    setParams((p) => {
      if (value) p.set(key, value);
      else p.delete(key);
      return p;
    }, { replace: true });

  const overview = useQuery({
    queryKey: ["health-overview", range, status],
    queryFn: () => api.healthOverview(range, status || undefined),
    staleTime: 15_000,
    refetchInterval: 30_000,
    placeholderData: (prev) => prev,
  });
  const timeline = useQuery({
    queryKey: ["health-timeline", range, TIMELINE_BUCKETS[range] ?? 24],
    queryFn: () => api.healthTimeline(range, TIMELINE_BUCKETS[range] ?? 24),
    staleTime: 30_000,
    refetchInterval: 60_000,
    placeholderData: (prev) => prev,
  });
  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 5 * 60_000 });
  const models = useQuery({
    queryKey: ["health-models", range, status],
    queryFn: () => api.healthModels(range, status || undefined),
    enabled: tab === "models",
    staleTime: 15_000,
  });
  const chains = useQuery({
    queryKey: ["health-chains", range],
    queryFn: () => api.healthChains(range),
    enabled: tab === "chains",
    staleTime: 15_000,
  });

  const meta = new Map((providers.data?.providers ?? []).map((p) => [p.id, p]));
  const strips = new Map((timeline.data?.providers ?? []).map((p) => [p.provider, p]));
  const refreshing = overview.isFetching && !overview.isLoading;

  return (
    <div>
      <PageHeader
        title="Provider health"
        description="Which upstreams are failing or slow, and what to do."
        action={
          <>
            <RangeSelect value={range} onChange={(v) => setParam("range", v)} />
            <button
              type="button"
              onClick={() => {
                qc.invalidateQueries({ queryKey: ["health-overview"] });
                qc.invalidateQueries({ queryKey: ["health-timeline"] });
                qc.invalidateQueries({ queryKey: ["health-models"] });
                qc.invalidateQueries({ queryKey: ["health-chains"] });
              }}
              aria-label="Refresh health"
              className={cn("inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS)}
            >
              <RefreshCw className={cn("h-4 w-4", refreshing && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
            </button>
          </>
        }
      />

      {overview.isLoading ? (
        <div className="space-y-6" aria-busy="true">
          <span className="sr-only" role="status">Loading provider health</span>
          <Skeleton className="h-[84px] w-full rounded-2xl" />
          <TableSkeleton rows={6} />
        </div>
      ) : overview.isError ? (
        <ErrorCard message="Couldn't load provider health. Try refreshing." />
      ) : overview.data ? (
        <div className="space-y-6">
          <SummaryStrip summary={overview.data.summary} rows={overview.data.providers} />

          <div className="flex flex-wrap items-end justify-between gap-x-3 gap-y-2 border-b border-line">
            <div className="-mb-px flex gap-1" role="tablist" aria-label="Health views" onKeyDown={onTabKeyDown}>
              {TABS.map((t) => {
                const active = tab === t.value;
                return (
                  <button
                    key={t.value}
                    id={`health-tab-${t.value}`}
                    type="button"
                    role="tab"
                    aria-selected={active}
                    aria-controls="health-tabpanel"
                    tabIndex={active ? 0 : -1}
                    onClick={() => setParam("tab", t.value === "providers" ? "" : t.value)}
                    className={cn("relative px-3 py-2.5 text-[13px] font-medium transition-colors", FOCUS, active ? "text-fg" : "text-fg-muted hover:text-fg")}
                  >
                    {t.label}
                    {active && <span className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-500" aria-hidden="true" />}
                  </button>
                );
              })}
            </div>
            {(tab === "providers" || tab === "models") && (
              <div className="mb-2 flex flex-wrap gap-1" role="radiogroup" aria-label="Filter by status" onKeyDown={onRadioKeyDown}>
                {STATUS_FILTERS.map((f) => {
                  const active = status === f.value;
                  return (
                    <button
                      key={f.value}
                      type="button"
                      role="radio"
                      aria-checked={active}
                      tabIndex={active ? 0 : -1}
                      onClick={() => setParam("status", f.value)}
                      className={cn(
                        "h-7 rounded-lg border px-2.5 text-[12px] font-medium transition-colors",
                        FOCUS,
                        active ? "border-accent-500/30 bg-accent-500/10 text-link" : "border-line bg-surface text-fg-muted hover:text-fg",
                      )}
                    >
                      {f.label}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          <div role="tabpanel" id="health-tabpanel" aria-labelledby={`health-tab-${tab}`}>
            {tab === "providers" && <ProviderTable rows={overview.data.providers} meta={meta} strips={strips} range={range} />}
            {tab === "models" && <ModelTable query={models} />}
            {tab === "chains" && <ChainTable query={chains} />}
            {tab === "probes" && <ProbeHistoryTable range={range} />}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function SummaryStrip({ summary, rows }: { summary: HealthSummary; rows: HealthProviderRow[] }) {
  const total = summary.healthy + summary.degraded + summary.unhealthy + summary.unknown + summary.disabled;
  const requests = rows.reduce((n, r) => n + (r.requests ?? 0), 0);
  // Status cells are green when clear, tinted when something needs attention.
  const statusTone = (n: number, bad: "warn" | "bad") => (!total ? "section" : n ? bad : "ok");
  return (
    <KpiGrid cols={4} label="Health summary">
      <Kpi
        icon={CircleX}
        label="Unhealthy"
        value={String(summary.unhealthy)}
        tone={statusTone(summary.unhealthy, "bad")}
        valueClassName={summary.unhealthy ? "text-bad" : ""}
        hint={total ? `of ${total} providers` : "No telemetry yet"}
      />
      <Kpi
        icon={CircleAlert}
        label="Degraded"
        value={String(summary.degraded)}
        tone={statusTone(summary.degraded, "warn")}
        valueClassName={summary.degraded ? "text-warn" : ""}
        hint={total ? `${summary.healthy} healthy` : undefined}
      />
      <Kpi
        icon={ICONS.fallbacks}
        label="Fallbacks"
        value={summary.fallbacks.toLocaleString("en-US")}
        tone={summary.fallbacks ? "warn" : "section"}
        valueClassName={summary.fallbacks ? "text-warn" : ""}
        hint={requests ? `of ${requests.toLocaleString("en-US")} requests` : undefined}
      />
      <Kpi icon={ICONS.latency} label="Avg p95 latency" value={fmtMs(summary.avg_p95_latency_ms)} />
    </KpiGrid>
  );
}

const TICK_CLASS: Record<string, string> = { ok: "bg-ok/70", degraded: "bg-warn", down: "bg-bad", idle: "bg-track" };

function stripLabel(name: string, strip: HealthTimelineProvider) {
  const down = strip.buckets.filter((b) => b.status === "down").length;
  const degraded = strip.buckets.filter((b) => b.status === "degraded").length;
  if (!down && !degraded) return `${name}: no problems in ${strip.buckets.length} intervals`;
  return `${name}: ${down} down and ${degraded} degraded of ${strip.buckets.length} intervals`;
}

function ProviderTable({
  rows,
  meta,
  strips,
  range,
}: {
  rows: HealthProviderRow[];
  meta: Map<string, Provider>;
  strips: Map<string, HealthTimelineProvider>;
  range: string;
}) {
  const navigate = useNavigate();
  const sorted = [...rows].sort((a, b) => (STATUS_RANK[a.status] ?? 9) - (STATUS_RANK[b.status] ?? 9));
  const { page, pages, paged, setPage, total } = useClientPagination(sorted, 15);
  if (rows.length === 0) {
    return <EmptyPanel icon={ICONS.health} title="No provider telemetry in this range" hint="Health appears once requests flow or a probe runs." />;
  }
  return (
    <TableCard footer={<TablePagination page={page} pages={pages} total={total} onPage={setPage} />}>
      <table className="w-full min-w-[860px] text-[13px]">
        <thead>
          <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
            <th scope="col" className={TH}>Provider</th>
            <th scope="col" className={TH}>Status</th>
            <th scope="col" className={TH}>Uptime · {range === "15m" ? "live" : range}</th>
            <th scope="col" className={THR}>Requests</th>
            <th scope="col" className={THR}>Success</th>
            <th scope="col" className={THR}>p95</th>
            <th scope="col" className={TH}>Main issue</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {paged.map((r) => {
            const p = meta.get(r.provider);
            const strip = strips.get(r.provider);
            const name = p?.display_name ?? r.provider;
            const href = `/provider-health/${encodeURIComponent(r.provider)}`;
            return (
              <tr key={r.provider} className="cursor-pointer transition-colors hover:bg-hover" onClick={() => navigate(href)}>
                <td className="px-4 py-2.5">
                  <Link to={href} onClick={(e) => e.stopPropagation()} className={cn("flex items-center gap-2.5 rounded-md", FOCUS)}>
                    <ProviderLogo icon={p?.icon} name={name} size={22} />
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-fg">{name}</span>
                      <span className="block truncate font-mono text-[11.5px] text-fg-faint">
                        {r.provider} · {r.accounts} account{r.accounts === 1 ? "" : "s"}
                      </span>
                    </span>
                  </Link>
                </td>
                <td className="px-4 py-2.5" title={`Score ${r.score}${r.live_status ? " · live status" : ""}`}>
                  <HealthStatusBadge status={r.status as HealthStatus} issue={r.main_issue} />
                </td>
                <td className="px-4 py-2.5">
                  {strip ? (
                    <div className="flex w-40 gap-[2px]" role="img" aria-label={stripLabel(name, strip)}>
                      {strip.buckets.map((b) => (
                        <span key={b.start} className={cn("h-3.5 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")} title={`${fmtTime(b.start)} · ${b.status}${b.requests ? ` · ${b.requests} req` : ""}`} />
                      ))}
                    </div>
                  ) : (
                    <span className="text-[12px] text-fg-faint">—</span>
                  )}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                  <span className="block text-fg">{r.requests != null ? r.requests.toLocaleString("en-US") : "—"}</span>
                  {r.fallback_count > 0 && (
                    <span className="block text-[12px] text-warn">
                      {r.fallback_count.toLocaleString("en-US")} fallback{r.fallback_count === 1 ? "" : "s"}
                    </span>
                  )}
                </td>
                <td className={cn("whitespace-nowrap px-4 py-2.5 text-right tabular-nums", r.success_rate >= 99 ? "text-fg" : r.success_rate >= 95 ? "text-warn" : "text-bad")}>{fmtPct(r.success_rate)}</td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtMs(r.latency_p95_ms)}</td>
                <td className="max-w-[260px] px-4 py-2.5">
                  {r.main_issue ? (
                    <>
                      <span className="block truncate text-fg">{fmtIssue(r.main_issue)}</span>
                      {r.recommendation && (
                        <span className="block truncate text-[12px] text-fg-muted" title={r.recommendation}>
                          {r.recommendation}
                        </span>
                      )}
                    </>
                  ) : (
                    <span className="text-fg-faint">—</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableCard>
  );
}

const TH = "px-4 py-2 font-medium";
const THR = "px-4 py-2 text-right font-medium";

function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]" aria-hidden="true">
      <div className="h-9 border-b border-line bg-subtle" />
      <div className="divide-y divide-line">
        {Array.from({ length: rows }, (_, i) => (
          <div key={i} className="flex items-center gap-4 px-4 py-3">
            <Skeleton className="h-4 w-48" />
            <Skeleton className="h-4 w-16" />
            <Skeleton className="ml-auto h-4 w-24" />
          </div>
        ))}
      </div>
    </div>
  );
}

function LoadingTable() {
  return (
    <div aria-busy="true">
      <span className="sr-only" role="status">Loading</span>
      <TableSkeleton />
    </div>
  );
}

function EmptyPanel({ icon, title, hint }: { icon: LucideIcon; title: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-10 text-center">
      <IconTile icon={icon} size="lg" className="mb-3" />
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {hint && <p className="mt-1 text-[12.5px] text-fg-muted">{hint}</p>}
    </div>
  );
}

function TableCard({ children, footer }: { children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="overflow-x-auto">{children}</div>
      {footer}
    </div>
  );
}

function TargetCell({ provider, model }: { provider?: string; model?: string }) {
  return (
    <span className="flex min-w-0 items-baseline gap-1 font-mono text-[12.5px]">
      <span className="text-fg-muted">{provider}/</span>
      <span className="truncate text-fg" title={model}>{model}</span>
    </span>
  );
}

function ModelTable({ query }: { query: UseQueryResult<{ models: HealthModelRow[] } | HealthProviderDetail> }) {
  if (query.isLoading) return <LoadingTable />;
  if (query.isError) return <ErrorCard message="Couldn't load model health. Try refreshing." />;
  const rows = (query.data as { models?: HealthModelRow[] } | undefined)?.models ?? [];
  if (rows.length === 0) return <EmptyPanel icon={ICONS.model} title="No model health data yet" hint="Rows appear once a model gets traffic or a probe." />;
  return <ModelTableInner rows={rows} />;
}

function ModelTableInner({ rows }: { rows: HealthModelRow[] }) {
  const { page, pages, paged, setPage, total } = useClientPagination(rows, 10);
  return (
    <TableCard footer={<TablePagination page={page} pages={pages} total={total} onPage={setPage} />}>
      <table className="w-full min-w-[760px] text-[13px]">
        <thead>
          <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
            <th scope="col" className={TH}>Model</th>
            <th scope="col" className={TH}>Status</th>
            <th scope="col" className={TH}>Score</th>
            <th scope="col" className={THR}>Success</th>
            <th scope="col" className={THR}>p95</th>
            <th scope="col" className={THR}>Fallbacks</th>
            <th scope="col" className={TH}>Main issue</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {paged.map((r: HealthModelRow, i: number) => (
            <tr key={`${r.provider}/${r.model}-${i}`} className="hover:bg-hover">
              <td className="max-w-[320px] px-4 py-2.5"><TargetCell provider={r.provider} model={r.model ?? ""} /></td>
              <td className="px-4 py-2.5"><HealthStatusBadge status={r.status as HealthStatus} issue={r.main_issue} /></td>
              <td className="px-4 py-2.5"><HealthScoreRing score={r.score} /></td>
              <td className="px-4 py-2.5 text-right tabular-nums text-fg">{fmtPct(r.success_rate)}</td>
              <td className="px-4 py-2.5 text-right tabular-nums text-fg">{fmtMs(r.latency_p95_ms)}</td>
              <td className={cn("px-4 py-2.5 text-right tabular-nums", r.fallback_count ? "text-warn" : "text-fg-muted")}>{r.fallback_count.toLocaleString("en-US")}</td>
              <td className="px-4 py-2.5 text-fg-muted">{fmtIssue(r.main_issue) || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableCard>
  );
}

function ChainTable({ query }: { query: UseQueryResult<{ chains: HealthChainRow[] }> }) {
  const [selected, setSelected] = useState<string | null>(null);
  if (query.isLoading) return <LoadingTable />;
  if (query.isError) return <ErrorCard message="Couldn't load chain impact. Try refreshing." />;
  const rows = query.data?.chains ?? [];
  if (rows.length === 0) return <EmptyPanel icon={ICONS.chains} title="No chains configured" hint="Chains show here once they serve traffic." />;
  const close = () => {
    const id = selected;
    setSelected(null);
    // Return focus to the row that opened the detail.
    if (id) requestAnimationFrame(() => document.getElementById(`chain-row-${id}`)?.focus());
  };
  return (
    <div className="space-y-4">
      <ChainTableInner rows={rows} selected={selected} onSelect={(id) => setSelected((s) => (s === id ? null : id))} />
      {selected && <ChainDetail id={selected} onClose={close} />}
    </div>
  );
}

function ChainTableInner({ rows, selected, onSelect }: { rows: HealthChainRow[]; selected: string | null; onSelect: (id: string) => void }) {
  const { page, pages, paged, setPage, total } = useClientPagination(rows, 10);
  return (
    <TableCard footer={<TablePagination page={page} pages={pages} total={total} onPage={setPage} />}>
      <table className="w-full min-w-[760px] text-[13px]">
        <thead>
          <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
            <th scope="col" className={TH}>Chain</th>
            <th scope="col" className={TH}>Status</th>
            <th scope="col" className={THR}>Requests</th>
            <th scope="col" className={THR}>Fallback rate</th>
            <th scope="col" className={THR}>Final failures</th>
            <th scope="col" className={TH}>Most affected</th>
            <th scope="col" className="w-10 px-4 py-2"><span className="sr-only">Details</span></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {paged.map((r) => {
            const open = selected === r.chain_id;
            return (
              <tr key={r.chain_id} className={cn("cursor-pointer hover:bg-hover", open && "bg-subtle")} onClick={() => onSelect(r.chain_id)}>
                <td className="px-4 py-2.5">
                  <button
                    id={`chain-row-${r.chain_id}`}
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      onSelect(r.chain_id);
                    }}
                    aria-expanded={open}
                    aria-controls={open ? "chain-detail" : undefined}
                    className={cn("rounded-md text-left font-medium text-fg hover:underline", FOCUS)}
                  >
                    {r.name}
                  </button>
                </td>
                <td className="px-4 py-2.5"><HealthStatusBadge status={r.status} issue={r.main_issue} /></td>
                <td className="px-4 py-2.5 text-right tabular-nums text-fg">{r.requests.toLocaleString("en-US")}</td>
                <td className="px-4 py-2.5 text-right tabular-nums text-fg">{fmtPct(r.fallback_rate)}</td>
                <td className={cn("px-4 py-2.5 text-right tabular-nums", r.final_failure_count > 0 ? "font-medium text-bad" : "text-fg-muted")}>{r.final_failure_count.toLocaleString("en-US")}</td>
                <td className="max-w-[280px] px-4 py-2.5">{r.affected_provider ? <TargetCell provider={r.affected_provider} model={r.affected_model ?? ""} /> : <span className="text-fg-faint">—</span>}</td>
                <td className="px-4 py-2.5 text-fg-faint"><ChevronRight className={cn("h-4 w-4 transition-transform", open && "rotate-90")} strokeWidth={1.75} aria-hidden="true" /></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableCard>
  );
}

function ChainDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const q = useQuery({
    queryKey: ["health-chain", id],
    queryFn: () => api.healthChainDetail(id),
    staleTime: 15_000,
  });
  return (
    <section
      id="chain-detail"
      aria-labelledby="chain-detail-title"
      aria-busy={q.isLoading}
      onKeyDown={(e) => e.key === "Escape" && onClose()}
      className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]"
    >
      <div className="border-b border-line px-4 py-3">
        <SectionTitle
          id="chain-detail-title"
          icon={ICONS.chains}
          title={
            <span className="inline-flex flex-wrap items-center gap-2">
              {q.data?.name ?? "Chain detail"}
              {q.data && <Badge tone="neutral">{q.data.strategy}</Badge>}
            </span>
          }
          subtitle={
            q.data && (
              <span className="flex flex-wrap gap-x-2 tabular-nums">
                {q.data.requests != null && <span>{q.data.requests.toLocaleString("en-US")} requests</span>}
                {q.data.fallback_rate != null && <span>· {fmtPct(q.data.fallback_rate)} fallback rate</span>}
                {q.data.final_failure_count != null && q.data.final_failure_count > 0 && <span className="text-bad">· {q.data.final_failure_count} final failures</span>}
              </span>
            )
          }
          action={
            <button type="button" onClick={onClose} aria-label="Close chain detail" className={cn("inline-flex h-7 w-7 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg", FOCUS)}>
              <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            </button>
          }
        />
      </div>
      {q.isLoading ? (
        <div className="space-y-2 px-4 py-4" aria-hidden="true">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-11 w-full" />)}</div>
      ) : q.isError ? (
        <p className="px-4 py-8 text-center text-[13px] text-fg-muted">Couldn't load this chain. Close it and try again.</p>
      ) : q.data ? (
        <ol className="divide-y divide-line">
          {q.data.steps.map((step) => (
            <li key={step.position} className="flex items-center gap-3 px-4 py-2.5">
              <span className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-line bg-subtle text-[11.5px] font-medium tabular-nums text-fg-muted" aria-label={`Step ${step.position + 1}`}>{step.position + 1}</span>
              <div className="min-w-0 flex-1">
                <TargetCell provider={step.provider} model={step.model} />
                {step.main_issue && <p className="mt-0.5 text-[12px] text-fg-muted">{fmtIssue(step.main_issue)}</p>}
              </div>
              <HealthStatusBadge status={step.status} />
              <HealthScoreRing score={step.score} />
            </li>
          ))}
          {q.data.fallback_provider && (
            <li className="flex items-center gap-3 bg-subtle px-4 py-2.5">
              <span className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-dashed border-line-strong text-fg-faint" aria-hidden="true">
                <CornerDownRight className="h-3.5 w-3.5" strokeWidth={1.75} />
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-[12px] text-fg-muted">Final fallback</p>
                <TargetCell provider={q.data.fallback_provider} model={q.data.fallback_model ?? ""} />
              </div>
            </li>
          )}
        </ol>
      ) : null}
    </section>
  );
}

// ---- Probe history ---------------------------------------------------------

function ProbeHistoryTable({ range }: { range: string }) {
  const [page, setPage] = useState(1);
  const q = useQuery({
    queryKey: ["health-probes", range, page],
    queryFn: () => api.healthProbeHistory({ range, page, limit: 50 }),
    staleTime: 15_000,
  });
  if (q.isLoading) return <LoadingTable />;
  if (q.isError) return <ErrorCard message="Couldn't load probe history. Try refreshing." />;
  const rows = q.data?.items ?? [];
  const total = q.data?.pagination.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / 50));
  if (rows.length === 0) return <EmptyPanel icon={Play} title="No probes in this range" hint="Run a probe from a provider's health page." />;
  return (
    <TableCard footer={<TablePagination page={page} pages={pages} total={total} onPage={setPage} />}>
      <table className="w-full min-w-[820px] text-[13px]">
        <thead>
          <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
            <th scope="col" className={TH}>Time</th>
            <th scope="col" className={TH}>Model</th>
            <th scope="col" className={TH}>Result</th>
            <th scope="col" className={THR}>Latency</th>
            <th scope="col" className={THR}>HTTP</th>
            <th scope="col" className={TH}>Error</th>
            <th scope="col" className={TH}>Trigger</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {rows.map((r: HealthProbeRow, i) => (
            <tr key={i} className="hover:bg-hover">
              <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">{fmtTime(r.time)}</td>
              <td className="max-w-[320px] px-4 py-2.5"><TargetCell provider={r.provider} model={r.model} /></td>
              <td className="px-4 py-2.5">
                <span className={cn("inline-flex items-center gap-1.5 text-[12.5px]", r.status === "success" ? "text-fg" : "text-bad")}>
                  <span className={cn("h-1.5 w-1.5 rounded-full", r.status === "success" ? "bg-ok" : "bg-bad")} aria-hidden="true" />
                  {r.status === "success" ? "Success" : r.status}
                </span>
              </td>
              <td className="px-4 py-2.5 text-right tabular-nums text-fg">{fmtMs(r.latency_ms)}</td>
              <td className="px-4 py-2.5 text-right font-mono text-[12.5px] tabular-nums text-fg-muted">{r.http_status ?? "—"}</td>
              <td className="px-4 py-2.5 text-fg-muted">{fmtIssue(r.error_type) || "—"}</td>
              <td className="px-4 py-2.5"><Badge tone="neutral">{r.triggered_by}</Badge></td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableCard>
  );
}

// ---- Provider detail -------------------------------------------------------

function ProviderDetail({ provider }: { provider: string }) {
  const [params, setParams] = useSearchParams();
  const range = params.get("range") ?? "24h";

  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 5 * 60_000 });
  const detail = useQuery({
    queryKey: ["health-provider", provider, range],
    queryFn: () => api.healthProviderDetail(provider, range),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });

  if (detail.isLoading) {
    return (
      <div className="space-y-4" aria-busy="true">
        <span className="sr-only" role="status">Loading provider health</span>
        <Skeleton className="h-4 w-48" />
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-[76px] w-full rounded-2xl" />
        <Skeleton className="h-[260px] w-full rounded-2xl" />
      </div>
    );
  }
  if (detail.isError) return (
    <div>
      <BackLink />
      <ErrorCard message="Couldn't load this provider's health. Try refreshing." />
    </div>
  );
  const d = detail.data;
  if (!d) return null;

  const meta = providers.data?.providers.find((p) => p.id === d.provider);
  const name = meta?.display_name ?? d.provider;
  const m = d.metrics;
  // Success is a status value: tinted only once there is traffic to judge.
  const successTone = !m.requests ? "section" : m.success_rate >= 99 ? "ok" : m.success_rate >= 95 ? "warn" : "bad";

  return (
    <div>
      <nav aria-label="Breadcrumb" className="mb-3">
        <ol className="flex items-center gap-1.5 text-[13px] text-fg-muted">
          <li>
            <Link to={`/provider-health?range=${range}`} className={cn("inline-flex items-center gap-1.5 rounded-md hover:text-fg", FOCUS)}>
              <ArrowLeft className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
              Provider health
            </Link>
          </li>
          <li aria-hidden="true" className="text-fg-faint">/</li>
          <li aria-current="page" className="text-fg">{name}</li>
        </ol>
      </nav>

      <header className="mb-4 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-center gap-3">
          <ProviderLogo icon={meta?.icon} name={name} size={40} className="rounded-lg" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{name}</h1>
              <HealthStatusBadge status={d.status as HealthStatus} issue={d.main_issue} />
              <HealthScoreRing score={d.score} />
            </div>
            <p className="mt-0.5 flex flex-wrap items-center gap-2 text-[13px] text-fg-muted">
              <span className="font-mono text-[12.5px]">{d.provider}</span>
              {meta && (
                <>
                  <span aria-hidden="true" className="text-fg-faint">·</span>
                  <Link to={`/providers/${d.provider}`} className={cn("rounded-sm font-medium text-link hover:underline", FOCUS)}>
                    Manage accounts
                  </Link>
                </>
              )}
            </p>
          </div>
        </div>
        <RangeSelect value={range} onChange={(v) => setParams((p) => { p.set("range", v); return p; }, { replace: true })} />
      </header>

      <div className="space-y-6">
        {(d.main_issue || d.recommendation) && (
          <RecommendationPanel issue={fmtIssue(d.main_issue)} recommendation={d.recommendation} />
        )}

        <KpiGrid cols={4} label="Metrics">
          <Kpi icon={ICONS.requests} label="Requests" value={m.requests.toLocaleString("en-US")} />
          <Kpi
            icon={ICONS.successRate}
            label="Success"
            value={fmtPct(m.success_rate)}
            tone={successTone}
            valueClassName={m.requests && m.success_rate < 95 ? "text-warn" : ""}
            hint={<span className={cn("tabular-nums", m.error_rate >= 5 && "text-bad")}>{fmtPct(m.error_rate)} errors</span>}
          />
          <Kpi icon={ICONS.latency} label="p95 latency" value={fmtMs(m.latency_p95_ms)} hint={<span className="tabular-nums">TTFT {fmtMs(m.ttft_p95_ms)}</span>} />
          <Kpi
            icon={ICONS.fallbacks}
            label="Fallbacks"
            value={m.fallback_count.toLocaleString("en-US")}
            tone={m.fallback_count ? "warn" : "section"}
            valueClassName={m.fallback_count ? "text-warn" : ""}
          />
        </KpiGrid>

        <HealthTrends snapshots={d.snapshots ?? []} range={range} />

        <div className="grid gap-4 lg:grid-cols-2">
          <ErrorTypeBreakdown breakdown={d.error_breakdown} />
          <section aria-labelledby="probe-title" className="self-start rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
            <div className="border-b border-line px-4 py-3">
              <SectionTitle id="probe-title" icon={Play} title="Run a probe" subtitle="Sends one test request now" />
            </div>
            <div className="p-4">
              <ManualProbeInline provider={provider} models={d.models.map((m) => m.model).filter(Boolean)} />
            </div>
          </section>
        </div>

        <section aria-labelledby="models-title">
          <div className="mb-3">
            <SectionTitle id="models-title" icon={ICONS.model} title="Models" />
          </div>
          <ModelTable query={detail} />
        </section>
      </div>
    </div>
  );
}

function BackLink() {
  return (
    <Link to="/provider-health" className={cn("mb-3 inline-flex items-center gap-1.5 rounded-md text-[13px] text-fg-muted hover:text-fg", FOCUS)}>
      <ArrowLeft className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" /> Provider health
    </Link>
  );
}

function RecommendationPanel({ issue, recommendation }: { issue: string; recommendation: string }) {
  if (!issue && !recommendation) return null;
  return (
    <section aria-label="Main issue" className="flex items-start gap-3 rounded-2xl border border-warn/30 bg-warn/5 px-4 py-3">
      <Lightbulb className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
      <div className="text-[13px]">
        {issue && <p className="font-medium text-fg">{issue}</p>}
        {recommendation && <p className="mt-0.5 text-fg-muted">{recommendation}</p>}
      </div>
    </section>
  );
}

function ManualProbeInline({ provider, models }: { provider: string; models: string[] }) {
  const [model, setModel] = useState(models[0] ?? "");
  const [running, setRunning] = useState(false);
  const qc = useQueryClient();
  const toast = useToast();
  const run = async () => {
    if (!model) return;
    setRunning(true);
    try {
      const res = await api.runHealthProbe({ provider, model });
      toast.success(res.message);
      qc.invalidateQueries({ queryKey: ["health-probes"] });
      qc.invalidateQueries({ queryKey: ["health-provider", provider] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setRunning(false);
    }
  };
  if (models.length === 0) {
    return <p className="text-[13px] text-fg-muted">No models to probe yet.</p>;
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <label htmlFor="probe-model" className="sr-only">Model to probe</label>
      <select
        id="probe-model"
        value={model}
        onChange={(e) => setModel(e.target.value)}
        className={cn("h-9 min-w-0 flex-1 rounded-lg border border-input bg-surface px-2.5 font-mono text-[12.5px] text-fg focus:border-accent-500", FOCUS)}
      >
        {models.map((m) => <option key={m} value={m}>{m}</option>)}
      </select>
      <Button type="button" onClick={run} disabled={running || !model} aria-busy={running}>
        {running ? <RefreshCw className="animate-spin" aria-hidden="true" /> : <Play aria-hidden="true" />}
        {running ? "Running…" : "Run probe"}
      </Button>
    </div>
  );
}
