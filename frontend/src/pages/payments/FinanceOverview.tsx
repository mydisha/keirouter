import { Activity, Coins, Percent, PiggyBank, Receipt, Wallet, Wrench } from "lucide-react";
import type { FinanceOverview as Overview, FinanceRange } from "../../lib/api";
import { ErrorBanner, Kpi, KpiGrid, LoadingRegion, Skeleton } from "../../components/ui";
import { count, idr, usd } from "./shared";

export const RANGE_LABEL: Record<FinanceRange, string> = {
  today: "Hari ini",
  "7d": "7 hari",
  "30d": "30 hari",
  all: "Semua",
};

export function FinanceOverview({
  overview,
  range,
  loading,
  error,
}: {
  overview?: Overview;
  range: FinanceRange;
  loading: boolean;
  error: string | null;
}) {
  if (loading && !overview) {
    return (
      <LoadingRegion label="Memuat ringkasan keuangan">
        <Skeleton className="h-[188px] w-full rounded-2xl" />
      </LoadingRegion>
    );
  }
  if (!overview) {
    return <ErrorBanner message={`Ringkasan keuangan gagal dimuat. ${error ?? ""}`.trim()} />;
  }

  const o = overview;
  const cost = o.cost_exact_usd + o.cost_estimated_usd;
  const rangeText = RANGE_LABEL[range].toLowerCase();

  return (
    <div className="space-y-2">
      {error && <ErrorBanner message={`Data terakhir gagal diperbarui, angka di bawah mungkin sudah lama. ${error}`} />}
      <KpiGrid cols={4} label={`Credit dan modal, ${rangeText}`}>
        <Kpi
          icon={Wallet}
          label="Credit masuk"
          value={idr(o.credit_in_idr)}
          hint={`${usd(o.credit_in_usd)} dari ${count(o.paid_orders)} order`}
        />
        <Kpi icon={Coins} label="Credit user tersisa" value={usd(o.outstanding_credit_usd)} hint="Saldo semua user saat ini" />
        <Kpi
          icon={Receipt}
          label="Pendapatan"
          value={usd(o.revenue_usd)}
          hint={`Markup dari tagihan ${usd(o.billed_usd)} · ${count(o.requests)} request`}
        />
        <Kpi
          icon={Wrench}
          label="Modal"
          value={usd(cost)}
          hint={o.cost_estimated_usd > 0 ? `Harga upstream, termasuk estimasi ${usd(o.cost_estimated_usd)}` : "Harga upstream sebelum markup"}
        />
      </KpiGrid>
      <KpiGrid cols={3} label={`Keuntungan dan request, ${rangeText}`}>
        <Kpi
          icon={PiggyBank}
          label="Keuntungan"
          value={usd(o.profit_usd)}
          tone={o.profit_usd < 0 ? "bad" : "section"}
          valueClassName={o.profit_usd < 0 ? "text-bad" : ""}
          hint="Tagihan user dikurangi modal"
        />
        <Kpi
          icon={Percent}
          label="Margin"
          value={`${o.margin_pct.toLocaleString("id-ID", { maximumFractionDigits: 1 })}%`}
          hint="Keuntungan dibagi modal"
        />
        <Kpi icon={Activity} label="Request aktif" value={count(o.in_flight)} hint="Sedang berjalan saat ini" />
      </KpiGrid>
      {o.cost_estimated_usd > 0 && (
        <p className="text-[12px] text-fg-muted">
          Sebagian modal ({usd(o.cost_estimated_usd)}) dihitung dari tarif jual dikurangi markup karena harga upstream tidak tercatat. Pendapatan, keuntungan,
          dan margin memakai angka ini.
        </p>
      )}
    </div>
  );
}
