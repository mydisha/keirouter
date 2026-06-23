import { useState, useMemo, useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity, DollarSign, Zap, RefreshCw, TrendingUp, Clock, Box, TerminalSquare, ArrowUpDown, ArrowUp, ArrowDown, Search
} from "lucide-react";
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid
} from "recharts";
import { api, connectUsageStream, type ProviderUsage, type RecentActivity, type ModelUsage, type SeriesPoint } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { Spinner, ErrorCard, StatCard } from "../components/ui";
import { useToast } from "../components/Toast";
import { TokenSavingsBreakdown } from "../components/SavingsBreakdown";

const periods = [
  { value: "today", label: "Today" },
  { value: "24h", label: "24h" },
  { value: "week", label: "7D" },
  { value: "month", label: "30D" },
];

const USAGE_REFRESH_DEBOUNCE_MS = 8000;

export function UsagePage() {
  const [period, setPeriod] = useState("today");
  const qc = useQueryClient();
  const toast = useToast();
  const refreshTimer = useRef<number | null>(null);

  const insights = useQuery({
    queryKey: ["usage-insights", period],
    queryFn: () => api.usageInsights(period),
    staleTime: 12_000,
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  });

  const modelUsage = useQuery({
    queryKey: ["usage-models", period],
    queryFn: () => api.modelUsage(period),
    staleTime: 12_000,
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  });

  useEffect(() => {
    const scheduleRefresh = () => {
      if (refreshTimer.current != null) return;
      refreshTimer.current = window.setTimeout(() => {
        refreshTimer.current = null;
        qc.invalidateQueries({ queryKey: ["usage-insights", period] });
        qc.invalidateQueries({ queryKey: ["usage-models", period] });
      }, USAGE_REFRESH_DEBOUNCE_MS);
    };
    return connectUsageStream(() => {
      scheduleRefresh();
    });
  }, [qc, period]);

  useEffect(() => {
    return () => {
      if (refreshTimer.current != null) {
        window.clearTimeout(refreshTimer.current);
      }
    };
  }, []);

  const handleRefresh = () => {
    qc.invalidateQueries({ queryKey: ["usage-insights", period] });
    qc.invalidateQueries({ queryKey: ["usage-models", period] });
    toast.success("Usage data refreshed", "All usage metrics and breakdowns have been re-fetched from the server.");
  };

  return (
    <>
      <PageHeader
        title="Usage"
        icon={Activity}
        description="Monitor request flow and provider distribution."
        action={
          <div className="flex items-center gap-2">
            <div className="flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] p-1">
              {periods.map((p) => (
                <button key={p.value} onClick={() => setPeriod(p.value)}
                  className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${period === p.value ? "bg-accent-600 text-white shadow-sm" : "text-[var(--text-muted)] hover:text-[var(--text)]"}`}>
                  {p.label}
                </button>
              ))}
            </div>
            <button onClick={handleRefresh}
              className="flex h-8 w-8 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]">
              <RefreshCw className="h-4 w-4" />
            </button>
          </div>
        }
      />

      {insights.isLoading ? <Spinner />
        : insights.isError ? <ErrorCard message="Failed to load usage. Is the backend running?" />
        : insights.data ? <UsageContent data={insights.data} models={modelUsage.data?.models ?? []} period={period} /> : null}
    </>
  );
}

function UsageContent({ data, models, period }: { data: any; models: ModelUsage[]; period: string }) {
  const { summary, savings, providers, recent, series } = data;

  return (
    <div className="space-y-8 pb-12">
      {/* Precision Minimalist Stat Grid */}
      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
        <StatCard label="REQUESTS" value={fmtNum(summary.total_requests)} icon={Activity} />
        <StatCard label="INPUT TOKENS" value={fmtNum(summary.prompt_tokens)} icon={Zap} />
        <StatCard label="OUTPUT TOKENS" value={fmtNum(summary.completion_tokens)} icon={TrendingUp} />
        <StatCard label="EST. COST" value={`$${summary.cost_usd.toFixed(2)}`} icon={DollarSign} />
        
        <div className="flex flex-col justify-between p-5 rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm transition-colors hover:bg-[var(--bg-subtle)] relative overflow-hidden group col-span-2 lg:col-span-1">
          <div className="absolute inset-0 bg-gradient-to-br from-emerald-500/5 to-transparent opacity-0 transition-opacity duration-500 group-hover:opacity-100 dark:from-emerald-500/10" />
          <div className="relative flex items-center justify-between mb-4">
            <div className="flex items-center gap-2">
              <Clock className="h-4 w-4 text-[var(--text-muted)]" />
              <span className="text-xs font-medium tracking-wide uppercase text-[var(--text-muted)]">EFFICIENCY</span>
            </div>
          </div>
          <div className="relative space-y-2">
            <div className="flex items-baseline justify-between">
              <div className="text-lg font-light tracking-tight tabular-nums text-[var(--text)]">
                {summary.avg_latency_ms > 0 ? `${summary.avg_latency_ms}ms` : summary.total_requests > 0 ? "<1ms" : "—"}
              </div>
              <div className="text-[11px] text-[var(--text-muted)]">latency</div>
            </div>
            <div className="flex items-baseline justify-between">
              <div className="text-lg font-light tracking-tight tabular-nums text-[var(--text)]">
                {summary.success_rate != null ? (summary.success_rate * 100).toFixed(1) : summary.total_requests > 0 ? "—" : "100"}%
              </div>
              <div className="text-[11px] text-[var(--text-muted)]">success</div>
            </div>
          </div>
        </div>
      </div>

      {/* Main Layout Grid */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,2fr)_minmax(320px,1fr)] items-stretch">
        {/* Custom SVG Topology */}
        <div className="flex flex-col rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden h-full">
          <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
            <div className="flex items-center gap-2">
              <Box className="h-4 w-4 text-[var(--text-muted)]" />
              <h3 className="text-sm font-semibold tracking-tight">Routing Topology</h3>
            </div>
          </div>
          <div className="p-4 flex-1 flex flex-col justify-center relative bg-gradient-to-br from-blue-50/30 via-transparent to-accent-50/30 dark:from-transparent dark:to-transparent">
            <ProviderTopology providers={providers} />
          </div>
        </div>

        {/* Insights */}
        <UsageInsightsCard data={data} />
        
        {/* Activity chart */}
        <div className="flex flex-col rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden h-full">
          <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3">
            <h3 className="text-sm font-semibold tracking-tight">Usage Trends</h3>
            <span className="text-[11px] font-medium text-[var(--text-muted)] uppercase tracking-wider">Tokens over time</span>
          </div>
          <div className="flex-1 px-2 pb-4 pt-4 min-h-[260px]">
            <ActivityChart series={series} />
          </div>
        </div>

        {/* Recent Requests */}
        <div className="flex flex-col rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden h-full min-h-[300px]">
          <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3 bg-[var(--bg-subtle)] shrink-0">
            <div className="flex items-center gap-2">
              <TerminalSquare className="h-4 w-4 text-[var(--text-muted)]" />
              <h3 className="text-sm font-semibold tracking-tight">Recent Requests</h3>
            </div>
          </div>
          {!recent.length ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 bg-[var(--bg)]">
              <Activity className="h-6 w-6 text-[var(--text-muted)] opacity-30" />
              <p className="text-xs font-medium text-[var(--text-muted)]">No active requests</p>
            </div>
          ) : (
            <div className="flex-1 overflow-y-auto">
              <table className="w-full border-collapse text-xs">
                <thead className="sticky top-0 z-10 bg-[var(--bg-elevated)] backdrop-blur-sm bg-opacity-90">
                  <tr className="border-b border-[var(--border)]">
                    <th className="w-6 px-3 py-2.5" />
                    <th className="px-3 py-2.5 text-left font-medium text-[var(--text-muted)] uppercase tracking-wider text-[10px]">Model</th>
                    <th className="px-3 py-2.5 text-right font-medium text-[var(--text-muted)] uppercase tracking-wider text-[10px]">Tokens</th>
                    <th className="px-3 py-2.5 text-right font-medium text-[var(--text-muted)] uppercase tracking-wider text-[10px]">Time</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {recent.map((r: RecentActivity) => <RecentRow key={r.id} row={r} />)}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {/* Token Savings breakdown */}
      {savings && (
        <TokenSavingsBreakdown savings={savings} totalRequests={summary.total_requests} insights={data} period={period} />
      )}

      {/* Breakdowns container to keep grid alignment */}
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <ModelUsageTable models={models} />
        <ProviderBreakdown providers={providers} />
      </div>
    </div>
  );
}

// ─── Custom SVG Topology ─────────────────────────────────────────────────────

function ProviderTopology({ providers }: { providers: ProviderUsage[] }) {
  const activeProviders = providers.filter((p) => p.share_pct > 0).sort((a,b) => b.share_pct - a.share_pct);
  
  if (activeProviders.length === 0) {
    return (
      <div className="flex h-[240px] flex-col items-center justify-center gap-3 bg-[var(--bg)] rounded-lg border border-dashed border-[var(--border)]">
        <TrendingUp className="h-6 w-6 text-[var(--text-muted)] opacity-30" />
        <p className="text-xs font-medium text-[var(--text-muted)] uppercase tracking-wider">No routing activity</p>
      </div>
    );
  }

  // Dynamic SVG sizing
  const width = 600;
  const rowHeight = 60;
  const height = Math.max(260, activeProviders.length * rowHeight + 80);
  const routerX = width - 140;
  const routerY = height / 2;

  return (
    <div className="relative w-full overflow-hidden flex justify-center items-center py-4 bg-[var(--bg-subtle)]">
      <style>{`
        @keyframes flowDash {
          from { stroke-dashoffset: 24; }
          to { stroke-dashoffset: 0; }
        }
        .animate-flow {
          animation: flowDash 1s linear infinite;
        }
      `}</style>
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="xMidYMid meet" className="w-full h-auto max-w-full drop-shadow-sm">
        
        {/* Connection Paths */}
        {activeProviders.map((p, i) => {
          const providerX = 160;
          const providerY = (height / (activeProviders.length + 1)) * (i + 1);
          const color = p.color || "var(--color-ink-400)";
          const isHigh = p.share_pct > 20;
          
          return (
            <g key={`path-${p.provider}`}>
              {/* Background faint path */}
              <path
                d={`M ${providerX} ${providerY} C ${(providerX + routerX) / 2} ${providerY}, ${(providerX + routerX) / 2} ${routerY}, ${routerX - 60} ${routerY}`}
                fill="none"
                stroke={color}
                strokeWidth={isHigh ? 3 : 1.5}
                strokeOpacity={isHigh ? 0.3 : 0.15}
                strokeLinecap="round"
              />
              {/* Animated dashed path overlay */}
              <path
                d={`M ${providerX} ${providerY} C ${(providerX + routerX) / 2} ${providerY}, ${(providerX + routerX) / 2} ${routerY}, ${routerX - 60} ${routerY}`}
                fill="none"
                stroke={color}
                strokeWidth={isHigh ? 3 : 1.5}
                className="animate-flow"
                strokeDasharray="4 8"
                strokeLinecap="round"
                strokeOpacity={isHigh ? 0.9 : 0.6}
              />
            </g>
          );
        })}

        {/* Provider Nodes */}
        {activeProviders.map((p, i) => {
          const providerX = 20;
          const providerY = (height / (activeProviders.length + 1)) * (i + 1);
          const color = p.color || "var(--color-ink-400)";
          
          return (
            <g key={`node-${p.provider}`} transform={`translate(${providerX}, ${providerY - 20})`}>
              <rect width="140" height="40" rx="6" fill="var(--bg-elevated)" stroke="var(--border)" strokeWidth="1" className="drop-shadow-sm" />
              {/* Color indicator bar */}
              <rect x="0" y="0" width="4" height="40" rx="2" fill={color} />
              
              <text x="14" y="18" className="text-xs font-semibold fill-[var(--text)]">{p.display_name.slice(0,16)}</text>
              <text x="14" y="32" className="text-[9px] font-medium uppercase tracking-wider fill-[var(--text-muted)]">{p.share_pct.toFixed(1)}% traffic</text>
            </g>
          );
        })}

        {/* Router Node (Destination) */}
        <g transform={`translate(${routerX - 60}, ${routerY - 24})`}>
          <rect width="120" height="48" rx="24" fill="var(--bg-elevated)" stroke="var(--border)" strokeWidth="1.5" className="drop-shadow-md" />
          <circle cx="24" cy="24" r="14" fill="var(--bg-subtle)" stroke="var(--border)" strokeWidth="1" />
          <image href="/keirouter-favicon.png" x="16" y="16" width="16" height="16" />
          <text x="46" y="28" className="text-sm font-bold fill-[var(--text)] tracking-tight">Router</text>
        </g>
      </svg>
    </div>
  );
}

// ─── Insights Component ───────────────────────────────────────────────────────

function UsageInsightsCard({ data }: { data: any }) {
  const { providers, summary } = data;
  const activeProviders = providers.filter((p: any) => p.share_pct > 0);
  
  const tokenRatio = summary.prompt_tokens > 0 
    ? summary.completion_tokens / summary.prompt_tokens
    : 0;

  return (
    <div className="flex flex-col rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden">
      <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3 bg-[var(--bg-subtle)]">
        <h3 className="text-sm font-semibold tracking-tight">Insights</h3>
      </div>
      <div className="p-5 flex flex-col gap-6">
        
        {/* Token Efficiency - Elevated design */}
        <div className="flex flex-col">
          <h4 className="text-[10px] font-semibold tracking-widest text-[var(--text-muted)] uppercase mb-3">Efficiency Multiplier</h4>
          <div className="flex items-end gap-3">
            <span className="text-4xl font-light tabular-nums leading-none text-[var(--text)] tracking-tighter">
              {tokenRatio.toFixed(2)}<span className="text-2xl text-[var(--text-muted)]">x</span>
            </span>
            <div className="pb-1">
              {summary.prompt_tokens > 0 && (
                <p className="text-[11px] text-[var(--text-muted)] font-medium">
                  {fmtNum(summary.completion_tokens)} out / {fmtNum(summary.prompt_tokens)} in
                </p>
              )}
            </div>
          </div>
        </div>

        <div className="h-px w-full bg-[var(--border)]" />

        {/* Provider Distribution - Sleek Stacked Bar */}
        <div>
          <h4 className="text-[10px] font-semibold tracking-widest text-[var(--text-muted)] uppercase mb-4">Traffic Distribution</h4>
          
          <div className="flex h-2.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)] mb-4">
            {activeProviders.map((p: any) => (
              <div 
                key={p.provider} 
                className="h-full transition-all"
                style={{ width: `${p.share_pct}%`, backgroundColor: p.color || "var(--color-chart-1)" }} 
                title={`${p.provider_name}: ${p.share_pct.toFixed(1)}%`}
              />
            ))}
          </div>

          <div className="grid grid-cols-2 gap-y-3 gap-x-2">
            {activeProviders.slice(0, 4).map((p: any) => (
              <div key={p.provider} className="flex items-center justify-between text-xs pr-2 border-r border-[var(--border)] last:border-r-0 even:border-r-0">
                <div className="flex items-center gap-2 truncate">
                  <span className="h-1.5 w-1.5 rounded-full shrink-0" style={{ backgroundColor: p.color || "var(--color-chart-1)" }} />
                  <span className="text-[var(--text-muted)] truncate font-medium">{p.provider_name || p.provider}</span>
                </div>
                <span className="font-semibold tabular-nums ml-2">{p.share_pct.toFixed(0)}%</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── Activity Chart ──────────────────────────────────────────────────────────

function ActivityChart({ series }: { series: SeriesPoint[] }) {
  if (!series.length) {
    return <div className="flex h-full items-center justify-center text-xs font-medium uppercase tracking-wider text-[var(--text-muted)]">No data</div>;
  }

  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={series} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
        <defs>
          <linearGradient id="usageFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="var(--color-chart-1)" stopOpacity={0.15} />
            <stop offset="95%" stopColor="var(--color-chart-1)" stopOpacity={0} />
          </linearGradient>
        </defs>
        {/* Removed harsh grid, kept only subtle horizontal lines */}
        <CartesianGrid vertical={false} stroke="var(--border)" opacity={0.3} />
        <XAxis 
          dataKey="label" 
          tick={{ fontSize: 10, fill: "var(--text-muted)", fontWeight: 500 }} 
          tickLine={false} 
          axisLine={false} 
          dy={10}
        />
        <YAxis 
          tick={{ fontSize: 10, fill: "var(--text-muted)", fontWeight: 500 }} 
          tickLine={false} 
          axisLine={false} 
          tickFormatter={(v: number) => fmtNum(v)} 
          width={60} 
        />
        <Tooltip
          contentStyle={{ fontSize: 12, background: "var(--bg-elevated)", border: "1px solid var(--border)", borderRadius: 6, boxShadow: "0 4px 6px -1px rgb(0 0 0 / 0.1)" }}
          itemStyle={{ color: "var(--text)", fontWeight: 600 }}
          formatter={(value: number) => [fmtNum(value), "Requests"]}
          labelStyle={{ color: "var(--text-muted)", marginBottom: 4 }}
        />
        <Area 
          type="monotone" 
          dataKey="count" 
          stroke="var(--color-chart-1)" 
          strokeWidth={2} 
          fill="url(#usageFill)" 
          activeDot={{ r: 4, strokeWidth: 0, fill: "var(--color-chart-1)" }}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ─── Recent Row ──────────────────────────────────────────────────────────────

function RecentRow({ row }: { row: RecentActivity }) {
  const success = row.latency_ms > 0;
  const hasSavings = (row.slim_bytes_saved ?? 0) > 0 || row.caveman_active || row.terse_active || row.headroom_active;
  return (
    <tr className="transition-colors hover:bg-[var(--bg-subtle)] group">
      <td className="w-6 px-3 py-2.5">
        <span className={`block h-1.5 w-1.5 rounded-full ${success ? "bg-emerald-500" : "bg-red-500"}`} />
      </td>
      <td className="px-3 py-2.5">
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-1.5">
            <span className="font-mono text-[11px] font-semibold text-[var(--text)]">{row.model || "—"}</span>
          </div>
          <div className="flex items-center gap-1.5">
            {row.provider && (
              <>
                <ProviderIcon provider={row.provider} className="h-3 w-3" />
                <span className="text-[9px] uppercase tracking-wider font-medium text-[var(--text-muted)]">{row.provider}</span>
              </>
            )}
            {hasSavings && (
              <span className="flex items-center gap-1">
                {(row.slim_bytes_saved ?? 0) > 0 && (
                  <span className="text-[9px] font-bold text-teal-600 dark:text-teal-400 uppercase tracking-widest" title={`RTK saved ${fmtBytes(row.slim_bytes_saved ?? 0)}`}>
                    [RTK]
                  </span>
                )}
                {row.caveman_active && (
                  <span className="text-[9px] font-bold text-purple-600 dark:text-purple-400 uppercase tracking-widest" title="Caveman compression">
                    [CVMN]
                  </span>
                )}
                {row.headroom_active && (
                  <span className="text-[9px] font-bold text-amber-600 dark:text-amber-400 uppercase tracking-widest" title={`Headroom saved ${fmtNum(row.headroom_tokens_saved ?? 0)} tokens`}>
                    [HDRM]
                  </span>
                )}
                {row.terse_active && (
                  <span className="text-[9px] font-bold text-indigo-600 dark:text-indigo-400 uppercase tracking-widest" title="Terse compression">
                    [TRSE]
                  </span>
                )}
              </span>
            )}
          </div>
        </div>
      </td>
      <td className="px-3 py-2.5 text-right tabular-nums text-[11px] font-medium text-[var(--text-muted)]">
        <span className="text-[var(--text)]">{fmtNum(Math.round(row.tokens * 0.6))}</span> <span className="opacity-40">in</span><br/>
        <span className="text-[var(--text)]">{fmtNum(Math.round(row.tokens * 0.4))}</span> <span className="opacity-40">out</span>
      </td>
      <td className="px-3 py-2.5 text-right text-[10px] font-medium text-[var(--text-muted)] whitespace-nowrap">
        {relTime(row.created_at)}
      </td>
    </tr>
  );
}

function ProviderIcon({ provider, className = "h-5 w-5" }: { provider: string, className?: string }) {
  const [errored, setErrored] = useState(false);
  if (errored) {
    return (
      <div className={`flex shrink-0 items-center justify-center rounded bg-[var(--bg-subtle)] border border-[var(--border)] text-[8px] font-bold text-[var(--text-muted)] uppercase ${className}`}>
        {provider.slice(0, 2)}
      </div>
    );
  }
  return <img src={`/providers/${provider}.png`} alt={provider} onError={() => setErrored(true)} className={`shrink-0 rounded object-contain grayscale opacity-80 mix-blend-multiply dark:mix-blend-screen ${className}`} />;
}

// ─── Token Savings Breakdown moved to ../components/SavingsBreakdown.tsx ──

// ─── Model Usage Table ──────────────────────────────────────────────────────

type SortKey = "provider" | "model" | "requests" | "prompt" | "completion" | "cost";

function ModelUsageTable({ models }: { models: ModelUsage[] }) {
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("requests");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("desc");
    }
  };

  const filtered = useMemo(() => {
    const q = search.toLowerCase();
    let rows = models;
    if (q) {
      rows = rows.filter(
        (m) => m.model.toLowerCase().includes(q) || m.provider_name.toLowerCase().includes(q) || m.provider.toLowerCase().includes(q),
      );
    }
    return [...rows].sort((a, b) => {
      const dir = sortDir === "asc" ? 1 : -1;
      switch (sortKey) {
        case "provider": return dir * a.provider_name.localeCompare(b.provider_name);
        case "model": return dir * a.model.localeCompare(b.model);
        case "requests": return dir * (a.total_requests - b.total_requests);
        case "prompt": return dir * (a.prompt_tokens - b.prompt_tokens);
        case "completion": return dir * (a.completion_tokens - b.completion_tokens);
        case "cost": return dir * (a.cost_usd - b.cost_usd);
        default: return 0;
      }
    });
  }, [models, search, sortKey, sortDir]);

  const SortIcon = ({ col }: { col: SortKey }) => {
    if (sortKey !== col) return <ArrowUpDown className="ml-1 inline h-3 w-3 opacity-30" />;
    return sortDir === "asc" ? <ArrowUp className="ml-1 inline h-3 w-3" /> : <ArrowDown className="ml-1 inline h-3 w-3" />;
  };

  const th = (col: SortKey, label: string, align: "left" | "right" = "left") => (
    <th
      className={`cursor-pointer select-none px-4 py-3 font-semibold uppercase tracking-wider text-[10px] text-[var(--text-muted)] transition-colors hover:text-[var(--text)] ${align === "right" ? "text-right" : "text-left"}`}
      onClick={() => toggleSort(col)}
    >
      {label}
      <SortIcon col={col} />
    </th>
  );

  return (
    <div className="flex flex-col rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden">
      <div className="flex flex-col gap-3 border-b border-[var(--border)] px-5 py-3 bg-[var(--bg-subtle)] sm:flex-row sm:items-center sm:justify-between">
        <h3 className="text-sm font-semibold tracking-tight">Model Usage</h3>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--text-muted)]" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search models…"
            className="rounded-lg border border-[var(--border)] bg-[var(--bg)] py-1.5 pl-8 pr-3 text-xs placeholder:text-[var(--text-muted)] focus:border-[var(--text)] focus:outline-none transition-colors w-48"
          />
        </div>
      </div>
      {filtered.length === 0 ? (
        <div className="py-12 text-center text-xs font-medium text-[var(--text-muted)]">
          {models.length === 0 ? "No model data for this period" : "No models match your search"}
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-[var(--bg)]">
              <tr className="border-b border-[var(--border)]">
                {th("model", "Model")}
                {th("requests", "Req", "right")}
                {th("prompt", "In", "right")}
                {th("completion", "Out", "right")}
                {th("cost", "Cost", "right")}
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {filtered.map((m) => (
                <tr key={`${m.provider}/${m.model}`} className="transition-colors hover:bg-[var(--bg-subtle)]">
                  <td className="px-4 py-3">
                    <div className="flex flex-col gap-1">
                      <span className="font-mono text-[11px] font-semibold">{m.model}</span>
                      <div className="flex items-center gap-1.5">
                        <ProviderIcon provider={m.provider} className="h-3 w-3" />
                        <span className="text-[9px] uppercase tracking-wider text-[var(--text-muted)]">{m.provider_name}</span>
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums font-medium">{m.total_requests.toLocaleString()}</td>
                  <td className="px-4 py-3 text-right tabular-nums text-[var(--text-muted)]">{fmtNum(m.prompt_tokens)}</td>
                  <td className="px-4 py-3 text-right tabular-nums text-[var(--text-muted)]">{fmtNum(m.completion_tokens)}</td>
                  <td className="px-4 py-3 text-right tabular-nums font-medium text-[var(--text)]">${m.cost_usd.toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ─── Provider Breakdown ─────────────────────────────────────────────────────

function ProviderBreakdown({ providers }: { providers: ProviderUsage[] }) {
  const active = providers.filter((p) => p.total_requests > 0).sort((a,b) => b.total_requests - a.total_requests);
  
  return (
    <div className="flex flex-col rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden">
      <div className="border-b border-[var(--border)] px-5 py-4 bg-[var(--bg-subtle)]">
        <h3 className="text-sm font-semibold tracking-tight">Provider Breakdown</h3>
      </div>
      {active.length === 0 ? (
        <div className="py-12 text-center text-xs font-medium text-[var(--text-muted)]">No provider data</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-[var(--bg)]">
              <tr className="border-b border-[var(--border)]">
                <th className="px-4 py-3 text-left font-semibold uppercase tracking-wider text-[10px] text-[var(--text-muted)]">Provider</th>
                <th className="px-4 py-3 text-right font-semibold uppercase tracking-wider text-[10px] text-[var(--text-muted)]">Req</th>
                <th className="px-4 py-3 text-right font-semibold uppercase tracking-wider text-[10px] text-[var(--text-muted)]">Share</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {active.map((p) => (
                <tr key={p.provider} className="transition-colors hover:bg-[var(--bg-subtle)]">
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-3">
                      <ProviderIcon provider={p.provider} />
                      <span className="font-medium text-[var(--text)]">{p.display_name}</span>
                    </div>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums font-medium">{p.total_requests.toLocaleString()}</td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-3">
                      <span className="w-8 text-right tabular-nums font-medium text-[var(--text)]">{p.share_pct.toFixed(0)}%</span>
                      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                        <div className="h-full rounded-full transition-all" style={{ width: `${Math.max(2, p.share_pct)}%`, backgroundColor: p.color || "var(--text)" }} />
                      </div>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

function fmtBytes(n: number): string {
  if (n >= 1_048_576) return `${(n / 1_048_576).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const diff = Date.now() - t;
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}
