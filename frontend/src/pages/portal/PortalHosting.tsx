import { useEffect, useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, CheckCircle2, Clock3, MapPin, Copy, CreditCard, ExternalLink, Eye, EyeOff, Loader2, MessageCircle, RefreshCw, Server, Ticket, X, XCircle } from "lucide-react";
import {
  checkPortalHostingCoupon,
  createPortalHostingOrder,
  fetchPortalHosting,
  fetchPortalHostingPassword,
  fetchPortalStatus,
  PortalAPIError,
  type HostingCouponCheck,
  type HostingRegionOffer,
  type PortalHostingInstance,
} from "../../lib/api";
import { Badge, Button, EmptyState, ErrorCard, Spinner } from "../../components/ui";
import { TurnstileWidget, useTurnstileSiteKey } from "../../components/TurnstileWidget";
import { useToast } from "../../components/Toast";
import { portal } from "../../lib/portalRoutes";
import { safeHttpUrl } from "../../lib/safeUrl";
import { cn } from "@/lib/utils";
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

// waitingActivation: paid, but the admin has not finished creating it yet.
// Customers do not need the admin-side steps (paid, provisioning, a failed
// attempt); they all mean "waiting for activation".
function waitingActivation(s: PortalHostingInstance["status"]): boolean {
  return s === "paid" || s === "provisioning" || s === "failed";
}

function statusView(s: PortalHostingInstance["status"]): { label: string; tone: Tone } {
  switch (s) {
    case "awaiting_payment":
      return { label: "Menunggu pembayaran", tone: "warning" };
    case "paid":
    case "provisioning":
    case "failed":
      return { label: "Menunggu aktivasi", tone: "accent" };
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


export function PortalHostingPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const termsId = useId();
  const [params] = useSearchParams();
  const returnStatus = params.get("status");
  const wantedRegion = params.get("region");

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
  const [couponInput, setCouponInput] = useState("");
  const [coupon, setCoupon] = useState<Extract<HostingCouponCheck, { valid: true }> | null>(null);
  const [couponError, setCouponError] = useState<string | null>(null);
  const [pickedRegion, setPickedRegion] = useState<string | null>(null);

  // The region asked for in the link, else the first one with stock.
  const regionOffers = hosting.data?.regions ?? [];
  const regionId =
    pickedRegion ??
    regionOffers.find((r) => r.id === wantedRegion && !r.sold_out)?.id ??
    regionOffers.find((r) => !r.sold_out)?.id ??
    regionOffers[0]?.id ??
    "";
  const region = regionOffers.find((r) => r.id === regionId) ?? null;

  const checkCoupon = useMutation({
    mutationFn: (code: string) => checkPortalHostingCoupon(code, { region: regionId || undefined }),
    onSuccess: (res) => {
      if (res.valid) {
        setCoupon(res);
        setCouponError(null);
      } else {
        setCoupon(null);
        setCouponError(res.message);
      }
    },
    onError: (e: Error) => {
      setCoupon(null);
      setCouponError(e instanceof PortalAPIError && e.status === 429 ? "Terlalu banyak percobaan. Tunggu 1 menit." : "Gagal mengecek kupon. Coba lagi.");
    },
  });
  const clearCoupon = () => {
    setCoupon(null);
    setCouponError(null);
    setCouponInput("");
  };

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
        coupon_code: coupon?.coupon_code,
        region: instanceId ? undefined : regionId || undefined,
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
      if (code === "region_sold_out" || code === "region_unavailable") {
        toast.error("Region tidak tersedia", e.message);
        qc.invalidateQueries({ queryKey: HOSTING_QK });
        return;
      }
      if (code?.startsWith("coupon_")) {
        setCoupon(null);
        setCouponError(e.message);
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
  // Renewing an existing instance needs no stock; a new order needs a region
  // that is not sold out.
  const canRenewAny = cfg.enabled;
  const canOrder = cfg.enabled && !!region && !region.sold_out && region.price_idr > 0;
  const formReady = accepted && !order.isPending && (!siteKey || !!turnstileToken);
  const ready = canOrder && formReady;
  const pendingFor = order.isPending ? (order.variables ?? "") : null;
  const supportUrl = safeHttpUrl(cfg.support_url);
  const basePrice = region?.price_idr ?? 0;
  const finalPrice = coupon ? coupon.final_idr : basePrice;

  const formBlocked = !accepted
    ? "Centang persetujuan ketentuan dulu."
    : siteKey && !turnstileToken
      ? "Selesaikan verifikasi di atas dulu."
      : undefined;
  const blockedReason = !cfg.enabled
    ? "Pemesanan hosting sedang ditutup."
    : region?.sold_out
      ? `Stok region ${region.name} sedang habis.`
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
        <div role="status" className="flex flex-col gap-3 rounded-2xl border border-ok/30 bg-ok/10 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-ok" strokeWidth={1.75} aria-hidden="true" />
            <p className="text-[13px] text-fg">
              Pembayaran diterima. Instance Anda berstatus <strong>Menunggu aktivasi</strong> dan akan diaktifkan admin paling lambat 1x24 jam pada hari kerja.
            </p>
          </div>
          {supportUrl && <SupportLink href={supportUrl} />}
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

            <CouponField
              value={couponInput}
              onChange={(v) => {
                setCouponInput(v);
                setCouponError(null);
              }}
              applied={coupon}
              error={couponError}
              checking={checkCoupon.isPending}
              disabled={!canOrder}
              onCheck={() => {
                const code = couponInput.trim();
                if (!code) {
                  setCouponError("Masukkan kode kupon.");
                  return;
                }
                checkCoupon.mutate(code);
              }}
              onClear={clearCoupon}
            />

            {termsNotice && (
              <div role="alert" className="flex items-start gap-2.5 rounded-xl border border-warn/30 bg-warn/10 px-3.5 py-2.5">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
                <p className="text-[13px] text-fg">{termsNotice}</p>
              </div>
            )}
          </div>

          <div className="flex flex-col border-t border-line bg-subtle p-4 sm:p-5 lg:border-l lg:border-t-0">
            {regionOffers.length > 0 && (
              <RegionPicker
                regions={regionOffers}
                value={regionId}
                periodDays={periodDays}
                disabled={!cfg.enabled || order.isPending}
                onChange={(id) => {
                  if (id === regionId) return;
                  setPickedRegion(id);
                  // A coupon was priced for the old region; check it again.
                  setCoupon(null);
                  setCouponError(null);
                }}
              />
            )}
            <dl className="mt-4 space-y-1">
              <dt className="text-[13px] text-fg-muted">
                Harga per {periodDays} hari{coupon?.bonus_days ? ` + ${coupon.bonus_days} hari bonus` : ""}
              </dt>
              <dd className="flex flex-wrap items-baseline gap-x-2">
                <span className="text-[22px] font-semibold tabular-nums text-fg">{basePrice > 0 ? formatIDR(finalPrice) : "—"}</span>
                {coupon && coupon.discount_idr > 0 && (
                  <span className="text-[13px] tabular-nums text-fg-muted line-through">{formatIDR(coupon.base_idr)}</span>
                )}
              </dd>
              {coupon && (
                <dd className="text-[12.5px] text-ok">
                  Kupon {coupon.coupon_code}:{" "}
                  {coupon.discount_idr > 0 ? `hemat ${formatIDR(coupon.discount_idr)}` : `masa aktif ${periodDays + coupon.bonus_days} hari`}
                </dd>
              )}
            </dl>
            {!cfg.enabled ? (
              <p className="mt-2 text-[12.5px] text-fg-muted" role="status">
                Pemesanan hosting sedang ditutup. Instance yang sudah ada tetap tampil di bawah.
              </p>
            ) : region?.sold_out ? (
              <p className="mt-2 text-[12.5px] text-fg-muted" role="status">
                Stok {region.name} sedang habis. Pilih region lain, atau cek lagi nanti. Perpanjangan instance tetap bisa.
              </p>
            ) : null}
            {canRenewAny && (
              <TurnstileWidget
                siteKey={siteKey}
                onToken={setTurnstileToken}
                resetSignal={resetSignal}
                className="mt-4 flex justify-center"
              />
            )}
            <Button className="mt-4 w-full" onClick={() => order.mutate(undefined)} disabled={!ready} title={blockedReason}>
              {pendingFor === "" ? <Loader2 className="animate-spin" aria-hidden="true" /> : <CreditCard aria-hidden="true" />}
              {pendingFor === "" ? "Membuka pembayaran…" : region?.sold_out ? "Stok habis" : "Pesan sekarang"}
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
                canRenew={canRenewAny && formReady}
                renewTitle={!cfg.enabled ? "Pemesanan hosting sedang ditutup." : formBlocked}
                pending={pendingFor === inst.id}
                onRenew={() => order.mutate(inst.id)}
                supportUrl={supportUrl}
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
  supportUrl,
}: {
  inst: PortalHostingInstance;
  periodDays: number;
  canRenew: boolean;
  renewTitle?: string;
  pending: boolean;
  onRenew: () => void;
  supportUrl: string | null;
}) {
  const view = statusView(inst.status);
  const url = safeHttpUrl(inst.url);
  const payLink = safeHttpUrl(inst.pending_payment_link);
  const awaiting = inst.status === "awaiting_payment";
  const waiting = waitingActivation(inst.status);

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
              {awaiting
                ? "Selesaikan pembayaran untuk memproses pesanan."
                : waiting
                  ? "Pembayaran diterima. Instance sedang menunggu aktivasi oleh admin."
                  : inst.status === "stopped"
                    ? "Instance tidak berjalan."
                    : "Alamat muncul setelah instance aktif."}
            </span>
          )}
        </div>
        {waiting && (
          <p className="flex items-start gap-1.5 text-[12.5px] text-fg-muted">
            <Clock3 className="mt-0.5 h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
            Aktivasi paling lambat 1x24 jam pada hari kerja. URL dan password muncul di sini setelah aktif.
          </p>
        )}
        {inst.region_name && (
          <p className="flex items-center gap-1.5 text-[12.5px] text-fg-muted">
            <MapPin className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
            {inst.region_name}
            {inst.price_idr > 0 && ` · ${formatIDR(inst.price_idr)} per ${periodDays} hari`}
          </p>
        )}
        {period && <p className="text-[12.5px] text-fg-muted">{period}</p>}
        {inst.has_password && inst.status === "active" && <PasswordReveal instanceId={inst.id} />}
        {inst.status === "expired" && (
          <p className="text-[12.5px] text-fg-muted">Perpanjang untuk menambah {periodDays} hari. Data dapat dihapus 7 hari setelah tanggal berakhir.</p>
        )}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2">
        {waiting && supportUrl && <SupportLink href={supportUrl} />}
        {payLink && (
          <a
            href={payLink}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          >
            <CreditCard className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            Lanjutkan pembayaran
          </a>
        )}
        {!(awaiting && payLink) && !waiting && (
          <Button variant="secondary" onClick={onRenew} disabled={!canRenew} title={renewTitle}>
            {pending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
            {awaiting ? "Buat tagihan baru" : "Perpanjang"}
          </Button>
        )}
      </div>
    </li>
  );
}

/** Radio cards for the region of a new instance, with price and stock. */
function RegionPicker({
  regions,
  value,
  periodDays,
  disabled,
  onChange,
}: {
  regions: HostingRegionOffer[];
  value: string;
  periodDays: number;
  disabled: boolean;
  onChange: (id: string) => void;
}) {
  const name = useId();
  return (
    <fieldset>
      <legend className="text-[13px] font-medium text-fg">Region</legend>
      <div className="mt-2 grid grid-cols-1 gap-2">
        {regions.map((r) => {
          const checked = r.id === value;
          const stock = r.sold_out ? "Habis" : r.stock < 0 ? "Tersedia" : `Sisa ${r.stock}`;
          return (
            <label
              key={r.id}
              className={cn(
                "flex cursor-pointer items-center gap-3 rounded-xl border bg-surface px-3 py-2.5 transition-colors focus-within:ring-2 focus-within:ring-accent-500",
                checked ? "border-accent-500" : "border-line hover:bg-hover",
                (disabled || r.sold_out) && "cursor-not-allowed opacity-60",
              )}
            >
              <input
                type="radio"
                name={name}
                value={r.id}
                checked={checked}
                disabled={disabled || r.sold_out}
                onChange={() => onChange(r.id)}
                className="h-4 w-4 shrink-0 accent-accent-500"
              />
              <span className="min-w-0 flex-1">
                <span className="block text-[13px] font-medium text-fg">{r.name}</span>
                <span className="block text-[12px] tabular-nums text-fg-muted">
                  {formatIDR(r.price_idr)} / {periodDays} hari
                </span>
              </span>
              <span className={cn("shrink-0 text-[12px] tabular-nums", r.sold_out ? "text-bad" : "text-fg-muted")}>{stock}</span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}

/** Contact button for customers waiting on activation. */
function SupportLink({ href }: { href: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    >
      <MessageCircle className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
      Hubungi admin
    </a>
  );
}

/** Coupon code input with a "check" button and the check result. */
function CouponField({
  value,
  onChange,
  applied,
  error,
  checking,
  disabled,
  onCheck,
  onClear,
}: {
  value: string;
  onChange: (v: string) => void;
  applied: Extract<HostingCouponCheck, { valid: true }> | null;
  error: string | null;
  checking: boolean;
  disabled: boolean;
  onCheck: () => void;
  onClear: () => void;
}) {
  const inputId = useId();
  const msgId = useId();
  if (applied) {
    return (
      <div className="flex items-center justify-between gap-3 rounded-xl border border-ok/30 bg-ok/10 px-3.5 py-2.5" role="status">
        <div className="flex min-w-0 items-center gap-2 text-[13px] text-fg">
          <Ticket className="h-4 w-4 shrink-0 text-ok" strokeWidth={1.75} aria-hidden="true" />
          <span className="min-w-0">
            Kupon <span className="font-mono font-medium">{applied.coupon_code}</span> terpasang:{" "}
            {applied.kind === "bonus_days"
              ? `bonus ${applied.bonus_days} hari masa aktif`
              : applied.discount_type === "percent"
                ? `diskon ${applied.discount_value}%`
                : `potongan ${formatIDR(applied.discount_idr)}`}
          </span>
        </div>
        <button
          type="button"
          onClick={onClear}
          aria-label="Hapus kupon"
          className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-fg-muted hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
        >
          <X className="h-4 w-4" aria-hidden="true" />
        </button>
      </div>
    );
  }
  return (
    <div className="space-y-1.5">
      <label htmlFor={inputId} className="text-[13px] font-medium text-fg">
        Kode kupon <span className="font-normal text-fg-muted">(opsional)</span>
      </label>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          onCheck();
        }}
      >
        <input
          id={inputId}
          value={value}
          onChange={(e) => onChange(e.target.value.toUpperCase())}
          placeholder="KODEKUPON"
          maxLength={32}
          autoComplete="off"
          spellCheck={false}
          disabled={disabled}
          aria-invalid={!!error}
          aria-describedby={error ? msgId : undefined}
          className="h-9 min-w-0 flex-1 rounded-lg border border-input bg-surface px-3 font-mono text-[13px] uppercase text-fg placeholder:text-fg-faint focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-60"
        />
        <Button type="submit" variant="secondary" disabled={disabled || checking}>
          {checking ? <Loader2 className="animate-spin" aria-hidden="true" /> : null}
          Cek
        </Button>
      </form>
      {error && (
        <p id={msgId} role="alert" className="text-[12.5px] text-bad">
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * The instance's initial dashboard password, hidden until asked for, like an
 * API key. It is fetched on first reveal or copy and kept only in state.
 */
function PasswordReveal({ instanceId }: { instanceId: string }) {
  const toast = useToast();
  const [password, setPassword] = useState<string | null>(null);
  const [visible, setVisible] = useState(false);
  const load = useMutation({
    mutationFn: () => fetchPortalHostingPassword(instanceId),
    onError: () => toast.error("Gagal memuat password", "Coba lagi sebentar lagi."),
  });

  const ensure = async (): Promise<string | null> => {
    if (password !== null) return password;
    try {
      const pw = await load.mutateAsync();
      setPassword(pw);
      return pw;
    } catch {
      return null;
    }
  };

  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
        <span className="text-fg-muted">Password:</span>
        <code className="rounded bg-hover px-1.5 py-0.5 font-mono text-[12.5px] text-fg">
          {visible && password !== null ? password : "••••••••••••"}
        </code>
        <button
          type="button"
          disabled={load.isPending}
          onClick={async () => {
            if (visible) {
              setVisible(false);
              return;
            }
            if (await ensure()) setVisible(true);
          }}
          aria-label={visible ? "Sembunyikan password" : "Tampilkan password"}
          className="inline-flex h-7 w-7 items-center justify-center rounded text-fg-muted hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-60"
        >
          {load.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          ) : visible ? (
            <EyeOff className="h-3.5 w-3.5" aria-hidden="true" />
          ) : (
            <Eye className="h-3.5 w-3.5" aria-hidden="true" />
          )}
        </button>
        <button
          type="button"
          disabled={load.isPending}
          onClick={async () => {
            const pw = await ensure();
            if (!pw) return;
            navigator.clipboard?.writeText(pw).then(
              () => toast.success("Password disalin"),
              () => toast.error("Gagal menyalin password"),
            );
          }}
          aria-label="Salin password"
          className="inline-flex h-7 w-7 items-center justify-center rounded text-fg-muted hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:opacity-60"
        >
          <Copy className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </div>
      <p className="text-[12px] text-fg-muted">Segera ganti password ini dari dashboard 9router setelah login pertama.</p>
    </div>
  );
}
