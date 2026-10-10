import { useId, useMemo } from "react";
import { Hourglass } from "lucide-react";
import { ChartCard, TimeBars, TimeLines, clockLabel, dayLabel } from "./charts/TimeSeries";
import type { HealthSnapshot } from "../lib/api";
import { cn } from "@/lib/utils";
import { ICONS } from "@/lib/icons";
import { IconTile, SectionTitle } from "./ui";
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

export function fmtMs(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "—";
  return v < 1000 ? `${Math.round(v)} ms` : `${(v / 1000).toFixed(v < 10_000 ? 2 : 1)} s`;
}

const isLongRange = (range: string) => range === "7d" || range === "30d";
const tickFor = (range: string) => (isLongRange(range) ? dayLabel : clockLabel);
const tipFor = (range: string) => (ms: number) =>
  new Date(ms).toLocaleString(undefined, isLongRange(range) ? { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" } : { hour: "2-digit", minute: "2-digit" });

// ── Assembled view ───────────────────────────────────────────────────────────

// Series colours are CSS variables so they follow the white-label accent
// palette; the chart kit takes colour strings, not classes.
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
      <div className="flex flex-col items-center rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-10 text-center">
        <IconTile icon={ICONS.trend} size="lg" className="mb-3" />
        <p className="text-[13px] font-medium text-fg">No trend data in this range</p>
        <p className="mt-1 text-[12.5px] text-fg-muted">Trends appear once requests or probes run.</p>
      </div>
    );
  }
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <ChartCard
        icon={ICONS.requests}
        title="Requests"
        subtitle={`${totals.requests.toLocaleString("en-US")} total · ${totals.failures.toLocaleString("en-US")} failed`}
        legend={[
          { label: "Succeeded", color: C.ok },
          { label: "Failed", color: C.alt },
        ]}
        className="lg:col-span-2"
      >
        <TimeBars
          data={data}
          x={(b) => b.start}
          xFormat={tickFor(range)}
          tooltipTime={tipFor(range)}
          label="Requests over time"
          series={[
            { key: "ok", label: "Succeeded", color: C.ok, value: (b) => Math.max(0, b.requests - b.failures) },
            { key: "failed", label: "Failed", color: C.alt, value: (b) => b.failures },
          ]}
        />
      </ChartCard>

      <ChartCard
        icon={ICONS.latency}
        title="Latency"
        subtitle="Worst p95 and p99 per bucket"
        legend={[
          { label: "p50", color: C.soft },
          { label: "p95", color: C.ok },
          { label: "p99", color: C.alt },
        ]}
      >
        <TimeLines
          data={data}
          x={(b) => b.start}
          format={fmtMs}
          xFormat={tickFor(range)}
          tooltipTime={tipFor(range)}
          label="Latency percentiles over time"
          series={[
            { key: "p50", label: "p50", color: C.soft, value: (b) => b.p50 },
            { key: "p95", label: "p95", color: C.ok, value: (b) => b.p95 },
            { key: "p99", label: "p99", color: C.alt, value: (b) => b.p99 },
          ]}
        />
      </ChartCard>

      <ChartCard icon={Hourglass} title="Time to first token" subtitle="Streaming requests, worst p95 per bucket" legend={[{ label: "TTFT p95", color: C.ok }]}>
        <TimeLines data={data} x={(b) => b.start} format={fmtMs} xFormat={tickFor(range)} tooltipTime={tipFor(range)} label="Time to first token over time" series={[{ key: "ttft", label: "TTFT p95", color: C.ok, value: (b) => b.ttft95 }]} />
      </ChartCard>

      <ChartCard icon={ICONS.errors} title="Error rate" legend={[{ label: "Errors", color: C.bad }]}>
        <TimeBars
          data={data}
          x={(b) => b.start}
          xFormat={tickFor(range)}
          tooltipTime={tipFor(range)}
          label="Error rate over time"
          format={(v) => `${Math.round(v * 10) / 10}%`}
          series={[{ key: "err", label: "Error rate", color: C.bad, value: (b) => (b.requests ? (b.failures / b.requests) * 100 : 0) }]}
        />
      </ChartCard>

      <ChartCard icon={ICONS.fallbacks} title="Fallbacks" subtitle={`${totals.fallbacks.toLocaleString("en-US")} moved to another target`} legend={[{ label: "Fell over", color: C.warn }]}>
        <TimeBars data={data} x={(b) => b.start} xFormat={tickFor(range)} tooltipTime={tipFor(range)} label="Fallbacks over time" series={[{ key: "fb", label: "Fell over", color: C.warn, value: (b) => b.fallbacks }]} />
      </ChartCard>
    </div>
  );
}

// ErrorTypeBreakdown ranks error classes in one list with proportional bars;
// rate limiting is a warning, everything else an error.
export function ErrorTypeBreakdown({ breakdown }: { breakdown: Record<string, number> }) {
  const rows = Object.entries(breakdown)
    .filter(([, v]) => v > 0)
    .sort((a, b) => b[1] - a[1]);
  const total = rows.reduce((n, [, v]) => n + v, 0);
  const titleId = useId();
  return (
    <section aria-labelledby={titleId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle
          id={titleId}
          icon={ICONS.errors}
          title="Errors by type"
          subtitle={total > 0 ? <span className="tabular-nums">{total.toLocaleString("en-US")} failed attempts</span> : undefined}
        />
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
              <div className="h-1.5 overflow-hidden rounded-full bg-track" aria-hidden="true">
                <div className={cn("h-full rounded-full", k === "rate_limited" || k === "quota_exceeded" ? "bg-warn" : "bg-bad")} style={{ width: `${(v / total) * 100}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
