import { useId, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { cn } from "@/lib/utils";

// Dependency-free time-series charts shared by Provider Health, System and
// the key portal. Plain HTML bars / SVG lines in the dashboard's visual
// language: dashed gridlines, nice-rounded y ticks, ~6 x labels, a hover
// crosshair with a tooltip, and colour only from the series definitions.
//
// Accessibility: the plot is focusable; ← → / Home / End move the crosshair
// (the tooltip text is announced through a live region), and a visually
// hidden summary gives the range, peak and latest value of each series.

export interface TimeSeriesDef<T> {
  key: string;
  label: string;
  color: string;
  value: (d: T) => number;
}

interface ChartProps<T> {
  data: T[];
  /** Point time in epoch milliseconds. */
  x: (d: T) => number;
  series: TimeSeriesDef<T>[];
  /** Y tick / tooltip value formatter. */
  format?: (v: number) => string;
  /** X label formatter; defaults to HH:MM. */
  xFormat?: (ms: number) => string;
  /** Tooltip time formatter; defaults to xFormat. */
  tooltipTime?: (ms: number) => string;
  /** Accessible description of the chart. */
  label: string;
  height?: number;
  /** Fixed y maximum (e.g. 100 for percentages). */
  max?: number;
  /** Dashed reference line, e.g. an alert threshold. */
  threshold?: { value: number; label: string };
  /** Replaces the generated screen-reader summary. */
  summary?: string;
}

// seriesSummary is the sr-only text description: time range, then peak and
// latest value for every series.
export function seriesSummary<T>(
  data: T[],
  x: (d: T) => number,
  series: { label: string; value: (d: T) => number }[],
  format: (v: number) => string,
  time: (ms: number) => string,
): string {
  if (!data.length) return "No data.";
  const parts = [`${data.length} points from ${time(x(data[0]))} to ${time(x(data[data.length - 1]))}.`];
  for (const s of series) {
    let peakIdx = 0;
    data.forEach((d, i) => {
      if (s.value(d) > s.value(data[peakIdx])) peakIdx = i;
    });
    const latest = s.value(data[data.length - 1]);
    parts.push(`${s.label}: peak ${format(s.value(data[peakIdx]))} at ${time(x(data[peakIdx]))}, latest ${format(latest)}.`);
  }
  return parts.join(" ");
}

// useChartKeys moves a crosshair index with the arrow keys.
export function useChartKeys(length: number, hover: number | null, setHover: (i: number | null) => void) {
  return (event: ReactKeyboardEvent<HTMLElement>) => {
    if (!length) return;
    let next: number | null = null;
    if (event.key === "ArrowRight") next = hover == null ? 0 : Math.min(length - 1, hover + 1);
    else if (event.key === "ArrowLeft") next = hover == null ? length - 1 : Math.max(0, hover - 1);
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = length - 1;
    else if (event.key === "Escape" && hover != null) next = -1;
    if (next == null) return;
    event.preventDefault();
    setHover(next < 0 ? null : next);
  };
}

export function niceScale(peak: number, fixedMax?: number): { max: number; ticks: number[] } {
  if (fixedMax && fixedMax > 0) {
    const step = fixedMax / 4;
    return { max: fixedMax, ticks: [fixedMax, step * 3, step * 2, step, 0] };
  }
  if (!(peak > 0)) return { max: 1, ticks: [1, 0.5, 0] };
  const rough = peak / 4;
  const pow = Math.pow(10, Math.floor(Math.log10(rough)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= rough) ?? 10 * pow;
  return { max: step * 4, ticks: [step * 4, step * 3, step * 2, step, 0] };
}

export const clockLabel = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
export const dayLabel = (ms: number) => new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric" });

function compact(v: number): string {
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(v >= 1e4 ? 0 : 1)}k`;
  return String(Math.round(v * 10) / 10);
}

function Frame<T>({
  data,
  x,
  ticks,
  max,
  format,
  xFormat,
  tooltipTime,
  series,
  hover,
  setHover,
  height,
  threshold,
  label,
  summary,
  children,
}: {
  label: string;
  summary: string;
  data: T[];
  x: (d: T) => number;
  ticks: number[];
  max: number;
  format: (v: number) => string;
  xFormat: (ms: number) => string;
  tooltipTime: (ms: number) => string;
  series: TimeSeriesDef<T>[];
  hover: number | null;
  setHover: (i: number | null) => void;
  height: number;
  threshold?: { value: number; label: string };
  children: ReactNode;
}) {
  const every = Math.max(1, Math.ceil(data.length / 6));
  const hovered = hover != null ? data[hover] : null;
  const left = hover != null ? ((hover + 0.5) / data.length) * 100 : 0;
  const summaryId = useId();
  const moveByKey = useChartKeys(data.length, hover, setHover);
  // Only keyboard moves are announced; mouse hover stays silent.
  const [viaKeys, setViaKeys] = useState(false);
  const onKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    setViaKeys(true);
    moveByKey(event);
  };
  const announcement = hovered && viaKeys
    ? `${tooltipTime(x(hovered))}: ${series.map((s) => `${s.label} ${format(s.value(hovered))}`).join(", ")}`
    : "";
  return (
    <div className="flex gap-2.5">
      <div className="flex min-w-10 flex-col justify-between text-right text-[11px] tabular-nums text-fg-faint" style={{ height }} aria-hidden="true">
        {ticks.map((t) => (
          <span key={t} className="-translate-y-1/2 first:translate-y-0 last:translate-y-0">
            {t === 0 ? "0" : format(t)}
          </span>
        ))}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div
          className="relative rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-4 focus-visible:ring-offset-surface"
          style={{ height }}
          tabIndex={0}
          role="group"
          aria-roledescription="chart"
          aria-label={`${label}. Use left and right arrow keys to read values.`}
          aria-describedby={summaryId}
          onKeyDown={onKeyDown}
          onBlur={() => setHover(null)}
          onMouseLeave={() => setHover(null)}
          onMouseMove={(e) => {
            if (viaKeys) setViaKeys(false);
            const r = e.currentTarget.getBoundingClientRect();
            setHover(Math.max(0, Math.min(data.length - 1, Math.floor(((e.clientX - r.left) / r.width) * data.length))));
          }}
        >
          {[0, 25, 50, 75].map((top) => (
            <div key={top} className="absolute inset-x-0 border-t border-dashed border-line" style={{ top: `${top}%` }} aria-hidden="true" />
          ))}
          <div className="absolute inset-x-0 bottom-0 border-t border-line-strong" aria-hidden="true" />
          {threshold && threshold.value < max && (
            <div className="absolute inset-x-0 border-t border-dashed border-warn/70" style={{ top: `${100 - (threshold.value / max) * 100}%` }} aria-hidden="true">
              <span className="absolute right-0 -translate-y-full pb-0.5 text-[10.5px] text-warn">{threshold.label}</span>
            </div>
          )}
          <span id={summaryId} className="sr-only">{summary}</span>
          <span className="sr-only" aria-live="polite" aria-atomic="true">{announcement}</span>
          {children}
          {hovered && (
            <>
              <div className="pointer-events-none absolute inset-y-0 w-px bg-line-strong" style={{ left: `${left}%` }} aria-hidden="true" />
              <div
                aria-hidden="true"
                className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-xl border border-line bg-surface px-2.5 py-2 text-[12px] shadow-[var(--shadow-pop)]"
                style={left > 65 ? { right: `${100 - left}%`, marginRight: 8 } : { left: `${left}%`, marginLeft: 8 }}
              >
                <p className="mb-1 font-medium text-fg">{tooltipTime(x(hovered))}</p>
                {series.map((s) => (
                  <p key={s.key} className="flex items-center gap-2 text-fg-muted">
                    <span className="h-2 w-2 rounded-[2px]" style={{ background: s.color }} />
                    {s.label}
                    <span className="ml-auto pl-3 font-medium tabular-nums text-fg">{format(s.value(hovered))}</span>
                  </p>
                ))}
              </div>
            </>
          )}
        </div>
        <div className="relative h-4 text-[11px] tabular-nums text-fg-faint" aria-hidden="true">
          {data.map((d, i) =>
            i % every === 0 ? (
              <span key={i} className="absolute" style={{ left: `${(i / data.length) * 100}%` }}>
                {xFormat(x(d))}
              </span>
            ) : null,
          )}
        </div>
      </div>
    </div>
  );
}

function EmptyChart({ height }: { height: number }) {
  return (
    <p className="flex items-center justify-center text-[12.5px] text-fg-muted" style={{ height: height + 24 }} role="status">
      No measurements in this range.
    </p>
  );
}

/** Stacked bars, one column per point. */
export function TimeBars<T>({ data, x, series, format = compact, xFormat = clockLabel, tooltipTime, label, height = 180, max: fixedMax, threshold, summary }: ChartProps<T>) {
  const [hover, setHover] = useState<number | null>(null);
  const peak = useMemo(() => Math.max(0, ...data.map((d) => series.reduce((s, x2) => s + x2.value(d), 0))), [data, series]);
  const { max, ticks } = niceScale(peak, fixedMax);
  const text = useMemo(() => summary ?? seriesSummary(data, x, series, format, tooltipTime ?? xFormat), [summary, data, x, series, format, tooltipTime, xFormat]);
  if (!data.length || peak <= 0) return <EmptyChart height={height} />;
  return (
    <Frame label={label} summary={text} data={data} x={x} ticks={ticks} max={max} format={format} xFormat={xFormat} tooltipTime={tooltipTime ?? xFormat} series={series} hover={hover} setHover={setHover} height={height} threshold={threshold}>
      <div className="absolute inset-0 flex items-end gap-[2px]" aria-hidden="true">
        {data.map((d, i) => (
          <div key={i} className={cn("flex h-full min-w-[2px] flex-1 flex-col-reverse gap-px", hover != null && hover !== i && "opacity-60")}>
            {series.map((s) => {
              const v = s.value(d);
              return v > 0 ? <div key={s.key} className="rounded-[1.5px]" style={{ height: `${(v / max) * 100}%`, background: s.color }} /> : null;
            })}
          </div>
        ))}
      </div>
    </Frame>
  );
}

/** Lines; points with a value ≤ 0 break the line rather than drop to zero. */
export function TimeLines<T>({ data, x, series, format = compact, xFormat = clockLabel, tooltipTime, label, height = 180, max: fixedMax, threshold, summary }: ChartProps<T> & { fill?: boolean }) {
  const [hover, setHover] = useState<number | null>(null);
  const peak = useMemo(() => Math.max(0, ...data.flatMap((d) => series.map((s) => s.value(d)))), [data, series]);
  const { max, ticks } = niceScale(peak, fixedMax);
  const text = useMemo(() => summary ?? seriesSummary(data, x, series, format, tooltipTime ?? xFormat), [summary, data, x, series, format, tooltipTime, xFormat]);
  if (!data.length || peak <= 0) return <EmptyChart height={height} />;
  const paths = series.map((s) => {
    const segments: string[][] = [];
    let current: string[] = [];
    data.forEach((d, i) => {
      const v = s.value(d);
      if (v > 0) current.push(`${(((i + 0.5) / data.length) * 100).toFixed(2)},${(100 - (Math.min(v, max) / max) * 100).toFixed(2)}`);
      else if (current.length) {
        segments.push(current);
        current = [];
      }
    });
    if (current.length) segments.push(current);
    return { ...s, segments };
  });
  return (
    <Frame label={label} summary={text} data={data} x={x} ticks={ticks} max={max} format={format} xFormat={xFormat} tooltipTime={tooltipTime ?? xFormat} series={series} hover={hover} setHover={setHover} height={height} threshold={threshold}>
      <svg className="absolute inset-0 h-full w-full overflow-visible" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true" focusable="false">
        {paths.map((p) =>
          p.segments.map((pts, i) =>
            pts.length > 1 ? (
              <polyline key={`${p.key}-${i}`} points={pts.join(" ")} fill="none" stroke={p.color} strokeWidth={1.75} vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
            ) : (
              <circle key={`${p.key}-${i}`} cx={pts[0].split(",")[0]} cy={pts[0].split(",")[1]} r={0.8} fill={p.color} />
            ),
          ),
        )}
      </svg>
    </Frame>
  );
}

/** Card chrome shared by the chart pages. */
export function ChartCard({
  title,
  subtitle,
  legend,
  action,
  className,
  children,
}: {
  title: string;
  subtitle?: ReactNode;
  legend?: { label: string; color: string }[];
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const titleId = useId();
  return (
    <section aria-labelledby={titleId} className={cn("min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-3">
        <div className="min-w-0">
          <h2 id={titleId} className="text-[13px] font-semibold text-fg">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
        </div>
        <div className="flex flex-wrap items-center gap-3">
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
          {action}
        </div>
      </div>
      <div className="px-4 pb-3 pt-4">{children}</div>
    </section>
  );
}
