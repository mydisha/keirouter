import { useId, useMemo, useState, type KeyboardEvent } from "react";
import type { LucideIcon } from "lucide-react";
import type { SeriesPoint, UsageInsights } from "../../lib/api";
import { ICONS } from "../../lib/icons";
import { cn } from "@/lib/utils";

// TrafficCard is the shared requests / tokens / cost / latency bar chart used
// by Overview and Usage. Plain HTML bars (no charting library), stacked where
// a metric has two parts, with a hover tooltip.

// ── Traffic chart ────────────────────────────────────────────────────────────

type MetricKey = "requests" | "tokens" | "cost" | "latency";

interface MetricDef {
  label: string;
  icon: LucideIcon;
  legendA: string;
  legendB?: string;
  /** Secondary series colour: "alt" = orange (failures), "soft" = light accent. */
  bTone?: "alt" | "soft";
  a: (p: SeriesPoint) => number;
  b?: (p: SeriesPoint) => number;
  fmt: (v: number) => string;
  total: (d: UsageInsights) => string;
}

const METRICS: Record<MetricKey, MetricDef> = {
  requests: {
    label: "Requests",
    icon: ICONS.requests,
    legendA: "Succeeded",
    legendB: "Failed",
    bTone: "alt",
    a: (p) => Math.max(0, p.requests - p.failures),
    b: (p) => p.failures,
    fmt: (v) => fmtCompact(v),
    total: (d) => fmtInt(d.summary.total_requests),
  },
  tokens: {
    label: "Tokens",
    icon: ICONS.tokens,
    legendA: "Input",
    legendB: "Output",
    bTone: "soft",
    a: (p) => p.prompt_tokens,
    b: (p) => p.completion_tokens,
    fmt: (v) => fmtCompact(v),
    total: (d) => fmtCompact(d.summary.total_tokens),
  },
  cost: {
    label: "Cost",
    icon: ICONS.spend,
    legendA: "Billed",
    legendB: "Saved",
    bTone: "soft",
    a: (p) => p.cost_usd,
    b: (p) => p.saved_usd ?? 0,
    fmt: (v) => fmtUSD(v),
    total: (d) => fmtUSD(d.summary.cost_usd),
  },
  latency: {
    label: "Latency",
    icon: ICONS.latency,
    legendA: "Avg latency",
    a: (p) => p.avg_latency_ms ?? 0,
    fmt: (v) => fmtMs(v),
    total: (d) => (d.summary.p95_latency_ms ? `p95 ${fmtMs(d.summary.p95_latency_ms)}` : "—"),
  },
};
const METRIC_KEYS = Object.keys(METRICS) as MetricKey[];

export function TrafficCard({ data, className }: { data: UsageInsights; className?: string }) {
  const [metric, setMetric] = useState<MetricKey>("requests");
  const [hover, setHover] = useState<number | null>(null);
  const [viaKeys, setViaKeys] = useState(false);
  const uid = useId();
  const def = METRICS[metric];
  const series = data.series;

  const { max, ticks } = useMemo(() => {
    const peak = Math.max(0, ...series.map((p) => def.a(p) + (def.b ? def.b(p) : 0)));
    return niceScale(peak);
  }, [series, def]);

  const labelEvery = Math.max(1, Math.ceil(series.length / 7));
  // Series colours as classes (accent first, then the lightness-separated alt).
  const aClass = "bg-accent-500";
  const bClass = def.bTone === "alt" ? "bg-[var(--series-alt)]" : "bg-accent-300";
  const hovered = hover !== null ? series[hover] : null;
  const withDate = series.length > 30 || data.period !== "24h";

  // Screen-reader summary of the visible metric: range, peak and total.
  const summary = useMemo(() => {
    if (!series.length) return "No data in this period.";
    let peakIdx = 0;
    series.forEach((p, i) => {
      if (def.a(p) + (def.b ? def.b(p) : 0) > def.a(series[peakIdx]) + (def.b ? def.b(series[peakIdx]) : 0)) peakIdx = i;
    });
    const peakP = series[peakIdx];
    const peakText = def.b && def.legendB
      ? `${def.legendA} ${def.fmt(def.a(peakP))}, ${def.legendB} ${def.fmt(def.b(peakP))}`
      : def.fmt(def.a(peakP));
    return `${series.length} buckets from ${formatBucket(series[0].start, withDate)} to ${formatBucket(series[series.length - 1].start, withDate)}. Total ${def.total(data)}. Busiest at ${formatBucket(peakP.start, withDate)}: ${peakText}.`;
  }, [series, def, data, withDate]);

  const announcement = hovered && viaKeys
    ? `${formatBucket(hovered.start, withDate)}: ${def.legendA} ${def.fmt(def.a(hovered))}${def.b && def.legendB ? `, ${def.legendB} ${def.fmt(def.b(hovered))}` : ""}`
    : "";

  const onTabKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const i = METRIC_KEYS.indexOf(metric);
    const next =
      event.key === "Home" ? 0 : event.key === "End" ? METRIC_KEYS.length - 1 : (i + (event.key === "ArrowRight" ? 1 : -1) + METRIC_KEYS.length) % METRIC_KEYS.length;
    setMetric(METRIC_KEYS[next]);
    document.getElementById(`${uid}-tab-${METRIC_KEYS[next]}`)?.focus();
  };

  const onPlotKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!series.length) return;
    let next: number | null = null;
    if (event.key === "ArrowRight") next = hover == null ? 0 : Math.min(series.length - 1, hover + 1);
    else if (event.key === "ArrowLeft") next = hover == null ? series.length - 1 : Math.max(0, hover - 1);
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = series.length - 1;
    else if (event.key === "Escape" && hover != null) next = -1;
    if (next == null) return;
    event.preventDefault();
    setViaKeys(true);
    setHover(next < 0 ? null : next);
  };

  return (
    <section aria-label="Traffic" className={cn("flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-stretch border-b border-line">
        <div className="flex flex-wrap items-stretch" role="tablist" aria-label="Chart metric" onKeyDown={onTabKeyDown}>
          {METRIC_KEYS.map((key) => {
            const active = key === metric;
            const MetricIcon = METRICS[key].icon;
            return (
              <button
                key={key}
                id={`${uid}-tab-${key}`}
                type="button"
                role="tab"
                aria-selected={active}
                aria-controls={`${uid}-panel`}
                tabIndex={active ? 0 : -1}
                onClick={() => setMetric(key)}
                className={cn(
                  "relative flex min-w-[128px] flex-col items-start gap-0.5 border-r border-line px-4 py-2.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500",
                  active ? "bg-surface" : "bg-subtle hover:bg-hover",
                )}
              >
                <span className="inline-flex items-center gap-1.5 text-[12px] font-medium text-fg-muted">
                  <MetricIcon className={cn("h-3.5 w-3.5 shrink-0", active ? "text-tone" : "text-fg-faint")} strokeWidth={1.75} aria-hidden="true" />
                  {METRICS[key].label}
                </span>
                <span className={cn("text-[16px] font-semibold tracking-[-0.01em] tabular-nums", active ? "text-fg" : "text-fg-muted")}>
                  {METRICS[key].total(data)}
                </span>
                {active && <span className="absolute inset-x-0 bottom-0 h-0.5 bg-accent-500" aria-hidden="true" />}
              </button>
            );
          })}
        </div>
        <div className="ml-auto flex items-center gap-3.5 px-4 py-2 text-[12px] text-fg-muted">
          <LegendSwatch className={aClass} label={def.legendA} />
          {def.legendB && <LegendSwatch className={bClass} label={def.legendB} />}
        </div>
      </div>

      <div id={`${uid}-panel`} role="tabpanel" aria-labelledby={`${uid}-tab-${metric}`} className="flex flex-1 gap-2.5 px-4 pb-3 pt-4">
        <div className="flex h-[220px] min-w-9 flex-col justify-between text-right text-[11px] tabular-nums text-fg-faint" aria-hidden="true">
          {ticks.map((t) => (
            <span key={t} className="-translate-y-1/2 first:translate-y-0 last:translate-y-0">{def.fmt(t)}</span>
          ))}
        </div>
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div
            className="relative h-[220px] rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-4 focus-visible:ring-offset-surface"
            tabIndex={0}
            role="group"
            aria-roledescription="chart"
            aria-label={`${def.label} over the selected period. Use left and right arrow keys to read values.`}
            aria-describedby={`${uid}-summary`}
            onKeyDown={onPlotKeyDown}
            onBlur={() => setHover(null)}
            onMouseLeave={() => setHover(null)}
            onMouseMove={() => viaKeys && setViaKeys(false)}
          >
            <span id={`${uid}-summary`} className="sr-only">{summary}</span>
            <span className="sr-only" aria-live="polite" aria-atomic="true">{announcement}</span>
            {[0, 25, 50, 75].map((top) => (
              <div key={top} className="absolute inset-x-0 border-t border-dashed border-line" style={{ top: `${top}%` }} aria-hidden="true" />
            ))}
            <div className="absolute inset-x-0 bottom-0 border-t border-line-strong" aria-hidden="true" />
            <div className="absolute inset-0 flex items-end gap-[3px]" aria-hidden="true">
              {series.map((p, i) => {
                const a = def.a(p);
                const b = def.b ? def.b(p) : 0;
                return (
                  <div
                    key={p.start}
                    className={cn("flex h-full min-w-[2px] flex-1 flex-col justify-end gap-px", hover !== null && hover !== i && "opacity-60")}
                    onMouseEnter={() => setHover(i)}
                  >
                    {b > 0 && <div className={cn("rounded-t-[2px]", bClass)} style={{ height: `${(b / max) * 100}%` }} />}
                    <div className={cn(aClass, b > 0 ? "rounded-[1px]" : "rounded-t-[2px]")} style={{ height: `${(a / max) * 100}%` }} />
                  </div>
                );
              })}
            </div>
            {hovered && (
              <ChartTooltip
                leftPct={((hover! + 0.5) / series.length) * 100}
                title={formatBucket(hovered.start, withDate)}
                rows={[
                  { label: def.legendA, value: def.fmt(def.a(hovered)), className: aClass },
                  ...(def.b && def.legendB ? [{ label: def.legendB, value: def.fmt(def.b(hovered)), className: bClass }] : []),
                ]}
              />
            )}
          </div>
          <div className="relative h-4 text-[11px] tabular-nums text-fg-faint" aria-hidden="true">
            {series.map((p, i) =>
              i % labelEvery === 0 ? (
                <span key={p.start} className="absolute -translate-x-0" style={{ left: `${(i / series.length) * 100}%` }}>
                  {p.label}
                </span>
              ) : null,
            )}
          </div>
        </div>
      </div>

      {data.busiest && (
        <div className="border-t border-line bg-subtle px-4 py-2.5 text-[12px] text-fg-muted">
          Busiest bucket <span className="font-medium text-fg">{data.busiest}</span>
          {data.summary.cache_hits > 0 && (
            <>
              {" "}· <span className="tabular-nums text-fg">{fmtInt(data.summary.cache_hits)}</span> served from the semantic cache
            </>
          )}
        </div>
      )}
    </section>
  );
}

function LegendSwatch({ className, label }: { className: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={cn("h-2 w-2 rounded-[2px]", className)} aria-hidden="true" />
      {label}
    </span>
  );
}

function ChartTooltip({ leftPct, title, rows }: { leftPct: number; title: string; rows: { label: string; value: string; className: string }[] }) {
  const alignRight = leftPct > 70;
  return (
    <>
      <div
        aria-hidden="true"
        className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-xl border border-line bg-surface px-2.5 py-2 text-[12px] shadow-[var(--shadow-pop)]"
        style={alignRight ? { right: `${100 - leftPct}%`, marginRight: 8 } : { left: `${leftPct}%`, marginLeft: 8 }}
      >
        <p className="mb-1 font-medium text-fg">{title}</p>
        {rows.map((r) => (
          <p key={r.label} className="flex items-center gap-2 text-fg-muted">
            <span className={cn("h-2 w-2 rounded-[2px]", r.className)} />
            {r.label}
            <span className="ml-auto pl-3 font-medium tabular-nums text-fg">{r.value}</span>
          </p>
        ))}
      </div>
    </>
  );
}

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

function niceScale(peak: number): { max: number; ticks: number[] } {
  if (!(peak > 0)) return { max: 1, ticks: [1, 0.75, 0.5, 0.25, 0] };
  const rough = peak / 4;
  const pow = Math.pow(10, Math.floor(Math.log10(rough)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= rough) ?? 10 * pow;
  const max = step * 4;
  return { max, ticks: [max, step * 3, step * 2, step, 0] };
}

function formatBucket(iso: string, withDate: boolean): string {
  const d = new Date(iso);
  return withDate
    ? d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
    : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}
