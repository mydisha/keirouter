import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Cpu,
  MemoryStick,
  Server,
  AlertTriangle,
  RefreshCw,
  Globe,
} from "lucide-react";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  ResponsiveContainer,
  Tooltip,
  CartesianGrid,
} from "recharts";
import { api } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { Card, SectionHeader, Spinner, ErrorCard, Badge } from "../components/ui";

// ---- helpers ----------------------------------------------------------------

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h < 24) return `${h}h ${m}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h ${m}m`;
}

function tsLabel(ts: number): string {
  const d = new Date(ts * 1000);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

// ---- page -------------------------------------------------------------------

export function SystemPage() {
  const snap = useQuery({
    queryKey: ["system"],
    queryFn: () => api.systemMonitor(),
    refetchInterval: 5000,
  });

  const history = useQuery({
    queryKey: ["system-history"],
    queryFn: () => api.systemHistory(),
    refetchInterval: 5000,
  });

  if (snap.isLoading) return <Spinner />;
  if (snap.isError) return <ErrorCard message={(snap.error as Error).message} />;

  const s = snap.data!;
  const h = history.data;

  const chartData = (h?.samples ?? []).map((pt) => ({
    time: tsLabel(pt.ts),
    cpu: +pt.cpu_pct.toFixed(1),
    mem: +pt.mem_pct.toFixed(1),
    procCpu: +(pt.proc_cpu_pct ?? 0).toFixed(1),
    procRss: +(pt.proc_rss_mb ?? 0).toFixed(1),
  }));

  const spikes = h?.spikes ?? [];

  return (
    <>
      <PageHeader
        title="System Monitor"
        description="Real-time resource usage and runtime health"
        icon={Activity}
        action={
          <button
            onClick={() => { snap.refetch(); history.refetch(); }}
            className="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-sm font-medium text-[var(--text-muted)] transition-colors hover:bg-ink-100 dark:hover:bg-ink-800"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh
          </button>
        }
      />

      {/* ── Overview panel ─────────────────────────────────────────── */}
      <Card className="mb-6">
        <SectionHeader
          title="System Overview"
          description="Host and process resource usage"
          icon={Server}
          iconTone="accent"
        />
        <div className="px-6 pb-5 grid grid-cols-1 md:grid-cols-2 gap-x-10 gap-y-4">
          {/* Host column */}
          <div className="space-y-3">
            <p className="text-xs font-medium text-[var(--text-muted)]">Host</p>
            <MetricBar label="CPU" value={s.cpu_pct} unit="%" detail={`${s.cpu_per_core.length} cores`} />
            <MetricBar label="Memory" value={s.mem_pct} unit="%" detail={`${s.mem_used_mb} / ${s.mem_total_mb} MB`} />
            <MetricBar label="Disk" value={s.disk_pct} unit="%" detail={`${s.disk_used_gb.toFixed(1)} / ${s.disk_total_gb.toFixed(1)} GB`} />
            <div className="flex items-center justify-between text-sm pt-1">
              <span className="text-[var(--text-muted)]">Network Connections</span>
              <span className="tabular-nums font-medium">{s.net_conns.toLocaleString()}</span>
            </div>
          </div>
          {/* Process column */}
          <div className="space-y-3">
            <p className="text-xs font-medium text-[var(--text-muted)]">Process (PID {s.pid})</p>
            <MetricBar label="CPU" value={s.proc_cpu_pct} unit="%" detail={`Uptime ${formatUptime(s.uptime_s)}`} />
            <MetricBar label="RSS" value={Math.min((s.proc_rss_mb / s.mem_total_mb) * 100, 100)} unit="%" detail={`${s.proc_rss_mb.toFixed(0)} MB`} />
            <div className="flex items-center justify-between text-sm">
              <span className="text-[var(--text-muted)]">Goroutines</span>
              <span className="tabular-nums font-medium">{s.goroutines.toLocaleString()}</span>
            </div>
            <div className="flex items-center justify-between text-sm">
              <span className="text-[var(--text-muted)]">Threads</span>
              <span className="tabular-nums font-medium">{s.proc_threads.toLocaleString()}</span>
            </div>
            <div className="flex items-center justify-between text-sm">
              <span className="text-[var(--text-muted)]">Open FDs</span>
              <span className="tabular-nums font-medium">{s.proc_open_fds.toLocaleString()}</span>
            </div>
          </div>
        </div>
      </Card>

      {/* ── Host charts ──────────────────────────────────────────── */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 mb-6">
        <MetricChart
          title="Host CPU"
          description="System-wide CPU percentage over time"
          icon={Cpu}
          data={chartData}
          dataKey="cpu"
          color="var(--color-accent-500)"
          unit="%"
          threshold={80}
        />
        <MetricChart
          title="Host Memory"
          description="System-wide memory percentage over time"
          icon={MemoryStick}
          data={chartData}
          dataKey="mem"
          color="var(--color-secondary-500, #d98a6a)"
          unit="%"
          threshold={85}
        />
      </div>

      {/* ── Process charts ───────────────────────────────────────── */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 mb-6">
        <MetricChart
          title="Process CPU"
          description="keirouter's own CPU usage over time"
          icon={Cpu}
          data={chartData}
          dataKey="procCpu"
          color="#f59e0b"
          unit="%"
          threshold={80}
        />
        <MetricChart
          title="Process RSS"
          description="keirouter's resident memory over time"
          icon={MemoryStick}
          data={chartData}
          dataKey="procRss"
          color="#06b6d4"
          unit=" MB"
        />
      </div>

      {/* ── Spike log ────────────────────────────────────────────── */}
      {spikes.length > 0 && (
        <Card className="mb-6">
          <SectionHeader
            title="Spike Events"
            description="Recent CPU (>80%) or Memory (>85%) spikes detected"
            icon={AlertTriangle}
            iconTone="danger"
          />
          <div className="px-6 pb-5">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-[var(--border)]">
                    <th className="pb-2 pr-4 text-left text-xs font-medium text-[var(--text-muted)]">Time</th>
                    <th className="pb-2 pr-4 text-left text-xs font-medium text-[var(--text-muted)]">Type</th>
                    <th className="pb-2 pr-4 text-right text-xs font-medium text-[var(--text-muted)]">CPU %</th>
                    <th className="pb-2 pr-4 text-right text-xs font-medium text-[var(--text-muted)]">Mem %</th>
                    <th className="pb-2 text-right text-xs font-medium text-[var(--text-muted)]">Goroutines</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {spikes.map((sp, i) => (
                    <tr key={i} className="group">
                      <td className="py-2 pr-4 font-mono text-xs text-[var(--text-muted)]">{tsLabel(sp.ts)}</td>
                      <td className="py-2 pr-4">
                        {sp.cpu_spike && <Badge tone="danger">CPU spike</Badge>}
                        {sp.mem_spike && <Badge tone="danger">Memory spike</Badge>}
                      </td>
                      <td className="py-2 pr-4 text-right tabular-nums">
                        <span className={sp.cpu_spike ? "text-[color:var(--color-danger)] font-medium" : ""}>
                          {sp.cpu_pct.toFixed(1)}%
                        </span>
                      </td>
                      <td className="py-2 pr-4 text-right tabular-nums">
                        <span className={sp.mem_spike ? "text-[color:var(--color-danger)] font-medium" : ""}>
                          {sp.mem_pct.toFixed(1)}%
                        </span>
                      </td>
                      <td className="py-2 text-right tabular-nums">{sp.goroutines}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </Card>
      )}

      {/* ── Runtime details ──────────────────────────────────────── */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 mb-6">
        <Card>
          <SectionHeader
            title="Go Runtime"
            description="Memory and GC statistics"
            icon={Server}
            iconTone="accent"
          />
          <div className="px-6 pb-5">
            <div className="grid grid-cols-2 gap-x-8 gap-y-3">
              <DetailRow label="Heap Alloc" value={`${s.heap_alloc_mb.toFixed(1)} MB`} />
              <DetailRow label="Heap Sys" value={`${s.heap_sys_mb.toFixed(1)} MB`} />
              <DetailRow label="Heap In-Use" value={`${s.heap_inuse_mb.toFixed(1)} MB`} />
              <DetailRow label="Heap Idle" value={`${s.heap_idle_mb.toFixed(1)} MB`} />
              <DetailRow label="GC Cycles" value={s.gc_cycles.toLocaleString()} />
              <DetailRow label="GC Pause (total)" value={`${s.gc_pause_total_ms.toFixed(1)} ms`} />
              <DetailRow label="GC Pause (last)" value={`${s.gc_pause_last_ms.toFixed(2)} ms`} />
              <DetailRow label="Network Conns" value={s.net_conns.toLocaleString()} />
              <DetailRow label="Process FDs" value={s.proc_open_fds.toLocaleString()} />
              <DetailRow label="Process Threads" value={s.proc_threads.toLocaleString()} />
            </div>
          </div>
        </Card>

        <Card>
          <SectionHeader
            title="Host Info"
            description="System and process details"
            icon={Globe}
            iconTone="secondary"
          />
          <div className="px-6 pb-5">
            <div className="grid grid-cols-2 gap-x-8 gap-y-3">
              <DetailRow label="Hostname" value={s.host || "—"} />
              <DetailRow label="OS" value={s.os || "—"} />
              <DetailRow label="Architecture" value={s.arch || "—"} />
              <DetailRow label="PID" value={String(s.pid)} />
              <DetailRow label="Uptime" value={formatUptime(s.uptime_s)} />
              <DetailRow label="Memory Available" value={`${s.mem_available_mb} MB`} />
              <DetailRow label="Disk Free" value={`${s.disk_free_gb.toFixed(1)} GB`} />
            </div>
          </div>
        </Card>
      </div>

      {/* ── Per-core CPU ─────────────────────────────────────────── */}
      {s.cpu_per_core.length > 0 && (
        <Card className="mb-6">
          <SectionHeader
            title="CPU Per Core"
            description={`Utilization across ${s.cpu_per_core.length} cores`}
            icon={Cpu}
            iconTone="accent"
          />
          <div className="px-6 pb-5">
            {/* Heat strip: compact colored cells, wraps to fill width */}
            <div className="flex flex-wrap gap-1">
              {s.cpu_per_core.map((pct, i) => {
                const bg =
                  pct >= 85 ? "bg-[color:var(--color-danger)]" :
                  pct >= 60 ? "bg-[color:var(--color-warning)]" :
                  pct >= 20 ? "bg-accent-500" :
                  "bg-ink-200 dark:bg-ink-700";
                return (
                  <div
                    key={i}
                    title={`Core ${i}: ${pct.toFixed(0)}%`}
                    className={`h-5 rounded-sm transition-colors duration-300 ${bg}`}
                    style={{
                      width: s.cpu_per_core.length <= 16
                        ? `calc(${100 / 8}% - 4px)`    // ~8 per row for small counts
                        : s.cpu_per_core.length <= 32
                        ? `calc(${100 / 16}% - 3px)`   // ~16 per row
                        : `calc(${100 / 20}% - 3px)`,  // ~20 per row for 40+
                      minWidth: 8,
                      opacity: Math.max(0.15, pct / 100),
                    }}
                  />
                );
              })}
            </div>
            {/* Legend */}
            <div className="flex items-center gap-3 mt-3 text-[11px] text-[var(--text-muted)]">
              <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-sm bg-ink-200 dark:bg-ink-700" /> Idle</span>
              <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-sm bg-accent-500" /> 20-60%</span>
              <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-sm bg-[color:var(--color-warning)]" /> 60-85%</span>
              <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-sm bg-[color:var(--color-danger)]" /> 85%+</span>
            </div>
          </div>
        </Card>
      )}
    </>
  );
}

// ---- sub-components ---------------------------------------------------------

function MetricChart({
  title,
  description,
  icon: Icon,
  data,
  dataKey,
  color,
  unit,
  threshold,
}: {
  title: string;
  description: string;
  icon: typeof Activity;
  data: Record<string, string | number>[];
  dataKey: string;
  color: string;
  unit: string;
  threshold?: number;
}) {
  return (
    <Card>
      <SectionHeader title={title} description={description} icon={Icon} iconTone="accent" />
      <div className="px-4 pb-4" style={{ height: 220 }}>
        {data.length < 2 ? (
          <div className="flex h-full items-center justify-center text-sm text-[var(--text-muted)]">
            Collecting data…
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={data} margin={{ top: 4, right: 8, left: -12, bottom: 0 }}>
              <defs>
                <linearGradient id={`grad-${dataKey}`} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={color} stopOpacity={0.3} />
                  <stop offset="100%" stopColor={color} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
              <XAxis
                dataKey="time"
                tick={{ fontSize: 10, fill: "var(--text-muted)" }}
                tickLine={false}
                axisLine={false}
              />
              <YAxis
                tick={{ fontSize: 10, fill: "var(--text-muted)" }}
                tickLine={false}
                axisLine={false}
                domain={unit === "%" ? [0, 100] : ["auto", "auto"]}
              />
              <Tooltip
                contentStyle={{
                  backgroundColor: "var(--bg-elevated)",
                  border: "1px solid var(--border)",
                  borderRadius: 12,
                  fontSize: 12,
                  boxShadow: "var(--shadow-pop)",
                }}
                labelStyle={{ fontSize: 11, color: "var(--text-muted)" }}
                formatter={(val) => [`${val}${unit}`, title]}
              />
              {threshold != null && (
                <Area
                  type="monotone"
                  dataKey={() => threshold}
                  stroke="#ef4444"
                  strokeDasharray="4 4"
                  strokeWidth={1}
                  fill="none"
                  dot={false}
                  activeDot={false}
                  isAnimationActive={false}
                />
              )}
              <Area
                type="monotone"
                dataKey={dataKey}
                stroke={color}
                strokeWidth={2}
                fill={`url(#grad-${dataKey})`}
                dot={false}
                activeDot={{ r: 4, stroke: color, strokeWidth: 2, fill: "var(--bg-elevated)" }}
              />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </div>
    </Card>
  );
}

function MetricBar({ label, value, unit, detail }: { label: string; value: number; unit: string; detail: string }) {
  const color =
    value >= 85 ? "var(--color-danger, #ef4444)" :
    value >= 60 ? "var(--color-warning, #f59e0b)" :
    "var(--color-accent-500, #6366f1)";
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-sm">
        <span className="text-[var(--text-muted)]">{label}</span>
        <span className="tabular-nums font-medium">{value.toFixed(1)}{unit}</span>
      </div>
      <div className="h-1.5 w-full rounded-full bg-ink-200 dark:bg-ink-700 overflow-hidden">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{ width: `${Math.min(value, 100)}%`, backgroundColor: color }}
        />
      </div>
      <p className="text-[11px] text-[var(--text-muted)]">{detail}</p>
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <span className="text-xs text-[var(--text-muted)]">{label}</span>
      <span className="text-sm font-medium tabular-nums">{value}</span>
    </div>
  );
}