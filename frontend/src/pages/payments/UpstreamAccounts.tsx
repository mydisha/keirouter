import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ExternalLink, Loader2, Plus, Server } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type FinanceReport, type UpstreamAccountBalance } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { Badge, Button, EmptyState, ErrorBanner, Field, Input, LoadingRegion, Modal, ModalFooter, Skeleton } from "../../components/ui";
import { QK, Panel, SETTINGS_ANCHOR, count, errorText, todayISO, usd } from "./shared";

export function UpstreamAccounts({
  report,
  loading,
  error,
  rangeLabel,
}: {
  report?: FinanceReport;
  loading: boolean;
  error: string | null;
  rangeLabel: string;
}) {
  const [topupFor, setTopupFor] = useState<UpstreamAccountBalance | null>(null);
  const accounts = (report?.accounts ?? []).filter((a) => a.status !== "archived");
  // Active accounts first, planned ones after; the API order is kept inside each group.
  const sorted = [...accounts.filter((a) => a.status !== "planned"), ...accounts.filter((a) => a.status === "planned")];
  const unmapped = report?.unmapped;

  return (
    <Panel icon={Server} title="Saldo provider upstream" subtitle={`Saldo dihitung dari saldo awal, topup, dan pemakaian. Terpakai: ${rangeLabel.toLowerCase()}.`}>
      {loading && !report ? (
        <LoadingRegion label="Memuat saldo provider" className="grid grid-cols-1 gap-3 p-4 md:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-[220px] w-full rounded-xl" />
          ))}
        </LoadingRegion>
      ) : !report ? (
        <div className="p-4">
          <ErrorBanner message={`Saldo provider gagal dimuat. ${error ?? ""}`.trim()} />
        </div>
      ) : (
        <div className="space-y-3 p-4">
          {unmapped && (unmapped.spent_range_usd > 0 || unmapped.requests > 0) && (
            <div role="status" className="flex flex-col gap-2 rounded-lg border border-warn/30 bg-warn/10 px-3.5 py-2.5 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex min-w-0 items-start gap-2.5">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
                <p className="min-w-0 break-words text-[13px] leading-snug text-fg">
                  {usd(unmapped.spent_range_usd)} dari {count(unmapped.requests)} request belum masuk ke akun mana pun
                  {unmapped.providers.length > 0 && <> ({unmapped.providers.join(", ")})</>}. Saldo akun tidak menghitung pemakaian ini.
                </p>
              </div>
              <a href={`#${SETTINGS_ANCHOR}`} className="shrink-0 text-[12.5px] font-medium text-link hover:underline">
                Atur pemetaan provider
              </a>
            </div>
          )}
          {sorted.length === 0 ? (
            <EmptyState icon={Server} size="sm" title="Belum ada akun upstream" hint="Akun upstream muncul di sini setelah dibuat di pengaturan." />
          ) : (
            <ul className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
              {sorted.map((a) => (
                <AccountCard key={a.id} account={a} onTopup={() => setTopupFor(a)} />
              ))}
            </ul>
          )}
        </div>
      )}
      {topupFor && <TopupModal account={topupFor} onClose={() => setTopupFor(null)} />}
    </Panel>
  );
}

function daysLeftText(a: UpstreamAccountBalance): string {
  if (a.days_left == null) return "Belum ada pemakaian 7 hari";
  if (a.days_left <= 0) return "Habis";
  if (a.days_left < 1) return "Kurang dari 1 hari";
  return `Sekitar ${Math.floor(a.days_left).toLocaleString("id-ID")} hari`;
}

function AccountCard({ account: a, onTopup }: { account: UpstreamAccountBalance; onTopup: () => void }) {
  const planned = a.status === "planned";
  return (
    <li
      className={cn(
        "flex min-w-0 flex-col rounded-xl border bg-surface p-4",
        a.low && !planned ? "border-bad/40" : "border-line",
        planned && "bg-subtle",
      )}
    >
      <div className={cn("flex min-w-0 items-start justify-between gap-2", planned && "opacity-70")}>
        <div className="min-w-0">
          <h3 className="truncate text-[14px] font-semibold text-fg">{a.name}</h3>
          {a.url && (
            <a href={a.url} target="_blank" rel="noreferrer" className="inline-flex max-w-full items-center gap-1 truncate text-[12px] text-fg-muted hover:text-fg hover:underline">
              <span className="truncate">{a.url.replace(/^https?:\/\//, "")}</span>
              <ExternalLink className="h-3 w-3 shrink-0" aria-hidden="true" />
            </a>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap justify-end gap-1">
          {planned && <Badge>Direncanakan</Badge>}
          {a.low && !planned && <Badge tone="danger">Saldo rendah</Badge>}
        </div>
      </div>

      <div className={cn("mt-3", planned && "opacity-70")}>
        <p className="text-[12px] text-fg-muted">Saldo</p>
        <p className={cn("text-[22px] font-semibold leading-tight tabular-nums", a.low && !planned ? "text-bad" : "text-fg")}>{usd(a.balance_usd)}</p>
        <p className="mt-0.5 text-[12px] text-fg-muted">
          Batas peringatan {a.low_balance_usd > 0 ? usd(a.low_balance_usd) : "belum diatur"}
          {a.spent_estimated_usd > 0 && <> · termasuk estimasi {usd(a.spent_estimated_usd)}</>}
        </p>
      </div>

      <dl className={cn("mt-3 grid grid-cols-2 gap-x-3 gap-y-2 text-[12.5px]", planned && "opacity-70")}>
        <div className="min-w-0">
          <dt className="text-fg-muted">Terpakai di rentang</dt>
          <dd className="tabular-nums text-fg">{usd(a.spent_range_usd)}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-muted">Perkiraan habis</dt>
          <dd className="text-fg">{daysLeftText(a)}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-muted">Request aktif</dt>
          <dd className="tabular-nums text-fg">{count(a.in_flight)}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-muted">Rata-rata harian</dt>
          <dd className="tabular-nums text-fg">{usd(a.avg_daily_usd)}</dd>
        </div>
      </dl>

      <div className={cn("mt-3 flex min-w-0 flex-wrap gap-1", planned && "opacity-70")}>
        {a.providers.length === 0 ? (
          <span className="text-[12px] text-fg-faint">Belum ada provider dipetakan</span>
        ) : (
          a.providers.map((p) => (
            <Badge key={p} className="max-w-full truncate">
              {p}
            </Badge>
          ))
        )}
      </div>

      <div className="mt-4 flex flex-1 items-end">
        <Button variant="ghost" onClick={onTopup} className="w-full sm:w-auto">
          <Plus aria-hidden="true" /> Catat topup
        </Button>
      </div>
    </li>
  );
}

function TopupModal({ account, onClose }: { account: UpstreamAccountBalance; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(todayISO());
  const [note, setNote] = useState("");
  const [submitted, setSubmitted] = useState(false);

  const amountNum = Number(amount);
  const amountError =
    amount.trim() === "" || !Number.isFinite(amountNum) || amountNum <= 0
      ? "Isi jumlah lebih dari 0."
      : amountNum > 1_000_000
        ? "Jumlah maksimal $1,000,000."
        : null;
  const dateError = date && date > todayISO() ? "Tanggal tidak boleh di masa depan." : null;
  const noteError = note.length > 500 ? "Catatan maksimal 500 karakter." : null;
  const valid = !amountError && !dateError && !noteError;

  const create = useMutation({
    mutationFn: () => api.createUpstreamTopup(account.id, { amount_usd: amountNum, topup_at: date || undefined, note: note.trim() || undefined }),
    onSuccess: () => {
      toast.success("Topup dicatat", `${usd(amountNum)} ditambahkan ke saldo ${account.name}.`);
      qc.invalidateQueries({ queryKey: QK.finance });
      qc.invalidateQueries({ queryKey: QK.accounts });
      qc.invalidateQueries({ queryKey: QK.topups });
      onClose();
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    if (!valid || create.isPending) return;
    create.mutate();
  };

  return (
    <Modal open onClose={onClose} title="Catat topup" subtitle={account.name}>
      <form onSubmit={onSubmit} noValidate>
        <div className="space-y-4 px-5 py-4">
          <Field label="Jumlah (USD)" required error={submitted ? amountError : undefined}>
            <Input
              type="number"
              inputMode="decimal"
              min="0.01"
              step="0.01"
              autoFocus
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder="50.00"
            />
          </Field>
          <Field label="Tanggal" hint="Tanggal saldo masuk di provider." error={dateError ?? undefined}>
            <Input type="date" max={todayISO()} value={date} onChange={(e) => setDate(e.target.value)} />
          </Field>
          <Field label="Catatan" optional error={noteError ?? undefined}>
            <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Misal: transfer via kartu kredit" maxLength={500} />
          </Field>
          {create.isError && <ErrorBanner message={`Topup gagal dicatat. ${errorText(create.error)}`} />}
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Plus aria-hidden="true" />}
            Simpan topup
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
