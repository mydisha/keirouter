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
  other: "Lainnya",
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
      <LoadingRegion label="Memuat keuangan hosting" className="space-y-4">
        <Skeleton className="h-[188px] w-full rounded-2xl" />
        <Skeleton className="h-[340px] w-full rounded-2xl" />
      </LoadingRegion>
    );
  }
  if (!f) {
    return <ErrorBanner message={`Keuangan hosting gagal dimuat. ${q.isError ? errorText(q.error) : ""}`.trim()} />;
  }

  return (
    <div className="space-y-6">
      {q.isError && <ErrorBanner message={`Data terakhir gagal diperbarui, angka di bawah mungkin sudah lama. ${errorText(q.error)}`} />}
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
      <KpiGrid cols={4} label={`Keuangan hosting ${month}`}>
        <Kpi icon={Wallet} label={`Pendapatan ${month}`} value={idr(c.revenue_idr)} hint="Kotor, sebelum biaya SumoPod" />
        <Kpi icon={Receipt} label="Biaya" value={idr(c.cost_idr)} hint="Biaya operasional bulan ini" />
        <Kpi
          icon={PiggyBank}
          label="Laba"
          value={signedIdr(c.profit_idr)}
          tone={c.profit_idr < 0 ? "bad" : c.profit_idr > 0 ? "ok" : "section"}
          valueClassName={c.profit_idr < 0 ? "text-bad" : c.profit_idr > 0 ? "text-ok" : ""}
          hint={c.profit_idr < 0 ? "Rugi bulan ini" : c.profit_idr > 0 ? "Untung bulan ini" : "Impas"}
        />
        <Kpi
          icon={Repeat}
          label="MRR"
          value={idr(c.mrr_idr)}
          hint={c.price_idr > 0 ? `${count(c.running_instances)} instance, rata-rata ${idr(c.price_idr)}` : "Harga belum diatur"}
        />
      </KpiGrid>
      <KpiGrid cols={3} label="Kapasitas dan titik impas">
        <Kpi icon={Server} label="Instance berjalan" value={count(c.running_instances)} hint="Aktif dan kedaluwarsa yang belum dihentikan" />
        <Kpi
          icon={Calculator}
          label="Biaya per instance"
          value={c.running_instances > 0 ? idr(c.cost_per_instance_idr) : "-"}
          hint={c.running_instances > 0 ? "Biaya bulan ini ÷ instance berjalan" : "Belum ada instance berjalan"}
        />
        <Kpi
          icon={Scale}
          label="Titik impas"
          value={c.price_idr > 0 ? `${count(c.break_even_instances)} instance` : "-"}
          tone={c.price_idr > 0 && c.cost_idr > 0 ? (gap <= 0 ? "ok" : "warn") : "section"}
          hint={
            c.price_idr <= 0
              ? "Isi harga di tab Pengaturan"
              : c.cost_idr <= 0
                ? "Belum ada biaya bulan ini"
                : gap <= 0
                  ? "Sudah balik modal bulan ini"
                  : `Kurang ${count(gap)} instance untuk balik modal`
          }
        />
      </KpiGrid>
      <p className="text-[12px] text-fg-muted">
        Pendapatan dihitung dari nilai kotor order yang lunas. Biaya SumoPod belum dipotong, jadi laba sebenarnya sedikit lebih kecil.
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
      title="Pendapatan vs biaya"
      subtitle={
        <span className="tabular-nums">
          {range} bulan terakhir · pendapatan {idr(t.revenue_idr)} · biaya {idr(t.cost_idr)} · laba{" "}
          <span className={cn("font-medium", t.profit_idr < 0 ? "text-bad" : t.profit_idr > 0 ? "text-ok" : "text-fg")}>{signedIdr(t.profit_idr)}</span>
        </span>
      }
      action={
        <div className="flex flex-wrap items-center gap-2">
          {fetching && <Loader2 className="h-4 w-4 animate-spin text-fg-faint" aria-label="Memuat" />}
          <SegmentedControl<Range>
            label="Rentang bulan"
            value={range}
            onChange={onRange}
            options={[
              { value: "6", label: "6 bulan" },
              { value: "12", label: "12 bulan" },
              { value: "24", label: "24 bulan" },
            ]}
          />
          <SegmentedControl<"chart" | "table">
            label="Tampilan"
            value={view}
            onChange={setView}
            options={[
              { value: "chart", label: "Grafik" },
              { value: "table", label: "Tabel" },
            ]}
          />
        </div>
      }
    >
      {!hasData ? (
        <EmptyState
          icon={TrendingUp}
          size="sm"
          title="Belum ada pendapatan atau biaya"
          hint="Grafik terisi begitu ada order hosting yang lunas atau biaya operasional dicatat di bawah."
        />
      ) : view === "chart" ? (
        <div className={cn("px-4 pb-3 pt-4", VIZ_VARS)}>
          <ul className="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-fg-muted" aria-label="Keterangan grafik">
            <LegendItem color={C_REV} label="Pendapatan" />
            <LegendItem color={C_COST} label="Biaya" />
            <LegendItem color={C_PROFIT} label="Laba" line />
          </ul>
          <div className="h-[280px] w-full" role="img" aria-label={chartSummary(rows)}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={rows} margin={{ top: 8, right: 8, left: 0, bottom: 0 }} barGap={2} barCategoryGap="24%">
                <CartesianGrid vertical={false} stroke="var(--border)" />
                <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={{ stroke: "var(--border-strong)" }} dy={6} minTickGap={16} interval="preserveStartEnd" />
                <YAxis tick={axisTick} tickLine={false} axisLine={false} tickFormatter={idrCompact} width={72} />
                {hasLoss && <ReferenceLine y={0} stroke="var(--border-strong)" />}
                <Tooltip cursor={{ fill: "var(--bg-subtle)", opacity: 0.7 }} content={<FinanceTooltip />} />
                <Bar dataKey="revenue_idr" name="Pendapatan" fill={C_REV} radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false} />
                <Bar dataKey="cost_idr" name="Biaya" fill={C_COST} radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false} />
                <Line
                  dataKey="profit_idr"
                  name="Laba"
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
  if (!rows.length) return "Grafik pendapatan, biaya, dan laba per bulan.";
  const last = rows[rows.length - 1];
  const best = rows.reduce((a, b) => (b.profit_idr > a.profit_idr ? b : a));
  return `Grafik pendapatan, biaya, dan laba per bulan dari ${rows[0].label} sampai ${last.label}. Bulan ini: pendapatan ${idr(last.revenue_idr)}, biaya ${idr(last.cost_idr)}, laba ${idr(last.profit_idr)}. Laba tertinggi ${idr(best.profit_idr)} di ${best.label}. Pilih tampilan Tabel untuk angka lengkap.`;
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
    { label: "Pendapatan", color: C_REV, value: idr(row.revenue_idr) },
    { label: "Biaya", color: C_COST, value: idr(row.cost_idr) },
    { label: "Laba", color: C_PROFIT, value: signedIdr(row.profit_idr) },
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
        {count(row.orders)} order{row.refund_idr > 0 ? ` · refund ${idr(row.refund_idr)}` : ""}
      </p>
    </div>
  );
}

function MonthlyTable({ rows }: { rows: Row[] }) {
  return (
    <div className="overflow-x-auto" role="region" aria-label="Tabel pendapatan dan biaya per bulan" tabIndex={0}>
      <table className="w-full min-w-[640px] text-[13px]">
        <thead>
          <tr className={THEAD}>
            <th scope="col" className={TH}>Bulan</th>
            <th scope="col" className={cn(TH, "text-right")}>Pendapatan</th>
            <th scope="col" className={cn(TH, "text-right")}>Biaya</th>
            <th scope="col" className={cn(TH, "text-right")}>Laba</th>
            <th scope="col" className={cn(TH, "text-right")}>Order</th>
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
            {count(refunds.length)} order perlu di-refund ({idr(total)})
          </h2>
          <p className="mt-0.5 text-[12.5px] text-fg-muted">Order ini sudah dibayar, tetapi instance-nya dihapus sebelum dipakai. Kembalikan dana lewat SumoPod.</p>
          <ul className="mt-2 divide-y divide-warn/20 text-[12.5px]">
            {refunds.map((r) => (
              <li key={r.order_id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 py-1.5">
                <span className="min-w-0 break-all font-mono text-[12px] text-fg">{r.order_id}</span>
                <span className="flex flex-wrap items-baseline gap-x-3 tabular-nums text-fg-muted">
                  <span>Dibayar {r.paid_at ? dateID(r.paid_at, true) : "-"}</span>
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
      toast.success("Biaya dihapus");
      qc.invalidateQueries({ queryKey: QK_HOSTING.finance });
    },
    onError: (e) => toast.error("Gagal menghapus biaya", errorText(e)),
  });

  const onDelete = async (c: HostingCost) => {
    const ok = await confirm({
      title: "Hapus biaya?",
      description: (
        <>
          <span className="font-medium text-fg">{c.name}</span> dihapus dari semua bulan, jadi laba bulan-bulan sebelumnya ikut berubah. Kalau biaya ini
          sudah berhenti, lebih baik isi tanggal selesai.
        </>
      ),
      confirmLabel: "Hapus biaya",
      cancelLabel: "Batal",
      tone: "danger",
    });
    if (ok) remove.mutate(c.id);
  };

  const monthTotal = costs.reduce((s, c) => s + c.this_month_idr, 0);

  return (
    <Panel
      icon={Coins}
      title="Biaya operasional"
      subtitle={costs.length ? `Bulan ini ${idr(monthTotal)} dari ${count(costs.length)} biaya.` : "Sewa atau pembelian VPS, domain, dan biaya lain untuk hosting."}
      action={
        <Button onClick={() => setEditing("new")}>
          <Plus aria-hidden="true" />
          Tambah biaya
        </Button>
      }
    >
      {costs.length === 0 ? (
        <EmptyState
          icon={Coins}
          size="sm"
          title="Belum ada biaya"
          hint="Catat sewa VPS bulanan atau pembelian sekali bayar agar laba dan titik impas bisa dihitung."
        />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Tabel biaya operasional" tabIndex={0}>
          <table className="w-full min-w-[860px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Nama</th>
                <th scope="col" className={TH}>Kategori</th>
                <th scope="col" className={cn(TH, "text-right")}>Jumlah</th>
                <th scope="col" className={TH}>Masa berlaku</th>
                <th scope="col" className={cn(TH, "text-right")}>Bulan ini</th>
                <th scope="col" className={TH}>Catatan</th>
                <th scope="col" className={cn(TH, "text-right")}>
                  <span className="sr-only">Aksi</span>
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
                    <p className="text-[11.5px] text-fg-muted">{c.period === "monthly" ? "per bulan" : "sekali"}</p>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">
                    {c.period === "one_time" ? plainDateID(c.start_date) : `${plainDateID(c.start_date)} – ${c.end_date ? plainDateID(c.end_date) : "berjalan"}`}
                  </td>
                  <td className={cn("whitespace-nowrap px-4 py-2.5 text-right tabular-nums", c.this_month_idr > 0 ? "text-fg" : "text-fg-faint")}>
                    {idr(c.this_month_idr)}
                  </td>
                  <td className="max-w-[240px] break-words px-4 py-2.5 text-[12.5px] text-fg-muted">{c.note || "-"}</td>
                  <td className="px-4 py-2.5">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" onClick={() => setEditing(c)} title="Ubah biaya" aria-label={`Ubah ${c.name}`}>
                        <Pencil aria-hidden="true" />
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => onDelete(c)}
                        disabled={remove.isPending && remove.variables === c.id}
                        title="Hapus biaya"
                        aria-label={`Hapus ${c.name}`}
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
  const nameError = touched && !name.trim() ? "Isi nama biaya." : null;
  const amountError = touched && (amount.trim() === "" || !Number.isInteger(amountNum) || amountNum <= 0) ? "Isi angka bulat lebih dari 0." : null;
  const startError = touched && !start ? "Isi tanggal mulai." : null;
  const endError = period === "monthly" && end && start && end < start ? "Tanggal selesai tidak boleh sebelum tanggal mulai." : null;

  const save = useMutation({
    mutationFn: (body: HostingCostInput) => (cost ? api.updateHostingCost(cost.id, body) : api.createHostingCost(body)),
    onSuccess: () => {
      toast.success(cost ? "Biaya diperbarui" : "Biaya ditambahkan");
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
    <Modal open onClose={save.isPending ? () => {} : onClose} title={cost ? "Ubah biaya" : "Tambah biaya"} subtitle="Biaya operasional hosting">
      <form onSubmit={onSubmit} noValidate>
        <div className="space-y-4 px-5 py-4">
          <Field label="Nama" error={nameError} required>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="VPS Contabo 8 GB" autoFocus />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Kategori">
              <Select value={category} onChange={(e) => setCategory(e.target.value as HostingCostCategory)}>
                {(Object.keys(CATEGORY_LABEL) as HostingCostCategory[]).map((k) => (
                  <option key={k} value={k}>
                    {CATEGORY_LABEL[k]}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Periode" hint={period === "monthly" ? "Dihitung penuh setiap bulan selama berlaku." : "Dihitung sekali di bulan tanggal mulai."}>
              <Select value={period} onChange={(e) => setPeriod(e.target.value as HostingCostPeriod)}>
                <option value="monthly">Bulanan</option>
                <option value="one_time">Sekali bayar</option>
              </Select>
            </Field>
          </div>
          <Field label="Jumlah (IDR)" error={amountError} hint={!amountError && amountNum > 0 ? idr(amountNum) : undefined} required>
            <Input type="number" inputMode="numeric" min="1" step="1" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="250000" className="tabular-nums" />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Tanggal mulai" error={startError} required>
              <Input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
            </Field>
            {period === "monthly" && (
              <Field label="Tanggal selesai" optional hint={endError ? undefined : "Kosongkan selama masih berjalan."} error={endError}>
                <Input type="date" value={end} min={start || undefined} onChange={(e) => setEnd(e.target.value)} />
              </Field>
            )}
          </div>
          <Field label="Catatan" optional>
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} placeholder="Misal: server pelanggan, bayar via kartu kredit" />
          </Field>
          {save.isError && <ErrorBanner message={`Biaya gagal disimpan. ${errorText(save.error)}`} />}
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose} disabled={save.isPending}>
            Batal
          </Button>
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
            {cost ? "Simpan" : "Tambah"}
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
