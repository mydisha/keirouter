import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { Network, ReceiptText, Server, Settings2 } from "lucide-react";
import { api, type FinanceRange } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { PageHeader } from "../components/Layout";
import { SegmentedControl, TabBar, TabPanel } from "../components/ui";
import { FinanceOverview, RANGE_LABEL } from "./payments/FinanceOverview";
import { UpstreamAccounts } from "./payments/UpstreamAccounts";
import { TopupHistory } from "./payments/TopupHistory";
import { FinanceSettings } from "./payments/FinanceSettings";
import { PortalOrders } from "./payments/PortalOrders";
import { FinanceTab as HostingFinance } from "./hosting/HostingFinance";
import { QK, REFETCH_MS, errorText } from "./payments/shared";

const RANGES = (Object.keys(RANGE_LABEL) as FinanceRange[]).map((value) => ({ value, label: RANGE_LABEL[value] }));
const RANGE_STORAGE_KEY = "kr.payments.range";

// /finance is the AI gateway tab; the others have their own URL so the
// sidebar, bookmarks and links can open them directly.
const TABS = ["gateway", "hosting", "orders", "settings"] as const;
type Tab = (typeof TABS)[number];
const TABS_ID = "finance-tabs";

const DESCRIPTION: Record<Tab, string> = {
  gateway: "AI gateway money: credit sold, cost basis, profit and upstream provider balances.",
  hosting: "Hosting money: revenue from paid orders against what the servers cost.",
  orders: "Every portal payment, for credit top-ups and hosting. Approve one here if the gateway missed it.",
  settings: "Opening balances, alert thresholds and how providers map to upstream accounts.",
};

function initialRange(): FinanceRange {
  try {
    const saved = localStorage.getItem(RANGE_STORAGE_KEY);
    if (RANGES.some((r) => r.value === saved)) return saved as FinanceRange;
  } catch {
    // storage unavailable: use the default
  }
  return "30d";
}

export function FinancePage() {
  const params = useParams();
  const tab = (params.tab ?? "gateway") as Tab;
  const navigate = useNavigate();
  const [range, setRangeState] = useState<FinanceRange>(initialRange);
  const setRange = (r: FinanceRange) => {
    setRangeState(r);
    try {
      localStorage.setItem(RANGE_STORAGE_KEY, r);
    } catch {
      // ignore
    }
  };

  const gatewayData = tab === "gateway";
  const accountsData = tab === "gateway" || tab === "settings";
  const finance = useQuery({
    queryKey: [...QK.finance, range],
    queryFn: () => api.financeReport(range),
    refetchInterval: REFETCH_MS,
    enabled: gatewayData,
  });
  const accounts = useQuery({ queryKey: QK.accounts, queryFn: api.upstreamAccounts, refetchInterval: REFETCH_MS, enabled: accountsData });

  if (params.tab === "gateway" || !TABS.includes(tab)) return <Navigate to={dashboard("/finance")} replace />;

  const financeError = finance.isError ? errorText(finance.error) : null;
  const accountsError = accounts.isError ? errorText(accounts.error) : null;
  const go = (t: Tab) => navigate(dashboard(t === "gateway" ? "/finance" : `/finance/${t}`));

  return (
    <div className="space-y-6">
      <PageHeader
        title="Finance"
        description={DESCRIPTION[tab]}
        action={tab === "gateway" ? <SegmentedControl value={range} onChange={setRange} options={RANGES} label="Time range" /> : undefined}
      />
      <TabBar
        idPrefix={TABS_ID}
        label="Finance sections"
        active={tab}
        onChange={go}
        tabs={[
          { value: "gateway", label: "AI gateway", icon: Network },
          { value: "hosting", label: "Hosting", icon: Server },
          { value: "orders", label: "Orders", icon: ReceiptText },
          { value: "settings", label: "Settings", icon: Settings2 },
        ]}
      />
      <TabPanel idPrefix={TABS_ID} value={tab} className="space-y-6">
        {tab === "gateway" ? (
          <>
            <FinanceOverview overview={finance.data?.overview} range={range} loading={finance.isLoading} error={financeError} />
            <UpstreamAccounts report={finance.data} loading={finance.isLoading} error={financeError} rangeLabel={RANGE_LABEL[range]} />
            <TopupHistory accounts={accounts.data?.accounts} accountsLoading={accounts.isLoading} accountsError={accountsError} />
          </>
        ) : tab === "hosting" ? (
          <HostingFinance />
        ) : tab === "orders" ? (
          <PortalOrders />
        ) : (
          <FinanceSettings accounts={accounts.data?.accounts} accountsLoading={accounts.isLoading} accountsError={accountsError} />
        )}
      </TabPanel>
    </div>
  );
}
