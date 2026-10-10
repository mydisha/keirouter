import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { HealthSnapshot } from "../lib/api";
import { fmtIssue } from "./HealthBadge";

const tooltipStyle = {
  fontSize: 12,
  background: "var(--bg-elevated)",
  border: "1px solid var(--border)",
  borderRadius: 6,
  boxShadow: "0 4px 6px -1px rgb(0 0 0 / 0.1)",
};
const axisTick = { fontSize: 10, fill: "var(--text-muted)", fontWeight: 500 };

function fmtTime(t: string) {
  const d = new Date(t);
  if (isNaN(d.getTime())) return t;
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function fmtMs(v?: number) {
  if (v == null) return "0";
  if (v < 1000) return `${v}ms`;
  return `${(v / 1000).toFixed(1)}s`;
}

function emptyState() {
  return (
    <div className="flex h-full items-center justify-center text-xs font-medium text-[var(--text-muted)]">
      No data
    </div>
  );
}

export function RequestVolumeChart({ data }: { data: HealthSnapshot[] }) {
  if (!data.length) return emptyState();
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
        <defs>
          <linearGradient id="reqFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="var(--color-chart-1)" stopOpacity={0.2} />
            <stop offset="95%" stopColor="var(--color-chart-1)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.3} />
        <XAxis dataKey="bucket_start" tickFormatter={fmtTime} tick={axisTick} tickLine={false} axisLine={false} dy={10} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={50} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => fmtTime(String(t))} />
        <Area type="monotone" dataKey="request_count" name="Requests" stroke="var(--color-chart-1)" strokeWidth={2} fill="url(#reqFill)" />
      </AreaChart>
    </ResponsiveContainer>
  );
}

export function ErrorRateChart({ data }: { data: HealthSnapshot[] }) {
  if (!data.length) return emptyState();
  const series = data.map((d) => ({
    bucket_start: d.bucket_start,
    error_rate: d.request_count > 0 ? (d.failure_count / d.request_count) * 100 : 0,
  }));
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={series} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
        <defs>
          <linearGradient id="errFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="var(--color-danger)" stopOpacity={0.2} />
            <stop offset="95%" stopColor="var(--color-danger)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.3} />
        <XAxis dataKey="bucket_start" tickFormatter={fmtTime} tick={axisTick} tickLine={false} axisLine={false} dy={10} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={40} tickFormatter={(v: number) => `${v}%`} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => fmtTime(String(t))} formatter={(v) => [`${Number(v).toFixed(1)}%`, "Error rate"]} />
        <Area type="monotone" dataKey="error_rate" stroke="var(--color-danger)" strokeWidth={2} fill="url(#errFill)" />
      </AreaChart>
    </ResponsiveContainer>
  );
}

export function LatencyChart({ data }: { data: HealthSnapshot[] }) {
  if (!data.length) return emptyState();
  return (
    <ResponsiveContainer width="100%" height="100%">
      <LineChart data={data} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.3} />
        <XAxis dataKey="bucket_start" tickFormatter={fmtTime} tick={axisTick} tickLine={false} axisLine={false} dy={10} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={50} tickFormatter={fmtMs} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => fmtTime(String(t))} formatter={(v) => [fmtMs(Number(v)), ""]} />
        <Line type="monotone" dataKey="latency_p50_ms" name="p50" stroke="var(--color-chart-3)" strokeWidth={1.5} dot={false} />
        <Line type="monotone" dataKey="latency_p95_ms" name="p95" stroke="var(--color-warning)" strokeWidth={2} dot={false} />
        <Line type="monotone" dataKey="latency_p99_ms" name="p99" stroke="var(--color-danger)" strokeWidth={1.5} dot={false} />
      </LineChart>
    </ResponsiveContainer>
  );
}

export function TTFTChart({ data }: { data: HealthSnapshot[] }) {
  if (!data.length) return emptyState();
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
        <defs>
          <linearGradient id="ttftFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="var(--color-chart-2)" stopOpacity={0.2} />
            <stop offset="95%" stopColor="var(--color-chart-2)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.3} />
        <XAxis dataKey="bucket_start" tickFormatter={fmtTime} tick={axisTick} tickLine={false} axisLine={false} dy={10} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={50} tickFormatter={fmtMs} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => fmtTime(String(t))} formatter={(v) => [fmtMs(Number(v)), "TTFT p95"]} />
        <Area type="monotone" dataKey="ttft_p95_ms" stroke="var(--color-chart-2)" strokeWidth={2} fill="url(#ttftFill)" />
      </AreaChart>
    </ResponsiveContainer>
  );
}

export function FallbackChart({ data }: { data: HealthSnapshot[] }) {
  if (!data.length) return emptyState();
  return (
    <ResponsiveContainer width="100%" height="100%">
      <BarChart data={data} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.3} />
        <XAxis dataKey="bucket_start" tickFormatter={fmtTime} tick={axisTick} tickLine={false} axisLine={false} dy={10} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={40} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => fmtTime(String(t))} />
        <Bar dataKey="fallback_count" name="Fallbacks" fill="var(--color-secondary-500)" radius={[3, 3, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
}

export function ErrorBreakdownChart({ data }: { data: HealthSnapshot[] }) {
  if (!data.length) return emptyState();
  const totals = data.reduce(
    (acc, d) => {
      acc.rate_limited += d.rate_limited_count;
      acc.auth += d.auth_error_count;
      acc.quota += d.quota_exceeded_count;
      acc.timeout += d.timeout_count;
      acc.five_xx += d.provider_5xx_count;
      acc.bad_request += d.bad_request_count;
      acc.network += d.network_error_count;
      return acc;
    },
    { rate_limited: 0, auth: 0, quota: 0, timeout: 0, five_xx: 0, bad_request: 0, network: 0 },
  );
  const series = [
    { name: fmtIssue("rate_limited"), value: totals.rate_limited, fill: "var(--color-warning)" },
    { name: fmtIssue("auth_error"), value: totals.auth, fill: "var(--color-danger)" },
    { name: fmtIssue("quota_exceeded"), value: totals.quota, fill: "var(--color-secondary-500)" },
    { name: fmtIssue("timeout"), value: totals.timeout, fill: "var(--color-chart-3)" },
    { name: "Provider 5xx", value: totals.five_xx, fill: "var(--color-chart-4)" },
    { name: fmtIssue("bad_request"), value: totals.bad_request, fill: "var(--color-chart-5)" },
    { name: fmtIssue("network_error"), value: totals.network, fill: "var(--color-chart-2)" },
  ].filter((s) => s.value > 0);
  if (!series.length) return emptyState();
  return (
    <ResponsiveContainer width="100%" height="100%">
      <BarChart data={series} layout="vertical" margin={{ top: 5, right: 10, left: 20, bottom: 5 }}>
        <CartesianGrid horizontal={false} stroke="var(--border)" opacity={0.3} />
        <XAxis type="number" tick={axisTick} tickLine={false} axisLine={false} />
        <YAxis type="category" dataKey="name" tick={axisTick} tickLine={false} axisLine={false} width={80} />
        <Tooltip contentStyle={tooltipStyle} />
        <Bar dataKey="value" radius={[0, 3, 3, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
}
