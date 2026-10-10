import { useState, type ReactNode } from "react";
import { useParams, useSearchParams } from "react-router-dom";
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

const RANGES = [
  { value: "5m", label: "5m" },
  { value: "15m", label: "15m" },
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

const STATUS_FILTERS = [
  { value: "", label: "All" },
  { value: "healthy", label: "Healthy" },
  { value: "degraded", label: "Degraded" },
  { value: "unhealthy", label: "Unhealthy" },
  { value: "unknown", label: "Unknown" },
];

function fmtMs(ms?: number) {
  if (ms == null) return "—";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
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
  const range = params.get("range") ?? "1h";
  const status = params.get("status") ?? "";
  const [tab, setTab] = useState<Tab>("providers");

  const setRange = (v: string) => setParams((p) => { p.set("range", v); return p; }, { replace: true });
  const setStatus = (v: string) => setParams((p) => { if (v) p.set("status", v); else p.delete("status"); return p; }, { replace: true });

  const overview = useQuery({
    queryKey: ["health-overview", range, status],
    queryFn: () => api.healthOverview(range, status || undefined),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });

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

  return (
    <div>
      <PageHeader
        title="Provider Health"
        description="Monitor the health of every AI provider connected to KeiRouter. See which are failing or slow, why, which routing chains are affected, and what to do next."
        action={
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedControl value={range} onChange={setRange} options={RANGES} />
            <button
              onClick={() => overview.refetch()}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2.5 py-2 text-xs font-medium hover:bg-[var(--bg-subtle)]"
            >
              <RefreshCw className="h-3.5 w-3.5" /> Refresh
            </button>
          </div>
        }
      />

      {overview.isLoading ? (
        <Spinner />
      ) : overview.isError ? (
        <ErrorCard message="Failed to load provider health." />
      ) : overview.data ? (
        <>
          <SummaryCards summary={overview.data.summary} />
          <div className="mt-5">
            <SegmentedControl
              value={tab}
              onChange={(v) => setTab(v as Tab)}
              options={[
                { value: "providers", label: "Providers" },
                { value: "models", label: "Models" },
                { value: "chains", label: "Chains" },
                { value: "probes", label: "Probes" },
              ]}
            />
          </div>
          <div className="mt-4">
            {tab === "providers" && (
              <ProviderTable rows={overview.data.providers} status={status} onStatus={setStatus} />
            )}
            {tab === "models" && <ModelTable query={models} />}
            {tab === "chains" && <ChainTable query={chains} />}
            {tab === "probes" && <ProbeHistoryTable range={range} />}
          </div>
        </>
      ) : null}
    </div>
  );
}

function SummaryCards({ summary }: { summary: HealthSummary }) {
  const total = summary.healthy + summary.degraded + summary.unhealthy + summary.unknown + summary.disabled;
  const segments = [
    { key: "healthy", label: "Healthy", count: summary.healthy, color: "var(--color-accent-500)" },
    { key: "degraded", label: "Degraded", count: summary.degraded, color: "var(--color-warning)" },
    { key: "unhealthy", label: "Unhealthy", count: summary.unhealthy, color: "var(--color-danger)" },
    { key: "unknown", label: "Unknown", count: summary.unknown, color: "var(--color-ink-400)" },
    { key: "disabled", label: "Disabled", count: summary.disabled, color: "var(--color-ink-300)" },
  ].filter((s) => s.count > 0);

  return (
    <Card className="p-4">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        {/* Distribution bar + legend */}
        <div className="min-w-0 flex-1">
          <div className="mb-2 flex items-baseline justify-between">
            <span className="text-xs font-medium text-[var(--text-muted)]">
              Provider Health
            </span>
            <span className="text-sm font-semibold tabular-nums">{total} total</span>
          </div>
          {total === 0 ? (
            <div className="h-2.5 rounded-full bg-[var(--bg-subtle)]" />
          ) : (
            <div className="flex h-2.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
              {segments.map((s) => (
                <div
                  key={s.key}
                  style={{ width: `${(s.count / total) * 100}%`, backgroundColor: s.color }}
                  title={`${s.label}: ${s.count}`}
                />
              ))}
            </div>
          )}
          <div className="mt-2.5 flex flex-wrap gap-x-4 gap-y-1">
            {segments.map((s) => (
              <div key={s.key} className="flex items-center gap-1.5 text-xs">
                <span className="inline-block h-2 w-2 rounded-full" style={{ backgroundColor: s.color }} />
                <span className="text-[var(--text-muted)]">{s.label}</span>
                <span className="font-semibold tabular-nums">{s.count}</span>
              </div>
            ))}
          </div>
          <p className="mt-2 text-xs text-[var(--text-muted)]">
            Share of providers by current health status. Green is working normally; yellow is slower or less reliable; red should be avoided.
          </p>
        </div>

        {/* Compact key metrics */}
        <div className="flex shrink-0 gap-5 border-t border-[var(--border)] pt-3 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0">
          <CompactStat label="Fallbacks" value={summary.fallbacks.toLocaleString()} tone={summary.fallbacks > 0 ? "warning" : "muted"} />
          <CompactStat label="Avg p95" value={fmtMs(summary.avg_p95_latency_ms)} tone="muted" />
        </div>
      </div>
    </Card>
  );
}

function CompactStat({ label, value, tone = "muted" }: { label: string; value: string; tone?: "muted" | "warning" | "danger" }) {
  const color = tone === "warning" ? "text-[color:var(--color-warning)]" : tone === "danger" ? "text-[color:var(--color-danger)]" : "text-[var(--text)]";
  return (
    <div>
      <div className="text-[11.5px] font-medium text-[var(--text-muted)]">{label}</div>
      <div className={`mt-0.5 text-lg font-semibold tabular-nums ${color}`}>{value}</div>
    </div>
  );
}

function MetricPill({ label, value, tone = "muted" }: { label: string; value: string; tone?: "muted" | "good" | "warn" | "bad" }) {
  const color =
    tone === "good" ? "text-[color:var(--color-accent-500)]" :
    tone === "warn" ? "text-[color:var(--color-warning)]" :
    tone === "bad" ? "text-[color:var(--color-danger)]" :
    "text-[var(--text)]";
  return (
    <div className="flex items-baseline gap-1.5">
      <span className="text-[12px] font-medium text-[var(--text-muted)]">{label}</span>
      <span className={`text-sm font-semibold tabular-nums ${color}`}>{value}</span>
    </div>
  );
}

function ProviderTable({
  rows,
  status,
  onStatus,
}: {
  rows: HealthProviderRow[];
  status: string;
  onStatus: (v: string) => void;
}) {
  const { page, pages, paged, setPage, total } = useClientPagination(rows, 10);
  return (
    <Card>
      <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3">
        <h2 className="text-sm font-semibold">Provider Status</h2>
        <select
          value={status}
          onChange={(e) => onStatus(e.target.value)}
          className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-1 text-xs"
        >
          {STATUS_FILTERS.map((f) => (
            <option key={f.value} value={f.value}>{f.label}</option>
          ))}
        </select>
      </div>
      {rows.length === 0 ? (
        <EmptyState title="No health data yet." hint="Run a probe or send traffic through a provider." />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[var(--border)] text-left text-xs text-[var(--text-muted)]">
                  <th className="px-4 py-2 font-medium">Provider</th>
                  <th className="px-4 py-2 font-medium">Status</th>
                  <th className="px-4 py-2 font-medium">Score</th>
                  <th className="px-4 py-2 font-medium">Success</th>
                  <th className="px-4 py-2 font-medium">Errors</th>
                  <th className="px-4 py-2 font-medium">p95</th>
                  <th className="px-4 py-2 font-medium">Fallbacks</th>
                  <th className="px-4 py-2 font-medium">Last Probe</th>
                  <th className="px-4 py-2 font-medium">Main Issue</th>
                </tr>
              </thead>
              <tbody>
                {paged.map((r) => (
                  <tr key={r.provider} className="border-b border-[var(--border)] last:border-0 hover:bg-[var(--bg-subtle)]">
                    <td className="px-4 py-2.5 font-medium">{r.provider}</td>
                    <td className="px-4 py-2.5"><HealthStatusBadge status={r.status as HealthStatus} issue={r.main_issue} /></td>
                    <td className="px-4 py-2.5"><HealthScoreRing score={r.score} /></td>
                    <td className="px-4 py-2.5 tabular-nums">{fmtPct(r.success_rate)}</td>
                    <td className="px-4 py-2.5 tabular-nums">{fmtPct(r.error_rate)}</td>
                    <td className="px-4 py-2.5 tabular-nums">{fmtMs(r.latency_p95_ms)}</td>
                    <td className="px-4 py-2.5 tabular-nums">{r.fallback_count}</td>
                    <td className="px-4 py-2.5 text-[var(--text-muted)]">{fmtTime(r.last_probe_at)}</td>
                    <td className="px-4 py-2.5 text-[var(--text-muted)]">{fmtIssue(r.main_issue) || "—"}</td>
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

  return (
    <div>
      <Card className="mb-4 p-4">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex items-center gap-4">
            <BackLink />
            <HealthScoreRing score={d.score} size={56} />
            <div>
              <h1 className="font-display text-2xl font-semibold tracking-tight">{d.provider}</h1>
              <div className="mt-1 flex items-center gap-2">
              <HealthStatusBadge status={d.status as HealthStatus} issue={d.main_issue} />
              {d.main_issue && <span className="text-xs text-[var(--text-muted)]">{fmtIssue(d.main_issue)}</span>}
              </div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <SegmentedControl value={range} onChange={(v) => setParams((p) => { p.set("range", v); return p; }, { replace: true })} options={RANGES} />
          </div>
        </div>

        {/* Compact inline metric strip — replaces the old 6-card grid */}
        <div className="mt-4 flex flex-wrap items-center gap-x-6 gap-y-3 border-t border-[var(--border)] pt-3">
          <MetricPill label="Requests" value={d.metrics.requests.toLocaleString()} />
          <MetricPill label="Success" value={fmtPct(d.metrics.success_rate)} tone={d.metrics.success_rate >= 95 ? "good" : "warn"} />
          <MetricPill label="Errors" value={fmtPct(d.metrics.error_rate)} tone={d.metrics.error_rate >= 5 ? "bad" : "muted"} />
          <MetricPill label="p95" value={fmtMs(d.metrics.latency_p95_ms)} />
          <MetricPill label="TTFT" value={fmtMs(d.metrics.ttft_p95_ms)} />
          <MetricPill label="Fallbacks" value={d.metrics.fallback_count.toLocaleString()} tone={d.metrics.fallback_count > 0 ? "warn" : "muted"} />
        </div>
      </Card>

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
