import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, Check, Loader2, Send, Settings2, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  api,
  type FinanceProviderOption,
  type UpstreamAccount,
  type UpstreamAccountInput,
  type UpstreamAccountStatus,
} from "../../lib/api";
import { useToast } from "../../components/Toast";
import { useConfirm } from "../../components/ui/confirm-dialog";
import { Badge, Button, EmptyState, ErrorBanner, Field, Input, LoadingRegion, Select, Skeleton, Toggle } from "../../components/ui";
import { QK, Panel, REFETCH_MS, SETTINGS_ANCHOR, dateInputToRFC3339, errorText, toDateInput, todayISO } from "./shared";

const STATUS_LABEL: Record<UpstreamAccountStatus, string> = {
  active: "Aktif",
  planned: "Direncanakan",
  archived: "Diarsipkan",
};

export function FinanceSettings({
  accounts,
  accountsLoading,
  accountsError,
}: {
  accounts?: UpstreamAccount[];
  accountsLoading: boolean;
  accountsError: string | null;
}) {
  const providersQuery = useQuery({ queryKey: QK.providers, queryFn: api.financeProviders, refetchInterval: REFETCH_MS });
  const providers = providersQuery.data?.providers ?? [];

  return (
    <Panel id={SETTINGS_ANCHOR} icon={Settings2} title="Pengaturan keuangan" subtitle="Saldo awal, batas peringatan, dan provider untuk tiap akun upstream.">
      <div className="space-y-4 p-4">
        {accountsLoading && !accounts ? (
          <LoadingRegion label="Memuat akun upstream" className="space-y-3">
            {[0, 1].map((i) => (
              <Skeleton key={i} className="h-[260px] w-full rounded-xl" />
            ))}
          </LoadingRegion>
        ) : !accounts ? (
          <ErrorBanner message={`Akun upstream gagal dimuat. ${accountsError ?? ""}`.trim()} />
        ) : accounts.length === 0 ? (
          <EmptyState icon={Settings2} size="sm" title="Belum ada akun upstream" hint="Akun Surplus Intelligence, Inferhub, dan Crowbot dibuat oleh migrasi database." />
        ) : (
          <>
            {providersQuery.isError && (
              <ErrorBanner message={`Daftar provider gagal dimuat, pemetaan provider belum bisa diubah. ${errorText(providersQuery.error)}`} />
            )}
            <ul className="space-y-3">
              {accounts.map((a) => (
                // Re-key on updated_at so a successful save resets the form to the stored values.
                <AccountForm
                  key={`${a.id}:${a.updated_at}:${a.providers.join(",")}`}
                  account={a}
                  accounts={accounts}
                  providers={providers}
                  providersReady={providersQuery.isSuccess}
                />
              ))}
            </ul>
          </>
        )}
        <AlertSettings />
      </div>
    </Panel>
  );
}

function AccountForm({
  account,
  accounts,
  providers,
  providersReady,
}: {
  account: UpstreamAccount;
  accounts: UpstreamAccount[];
  providers: FinanceProviderOption[];
  providersReady: boolean;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [status, setStatus] = useState<UpstreamAccountStatus>(account.status);
  const [opening, setOpening] = useState(String(account.opening_balance_usd));
  const [openingAt, setOpeningAt] = useState(toDateInput(account.opening_at));
  const [threshold, setThreshold] = useState(String(account.low_balance_usd));
  const [mapped, setMapped] = useState<string[]>(account.providers);

  const openingNum = Number(opening);
  const thresholdNum = Number(threshold);
  const amountError = (raw: string, n: number) =>
    raw.trim() === "" || !Number.isFinite(n) || n < 0 ? "Isi angka 0 atau lebih." : n > 1e9 ? "Angka terlalu besar." : null;
  const openingError = amountError(opening, openingNum);
  const thresholdError = amountError(threshold, thresholdNum);
  const openingAtError = !openingAt ? "Isi tanggal saldo awal." : openingAt > todayISO() ? "Tanggal tidak boleh di masa depan." : null;

  // Only changed fields are sent, so an untouched opening date keeps its exact stored time.
  const changes: UpstreamAccountInput = {};
  if (status !== account.status) changes.status = status;
  if (!openingError && openingNum !== account.opening_balance_usd) changes.opening_balance_usd = openingNum;
  if (!openingAtError && openingAt !== toDateInput(account.opening_at)) changes.opening_at = dateInputToRFC3339(openingAt);
  if (!thresholdError && thresholdNum !== account.low_balance_usd) changes.low_balance_usd = thresholdNum;
  const sameProviders = mapped.length === account.providers.length && mapped.every((p) => account.providers.includes(p));
  if (!sameProviders) changes.providers = mapped;
  const dirty = Object.keys(changes).length > 0;
  const valid = !openingError && !thresholdError && !openingAtError;

  const save = useMutation({
    mutationFn: () => api.updateUpstreamAccount(account.id, changes),
    onSuccess: () => {
      toast.success("Akun disimpan", `Pengaturan ${account.name} sudah diperbarui.`);
      qc.invalidateQueries({ queryKey: QK.finance });
      qc.invalidateQueries({ queryKey: QK.accounts });
      qc.invalidateQueries({ queryKey: QK.topups });
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!dirty || !valid || save.isPending) return;
    save.mutate();
  };

  // Providers already mapped to another account: picking one moves it here.
  const owner = new Map<string, string>();
  for (const other of accounts) {
    if (other.id === account.id) continue;
    for (const p of other.providers) owner.set(p, other.name);
  }
  // Keep mapped ids visible even when the provider list does not include them.
  const options: FinanceProviderOption[] = [
    ...providers,
    ...mapped.filter((id) => !providers.some((p) => p.id === id)).map((id) => ({ id, label: id })),
  ];

  const toggleProvider = (id: string) => setMapped((cur) => (cur.includes(id) ? cur.filter((p) => p !== id) : [...cur, id]));

  return (
    <li className={cn("rounded-xl border border-line", account.status !== "active" && "bg-subtle")}>
      <form onSubmit={onSubmit} noValidate>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2.5">
          <h3 className="text-[13.5px] font-semibold text-fg">{account.name}</h3>
          {account.status !== "active" && <Badge>{STATUS_LABEL[account.status]}</Badge>}
        </div>
        <div className="grid grid-cols-1 gap-4 px-4 py-4 sm:grid-cols-2 xl:grid-cols-4">
          <Field label="Status">
            <Select value={status} onChange={(e) => setStatus(e.target.value as UpstreamAccountStatus)}>
              {(Object.keys(STATUS_LABEL) as UpstreamAccountStatus[]).map((s) => (
                <option key={s} value={s}>
                  {STATUS_LABEL[s]}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Saldo awal (USD)" error={openingError}>
            <Input type="number" inputMode="decimal" min="0" step="0.01" value={opening} onChange={(e) => setOpening(e.target.value)} />
          </Field>
          <Field label="Tanggal saldo awal" hint="Pemakaian dihitung mulai tanggal ini." error={openingAtError}>
            <Input type="date" max={todayISO()} value={openingAt} onChange={(e) => setOpeningAt(e.target.value)} />
          </Field>
          <Field label="Batas peringatan (USD)" hint="0 berarti tanpa peringatan." error={thresholdError}>
            <Input type="number" inputMode="decimal" min="0" step="0.01" value={threshold} onChange={(e) => setThreshold(e.target.value)} />
          </Field>
        </div>
        <fieldset className="px-4 pb-4">
          <legend className="mb-1.5 text-[12.5px] font-medium text-fg">Provider</legend>
          <p className="mb-2 text-[12px] text-fg-muted">Pemakaian provider yang dicentang mengurangi saldo akun ini. Provider yang sudah dipakai akun lain akan dipindah ke sini.</p>
          {!providersReady && options.length === 0 ? (
            <p className="text-[12px] text-fg-faint">Daftar provider belum tersedia.</p>
          ) : options.length === 0 ? (
            <p className="text-[12px] text-fg-faint">Belum ada provider dengan pemakaian atau provider kustom.</p>
          ) : (
            <div className="flex max-h-48 flex-wrap gap-1.5 overflow-y-auto">
              {options.map((p) => {
                const checked = mapped.includes(p.id);
                const elsewhere = owner.get(p.id);
                return (
                  <label
                    key={p.id}
                    className={cn(
                      "inline-flex min-h-8 max-w-full cursor-pointer items-center gap-2 rounded-lg border px-2.5 py-1 text-[12.5px] transition-colors",
                      checked ? "border-accent-500/40 bg-accent-500/10 text-fg" : "border-line bg-surface text-fg-muted hover:bg-hover",
                    )}
                  >
                    <input type="checkbox" className="h-4 w-4 rounded border-input accent-accent-500" checked={checked} onChange={() => toggleProvider(p.id)} />
                    <span className="truncate">{p.label}</span>
                    {p.label !== p.id && <span className="truncate font-mono text-[11px] text-fg-faint">{p.id}</span>}
                    {elsewhere && !checked && <span className="shrink-0 text-[11px] text-fg-faint">({elsewhere})</span>}
                  </label>
                );
              })}
            </div>
          )}
        </fieldset>
        {save.isError && (
          <div className="px-4 pb-3">
            <ErrorBanner message={`Gagal menyimpan ${account.name}. ${errorText(save.error)}`} />
          </div>
        )}
        <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
          {dirty && <span className="text-[12px] text-fg-muted sm:mr-auto">Ada perubahan yang belum disimpan.</span>}
          <Button type="submit" disabled={!dirty || !valid || save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
            Simpan
          </Button>
        </div>
      </form>
    </li>
  );
}

function AlertSettings() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const alerts = useQuery({ queryKey: QK.alerts, queryFn: api.financeAlerts, refetchInterval: REFETCH_MS });
  const [url, setUrl] = useState("");
  // null means "follow the stored value" until the operator flips the switch.
  const [enabledDraft, setEnabledDraft] = useState<boolean | null>(null);
  const enabled = enabledDraft ?? alerts.data?.enabled ?? false;

  const configured = alerts.data?.configured ?? false;
  const hasWebhook = configured || url.trim() !== "";
  // The server turns alerts off without a webhook; block that save up front.
  const needsWebhook = enabled && !hasWebhook;

  const save = useMutation({
    mutationFn: () => api.updateFinanceAlerts({ enabled, ...(url.trim() ? { discord_webhook_url: url.trim() } : {}) }),
    onSuccess: (data) => {
      qc.setQueryData(QK.alerts, data);
      setUrl("");
      setEnabledDraft(null);
      toast.success("Notifikasi disimpan", data.enabled ? "Peringatan saldo rendah akan dikirim ke Discord." : "Peringatan saldo rendah dimatikan.");
    },
  });

  const remove = useMutation({
    mutationFn: () => api.updateFinanceAlerts({ enabled: false, discord_webhook_url: "" }),
    onSuccess: (data) => {
      qc.setQueryData(QK.alerts, data);
      setUrl("");
      setEnabledDraft(null);
      toast.success("Webhook dihapus", "Peringatan saldo rendah dimatikan.");
    },
  });

  const onRemove = async () => {
    if (remove.isPending) return;
    const ok = await confirm({
      title: "Hapus webhook Discord?",
      description: "Peringatan saldo rendah akan dimatikan sampai webhook baru disimpan.",
      confirmLabel: "Hapus webhook",
      cancelLabel: "Batal",
      tone: "danger",
    });
    if (!ok) return;
    save.reset();
    test.reset();
    remove.mutate();
  };

  const test = useMutation({
    mutationFn: api.testFinanceAlerts,
    onSuccess: () => toast.success("Pesan tes terkirim", "Cek channel Discord yang terhubung."),
  });

  const dirty = url.trim() !== "" || (enabledDraft !== null && enabledDraft !== alerts.data?.enabled);

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!dirty || needsWebhook || save.isPending) return;
    test.reset();
    remove.reset();
    save.mutate();
  };

  return (
    <section className="rounded-xl border border-line" aria-labelledby="finance-alerts-title">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
        <Bell className="h-4 w-4 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
        <h3 id="finance-alerts-title" className="text-[13.5px] font-semibold text-fg">
          Notifikasi Discord
        </h3>
      </div>
      {alerts.isLoading ? (
        <LoadingRegion label="Memuat pengaturan notifikasi" className="p-4">
          <Skeleton className="h-24 w-full" />
        </LoadingRegion>
      ) : alerts.isError && !alerts.data ? (
        <div className="p-4">
          <ErrorBanner message={`Pengaturan notifikasi gagal dimuat. ${errorText(alerts.error)}`} />
        </div>
      ) : (
        <form onSubmit={onSubmit} noValidate>
          <div className="space-y-4 px-4 py-4">
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <p id="finance-alerts-enabled" className="text-[13px] font-medium text-fg">
                  Kirim peringatan saldo rendah
                </p>
                <p className="text-[12px] text-fg-muted">Satu pesan saat saldo akun turun di bawah batas peringatan.</p>
              </div>
              <Toggle checked={enabled} onChange={setEnabledDraft} aria-labelledby="finance-alerts-enabled" />
            </div>
            <Field
              label="Webhook Discord"
              hint={
                alerts.data?.configured
                  ? `Tersimpan: ${alerts.data.discord_webhook_url_masked}. Kosongkan untuk tetap memakai webhook ini.`
                  : "Belum ada webhook. Salin dari pengaturan channel Discord, menu Integrasi."
              }
            >
              <Input
                type="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://discord.com/api/webhooks/..."
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            </Field>
            {needsWebhook && (
              <p className="text-[12px] text-fg-muted" role="status">
                Isi webhook Discord dulu. Peringatan hanya bisa diaktifkan setelah webhook tersimpan.
              </p>
            )}
            {save.isError && <ErrorBanner message={`Pengaturan notifikasi gagal disimpan. ${errorText(save.error)}`} />}
            {remove.isError && <ErrorBanner message={`Webhook gagal dihapus. ${errorText(remove.error)}`} />}
            {test.isError && <ErrorBanner message={`Pesan tes gagal dikirim. ${errorText(test.error)}`} />}
          </div>
          <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
            {configured && (
              <Button type="button" variant="ghost" className="sm:mr-auto" onClick={onRemove} disabled={remove.isPending || save.isPending}>
                {remove.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
                Hapus webhook
              </Button>
            )}
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                remove.reset();
                save.reset();
                test.mutate();
              }}
              disabled={!alerts.data?.configured || test.isPending}
              title={alerts.data?.configured ? undefined : "Simpan webhook dulu"}
            >
              {test.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Send aria-hidden="true" />}
              Kirim tes
            </Button>
            <Button type="submit" disabled={!dirty || needsWebhook || save.isPending || remove.isPending}>
              {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
              Simpan
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
