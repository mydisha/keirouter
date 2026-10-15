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
// sentence case with spaces.
const ISSUE_LABELS: Record<string, string> = {
  rate_limited: "Rate limited",
  auth_error: "Auth error",
  quota_exceeded: "Quota exceeded",
  timeout: "Timeout",
  provider_5xx: "Provider 5xx",
  bad_request: "Bad request",
  network_error: "Network error",
  unsupported_model_or_capability: "Unsupported model",
  unknown_error: "Unknown error",
  high_latency: "High latency",
  fallback_spike: "Fallback spike",
};

export function fmtIssue(issue?: string): string {
  if (!issue) return "";
  if (ISSUE_LABELS[issue]) return ISSUE_LABELS[issue];
  const words = issue.split("_").filter(Boolean).join(" ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

const STATUS_TONE: Record<HealthStatus, string> = {
  // Mirrors ui Badge tones (success / warning / danger / neutral).
  healthy: "border-ok/20 bg-ok/10 text-ok",
  degraded: "border-warn/25 bg-warn/12 text-warn",
  unhealthy: "border-bad/20 bg-bad/10 text-bad",
  unknown: "border-line bg-subtle text-fg-muted",
  disabled: "border-line bg-subtle text-fg-muted",
};

const STATUS_DOT: Record<HealthStatus, string> = {
  healthy: "bg-ok",
  degraded: "bg-warn",
  unhealthy: "bg-bad",
  unknown: "bg-fg-faint",
  disabled: "bg-fg-faint",
};

export function HealthStatusBadge({ status, issue }: { status: HealthStatus; issue?: string }) {
  return (
    <span
      className={`inline-flex h-5 items-center gap-1.5 whitespace-nowrap rounded-md border px-1.5 text-[11.5px] font-medium ${STATUS_TONE[status]}`}
      title={issue || STATUS_LABEL[status]}
    >
      <span className={`inline-block h-1.5 w-1.5 rounded-full ${STATUS_DOT[status]}`} aria-hidden="true" />
      {STATUS_LABEL[status]}
    </span>
  );
}

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
