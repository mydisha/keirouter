import { useEffect, useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, CheckCircle2, Copy, CreditCard, ExternalLink, Eye, EyeOff, KeyRound, Loader2, RefreshCw, Server, XCircle } from "lucide-react";
import {
  createPortalHostingOrder,
  fetchPortalHosting,
  fetchPortalHostingPassword,
  fetchPortalStatus,
  PortalAPIError,
  type PortalHostingInstance,
} from "../../lib/api";
import { Badge, Button, EmptyState, ErrorCard, Spinner } from "../../components/ui";
import { TurnstileWidget, useTurnstileSiteKey } from "../../components/TurnstileWidget";
import { useToast } from "../../components/Toast";
import { portal } from "../../lib/portalRoutes";
import { PageHeader, PanelCard } from "./components";

const HOSTING_QK = ["portal-hosting"] as const;

function formatIDR(n: number): string {
  return new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", maximumFractionDigits: 0 }).format(n);
}

function formatDateLong(value: string): string {
  const d = new Date(value);
  if (!value || Number.isNaN(d.getTime())) return "—";
  return d.toLocaleDateString("id-ID", { day: "numeric", month: "long", year: "numeric" });
}

type Tone = "success" | "warning" | "danger" | "neutral" | "accent";

// Customers do not need the admin-side steps: paid, provisioning and a failed
// attempt all mean "we are setting it up".
function statusView(s: PortalHostingInstance["status"]): { label: string; tone: Tone } {
  switch (s) {
    case "awaiting_payment":
      return { label: "Menunggu pembayaran", tone: "warning" };
    case "paid":
    case "provisioning":
    case "failed":
      return { label: "Sedang disiapkan", tone: "accent" };
    case "active":
      return { label: "Aktif", tone: "success" };
    case "expired":
      return { label: "Kedaluwarsa", tone: "danger" };
    case "stopped":
      return { label: "Dihentikan", tone: "neutral" };
    default:
      return { label: s, tone: "neutral" };
  }
}

function safeHttpUrl(link: string | undefined): string | null {
  if (!link) return null;
  try {
    const proto = new URL(link).protocol;
    return proto === "http:" || proto === "https:" ? link : null;
  } catch {
    return null;
  }
}

export function PortalHostingPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const termsId = useId();
  const [params] = useSearchParams();
  const returnStatus = params.get("status");

  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const hosting = useQuery({
    queryKey: HOSTING_QK,
    queryFn: fetchPortalHosting,
    retry: false,
    enabled: !!status?.has_key,
  });

  const [accepted, setAccepted] = useState(false);
  const [termsNotice, setTermsNotice] = useState<string | null>(null);
  const siteKey = useTurnstileSiteKey();
  const [turnstileToken, setTurnstileToken] = useState("");
  const [resetSignal, setResetSignal] = useState(0);

  // Back from the gateway: refresh so the paid instance shows its new state.
  useEffect(() => {
    if (returnStatus === "success" || returnStatus === "cancel") {
      qc.invalidateQueries({ queryKey: HOSTING_QK });
    }
  }, [returnStatus, qc]);

  const order = useMutation({
    mutationFn: (instanceId?: string) =>
      createPortalHostingOrder({
        instance_id: instanceId,
        accept_terms: accepted,
        terms_version: hosting.data?.terms_version ?? "",
        idempotency_key: crypto.randomUUID(),
        turnstile_token: turnstileToken || undefined,
      }),
    onSuccess: (o) => {
      const link = safeHttpUrl(o.payment_link_url);
      if (link) {
        window.location.href = link;
      } else {
        toast.error("Link pembayaran tidak valid");
      }
    },
    onError: (e: Error) => {
      setTurnstileToken("");
      setResetSignal((n) => n + 1);
      const code = e instanceof PortalAPIError ? e.code : undefined;
      if (code === "terms_outdated") {
        setAccepted(false);
        setTermsNotice("Ketentuan layanan hosting baru saja diperbarui. Baca lagi ketentuannya, lalu centang persetujuan untuk melanjutkan.");
        qc.invalidateQueries({ queryKey: HOSTING_QK });
        return;
      }
      if (code === "terms_required") {
        setAccepted(false);
        setTermsNotice("Centang persetujuan Ketentuan Layanan Hosting 9router sebelum memesan.");
        return;
      }
      if (e instanceof PortalAPIError && e.status === 503) {
        toast.error("Hosting belum tersedia", "Pemesanan hosting sedang ditutup. Coba lagi nanti.");
        qc.invalidateQueries({ queryKey: HOSTING_QK });
        return;
      }
      toast.error("Pembayaran gagal dibuat", e.message);
    },
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Gagal memuat status portal" />;

  if (status.has_key === false) {
    return (
      <div className="space-y-6">
        <PageHeader icon={Server} title="Hosting 9router" />
        <EmptyState
          variant="card"
          icon={Server}
          title="Belum ada API key"
          hint="Buat API key dulu untuk memesan hosting 9router."
          action={<Link to={portal("/key")} className="text-[13px] font-medium text-link hover:underline">Buat API key</Link>}
        />
      </div>
    );
  }

  if (hosting.isLoading) return <Spinner />;
  if (hosting.isError || !hosting.data) {
    const msg = hosting.error instanceof Error ? hosting.error.message : "Gagal memuat data hosting";
    return <ErrorCard message={msg} />;
  }

  const cfg = hosting.data;
  const instances = cfg.instances ?? [];
  const termsUrl = cfg.terms_url || "/ketentuan/hosting-9router";
  const periodDays = cfg.period_days || 30;
  const canOrder = cfg.enabled && cfg.price_idr > 0;
  const ready = canOrder && accepted && !order.isPending && (!siteKey || !!turnstileToken);
  const pendingFor = order.isPending ? (order.variables ?? "") : null;

  const blockedReason = !canOrder
    ? "Pemesanan hosting sedang ditutup."
    : !accepted
      ? "Centang persetujuan ketentuan dulu."
      : siteKey && !turnstileToken
        ? "Selesaikan verifikasi di atas dulu."
        : undefined;

  return (
    <div className="space-y-6">
      <PageHeader
        icon={Server}
        title="Hosting 9router"
        subtitle="9router siap pakai di server kami, dengan subdomain HTTPS sendiri."
      />

      {returnStatus === "success" && (
        <div role="status" className="flex items-start gap-3 rounded-2xl border border-ok/30 bg-ok/10 px-4 py-3">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-ok" strokeWidth={1.75} aria-hidden="true" />
          <p className="text-[13px] text-fg">
            Pembayaran diterima. Instance Anda akan disiapkan admin paling lambat 1x24 jam pada hari kerja.
          </p>
        </div>
      )}
      {returnStatus === "cancel" && (
        <div role="status" className="flex items-start gap-3 rounded-2xl border border-line bg-subtle px-4 py-3">
          <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-fg-muted" strokeWidth={1.75} aria-hidden="true" />
          <p className="text-[13px] text-fg-muted">Pembayaran dibatalkan, tidak ada biaya yang ditagih.</p>
        </div>
      )}

      <PanelCard icon={CreditCard} title="Pesan hosting">
        <div className="grid grid-cols-1 lg:grid-cols-[1fr_340px]">
          <div className="space-y-4 p-4 sm:p-5">
            <ul className="list-disc space-y-1.5 pl-5 text-[13px] text-fg-muted">
              <li>Satu container 9router di subdomain HTTPS acak milik Anda sendiri.</li>
              <li>Masa aktif {periodDays} hari per pembayaran. Tidak ada perpanjangan otomatis.</li>
              <li>Instance dibuat manual oleh admin setelah pembayaran berhasil, paling lambat 1x24 jam pada hari kerja.</li>
              <li>Masa {periodDays} hari dihitung sejak instance aktif, bukan sejak pembayaran.</li>
            </ul>

            <div className="rounded-xl border border-line bg-subtle px-3.5 py-3">
              <label htmlFor={termsId} className="flex cursor-pointer items-start gap-2.5 text-[13px] text-fg">
                <input
                  id={termsId}
                  type="checkbox"
                  className="mt-0.5 h-4 w-4 shrink-0 rounded border-input accent-accent-500"
                  checked={accepted}
                  onChange={(e) => {
                    setAccepted(e.target.checked);
                    if (e.target.checked) setTermsNotice(null);
                  }}
                />
                <span>
                  Saya telah membaca dan menyetujui{" "}
                  <a href={termsUrl} target="_blank" rel="noopener noreferrer" className="font-medium text-link hover:underline">
                    Ketentuan Layanan Hosting 9router
                  </a>
                  .
                </span>
              </label>
              <p className="mt-1.5 pl-[26px] text-[12px] text-fg-muted">
                Berlaku untuk pesanan baru dan perpanjangan. Versi {cfg.terms_version}.
              </p>
            </div>

            {termsNotice && (
              <div role="alert" className="flex items-start gap-2.5 rounded-xl border border-warn/30 bg-warn/10 px-3.5 py-2.5">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
                <p className="text-[13px] text-fg">{termsNotice}</p>
              </div>
            )}
          </div>

          <div className="flex flex-col border-t border-line bg-subtle p-4 sm:p-5 lg:border-l lg:border-t-0">
            <dl className="space-y-1">
              <dt className="text-[13px] text-fg-muted">Harga per {periodDays} hari</dt>
              <dd className="text-[22px] font-semibold tabular-nums text-fg">{cfg.price_idr > 0 ? formatIDR(cfg.price_idr) : "—"}</dd>
            </dl>
            {!canOrder && (
              <p className="mt-2 text-[12.5px] text-fg-muted" role="status">
                Pemesanan hosting sedang ditutup. Instance yang sudah ada tetap tampil di bawah.
              </p>
            )}
            {canOrder && (
              <TurnstileWidget
                siteKey={siteKey}
                onToken={setTurnstileToken}
                resetSignal={resetSignal}
                className="mt-4 flex justify-center"
              />
            )}
            <Button className="mt-4 w-full" onClick={() => order.mutate(undefined)} disabled={!ready} title={blockedReason}>
              {pendingFor === "" ? <Loader2 className="animate-spin" aria-hidden="true" /> : <CreditCard aria-hidden="true" />}
              {pendingFor === "" ? "Membuka pembayaran…" : "Pesan sekarang"}
            </Button>
          </div>
        </div>
      </PanelCard>

      <PanelCard icon={Server} title="Instance saya" count={instances.length}>
        {instances.length === 0 ? (
          <EmptyState title="Belum ada instance" hint="Instance yang Anda pesan akan muncul di sini." />
        ) : (
          <ul className="divide-y divide-line">
            {instances.map((inst) => (
              <InstanceRow
                key={inst.id}
                inst={inst}
                periodDays={periodDays}
                canRenew={ready}
                renewTitle={blockedReason}
                pending={pendingFor === inst.id}
                onRenew={() => order.mutate(inst.id)}
              />
            ))}
          </ul>
        )}
      </PanelCard>
    </div>
  );
}

function InstanceRow({
  inst,
  periodDays,
  canRenew,
  renewTitle,
  pending,
  onRenew,
}: {
  inst: PortalHostingInstance;
  periodDays: number;
  canRenew: boolean;
  renewTitle?: string;
  pending: boolean;
  onRenew: () => void;
}) {
  const view = statusView(inst.status);
  const url = safeHttpUrl(inst.url);
  const payLink = safeHttpUrl(inst.pending_payment_link);
  const awaiting = inst.status === "awaiting_payment";

  let period: string | null = null;
  if (inst.paid_until) {
    period = `${inst.status === "expired" ? "Berakhir" : "Aktif sampai"} ${formatDateLong(inst.paid_until)}`;
    if (inst.prepaid_days > 0) period += ` (+${inst.prepaid_days} hari berikutnya sudah dibayar)`;
  } else if (inst.prepaid_days > 0) {
    period = `${inst.prepaid_days} hari, dimulai saat instance aktif`;
  }

  return (
    <li className="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between sm:px-5">
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={view.tone}>{view.label}</Badge>
          {url ? (
            <a
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-w-0 items-center gap-1 break-all font-mono text-[13px] text-link hover:underline"
            >
              {url.replace(/^https?:\/\//, "")}
              <ExternalLink className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
            </a>
          ) : (
            <span className="text-[13px] text-fg-muted">
              {awaiting ? "Selesaikan pembayaran untuk memproses pesanan." : inst.status === "stopped" ? "Instance tidak berjalan." : "Alamat muncul setelah instance aktif."}
            </span>
          )}
        </div>
        {period && <p className="text-[12.5px] text-fg-muted">{period}</p>}
        {inst.has_password && <PasswordReveal instanceId={inst.id} />}
        {inst.status === "expired" && (
          <p className="text-[12.5px] text-fg-muted">Perpanjang untuk menambah {periodDays} hari. Data dapat dihapus 7 hari setelah tanggal berakhir.</p>
        )}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2">
        {payLink && (
          <a
            href={payLink}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          >
            <CreditCard className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            Lanjutkan pembayaran
          </a>
        )}
        {!(awaiting && payLink) && (
          <Button variant="secondary" onClick={onRenew} disabled={!canRenew} title={renewTitle}>
            {pending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
            {awaiting ? "Buat tagihan baru" : "Perpanjang"}
          </Button>
        )}
      </div>
    </li>
  );
}

/** Shows the instance's initial dashboard password on request. */
function PasswordReveal({ instanceId }: { instanceId: string }) {
  const toast = useToast();
  const [password, setPassword] = useState<string | null>(null);
  const [visible, setVisible] = useState(false);
  const reveal = useMutation({
    mutationFn: () => fetchPortalHostingPassword(instanceId),
    onSuccess: (pw) => {
      setPassword(pw);
      setVisible(true);
    },
    onError: () => toast.error("Gagal memuat password", "Coba lagi sebentar lagi."),
  });

  if (password === null) {
    return (
      <button
        type="button"
        onClick={() => reveal.mutate()}
        disabled={reveal.isPending}
        className="inline-flex items-center gap-1.5 text-[12.5px] font-medium text-link hover:underline disabled:opacity-60 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 rounded"
      >
        {reveal.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" /> : <KeyRound className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />}
        Lihat password awal dashboard
      </button>
    );
  }
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
        <span className="text-fg-muted">Password awal:</span>
        <code className="rounded bg-hover px-1.5 py-0.5 font-mono text-[12.5px] text-fg">{visible ? password : "••••••••••••••••••••"}</code>
        <button
          type="button"
          onClick={() => setVisible((v) => !v)}
          aria-label={visible ? "Sembunyikan password" : "Tampilkan password"}
          className="inline-flex h-7 w-7 items-center justify-center rounded text-fg-muted hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
        >
          {visible ? <EyeOff className="h-3.5 w-3.5" aria-hidden="true" /> : <Eye className="h-3.5 w-3.5" aria-hidden="true" />}
        </button>
        <button
          type="button"
          onClick={() => {
            void navigator.clipboard?.writeText(password).then(
              () => toast.success("Password disalin"),
              () => toast.error("Gagal menyalin password"),
            );
          }}
          aria-label="Salin password"
          className="inline-flex h-7 w-7 items-center justify-center rounded text-fg-muted hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
        >
          <Copy className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </div>
      <p className="text-[12px] text-fg-muted">Segera ganti password ini dari dashboard 9router setelah login pertama.</p>
    </div>
  );
}
