import { useMemo, useState, type ReactNode } from "react";
import type { HealthSnapshot } from "../lib/api";
import { cn } from "@/lib/utils";
import { fmtIssue } from "./HealthBadge";

// Provider-health trend charts. Raw snapshots are one row per minute per
// account/model/capability, so they are first folded into ~48 equal time
// buckets (counts summed, p50 request-weighted, p95/p99 worst-of). The
// charts are plain HTML/SVG in the same visual language as the Overview
// traffic chart — no charting library on this page.

const RANGE_MS: Record<string, number> = {
  "15m": 15 * 60_000,
  "1h": 3_600_000,
  "6h": 6 * 3_600_000,
  "24h": 24 * 3_600_000,
  "7d": 7 * 86_400_000,
  "30d": 30 * 86_400_000,
};

export interface HealthBucket {
  start: number;
  requests: number;
  failures: number;
  fallbacks: number;
  p50: number;
  p95: number;
  p99: number;
  ttft95: number;
}

export function bucketSnapshots(snapshots: HealthSnapshot[], range: string, count = 48): HealthBucket[] {
  const span = RANGE_MS[range] ?? RANGE_MS["24h"];
  const n = range === "15m" ? 15 : range === "1h" ? 30 : count;
  const end = Date.now();
  const start = end - span;
  const width = span / n;
  const buckets: (HealthBucket & { p50w: number })[] = Array.from({ length: n }, (_, i) => ({
    start: start + i * width,
    requests: 0,
    failures: 0,
    fallbacks: 0,
    p50: 0,
    p95: 0,
    p99: 0,
    ttft95: 0,
    p50w: 0,
  }));
  for (const s of snapshots) {
    const t = new Date(s.bucket_start).getTime();
    if (!(t >= start && t <= end)) continue;
    const b = buckets[Math.min(n - 1, Math.floor((t - start) / width))];
    b.requests += s.request_count;
    b.failures += s.failure_count;
    b.fallbacks += s.fallback_count;
    if (s.latency_p50_ms && s.request_count) {
      b.p50 += s.latency_p50_ms * s.request_count;
      b.p50w += s.request_count;
    }
    b.p95 = Math.max(b.p95, s.latency_p95_ms ?? 0);
    b.p99 = Math.max(b.p99, s.latency_p99_ms ?? 0);
    b.ttft95 = Math.max(b.ttft95, s.ttft_p95_ms ?? 0);
  }
  return buckets.map(({ p50w, ...b }) => ({ ...b, p50: p50w ? b.p50 / p50w : 0 }));
}

// ── Shared chart frame ───────────────────────────────────────────────────────

interface Series {
  key: string;
  label: string;
  color: string;
  value: (b: HealthBucket) => number;
}

function niceScale(peak: number): { max: number; ticks: number[] } {
  if (!(peak > 0)) return { max: 1, ticks: [1, 0.5, 0] };
  const rough = peak / 4;
  const pow = Math.pow(10, Math.floor(Math.log10(rough)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= rough) ?? 10 * pow;
  return { max: step * 4, ticks: [step * 4, step * 3, step * 2, step, 0] };
}

function fmtBucket(ms: number, range: string): string {
  const d = new Date(ms);
  return range === "7d" || range === "30d"
    ? d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
    : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
}

function fmtTooltipTime(ms: number, range: string): string {
  const d = new Date(ms);
  return range === "7d" || range === "30d"
    ? d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
    : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function fmtMs(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "—";
  return v < 1000 ? `${Math.round(v)} ms` : `${(v / 1000).toFixed(v < 10_000 ? 2 : 1)} s`;
}

function fmtCount(v: number): string {
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(v >= 1e4 ? 0 : 1)}k`;
  return String(Math.round(v * 10) / 10);
}

function ChartFrame({
  data,
  range,
  ticks,
  format,
  hover,
  setHover,
  tooltip,
  children,
}: {
  data: HealthBucket[];
  range: string;
  ticks: number[];
  format: (v: number) => string;
  hover: number | null;
  setHover: (i: number | null) => void;
  tooltip: (b: HealthBucket) => { label: string; value: string; color: string }[];
  children: ReactNode;
}) {
  const every = Math.max(1, Math.ceil(data.length / 6));
  const hovered = hover != null ? data[hover] : null;
  const left = hover != null ? ((hover + 0.5) / data.length) * 100 : 0;
  return (
    <div className="flex gap-2.5">
      <div className="flex h-[180px] min-w-10 flex-col justify-between text-right text-[11px] tabular-nums text-fg-faint" aria-hidden="true">
        {ticks.map((t) => (
          <span key={t} className="-translate-y-1/2 first:translate-y-0 last:translate-y-0">
            {t === 0 ? "0" : format(t)}
          </span>
        ))}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div
          className="relative h-[180px]"
          onMouseLeave={() => setHover(null)}
          onMouseMove={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            setHover(Math.max(0, Math.min(data.length - 1, Math.floor(((e.clientX - r.left) / r.width) * data.length))));
          }}
        >
          {[0, 25, 50, 75].map((top) => (
            <div key={top} className="absolute inset-x-0 border-t border-dashed border-line" style={{ top: `${top}%` }} />
          ))}
          <div className="absolute inset-x-0 bottom-0 border-t border-line-strong" />
          {children}
          {hovered && (
            <>
              <div className="pointer-events-none absolute inset-y-0 w-px bg-line-strong" style={{ left: `${left}%` }} />
              <div
                className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-xl border border-line bg-surface px-2.5 py-2 text-[12px] shadow-[var(--shadow-pop)]"
                style={left > 65 ? { right: `${100 - left}%`, marginRight: 8 } : { left: `${left}%`, marginLeft: 8 }}
              >
                <p className="mb-1 font-medium text-fg">{fmtTooltipTime(hovered.start, range)}</p>
                {tooltip(hovered).map((row) => (
                  <p key={row.label} className="flex items-center gap-2 text-fg-muted">
                    <span className="h-2 w-2 rounded-[2px]" style={{ background: row.color }} />
                    {row.label}
                    <span className="ml-auto pl-3 font-medium tabular-nums text-fg">{row.value}</span>
                  </p>
                ))}
              </div>
            </>
          )}
        </div>
        <div className="relative h-4 text-[11px] tabular-nums text-fg-faint" aria-hidden="true">
          {data.map((b, i) =>
            i % every === 0 ? (
              <span key={b.start} className="absolute" style={{ left: `${(i / data.length) * 100}%` }}>
                {fmtBucket(b.start, range)}
              </span>
            ) : null,
          )}
        </div>
      </div>
    </div>
  );
}

// ── Stacked bars ─────────────────────────────────────────────────────────────

export function StackedBars({ data, range, series, format = fmtCount, label }: { data: HealthBucket[]; range: string; series: Series[]; format?: (v: number) => string; label: string }) {
  const [hover, setHover] = useState<number | null>(null);
  const { max, ticks } = useMemo(() => niceScale(Math.max(0, ...data.map((b) => series.reduce((s, x) => s + x.value(b), 0)))), [data, series]);
  return (
    <ChartFrame
      data={data}
      range={range}
      ticks={ticks}
      format={format}
      hover={hover}
      setHover={setHover}
      tooltip={(b) => series.map((s) => ({ label: s.label, value: format(s.value(b)), color: s.color }))}
    >
      <div className="absolute inset-0 flex items-end gap-[2px]" role="img" aria-label={label}>
        {data.map((b, i) => (
          <div key={b.start} className={cn("flex h-full min-w-[2px] flex-1 flex-col-reverse gap-px", hover != null && hover !== i && "opacity-60")}>
            {series.map((s) => {
              const v = s.value(b);
              return v > 0 ? <div key={s.key} className="rounded-[1.5px]" style={{ height: `${(v / max) * 100}%`, background: s.color }} /> : null;
            })}
          </div>
        ))}
      </div>
    </ChartFrame>
  );
}

// ── Lines ────────────────────────────────────────────────────────────────────

export function Lines({ data, range, series, format = fmtMs, label }: { data: HealthBucket[]; range: string; series: Series[]; format?: (v: number) => string; label: string }) {
  const [hover, setHover] = useState<number | null>(null);
  const peak = useMemo(() => Math.max(0, ...data.flatMap((b) => series.map((s) => s.value(b)))), [data, series]);
  const { max, ticks } = niceScale(peak);
  // Each series is drawn as connected runs; buckets without traffic break the
  // line instead of dropping it to zero.
  const paths = series.map((s) => {
    const segments: string[] = [];
    let current: string[] = [];
    data.forEach((b, i) => {
      const v = s.value(b);
      if (v > 0) {
        current.push(`${(((i + 0.5) / data.length) * 100).toFixed(2)},${(100 - (v / max) * 100).toFixed(2)}`);
      } else if (current.length) {
        segments.push(current.join(" "));
        current = [];
      }
    });
    if (current.length) segments.push(current.join(" "));
    return { ...s, segments };
  });
  if (peak <= 0) {
    return <p className="flex h-[200px] items-center justify-center text-[12.5px] text-fg-muted">No measurements in this range.</p>;
  }
  return (
    <ChartFrame
      data={data}
      range={range}
      ticks={ticks}
      format={format}
      hover={hover}
      setHover={setHover}
      tooltip={(b) => series.map((s) => ({ label: s.label, value: format(s.value(b)), color: s.color }))}
    >
      <svg className="absolute inset-0 h-full w-full overflow-visible" viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label={label}>
        {paths.map((p) =>
          p.segments.map((pts, i) =>
            pts.includes(" ") ? (
              <polyline key={`${p.key}-${i}`} points={pts} fill="none" stroke={p.color} strokeWidth={1.75} vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
            ) : (
              <circle key={`${p.key}-${i}`} cx={pts.split(",")[0]} cy={pts.split(",")[1]} r={0.8} fill={p.color} />
            ),
          ),
        )}
      </svg>
    </ChartFrame>
  );
}

// ── Assembled view ───────────────────────────────────────────────────────────

const C = {
  ok: "var(--color-accent-500)",
  soft: "var(--color-accent-300)",
  alt: "var(--series-alt)",
  bad: "var(--status-bad)",
  warn: "var(--status-warn)",
};

export function HealthTrends({ snapshots, range }: { snapshots: HealthSnapshot[]; range: string }) {
  const data = useMemo(() => bucketSnapshots(snapshots, range), [snapshots, range]);
  const totals = data.reduce(
    (acc, b) => ({ requests: acc.requests + b.requests, failures: acc.failures + b.failures, fallbacks: acc.fallbacks + b.fallbacks }),
    { requests: 0, failures: 0, fallbacks: 0 },
  );
  if (!snapshots.length) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-10 text-center">
        <p className="text-[13px] font-medium text-fg">No trend data in this range</p>
        <p className="mt-1 text-[12.5px] text-fg-muted">Snapshots are written once requests flow through this provider or a probe runs.</p>
      </div>
    );
  }
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <ChartCard
        title="Requests"
        subtitle={`${totals.requests.toLocaleString("en-US")} total · ${totals.failures.toLocaleString("en-US")} failed`}
        legend={[
          { label: "Succeeded", color: C.ok },
          { label: "Failed", color: C.alt },
        ]}
        className="lg:col-span-2"
      >
        <StackedBars
          data={data}
          range={range}
          label="Requests over time"
          series={[
            { key: "ok", label: "Succeeded", color: C.ok, value: (b) => Math.max(0, b.requests - b.failures) },
            { key: "failed", label: "Failed", color: C.alt, value: (b) => b.failures },
          ]}
        />
      </ChartCard>

      <ChartCard
        title="Latency"
        subtitle="p50 is request-weighted; p95 and p99 are the worst value in each bucket"
        legend={[
          { label: "p50", color: C.soft },
          { label: "p95", color: C.ok },
          { label: "p99", color: C.alt },
        ]}
      >
        <Lines
          data={data}
          range={range}
          label="Latency percentiles over time"
          series={[
            { key: "p50", label: "p50", color: C.soft, value: (b) => b.p50 },
            { key: "p95", label: "p95", color: C.ok, value: (b) => b.p95 },
            { key: "p99", label: "p99", color: C.alt, value: (b) => b.p99 },
          ]}
        />
      </ChartCard>

      <ChartCard title="Time to first token" subtitle="Worst p95 per bucket, streaming requests only" legend={[{ label: "TTFT p95", color: C.ok }]}>
        <Lines data={data} range={range} label="Time to first token over time" series={[{ key: "ttft", label: "TTFT p95", color: C.ok, value: (b) => b.ttft95 }]} />
      </ChartCard>

      <ChartCard title="Error rate" subtitle="Failed share of requests in each bucket" legend={[{ label: "Errors", color: C.bad }]}>
        <StackedBars
          data={data}
          range={range}
          label="Error rate over time"
          format={(v) => `${Math.round(v * 10) / 10}%`}
          series={[{ key: "err", label: "Error rate", color: C.bad, value: (b) => (b.requests ? (b.failures / b.requests) * 100 : 0) }]}
        />
      </ChartCard>

      <ChartCard title="Fallbacks" subtitle={`${totals.fallbacks.toLocaleString("en-US")} requests moved to another target`} legend={[{ label: "Fell over", color: C.warn }]}>
        <StackedBars data={data} range={range} label="Fallbacks over time" series={[{ key: "fb", label: "Fell over", color: C.warn, value: (b) => b.fallbacks }]} />
      </ChartCard>
    </div>
  );
}

function ChartCard({
  title,
  subtitle,
  legend,
  className,
  children,
}: {
  title: string;
  subtitle?: string;
  legend?: { label: string; color: string }[];
  className?: string;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className={cn("overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-[13px] font-semibold text-fg">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
        </div>
        {legend && (
          <div className="flex flex-wrap gap-3 text-[12px] text-fg-muted">
            {legend.map((l) => (
              <span key={l.label} className="inline-flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-[2px]" style={{ background: l.color }} aria-hidden="true" />
                {l.label}
              </span>
            ))}
          </div>
        )}
      </div>
      <div className="px-4 pb-3 pt-4">{children}</div>
    </section>
  );
}

// ErrorTypeBreakdown ranks error classes in one list with proportional bars;
// rate limiting is a warning, everything else an error.
export function ErrorTypeBreakdown({ breakdown }: { breakdown: Record<string, number> }) {
  const rows = Object.entries(breakdown)
    .filter(([, v]) => v > 0)
    .sort((a, b) => b[1] - a[1]);
  const total = rows.reduce((n, [, v]) => n + v, 0);
  return (
    <section aria-label="Errors by type" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <h2 className="text-[13px] font-semibold text-fg">Errors by type</h2>
        <p className="mt-0.5 text-[12px] text-fg-muted">{total ? `${total.toLocaleString("en-US")} failed attempts in this range` : "No failed attempts in this range"}</p>
      </div>
      {rows.length === 0 ? (
        <p className="px-4 py-8 text-center text-[13px] text-fg-muted">Every attempt in this range succeeded.</p>
      ) : (
        <ul className="space-y-3 px-4 py-4">
          {rows.map(([k, v]) => (
            <li key={k}>
              <div className="mb-1 flex items-baseline justify-between gap-2 text-[13px]">
                <span className="text-fg">{k === "provider_5xx" ? "Provider 5xx" : fmtIssue(k)}</span>
                <span className="tabular-nums text-fg-muted">
                  {v.toLocaleString("en-US")} <span className="text-fg-faint">· {Math.round((v / total) * 100)}%</span>
                </span>
              </div>
              <div className="h-1.5 overflow-hidden rounded-full bg-track">
                <div className={cn("h-full rounded-full", k === "rate_limited" || k === "quota_exceeded" ? "bg-warn" : "bg-bad")} style={{ width: `${(v / total) * 100}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
