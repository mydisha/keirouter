import { useId, useMemo, useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Bar, XAxis, YAxis, Tooltip, ResponsiveContainer,
  CartesianGrid, PieChart, Pie, Cell, ComposedChart, Line, Area,
} from "recharts";
import {
  AlertTriangle, ArrowDownRight, ArrowUpRight, DollarSign, Layers,
  TrendingUp, Coins, Trophy, Infinity as InfinityIcon, Clock,
  Copy, Check, Activity, KeyRound, Gauge, type LucideIcon,
} from "lucide-react";
import {
  Button, Badge, Input, SegmentedControl, IconTile, Kpi, KpiGrid,
  SectionTitle as DsSectionTitle, TablePagination, useClientPagination,
} from "../../components/ui";
import { claimPortalKey, type KeyUsageData, type PortalRecentRequest } from "../../lib/api";
import { formatCostUSD, formatSpendUSD } from "../../lib/format";

// ─── Layout primitives (mirror the dashboard's page header and panel card) ──

export function PageHeader({
  title, subtitle, icon, action,
}: {
  title: string;
  subtitle?: ReactNode;
  icon?: LucideIcon;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
      <div className="flex min-w-0 items-center gap-3">
        {icon && <IconTile icon={icon} size="lg" />}
        <div className="min-w-0">
          <h1 className="truncate text-[22px] font-semibold tracking-[-0.015em] text-fg">{title}</h1>
          {subtitle && <p className="mt-0.5 text-[13px] text-fg-muted">{subtitle}</p>}
        </div>
      </div>
      {action && <div className="flex flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

// PanelCard is the dashboard's bordered section: a header row with a tone
// icon and title, then the body (rows, a table or a chart).
export function PanelCard({
  icon, title, subtitle, count, action, children, footer, className = "",
}: {
  icon?: LucideIcon;
  title: ReactNode;
  subtitle?: ReactNode;
  count?: number;
  action?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
}) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={`overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)] ${className}`}>
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <DsSectionTitle
          icon={icon}
          id={headingId}
          title={
            <span className="inline-flex items-center gap-2">
              {title}
              {count != null && (
                <span className="rounded-md bg-subtle px-1.5 py-px text-[11.5px] font-medium tabular-nums text-fg-muted">{count}</span>
              )}
            </span>
          }
          subtitle={subtitle}
          action={action}
        />
      </div>
      {children}
      {footer}
    </section>
  );
}

// PanelRow is one label/description-left, value-right line inside a panel.
export function PanelRow({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-6 sm:px-5">
      <div className="min-w-0">
        <div className="text-[13px] font-medium text-fg">{label}</div>
        {hint && <div className="mt-0.5 text-[12px] text-fg-muted">{hint}</div>}
      </div>
      {children != null && <div className="min-w-0 text-[13px] text-fg sm:text-right">{children}</div>}
    </div>
  );
}

// IdField shows a monospace identifier with copy (and optional extra) controls.
export function IdField({ value, children }: { value: string; children?: ReactNode }) {
  return (
    <div className="flex items-center gap-1 rounded-lg border border-line bg-subtle py-1 pl-3 pr-1">
      <code className="min-w-0 flex-1 break-all py-1 font-mono text-[13px] text-fg">{value}</code>
      {children}
      <CopyButton value={value} />
    </div>
  );
}

// ─── Shared portal onboarding surfaces ────────────────────────────────────

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
    <div className="mx-auto w-full max-w-lg rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="flex items-center gap-3 border-b border-line px-5 py-4">
        <IconTile icon={KeyRound} size="md" />
        <h1 className="text-[15px] font-semibold text-fg">Your API key is ready</h1>
      </div>
      <div className="space-y-4 p-5">
        <p className="text-[13px] text-fg-muted">
          Copy this key now and store it securely. For your protection it is shown
          only once and cannot be retrieved later.
        </p>
        <IdField value={value} />
        <Button onClick={onDone} className="w-full">{doneLabel}</Button>
      </div>
    </div>
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
      className="space-y-2"
    >
      <label htmlFor="portal-claim-key" className="block text-[12.5px] font-medium text-fg">
        {label}
      </label>
      <div className="flex flex-col gap-2 sm:flex-row">
        <Input
          id="portal-claim-key"
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="kr_..."
          className="font-mono"
        />
        <Button type="submit" variant="ghost" className="shrink-0" disabled={!value.trim() || claim.isPending}>
          {claim.isPending ? "Claiming…" : submitLabel}
        </Button>
      </div>
      {claim.error instanceof Error && (
        <p role="alert" className="text-[12.5px] text-bad">{claim.error.message}</p>
      )}
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
    <div className="mx-auto w-full max-w-lg rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="flex items-center gap-3 border-b border-line px-5 py-4">
        <IconTile icon={KeyRound} size="md" />
        <div className="min-w-0">
          <h2 className="text-[15px] font-semibold text-fg">Set up your API key</h2>
          {email && (
            <p className="mt-0.5 truncate text-[12.5px] text-fg-muted">
              Signed in as <span className="font-medium text-fg">{email}</span>
            </p>
          )}
        </div>
      </div>

      <div className="space-y-4 p-5">
        {provisioning ? (
          <>
            <p className="text-[13px] text-fg-muted">{description}</p>
            {createError && <p role="alert" className="text-[12.5px] text-bad">{createError}</p>}
            <Button onClick={onCreate} disabled={creating} className="w-full">
              {creating ? "Creating…" : "Create API key"}
            </Button>
          </>
        ) : (
          <p className="text-[13px] text-fg-muted">
            Automatic key provisioning is disabled. Contact an administrator to set up your key.
          </p>
        )}
      </div>

      <div className="border-t border-line bg-subtle/60 p-5">
        <ClaimForm label={claimLabel} submitLabel={claimSubmitLabel} />
      </div>
    </div>
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
    <SegmentedControl<string>
      label="Time range"
      value={String(days)}
      onChange={(v) => onChange(Number(v))}
      options={DATE_RANGES.map((r) => ({ value: String(r.value), label: r.label }))}
    />
  );
}

// ─── Overview: KPI strip + limits ──────────────────────────────────────────
export function OverviewSection({ d }: { d: KeyUsageData }) {
  const daily = d.daily ?? [];
  const t = useMemo(() => aggregate(daily), [daily]);
  const totalTokens = t.prompt + t.completion;
  const inputPct = totalTokens ? Math.round((t.prompt / totalTokens) * 100) : 0;
  const activeDays = daily.filter((x) => x.requests > 0).length;
  const avgPerReq = t.requests ? Math.round(totalTokens / t.requests) : 0;
  const budgets = d.budgets ?? [];

  return (
    <div className="space-y-6">
      <KpiGrid cols={4} label="Period summary">
        <Kpi
          icon={Activity}
          tone="blue"
          label="Requests"
          value={formatNumber(t.requests)}
          hint={`${avgPerReq ? formatTokens(avgPerReq) : 0} tokens / req`}
          trend={daily.map((x) => x.requests)}
        />
        <Kpi
          icon={DollarSign}
          tone="orange"
          label="Total cost"
          value={formatCostUSD(t.cost)}
          hint={activeDays ? `~${formatCostUSD(t.cost / activeDays)} / day` : "no spend yet"}
          trend={daily.map((x) => x.cost_usd)}
        />
        <Kpi
          icon={ArrowDownRight}
          tone="violet"
          label="Input tokens"
          value={formatTokens(t.prompt)}
          hint={`${inputPct}% of tokens`}
          trend={daily.map((x) => x.prompt_tokens)}
        />
        <Kpi
          icon={ArrowUpRight}
          tone="teal"
          label="Output tokens"
          value={formatTokens(t.completion)}
          hint={`${100 - inputPct}% of tokens`}
          trend={daily.map((x) => x.completion_tokens)}
        />
      </KpiGrid>

      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-5">
        <PanelCard
          icon={Gauge}
          title="Allocations"
          className="lg:col-span-3"
          action={
            budgets.length > 0 ? (
              <Badge tone={budgets.some((b) => b.alert) ? "danger" : "neutral"}>
                {budgets.length} limit{budgets.length === 1 ? "" : "s"}
              </Badge>
            ) : undefined
          }
        >
          {budgets.length > 0 ? (
            <div className="divide-y divide-line">
              {budgets.map((b, i) => (
                <div key={i} className="space-y-4 px-4 py-4 sm:px-5">
                  <div className="flex items-center justify-between gap-3">
                    <p className="text-[13px] font-medium text-fg">
                      {b.period === "total" ? "All-time" : b.period.charAt(0).toUpperCase() + b.period.slice(1)} limit
                    </p>
                    {b.alert && (
                      <Badge tone="danger">
                        <span className="inline-flex items-center gap-1"><AlertTriangle className="h-3 w-3" aria-hidden="true" /> Exceeded</span>
                      </Badge>
                    )}
                  </div>
                  {b.limit_tokens > 0 && (
                    <BudgetProgress label="Tokens" used={b.tokens_used} limit={b.limit_tokens} pct={b.tokens_pct_used} alert={b.alert} remaining={b.tokens_remaining} format={formatTokens} />
                  )}
                  {b.limit_usd > 0 && (
                    <BudgetProgress label="Spend" used={b.spent_usd} limit={b.limit_usd} pct={b.usd_pct_used} alert={b.alert} remaining={b.usd_remaining} format={formatSpendUSD} />
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div className="flex items-center gap-3 px-4 py-4 sm:px-5">
              <IconTile icon={InfinityIcon} size="md" tone="ok" />
              <div>
                <p className="text-[13px] font-medium text-fg">Unrestricted</p>
                <p className="mt-0.5 text-[12px] text-fg-muted">This key has no configured budget limits and can be used indefinitely.</p>
              </div>
            </div>
          )}
        </PanelCard>

        <PanelCard icon={Clock} title="Period summary" className="lg:col-span-2">
          <div className="divide-y divide-line">
            <PanelRow label="Active days" hint="Days with at least one request">
              <span className="tabular-nums">{activeDays}</span>
            </PanelRow>
            <PanelRow label="This month so far" hint={`${formatNumber(d.current_period.total_requests)} requests`}>
              <span className="font-semibold tabular-nums">{formatCostUSD(d.current_period.cost_usd)}</span>
            </PanelRow>
            <PanelRow label="Total tokens" hint={`${formatTokens(t.prompt)} in · ${formatTokens(t.completion)} out`}>
              <span className="tabular-nums">{formatTokens(totalTokens)}</span>
            </PanelRow>
          </div>
        </PanelCard>
      </div>
    </div>
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
        : formatCostUSD(t.cost);

  return (
    <PanelCard
      icon={TrendingUp}
      title="Usage trend"
      action={
        <SegmentedControl<Metric>
          label="Chart metric"
          value={metric}
          onChange={setMetric}
          options={[
            { value: "tokens", label: "Tokens" },
            { value: "requests", label: "Requests" },
            { value: "cost", label: "Cost" },
          ]}
        />
      }
    >
      <div className="p-4 sm:p-5">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="text-[12px] text-fg-muted">{days}-day total</p>
            <p className="mt-0.5 text-[20px] font-semibold tabular-nums tracking-[-0.01em] text-fg">{headline}</p>
          </div>
          <div className="flex items-center gap-4 text-[12px]">
            {metric === "tokens" && (<><LegendDot color={C_INPUT} label="Input" /><LegendDot color={C_OUTPUT} label="Output" /><LegendDot color={C_REQ} label="Requests" /></>)}
            {metric === "requests" && (<><LegendDot color={C_REQ} label="Requests" /><LegendDot color={C_COST} label="Cost" /></>)}
            {metric === "cost" && (<><LegendDot color={C_COST} label="Cost" /><LegendDot color={C_REQ} label="Requests" /></>)}
          </div>
        </div>

        <div className="h-[260px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            {metric === "requests" ? (
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -18, bottom: 0 }}>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} dy={10} minTickGap={24} />
                <YAxis yAxisId="left" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatNumber} width={56} />
                <YAxis yAxisId="right" orientation="right" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={(v) => `$${v}`} width={52} />
                <Tooltip cursor={{ fill: "var(--bg-subtle)", opacity: 0.6 }} content={<ChartTooltip metric={metric} />} />
                <Bar yAxisId="left" dataKey="requests" fill={C_REQ} fillOpacity={0.85} radius={[3, 3, 0, 0]} maxBarSize={32} name="Requests" />
                <Line yAxisId="right" type="monotone" dataKey="cost_usd" stroke={C_COST} strokeWidth={2} dot={false} name="Cost" />
              </ComposedChart>
            ) : metric === "cost" ? (
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -8, bottom: 0 }}>
                <defs>
                  <linearGradient id="costFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={C_COST} stopOpacity={0.25} />
                    <stop offset="95%" stopColor={C_COST} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} dy={10} minTickGap={24} />
                <YAxis yAxisId="left" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={(v) => `$${v}`} width={56} />
                <YAxis yAxisId="right" orientation="right" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatNumber} width={52} />
                <Tooltip content={<ChartTooltip metric={metric} />} />
                <Area yAxisId="left" type="monotone" dataKey="cost_usd" stroke={C_COST} strokeWidth={2} fill="url(#costFill)" name="Cost" />
                <Line yAxisId="right" type="monotone" dataKey="requests" stroke={C_REQ} strokeWidth={1.75} strokeDasharray="4 3" dot={false} name="Requests" />
              </ComposedChart>
            ) : (
              <ComposedChart data={chartData} margin={{ top: 10, right: 8, left: -18, bottom: 0 }}>
                <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} dy={10} minTickGap={24} />
                <YAxis yAxisId="left" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatTokens} width={56} />
                <YAxis yAxisId="right" orientation="right" tick={axisTick} tickLine={false} axisLine={false} tickFormatter={formatNumber} width={52} />
                <Tooltip cursor={{ fill: "var(--bg-subtle)", opacity: 0.6 }} content={<ChartTooltip metric={metric} />} />
                <Bar yAxisId="left" dataKey="prompt_tokens" stackId="tok" fill={C_INPUT} fillOpacity={0.9} radius={[0, 0, 0, 0]} maxBarSize={32} name="Input" />
                <Bar yAxisId="left" dataKey="completion_tokens" stackId="tok" fill={C_OUTPUT} fillOpacity={0.9} radius={[3, 3, 0, 0]} maxBarSize={32} name="Output" />
                <Line yAxisId="right" type="monotone" dataKey="requests" stroke={C_REQ} strokeWidth={2} dot={false} name="Requests" />
              </ComposedChart>
            )}
          </ResponsiveContainer>
        </div>
      </div>
    </PanelCard>
  );
}

// Dashboard table chrome, shared by every portal table.
export const THEAD = "border-b border-line bg-subtle text-[12px] text-fg-muted";
export const TH = "px-4 py-2.5 font-medium whitespace-nowrap";
export const TD = "px-4 py-2.5";

// ─── Recent Requests table ─────────────────────────────────────────────────
export function RecentRequestsSection({ recent, days: _days }: { recent: PortalRecentRequest[]; days: number }) {
  const { page, pages, paged, setPage, total } = useClientPagination(recent, 15);

  return (
    <PanelCard
      icon={Clock}
      title="Request log"
      count={recent.length}
      footer={
        pages > 1 ? (
          <div className="border-t border-line px-4 py-2.5 sm:px-5">
            <TablePagination page={page} pages={pages} total={total} onPage={setPage} label="Request log pages" />
          </div>
        ) : undefined
      }
    >
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] text-[13px]">
          <thead className={THEAD}>
            <tr>
              <th scope="col" className={`${TH} text-left`}>Model</th>
              <th scope="col" className={`${TH} text-right`}>
                <span className="inline-flex items-center justify-end gap-1">
                  <ArrowDownRight className="h-3 w-3 text-[var(--color-chart-1)]" aria-hidden="true" /> In
                </span>
              </th>
              <th scope="col" className={`${TH} text-right`}>
                <span className="inline-flex items-center justify-end gap-1">
                  <ArrowUpRight className="h-3 w-3 text-[var(--color-chart-2)]" aria-hidden="true" /> Out
                </span>
              </th>
              <th scope="col" className={`${TH} text-right`}>Cost</th>
              <th scope="col" className={`${TH} text-center`}>Optimizations</th>
              <th scope="col" className={`${TH} text-right`}>Latency</th>
              <th scope="col" className={`${TH} text-right`}>Date</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {paged.map((r) => {
              const opts = r.optimizations ?? [];
              return (
                <tr key={r.id} className="transition-colors hover:bg-hover">
                  <td className={TD}>
                    <div className="flex items-center gap-2.5">
                      <ProviderIcon provider={r.provider} className="h-7 w-7" />
                      <span className="truncate font-mono text-[12.5px] text-fg">{r.model}</span>
                    </div>
                  </td>
                  <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg`}>{formatTokens(r.prompt_tokens)}</td>
                  <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg`}>{formatTokens(r.completion_tokens)}</td>
                  <td className={`${TD} text-right tabular-nums text-fg`}>{formatCostUSD(r.cost_usd)}</td>
                  <td className={`${TD} text-center`}>
                    {opts.length > 0 ? (
                      <div className="flex flex-wrap items-center justify-center gap-1">
                        {opts.map((opt) => (
                          <OptBadge key={opt} name={opt} detail={optDetail(r, opt)} />
                        ))}
                      </div>
                    ) : (
                      <span className="text-fg-faint">—</span>
                    )}
                  </td>
                  <td className={`${TD} text-right tabular-nums text-fg-muted`}>
                    {r.latency_ms > 0 ? `${r.latency_ms}ms` : r.cache_hit ? "cache" : "—"}
                  </td>
                  <td className={`${TD} whitespace-nowrap text-right`}>
                    <div className="flex flex-col items-end leading-tight">
                      <span className="tabular-nums text-fg">{formatDateTime(r.created_at)}</span>
                      <span className="text-[11.5px] tabular-nums text-fg-faint">{relTime(r.created_at)} ago</span>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </PanelCard>
  );
}

export function OptBadge({ name, detail }: { name: string; detail?: string }) {
  return (
    <span title={detail || name}>
      <Badge tone="accent">{name}</Badge>
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
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
      <PanelCard icon={Coins} title="Token composition">
        <div className="p-4 sm:p-5">
          {totalTokens > 0 ? (
            <div className="flex items-center gap-6">
              <div className="relative h-[136px] w-[136px] shrink-0">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie data={pieData} dataKey="value" nameKey="name" innerRadius={48} outerRadius={66} paddingAngle={2} stroke="none">
                      {pieData.map((entry) => <Cell key={entry.name} fill={entry.color} />)}
                    </Pie>
                    <Tooltip content={<ChartTooltip metric="tokens" simple />} />
                  </PieChart>
                </ResponsiveContainer>
                <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
                  <span className="text-[16px] font-semibold tabular-nums text-fg">{formatTokens(totalTokens)}</span>
                  <span className="text-[11px] text-fg-muted">tokens</span>
                </div>
              </div>
              <div className="flex-1 space-y-4">
                <CompositionRow color={C_INPUT} label="Input" value={formatTokens(t.prompt)} pct={inputPct} />
                <CompositionRow color={C_OUTPUT} label="Output" value={formatTokens(t.completion)} pct={100 - inputPct} />
                <div className="border-t border-line pt-3 text-[12px] text-fg-muted">
                  Avg <span className="font-medium tabular-nums text-fg">{formatTokens(avgPerReq)}</span> tokens per request
                </div>
              </div>
            </div>
          ) : (
            <p className="py-8 text-center text-[13px] text-fg-muted">No token usage recorded yet.</p>
          )}
        </div>
      </PanelCard>

      <PanelCard icon={Trophy} title="Highlights">
        <dl className="grid grid-cols-2 gap-px bg-line">
          <Highlight label="Busiest day" value={busiest && busiest.requests > 0 ? busiest.date.slice(5) : "—"} sub={busiest && busiest.requests > 0 ? `${formatNumber(busiest.requests)} requests` : "no activity"} />
          <Highlight label="Models used" value={String(d.models?.length ?? 0)} sub="in period" />
          <Highlight label="Avg / request" value={formatTokens(avgPerReq)} sub="tokens" />
          <Highlight label="This month" value={formatCostUSD(d.current_period.cost_usd)} sub={`${formatNumber(d.current_period.total_requests)} requests`} />
        </dl>
      </PanelCard>
    </div>
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
    <PanelCard icon={Layers} title="Model breakdown" count={sorted.length}>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px] text-[13px]">
          <thead className={THEAD}>
            <tr>
              <th scope="col" className={`${TH} text-left`}>Model</th>
              <th scope="col" className={`${TH} w-[26%] text-left`}>Requests</th>
              <th scope="col" className={`${TH} text-right`}>Input</th>
              <th scope="col" className={`${TH} text-right`}>Output</th>
              <th scope="col" className={`${TH} text-right`}>Avg / req</th>
              <th scope="col" className={`${TH} text-right`}>Cost</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {sorted.map((m, i) => {
              const share = totals.requests ? (m.total_requests / totals.requests) * 100 : 0;
              const avg = m.total_requests ? Math.round((m.prompt_tokens + m.completion_tokens) / m.total_requests) : 0;
              return (
                <tr key={i} className="transition-colors hover:bg-hover">
                  <td className={TD}>
                    <div className="flex items-center gap-2.5">
                      <ProviderIcon provider={m.provider} className="h-7 w-7" />
                      <span className="truncate font-mono text-[12.5px] text-fg">{m.model}</span>
                      {i === 0 && totals.requests > 0 && <Badge tone="accent" title="Most used model">Top</Badge>}
                    </div>
                  </td>
                  <td className={TD}>
                    <div className="flex items-center gap-3">
                      <span className="w-12 font-medium tabular-nums text-fg">{formatNumber(m.total_requests)}</span>
                      <div className="h-1.5 min-w-[48px] flex-1 overflow-hidden rounded-full bg-subtle">
                        <div className="h-full rounded-full bg-accent-500" style={{ width: `${Math.max(share, 2)}%` }} />
                      </div>
                      <span className="w-9 text-right text-[12px] tabular-nums text-fg-muted">{share.toFixed(0)}%</span>
                    </div>
                  </td>
                  <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg-muted`}>{formatTokens(m.prompt_tokens)}</td>
                  <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg-muted`}>{formatTokens(m.completion_tokens)}</td>
                  <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg-muted`}>{formatTokens(avg)}</td>
                  <td className={`${TD} text-right font-semibold tabular-nums ${m.cost_usd > 0 ? "text-fg" : "text-fg-muted"}`}>{formatCostUSD(m.cost_usd)}</td>
                </tr>
              );
            })}
          </tbody>
          {sorted.length > 1 && (
            <tfoot className="border-t border-line bg-subtle">
              <tr>
                <td className={`${TD} font-medium text-fg`}>Total · {sorted.length} models</td>
                <td className={`${TD} font-medium tabular-nums text-fg`}>{formatNumber(totals.requests)}</td>
                <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg-muted`}>{formatTokens(totals.prompt)}</td>
                <td className={`${TD} text-right font-mono text-[12.5px] tabular-nums text-fg-muted`}>{formatTokens(totals.completion)}</td>
                <td className={TD} />
                <td className={`${TD} text-right font-semibold tabular-nums text-fg`}>{formatCostUSD(totals.cost)}</td>
              </tr>
            </tfoot>
          )}
        </table>
      </div>
    </PanelCard>
  );
}

// ─── Small presentational helpers ─────────────────────────────────────────

export function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
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
      className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      title={copied ? "Copied" : label}
      aria-label={copied ? "Copied" : label}
    >
      {copied ? <Check className="h-4 w-4 text-ok" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}
    </button>
  );
}

export function BudgetProgress({ label, used, limit, pct, alert, remaining, format }: {
  label: string; used: number; limit: number; pct: number; alert: boolean; remaining: number; format: (v: number) => string;
}) {
  if (limit <= 0) return null;
  const safePct = Math.min(Math.max(pct, 0), 100);
  const isWarning = safePct > 80 && !alert;
  const barColor = alert ? "bg-bad" : isWarning ? "bg-warn" : "bg-accent-500";
  return (
    <div>
      <div className="mb-1.5 flex items-baseline justify-between gap-3 text-[13px]">
        <span className="text-fg-muted">{label}</span>
        <span className="tabular-nums">
          <span className="font-semibold text-fg">{format(used)}</span>
          <span className="text-fg-muted"> of {format(limit)}</span>
        </span>
      </div>
      <div
        className="h-1.5 w-full overflow-hidden rounded-full bg-subtle"
        role="progressbar"
        aria-label={`${label} used`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(safePct)}
      >
        <div className={`h-full rounded-full ${barColor} transition-[width] duration-700 ease-out`} style={{ width: `${safePct}%` }} />
      </div>
      <div className="mt-1.5 flex items-center justify-between text-[12px] tabular-nums text-fg-muted">
        <span>{safePct.toFixed(1)}% used</span>
        <span>{format(remaining)} left</span>
      </div>
    </div>
  );
}

export function CompositionRow({ color, label, value, pct }: { color: string; label: string; value: string; pct: number }) {
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between text-[13px]">
        <span className="flex items-center gap-2 font-medium text-fg">
          <span className="h-2 w-2 rounded-sm" style={{ background: color }} aria-hidden="true" />
          {label}
        </span>
        <span className="tabular-nums text-fg-muted"><span className="font-medium text-fg">{value}</span> · {pct}%</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-subtle">
        <div className="h-full rounded-full" style={{ width: `${pct}%`, background: color }} />
      </div>
    </div>
  );
}

export function Highlight({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="bg-surface px-4 py-3.5 sm:px-5">
      <dt className="text-[12px] font-medium text-fg-muted">{label}</dt>
      <dd className="mt-0.5 text-[18px] font-semibold tabular-nums tracking-[-0.01em] text-fg">{value}</dd>
      <dd className="mt-0.5 text-[12px] text-fg-faint">{sub}</dd>
    </div>
  );
}

export function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5 text-fg-muted">
      <span className="h-2 w-2 rounded-full" style={{ background: color }} aria-hidden="true" />
      {label}
    </span>
  );
}

export function ChartTooltip({ active, payload, label, metric: _metric, simple }: any) {
  if (!active || !payload?.length) return null;
  const fmtVal = (key: string, v: number) => {
    if (key === "requests" || key === "Requests") return formatNumber(v);
    if (key === "cost_usd" || key === "Cost") return formatCostUSD(v);
    return formatTokens(v);
  };
  const nameOf = (key: string) => (key === "prompt_tokens" || key === "Input" ? "Input" : key === "completion_tokens" || key === "Output" ? "Output" : key === "requests" ? "Requests" : key === "cost_usd" ? "Cost" : key);
  return (
    <div className="rounded-lg border border-line bg-surface px-3 py-2 shadow-[var(--shadow-pop)]">
      {!simple && label && <p className="mb-1 text-[12px] text-fg-muted">{label}</p>}
      <div className="space-y-0.5">
        {payload.map((p: any, i: number) => (
          <div key={i} className="flex items-center gap-2 text-[12.5px]">
            <span className="h-2 w-2 rounded-full" style={{ background: p.color || p.payload?.color }} />
            <span className="text-fg-muted">{nameOf(p.name ?? p.dataKey)}</span>
            <span className="ml-auto pl-3 font-medium tabular-nums text-fg">{fmtVal(p.dataKey ?? p.name, p.value)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function ProviderIcon({ provider, className }: { provider: string; className?: string }) {
  const [errored, setErrored] = useState(false);
  const sizeClass = className || "h-9 w-9";
  if (errored) {
    return (
      <div className={`flex shrink-0 items-center justify-center rounded-lg border border-line bg-subtle font-mono text-[10.5px] font-medium uppercase text-fg-muted ${sizeClass}`}>
        {provider ? provider.slice(0, 2) : "—"}
      </div>
    );
  }
  return (
    <div className={`flex shrink-0 items-center justify-center rounded-lg border border-line bg-surface p-1 ${sizeClass}`}>
      <img src={`/providers/${provider}.png`} alt="" onError={() => setErrored(true)} className="h-full w-full object-contain" />
    </div>
  );
}

// ─── Utilities ────────────────────────────────────────────────────────────

export const axisTick = { fontSize: 11.5, fill: "var(--text-muted)", fontFamily: "var(--font-sans)" } as const;

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
