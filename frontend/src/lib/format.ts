// Shared display formatters. Previously duplicated across Budgets, Overview,
// KeyPortal, and Usage — consolidated so spend/token formatting stays consistent.

export function microsToUSD(micros: number, decimals = 2): string {
  return formatUSD(micros / 1_000_000, decimals);
}

export function formatUSD(usd: number, decimals = 2): string {
  return `$${usd.toFixed(decimals)}`;
}

// Spend amounts always show micro-USD precision (6 decimals, matching the DB
// column) so every recorded charge is fully auditable — no sub-cent value
// collapses to "$0.00".
export function formatSpendUSD(usd: number): string {
  return `$${usd.toFixed(6)}`;
}

// Cost amounts shown to portal users: dynamic precision so a cheap request is
// never rounded away. Sub-cent values keep three significant digits
// ($0.0000121), cents show four decimals, dollars two with grouping.
export function formatCostUSD(usd: number): string {
  if (!Number.isFinite(usd) || usd === 0) return "$0.00";
  const sign = usd < 0 ? "-" : "";
  const abs = Math.abs(usd);
  if (abs >= 1) {
    return `${sign}$${abs.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  }
  if (abs >= 0.01) return `${sign}$${abs.toFixed(4)}`;
  const decimals = Math.min(12, Math.ceil(-Math.log10(abs)) + 2);
  const text = abs.toFixed(decimals).replace(/0+$/, "").replace(/\.$/, "");
  return `${sign}$${text}`;
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
  if (!d) return "—";
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", ...(opts.short && sameYear ? {} : { year: "numeric" }) });
}

/** "Oct 12, 2026, 14:03:22": the full timestamp, for tooltips and detail rows. */
export function formatDateTime(value: string | number | Date | null | undefined, opts: { seconds?: boolean } = { seconds: true }): string {
  const d = toDate(value);
  if (!d) return "—";
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
