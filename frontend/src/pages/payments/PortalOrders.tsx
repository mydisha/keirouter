import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, CreditCard, Loader2, ShoppingCart } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type PaymentOrderAdmin } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { Badge, Button, EmptyState, ErrorBanner, LoadingRegion, Modal, ModalFooter, Skeleton, Textarea } from "../../components/ui";
import { Panel, REFETCH_MS, TH, THEAD, errorText, idr, usd } from "./shared";

const STATUS_TONE: Record<string, "success" | "warning" | "danger" | "neutral"> = {
  completed: "success",
  manual: "success",
  pending: "warning",
  failed: "danger",
  expired: "neutral",
};

const STATUS_LABEL: Record<string, string> = {
  completed: "Lunas",
  manual: "Disetujui manual",
  pending: "Menunggu",
  failed: "Gagal",
  expired: "Kedaluwarsa",
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
    onSuccess: () => {
      toast.success("Pembayaran disetujui", "Credit sudah ditambahkan ke key user.");
      qc.invalidateQueries({ queryKey: ["payment-orders"] });
      qc.invalidateQueries({ queryKey: ["payment-summary"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
      setApproving(null);
      setReason("");
    },
  });

  const orders = ordersQuery.data?.orders ?? [];
  const pending = summaryQuery.data?.count_by_status?.pending ?? 0;

  const close = () => {
    setApproving(null);
    approve.reset();
  };

  return (
    <Panel
      icon={ShoppingCart}
      title="Order portal"
      subtitle="Topup credit user lewat payment gateway, dengan persetujuan manual."
      action={pending > 0 ? <Badge tone="warning">{pending} menunggu</Badge> : undefined}
    >
      {ordersQuery.isLoading ? (
        <LoadingRegion label="Memuat order portal" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </LoadingRegion>
      ) : ordersQuery.isError && !ordersQuery.data ? (
        <div className="p-4">
          <ErrorBanner message={`Order portal gagal dimuat. ${errorText(ordersQuery.error)}`} />
        </div>
      ) : orders.length === 0 ? (
        <EmptyState icon={ShoppingCart} size="sm" title="Belum ada pembayaran" hint="Topup dari portal muncul di sini begitu user memulai pembayaran." />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Tabel order portal" tabIndex={0}>
          <table className="w-full min-w-[760px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>User</th>
                <th scope="col" className={TH}>Key</th>
                <th scope="col" className={cn(TH, "text-right")}>Jumlah</th>
                <th scope="col" className={cn(TH, "text-right")}>Credit</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={cn(TH, "text-right")}>
                  <span className="sr-only">Aksi</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {orders.map((o) => (
                <tr key={o.order_id} className="transition-colors hover:bg-hover">
                  <td className="px-4 py-2.5">
                    <p className="text-fg">{o.user_email || o.google_sub || "-"}</p>
                    <p className="font-mono text-[11px] text-fg-muted">{o.provider_payment_id}</p>
                  </td>
                  <td className="px-4 py-2.5 font-mono text-[12px] text-fg-muted">{o.key_display || o.key_id}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">{idr(o.amount_idr)}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right font-semibold tabular-nums">{usd(o.credit_usd)}</td>
                  <td className="px-4 py-2.5">
                    <Badge tone={STATUS_TONE[o.status] ?? "neutral"}>{STATUS_LABEL[o.status] ?? o.status}</Badge>
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    {o.status === "pending" && (
                      <Button variant="ghost" onClick={() => setApproving(o)} title="Setujui manual">
                        <Check aria-hidden="true" /> Setujui
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {approving && (
        <Modal open onClose={close} title="Setujui pembayaran">
          <div className="space-y-3 px-5 py-4">
            <p className="text-[13px] text-fg-muted">
              Tambah credit {usd(approving.credit_usd)} ke {approving.user_email || approving.key_id}. Tindakan ini tidak bisa dibatalkan otomatis.
            </p>
            <Textarea
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Alasan, misal: transfer bank sudah dicek"
              aria-label="Alasan persetujuan"
              rows={3}
            />
            {approve.isError && <ErrorBanner message={`Persetujuan gagal. ${errorText(approve.error)}`} />}
          </div>
          <ModalFooter>
            <Button variant="ghost" onClick={close}>
              Batal
            </Button>
            <Button onClick={() => approve.mutate({ id: approving.order_id, reason })} disabled={approve.isPending}>
              {approve.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <CreditCard aria-hidden="true" />}
              Setujui dan tambah credit
            </Button>
          </ModalFooter>
        </Modal>
      )}
    </Panel>
  );
}
