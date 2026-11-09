// Shared display formatters. Previously duplicated across Budgets, Overview,
// KeyPortal, and Usage — consolidated so spend/token formatting stays consistent.

export function microsToUSD(micros: number, decimals = 2): string {
  return formatUSD(micros / 1_000_000, decimals);
}

export function formatUSD(usd: number, decimals = 2): string {
  return `$${usd.toFixed(decimals)}`;
}

// Picks a decimal count that keeps small spend amounts legible: sub-cent values
// show more digits so a $0.0042 charge doesn't collapse to "$0.00".
export function formatSpendUSD(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  if (usd < 1) return `$${usd.toFixed(3)}`;
  return `$${usd.toFixed(2)}`;
}

/** Token counts: 9,812 below ten thousand, then 12.4K, 1.2M, 3.4B. */
export function formatTokens(n: number): string {
  if (Math.abs(n) < 10_000) return new Intl.NumberFormat("en-US").format(n);
  return new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

export function compactNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

// ── Dates ────────────────────────────────────────────────────────────────────
// Numeric dates ("10/12/2026") read differently per locale, so the dashboard
// always spells the month: "Oct 12, 2026", "Oct 12, 14:03".

function toDate(value: string | number | Date | null | undefined): Date | null {
  if (value == null || value === "") return null;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** "Oct 12, 2026" (the year is dropped for dates in the current year when `short`). */
export function formatDate(value: string | number | Date | null | undefined, opts: { short?: boolean } = {}): string {
  const d = toDate(value);
  if (!d) return "n/a";
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", ...(opts.short && sameYear ? {} : { year: "numeric" }) });
}

/** "Oct 12, 2026, 14:03:22": the full timestamp, for tooltips and detail rows. */
export function formatDateTime(value: string | number | Date | null | undefined, opts: { seconds?: boolean } = { seconds: true }): string {
  const d = toDate(value);
  if (!d) return "n/a";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    ...(opts.seconds ? { second: "2-digit" } : {}),
    hour12: false,
  });
}

// ── Period-over-period deltas ────────────────────────────────────────────────
// Shared by every KPI that compares against the previous period.

export type DeltaTone = "good" | "bad" | "flat";
export interface Delta {
  text: string;
  tone: DeltaTone;
  /** Direction of the change, independent of whether it is good. */
  dir: "up" | "down" | "flat";
}

/** Relative change in percent. `mode` says which direction is good. */
export function relDelta(cur: number, prev: number | undefined, mode: "flat" | "lower-better" | "higher-better"): Delta | null {
  if (!prev || !Number.isFinite(prev) || prev <= 0 || !cur) return null;
  const change = (cur - prev) / prev;
  const text = `${change >= 0 ? "+" : "−"}${Math.abs(change * 100).toFixed(1)}%`;
  const dir = Math.abs(change) < 0.005 ? "flat" : change > 0 ? "up" : "down";
  if (dir === "flat" || mode === "flat") return { text, tone: "flat", dir };
  const better = mode === "lower-better" ? change < 0 : change > 0;
  return { text, tone: better ? "good" : "bad", dir };
}

/** Change of a ratio in percentage points (success rate, cache hit rate). */
export function ptsDelta(cur: number, prev: number): Delta {
  const diff = (cur - prev) * 100;
  const text = `${diff >= 0 ? "+" : "−"}${Math.abs(diff).toFixed(2)} pts`;
  if (Math.abs(diff) < 0.01) return { text, tone: "flat", dir: "flat" };
  return { text, tone: diff > 0 ? "good" : "bad", dir: diff > 0 ? "up" : "down" };
}

/** "3 minutes ago", "yesterday"; "Never" for a missing time. */
export function formatRelative(value: string | number | Date | null | undefined): string {
  if (value == null || value === "") return "Never";
  const seconds = Math.round((new Date(value).getTime() - Date.now()) / 1000);
  const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 31_536_000],
    ["month", 2_592_000],
    ["day", 86_400],
    ["hour", 3_600],
    ["minute", 60],
  ];
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit);
  }
  return "just now";
}

/** "840 ms", "1.2 s"; muted "n/a" for a missing value. */
export function formatMs(ms: number | null | undefined): string {
  if (ms == null) return "n/a";
  if (Math.round(ms) < 1000) return `${Math.round(ms)} ms`;
  return `${new Intl.NumberFormat("en-US", { maximumFractionDigits: 1 }).format(ms / 1000)} s`;
}
