import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type FinanceRange } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { SegmentedControl } from "../components/ui";
import { FinanceOverview, RANGE_LABEL } from "./payments/FinanceOverview";
import { UpstreamAccounts } from "./payments/UpstreamAccounts";
import { TopupHistory } from "./payments/TopupHistory";
import { FinanceSettings } from "./payments/FinanceSettings";
import { PortalOrders } from "./payments/PortalOrders";
import { QK, REFETCH_MS, errorText } from "./payments/shared";

const RANGES = (Object.keys(RANGE_LABEL) as FinanceRange[]).map((value) => ({ value, label: RANGE_LABEL[value] }));
const RANGE_STORAGE_KEY = "kr.payments.range";

function initialRange(): FinanceRange {
  try {
    const saved = localStorage.getItem(RANGE_STORAGE_KEY);
    if (RANGES.some((r) => r.value === saved)) return saved as FinanceRange;
  } catch {
    // storage unavailable: use the default
  }
  return "30d";
}

export function PaymentsPage() {
  const [range, setRangeState] = useState<FinanceRange>(initialRange);
  const setRange = (r: FinanceRange) => {
    setRangeState(r);
    try {
      localStorage.setItem(RANGE_STORAGE_KEY, r);
    } catch {
      // ignore
    }
  };

  const finance = useQuery({
    queryKey: [...QK.finance, range],
    queryFn: () => api.financeReport(range),
    refetchInterval: REFETCH_MS,
  });
  const accounts = useQuery({ queryKey: QK.accounts, queryFn: api.upstreamAccounts, refetchInterval: REFETCH_MS });

  const financeError = finance.isError ? errorText(finance.error) : null;
  const accountsError = accounts.isError ? errorText(accounts.error) : null;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Payments"
        description="Credit masuk, modal, keuntungan, dan saldo provider upstream. Diperbarui tiap 15 detik."
        action={<SegmentedControl value={range} onChange={setRange} options={RANGES} label="Rentang waktu" />}
      />

      <FinanceOverview overview={finance.data?.overview} range={range} loading={finance.isLoading} error={financeError} />

      <UpstreamAccounts report={finance.data} loading={finance.isLoading} error={financeError} rangeLabel={RANGE_LABEL[range]} />

      <TopupHistory accounts={accounts.data?.accounts} accountsLoading={accounts.isLoading} accountsError={accountsError} />

      <PortalOrders />

      <FinanceSettings accounts={accounts.data?.accounts} accountsLoading={accounts.isLoading} accountsError={accountsError} />
    </div>
  );
}
