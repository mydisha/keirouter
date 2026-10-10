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
export function HealthScoreRing({ score, size = 44 }: { score: number; size?: number }) {
  const stroke = 4;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const pct = Math.max(0, Math.min(100, score));
  const offset = c - (pct / 100) * c;
  const color =
    score >= 90 ? "var(--color-accent-500)" : score >= 65 ? "var(--color-warning)" : "var(--color-danger)";
  return (
    <svg width={size} height={size} className="shrink-0" role="img" aria-label={`Health score ${score}`}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border)" strokeWidth={stroke} />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke={color}
        strokeWidth={stroke}
        strokeDasharray={c}
        strokeDashoffset={offset}
        strokeLinecap="round"
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
        style={{ transition: "stroke-dashoffset 0.4s ease" }}
      />
      <text
        x="50%"
        y="50%"
        dy="0.35em"
        textAnchor="middle"
        className="fill-[var(--text)]"
        style={{ fontSize: size * 0.3, fontWeight: 600 }}
      >
        {score}
      </text>
    </svg>
  );
}
