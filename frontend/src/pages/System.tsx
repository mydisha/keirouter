import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, ChevronRight, FileStack, Network, RefreshCw, Workflow } from "lucide-react";
import { api, type SystemResourceBucket, type SystemSample, type SystemSnapshot } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { Badge, ErrorCard, IconTile, SectionTitle, SegmentedControl, Skeleton } from "../components/ui";
import { ChartCard, TimeLines } from "../components/charts/TimeSeries";
import { ICONS } from "../lib/icons";

// System answers two questions: is this machine healthy, and how much of it
// is KeiRouter using? Every number comes from the gateway's 5-second
// collector (/api/system and /api/system/history), so the cards and the live
// chart always agree. Longer ranges read the persisted, bucketed timeline
// (/api/system/resources, kept for 7 days).

// Defaults match the gateway; /system/history sends the live values.
const DEFAULT_THRESHOLDS = { cpu_pct: 80, mem_pct: 85, disk_pct: 90 };
// "Getting close" starts this many points below the alert line.
const WARN_MARGIN = 10;

// Series colours: KeiRouter is always the orange series, the machine (or
// "everything else") is always accent blue, the peak line its softer step.
const C_HOST = "var(--color-accent-500)";
const C_PEAK = "var(--color-accent-300)";
const C_KEI = "var(--series-alt)";

const FOCUS = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

type Range = "live" | "1h" | "24h" | "7d";
const RANGES: Record<Range, { label: string; hours: number; interval: string; stepS: number }> = {
  live: { label: "5 min", hours: 0, interval: "", stepS: 5 },
  "1h": { label: "1 hour", hours: 1, interval: "1m", stepS: 60 },
  "24h": { label: "24 hours", hours: 24, interval: "15m", stepS: 900 },
  "7d": { label: "7 days", hours: 168, interval: "2h", stepS: 7200 },
};

// Point is one chart column regardless of source. Live samples have no peak,
// so peak = value; stored buckets carry both. Gap points (gateway was off)
// are all zeros, which breaks the lines instead of drawing across the hole.
interface Point {
  t: number;
  cpu: number;
  cpuPeak: number;
  proc: number;
  mem: number;
  memPeak: number;
  rss: number;
  heap: number;
  goroutines: number;
  gap?: boolean;
}

type Level = "ok" | "warn" | "bad";

export function SystemPage() {
  const [range, setRange] = useState<Range>("live");
  const snap = useQuery({ queryKey: ["system"], queryFn: () => api.systemMonitor(), refetchInterval: 5000 });
  const history = useQuery({ queryKey: ["system-history"], queryFn: () => api.systemHistory(), refetchInterval: 5000 });
  const cfg = RANGES[range];
  const stored = useQuery({
    queryKey: ["system-resources", range],
    queryFn: () => api.systemResources(cfg.hours, cfg.interval),
    enabled: range !== "live",
    refetchInterval: 60_000,
  });
  const now = useNow();

  const refresh = () => {
    snap.refetch();
    history.refetch();
    if (range !== "live") stored.refetch();
  };

  const age = snap.data?.sampled_at ? Math.max(0, Math.round(now / 1000 - snap.data.sampled_at)) : null;
  const stale = snap.isError || (age != null && age > 20);

  const header = (
    <PageHeader
      title="System"
      description="How this machine is doing, and how much of it KeiRouter uses."
      action={
        <>
          <span role="status" className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] font-medium tabular-nums text-fg-muted sm:h-8">
            <span className={cn("h-1.5 w-1.5 rounded-full", snap.isError ? "bg-bad" : stale ? "bg-warn" : "live-dot bg-ok")} aria-hidden="true" />
            {snap.isError ? "Unreachable" : age == null ? "Live" : stale ? `Stale · ${fmtDuration(age)} ago` : `Live · ${age}s ago`}
          </span>
          <button
            type="button"
            onClick={refresh}
            aria-label="Refresh now"
            className={cn("inline-flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg sm:h-8 sm:w-8", FOCUS)}
          >
            <RefreshCw className={cn("h-4 w-4", (snap.isFetching || history.isFetching || stored.isFetching) && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
          </button>
        </>
      }
    />
  );

  const samples = useMemo(() => history.data?.samples ?? [], [history.data]);
  const points = useMemo<Point[]>(
    () => (range === "live" ? samples.map(livePoint) : bucketPoints(stored.data ?? [], cfg.stepS)),
    [range, samples, stored.data, cfg.stepS],
  );

  if (snap.isLoading) {
    return (
      <>
        {header}
        <div className="space-y-6" aria-busy="true">
          <span className="sr-only" role="status">Loading system metrics</span>
          <Skeleton className="h-[60px] w-full rounded-2xl" />
          <div className="grid gap-4 xl:grid-cols-2">
            <Skeleton className="h-[340px] rounded-2xl" />
            <Skeleton className="h-[340px] rounded-2xl" />
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
  const th = history.data?.thresholds ?? DEFAULT_THRESHOLDS;
  // Judge CPU on the last ~30s, not one 5s window: a single busy tick is normal.
  const recent = samples.slice(-6);
  const cpuRecent = recent.length ? recent.reduce((a, p) => a + p.cpu_pct, 0) / recent.length : s.cpu_pct;
  const issues = findIssues(s, th, cpuRecent, recent.length * (history.data?.interval_sec ?? 5));

  const episodes = spikeEpisodes(points, cfg.stepS, th);
  const loading = range !== "live" && stored.isLoading;
  const xFormat = axisFormat(points);
  const tooltipTime = range === "live" || range === "1h" ? fmtClockSeconds : fmtDayTime;
  const x = (p: Point) => p.t;
  const avgWord = range === "live" ? "5-second average" : `${cfg.interval.replace("m", " min").replace("h", " h")} average`;
  const showPeak = range !== "live";

  return (
    <>
      {header}
      <div className="space-y-6">
        <HealthBanner issues={issues} />

        <div className="grid gap-4 xl:grid-cols-2">
          <MachineCard s={s} th={th} />
          <ProcessCard s={s} />
        </div>

        <section aria-labelledby="history-title" className="space-y-3">
          <SectionTitle
            id="history-title"
            icon={ICONS.trend}
            title="History"
            subtitle={historyNote(range, points, stored.isError || history.isError)}
            action={<SegmentedControl label="Time range" value={range} onChange={setRange} options={(Object.keys(RANGES) as Range[]).map((r) => ({ value: r, label: RANGES[r].label }))} />}
          />

          {loading ? (
            <div className="grid gap-4 lg:grid-cols-2" aria-busy="true">
              {[0, 1, 2, 3].map((i) => (
                <Skeleton key={i} className="h-[260px] rounded-2xl" />
              ))}
            </div>
          ) : (
            <div className="grid gap-4 lg:grid-cols-2">
              <ChartCard
                icon={ICONS.system}
                title="CPU"
                subtitle={`Whole machine vs KeiRouter's share, ${avgWord}${showPeak ? "; peak is the busiest moment" : ""}`}
                legend={[
                  { label: showPeak ? "Machine avg" : "Machine", color: C_HOST },
                  ...(showPeak ? [{ label: "Machine peak", color: C_PEAK }] : []),
                  { label: "KeiRouter", color: C_KEI },
                ]}
              >
                <TimeLines
                  data={points}
                  x={x}
                  max={100}
                  threshold={{ value: th.cpu_pct, label: `Alert ${th.cpu_pct}%` }}
                  format={fmtPct}
                  tooltipTime={tooltipTime}
                  xFormat={xFormat}
                  label="CPU usage over time: whole machine and KeiRouter's share"
                  series={[
                    ...(showPeak ? [{ key: "peak", label: "Machine peak", color: C_PEAK, value: (p: Point) => p.cpuPeak }] : []),
                    { key: "host", label: showPeak ? "Machine avg" : "Machine", color: C_HOST, value: (p: Point) => p.cpu },
                    { key: "proc", label: "KeiRouter", color: C_KEI, value: (p: Point) => p.proc },
                  ]}
                />
              </ChartCard>

              <ChartCard
                icon={ICONS.memory}
                title="Machine memory"
                subtitle="Share of RAM in use by all programs (cache excluded)"
                legend={[{ label: showPeak ? "Average" : "In use", color: C_HOST }, ...(showPeak ? [{ label: "Peak", color: C_PEAK }] : [])]}
              >
                <TimeLines
                  data={points}
                  x={x}
                  max={100}
                  threshold={{ value: th.mem_pct, label: `Alert ${th.mem_pct}%` }}
                  format={fmtPct}
                  tooltipTime={tooltipTime}
                  xFormat={xFormat}
                  label="Machine memory usage over time"
                  series={[
                    ...(showPeak ? [{ key: "peak", label: "Peak", color: C_PEAK, value: (p: Point) => p.memPeak }] : []),
                    { key: "mem", label: showPeak ? "Average" : "In use", color: C_HOST, value: (p: Point) => p.mem },
                  ]}
                />
              </ChartCard>

              <ChartCard
                icon={ICONS.memory}
                title="KeiRouter memory"
                subtitle="RAM held by the process, and the part used by live data (heap)"
                legend={[
                  { label: "Process RAM", color: C_KEI },
                  { label: "Heap", color: C_HOST },
                ]}
              >
                <TimeLines
                  data={points}
                  x={x}
                  format={(v) => fmtMB(v)}
                  tooltipTime={tooltipTime}
                  xFormat={xFormat}
                  label="KeiRouter memory over time: process RAM and Go heap"
                  series={[
                    { key: "rss", label: "Process RAM", color: C_KEI, value: (p) => p.rss },
                    { key: "heap", label: "Heap", color: C_HOST, value: (p) => p.heap },
                  ]}
                />
              </ChartCard>

              <ChartCard icon={Workflow} title="Goroutines" subtitle="Tasks running inside KeiRouter. Rising without ever falling back can mean a leak." legend={[{ label: "Goroutines", color: C_KEI }]}>
                <TimeLines
                  data={points}
                  x={x}
                  format={(v) => Math.round(v).toLocaleString("en-US")}
                  tooltipTime={tooltipTime}
                  xFormat={xFormat}
                  label="Goroutines over time"
                  series={[{ key: "g", label: "Goroutines", color: C_KEI, value: (p) => p.goroutines }]}
                />
              </ChartCard>
            </div>
          )}
        </section>

        {!loading && <SpikeList episodes={episodes} th={th} hasData={points.some((p) => !p.gap)} rangeLabel={range === "live" ? "the last 5 minutes" : `the last ${cfg.label}`} />}

        <TechDetails s={s} />
      </div>
    </>
  );
}

// ---- Health summary ---------------------------------------------------------

interface Issue {
  level: Exclude<Level, "ok">;
  title: string;
  detail: string;
}

function findIssues(s: SystemSnapshot, th: typeof DEFAULT_THRESHOLDS, cpuRecent: number, windowS: number): Issue[] {
  const out: Issue[] = [];
  const cpuL = level(cpuRecent, th.cpu_pct);
  if (cpuL !== "ok")
    out.push({ level: cpuL, title: `CPU ${cpuL === "bad" ? "is overloaded" : "is busy"}`, detail: `${fmtPct(cpuRecent)} averaged over the last ${fmtDuration(Math.max(5, windowS))} (alert at ${th.cpu_pct}%). KeiRouter's share: ${fmtShare(s.proc_cpu_pct)}.` });
  const memL = level(s.mem_pct, th.mem_pct);
  if (memL !== "ok")
    out.push({ level: memL, title: `Memory ${memL === "bad" ? "is nearly full" : "is getting full"}`, detail: `${fmtPct(s.mem_pct)} in use, ${fmtMB(s.mem_available_mb)} still available (alert at ${th.mem_pct}%). KeiRouter holds ${fmtMB(s.proc_rss_mb)}.` });
  const diskL = level(s.disk_pct, th.disk_pct);
  if (diskL !== "ok")
    out.push({ level: diskL, title: `Disk ${diskL === "bad" ? "is nearly full" : "is filling up"}`, detail: `${fmtPct(s.disk_pct)} used on ${s.disk_path}, ${fmtGB(s.disk_free_gb)} free (alert at ${th.disk_pct}%).` });
  if (s.swap_total_mb > 0 && s.swap_used_mb / s.swap_total_mb >= 0.5 && s.mem_pct >= th.mem_pct - WARN_MARGIN)
    out.push({ level: "warn", title: "Swapping to disk", detail: `${fmtMB(s.swap_used_mb)} of ${fmtMB(s.swap_total_mb)} swap in use while RAM is tight, which slows everything down.` });
  if (s.proc_fd_limit && s.proc_open_fds / s.proc_fd_limit >= 0.6) {
    const bad = s.proc_open_fds / s.proc_fd_limit >= 0.8;
    out.push({ level: bad ? "bad" : "warn", title: "Running out of file handles", detail: `KeiRouter has ${s.proc_open_fds.toLocaleString("en-US")} of ${s.proc_fd_limit.toLocaleString("en-US")} allowed open. New connections fail at the limit.` });
  }
  return out.sort((a, b) => (a.level === b.level ? 0 : a.level === "bad" ? -1 : 1));
}

function HealthBanner({ issues }: { issues: Issue[] }) {
  if (!issues.length) {
    return (
      <div role="status" className="flex items-center gap-3 rounded-2xl border border-line bg-surface px-4 py-3 shadow-[var(--shadow-card)]">
        <IconTile icon={CheckCircle2} tone="ok" size="md" />
        <div className="min-w-0">
          <p className="text-[13px] font-semibold text-fg">Everything looks normal</p>
          <p className="text-[12.5px] text-fg-muted">CPU, memory and disk are all below their alert levels.</p>
        </div>
      </div>
    );
  }
  const worst = issues[0].level;
  return (
    <div role="status" className={cn("rounded-2xl border bg-surface px-4 py-3 shadow-[var(--shadow-card)]", worst === "bad" ? "border-bad/40" : "border-warn/40")}>
      <div className="flex items-start gap-3">
        <IconTile icon={AlertTriangle} tone={worst} size="md" />
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-semibold text-fg">{worst === "bad"
              ? `${issues.length === 1 ? "1 thing needs" : `${issues.length} things need`} attention now`
              : `${issues.length === 1 ? "1 thing" : `${issues.length} things`} to keep an eye on`}</p>
          <ul className="mt-1 space-y-1">
            {issues.map((i) => (
              <li key={i.title} className="text-[12.5px] text-fg-muted">
                <Badge tone={i.level === "bad" ? "danger" : "warning"}>{i.level === "bad" ? "Critical" : "Warning"}</Badge> <span className="font-medium text-fg">{i.title}.</span> {i.detail}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

// ---- Machine & process cards ------------------------------------------------

function MachineCard({ s, th }: { s: SystemSnapshot; th: typeof DEFAULT_THRESHOLDS }) {
  const other = Math.max(0, s.cpu_pct - s.proc_cpu_pct);
  const memOther = Math.max(0, s.mem_used_mb - s.proc_rss_mb);
  const memFree = Math.max(0, s.mem_total_mb - s.mem_used_mb);
  const load = s.load_avg;
  const platform = [s.os, s.arch].filter(Boolean).join(" · ");
  return (
    <section aria-labelledby="machine-title" className="min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle
          id="machine-title"
          icon={ICONS.server}
          title="This machine"
          subtitle={
            <span className="tabular-nums">
              <span className="break-all font-mono">{s.host || "unknown host"}</span>
              {platform && ` · ${platform}`}
              {s.host_uptime_s > 0 && ` · up ${fmtDuration(s.host_uptime_s)}`}
            </span>
          }
        />
      </div>
      <div className="divide-y divide-line">
        <Meter
          label="CPU"
          value={fmtPct(s.cpu_pct, 1)}
          lvl={level(s.cpu_pct, th.cpu_pct)}
          threshold={th.cpu_pct}
          segments={[
            { label: "KeiRouter", pct: s.proc_cpu_pct, color: C_KEI, text: fmtShare(s.proc_cpu_pct) },
            { label: "Other programs", pct: other, color: C_HOST, text: fmtPct(other, 1) },
          ]}
          rest={{ label: "Idle", text: fmtPct(Math.max(0, 100 - s.cpu_pct), 0) }}
          note={
            <>
              {s.cpu_count} {s.cpu_count === 1 ? "core" : "cores"}
              {load && (
                <>
                  {" · "}
                  <span title="Average number of tasks running or waiting for CPU over the last 1, 5 and 15 minutes">
                    load {load.map((l) => l.toFixed(2)).join(" / ")}
                  </span>
                  {" — "}
                  {loadVerdict(load[0], s.cpu_count)}
                </>
              )}
            </>
          }
        >
          {s.cpu_per_core.length > 1 && <PerCore cores={s.cpu_per_core} />}
        </Meter>

        <Meter
          label="Memory"
          value={fmtPct(s.mem_pct, 1)}
          lvl={level(s.mem_pct, th.mem_pct)}
          threshold={th.mem_pct}
          segments={[
            { label: "KeiRouter", pct: (s.proc_rss_mb / s.mem_total_mb) * 100, color: C_KEI, text: fmtMB(s.proc_rss_mb) },
            { label: "Other programs", pct: (memOther / s.mem_total_mb) * 100, color: C_HOST, text: fmtMB(memOther) },
          ]}
          rest={{ label: "Free & cache", text: fmtMB(memFree) }}
          note={
            <>
              {fmtMB(s.mem_used_mb)} of {fmtMB(s.mem_total_mb)} in use · {fmtMB(s.mem_available_mb)} available for new work
            </>
          }
        />

        {s.swap_total_mb > 0 && (
          <Meter
            label="Swap"
            value={fmtPct((s.swap_used_mb / s.swap_total_mb) * 100, 1)}
            lvl="ok"
            segments={[{ label: "Used", pct: (s.swap_used_mb / s.swap_total_mb) * 100, color: C_HOST, text: fmtMB(s.swap_used_mb) }]}
            rest={{ label: "Free", text: fmtMB(s.swap_total_mb - s.swap_used_mb) }}
            note="Disk space used as overflow RAM. Some use is normal; heavy use while memory is full slows things down."
          />
        )}

        <Meter
          label={
            <>
              Disk <span className="font-mono text-[11.5px] font-normal text-fg-faint">{s.disk_path}</span>
            </>
          }
          value={fmtPct(s.disk_pct, 1)}
          lvl={level(s.disk_pct, th.disk_pct)}
          threshold={th.disk_pct}
          segments={[{ label: "Used", pct: s.disk_pct, color: C_HOST, text: fmtGB(s.disk_used_gb) }]}
          rest={{ label: "Free", text: fmtGB(s.disk_free_gb) }}
          note={`${fmtGB(s.disk_total_gb)} total · the volume KeiRouter's data lives on`}
        />
      </div>
    </section>
  );
}

function ProcessCard({ s }: { s: SystemSnapshot }) {
  const fdShare = s.proc_fd_limit ? (s.proc_open_fds / s.proc_fd_limit) * 100 : 0;
  return (
    <section aria-labelledby="process-title" className="min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle
          id="process-title"
          icon={ICONS.system}
          title="KeiRouter process"
          subtitle={
            <span className="tabular-nums">
              PID <span className="font-mono">{s.pid}</span> · up {fmtDuration(s.uptime_s)}
              {s.go_version && ` · ${s.go_version}`}
            </span>
          }
        />
      </div>
      <dl className="grid grid-cols-1 gap-px bg-line sm:grid-cols-2">
        <Stat
          icon={ICONS.system}
          label="CPU"
          value={fmtShare(s.proc_cpu_pct)}
          hint={`of the whole machine · ≈ ${fmtCores(s.proc_cpu_cores)} of ${s.cpu_count} cores`}
        />
        <Stat
          icon={ICONS.memory}
          label="Memory"
          value={fmtMB(s.proc_rss_mb)}
          hint={`${fmtPct((s.proc_rss_mb / Math.max(1, s.mem_total_mb)) * 100, 1)} of machine RAM · heap ${fmtMB(s.heap_alloc_mb)}`}
        />
        <Stat
          icon={Network}
          label="Network connections"
          value={s.proc_conns.toLocaleString("en-US")}
          hint={`${s.proc_conns_established.toLocaleString("en-US")} active · clients and upstream providers`}
        />
        <Stat icon={Workflow} label="Goroutines" value={s.goroutines.toLocaleString("en-US")} hint={`on ${s.proc_threads.toLocaleString("en-US")} OS threads`} />
        <Stat
          icon={FileStack}
          label="Open files & sockets"
          value={s.proc_open_fds.toLocaleString("en-US")}
          lvl={fdShare >= 80 ? "bad" : fdShare >= 60 ? "warn" : "ok"}
          hint={s.proc_fd_limit ? `of ${s.proc_fd_limit.toLocaleString("en-US")} allowed (${fmtShare(fdShare)})` : "no limit reported on this OS"}
        />
        <Stat
          icon={ICONS.uptime}
          label="Garbage collection"
          value={`${s.gc_pause_last_ms.toFixed(2)} ms`}
          hint={`last pause · ${s.gc_cycles.toLocaleString("en-US")} runs since start`}
        />
      </dl>
    </section>
  );
}

interface Segment {
  label: string;
  pct: number;
  color: string;
  text: string;
}

// Meter is one labelled resource: headline %, a stacked bar on a fixed 0–100
// scale with the alert line marked, and a legend that names every segment
// with its absolute amount (so colour is never the only key).
function Meter({
  label,
  value,
  lvl,
  threshold,
  segments,
  rest,
  note,
  children,
}: {
  label: ReactNode;
  value: string;
  lvl: Level;
  threshold?: number;
  segments: Segment[];
  rest: { label: string; text: string };
  note?: ReactNode;
  children?: ReactNode;
}) {
  const parts = segments.map((sg) => ({ ...sg, pct: clamp(sg.pct) }));
  let left = 0;
  return (
    <div className="px-4 py-3.5">
      <div className="flex items-baseline justify-between gap-3">
        <span className="min-w-0 text-[12.5px] font-medium text-fg-muted [overflow-wrap:anywhere]">{label}</span>
        <span className={cn("inline-flex items-center gap-1.5 text-[18px] font-semibold leading-none tabular-nums", lvl === "bad" ? "text-bad" : lvl === "warn" ? "text-warn" : "text-fg")}>
          {lvl !== "ok" && <AlertTriangle className="h-4 w-4" strokeWidth={2} aria-hidden="true" />}
          {value}
          {lvl !== "ok" && <span className="sr-only">({lvl === "bad" ? "above alert level" : "close to alert level"})</span>}
        </span>
      </div>
      <div className="relative mt-2.5" aria-hidden="true">
        <div className="relative h-2.5 overflow-hidden rounded-full bg-track">
          {parts.map((sg) => {
            const style = { left: `${left}%`, width: sg.pct > 0 ? `max(3px, calc(${sg.pct}% - 2px))` : 0, background: sg.color };
            left += sg.pct;
            return <span key={sg.label} className="absolute inset-y-0 first:rounded-l-full" style={style} />;
          })}
        </div>
        {threshold != null && (
          <span className="absolute -top-1 -bottom-1 w-0.5 rounded-full bg-fg-faint" style={{ left: `calc(${threshold}% - 1px)` }} title={`Alert at ${threshold}%`} />
        )}
      </div>
      <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] tabular-nums text-fg-muted">
        {parts.map((sg) => (
          <li key={sg.label} className="inline-flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-[2px]" style={{ background: sg.color }} aria-hidden="true" />
            {sg.label} <span className="text-fg">{sg.text}</span>
          </li>
        ))}
        <li className="inline-flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-[2px] bg-track ring-1 ring-inset ring-line" aria-hidden="true" />
          {rest.label} <span className="text-fg">{rest.text}</span>
        </li>
        {threshold != null && (
          <li className="inline-flex items-center gap-1.5">
            <span className="h-3 w-0.5 rounded-full bg-fg-faint" aria-hidden="true" />
            Alert {threshold}%
          </li>
        )}
      </ul>
      {note && <p className="mt-1.5 text-[12px] text-fg-faint">{note}</p>}
      {children}
    </div>
  );
}

function Stat({ icon, label, value, hint, lvl = "ok" }: { icon: LucideIcon; label: string; value: string; hint: string; lvl?: Level }) {
  return (
    <div className="relative bg-surface py-3.5 pl-[3.75rem] pr-4">
      <dt className="text-[12px] font-medium text-fg-muted">
        <IconTile icon={icon} size="md" tone={lvl === "ok" ? "orange" : lvl} className="absolute left-4 top-4" />
        {label}
      </dt>
      <dd className={cn("mt-0.5 text-[18px] font-semibold leading-tight tabular-nums", lvl === "bad" ? "text-bad" : lvl === "warn" ? "text-warn" : "text-fg")}>{value}</dd>
      <dd className="mt-0.5 text-[12px] text-fg-muted">{hint}</dd>
    </div>
  );
}

// Per-core load as a compact column strip; each column's height is its load.
function PerCore({ cores }: { cores: number[] }) {
  const busiest = cores.reduce((m, v) => Math.max(m, v), 0);
  return (
    <details className="group mt-3">
      <summary className={cn("flex h-9 w-fit cursor-pointer list-none items-center gap-1 rounded-md text-[12px] font-medium text-fg-muted hover:text-fg sm:h-7 [&::-webkit-details-marker]:hidden", FOCUS)}>
        <ChevronRight className="h-3.5 w-3.5 transition-transform group-open:rotate-90" strokeWidth={1.75} aria-hidden="true" />
        Per core · busiest {fmtPct(busiest)}
      </summary>
      {/* At most 32 columns per strip so big machines still fit a 320px phone. */}
      {chunk(cores, 32).map((row, r) => {
        const first = r * 32;
        return (
          <div key={first} className="mt-1.5">
            <ul className="flex h-12 items-end gap-[2px]" aria-label={`CPU load, cores ${first} to ${first + row.length - 1}`}>
              {row.map((pct, j) => (
                <li key={first + j} className="relative h-full min-w-[4px] flex-1 rounded-[3px] bg-track" title={`Core ${first + j}: ${fmtPct(pct)}`} aria-label={`Core ${first + j}: ${fmtPct(pct)}`}>
                  <span className="absolute inset-x-0 bottom-0 rounded-[3px]" style={{ height: `${Math.max(pct > 0 ? 4 : 0, pct)}%`, background: C_HOST }} />
                </li>
              ))}
            </ul>
            <div className="mt-1 flex justify-between font-mono text-[11px] text-fg-faint" aria-hidden="true">
              <span>core {first}</span>
              <span>core {first + row.length - 1}</span>
            </div>
          </div>
        );
      })}
    </details>
  );
}

// ---- Spikes ----------------------------------------------------------------

interface Episode {
  kind: "cpu" | "mem";
  start: number;
  end: number;
  peak: number;
}

function SpikeList({ episodes, th, hasData, rangeLabel }: { episodes: Episode[]; th: typeof DEFAULT_THRESHOLDS; hasData: boolean; rangeLabel: string }) {
  if (!hasData) return null;
  if (!episodes.length) {
    return (
      <p className="flex items-center gap-2 text-[12.5px] text-fg-muted">
        <CheckCircle2 className="h-4 w-4 text-ok" strokeWidth={1.75} aria-hidden="true" />
        No CPU spikes above {th.cpu_pct}% or memory above {th.mem_pct}% in {rangeLabel}.
      </p>
    );
  }
  return (
    <section aria-labelledby="spikes-title" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle id="spikes-title" icon={AlertTriangle} title={`Spikes (${episodes.length})`} subtitle={`Stretches in ${rangeLabel} when CPU went above ${th.cpu_pct}% or memory above ${th.mem_pct}%`} />
      </div>
      {/* Focusable so keyboard users can scroll it sideways on phones; "When" stays pinned. */}
      <div className="overflow-x-auto focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500" tabIndex={0} role="region" aria-label="Spikes table">
        <table className="w-full min-w-[520px] text-[13px]">
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th scope="col" className="sticky left-0 z-[1] bg-subtle px-4 py-2 font-medium">When</th>
              <th scope="col" className="px-4 py-2 font-medium">What</th>
              <th scope="col" className="px-4 py-2 text-right font-medium">Lasted</th>
              <th scope="col" className="px-4 py-2 text-right font-medium">Peak</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {episodes.slice(0, 20).map((e) => (
              <tr key={`${e.kind}-${e.start}`} className="group hover:bg-hover">
                <td className="sticky left-0 z-[1] whitespace-nowrap bg-surface px-4 py-2 tabular-nums text-fg-muted group-hover:bg-hover">
                  {fmtDayTime(e.start)} – {fmtClockSeconds(e.end)}
                </td>
                <td className="px-4 py-2">
                  <Badge tone="danger">{e.kind === "cpu" ? "CPU" : "Memory"}</Badge>
                </td>
                <td className="px-4 py-2 text-right tabular-nums text-fg">{fmtDuration((e.end - e.start) / 1000)}</td>
                <td className="px-4 py-2 text-right font-medium tabular-nums text-bad">{fmtPct(e.peak, 1)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// spikeEpisodes merges consecutive over-threshold points into one stretch per
// resource, newest first, judged on each point's peak.
function spikeEpisodes(points: Point[], stepS: number, th: typeof DEFAULT_THRESHOLDS): Episode[] {
  const out: Episode[] = [];
  for (const kind of ["cpu", "mem"] as const) {
    const limit = kind === "cpu" ? th.cpu_pct : th.mem_pct;
    let cur: Episode | null = null;
    for (const p of points) {
      const v = kind === "cpu" ? p.cpuPeak : p.memPeak;
      if (!p.gap && v >= limit) {
        if (cur) {
          cur.end = p.t + stepS * 1000;
          cur.peak = Math.max(cur.peak, v);
        } else cur = { kind, start: p.t, end: p.t + stepS * 1000, peak: v };
      } else if (cur) {
        out.push(cur);
        cur = null;
      }
    }
    if (cur) out.push(cur);
  }
  return out.sort((a, b) => b.start - a.start);
}

// ---- Technical details --------------------------------------------------------

function TechDetails({ s }: { s: SystemSnapshot }) {
  return (
    <details className="group overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <summary className={cn("flex cursor-pointer list-none flex-wrap items-center gap-x-2 gap-y-0.5 px-4 py-3 text-[13px] font-semibold text-fg hover:bg-hover [&::-webkit-details-marker]:hidden", FOCUS)}>
        <ChevronRight className="h-4 w-4 text-fg-muted transition-transform group-open:rotate-90" strokeWidth={1.75} aria-hidden="true" />
        Technical details
        {/* Wraps under the title on phones, aligned past the chevron. */}
        <span className="basis-full pl-6 text-[12px] font-normal text-fg-muted sm:basis-auto sm:pl-0">Go runtime and host facts, for debugging</span>
      </summary>
      <div className="grid divide-y divide-line border-t border-line lg:grid-cols-2 lg:divide-x lg:divide-y-0">
        <DetailList
          title="Go runtime"
          rows={[
            ["Heap in use", `${s.heap_inuse_mb.toFixed(1)} MB`, "Memory spans holding live objects"],
            ["Heap idle", `${s.heap_idle_mb.toFixed(1)} MB`, "Reserved but empty; may be returned to the OS"],
            ["Heap reserved from OS", `${s.heap_sys_mb.toFixed(1)} MB`],
            ["Next GC at", `${s.next_gc_mb.toFixed(1)} MB`, "Heap size that triggers the next collection"],
            ["GC runs", s.gc_cycles.toLocaleString("en-US")],
            ["Last GC pause", `${s.gc_pause_last_ms.toFixed(2)} ms`],
            ["Total GC pause", `${s.gc_pause_total_ms.toFixed(1)} ms`],
            ["Go version", <span key="go" className="font-mono text-[12.5px]">{s.go_version || "—"}</span>],
          ]}
        />
        <DetailList
          title="Host & process"
          rows={[
            ["Hostname", <span key="h" className="font-mono text-[12.5px]">{s.host || "—"}</span>],
            ["Platform", <span key="p" className="font-mono text-[12.5px]">{[s.os, s.arch].filter(Boolean).join(" ") || "—"}</span>],
            ["CPU", s.cpu_model ? `${s.cpu_model} · ${s.cpu_count} threads` : `${s.cpu_count} threads`],
            ["Process ID", <span key="pid" className="font-mono text-[12.5px]">{s.pid}</span>],
            ["OS threads", s.proc_threads.toLocaleString("en-US")],
            ["Disk volume", <span key="d" className="font-mono text-[12.5px]">{s.disk_path}</span>],
            ["Machine up", s.host_uptime_s > 0 ? fmtDuration(s.host_uptime_s) : "—"],
            ["CPU measured over", `${(s.window_s || 5).toFixed(1)} s`, "Each CPU reading is the average across this window"],
          ]}
        />
      </div>
    </details>
  );
}

function DetailList({ title, rows }: { title: string; rows: [string, ReactNode, string?][] }) {
  return (
    <div className="min-w-0">
      <h3 className="px-4 pb-1 pt-3 text-[12.5px] font-medium text-fg-muted">{title}</h3>
      <dl className="divide-y divide-line">
        {rows.map(([label, value, help]) => (
          <div key={label} className="flex items-baseline justify-between gap-3 px-4 py-2.5 text-[13px] sm:gap-4">
            <dt className="min-w-0 text-fg-muted">
              {label}
              {help && <span className="block text-[11.5px] text-fg-faint">{help}</span>}
            </dt>
            {/* Long values (hostname, CPU model, disk path) wrap instead of overflowing at 320px. */}
            <dd className="min-w-0 max-w-[60%] text-right tabular-nums text-fg [overflow-wrap:anywhere]">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

// ---- Data shaping -----------------------------------------------------------

// Lines break at values ≤ 0, so real readings get a tiny floor: an idle 0.0%
// must still draw, while gap points stay at zero to break the line.
const floor = (v: number) => Math.max(0.01, v || 0);

function livePoint(p: SystemSample): Point {
  return {
    t: p.ts * 1000,
    cpu: floor(p.cpu_pct),
    cpuPeak: p.cpu_pct,
    proc: floor(p.proc_cpu_pct),
    mem: floor(p.mem_pct),
    memPeak: p.mem_pct,
    rss: floor(p.proc_rss_mb),
    heap: floor(p.heap_mb),
    goroutines: floor(p.goroutines),
  };
}

// bucketPoints lays stored buckets on a regular grid from the first bucket to
// now, inserting gap points wherever the gateway wasn't running.
function bucketPoints(buckets: SystemResourceBucket[], stepS: number): Point[] {
  if (!buckets.length) return [];
  const byTs = new Map(buckets.map((b) => [b.ts, b]));
  const first = buckets[0].ts;
  const last = Math.max(buckets[buckets.length - 1].ts, Math.floor(Date.now() / 1000 / stepS) * stepS);
  const out: Point[] = [];
  for (let t = first; t <= last && out.length < 2000; t += stepS) {
    const b = byTs.get(t);
    out.push(
      b
        ? {
            t: t * 1000,
            cpu: floor(b.host_cpu_avg),
            cpuPeak: b.host_cpu_max,
            proc: floor(b.proc_cpu_avg),
            mem: floor(b.host_mem_pct_avg),
            memPeak: b.host_mem_pct_max,
            rss: floor(b.proc_rss_avg_mb),
            heap: floor(b.heap_avg_mb),
            goroutines: floor(b.goroutines_avg),
          }
        : { t: t * 1000, cpu: 0, cpuPeak: 0, proc: 0, mem: 0, memPeak: 0, rss: 0, heap: 0, goroutines: 0, gap: true },
    );
  }
  return out;
}

function historyNote(range: Range, points: Point[], failed: boolean): string {
  if (failed) return "History is unavailable right now; the cards above are still live.";
  const real = points.filter((p) => !p.gap);
  if (!real.length) return range === "live" ? "Collecting samples…" : "Nothing stored for this range yet. Samples are saved every 15 seconds and kept for 7 days.";
  const span = (points[points.length - 1].t - points[0].t) / 1000;
  if (range === "live") return `Last ${fmtDuration(span || 5)}, one reading every 5 seconds`;
  const since = fmtDayTime(real[0].t);
  const gaps = points.some((p) => p.gap) ? " · breaks in the lines are times KeiRouter wasn't running" : "";
  return `Data since ${since}${gaps}`;
}

// ---- Helpers ----------------------------------------------------------------

function level(pct: number, alert: number): Level {
  return pct >= alert ? "bad" : pct >= alert - WARN_MARGIN ? "warn" : "ok";
}

function loadVerdict(load1: number, cores: number) {
  const r = load1 / Math.max(1, cores);
  if (r < 0.7) return "no work waiting";
  if (r < 1) return "busy, still keeping up";
  return "tasks are queueing for CPU";
}

function useNow(ms = 1000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [ms]);
  return now;
}

const clamp = (v: number) => Math.min(100, Math.max(0, v || 0));

function chunk<T>(list: T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < list.length; i += size) out.push(list.slice(i, i + size));
  return out;
}

// fmtShare keeps small shares readable: 0.45% rather than a misleading 0.0%.
function fmtShare(v: number) {
  if (!(v > 0)) return "0%";
  if (v < 0.1) return "<0.1%";
  return `${v.toFixed(v < 1 ? 2 : 1)}%`;
}

function fmtPct(v: number, digits = 0) {
  return `${(v || 0).toFixed(digits)}%`;
}

function fmtCores(c: number) {
  return c >= 1 ? c.toFixed(1) : c >= 0.01 ? c.toFixed(2) : c > 0 ? "<0.01" : "0";
}

function fmtMB(mb: number) {
  return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${Math.round(mb)} MB`;
}

function fmtGB(gb: number) {
  return gb >= 1024 ? `${(gb / 1024).toFixed(2)} TB` : `${gb.toFixed(1)} GB`;
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

function fmtDayTime(ms: number) {
  const d = new Date(ms);
  const sameDay = d.toDateString() === new Date().toDateString();
  const time = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
  return sameDay ? time : `${d.toLocaleDateString(undefined, { month: "short", day: "numeric" })} ${time}`;
}

// Short windows need seconds on the axis or every label reads the same
// minute; multi-day windows need the date.
function axisFormat(points: Point[]): (ms: number) => string {
  const span = points.length > 1 ? (points[points.length - 1].t - points[0].t) / 1000 : 0;
  if (span < 600) return fmtClockSeconds;
  if (span < 36 * 3600) return (ms) => new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
  return (ms) => new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
