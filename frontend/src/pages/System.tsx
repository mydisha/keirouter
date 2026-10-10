import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, ChevronRight, RefreshCw, Workflow } from "lucide-react";
import { api, type SystemSample } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { Badge, ErrorCard, Kpi, KpiGrid, SectionTitle, Skeleton } from "../components/ui";
import { ChartCard, TimeLines } from "../components/charts/TimeSeries";
import { ICONS } from "../lib/icons";

// System shows what this KeiRouter instance and its host are using, refreshed
// every 5 seconds from /api/system and /api/system/history. The history is a
// rolling in-memory ring kept by the gateway, so it starts empty after a
// restart and fills as samples arrive.

const CPU_WARN = 80;
const MEM_WARN = 85;

// Chart series colours are CSS custom properties handed to the SVG chart kit
// (accent = primary data series, as in HealthCharts).
const SERIES_MAIN = "var(--color-accent-500)";
const SERIES_SOFT = "var(--color-accent-300)";
const SERIES_ALT = "var(--series-alt)";

const FOCUS = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

export function SystemPage() {
  const snap = useQuery({ queryKey: ["system"], queryFn: () => api.systemMonitor(), refetchInterval: 5000 });
  const history = useQuery({ queryKey: ["system-history"], queryFn: () => api.systemHistory(), refetchInterval: 5000 });
  const refresh = () => {
    snap.refetch();
    history.refetch();
  };

  const header = (
    <PageHeader
      title="System"
      description="This instance and its host, refreshed every 5 seconds."
      action={
        <>
          <span role="status" className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] font-medium text-fg-muted">
            <span className={cn("h-1.5 w-1.5 rounded-full", snap.isError ? "bg-bad" : "live-dot bg-ok")} aria-hidden="true" />
            {snap.isError ? "Unreachable" : "Live"}
          </span>
          <button
            type="button"
            onClick={refresh}
            aria-label="Refresh now"
            className={cn("inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS)}
          >
            <RefreshCw className={cn("h-4 w-4", (snap.isFetching || history.isFetching) && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
          </button>
        </>
      }
    />
  );

  if (snap.isLoading) {
    return (
      <>
        {header}
        <div className="space-y-6" aria-busy="true">
          <span className="sr-only" role="status">Loading system metrics</span>
          <Skeleton className="h-[92px] w-full rounded-2xl" />
          <div className="grid gap-4 lg:grid-cols-2">
            <Skeleton className="h-[260px] rounded-2xl" />
            <Skeleton className="h-[260px] rounded-2xl" />
          </div>
        </div>
      </>
    );
  }
  if (snap.isError || !snap.data) {
    return (
      <>
        {header}
        <ErrorCard message={(snap.error as Error)?.message || "Couldn't read system metrics. Check that the gateway is running."} />
      </>
    );
  }

  const s = snap.data;
  const samples = history.data?.samples ?? [];
  const spikes = history.data?.spikes ?? [];
  const interval = history.data?.interval_sec ?? 5;
  const windowText = samples.length > 1 ? `Last ${fmtDuration((samples[samples.length - 1].ts - samples[0].ts) || interval * samples.length)}` : "Collecting samples…";
  const x = (p: SystemSample) => p.ts * 1000;
  const spanS = samples.length > 1 ? samples[samples.length - 1].ts - samples[0].ts : 0;
  // Short windows need seconds on the axis or every label reads the same minute.
  const xFormat = spanS < 600 ? fmtClockSeconds : (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });

  // Four vitals: host load leads, KeiRouter's own share is the context line.
  const vitals: { label: string; icon: LucideIcon; value: string; pct: number; warn: number; hint: string }[] = [
    { label: "CPU", icon: ICONS.system, value: `${s.cpu_pct.toFixed(1)}%`, pct: s.cpu_pct, warn: CPU_WARN, hint: `KeiRouter ${s.proc_cpu_pct.toFixed(1)}% · ${s.cpu_per_core.length} cores` },
    { label: "Memory", icon: ICONS.memory, value: `${s.mem_pct.toFixed(1)}%`, pct: s.mem_pct, warn: MEM_WARN, hint: `${fmtMB(s.mem_used_mb)} of ${fmtMB(s.mem_total_mb)} · KeiRouter ${fmtMB(s.proc_rss_mb)}` },
    { label: "Disk", icon: ICONS.disk, value: `${s.disk_pct.toFixed(1)}%`, pct: s.disk_pct, warn: 90, hint: `${s.disk_free_gb.toFixed(1)} GB free` },
    { label: "Uptime", icon: ICONS.uptime, value: fmtDuration(s.uptime_s), pct: -1, warn: 0, hint: `${s.proc_threads} threads` },
  ];

  return (
    <>
      {header}
      <div className="space-y-6">
        <KpiGrid cols={4} label="Current usage">
          {vitals.map((g) => (
            <Kpi
              key={g.label}
              icon={g.icon}
              label={g.label}
              tone={g.pct >= 0 ? toneTile(g.pct, g.warn) : "section"}
              valueClassName={g.pct >= 0 ? toneText(g.pct, g.warn) : ""}
              value={
                <>
                  {g.value}
                  {g.pct >= g.warn && g.pct >= 0 && <span className="sr-only"> (high)</span>}
                  {g.pct >= 0 && (
                    <span className="mt-1.5 block h-1 w-28 overflow-hidden rounded-full bg-track" aria-hidden="true">
                      <span className={cn("block h-full rounded-full", toneFill(g.pct, g.warn))} style={{ width: `${Math.min(100, g.pct)}%` }} />
                    </span>
                  )}
                </>
              }
              hint={
                <span className="tabular-nums" title={g.hint}>
                  {g.hint}
                </span>
              }
            />
          ))}
        </KpiGrid>

        <section aria-labelledby="history-title" className="space-y-3">
          <SectionTitle
            id="history-title"
            icon={ICONS.trend}
            title="History"
            action={
              <p className="text-[12px] tabular-nums text-fg-muted">
                {windowText}
                {samples.length > 1 && spikes.length === 0 && " · no spikes"}
              </p>
            }
          />
          {history.isError && (
            <p role="status" className="flex items-center gap-2 text-[12.5px] text-warn">
              <AlertTriangle className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
              History is unavailable; current values above are still live.
            </p>
          )}

          <div className="grid gap-4 lg:grid-cols-2">
            <ChartCard
              icon={ICONS.system}
              title="CPU"
              legend={[
                { label: "Host", color: SERIES_MAIN },
                { label: "KeiRouter", color: SERIES_ALT },
              ]}
            >
              <TimeLines
                data={samples}
                x={x}
                max={100}
                threshold={{ value: CPU_WARN, label: `${CPU_WARN}% spike` }}
                format={(v) => `${Math.round(v)}%`}
                tooltipTime={fmtClockSeconds}
                xFormat={xFormat}
                label="CPU usage over time, host and KeiRouter"
                series={[
                  { key: "host", label: "Host", color: SERIES_MAIN, value: (p) => p.cpu_pct },
                  { key: "proc", label: "KeiRouter", color: SERIES_ALT, value: (p) => p.proc_cpu_pct ?? 0 },
                ]}
              />
              {s.cpu_per_core.length > 0 && <PerCore cores={s.cpu_per_core} />}
            </ChartCard>

            <ChartCard icon={ICONS.memory} title="Host memory" legend={[{ label: "Memory", color: SERIES_MAIN }]}>
              <TimeLines
                data={samples}
                x={x}
                max={100}
                threshold={{ value: MEM_WARN, label: `${MEM_WARN}% spike` }}
                format={(v) => `${Math.round(v)}%`}
                tooltipTime={fmtClockSeconds}
                xFormat={xFormat}
                label="Host memory usage over time"
                series={[{ key: "mem", label: "Memory", color: SERIES_MAIN, value: (p) => p.mem_pct }]}
              />
            </ChartCard>

            <ChartCard
              icon={ICONS.memory}
              title="KeiRouter memory"
              legend={[
                { label: "RSS", color: SERIES_MAIN },
                { label: "Heap", color: SERIES_SOFT },
              ]}
            >
              <TimeLines
                data={samples}
                x={x}
                format={(v) => `${Math.round(v)} MB`}
                tooltipTime={fmtClockSeconds}
                xFormat={xFormat}
                label="KeiRouter process memory over time, RSS and heap"
                series={[
                  { key: "rss", label: "RSS", color: SERIES_MAIN, value: (p) => p.proc_rss_mb ?? 0 },
                  { key: "heap", label: "Heap", color: SERIES_SOFT, value: (p) => p.heap_mb },
                ]}
              />
            </ChartCard>

            <ChartCard icon={Workflow} title="Goroutines" subtitle="A steady climb can mean a leak" legend={[{ label: "Goroutines", color: SERIES_MAIN }]}>
              <TimeLines
                data={samples}
                x={x}
                tooltipTime={fmtClockSeconds}
                xFormat={xFormat}
                label="Goroutines over time"
                series={[{ key: "g", label: "Goroutines", color: SERIES_MAIN, value: (p) => p.goroutines }]}
              />
            </ChartCard>
          </div>
        </section>

        {spikes.length > 0 && (
          <section aria-labelledby="spikes-title" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
            <div className="border-b border-line px-4 py-3">
              <SectionTitle
                id="spikes-title"
                icon={AlertTriangle}
                title="Spikes"
                subtitle={`CPU above ${CPU_WARN}% or memory above ${MEM_WARN}%`}
              />
            </div>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[560px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="px-4 py-2 font-medium">Time</th>
                    <th scope="col" className="px-4 py-2 font-medium">Spike</th>
                    <th scope="col" className="px-4 py-2 text-right font-medium">CPU</th>
                    <th scope="col" className="px-4 py-2 text-right font-medium">Memory</th>
                    <th scope="col" className="px-4 py-2 text-right font-medium">Goroutines</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {spikes.map((sp, i) => (
                    <tr key={i} className="hover:bg-hover">
                      <td className="whitespace-nowrap px-4 py-2 font-mono text-[12px] text-fg-muted">{new Date(sp.ts * 1000).toLocaleString()}</td>
                      <td className="px-4 py-2">
                        <span className="inline-flex gap-1.5">
                          {sp.cpu_spike && <Badge tone="danger">CPU</Badge>}
                          {sp.mem_spike && <Badge tone="danger">Memory</Badge>}
                        </span>
                      </td>
                      <td className={cn("px-4 py-2 text-right tabular-nums", sp.cpu_spike ? "font-medium text-bad" : "text-fg")}>{sp.cpu_pct.toFixed(1)}%</td>
                      <td className={cn("px-4 py-2 text-right tabular-nums", sp.mem_spike ? "font-medium text-bad" : "text-fg")}>{sp.mem_pct.toFixed(1)}%</td>
                      <td className="px-4 py-2 text-right tabular-nums text-fg">{sp.goroutines.toLocaleString("en-US")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}

        <section aria-labelledby="details-title" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
          <div className="border-b border-line px-4 py-3">
            <SectionTitle id="details-title" icon={ICONS.server} title="Details" />
          </div>
          <div className="grid divide-y divide-line lg:grid-cols-2 lg:divide-x lg:divide-y-0">
            <DetailList
              title="Go runtime"
              rows={[
                ["Heap allocated", `${s.heap_alloc_mb.toFixed(1)} MB`],
                ["Heap in use", `${s.heap_inuse_mb.toFixed(1)} MB`],
                ["Heap idle", `${s.heap_idle_mb.toFixed(1)} MB`],
                ["Heap reserved from OS", `${s.heap_sys_mb.toFixed(1)} MB`],
                ["GC cycles", s.gc_cycles.toLocaleString("en-US")],
                ["Last GC pause", `${s.gc_pause_last_ms.toFixed(2)} ms`],
                ["Total GC pause", `${s.gc_pause_total_ms.toFixed(1)} ms`],
                ["Goroutines", s.goroutines.toLocaleString("en-US")],
              ]}
            />
            <DetailList
              title="Host & process"
              rows={[
                ["Hostname", <span key="h" className="font-mono text-[12.5px]">{s.host || "—"}</span>],
                ["Platform", <span key="p" className="font-mono text-[12.5px]">{s.os && s.arch ? `${s.os}/${s.arch}` : s.os || "—"}</span>],
                ["Process ID", <span key="pid" className="font-mono text-[12.5px]">{s.pid}</span>],
                ["Memory available", fmtMB(s.mem_available_mb)],
                ["Disk", `${s.disk_used_gb.toFixed(1)} of ${s.disk_total_gb.toFixed(1)} GB used`],
                ["Network connections", s.net_conns.toLocaleString("en-US")],
                ["Open file descriptors", `${s.proc_open_fds.toLocaleString("en-US")} process · ${s.open_fds.toLocaleString("en-US")} host`],
              ]}
            />
          </div>
        </section>
      </div>
    </>
  );
}

// Per-core load sits behind a disclosure inside the CPU card; most visits only
// need the aggregate.
function PerCore({ cores }: { cores: number[] }) {
  return (
    <details className="group mt-3 border-t border-line pt-2">
      <summary className={cn("flex h-7 w-fit cursor-pointer list-none items-center gap-1 rounded-md text-[12.5px] font-medium text-fg-muted hover:text-fg [&::-webkit-details-marker]:hidden", FOCUS)}>
        <ChevronRight className="h-4 w-4 transition-transform group-open:rotate-90" strokeWidth={1.75} aria-hidden="true" />
        Per core ({cores.length})
      </summary>
      <ul className="mt-2 grid grid-cols-[repeat(auto-fill,minmax(64px,1fr))] gap-1.5" aria-label="CPU load per core">
        {cores.map((pct, i) => (
          <li key={i} className="rounded-lg border border-line px-2 py-1.5" aria-label={`Core ${i}: ${Math.round(pct)}%`}>
            <div className="flex items-baseline justify-between text-[11px] tabular-nums" aria-hidden="true">
              <span className="font-mono text-fg-faint">{i}</span>
              <span className={pct >= 85 ? "text-bad" : pct >= 60 ? "text-warn" : "text-fg"}>{Math.round(pct)}%</span>
            </div>
            <div className="mt-1 h-1 overflow-hidden rounded-full bg-track" aria-hidden="true">
              <div className={cn("h-full rounded-full", pct >= 85 ? "bg-bad" : pct >= 60 ? "bg-warn" : "bg-accent-500")} style={{ width: `${Math.max(2, pct)}%` }} />
            </div>
          </li>
        ))}
      </ul>
    </details>
  );
}

function DetailList({ title, rows }: { title: string; rows: [string, ReactNode][] }) {
  return (
    <div className="min-w-0">
      <h3 className="px-4 pb-1 pt-3 text-[12.5px] font-medium text-fg-muted">{title}</h3>
      <dl className="divide-y divide-line">
        {rows.map(([label, value]) => (
          <div key={label} className="flex items-baseline justify-between gap-4 px-4 py-2.5 text-[13px]">
            <dt className="text-fg-muted">{label}</dt>
            <dd className="text-right tabular-nums text-fg">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function toneText(pct: number, warn: number) {
  return pct >= warn ? "text-bad" : pct >= warn * 0.75 ? "text-warn" : "text-fg";
}

function toneTile(pct: number, warn: number): "bad" | "warn" | "section" {
  return pct >= warn ? "bad" : pct >= warn * 0.75 ? "warn" : "section";
}

function toneFill(pct: number, warn: number) {
  return pct >= warn ? "bg-bad" : pct >= warn * 0.75 ? "bg-warn" : "bg-accent-500";
}

function fmtMB(mb: number) {
  return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${Math.round(mb)} MB`;
}

function fmtDuration(seconds: number) {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h < 24) return `${h}h ${m}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

function fmtClockSeconds(ms: number) {
  return new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}
