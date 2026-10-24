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
      toast.success("Top-up deleted", "Account balance recalculated.");
      qc.invalidateQueries({ queryKey: QK.finance });
      qc.invalidateQueries({ queryKey: QK.accounts });
      qc.invalidateQueries({ queryKey: QK.topups });
    },
    onError: (e) => toast.error("Couldn't delete the top-up", errorText(e)),
  });

  const onDelete = async (t: UpstreamTopup) => {
    const ok = await confirm({
      title: "Delete this top-up?",
      description: `${usd(t.amount_usd)} for ${names.get(t.account_id) ?? t.account_id} on ${dateID(t.topup_at)}. The account balance drops by this amount.`,
      confirmLabel: "Delete",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (ok) remove.mutate(t.id);
  };

  return (
    <Panel icon={History} title="Top-up history" subtitle="Top-ups recorded by hand for upstream accounts.">
      {accountsError && !accounts ? (
        <div className="p-4">
          <ErrorBanner message={`Couldn't load accounts, so top-up history can't be shown. ${accountsError}`} />
        </div>
      ) : loading && rows.length === 0 ? (
        <LoadingRegion label="Loading top-up history" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </LoadingRegion>
      ) : (
        <>
          {failed.length > 0 && (
            <div className="p-4 pb-0">
              <ErrorBanner message={`Part of the top-up history couldn't be loaded. ${errorText(failed[0].error)}`} />
            </div>
          )}
          {rows.length === 0 ? (
            failed.length === 0 && (
              <EmptyState icon={History} size="sm" title="No top-ups yet" hint='Use "Record top-up" on an account card to log added balance.' />
            )
          ) : (
            <>
              <div className="overflow-x-auto" role="region" aria-label="Top-up history table" tabIndex={0}>
                <table className="w-full min-w-[640px] text-[13px]">
                  <thead>
                    <tr className={THEAD}>
                      <th scope="col" className={TH}>Date</th>
                      <th scope="col" className={TH}>Account</th>
                      <th scope="col" className={cn(TH, "text-right")}>Amount</th>
                      <th scope="col" className={TH}>Note</th>
                      <th scope="col" className={cn(TH, "text-right")}>
                        <span className="sr-only">Actions</span>
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
                            <Button variant="danger" onClick={() => onDelete(t)} disabled={deleting} aria-label={`Delete top-up of ${usd(t.amount_usd)} on ${dateID(t.topup_at)}`}>
                              {deleting ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
                              Delete
                            </Button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              <TablePagination page={page} pages={pages} total={total} onPage={setPage} label="Top-up history pages" />
            </>
          )}
        </>
      )}
    </Panel>
  );
}
