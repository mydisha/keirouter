import { useMemo } from "react";
import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { History, Loader2, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type UpstreamAccount, type UpstreamTopup } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { useConfirm } from "../../components/ui/confirm-dialog";
import { Button, EmptyState, ErrorBanner, LoadingRegion, Skeleton, TablePagination, useClientPagination } from "../../components/ui";
import { QK, Panel, REFETCH_MS, TH, THEAD, dateID, errorText, usd } from "./shared";

export function TopupHistory({
  accounts,
  accountsLoading,
  accountsError,
}: {
  accounts?: UpstreamAccount[];
  accountsLoading: boolean;
  accountsError: string | null;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const list = useMemo(() => accounts ?? [], [accounts]);

  // The API lists top-ups per account, so fetch each and merge newest first.
  const results = useQueries({
    queries: list.map((a) => ({
      queryKey: [...QK.topups, a.id],
      queryFn: () => api.upstreamTopups(a.id),
      refetchInterval: REFETCH_MS,
    })),
  });

  const names = useMemo(() => new Map(list.map((a) => [a.id, a.name])), [list]);
  const failed = results.filter((r) => r.isError);
  const loading = accountsLoading || results.some((r) => r.isLoading);
  const rows: UpstreamTopup[] = results
    .flatMap((r) => r.data?.topups ?? [])
    .sort((a, b) => (Date.parse(b.topup_at || b.created_at) || 0) - (Date.parse(a.topup_at || a.created_at) || 0));
  const { paged, page, pages, setPage, total } = useClientPagination(rows, 10);

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteUpstreamTopup(id),
    onSuccess: () => {
      toast.success("Topup dihapus", "Saldo akun sudah dihitung ulang.");
      qc.invalidateQueries({ queryKey: QK.finance });
      qc.invalidateQueries({ queryKey: QK.accounts });
      qc.invalidateQueries({ queryKey: QK.topups });
    },
    onError: (e) => toast.error("Topup gagal dihapus", errorText(e)),
  });

  const onDelete = async (t: UpstreamTopup) => {
    const ok = await confirm({
      title: "Hapus topup ini?",
      description: `${usd(t.amount_usd)} untuk ${names.get(t.account_id) ?? t.account_id} pada ${dateID(t.topup_at)}. Saldo akun akan berkurang sebesar jumlah ini.`,
      confirmLabel: "Hapus",
      cancelLabel: "Batal",
      tone: "danger",
    });
    if (ok) remove.mutate(t.id);
  };

  return (
    <Panel icon={History} title="Riwayat topup" subtitle="Topup yang dicatat manual untuk akun upstream.">
      {accountsError && !accounts ? (
        <div className="p-4">
          <ErrorBanner message={`Daftar akun gagal dimuat, riwayat topup tidak bisa ditampilkan. ${accountsError}`} />
        </div>
      ) : loading && rows.length === 0 ? (
        <LoadingRegion label="Memuat riwayat topup" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </LoadingRegion>
      ) : (
        <>
          {failed.length > 0 && (
            <div className="p-4 pb-0">
              <ErrorBanner message={`Riwayat topup sebagian gagal dimuat. ${errorText(failed[0].error)}`} />
            </div>
          )}
          {rows.length === 0 ? (
            failed.length === 0 && (
              <EmptyState icon={History} size="sm" title="Belum ada topup" hint='Pakai tombol "Catat topup" di kartu akun untuk mencatat saldo masuk.' />
            )
          ) : (
            <>
              <div className="overflow-x-auto" role="region" aria-label="Tabel riwayat topup" tabIndex={0}>
                <table className="w-full min-w-[640px] text-[13px]">
                  <thead>
                    <tr className={THEAD}>
                      <th scope="col" className={TH}>Tanggal</th>
                      <th scope="col" className={TH}>Akun</th>
                      <th scope="col" className={cn(TH, "text-right")}>Jumlah</th>
                      <th scope="col" className={TH}>Catatan</th>
                      <th scope="col" className={cn(TH, "text-right")}>
                        <span className="sr-only">Aksi</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {paged.map((t) => {
                      const deleting = remove.isPending && remove.variables === t.id;
                      return (
                        <tr key={t.id} className="transition-colors hover:bg-hover">
                          <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">{dateID(t.topup_at)}</td>
                          <td className="whitespace-nowrap px-4 py-2.5 font-medium text-fg">{names.get(t.account_id) ?? t.account_id}</td>
                          <td className="whitespace-nowrap px-4 py-2.5 text-right font-semibold tabular-nums">{usd(t.amount_usd)}</td>
                          <td className="max-w-[320px] px-4 py-2.5 text-fg-muted">
                            <span className="line-clamp-2 break-words">{t.note || "-"}</span>
                          </td>
                          <td className="whitespace-nowrap px-4 py-2.5 text-right">
                            <Button variant="danger" onClick={() => onDelete(t)} disabled={deleting} aria-label={`Hapus topup ${usd(t.amount_usd)} ${dateID(t.topup_at)}`}>
                              {deleting ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
                              Hapus
                            </Button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              <TablePagination page={page} pages={pages} total={total} onPage={setPage} label="Halaman riwayat topup" />
            </>
          )}
        </>
      )}
    </Panel>
  );
}
