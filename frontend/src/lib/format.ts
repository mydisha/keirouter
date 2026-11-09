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

export function formatTokens(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
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
