import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { CreditCard, Wallet } from "lucide-react";
import {
  createTopupOrder,
  fetchPortalPaymentConfig,
  fetchPortalStatus,
  fetchPortalTopupOrders,
  fetchPortalTopups,
  type PaymentOrder,
} from "../../lib/api";
import { Badge, Button, Card, EmptyState, ErrorCard, Input, Spinner } from "../../components/ui";
import { TurnstileWidget, useTurnstileSiteKey } from "../../components/TurnstileWidget";
import { useToast } from "../../components/Toast";
import { formatSpendUSD, formatUSD } from "../../lib/format";
import { portal } from "../../lib/portalRoutes";
import { SectionTitle, formatDateTime } from "./components";

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
        <EmptyState
          title="No API key yet"
          hint="Create an API key to see your top-up history."
        />
        <div className="flex justify-center">
          <Link
            to={portal("/key")}
            className="text-sm font-semibold text-accent-600 hover:underline dark:text-accent-400"
          >
            Create an API key
          </Link>
        </div>
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
    <div className="space-y-8">
      {returnStatus === "success" && (
        <Card className="flex items-start gap-3 border-emerald-300 bg-emerald-50 px-5 py-4 dark:border-emerald-800/60 dark:bg-emerald-950">
          <CreditCard size={18} className="mt-0.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
          <p className="text-sm text-emerald-800 dark:text-emerald-200">
            Payment received. Your credit will appear here as soon as it is confirmed.
          </p>
        </Card>
      )}
      {returnStatus === "cancel" && (
        <Card className="flex items-start gap-3 border-[var(--border)] bg-[var(--bg-subtle)]/50 px-5 py-4">
          <CreditCard size={18} className="mt-0.5 shrink-0 text-[var(--text-muted)]" />
          <p className="text-sm text-[var(--text-muted)]">Payment cancelled — no charge was made.</p>
        </Card>
      )}

      <section className="space-y-4">
        <SectionTitle title="Balance" icon={<Wallet size={17} />} />
        <Card className="p-6 md:p-7">
          {balance ? (
            <div className="grid grid-cols-1 gap-5 sm:grid-cols-3">
              <BalanceStat label="Budget Limit" value={formatSpendUSD(balance.limit_usd)} />
              <BalanceStat label="Spent" value={formatSpendUSD(balance.spent_usd)} />
              <BalanceStat label="Remaining" value={formatSpendUSD(balance.usd_remaining)} accent />
            </div>
          ) : (
            <p className="py-2 text-sm text-[var(--text-muted)]">No budget limit configured.</p>
          )}
        </Card>
      </section>

      {showBuy && (
        <section className="space-y-4">
          <SectionTitle title="Buy Credit" icon={<CreditCard size={17} />} />
          <Card className="p-6 md:p-7">
            <div className="grid grid-cols-1 gap-7 lg:grid-cols-2">
              <div className="space-y-5">
                {packages.length > 0 && (
                  <div className="space-y-2">
                    <p className="text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
                      Packages
                    </p>
                    <div className="flex flex-wrap gap-2">
                      {packages.map((p) => {
                        const selected = packageId === p.id;
                        return (
                          <button
                            key={p.id}
                            type="button"
                            onClick={() => {
                              setPackageId(p.id);
                              setAmount("");
                            }}
                            className={`flex flex-col items-start rounded-xl border px-4 py-3 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 ${
                              selected
                                ? "border-accent-500 bg-accent-50 dark:bg-accent-900/30"
                                : "border-[var(--border)] bg-[var(--bg-elevated)] hover:border-[var(--border-strong)] hover:bg-[var(--bg-subtle)]"
                            }`}
                          >
                            <span className="text-sm font-semibold text-[var(--text)]">{p.label}</span>
                            <span className="text-xs tabular-nums text-[var(--text-muted)]">{formatIDR(p.amount_idr)}</span>
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )}

                <label className="block space-y-2">
                  <span className="text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
                    Custom amount (IDR)
                  </span>
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
                  />
                  {(minIdr > 0 || maxIdr > 0) && (
                    <span className="text-xs text-[var(--text-muted)]">
                      {minIdr > 0 && `Min ${formatIDR(minIdr)}`}
                      {minIdr > 0 && maxIdr > 0 && " · "}
                      {maxIdr > 0 && `Max ${formatIDR(maxIdr)}`}
                    </span>
                  )}
                </label>
              </div>

              <div className="flex flex-col justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/40 p-5">
                <div>
                  <p className="text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
                    You pay
                  </p>
                  <p className="mt-1 text-2xl font-display font-semibold tabular-nums tracking-tight text-[var(--text)]">
                    {amountIdr > 0 ? formatIDR(amountIdr) : "—"}
                  </p>
                  <p className="mt-4 text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
                    Estimated credit
                  </p>
                  <p className="mt-1 text-2xl font-display font-semibold tabular-nums tracking-tight text-accent-600 dark:text-accent-400">
                    {fxRate > 0 && amountIdr > 0 ? formatUSD(creditUsd) : "—"}
                  </p>
                  {config?.currency_source && (
                    <p className="mt-2 text-xs text-[var(--text-muted)]">
                      Rate source: {config.currency_source}
                    </p>
                  )}
                </div>
                <TurnstileWidget
                  siteKey={siteKey}
                  onToken={setTurnstileToken}
                  resetSignal={resetSignal}
                  className="mt-5 flex justify-center"
                />
                <Button
                  className="mt-5 w-full"
                  onClick={submit}
                  disabled={!amountValid || !fxRate || buyMutation.isPending || (!!siteKey && !turnstileToken)}
                >
                  <CreditCard size={16} />
                  {buyMutation.isPending ? "Starting payment…" : "Buy credit"}
                </Button>
              </div>
            </div>
          </Card>
        </section>
      )}

      {showBuy && (
        <section className="space-y-4">
          <SectionTitle title="Payment Orders" icon={<CreditCard size={17} />} count={orderList.length} />
          <Card>
            {orders.isError ? (
              <EmptyState title="Could not load payment orders" />
            ) : orderList.length === 0 ? (
              <EmptyState title="No payment orders yet" />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="border-b border-[var(--border)] bg-[var(--bg-subtle)]/50">
                    <tr className="text-[11px] uppercase tracking-wide text-[var(--text-muted)]">
                      <th className="px-6 py-4 text-left font-semibold">Date</th>
                      <th className="px-6 py-4 text-right font-semibold">Amount</th>
                      <th className="px-6 py-4 text-right font-semibold">Credit</th>
                      <th className="px-6 py-4 text-left font-semibold">Status</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)]">
                    {orderList.map((o) => (
                      <tr key={o.order_id} className="transition-colors hover:bg-[var(--bg-subtle)]/30">
                        <td className="px-6 py-4 whitespace-nowrap tabular-nums text-[var(--text-muted)]">
                          {formatDateTime(o.created_at)}
                        </td>
                        <td className="px-6 py-4 text-right tabular-nums text-[var(--text)]">
                          {formatIDR(o.amount_idr)}
                        </td>
                        <td className="px-6 py-4 text-right font-semibold tabular-nums text-[var(--text)]">
                          {formatUSD(o.credit_usd)}
                        </td>
                        <td className="px-6 py-4">
                          <Badge tone={ORDER_STATUS_TONE[o.status] ?? "neutral"}>{o.status}</Badge>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
        </section>
      )}

      <section className="space-y-4">
        <SectionTitle title="Top-up History" icon={<Wallet size={17} />} count={topups.length} />
        <Card>
          {topups.length === 0 ? (
            <EmptyState title="No top-ups yet" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b border-[var(--border)] bg-[var(--bg-subtle)]/50">
                  <tr className="text-[11px] uppercase tracking-wide text-[var(--text-muted)]">
                    <th className="px-6 py-4 text-left font-semibold">Date</th>
                    <th className="px-6 py-4 text-left font-semibold">Reason</th>
                    <th className="px-6 py-4 text-left font-semibold">Status</th>
                    <th className="px-6 py-4 text-right font-semibold">Amount</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {topups.map((t) => (
                    <tr key={t.id} className="transition-colors hover:bg-[var(--bg-subtle)]/30">
                      <td className="px-6 py-4 whitespace-nowrap tabular-nums text-[var(--text-muted)]">
                        {formatDateTime(t.created_at)}
                      </td>
                      <td className="px-6 py-4 text-[var(--text)]">{t.reason || "—"}</td>
                      <td className="px-6 py-4">
                        <Badge tone="success">credited</Badge>
                      </td>
                      <td className="px-6 py-4 text-right font-semibold tabular-nums text-[var(--text)]">
                        {formatUSD(t.amount_usd)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      </section>

      <p className="text-xs text-[var(--text-muted)]">
        Top-ups are applied by an administrator or automatically upon confirmed payment.
      </p>
    </div>
  );
}

function BalanceStat({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/40 px-4 py-3">
      <p className="text-[11px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">{label}</p>
      <p className={`mt-1 text-2xl font-display font-semibold tabular-nums tracking-tight ${accent ? "text-accent-600 dark:text-accent-400" : "text-[var(--text)]"}`}>
        {value}
      </p>
    </div>
  );
}
