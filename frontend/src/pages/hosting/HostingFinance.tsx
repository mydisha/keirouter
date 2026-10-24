import { useMemo, useState, type FormEvent } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bar, CartesianGrid, ComposedChart, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import {
  AlertTriangle,
  Calculator,
  Check,
  Coins,
  Loader2,
  Pencil,
  PiggyBank,
  Plus,
  Receipt,
  Repeat,
  Scale,
  Server,
  TrendingUp,
  Trash2,
  Wallet,
} from "lucide-react";
import { cn } from "@/lib/utils";
import {
  api,
  type HostingCost,
  type HostingCostCategory,
  type HostingCostInput,
  type HostingCostPeriod,
  type HostingFinance,
  type HostingFinanceMonth,
} from "../../lib/api";
import { useToast } from "../../components/Toast";
import { useConfirm } from "../../components/ui/confirm-dialog";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBanner,
  Field,
  Input,
  Kpi,
  KpiGrid,
  LoadingRegion,
  Modal,
  ModalFooter,
  SegmentedControl,
  Select,
  Skeleton,
  Textarea,
} from "../../components/ui";
import { Panel, TH, THEAD, count, dateID, errorText, idr, todayISO } from "../payments/shared";
import { QK_HOSTING, idrCompact, monthLabel, plainDateID } from "./shared";

type Range = "6" | "12" | "24";

const CATEGORY_LABEL: Record<HostingCostCategory, string> = {
  vps: "VPS",
  domain: "Domain",
  software: "Software",
  other: "Other",
};

const CATEGORY_TONE: Record<HostingCostCategory, "accent" | "secondary" | "neutral" | "warning"> = {
  vps: "accent",
  domain: "secondary",
  software: "warning",
  other: "neutral",
};

// Chart series colours. Revenue is the brand blue; cost and profit are the
// next two slots of a categorical order validated for colour-vision
// deficiency in both themes (dataviz validate_palette: blue · orange · aqua,
// all-pairs). The light orange/aqua sit under 3:1 on the card, so the chart
// always ships a legend, a tooltip and a table view.
const VIZ_VARS =
  "[--viz-rev:var(--color-accent-500)] [--viz-cost:#ea7a3a] [--viz-profit:#1baf7a] dark:[--viz-cost:#e06f30] dark:[--viz-profit:#199e70]";
const C_REV = "var(--viz-rev)";
const C_COST = "var(--viz-cost)";
const C_PROFIT = "var(--viz-profit)";

const axisTick = { fontSize: 11.5, fill: "var(--text-muted)", fontFamily: "var(--font-sans)" } as const;

function signedIdr(n: number): string {
  return n > 0 ? `+${idr(n)}` : idr(n);
}

export function FinanceTab() {
  const [range, setRange] = useState<Range>("12");
  const q = useQuery({
    queryKey: [...QK_HOSTING.finance, range],
    queryFn: () => api.hostingFinance(Number(range)),
    placeholderData: keepPreviousData,
  });
  const f = q.data;

  if (q.isLoading && !f) {
    return (
      <LoadingRegion label="Loading hosting finance" className="space-y-4">
        <Skeleton className="h-[188px] w-full rounded-2xl" />
        <Skeleton className="h-[340px] w-full rounded-2xl" />
      </LoadingRegion>
    );
  }
  if (!f) {
    return <ErrorBanner message={`Couldn't load hosting finance. ${q.isError ? errorText(q.error) : ""}`.trim()} />;
  }

  return (
    <div className="space-y-6">
      {q.isError && <ErrorBanner message={`Couldn't refresh the data. The numbers below may be out of date. ${errorText(q.error)}`} />}
      <RefundWarning refunds={f.refunds_needed ?? []} />
      <CurrentMonth f={f} />
      <MonthlyChart f={f} range={range} onRange={setRange} fetching={q.isFetching} />
      <CostsPanel costs={f.costs} />
    </div>
  );
}

// ─── Current month ──────────────────────────────────────────────────────────

function CurrentMonth({ f }: { f: HostingFinance }) {
  const c = f.current;
  const month = monthLabel(c.month);
  const gap = c.break_even_instances - c.running_instances;
  return (
    <div className="space-y-2">
      <KpiGrid cols={4} label={`Hosting finance, ${month}`}>
        <Kpi icon={Wallet} label={`Revenue, ${month}`} value={idr(c.revenue_idr)} hint="Gross, before SumoPod fees" />
        <Kpi icon={Receipt} label="Costs" value={idr(c.cost_idr)} hint="Operating costs this month" />
        <Kpi
          icon={PiggyBank}
          label="Profit"
          value={signedIdr(c.profit_idr)}
          tone={c.profit_idr < 0 ? "bad" : c.profit_idr > 0 ? "ok" : "section"}
          valueClassName={c.profit_idr < 0 ? "text-bad" : c.profit_idr > 0 ? "text-ok" : ""}
          hint={c.profit_idr < 0 ? "Loss this month" : c.profit_idr > 0 ? "Profit this month" : "Break-even"}
        />
        <Kpi
          icon={Repeat}
          label="MRR"
          value={idr(c.mrr_idr)}
          hint={c.price_idr > 0 ? `${count(c.running_instances)} running, avg ${idr(c.price_idr)}` : "No price set"}
        />
      </KpiGrid>
      <KpiGrid cols={3} label="Capacity and break-even">
        <Kpi icon={Server} label="Running instances" value={count(c.running_instances)} hint="Active, plus expired ones not yet stopped" />
        <Kpi
          icon={Calculator}
          label="Cost per instance"
          value={c.running_instances > 0 ? idr(c.cost_per_instance_idr) : "-"}
          hint={c.running_instances > 0 ? "This month's costs ÷ running instances" : "No running instances yet"}
        />
        <Kpi
          icon={Scale}
          label="Break-even"
          value={c.price_idr > 0 ? `${count(c.break_even_instances)} ${c.break_even_instances === 1 ? "instance" : "instances"}` : "-"}
          tone={c.price_idr > 0 && c.cost_idr > 0 ? (gap <= 0 ? "ok" : "warn") : "section"}
          hint={
            c.price_idr <= 0
              ? "Set a price in Hosting setup"
              : c.cost_idr <= 0
                ? "No costs this month yet"
                : gap <= 0
                  ? "Costs covered this month"
                  : `${count(gap)} more instances to break even`
          }
        />
      </KpiGrid>
      <p className="text-[12px] text-fg-muted">
        Revenue is the gross value of paid orders. SumoPod fees aren't deducted, so actual profit is slightly lower.
      </p>
    </div>
  );
}

// ─── Monthly chart ──────────────────────────────────────────────────────────

type Row = HostingFinanceMonth & { label: string };

function MonthlyChart({ f, range, onRange, fetching }: { f: HostingFinance; range: Range; onRange: (r: Range) => void; fetching: boolean }) {
  const [view, setView] = useState<"chart" | "table">("chart");
  const rows: Row[] = useMemo(() => f.months.map((m) => ({ ...m, label: monthLabel(m.month) })), [f.months]);
  const hasData = rows.some((r) => r.revenue_idr !== 0 || r.cost_idr !== 0);
  const hasLoss = rows.some((r) => r.profit_idr < 0);
  const t = f.totals;

  return (
    <Panel
      icon={TrendingUp}
      title="Revenue vs costs"
      subtitle={
        <span className="tabular-nums">
          Last {range} months · revenue {idr(t.revenue_idr)} · costs {idr(t.cost_idr)} · profit{" "}
          <span className={cn("font-medium", t.profit_idr < 0 ? "text-bad" : t.profit_idr > 0 ? "text-ok" : "text-fg")}>{signedIdr(t.profit_idr)}</span>
        </span>
      }
      action={
        <div className="flex flex-wrap items-center gap-2">
          {fetching && <Loader2 className="h-4 w-4 animate-spin text-fg-faint" aria-label="Loading" />}
          <SegmentedControl<Range>
            label="Month range"
            value={range}
            onChange={onRange}
            options={[
              { value: "6", label: "6 months" },
              { value: "12", label: "12 months" },
              { value: "24", label: "24 months" },
            ]}
          />
          <SegmentedControl<"chart" | "table">
            label="View"
            value={view}
            onChange={setView}
            options={[
              { value: "chart", label: "Chart" },
              { value: "table", label: "Table" },
            ]}
          />
        </div>
      }
    >
      {!hasData ? (
        <EmptyState
          icon={TrendingUp}
          size="sm"
          title="No revenue or costs yet"
          hint="The chart fills in once a hosting order is paid or an operating cost is added below."
        />
      ) : view === "chart" ? (
        <div className={cn("px-4 pb-3 pt-4", VIZ_VARS)}>
          <ul className="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-fg-muted" aria-label="Chart legend">
            <LegendItem color={C_REV} label="Revenue" />
            <LegendItem color={C_COST} label="Costs" />
            <LegendItem color={C_PROFIT} label="Profit" line />
          </ul>
          <div className="h-[280px] w-full" role="img" aria-label={chartSummary(rows)}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={rows} margin={{ top: 8, right: 8, left: 0, bottom: 0 }} barGap={2} barCategoryGap="24%">
                <CartesianGrid vertical={false} stroke="var(--border)" />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={{ stroke: "var(--border-strong)" }} dy={6} minTickGap={16} interval="preserveStartEnd" />
                <YAxis tick={axisTick} tickLine={false} axisLine={false} tickFormatter={idrCompact} width={72} />
                {hasLoss && <ReferenceLine y={0} stroke="var(--border-strong)" />}
                <Tooltip cursor={{ fill: "var(--bg-subtle)", opacity: 0.7 }} content={<FinanceTooltip />} />
                <Bar dataKey="revenue_idr" name="Revenue" fill={C_REV} radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false} />
                <Bar dataKey="cost_idr" name="Costs" fill={C_COST} radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false} />
                <Line
                  dataKey="profit_idr"
                  name="Profit"
                  type="monotone"
                  stroke={C_PROFIT}
                  strokeWidth={2}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  dot={{ r: 4, fill: C_PROFIT, stroke: "var(--bg-elevated)", strokeWidth: 2 }}
                  activeDot={{ r: 5, fill: C_PROFIT, stroke: "var(--bg-elevated)", strokeWidth: 2 }}
                  isAnimationActive={false}
                />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </div>
      ) : (
        <MonthlyTable rows={rows} />
      )}
    </Panel>
  );
}

function chartSummary(rows: Row[]): string {
  if (!rows.length) return "Monthly revenue, costs and profit chart.";
  const last = rows[rows.length - 1];
  const best = rows.reduce((a, b) => (b.profit_idr > a.profit_idr ? b : a));
  return `Monthly revenue, costs and profit from ${rows[0].label} to ${last.label}. This month: revenue ${idr(last.revenue_idr)}, costs ${idr(last.cost_idr)}, profit ${idr(last.profit_idr)}. Highest profit ${idr(best.profit_idr)} in ${best.label}. Switch to Table view for full numbers.`;
}

function LegendItem({ color, label, line }: { color: string; label: string; line?: boolean }) {
  return (
    <li className="inline-flex items-center gap-1.5">
      {line ? (
        <span className="relative inline-block h-2 w-4" aria-hidden="true">
          <span className="absolute inset-x-0 top-1/2 h-0.5 -translate-y-1/2 rounded-full" style={{ background: color }} />
          <span className="absolute left-1/2 top-1/2 h-2 w-2 -translate-x-1/2 -translate-y-1/2 rounded-full" style={{ background: color }} />
        </span>
      ) : (
        <span className="h-2 w-2 rounded-[2px]" style={{ background: color }} aria-hidden="true" />
      )}
      {label}
    </li>
  );
}

function FinanceTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  const row = payload[0]?.payload as Row | undefined;
  if (!row) return null;
  const items = [
    { label: "Revenue", color: C_REV, value: idr(row.revenue_idr) },
    { label: "Costs", color: C_COST, value: idr(row.cost_idr) },
    { label: "Profit", color: C_PROFIT, value: signedIdr(row.profit_idr) },
  ];
  return (
    <div className={cn("rounded-lg border border-line bg-surface px-3 py-2 text-[12.5px] shadow-[var(--shadow-pop)]", VIZ_VARS)}>
      <p className="mb-1 font-medium text-fg">{label}</p>
      {items.map((it) => (
        <p key={it.label} className="flex items-center gap-2 text-fg-muted">
          <span className="h-2 w-2 shrink-0 rounded-[2px]" style={{ background: it.color }} aria-hidden="true" />
          {it.label}
          <span className="ml-auto pl-4 font-medium tabular-nums text-fg">{it.value}</span>
        </p>
      ))}
      <p className="mt-1 border-t border-line pt-1 text-[11.5px] tabular-nums text-fg-faint">
        Orders: {count(row.orders)}{row.refund_idr > 0 ? ` · refund ${idr(row.refund_idr)}` : ""}
      </p>
    </div>
  );
}

function MonthlyTable({ rows }: { rows: Row[] }) {
  return (
    <div className="overflow-x-auto" role="region" aria-label="Monthly revenue and costs table" tabIndex={0}>
      <table className="w-full min-w-[640px] text-[13px]">
        <thead>
          <tr className={THEAD}>
            <th scope="col" className={TH}>Month</th>
            <th scope="col" className={cn(TH, "text-right")}>Revenue</th>
            <th scope="col" className={cn(TH, "text-right")}>Costs</th>
            <th scope="col" className={cn(TH, "text-right")}>Profit</th>
            <th scope="col" className={cn(TH, "text-right")}>Orders</th>
            <th scope="col" className={cn(TH, "text-right")}>Refund</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {[...rows].reverse().map((r) => (
            <tr key={r.month} className="tabular-nums">
              <th scope="row" className="whitespace-nowrap px-4 py-2 text-left font-normal text-fg">{r.label}</th>
              <td className="px-4 py-2 text-right">{idr(r.revenue_idr)}</td>
              <td className="px-4 py-2 text-right">{idr(r.cost_idr)}</td>
              <td className={cn("px-4 py-2 text-right font-medium", r.profit_idr < 0 ? "text-bad" : r.profit_idr > 0 ? "text-ok" : "text-fg")}>{signedIdr(r.profit_idr)}</td>
              <td className="px-4 py-2 text-right text-fg-muted">{count(r.orders)}</td>
              <td className="px-4 py-2 text-right text-fg-muted">{r.refund_idr > 0 ? idr(r.refund_idr) : "-"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ─── Refunds ────────────────────────────────────────────────────────────────

function RefundWarning({ refunds }: { refunds: NonNullable<HostingFinance["refunds_needed"]> }) {
  if (refunds.length === 0) return null;
  const total = refunds.reduce((s, r) => s + r.amount_idr, 0);
  return (
    <section role="alert" className="rounded-2xl border border-warn/30 bg-warn/10 px-4 py-3">
      <div className="flex items-start gap-2.5">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <h2 className="text-[13px] font-semibold text-fg">
            Refunds needed: {count(refunds.length)} ({idr(total)})
          </h2>
          <p className="mt-0.5 text-[12.5px] text-fg-muted">These orders were paid, but their instance was deleted before use. Refund them through SumoPod.</p>
          <ul className="mt-2 divide-y divide-warn/20 text-[12.5px]">
            {refunds.map((r) => (
              <li key={r.order_id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 py-1.5">
                <span className="min-w-0 break-all font-mono text-[12px] text-fg">{r.order_id}</span>
                <span className="flex flex-wrap items-baseline gap-x-3 tabular-nums text-fg-muted">
                  <span>Paid {r.paid_at ? dateID(r.paid_at, true) : "-"}</span>
                  {r.instance_id && <span className="font-mono text-[11.5px]">instance {r.instance_id}</span>}
                  <span className="font-medium text-fg">{idr(r.amount_idr)}</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
}

// ─── Operating costs ────────────────────────────────────────────────────────

function CostsPanel({ costs }: { costs: HostingCost[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<HostingCost | "new" | null>(null);

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteHostingCost(id),
    onSuccess: () => {
      toast.success("Cost deleted");
      qc.invalidateQueries({ queryKey: QK_HOSTING.finance });
    },
    onError: (e) => toast.error("Couldn't delete cost", errorText(e)),
  });

  const onDelete = async (c: HostingCost) => {
    const ok = await confirm({
      title: "Delete cost?",
      description: (
        <>
          <span className="font-medium text-fg">{c.name}</span> is removed from every month, so profit for past months changes too. If this cost
          has ended, set an end date instead.
        </>
      ),
      confirmLabel: "Delete cost",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (ok) remove.mutate(c.id);
  };

  const monthTotal = costs.reduce((s, c) => s + c.this_month_idr, 0);

  return (
    <Panel
      icon={Coins}
      title="Operating costs"
      subtitle={costs.length ? `This month: ${idr(monthTotal)} across ${count(costs.length)} costs.` : "VPS rental or purchase, domains and other hosting costs."}
      action={
        <Button onClick={() => setEditing("new")}>
          <Plus aria-hidden="true" />
          Add cost
        </Button>
      }
    >
      {costs.length === 0 ? (
        <EmptyState
          icon={Coins}
          size="sm"
          title="No costs yet"
          hint="Add monthly VPS rent or one-time purchases so profit and break-even can be calculated."
        />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Operating costs table" tabIndex={0}>
          <table className="w-full min-w-[860px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Name</th>
                <th scope="col" className={TH}>Category</th>
                <th scope="col" className={cn(TH, "text-right")}>Amount</th>
                <th scope="col" className={TH}>Period</th>
                <th scope="col" className={cn(TH, "text-right")}>This month</th>
                <th scope="col" className={TH}>Note</th>
                <th scope="col" className={cn(TH, "text-right")}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {costs.map((c) => (
                <tr key={c.id} className="align-top transition-colors hover:bg-hover">
                  <td className="px-4 py-2.5 font-medium text-fg">{c.name}</td>
                  <td className="px-4 py-2.5">
                    <Badge tone={CATEGORY_TONE[c.category] ?? "neutral"}>{CATEGORY_LABEL[c.category] ?? c.category}</Badge>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                    {idr(c.amount_idr)}
                    <p className="text-[11.5px] text-fg-muted">{c.period === "monthly" ? "per month" : "one-time"}</p>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">
                    {c.period === "one_time" ? plainDateID(c.start_date) : `${plainDateID(c.start_date)} – ${c.end_date ? plainDateID(c.end_date) : "ongoing"}`}
                  </td>
                  <td className={cn("whitespace-nowrap px-4 py-2.5 text-right tabular-nums", c.this_month_idr > 0 ? "text-fg" : "text-fg-faint")}>
                    {idr(c.this_month_idr)}
                  </td>
                  <td className="max-w-[240px] break-words px-4 py-2.5 text-[12.5px] text-fg-muted">{c.note || "-"}</td>
                  <td className="px-4 py-2.5">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" onClick={() => setEditing(c)} title="Edit cost" aria-label={`Edit ${c.name}`}>
                        <Pencil aria-hidden="true" />
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => onDelete(c)}
                        disabled={remove.isPending && remove.variables === c.id}
                        title="Delete cost"
                        aria-label={`Delete ${c.name}`}
                      >
                        {remove.isPending && remove.variables === c.id ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && <CostModal cost={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

function CostModal({ cost, onClose }: { cost: HostingCost | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState(cost?.name ?? "");
  const [category, setCategory] = useState<HostingCostCategory>(cost?.category ?? "vps");
  const [amount, setAmount] = useState(cost ? String(cost.amount_idr) : "");
  const [period, setPeriod] = useState<HostingCostPeriod>(cost?.period ?? "monthly");
  const [start, setStart] = useState(cost?.start_date ?? todayISO());
  const [end, setEnd] = useState(cost?.end_date ?? "");
  const [note, setNote] = useState(cost?.note ?? "");
  const [touched, setTouched] = useState(false);

  const amountNum = Number(amount);
  const nameError = touched && !name.trim() ? "Enter a cost name." : null;
  const amountError = touched && (amount.trim() === "" || !Number.isInteger(amountNum) || amountNum <= 0) ? "Enter a whole number above 0." : null;
  const startError = touched && !start ? "Enter a start date." : null;
  const endError = period === "monthly" && end && start && end < start ? "End date can't be before the start date." : null;

  const save = useMutation({
    mutationFn: (body: HostingCostInput) => (cost ? api.updateHostingCost(cost.id, body) : api.createHostingCost(body)),
    onSuccess: () => {
      toast.success(cost ? "Cost updated" : "Cost added");
      qc.invalidateQueries({ queryKey: QK_HOSTING.finance });
      onClose();
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    const valid = name.trim() && Number.isInteger(amountNum) && amountNum > 0 && start && !endError;
    if (!valid || save.isPending) return;
    save.mutate({
      name: name.trim(),
      category,
      amount_idr: amountNum,
      period,
      start_date: start,
      ...(period === "monthly" && end ? { end_date: end } : {}),
      note: note.trim(),
    });
  };

  return (
    <Modal open onClose={save.isPending ? () => {} : onClose} title={cost ? "Edit cost" : "Add cost"} subtitle="Hosting operating cost">
      <form onSubmit={onSubmit} noValidate>
        <div className="space-y-4 px-5 py-4">
          <Field label="Name" error={nameError} required>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="VPS Contabo 8 GB" autoFocus />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Category">
              <Select value={category} onChange={(e) => setCategory(e.target.value as HostingCostCategory)}>
                {(Object.keys(CATEGORY_LABEL) as HostingCostCategory[]).map((k) => (
                  <option key={k} value={k}>
                    {CATEGORY_LABEL[k]}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Billing" hint={period === "monthly" ? "Counted in full every month while active." : "Counted once, in the start date's month."}>
              <Select value={period} onChange={(e) => setPeriod(e.target.value as HostingCostPeriod)}>
                <option value="monthly">Monthly</option>
                <option value="one_time">One-time</option>
              </Select>
            </Field>
          </div>
          <Field label="Amount (IDR)" error={amountError} hint={!amountError && amountNum > 0 ? idr(amountNum) : undefined} required>
            <Input type="number" inputMode="numeric" min="1" step="1" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="250000" className="tabular-nums" />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Start date" error={startError} required>
              <Input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
            </Field>
            {period === "monthly" && (
              <Field label="End date" optional hint={endError ? undefined : "Leave empty while ongoing."} error={endError}>
                <Input type="date" value={end} min={start || undefined} onChange={(e) => setEnd(e.target.value)} />
              </Field>
            )}
          </div>
          <Field label="Note" optional>
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} placeholder="e.g. customer server, paid by credit card" />
          </Field>
          {save.isError && <ErrorBanner message={`Couldn't save cost. ${errorText(save.error)}`} />}
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
            {cost ? "Save" : "Add"}
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
