import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, CreditCard, Loader2, ShoppingCart } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type PaymentOrderAdmin } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { Badge, Button, EmptyState, ErrorBanner, LoadingRegion, Modal, ModalFooter, Skeleton, TablePagination, Textarea, useClientPagination } from "../../components/ui";
import { Panel, REFETCH_MS, TH, THEAD, dateID, errorText, idr, usd } from "./shared";

const STATUS_TONE: Record<string, "success" | "warning" | "danger" | "neutral"> = {
  completed: "success",
  manual: "success",
  pending: "warning",
  failed: "danger",
  expired: "neutral",
};

const STATUS_LABEL: Record<string, string> = {
  completed: "Paid",
  manual: "Approved manually",
  pending: "Pending",
  failed: "Failed",
  expired: "Expired",
};

export function PortalOrders() {
  const qc = useQueryClient();
  const toast = useToast();
  const [approving, setApproving] = useState<PaymentOrderAdmin | null>(null);
  const [reason, setReason] = useState("");

  const ordersQuery = useQuery({ queryKey: ["payment-orders"], queryFn: api.paymentOrders, refetchInterval: REFETCH_MS });
  const summaryQuery = useQuery({ queryKey: ["payment-summary"], queryFn: api.paymentSummary, refetchInterval: REFETCH_MS });

  const approve = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.approvePaymentOrder(id, reason),
    onSuccess: (order) => {
      const hosting = (order?.product ?? approving?.product) === "hosting";
      toast.success("Payment approved", hosting ? "The instance can now be created on the Hosting page." : "Credit added to the user's key.");
      if (hosting) qc.invalidateQueries({ queryKey: ["hosting-instances"] });
      qc.invalidateQueries({ queryKey: ["payment-orders"] });
      qc.invalidateQueries({ queryKey: ["payment-summary"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
      setApproving(null);
      setReason("");
    },
  });

  const orders = ordersQuery.data?.orders ?? [];
  const pending = summaryQuery.data?.count_by_status?.pending ?? 0;
  const { paged, page, pages, setPage, total } = useClientPagination(orders, 10);

  const close = () => {
    setApproving(null);
    approve.reset();
  };

  return (
    <Panel
      icon={ShoppingCart}
      title="Portal orders"
      subtitle="User credit top-ups through the payment gateway, with manual approval."
      action={pending > 0 ? <Badge tone="warning">{pending} pending</Badge> : undefined}
    >
      {ordersQuery.isLoading ? (
        <LoadingRegion label="Loading portal orders" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </LoadingRegion>
      ) : ordersQuery.isError && !ordersQuery.data ? (
        <div className="p-4">
          <ErrorBanner message={`Couldn't load portal orders. ${errorText(ordersQuery.error)}`} />
        </div>
      ) : orders.length === 0 ? (
        <EmptyState icon={ShoppingCart} size="sm" title="No payments yet" hint="Portal top-ups show up here as soon as a user starts a payment." />
      ) : (
        <>
          <div className="overflow-x-auto" role="region" aria-label="Portal orders table" tabIndex={0}>
            <table className="w-full min-w-[960px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <th scope="col" className={TH}>User</th>
                  <th scope="col" className={TH}>Key</th>
                  <th scope="col" className={cn(TH, "text-right")}>Amount</th>
                  <th scope="col" className={cn(TH, "text-right")}>Credit</th>
                  <th scope="col" className={TH}>Status</th>
                  <th scope="col" className={TH}>Invoice created</th>
                  <th scope="col" className={TH}>Paid</th>
                  <th scope="col" className={cn(TH, "text-right")}>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {paged.map((o) => (
                  <tr key={o.order_id} className="transition-colors hover:bg-hover">
                    <td className="px-4 py-2.5">
                      <p className="flex flex-wrap items-center gap-1.5 text-fg">
                        {o.user_email || o.google_sub || "-"}
                        {o.product === "hosting" && <Badge tone="accent">Hosting</Badge>}
                      </p>
                      <p className="font-mono text-[11px] text-fg-muted">{o.provider_payment_id}</p>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[12px] text-fg-muted">{o.key_display || o.key_id}</td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">{idr(o.amount_idr)}</td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right font-semibold tabular-nums">
                      {o.product === "hosting" ? <span className="font-normal text-fg-muted">-</span> : usd(o.credit_usd)}
                    </td>
                    <td className="px-4 py-2.5">
                      <Badge tone={STATUS_TONE[o.status] ?? "neutral"}>{STATUS_LABEL[o.status] ?? o.status}</Badge>
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">{dateID(o.created_at, true)}</td>
                    <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">{o.paid_at ? dateID(o.paid_at, true) : "-"}</td>
                    <td className="px-4 py-2.5 text-right">
                      {o.status === "pending" && (
                        <Button variant="ghost" onClick={() => setApproving(o)} title="Approve manually">
                          <Check aria-hidden="true" /> Approve
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} label="Portal order pages" />
        </>
      )}

      {approving && (
        <Modal open onClose={close} title="Approve payment">
          <div className="space-y-3 px-5 py-4">
            <p className="text-[13px] text-fg-muted">
              {approving.product === "hosting"
                ? `Mark the ${idr(approving.amount_idr)} hosting order from ${approving.user_email || approving.key_id} as paid. The instance can then be created on the Hosting page.`
                : `Add ${usd(approving.credit_usd)} credit to ${approving.user_email || approving.key_id}.`}{" "}
              This can't be reversed automatically.
            </p>
            <Textarea
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Reason, for example: bank transfer checked"
              aria-label="Approval reason"
              rows={3}
            />
            {approve.isError && <ErrorBanner message={`Approval failed. ${errorText(approve.error)}`} />}
          </div>
          <ModalFooter>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button onClick={() => approve.mutate({ id: approving.order_id, reason })} disabled={approve.isPending}>
              {approve.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <CreditCard aria-hidden="true" />}
              {approving.product === "hosting" ? "Approve payment" : "Approve and add credit"}
            </Button>
          </ModalFooter>
        </Modal>
      )}
    </Panel>
  );
}
