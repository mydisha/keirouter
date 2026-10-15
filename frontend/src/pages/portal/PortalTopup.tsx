import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { CheckCircle2, CreditCard, History, Receipt, Wallet, XCircle } from "lucide-react";
import {
  createTopupOrder,
  fetchPortalPaymentConfig,
  fetchPortalStatus,
  fetchPortalTopupOrders,
  fetchPortalTopups,
  type PaymentOrder,
} from "../../lib/api";
import { Badge, Button, EmptyState, ErrorCard, Input, Kpi, KpiGrid, Spinner } from "../../components/ui";
import { TurnstileWidget, useTurnstileSiteKey } from "../../components/TurnstileWidget";
import { useToast } from "../../components/Toast";
import { formatSpendUSD, formatUSD } from "../../lib/format";
import { portal } from "../../lib/portalRoutes";
import { PageHeader, PanelCard, TD, TH, THEAD, formatDateTime } from "./components";

function formatIDR(n: number): string {
  return new Intl.NumberFormat("id-ID", {
    style: "currency",
    currency: "IDR",
    maximumFractionDigits: 0,
  }).format(n);
}

const ORDER_STATUS_TONE: Record<PaymentOrder["status"], "success" | "warning" | "danger" | "neutral"> = {
  completed: "success",
  manual: "success",
  pending: "warning",
  failed: "danger",
  expired: "neutral",
};

export function PortalTopupPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [params] = useSearchParams();
  const returnStatus = params.get("status");

  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const topup = useQuery({
    queryKey: ["portal-topups"],
    queryFn: fetchPortalTopups,
    retry: false,
    enabled: !!status?.has_key,
  });

  const cfg = useQuery({
    queryKey: ["portal-payment-config"],
    queryFn: fetchPortalPaymentConfig,
    retry: false,
  });

  const orders = useQuery({
    queryKey: ["portal-topup-orders"],
    queryFn: fetchPortalTopupOrders,
    retry: false,
    enabled: !!status?.has_key && cfg.data?.enabled === true,
  });

  const [packageId, setPackageId] = useState<string | null>(null);
  const [amount, setAmount] = useState("");
  const siteKey = useTurnstileSiteKey();
  const [turnstileToken, setTurnstileToken] = useState("");
  const [resetSignal, setResetSignal] = useState(0);

  // On return from the gateway, refresh the ledger/balance and orders so the
  // just-completed payment is reflected without a manual reload.
  useEffect(() => {
    if (returnStatus === "success" || returnStatus === "cancel") {
      qc.invalidateQueries({ queryKey: ["portal-topups"] });
      qc.invalidateQueries({ queryKey: ["portal-topup-orders"] });
    }
  }, [returnStatus, qc]);

  const buyMutation = useMutation({
    mutationFn: (input: { amount_idr?: number; package_id?: string }) =>
      createTopupOrder({
        ...input,
        idempotency_key: crypto.randomUUID(),
        turnstile_token: turnstileToken || undefined,
      }),
    onSuccess: (order) => {
      const link = order.payment_link_url;
      let safe = false;
      if (link) {
        try {
          const proto = new URL(link).protocol;
          safe = proto === "http:" || proto === "https:";
        } catch {
          safe = false;
        }
      }
      if (safe && link) {
        window.location.href = link;
      } else {
        toast.error("Invalid payment link");
      }
    },
    onError: (e: Error) => {
      toast.error("Payment failed", e.message);
      setTurnstileToken("");
      setResetSignal((n) => n + 1);
    },
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Failed to load portal status" />;

  if (status.has_key === false) {
    return (
      <div className="space-y-6">
        <PageHeader icon={Wallet} title="Topup" />
        <EmptyState
          variant="card"
          icon={Wallet}
          title="No API key yet"
          hint="Create an API key to see your top-up history."
          action={<Link to={portal("/key")} className="text-[13px] font-medium text-link hover:underline">Create an API key</Link>}
        />
      </div>
    );
  }

  if (topup.isLoading) return <Spinner />;
  if (topup.isError) {
    const msg = topup.error instanceof Error ? topup.error.message : "Failed to load top-ups";
    return <ErrorCard message={msg} />;
  }

  const data = topup.data;
  if (!data) return <Spinner />;

  const topups = data.topups ?? [];
  const balance = data.balance;
  const config = cfg.data;
  const showBuy = !!config?.enabled;

  const packages = config?.packages ?? [];
  const minIdr = config?.min_topup_idr ?? 0;
  const maxIdr = config?.max_topup_idr ?? 0;
  const fxRate = config?.fx_rate ?? 0;

  const amountIdr = packageId
    ? packages.find((p) => p.id === packageId)?.amount_idr ?? 0
    : Number(amount) || 0;
  const creditUsd = fxRate > 0 ? amountIdr / fxRate : 0;
  const amountValid = amountIdr >= minIdr && amountIdr > 0 && (maxIdr <= 0 || amountIdr <= maxIdr);

  const submit = () => {
    if (!amountValid || !fxRate) return;
    buyMutation.mutate(packageId ? { package_id: packageId } : { amount_idr: amountIdr });
  };

  const orderList = orders.data?.orders ?? [];

  return (
    <div className="space-y-6">
      <PageHeader icon={Wallet} title="Topup" subtitle="Your balance, credit purchases and top-up history." />

      {returnStatus === "success" && (
        <div role="status" className="flex items-start gap-3 rounded-2xl border border-ok/30 bg-ok/10 px-4 py-3">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-ok" strokeWidth={1.75} aria-hidden="true" />
          <p className="text-[13px] text-fg">
            Payment received. Your credit will appear here as soon as it is confirmed.
          </p>
        </div>
      )}
      {returnStatus === "cancel" && (
        <div role="status" className="flex items-start gap-3 rounded-2xl border border-line bg-subtle px-4 py-3">
          <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-fg-muted" strokeWidth={1.75} aria-hidden="true" />
          <p className="text-[13px] text-fg-muted">Payment cancelled — no charge was made.</p>
        </div>
      )}

      {balance ? (
        <KpiGrid cols={3} label="Balance">
          <Kpi icon={Wallet} tone="slate" label="Budget limit" value={formatSpendUSD(balance.limit_usd)} />
          <Kpi icon={Receipt} tone="orange" label="Spent" value={formatSpendUSD(balance.spent_usd)} />
          <Kpi icon={CreditCard} tone="teal" label="Remaining" value={formatSpendUSD(balance.usd_remaining)} />
        </KpiGrid>
      ) : (
        <PanelCard icon={Wallet} title="Balance">
          <p className="px-4 py-3 text-[13px] text-fg-muted sm:px-5">No budget limit configured.</p>
        </PanelCard>
      )}

      {showBuy && (
        <PanelCard icon={CreditCard} title="Buy credit">
          <div className="grid grid-cols-1 lg:grid-cols-[1fr_340px]">
            <div className="space-y-5 p-4 sm:p-5">
              {packages.length > 0 && (
                <fieldset className="space-y-2">
                  <legend className="mb-2 text-[12.5px] font-medium text-fg">Packages</legend>
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                    {packages.map((p) => {
                      const selected = packageId === p.id;
                      return (
                        <button
                          key={p.id}
                          type="button"
                          aria-pressed={selected}
                          onClick={() => {
                            setPackageId(p.id);
                            setAmount("");
                          }}
                          className={`flex flex-col items-start rounded-lg border px-3 py-2.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 ${
                            selected
                              ? "border-accent-500 bg-accent-500/10 ring-1 ring-inset ring-accent-500"
                              : "border-line bg-surface hover:border-line-strong hover:bg-hover"
                          }`}
                        >
                          <span className="text-[13px] font-semibold text-fg">{p.label}</span>
                          <span className="text-[12px] tabular-nums text-fg-muted">{formatIDR(p.amount_idr)}</span>
                        </button>
                      );
                    })}
                  </div>
                </fieldset>
              )}

              <label className="block space-y-1.5">
                <span className="block text-[12.5px] font-medium text-fg">Custom amount (IDR)</span>
                <Input
                  type="number"
                  inputMode="numeric"
                  min={minIdr || undefined}
                  max={maxIdr || undefined}
                  placeholder={minIdr ? `Min ${minIdr.toLocaleString("id-ID")}` : "Amount in IDR"}
                  value={amount}
                  onChange={(e) => {
                    setPackageId(null);
                    setAmount(e.target.value);
                  }}
                  className="tabular-nums"
                />
                {(minIdr > 0 || maxIdr > 0) && (
                  <span className="block text-[12px] text-fg-muted">
                    {minIdr > 0 && `Min ${formatIDR(minIdr)}`}
                    {minIdr > 0 && maxIdr > 0 && " · "}
                    {maxIdr > 0 && `Max ${formatIDR(maxIdr)}`}
                  </span>
                )}
              </label>
            </div>

            <div className="flex flex-col border-t border-line bg-subtle p-4 sm:p-5 lg:border-l lg:border-t-0">
              <dl className="space-y-3">
                <div className="flex items-baseline justify-between gap-3">
                  <dt className="text-[13px] text-fg-muted">You pay</dt>
                  <dd className="text-[18px] font-semibold tabular-nums text-fg">{amountIdr > 0 ? formatIDR(amountIdr) : "—"}</dd>
                </div>
                <div className="flex items-baseline justify-between gap-3">
                  <dt className="text-[13px] text-fg-muted">Estimated credit</dt>
                  <dd className="text-[18px] font-semibold tabular-nums text-link">{fxRate > 0 && amountIdr > 0 ? formatUSD(creditUsd) : "—"}</dd>
                </div>
                {config?.currency_source && (
                  <p className="text-[12px] text-fg-faint">Rate source: {config.currency_source}</p>
                )}
              </dl>
              <TurnstileWidget
                siteKey={siteKey}
                onToken={setTurnstileToken}
                resetSignal={resetSignal}
                className="mt-4 flex justify-center"
              />
              <Button
                className="mt-4 w-full"
                onClick={submit}
                disabled={!amountValid || !fxRate || buyMutation.isPending || (!!siteKey && !turnstileToken)}
              >
                <CreditCard aria-hidden="true" />
                {buyMutation.isPending ? "Starting payment…" : "Buy credit"}
              </Button>
            </div>
          </div>
        </PanelCard>
      )}

      {showBuy && (
        <PanelCard icon={Receipt} title="Payment orders" count={orderList.length}>
          {orders.isError ? (
            <EmptyState title="Could not load payment orders" />
          ) : orderList.length === 0 ? (
            <EmptyState title="No payment orders yet" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[520px] text-[13px]">
                <thead className={THEAD}>
                  <tr>
                    <th scope="col" className={`${TH} text-left`}>Date</th>
                    <th scope="col" className={`${TH} text-right`}>Amount</th>
                    <th scope="col" className={`${TH} text-right`}>Credit</th>
                    <th scope="col" className={`${TH} text-left`}>Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {orderList.map((o) => (
                    <tr key={o.order_id} className="transition-colors hover:bg-hover">
                      <td className={`${TD} whitespace-nowrap tabular-nums text-fg-muted`}>{formatDateTime(o.created_at)}</td>
                      <td className={`${TD} text-right tabular-nums text-fg`}>{formatIDR(o.amount_idr)}</td>
                      <td className={`${TD} text-right font-semibold tabular-nums text-fg`}>{formatUSD(o.credit_usd)}</td>
                      <td className={TD}>
                        <Badge tone={ORDER_STATUS_TONE[o.status] ?? "neutral"}>{o.status}</Badge>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </PanelCard>
      )}

      <PanelCard
        icon={History}
        title="Top-up history"
        count={topups.length}
        footer={
          <p className="border-t border-line bg-subtle px-4 py-2.5 text-[12px] text-fg-muted sm:px-5">
            Top-ups are applied by an administrator or automatically upon confirmed payment.
          </p>
        }
      >
        {topups.length === 0 ? (
          <EmptyState title="No top-ups yet" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[520px] text-[13px]">
              <thead className={THEAD}>
                <tr>
                  <th scope="col" className={`${TH} text-left`}>Date</th>
                  <th scope="col" className={`${TH} text-left`}>Reason</th>
                  <th scope="col" className={`${TH} text-left`}>Status</th>
                  <th scope="col" className={`${TH} text-right`}>Amount</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {topups.map((t) => (
                  <tr key={t.id} className="transition-colors hover:bg-hover">
                    <td className={`${TD} whitespace-nowrap tabular-nums text-fg-muted`}>{formatDateTime(t.created_at)}</td>
                    <td className={`${TD} text-fg`}>{t.reason || "—"}</td>
                    <td className={TD}>
                      <Badge tone="success">credited</Badge>
                    </td>
                    <td className={`${TD} text-right font-semibold tabular-nums text-fg`}>{formatUSD(t.amount_usd)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </PanelCard>
    </div>
  );
}
