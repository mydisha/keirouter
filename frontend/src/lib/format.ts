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
