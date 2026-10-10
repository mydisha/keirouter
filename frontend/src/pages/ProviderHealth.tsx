import { useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { ArrowLeft, Lightbulb, Play, RefreshCw } from "lucide-react";
import {
  api,
  type HealthStatus,
  type HealthSummary,
  type HealthProviderRow,
  type HealthProviderDetail,
  type HealthModelRow,
  type HealthChainRow,
  type HealthProbeRow,
} from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import {
  Badge,
  Card,
  EmptyState,
  ErrorCard,
  Spinner,
  TablePagination,
  useClientPagination,
} from "../components/ui";
import { HealthStatusBadge, HealthScoreRing, fmtIssue } from "../components/HealthBadge";
import {
  ErrorBreakdownChart,
  ErrorRateChart,
  FallbackChart,
  LatencyChart,
  RequestVolumeChart,
  TTFTChart,
} from "../components/HealthCharts";
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
  "15m": "live window · last 15 minutes",
  "1h": "last hour",
  "6h": "last 6 hours",
  "24h": "last 24 hours",
  "7d": "last 7 days",
  "30d": "last 30 days",
};

// Bucket counts keep each uptime strip between 15 and 36 ticks.
const TIMELINE_BUCKETS: Record<string, number> = { "15m": 15, "1h": 30, "6h": 36, "24h": 24, "7d": 28, "30d": 30 };

const STATUS_FILTERS = [
  { value: "", label: "All" },
  { value: "healthy", label: "Healthy" },
  { value: "degraded", label: "Degraded" },
  { value: "unhealthy", label: "Unhealthy" },
];

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


export function ProviderHealthPage() {
  const { provider } = useParams();
  if (provider) return <ProviderDetail provider={provider} />;
  return <Overview />;
}

// ---- Overview ---------------------------------------------------------------

type Tab = "providers" | "models" | "chains" | "probes";

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
        description={`Which upstreams are failing or slow, why, and what to do about it · ${RANGE_LABEL[range] ?? range}`}
        action={
          <>
            <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Time range">
              {RANGES.map((r) => (
                <button
                  key={r.value}
                  type="button"
                  role="radio"
                  aria-checked={range === r.value}
                  onClick={() => setParam("range", r.value)}
                  className={cn(
                    "inline-flex h-full items-center gap-1.5 rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                    range === r.value ? "bg-surface text-fg shadow-[0_0_0_1px_var(--border-strong)]" : "text-fg-muted hover:text-fg",
                    r.value !== "15m" && "font-mono",
                  )}
                >
                  {r.value === "15m" && <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />}
                  {r.label}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={() => {
                qc.invalidateQueries({ queryKey: ["health-overview"] });
                qc.invalidateQueries({ queryKey: ["health-timeline"] });
                qc.invalidateQueries({ queryKey: ["health-models"] });
                qc.invalidateQueries({ queryKey: ["health-chains"] });
              }}
              aria-label="Refresh health"
              className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />
            </button>
          </>
        }
      />

      {overview.isLoading ? (
        <Spinner />
      ) : overview.isError ? (
        <ErrorCard message="Failed to load provider health." />
      ) : overview.data ? (
        <div className="space-y-5">
          <SummaryStrip summary={overview.data.summary} rows={overview.data.providers} />

          <div className="flex flex-wrap items-end justify-between gap-3 border-b border-line">
            <div className="-mb-px flex gap-1" role="tablist" aria-label="Health views">
              {(["providers", "models", "chains", "probes"] as Tab[]).map((t) => (
                <button
                  key={t}
                  type="button"
                  role="tab"
                  aria-selected={tab === t}
                  onClick={() => setParam("tab", t === "providers" ? "" : t)}
                  className={cn(
                    "relative px-3 py-2.5 text-[13px] font-medium capitalize transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                    tab === t ? "text-fg" : "text-fg-muted hover:text-fg",
                  )}
                >
                  {t}
                  {tab === t && <span className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-500" />}
                </button>
              ))}
            </div>
            {(tab === "providers" || tab === "models") && (
              <div className="mb-2 flex gap-1" role="radiogroup" aria-label="Filter by status">
                {STATUS_FILTERS.map((f) => (
                  <button
                    key={f.value}
                    type="button"
                    role="radio"
                    aria-checked={status === f.value}
                    onClick={() => setParam("status", f.value)}
                    className={cn(
                      "h-7 rounded-lg border px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                      status === f.value ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:text-fg",
                    )}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
            )}
          </div>

          {tab === "providers" && <ProviderTable rows={overview.data.providers} meta={meta} strips={strips} range={range} />}
          {tab === "models" && <ModelTable query={models} />}
          {tab === "chains" && <ChainTable query={chains} />}
          {tab === "probes" && <ProbeHistoryTable range={range} />}
        </div>
      ) : null}
    </div>
  );
}

function SummaryStrip({ summary, rows }: { summary: HealthSummary; rows: HealthProviderRow[] }) {
  const total = summary.healthy + summary.degraded + summary.unhealthy + summary.unknown + summary.disabled;
  const requests = rows.reduce((n, r) => n + (r.requests ?? 0), 0);
  const cells: { label: string; value: string; tone?: "warn" | "bad"; hint?: string }[] = [
    { label: "Providers", value: String(total), hint: total ? `${summary.healthy} healthy` : "No telemetry yet" },
    { label: "Degraded", value: String(summary.degraded), tone: summary.degraded ? "warn" : undefined, hint: "Slower or partly failing" },
    { label: "Unhealthy", value: String(summary.unhealthy), tone: summary.unhealthy ? "bad" : undefined, hint: "Avoid until it recovers" },
    { label: "Fallbacks", value: summary.fallbacks.toLocaleString("en-US"), tone: summary.fallbacks ? "warn" : undefined, hint: requests ? `${requests.toLocaleString("en-US")} requests observed` : "Requests that failed over" },
    { label: "Avg p95 latency", value: fmtMs(summary.avg_p95_latency_ms), hint: "Mean of each provider's worst p95" },
  ];
  return (
    <section aria-label="Health summary" className="grid grid-cols-1 gap-px overflow-hidden rounded-2xl border border-line bg-line shadow-[var(--shadow-card)] sm:grid-cols-2 xl:grid-cols-5">
      {cells.map((c) => (
        <div key={c.label} className="flex flex-col gap-1 bg-surface px-4 py-3 sm:last:col-span-2 xl:last:col-span-1">
          <span className="text-[12px] font-medium text-fg-muted">{c.label}</span>
          <span className={cn("text-[22px] font-semibold leading-tight tracking-[-0.02em] tabular-nums", c.tone === "bad" ? "text-bad" : c.tone === "warn" ? "text-warn" : "text-fg")}>
            {c.value}
          </span>
          {c.hint && <span className="text-[12px] text-fg-faint">{c.hint}</span>}
        </div>
      ))}
    </section>
  );
}

const TICK_CLASS: Record<string, string> = { ok: "bg-ok/70", degraded: "bg-warn", down: "bg-bad", idle: "bg-track" };

function ProviderTable({
  rows,
  meta,
  strips,
  range,
}: {
  rows: HealthProviderRow[];
  meta: Map<string, import("../lib/api").Provider>;
  strips: Map<string, import("../lib/api").HealthTimelineProvider>;
  range: string;
}) {
  const navigate = useNavigate();
  const { page, pages, paged, setPage, total } = useClientPagination(rows, 15);
  if (rows.length === 0) {
    return (
      <Card>
        <EmptyState title="No provider telemetry in this range" hint="Health appears once requests flow through a provider, or after you run a probe from a provider's page." />
      </Card>
    );
  }
  return (
    <Card>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px] text-[13px]">
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th className="px-4 py-2 font-medium">Provider</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Uptime · {range === "15m" ? "live" : range}</th>
              <th className="px-4 py-2 text-right font-medium">Requests</th>
              <th className="px-4 py-2 text-right font-medium">Success</th>
              <th className="px-4 py-2 text-right font-medium">p95</th>
              <th className="px-4 py-2 text-right font-medium">Fallbacks</th>
              <th className="px-4 py-2 font-medium">Main issue</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {paged.map((r) => {
              const p = meta.get(r.provider);
              const strip = strips.get(r.provider);
              const name = p?.display_name ?? r.provider;
              return (
                <tr
                  key={r.provider}
                  className="cursor-pointer transition-colors hover:bg-hover"
                  onClick={() => navigate(`/provider-health/${encodeURIComponent(r.provider)}`)}
                >
                  <td className="px-4 py-2.5">
                    <Link
                      to={`/provider-health/${encodeURIComponent(r.provider)}`}
                      onClick={(e) => e.stopPropagation()}
                      className="flex items-center gap-2.5 focus:outline-none focus-visible:underline"
                    >
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
                      <div className="flex w-40 gap-[2px]" role="img" aria-label={`${name} status over the selected range`}>
                        {strip.buckets.map((b) => (
                          <span key={b.start} className={cn("h-3.5 min-w-[2px] flex-1 rounded-[1.5px]", TICK_CLASS[b.status] ?? "bg-track")} title={`${fmtTime(b.start)} · ${b.status}${b.requests ? ` · ${b.requests} req` : ""}`} />
                        ))}
                      </div>
                    ) : (
                      <span className="text-[12px] text-fg-faint">—</span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 text-right tabular-nums text-fg">{r.requests != null ? r.requests.toLocaleString("en-US") : "—"}</td>
                  <td className={cn("whitespace-nowrap px-4 py-2.5 text-right tabular-nums", r.success_rate >= 99 ? "text-fg" : r.success_rate >= 95 ? "text-warn" : "text-bad")}>{fmtPct(r.success_rate)}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtMs(r.latency_p95_ms)}</td>
                  <td className={cn("px-4 py-2.5 text-right tabular-nums", r.fallback_count ? "text-warn" : "text-fg-muted")}>{r.fallback_count.toLocaleString("en-US")}</td>
                  <td className="max-w-[260px] px-4 py-2.5">
                    {r.main_issue ? (
                      <span className="block truncate text-fg-muted" title={r.recommendation || undefined}>
                        {fmtIssue(r.main_issue)}
                        {r.recommendation && <span className="text-fg-faint"> — {r.recommendation}</span>}
                      </span>
                    ) : (
                      <span className="text-fg-faint">—</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </Card>
  );
}

function ModelTable({ query }: { query: UseQueryResult<{ models: HealthModelRow[] } | HealthProviderDetail> }) {
  if (query.isLoading) return <Spinner />;
  if (query.isError) return <ErrorCard message="Failed to load model health." />;
  const rows = (query.data as { models?: HealthModelRow[] } | undefined)?.models ?? [];
  if (rows.length === 0) return <Card><EmptyState title="No model health data yet." /></Card>;
  return <ModelTableInner rows={rows} />;
}

function ModelTableInner({ rows }: { rows: HealthModelRow[] }) {
  const { page, pages, paged, setPage, total } = useClientPagination(rows, 10);
  return (
    <Card>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-left text-xs text-[var(--text-muted)]">
              <th className="px-4 py-2 font-medium">Provider / Model</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Score</th>
              <th className="px-4 py-2 font-medium">Success</th>
              <th className="px-4 py-2 font-medium">p95</th>
              <th className="px-4 py-2 font-medium">Fallbacks</th>
              <th className="px-4 py-2 font-medium">Main Issue</th>
            </tr>
          </thead>
          <tbody>
            {paged.map((r: HealthModelRow, i: number) => (
              <tr key={`${r.provider}/${r.model}-${i}`} className="border-b border-[var(--border)] last:border-0 hover:bg-[var(--bg-subtle)]">
                <td className="px-4 py-2.5 font-medium">{r.provider}/{r.model}</td>
                <td className="px-4 py-2.5"><HealthStatusBadge status={r.status as HealthStatus} issue={r.main_issue} /></td>
                <td className="px-4 py-2.5"><HealthScoreRing score={r.score} /></td>
                <td className="px-4 py-2.5 tabular-nums">{fmtPct(r.success_rate)}</td>
                <td className="px-4 py-2.5 tabular-nums">{fmtMs(r.latency_p95_ms)}</td>
                <td className="px-4 py-2.5 tabular-nums">{r.fallback_count}</td>
                <td className="px-4 py-2.5 text-[var(--text-muted)]">{fmtIssue(r.main_issue) || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </Card>
  );
}

function ChainTable({ query }: { query: UseQueryResult<{ chains: HealthChainRow[] }> }) {
  const [selected, setSelected] = useState<string | null>(null);
  if (query.isLoading) return <Spinner />;
  if (query.isError) return <ErrorCard message="Failed to load chain impact." />;
  const rows = query.data?.chains ?? [];
  if (rows.length === 0) return <Card><EmptyState title="No chains configured." /></Card>;
  return (
    <div className="space-y-4">
      <ChainTableInner rows={rows} onSelect={setSelected} />
      {selected && <ChainDetail id={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}

function ChainTableInner({ rows, onSelect }: { rows: HealthChainRow[]; onSelect: (id: string) => void }) {
  const { page, pages, paged, setPage, total } = useClientPagination(rows, 10);
  return (
    <Card>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-left text-xs text-[var(--text-muted)]">
              <th className="px-4 py-2 font-medium">Chain</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Requests</th>
              <th className="px-4 py-2 font-medium">Fallback Rate</th>
              <th className="px-4 py-2 font-medium">Final Failures</th>
              <th className="px-4 py-2 font-medium">Affected</th>
              <th className="px-4 py-2 font-medium">Action</th>
            </tr>
          </thead>
          <tbody>
            {paged.map((r) => (
              <tr key={r.chain_id} className="border-b border-[var(--border)] last:border-0 hover:bg-[var(--bg-subtle)] cursor-pointer" onClick={() => onSelect(r.chain_id)}>
                <td className="px-4 py-2.5 font-medium">{r.name}</td>
                <td className="px-4 py-2.5"><HealthStatusBadge status={r.status} issue={r.main_issue} /></td>
                <td className="px-4 py-2.5 tabular-nums">{r.requests.toLocaleString()}</td>
                <td className="px-4 py-2.5 tabular-nums">{fmtPct(r.fallback_rate)}</td>
                <td className="px-4 py-2.5 tabular-nums">
                  {r.final_failure_count > 0 ? <span className="text-[color:var(--color-danger)] font-medium">{r.final_failure_count}</span> : "0"}
                </td>
                <td className="px-4 py-2.5 text-[var(--text-muted)]">{r.affected_provider ? `${r.affected_provider}/${r.affected_model}` : "—"}</td>
                <td className="px-4 py-2.5 text-xs text-accent-600 dark:text-accent-300">View →</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </Card>
  );
}

function ChainDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const q = useQuery({
    queryKey: ["health-chain", id],
    queryFn: () => api.healthChainDetail(id),
    staleTime: 15_000,
  });
  return (
    <Card>
      <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3">
        <h2 className="text-sm font-semibold">Chain Detail</h2>
        <button onClick={onClose} className="text-xs text-[var(--text-muted)] hover:text-[var(--text)]">Close</button>
      </div>
      {q.isLoading ? <Spinner /> : q.isError ? <EmptyState title="Failed to load chain detail." /> : q.data ? (
        <div className="px-4 py-3">
          <div className="mb-3 flex flex-wrap items-center gap-3 text-sm">
            <span className="font-medium">{q.data.name}</span>
            <Badge tone="neutral">{q.data.strategy}</Badge>
            {q.data.requests != null && <span className="text-[var(--text-muted)]">{q.data.requests.toLocaleString()} requests</span>}
            {q.data.fallback_rate != null && <span className="text-[var(--text-muted)]">{fmtPct(q.data.fallback_rate)} fallback rate</span>}
            {q.data.final_failure_count != null && q.data.final_failure_count > 0 && (
              <span className="text-[color:var(--color-danger)]">{q.data.final_failure_count} final failures</span>
            )}
          </div>
          <div className="space-y-2">
            {q.data.steps.map((step) => (
              <div key={step.position} className="flex items-center gap-3 rounded-lg border border-[var(--border)] px-3 py-2">
                <span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-[var(--bg-subtle)] text-xs font-medium">{step.position + 1}</span>
                <div className="flex-1">
                  <div className="text-sm font-medium">{step.provider}/{step.model}</div>
                  {step.main_issue && <div className="text-xs text-[var(--text-muted)]">{fmtIssue(step.main_issue)}</div>}
                </div>
                <HealthStatusBadge status={step.status} />
                <HealthScoreRing score={step.score} size={36} />
              </div>
            ))}
            {q.data.fallback_provider && (
              <div className="flex items-center gap-3 rounded-lg border border-dashed border-[var(--border)] px-3 py-2">
                <span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-[var(--bg-subtle)] text-xs">★</span>
                <div className="flex-1 text-sm text-[var(--text-muted)]">
                  Fallback: {q.data.fallback_provider}/{q.data.fallback_model}
                </div>
              </div>
            )}
          </div>
        </div>
      ) : null}
    </Card>
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
  if (q.isLoading) return <Spinner />;
  if (q.isError) return <ErrorCard message="Failed to load probe history." />;
  const rows = q.data?.items ?? [];
  const total = q.data?.pagination.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / 50));
  if (rows.length === 0) return <Card><EmptyState title="No probes yet." hint="Run a manual probe to see results here." /></Card>;
  return (
    <Card>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-left text-xs text-[var(--text-muted)]">
              <th className="px-4 py-2 font-medium">Time</th>
              <th className="px-4 py-2 font-medium">Provider / Model</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Latency</th>
              <th className="px-4 py-2 font-medium">HTTP</th>
              <th className="px-4 py-2 font-medium">Error</th>
              <th className="px-4 py-2 font-medium">Trigger</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r: HealthProbeRow, i) => (
              <tr key={i} className="border-b border-[var(--border)] last:border-0">
                <td className="px-4 py-2.5 text-[var(--text-muted)]">{fmtTime(r.time)}</td>
                <td className="px-4 py-2.5 font-medium">{r.provider}/{r.model}</td>
                <td className="px-4 py-2.5">
                  <Badge tone={r.status === "success" ? "success" : "danger"}>{r.status}</Badge>
                </td>
                <td className="px-4 py-2.5 tabular-nums">{fmtMs(r.latency_ms)}</td>
                <td className="px-4 py-2.5 tabular-nums">{r.http_status ?? "—"}</td>
                <td className="px-4 py-2.5 text-[var(--text-muted)]">{fmtIssue(r.error_type) || "—"}</td>
                <td className="px-4 py-2.5"><Badge tone="neutral">{r.triggered_by}</Badge></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </Card>
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

  if (detail.isLoading) return <Spinner />;
  if (detail.isError) return (
    <div>
      <BackLink />
      <ErrorCard message="Failed to load provider detail." />
    </div>
  );
  const d = detail.data;
  if (!d) return null;

  const meta = providers.data?.providers.find((p) => p.id === d.provider);
  const name = meta?.display_name ?? d.provider;
  return (
    <div>
      <nav aria-label="Breadcrumb" className="mb-3 flex items-center gap-1.5 text-[13px] text-fg-muted">
        <Link to={`/provider-health?range=${range}`} className="inline-flex items-center gap-1.5 hover:text-fg">
          <ArrowLeft className="h-3.5 w-3.5" />
          Provider health
        </Link>
        <span aria-hidden="true" className="text-fg-faint">/</span>
        <span className="text-fg">{name}</span>
      </nav>
      <header className="mb-4 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-center gap-3">
          <ProviderLogo icon={meta?.icon} name={name} size={40} className="rounded-lg" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{name}</h1>
              <HealthStatusBadge status={d.status as HealthStatus} issue={d.main_issue} />
            </div>
            <p className="mt-0.5 flex flex-wrap items-center gap-2 text-[13px] text-fg-muted">
              <span className="font-mono text-[12.5px]">{d.provider}</span>
              <span aria-hidden="true" className="text-fg-faint">·</span>
              <span className="inline-flex items-center gap-1.5">Health score <HealthScoreRing score={d.score} /></span>
              {meta && (
                <>
                  <span aria-hidden="true" className="text-fg-faint">·</span>
                  <Link to={`/providers/${d.provider}`} className="font-medium text-accent-500 hover:underline dark:text-accent-400">
                    Manage accounts
                  </Link>
                </>
              )}
            </p>
          </div>
        </div>
        <div className="inline-flex h-8 items-center rounded-xl border border-line bg-subtle p-0.5" role="radiogroup" aria-label="Time range">
          {RANGES.map((r) => (
            <button
              key={r.value}
              type="button"
              role="radio"
              aria-checked={range === r.value}
              onClick={() => setParams((p) => { p.set("range", r.value); return p; }, { replace: true })}
              className={cn(
                "h-full rounded-lg px-2.5 text-[12px] font-medium",
                range === r.value ? "bg-surface text-fg shadow-[0_0_0_1px_var(--border-strong)]" : "text-fg-muted hover:text-fg",
                r.value !== "15m" && "font-mono",
              )}
            >
              {r.label}
            </button>
          ))}
        </div>
      </header>

      <section aria-label="Metrics" className="mb-4 grid grid-cols-2 gap-px overflow-hidden rounded-2xl border border-line bg-line shadow-[var(--shadow-card)] sm:grid-cols-3 xl:grid-cols-6">
        {[
          { label: "Requests", value: d.metrics.requests.toLocaleString("en-US") },
          { label: "Success", value: fmtPct(d.metrics.success_rate), tone: d.metrics.requests && d.metrics.success_rate < 95 ? "text-warn" : "" },
          { label: "Errors", value: fmtPct(d.metrics.error_rate), tone: d.metrics.error_rate >= 5 ? "text-bad" : "" },
          { label: "p95 latency", value: fmtMs(d.metrics.latency_p95_ms) },
          { label: "p95 TTFT", value: fmtMs(d.metrics.ttft_p95_ms) },
          { label: "Fallbacks", value: d.metrics.fallback_count.toLocaleString("en-US"), tone: d.metrics.fallback_count ? "text-warn" : "" },
        ].map((m) => (
          <div key={m.label} className="bg-surface px-4 py-3">
            <p className="text-[12px] font-medium text-fg-muted">{m.label}</p>
            <p className={cn("mt-1 text-[18px] font-semibold tracking-[-0.01em] tabular-nums text-fg", m.tone)}>{m.value}</p>
          </div>
        ))}
      </section>

      {(d.main_issue || d.recommendation) && (
        <RecommendationPanel issue={fmtIssue(d.main_issue)} recommendation={d.recommendation} />
      )}


      <TrendCharts snapshots={d.snapshots ?? []} />

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card>
          <div className="border-b border-[var(--border)] px-4 py-3">
            <h2 className="text-sm font-semibold">Error Breakdown</h2>
          </div>
          {Object.keys(d.error_breakdown).length === 0 ? (
            <EmptyState title="No errors in this window." />
          ) : (
            <div className="px-4 py-3 space-y-2">
              {Object.entries(d.error_breakdown)
                .sort((a, b) => b[1] - a[1])
                .map(([k, v]) => (
                  <div key={k} className="flex items-center justify-between text-sm">
                    <span className="text-[var(--text-muted)]">{fmtIssue(k)}</span>
                    <span className="tabular-nums font-medium">{v}</span>
                  </div>
                ))}
            </div>
          )}
        </Card>
        <Card>
          <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3">
            <h2 className="text-sm font-semibold">Manual Probe</h2>
            <ManualProbeInline provider={provider} models={d.models.map((m) => m.model).filter(Boolean)} />
          </div>
          <div className="px-4 py-3 text-sm text-[var(--text-muted)]">
            Run a synthetic probe to test this provider now. Result appears in probe history.
          </div>
        </Card>
      </div>

      <div className="mt-4">
        <h2 className="mb-2 text-sm font-semibold">Models</h2>
        <ModelTable query={detail} />
      </div>
    </div>
  );
}

function TrendCharts({ snapshots }: { snapshots: import("../lib/api").HealthSnapshot[] }) {
  if (!snapshots.length) {
    return (
      <Card className="mt-4">
        <EmptyState title="No trend data yet." hint="Snapshots appear after traffic flows through this provider." />
      </Card>
    );
  }
  return (
    <div className="mt-4 grid gap-4 lg:grid-cols-2">
      <ChartCard title="Request Volume">
        <RequestVolumeChart data={snapshots} />
      </ChartCard>
      <ChartCard title="Error Rate">
        <ErrorRateChart data={snapshots} />
      </ChartCard>
      <ChartCard title="Latency p50 / p95 / p99">
        <LatencyChart data={snapshots} />
      </ChartCard>
      <ChartCard title="TTFT p95">
        <TTFTChart data={snapshots} />
      </ChartCard>
      <ChartCard title="Fallbacks">
        <FallbackChart data={snapshots} />
      </ChartCard>
      <ChartCard title="Error Type Breakdown">
        <ErrorBreakdownChart data={snapshots} />
      </ChartCard>
    </div>
  );
}

function ChartCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card>
      <div className="border-b border-[var(--border)] px-4 py-3">
        <h2 className="text-sm font-semibold">{title}</h2>
      </div>
      <div className="h-56 p-3">{children}</div>
    </Card>
  );
}

function BackLink() {
  return (
    <a href="/provider-health" className="inline-flex items-center gap-1 text-sm text-[var(--text-muted)] hover:text-[var(--text)]">
      <ArrowLeft className="h-4 w-4" /> Back
    </a>
  );
}

function RecommendationPanel({ issue, recommendation }: { issue: string; recommendation: string }) {
  if (!issue && !recommendation) return null;
  return (
    <Card className="mt-4 border-[color:var(--color-warning)]/30 bg-[color:var(--color-warning)]/5">
      <div className="flex items-start gap-3 px-4 py-3.5">
        <Lightbulb className="mt-0.5 h-4 w-4 shrink-0 text-[color:var(--color-warning)]" />
        <div className="text-sm">
          {issue && <p className="font-medium">Main issue: <span className="text-[var(--text-muted)]">{fmtIssue(issue)}</span></p>}
          {recommendation && <p className="mt-1 text-[var(--text-muted)]">{recommendation}</p>}
        </div>
      </div>
    </Card>
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
  return (
    <div className="flex items-center gap-2">
      <select value={model} onChange={(e) => setModel(e.target.value)} className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-1 text-xs">
        {models.map((m) => <option key={m} value={m}>{m}</option>)}
      </select>
      <button
        onClick={run}
        disabled={running || !model}
        className="inline-flex items-center gap-1.5 rounded-lg bg-accent-600 px-2.5 py-1.5 text-xs font-medium text-white hover:bg-accent-700 disabled:opacity-50"
      >
        {running ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
        Run Probe
      </button>
    </div>
  );
}
