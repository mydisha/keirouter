import { useState, useEffect, useMemo } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AreaChart, Area, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer,
  CartesianGrid, PieChart, Pie, Cell, ComposedChart, Line,
} from "recharts";
import {
  AlertTriangle, ArrowDownRight, ArrowUpRight, DollarSign, Layers, Radio,
  TrendingUp, Coins, Calendar, Trophy, Infinity as InfinityIcon, Clock,
  ChevronLeft, ChevronRight, Copy, Check, Activity, KeyRound,
} from "lucide-react";
import { Button, Card, Badge, Input, SegmentedControl } from "../../components/ui";
import { claimPortalKey, type KeyUsageData, type PortalRecentRequest } from "../../lib/api";

// ─── Shared portal onboarding surfaces ────────────────────────────────────

export function PageHeader({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <header className="space-y-1">
      <h1 className="text-2xl font-display font-semibold tracking-tight text-[var(--text)]">{title}</h1>
      {subtitle && <p className="text-sm text-[var(--text-muted)]">{subtitle}</p>}
    </header>
  );
}

// RevealPanel shows a freshly created plaintext key exactly once. The Done
// button clears it and refetches status/metadata at the call site.
export function RevealPanel({
  value,
  onDone,
  doneLabel = "Continue",
}: {
  value: string;
  onDone: () => void;
  doneLabel?: string;
}) {
  return (
    <Card className="mx-auto w-full max-w-md p-8">
      <div className="mb-6 flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-accent-100 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300">
          <KeyRound size={18} />
        </div>
        <h1 className="text-xl font-display font-semibold tracking-tight text-[var(--text)]">Your API key is ready</h1>
      </div>
      <p className="mb-4 text-sm text-[var(--text-muted)]">
        Copy this key now and store it securely. For your protection it is shown
        only once and cannot be retrieved later.
      </p>
      <div className="mb-6 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60 p-3">
        <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-[var(--text)]">{value}</code>
        <CopyButton value={value} />
      </div>
      <Button onClick={onDone} className="w-full">{doneLabel}</Button>
    </Card>
  );
}

// ClaimForm binds an existing key to the signed-in account.
export function ClaimForm({
  label = "I already have a key",
  submitLabel = "Claim existing key",
}: {
  label?: string;
  submitLabel?: string;
}) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState("");
  const claim = useMutation({
    mutationFn: () => claimPortalKey(value.trim()),
    onSuccess: () => {
      setValue("");
      queryClient.invalidateQueries({ queryKey: ["portal-status"] });
      queryClient.invalidateQueries({ queryKey: ["portal-key"] });
    },
  });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (value.trim()) claim.mutate();
      }}
      className="space-y-3"
    >
      <label htmlFor="portal-claim-key" className="block text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
        {label}
      </label>
      <Input
        id="portal-claim-key"
        type="password"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="kr_..."
      />
      {claim.error instanceof Error && (
        <p className="text-sm text-[color:var(--color-danger)]">{claim.error.message}</p>
      )}
      <Button type="submit" variant="ghost" className="w-full" disabled={!value.trim() || claim.isPending}>
        {claim.isPending ? "Claiming…" : submitLabel}
      </Button>
    </form>
  );
}

// SetupCard is the onboarding surface for a signed-in user without a binding.
export function SetupCard({
  email,
  provisioning,
  description = "Generate an API key on your plan and start monitoring your usage.",
  claimLabel,
  claimSubmitLabel,
  onCreate,
  creating,
  createError,
}: {
  email?: string;
  provisioning: boolean;
  description?: string;
  claimLabel?: string;
  claimSubmitLabel?: string;
  onCreate: () => void;
  creating: boolean;
  createError: string;
}) {
  return (
    <Card className="mx-auto w-full max-w-md p-8">
      <div className="mb-6 text-center">
        <h2 className="text-xl font-display font-semibold tracking-tight text-[var(--text)]">Set up your API key</h2>
        {email && (
          <p className="mt-2 text-sm text-[var(--text-muted)]">
            Signed in as <strong className="font-medium text-[var(--text)]">{email}</strong>
          </p>
        )}
      </div>

      {provisioning ? (
        <div className="space-y-5">
          <p className="text-center text-sm text-[var(--text-muted)]">{description}</p>
          {createError && <p className="text-sm text-[color:var(--color-danger)]">{createError}</p>}
          <Button onClick={onCreate} disabled={creating} className="w-full">
            {creating ? "Creating…" : "Create API key"}
          </Button>
        </div>
      ) : (
        <p className="text-center text-sm text-[var(--text-muted)]">
          Automatic key provisioning is disabled. Contact an administrator to set up your key.
        </p>
      )}

      <div className="mt-5 border-t border-[var(--border)] pt-5">
        <ClaimForm label={claimLabel} submitLabel={claimSubmitLabel} />
      </div>
    </Card>
  );
}

// Chart palette pulled from the design-system CSS variables so the portal
// matches the admin dashboard in both light and dark themes.
export const C_INPUT = "var(--color-chart-1)";
export const C_OUTPUT = "var(--color-chart-2)";
export const C_COST = "var(--color-chart-3)";
export const C_REQ = "var(--color-chart-5)";

export const DATE_RANGES = [
  { value: 7, label: "7D" },
  { value: 14, label: "14D" },
  { value: 30, label: "30D" },
  { value: 90, label: "90D" },
];

// ─── Date Filter ───────────────────────────────────────────────────────────
export function DateFilter({ days, onChange }: { days: number; onChange: (v: number) => void }) {
  return (
    <div className="flex items-center justify-center sm:justify-end">
      <div className="flex items-center gap-1 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] p-1 shadow-sm">
        {DATE_RANGES.map((r) => (
          <button
            key={r.value}
            onClick={() => onChange(r.value)}
            className={`rounded-lg px-4 py-2 text-xs font-semibold transition-all ${
              days === r.value
                ? "bg-accent-600 text-white shadow-sm"
                : "text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-subtle)]"
            }`}
          >
            {r.label}
          </button>
        ))}
      </div>
    </div>
  );
}

// ─── Overview: budget allocations + KPI cards ──────────────────────────────
export function OverviewSection({ d }: { d: KeyUsageData }) {
  const daily = d.daily ?? [];
  const t = useMemo(() => aggregate(daily), [daily]);
  const totalTokens = t.prompt + t.completion;
  const inputPct = totalTokens ? Math.round((t.prompt / totalTokens) * 100) : 0;
  const activeDays = daily.filter((x) => x.requests > 0).length;
  const avgPerReq = t.requests ? Math.round(totalTokens / t.requests) : 0;

  return (
    <section>
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-5">
        {/* Allocations */}
        <div className="lg:col-span-5">
          <Card className="h-full p-7 md:p-8 flex flex-col">
            <div className="mb-6 flex items-center justify-between">
              <h2 className="text-xs font-semibold tracking-widest text-[var(--text-muted)] uppercase">Allocations</h2>
              {d.budgets && d.budgets.length > 0 && (
                <Badge tone={d.budgets.some((b) => b.alert) ? "danger" : "neutral"}>
                  {d.budgets.length} limit{d.budgets.length === 1 ? "" : "s"}
                </Badge>
              )}
            </div>

            {d.budgets && d.budgets.length > 0 ? (
              <div className="flex-1 flex flex-col justify-center space-y-9">
                {d.budgets.map((b, i) => (
                  <div key={i}>
                    <div className="mb-5 flex items-center justify-between">
                      <div className="flex items-center gap-3">
                        <span className={`h-2.5 w-2.5 rounded-full ${b.alert ? "bg-[color:var(--color-danger)] shadow-[0_0_10px_var(--color-danger)]" : "bg-accent-500 shadow-[0_0_10px_var(--color-accent-500)]"}`} />
                        <h3 className="text-xl font-display font-semibold tracking-tight text-[var(--text)]">
                          {b.period === "total" ? "All-Time" : b.period.charAt(0).toUpperCase() + b.period.slice(1)} Limit
                        </h3>
                      </div>
                      {b.alert && (
                        <Badge tone="danger">
                          <span className="flex items-center gap-1.5"><AlertTriangle size={13} /> Exceeded</span>
                        </Badge>
                      )}
                    </div>
                    <div className="space-y-6">
                      {b.limit_tokens > 0 && (
                        <BudgetProgress label="Tokens" used={b.tokens_used} limit={b.limit_tokens} pct={b.tokens_pct_used} alert={b.alert} remaining={b.tokens_remaining} format={formatTokens} />
                      )}
                      {b.limit_usd > 0 && (
                        <BudgetProgress label="Spend" used={b.spent_usd} limit={b.limit_usd} pct={b.usd_pct_used} alert={b.alert} remaining={b.usd_remaining} format={(v: number) => `$${v.toFixed(2)}`} />
                      )}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="flex-1 flex flex-col items-center justify-center text-center py-6">
                <div className="mb-5 flex h-16 w-16 items-center justify-center rounded-full bg-accent-50 text-accent-600 ring-4 ring-accent-50/50 dark:bg-accent-900/20 dark:ring-accent-900/30">
                  <InfinityIcon size={30} strokeWidth={1.75} />
                </div>
                <h3 className="text-xl font-display font-semibold text-[var(--text)]">Unrestricted</h3>
                <p className="mt-2 text-sm text-[var(--text-muted)] max-w-xs">This key has no configured budget limits and can be used indefinitely.</p>
              </div>
            )}
          </Card>
        </div>

        {/* KPI cards */}
        <div className="lg:col-span-7">
          <div className="mb-3 flex items-center justify-between px-1">
            <h2 className="text-xs font-semibold tracking-widest text-[var(--text-muted)] uppercase">Period Summary</h2>
            <span className="text-xs text-[var(--text-muted)]">{activeDays} active day{activeDays === 1 ? "" : "s"}</span>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <KpiCard icon={Activity} label="Requests" value={formatNumber(t.requests)} sub={`${avgPerReq ? formatTokens(avgPerReq) : 0} tokens / req`} color={C_REQ} data={daily} dataKey="requests" sparkId="sp-req" />
            <KpiCard icon={DollarSign} label="Total Cost" value={`$${t.cost.toFixed(4)}`} sub={activeDays ? `~$${(t.cost / activeDays).toFixed(2)} / day` : "no spend yet"} color={C_COST} accent data={daily} dataKey="cost_usd" sparkId="sp-cost" />
            <KpiCard icon={ArrowDownRight} label="Input Tokens" value={formatTokens(t.prompt)} sub={`${inputPct}% of tokens`} color={C_INPUT} data={daily} dataKey="prompt_tokens" sparkId="sp-in" />
            <KpiCard icon={ArrowUpRight} label="Output Tokens" value={formatTokens(t.completion)} sub={`${100 - inputPct}% of tokens`} color={C_OUTPUT} data={daily} dataKey="completion_tokens" sparkId="sp-out" />
          </div>
          <div className="mt-4 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/40 px-4 py-2.5 text-xs text-[var(--text-muted)]">
            <Calendar size={14} className="text-[var(--text-muted)]" />
            This month so far:
            <strong className="font-medium text-[var(--text)]">{formatNumber(d.current_period.total_requests)}</strong> requests ·
            <strong className="font-medium text-[var(--text)]">${d.current_period.cost_usd.toFixed(4)}</strong> spent
          </div>
        </div>
      </div>
    </section>
  );
}

// ─── Usage trend chart with metric toggle ─────────────────────────────────
export type Metric = "tokens" | "requests" | "cost";

export function TrendSection({ daily, days }: { daily: NonNullable<KeyUsageData["daily"]>; days: number }) {
  const [metric, setMetric] = useState<Metric>("tokens");
  const chartData = useMemo(() => daily.map((dp) => ({ ...dp, label: dp.date.slice(5) })), [daily]);
  const t = useMemo(() => aggregate(daily), [daily]);

  const headline =
    metric === "tokens" ? formatTokens(t.prompt + t.completion) + " tokens"
      : metric === "requests" ? formatNumber(t.requests) + " requests"
        : "$" + t.cost.toFixed(4);

  return (
    <section className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <SectionTitle title="Usage Trend" icon={<TrendingUp size={17} />} />
        <SegmentedControl<Metric>
          value={metric}
          onChange={setMetric}
          options={[
            { value: "tokens", label: "Tokens" },
            { value: "requests", label: "Requests" },
            { value: "cost", label: "Cost" },
          ]}
        />
      </div>

      <Card className="p-6 md:p-7">
        <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="text-xs font-medium uppercase tracking-widest text-[var(--text-muted)]">{days}-day total</p>
            <p className="mt-1 text-2xl font-display font-semibold tabular-nums tracking-tight text-[var(--text)]">{headline}</p>
          </div>
          {metric === "tokens" && (
            <div className="flex items-center gap-4 text-xs">
              <LegendDot color={C_INPUT} label="Input" />
              <LegendDot color={C_OUTPUT} label="Output" />
              <LegendDot color={C_REQ} label="Requests" />
            </div>
          )}
          {metric === "requests" && (
            <div className="flex items-center gap-4 text-xs">
              <LegendDot color={C_REQ} label="Requests" />
              <LegendDot color={C_COST} label="Cost" />
            </div>
          )}
          {metric === "cost" && (
            <div className="flex items-center gap-4 text-xs">
              <LegendDot color={C_COST} label="Cost" />
              <LegendDot color={C_REQ} label="Requests" />
            </div>
          )}
        </div>

        <div className="h-[260px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            {metric === "requests" ? (
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -18, bottom: 0 }}>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="4 4" opacity={0.6} />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} dy={12} minTickGap={24} />
                <YAxis yAxisId="left" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatNumber} width={56} />
                <YAxis yAxisId="right" orientation="right" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={(v) => `$${v}`} width={52} />
                <Tooltip cursor={{ fill: "var(--bg-subtle)", opacity: 0.5 }} content={<ChartTooltip metric={metric} />} />
                <Bar yAxisId="left" dataKey="requests" fill={C_REQ} fillOpacity={0.85} radius={[6, 6, 0, 0]} maxBarSize={38} name="Requests" />
                <Line yAxisId="right" type="monotone" dataKey="cost_usd" stroke={C_COST} strokeWidth={2.5} dot={false} name="Cost" />
              </ComposedChart>
            ) : metric === "cost" ? (
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -8, bottom: 0 }}>
                <defs>
                  <linearGradient id="costFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={C_COST} stopOpacity={0.3} />
                    <stop offset="95%" stopColor={C_COST} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="4 4" opacity={0.6} />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} dy={12} minTickGap={24} />
                <YAxis yAxisId="left" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={(v) => `$${v}`} width={56} />
                <YAxis yAxisId="right" orientation="right" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatNumber} width={52} />
                <Tooltip content={<ChartTooltip metric={metric} />} />
                <Area yAxisId="left" type="monotone" dataKey="cost_usd" stroke={C_COST} strokeWidth={2.5} fill="url(#costFill)" name="Cost" />
                <Line yAxisId="right" type="monotone" dataKey="requests" stroke={C_REQ} strokeWidth={2} strokeDasharray="4 3" dot={false} name="Requests" />
              </ComposedChart>
            ) : (
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -18, bottom: 0 }}>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="4 4" opacity={0.6} />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} dy={12} minTickGap={24} />
                <YAxis yAxisId="left" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatTokens} width={56} />
                <YAxis yAxisId="right" orientation="right" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatNumber} width={52} />
                <Tooltip cursor={{ fill: "var(--bg-subtle)", opacity: 0.5 }} content={<ChartTooltip metric={metric} />} />
                <Bar yAxisId="left" dataKey="prompt_tokens" stackId="tok" fill={C_INPUT} fillOpacity={0.85} radius={[0, 0, 0, 0]} maxBarSize={38} name="Input" />
                <Bar yAxisId="left" dataKey="completion_tokens" stackId="tok" fill={C_OUTPUT} fillOpacity={0.85} radius={[6, 6, 0, 0]} maxBarSize={38} name="Output" />
                <Line yAxisId="right" type="monotone" dataKey="requests" stroke={C_REQ} strokeWidth={2.5} dot={false} name="Requests" />
              </ComposedChart>
            )}
          </ResponsiveContainer>
        </div>
      </Card>
    </section>
  );
}

// ─── Recent Requests table ─────────────────────────────────────────────────
export function RecentRequestsSection({ recent, days: _days }: { recent: PortalRecentRequest[]; days: number }) {
  const PAGE_SIZE =15;
  const [page, setPage] = useState(0);
  const totalPages = Math.max(1, Math.ceil(recent.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages -1);
  const start = safePage * PAGE_SIZE;
  const pageItems = recent.slice(start, start + PAGE_SIZE);

  return (
    <section className="space-y-4">
      <SectionTitle title="Request Log" icon={<Clock size={17} />} count={recent.length} />
      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-[var(--bg-subtle)]/50 border-b border-[var(--border)]">
              <tr className="text-[11px] uppercase tracking-wide text-[var(--text-muted)]">
                <th className="px-4 py-3 text-left font-semibold">Model</th>
                <th className="px-4 py-3 text-right font-semibold">
                  <span className="inline-flex items-center justify-end gap-1">
                    <ArrowDownRight size={12} className="text-[var(--color-chart-1)]" /> In
                  </span>
                </th>
                <th className="px-4 py-3 text-right font-semibold">
                  <span className="inline-flex items-center justify-end gap-1">
                    <ArrowUpRight size={12} className="text-[var(--color-chart-2)]" /> Out
                  </span>
                </th>
                <th className="px-4 py-3 text-right font-semibold">Cost</th>
                <th className="px-4 py-3 text-center font-semibold">Optimizations</th>
                <th className="px-4 py-3 text-right font-semibold">Latency</th>
                <th className="px-4 py-3 text-right font-semibold">Date</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {pageItems.map((r) => {
                const opts = r.optimizations ?? [];
                return (
                  <tr key={r.id} className="group transition-colors hover:bg-[var(--bg-subtle)]/30">
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-3">
                        <ProviderIcon provider={r.provider} className="h-8 w-8" />
                        <div className="flex min-w-0 flex-col">
                          <span className="font-semibold text-[var(--text)] text-[13px] truncate">{r.model}</span>
                          <span className="text-[11px] text-[var(--text-muted)] capitalize truncate">{r.provider}</span>
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums font-mono text-[13px] text-[var(--text)]">
                      <span className="inline-flex items-center justify-end gap-1.5">
                        <ArrowDownRight size={12} className="text-[var(--color-chart-1)]" />
                        {formatTokens(r.prompt_tokens)}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums font-mono text-[13px] text-[var(--text)]">
                      <span className="inline-flex items-center justify-end gap-1.5">
                        <ArrowUpRight size={12} className="text-[var(--color-chart-2)]" />
                        {formatTokens(r.completion_tokens)}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums text-[13px] text-[var(--text-muted)]">${r.cost_usd.toFixed(4)}</td>
                    <td className="px-4 py-3 text-center">
                      {opts.length >0 ? (
                        <div className="flex flex-wrap items-center justify-center gap-1">
                          {opts.map((opt) => (
                            <OptBadge key={opt} name={opt} detail={optDetail(r, opt)} />
                          ))}
                        </div>
                      ) : (
                        <span className="text-[11px] text-[var(--text-muted)] opacity-40">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right tabular-nums text-[12px] text-[var(--text-muted)]">
                      {r.latency_ms >0 ? `${r.latency_ms}ms` : r.cache_hit ? "cache" : "—"}
                    </td>
                    <td className="px-4 py-3 text-right whitespace-nowrap">
                      <div className="flex flex-col items-end leading-tight">
                        <span className="text-[12px] font-medium text-[var(--text)] tabular-nums">{formatDateTime(r.created_at)}</span>
                        <span className="text-[10px] text-[var(--text-muted)] tabular-nums">{relTime(r.created_at)} ago</span>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {totalPages >1 && (
          <div className="flex items-center justify-between border-t border-[var(--border)] px-4 py-3">
            <span className="text-[11px] text-[var(--text-muted)]">
              Showing {start +1}–{Math.min(start + PAGE_SIZE, recent.length)} of {recent.length}
            </span>
            <div className="flex items-center gap-1.5">
              <button
                onClick={() => setPage((p) => Math.max(0, p -1))}
                disabled={safePage ===0}
                className="flex h-8 w-8 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-40 disabled:hover:bg-[var(--bg-elevated)] disabled:hover:text-[var(--text-muted)]"
                aria-label="Previous page"
              >
                <ChevronLeft size={16} />
              </button>
              <span className="px-2 text-[12px] font-medium tabular-nums text-[var(--text)]">
                {safePage +1} / {totalPages}
              </span>
              <button
                onClick={() => setPage((p) => Math.min(totalPages -1, p +1))}
                disabled={safePage === totalPages -1}
                className="flex h-8 w-8 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-40 disabled:hover:bg-[var(--bg-elevated)] disabled:hover:text-[var(--text-muted)]"
                aria-label="Next page"
              >
                <ChevronRight size={16} />
              </button>
            </div>
          </div>
        )}
      </Card>
    </section>
  );
}

export function OptBadge({ name, detail }: { name: string; detail?: string }) {
  const styles: Record<string, string> = {
    RTK: "bg-teal-500/10 text-teal-600 dark:text-teal-400",
    Caveman: "bg-purple-500/10 text-purple-600 dark:text-purple-400",
    Terse: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400",
    Headroom: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
    Ponytail: "bg-pink-500/10 text-pink-600 dark:text-pink-400",
  };
  const style = styles[name] || "bg-[var(--bg-subtle)] text-[var(--text-muted)]";
  return (
    <span
      className={`inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${style}`}
      title={detail || name}
    >
      {name}
    </span>
  );
}

export function optDetail(r: PortalRecentRequest, name: string): string {
  switch (name) {
    case "RTK":
      return r.slim_rules ? `RTK rules: ${r.slim_rules}${r.slim_tokens_saved ? ` (${formatTokens(r.slim_tokens_saved)} tokens saved)` : ""}` : "RTK compression";
    case "Caveman":
      return "Caveman output compression";
    case "Terse":
      return "Terse output compression";
    case "Headroom":
      return r.headroom_tokens_saved ? `Headroom saved ${formatTokens(r.headroom_tokens_saved)} tokens` : "Headroom compression";
    case "Ponytail":
      return "Ponytail injection";
    default:
      return name;
  }
}

// ─── Token composition donut + activity highlights ────────────────────────
export function InsightsSection({ d }: { d: KeyUsageData }) {
  const daily = d.daily ?? [];
  const t = aggregate(daily);
  const totalTokens = t.prompt + t.completion;
  const inputPct = totalTokens ? Math.round((t.prompt / totalTokens) * 100) : 0;
  const busiest = daily.reduce<null | NonNullable<KeyUsageData["daily"]>[number]>(
    (max, dp) => (dp.requests > (max?.requests ?? -1) ? dp : max), null,
  );
  const avgPerReq = t.requests ? Math.round(totalTokens / t.requests) : 0;
  const pieData = [
    { name: "Input", value: t.prompt, color: C_INPUT },
    { name: "Output", value: t.completion, color: C_OUTPUT },
  ];

  return (
    <section className="grid grid-cols-1 gap-5 lg:grid-cols-2">
      {/* Token composition */}
      <div className="space-y-4">
        <SectionTitle title="Token Composition" icon={<Coins size={17} />} />
        <Card className="p-6 md:p-7">
          {totalTokens > 0 ? (
            <div className="flex items-center gap-6">
              <div className="relative h-[150px] w-[150px] shrink-0">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie data={pieData} dataKey="value" nameKey="name" innerRadius={52} outerRadius={72} paddingAngle={2} stroke="none">
                      {pieData.map((entry) => <Cell key={entry.name} fill={entry.color} />)}
                    </Pie>
                    <Tooltip content={<ChartTooltip metric="tokens" simple />} />
                  </PieChart>
                </ResponsiveContainer>
                <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
                  <span className="text-lg font-display font-semibold tabular-nums text-[var(--text)]">{formatTokens(totalTokens)}</span>
                  <span className="text-[10px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">Tokens</span>
                </div>
              </div>
              <div className="flex-1 space-y-4">
                <CompositionRow color={C_INPUT} label="Input" value={formatTokens(t.prompt)} pct={inputPct} />
                <CompositionRow color={C_OUTPUT} label="Output" value={formatTokens(t.completion)} pct={100 - inputPct} />
                <div className="border-t border-[var(--border)] pt-3 text-xs text-[var(--text-muted)]">
                  Avg <strong className="font-medium text-[var(--text)]">{formatTokens(avgPerReq)}</strong> tokens per request
                </div>
              </div>
            </div>
          ) : (
            <p className="py-8 text-center text-sm text-[var(--text-muted)]">No token usage recorded yet.</p>
          )}
        </Card>
      </div>

      {/* Highlights */}
      <div className="space-y-4">
        <SectionTitle title="Highlights" icon={<Trophy size={17} />} />
        <Card className="p-6 md:p-7">
          <div className="grid grid-cols-2 gap-x-6 gap-y-6">
            <Highlight label="Busiest Day" value={busiest && busiest.requests > 0 ? busiest.date.slice(5) : "—"} sub={busiest && busiest.requests > 0 ? `${formatNumber(busiest.requests)} requests` : "no activity"} />
            <Highlight label="Models Used" value={String(d.models?.length ?? 0)} sub="in period" />
            <Highlight label="Avg / Request" value={formatTokens(avgPerReq)} sub="tokens" />
            <Highlight label="This Month" value={`$${d.current_period.cost_usd.toFixed(2)}`} sub={`${formatNumber(d.current_period.total_requests)} requests`} />
          </div>
        </Card>
      </div>
    </section>
  );
}

// ─── Per-model breakdown ──────────────────────────────────────────────────
export function ModelSection({ models }: { models: NonNullable<KeyUsageData["models"]> }) {
  const sorted = useMemo(() => [...models].sort((a, b) => b.total_requests - a.total_requests), [models]);
  const totals = useMemo(() => sorted.reduce(
    (acc, m) => ({
      requests: acc.requests + m.total_requests,
      prompt: acc.prompt + m.prompt_tokens,
      completion: acc.completion + m.completion_tokens,
      cost: acc.cost + m.cost_usd,
    }),
    { requests: 0, prompt: 0, completion: 0, cost: 0 },
  ), [sorted]);

  return (
    <section className="space-y-4">
      <SectionTitle title="Model Breakdown" icon={<Layers size={17} />} count={sorted.length} />
      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-[var(--bg-subtle)]/50 border-b border-[var(--border)]">
              <tr className="text-[11px] uppercase tracking-wide text-[var(--text-muted)]">
                <th className="px-6 py-4 text-left font-semibold">Model</th>
                <th className="px-6 py-4 text-left font-semibold w-[26%]">Requests</th>
                <th className="px-6 py-4 text-right font-semibold">Input</th>
                <th className="px-6 py-4 text-right font-semibold">Output</th>
                <th className="px-6 py-4 text-right font-semibold">Avg / Req</th>
                <th className="px-6 py-4 text-right font-semibold">Cost</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {sorted.map((m, i) => {
                const share = totals.requests ? (m.total_requests / totals.requests) * 100 : 0;
                const avg = m.total_requests ? Math.round((m.prompt_tokens + m.completion_tokens) / m.total_requests) : 0;
                return (
                  <tr key={i} className="group transition-colors hover:bg-[var(--bg-subtle)]/30">
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-3.5">
                        <ProviderIcon provider={m.provider} />
                        <div className="flex min-w-0 flex-col">
                          <div className="flex items-center gap-2">
                            <span className="font-semibold text-[var(--text)] text-[15px] truncate">{m.model}</span>
                            {i === 0 && totals.requests > 0 && <Badge tone="accent" title="Most used model">Top</Badge>}
                          </div>
                          <span className="text-[13px] text-[var(--text-muted)] capitalize truncate">{m.provider}</span>
                        </div>
                      </div>
                    </td>
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-3">
                        <span className="tabular-nums text-[var(--text)] font-medium w-12">{formatNumber(m.total_requests)}</span>
                        <div className="h-1.5 flex-1 min-w-[48px] overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                          <div className="h-full rounded-full bg-accent-500 transition-all" style={{ width: `${Math.max(share, 2)}%` }} />
                        </div>
                        <span className="tabular-nums text-xs text-[var(--text-muted)] w-9 text-right">{share.toFixed(0)}%</span>
                      </div>
                    </td>
                    <td className="px-6 py-4 text-right tabular-nums font-mono text-[13px] text-[var(--text-muted)]">{formatTokens(m.prompt_tokens)}</td>
                    <td className="px-6 py-4 text-right tabular-nums font-mono text-[13px] text-[var(--text-muted)]">{formatTokens(m.completion_tokens)}</td>
                    <td className="px-6 py-4 text-right tabular-nums font-mono text-[13px] text-[var(--text-muted)]">{formatTokens(avg)}</td>
                    <td className={`px-6 py-4 text-right tabular-nums font-semibold text-[15px] ${m.cost_usd > 0 ? "text-[var(--text)]" : "text-[var(--text-muted)]"}`}>${m.cost_usd.toFixed(4)}</td>
                  </tr>
                );
              })}
            </tbody>
            {sorted.length > 1 && (
              <tfoot className="border-t-2 border-[var(--border)] bg-[var(--bg-subtle)]/30">
                <tr className="text-[13px]">
                  <td className="px-6 py-4 font-semibold text-[var(--text)]">Total · {sorted.length} models</td>
                  <td className="px-6 py-4 tabular-nums font-semibold text-[var(--text)]">{formatNumber(totals.requests)}</td>
                  <td className="px-6 py-4 text-right tabular-nums font-mono text-[var(--text-muted)]">{formatTokens(totals.prompt)}</td>
                  <td className="px-6 py-4 text-right tabular-nums font-mono text-[var(--text-muted)]">{formatTokens(totals.completion)}</td>
                  <td className="px-6 py-4" />
                  <td className="px-6 py-4 text-right tabular-nums font-semibold text-[var(--text)]">${totals.cost.toFixed(4)}</td>
                </tr>
              </tfoot>
            )}
          </table>
        </div>
      </Card>
    </section>
  );
}

// ─── Small presentational helpers ─────────────────────────────────────────

export function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      className="shrink-0 rounded-lg p-2 text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]"
      title="Copy"
    >
      {copied ? <Check size={16} /> : <Copy size={16} />}
    </button>
  );
}

export function SectionTitle({ title, icon, count }: { title: string; icon?: React.ReactNode; count?: number }) {
  return (
    <div className="flex items-center gap-2.5 pl-1">
      {icon && <div className="text-[var(--text-muted)]">{icon}</div>}
      <h2 className="text-sm font-semibold tracking-widest text-[var(--text-muted)] uppercase">{title}</h2>
      {count != null && (
        <span className="rounded-full bg-[var(--bg-subtle)] px-2 py-0.5 text-[11px] font-semibold text-[var(--text-muted)] tabular-nums">{count}</span>
      )}
    </div>
  );
}

export function LiveIndicator({ updatedAt }: { updatedAt?: number }) {
  const [, force] = useState(0);
  useEffect(() => {
    const t = setInterval(() => force((n) => n + 1), 15000);
    return () => clearInterval(t);
  }, []);
  const label = updatedAt ? relativeTime(updatedAt) : "live";
  return (
    <div className="hidden items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 shadow-sm sm:flex" title="Auto-refreshes every 30s">
      <span className="relative flex h-2 w-2">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent-500 opacity-60" />
        <span className="relative inline-flex h-2 w-2 rounded-full bg-accent-500" />
      </span>
      <Radio size={13} className="text-[var(--text-muted)]" />
      <span className="text-xs font-medium text-[var(--text-muted)]">{label}</span>
    </div>
  );
}

export function KpiCard({
  icon: Icon, label, value, sub, color, accent, data, dataKey, sparkId,
}: {
  icon: any; label: string; value: string; sub: string; color: string; accent?: boolean;
  data: any[]; dataKey: string; sparkId: string;
}) {
  return (
    <div className="flex flex-col justify-between rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] p-5 shadow-sm ring-1 ring-inset ring-white/50 dark:ring-0">
      <div className="flex items-center gap-2.5">
        <span className="flex h-8 w-8 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)]/60 text-[var(--text-muted)]" style={accent ? { color, borderColor: color + "40" } : undefined}>
          <Icon size={16} strokeWidth={2} />
        </span>
        <p className="text-[11px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">{label}</p>
      </div>
      <div className="mt-3">
        <p className={`text-3xl font-display font-semibold tracking-tight tabular-nums ${accent ? "text-accent-600 dark:text-accent-400" : "text-[var(--text)]"}`}>{value}</p>
        <p className="mt-1 text-xs text-[var(--text-muted)]">{sub}</p>
      </div>
      <div className="-mx-1 mt-3 h-9">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 2, right: 2, left: 2, bottom: 0 }}>
            <defs>
              <linearGradient id={sparkId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={color} stopOpacity={0.35} />
                <stop offset="100%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <Area type="monotone" dataKey={dataKey} stroke={color} strokeWidth={1.75} fill={`url(#${sparkId})`} isAnimationActive={false} />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

export function BudgetProgress({ label, used, limit, pct, alert, remaining, format }: {
  label: string; used: number; limit: number; pct: number; alert: boolean; remaining: number; format: (v: number) => string;
}) {
  if (limit <= 0) return null;
  const safePct = Math.min(Math.max(pct, 0), 100);
  const isWarning = safePct > 80 && !alert;
  const barColor = alert
    ? "bg-[color:var(--color-danger)] shadow-[0_0_10px_var(--color-danger)]"
    : isWarning ? "bg-[color:var(--color-warning)] shadow-[0_0_10px_var(--color-warning)]" : "bg-accent-500 shadow-[0_0_10px_var(--color-accent-500)]";
  return (
    <div>
      <div className="mb-2.5 flex items-end justify-between">
        <span className="text-sm font-medium text-[var(--text)]">{label}</span>
        <div className="text-right">
          <span className="text-[17px] font-display font-semibold text-[var(--text)] tabular-nums tracking-tight">{format(used)}</span>
          <span className="ml-1.5 text-sm font-medium text-[var(--text-muted)]">/ {format(limit)}</span>
        </div>
      </div>
      <div className="h-2.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)] ring-1 ring-inset ring-[var(--border)]">
        <div className={`h-full rounded-full ${barColor} transition-all duration-1000 ease-out`} style={{ width: `${safePct}%` }} />
      </div>
      <div className="mt-2 flex items-center justify-between text-xs text-[var(--text-muted)]">
        <span className="tabular-nums">{safePct.toFixed(1)}% used</span>
        <span className="tabular-nums">{format(remaining)} left</span>
      </div>
    </div>
  );
}

export function CompositionRow({ color, label, value, pct }: { color: string; label: string; value: string; pct: number }) {
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <span className="flex items-center gap-2 text-sm font-medium text-[var(--text)]">
          <span className="h-2.5 w-2.5 rounded-sm" style={{ background: color }} />
          {label}
        </span>
        <span className="text-sm tabular-nums text-[var(--text-muted)]"><strong className="font-medium text-[var(--text)]">{value}</strong> · {pct}%</span>
      </div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className="h-full rounded-full transition-all" style={{ width: `${pct}%`, background: color }} />
      </div>
    </div>
  );
}

export function Highlight({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div>
      <p className="text-[11px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">{label}</p>
      <p className="mt-1.5 text-2xl font-display font-semibold tabular-nums tracking-tight text-[var(--text)]">{value}</p>
      <p className="mt-0.5 text-xs text-[var(--text-muted)]">{sub}</p>
    </div>
  );
}

export function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5 text-[var(--text-muted)]">
      <span className="h-2 w-2 rounded-full" style={{ background: color }} />
      {label}
    </span>
  );
}

export function ChartTooltip({ active, payload, label, metric: _metric, simple }: any) {
  if (!active || !payload?.length) return null;
  const fmtVal = (key: string, v: number) => {
    if (key === "requests" || key === "Requests") return formatNumber(v);
    if (key === "cost_usd" || key === "Cost") return `$${v.toFixed(4)}`;
    return formatTokens(v);
  };
  const nameOf = (key: string) => (key === "prompt_tokens" || key === "Input" ? "Input" : key === "completion_tokens" || key === "Output" ? "Output" : key === "requests" ? "Requests" : key === "cost_usd" ? "Cost" : key);
  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3.5 py-2.5 shadow-[var(--shadow-pop)]">
      {!simple && label && <p className="mb-1.5 text-xs font-medium text-[var(--text-muted)]">{label}</p>}
      <div className="space-y-1">
        {payload.map((p: any, i: number) => (
          <div key={i} className="flex items-center gap-2 text-sm">
            <span className="h-2 w-2 rounded-full" style={{ background: p.color || p.payload?.color }} />
            <span className="text-[var(--text-muted)]">{nameOf(p.name ?? p.dataKey)}</span>
            <span className="ml-auto font-semibold tabular-nums text-[var(--text)]">{fmtVal(p.dataKey ?? p.name, p.value)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function ProviderIcon({ provider, className }: { provider: string; className?: string }) {
  const [errored, setErrored] = useState(false);
  const sizeClass = className || "h-10 w-10";
  if (errored) {
    return (
      <div className={`flex shrink-0 items-center justify-center rounded-xl bg-[var(--bg-elevated)] border border-[var(--border)] shadow-sm text-[11px] font-bold text-[var(--text-muted)] uppercase tracking-wider ${sizeClass}`}>
        {provider.slice(0, 2)}
      </div>
    );
  }
  return (
    <div className={`flex shrink-0 items-center justify-center rounded-xl bg-[var(--bg-elevated)] border border-[var(--border)] shadow-sm ${sizeClass} p-1.5`}>
      <img src={`/providers/${provider}.png`} alt={provider} onError={() => setErrored(true)} className="h-full w-full object-contain" />
    </div>
  );
}

// ─── Utilities ────────────────────────────────────────────────────────────

export const axisTick = { fontSize: 12, fill: "var(--text-muted)", fontFamily: "var(--font-sans)" } as const;

export function aggregate(daily: NonNullable<KeyUsageData["daily"]>) {
  return daily.reduce(
    (acc, dp) => ({
      requests: acc.requests + dp.requests,
      prompt: acc.prompt + dp.prompt_tokens,
      completion: acc.completion + dp.completion_tokens,
      cost: acc.cost + dp.cost_usd,
    }),
    { requests: 0, prompt: 0, completion: 0, cost: 0 },
  );
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

export function formatNumber(n: number): string {
  return Math.round(n).toLocaleString();
}

export function relativeTime(ts: number): string {
  const s = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (s < 10) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}

export function relTime(iso: string): string {
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

export function formatDateTime(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "—";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())}`;
}
