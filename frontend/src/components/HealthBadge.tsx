import type { HealthStatus } from "../lib/api";

const STATUS_LABEL: Record<HealthStatus, string> = {
  healthy: "Healthy",
  degraded: "Degraded",
  unhealthy: "Unhealthy",
  unknown: "Unknown",
  disabled: "Disabled",
};

// fmtIssue converts a snake_case issue/error-type label to a human-readable
// phrase. Known issues get explicit friendly names; unknown ones fall back to
// Title Case with spaces.
const ISSUE_LABELS: Record<string, string> = {
  rate_limited: "Rate Limited",
  auth_error: "Auth Error",
  quota_exceeded: "Quota Exceeded",
  timeout: "Timeout",
  provider_5xx: "Provider 5xx",
  bad_request: "Bad Request",
  network_error: "Network Error",
  unsupported_model_or_capability: "Unsupported Model",
  unknown_error: "Unknown Error",
  high_latency: "High Latency",
  fallback_spike: "Fallback Spike",
};

export function fmtIssue(issue?: string): string {
  if (!issue) return "";
  if (ISSUE_LABELS[issue]) return ISSUE_LABELS[issue];
  return issue
    .split("_")
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

const STATUS_TONE: Record<HealthStatus, string> = {
  healthy: "bg-ok/10 text-ok",
  degraded: "bg-[color:var(--color-warning)]/15 text-[color:var(--color-warning)]",
  unhealthy: "bg-[color:var(--color-danger)]/15 text-[color:var(--color-danger)]",
  unknown: "bg-ink-100 text-ink-500 dark:bg-ink-800 dark:text-ink-400",
  disabled: "bg-ink-100 text-ink-500 dark:bg-ink-800 dark:text-ink-400",
};

const STATUS_DOT: Record<HealthStatus, string> = {
  healthy: "bg-ok",
  degraded: "bg-[color:var(--color-warning)]",
  unhealthy: "bg-[color:var(--color-danger)]",
  unknown: "bg-ink-400",
  disabled: "bg-ink-400",
};

export function HealthStatusBadge({ status, issue }: { status: HealthStatus; issue?: string }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium ${STATUS_TONE[status]}`}
      title={issue || STATUS_LABEL[status]}
    >
      <span className={`inline-block h-1.5 w-1.5 rounded-full ${STATUS_DOT[status]}`} />
      {STATUS_LABEL[status]}
    </span>
  );
}

// HealthScoreRing renders a compact circular gauge for the 0-100 score.
// HealthScoreRing shows the 0–100 health score as a compact number with a
// short bar; colour carries meaning only below the healthy threshold.
export function HealthScoreRing({ score }: { score: number; size?: number }) {
  const pct = Math.max(0, Math.min(100, score));
  const tone = score >= 90 ? "bg-ok" : score >= 65 ? "bg-warn" : "bg-bad";
  const text = score >= 90 ? "text-fg" : score >= 65 ? "text-warn" : "text-bad";
  return (
    <span className="inline-flex items-center gap-2" role="img" aria-label={`Health score ${score}`}>
      <span className={`w-7 text-right text-[13px] font-medium tabular-nums ${text}`}>{score}</span>
      <span className="h-1.5 w-12 overflow-hidden rounded-full bg-track" aria-hidden="true">
        <span className={`block h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </span>
    </span>
  );
}
