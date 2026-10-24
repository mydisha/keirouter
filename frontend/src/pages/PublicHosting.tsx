// Hosting 9router landing at `/9router`. Shows each region's price
// and live stock from /v1/public/hosting; "Pesan" sends the visitor to the
// portal hosting page with the region preselected. A sold-out region (or
// closed ordering) cannot be ordered.

import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight, Clock3, CreditCard, Database, Globe2, KeyRound, LockKeyhole, MapPin, Server, ShieldCheck,
  type LucideIcon,
} from "lucide-react";
import { PublicLayout, TELEGRAM_URL } from "../components/PublicLayout";
import { fetchPublicHosting, type PublicHostingRegion } from "../lib/publicApi";

const FOCUS = "focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg)]";

function formatIDR(n: number): string {
  return new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", maximumFractionDigits: 0 }).format(n);
}

function stockLabel(r: PublicHostingRegion, open: boolean): string {
  if (!open) return "Pemesanan ditutup";
  if (r.sold_out) return "Stok habis";
  if (r.stock < 0) return "Tersedia";
  return `Sisa ${r.stock} slot`;
}

const FEATURES: { icon: LucideIcon; title: string; body: string }[] = [
  { icon: Globe2, title: "Subdomain HTTPS sendiri", body: "Setiap instance mendapat subdomain acak dengan sertifikat HTTPS, siap dipakai tanpa atur DNS." },
  { icon: Database, title: "Data tidak hilang", body: "Database, provider, dan API key di 9router tersimpan di volume sendiri dan tetap ada saat instance di-restart." },
  { icon: LockKeyhole, title: "Password hanya untukmu", body: "Password awal dashboard tersimpan terenkripsi dan hanya bisa dilihat dari portal akunmu." },
  { icon: ShieldCheck, title: "Container terpisah", body: "Satu pelanggan satu container. Instance lain tidak bisa membaca data milikmu." },
];

const STEPS: { icon: LucideIcon; title: string; body: string }[] = [
  { icon: MapPin, title: "Pilih region", body: "Pilih negara server yang paling dekat dengan penggunamu. Harga bisa berbeda tiap negara." },
  { icon: CreditCard, title: "Bayar lewat QRIS", body: "Login portal dengan Google, setujui ketentuan, lalu bayar dari aplikasi bank atau e-wallet." },
  { icon: Clock3, title: "Instance diaktifkan", body: "Admin membuat instance paling lambat 1x24 jam pada hari kerja. URL dan password muncul di portal." },
];

export default function PublicHosting() {
  const q = useQuery({ queryKey: ["public-hosting"], queryFn: fetchPublicHosting, refetchInterval: 60_000, retry: 1 });
  const data = q.data;
  const open = !!data?.enabled;
  const regions = data?.regions ?? [];
  const periodDays = data?.period_days || 30;
  const termsUrl = data?.terms_url || "/ketentuan#hosting";
  const cheapest = regions.reduce<number | null>((min, r) => (min === null || r.price_idr < min ? r.price_idr : min), null);

  return (
    <PublicLayout>
      <section className="mx-auto max-w-3xl text-center">
        <span className="inline-flex items-center gap-2 rounded-full border border-[var(--line)] bg-[var(--paper)] px-3 py-1 text-[12px] text-[var(--muted)]">
          <Server className="h-3.5 w-3.5 text-[var(--green)]" strokeWidth={1.75} aria-hidden="true" />
          Hosting 9router
        </span>
        <h1 className="mt-5 text-[34px] font-semibold leading-[1.08] tracking-[-1.2px] text-[var(--ink)] sm:text-[46px]">
          9router siap pakai,{" "}
          <span className="text-[var(--green)]">tanpa urus server.</span>
        </h1>
        <p className="mx-auto mt-5 max-w-xl text-[16px] leading-relaxed text-[var(--muted)]">
          Kami jalankan 9router di server kami dengan subdomain HTTPS milikmu sendiri.
          {cheapest !== null ? ` Mulai ${formatIDR(cheapest)} per ${periodDays} hari.` : ""}
        </p>
      </section>

      <section className="mt-12" aria-labelledby="regions-title">
        <h2 id="regions-title" className="sr-only">Pilih region</h2>
        {q.isLoading ? (
          <div className="mx-auto grid max-w-3xl gap-4 sm:grid-cols-2" aria-busy="true" aria-label="Memuat region">
            {[0, 1].map((i) => (
              <div key={i} className="h-[248px] animate-pulse rounded-xl border border-[var(--line)] bg-[var(--paper)]" />
            ))}
          </div>
        ) : regions.length === 0 ? (
          <p className="mx-auto max-w-3xl rounded-xl border border-dashed border-[var(--line)] p-8 text-center text-[14px] text-[var(--muted)]">
            {q.isError ? "Data region gagal dimuat. Coba muat ulang halaman." : "Pemesanan hosting sedang ditutup. Pantau pengumuman di Telegram."}
          </p>
        ) : (
          <ul className="mx-auto grid max-w-3xl gap-4 sm:grid-cols-2">
            {regions.map((r) => (
              <RegionCard key={r.id} region={r} open={open} periodDays={periodDays} />
            ))}
          </ul>
        )}
        <p className="mx-auto mt-4 max-w-3xl text-center text-[12.5px] text-[var(--muted)]">
          Stok diperbarui otomatis. Dengan memesan, kamu menyetujui{" "}
          <a href={termsUrl} className="text-[var(--ink)] underline underline-offset-2 hover:opacity-80">
            Ketentuan Layanan Hosting 9router
          </a>
          .
        </p>
      </section>

      <section className="mt-20">
        <SectionTitle eyebrow="Cara pesan" title="Tiga langkah sampai 9router-mu aktif" />
        <ol className="mt-8 grid gap-4 md:grid-cols-3">
          {STEPS.map((s, i) => (
            <li key={s.title} className="rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5">
              <div className="flex items-center justify-between">
                <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--accent-bg)] text-[var(--green)]">
                  <s.icon className="h-4.5 w-4.5" strokeWidth={1.75} aria-hidden="true" />
                </span>
                <span className="font-mono text-[12px] text-[var(--muted)]">0{i + 1}</span>
              </div>
              <h3 className="mt-4 text-[15px] font-semibold text-[var(--ink)]">{s.title}</h3>
              <p className="mt-1.5 text-[14px] leading-relaxed text-[var(--muted)]">{s.body}</p>
            </li>
          ))}
        </ol>
      </section>

      <section className="mt-20">
        <SectionTitle eyebrow="Yang kamu dapat" title="Satu container 9router, lengkap" />
        <div className="mt-8 grid gap-px overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--line)] sm:grid-cols-2 lg:grid-cols-4">
          {FEATURES.map((f) => (
            <div key={f.title} className="bg-[var(--paper)] p-5">
              <f.icon className="h-5 w-5 text-[var(--green)]" strokeWidth={1.75} aria-hidden="true" />
              <h3 className="mt-3 text-[15px] font-semibold text-[var(--ink)]">{f.title}</h3>
              <p className="mt-1.5 text-[14px] leading-relaxed text-[var(--muted)]">{f.body}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="mt-20 rounded-xl border border-[var(--line)] bg-[var(--paper)] p-6 text-center sm:p-8">
        <KeyRound className="mx-auto h-6 w-6 text-[var(--green)]" strokeWidth={1.75} aria-hidden="true" />
        <h2 className="mt-3 text-[20px] font-semibold tracking-[-0.3px] text-[var(--ink)]">Ada pertanyaan sebelum memesan?</h2>
        <p className="mx-auto mt-2 max-w-md text-[14px] leading-relaxed text-[var(--muted)]">
          Tanyakan di komunitas Telegram. Masa aktif {periodDays} hari dihitung sejak instance aktif, dan tidak ada perpanjangan otomatis.
        </p>
        <a
          href={TELEGRAM_URL}
          target="_blank"
          rel="noopener noreferrer"
          className={`mt-5 inline-flex h-10 items-center justify-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--bg)] px-4 text-[14px] font-medium text-[var(--ink)] no-underline transition-colors hover:border-[var(--line-strong)] ${FOCUS}`}
        >
          Gabung Telegram
          <ArrowRight className="h-4 w-4" aria-hidden="true" />
        </a>
      </section>
    </PublicLayout>
  );
}

function SectionTitle({ eyebrow, title }: { eyebrow: string; title: string }) {
  return (
    <div className="max-w-2xl">
      <p className="font-mono text-[11px] uppercase tracking-[1.2px] text-[var(--green)]">{eyebrow}</p>
      <h2 className="mt-2 text-[24px] font-semibold tracking-[-0.5px] text-[var(--ink)] sm:text-[28px]">{title}</h2>
    </div>
  );
}

function RegionCard({ region: r, open, periodDays }: { region: PublicHostingRegion; open: boolean; periodDays: number }) {
  const orderable = open && !r.sold_out;
  const lowStock = orderable && r.stock >= 0 && r.stock <= 3;
  return (
    <li className="flex flex-col rounded-xl border border-[var(--line)] bg-[var(--paper)] p-6">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--accent-bg)] text-[var(--green)]">
            <MapPin className="h-4.5 w-4.5" strokeWidth={1.75} aria-hidden="true" />
          </span>
          <h3 className="text-[17px] font-semibold text-[var(--ink)]">{r.name}</h3>
        </div>
        <span
          className={`inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[12px] font-medium ${
            orderable ? "border-[var(--line)] text-[var(--ink)]" : "border-[var(--line)] text-[var(--muted)]"
          }`}
        >
          <span
            className={`h-1.5 w-1.5 rounded-full ${orderable ? (lowStock ? "bg-amber-500" : "bg-[var(--ok)]") : "bg-[var(--muted)]"}`}
            aria-hidden="true"
          />
          {stockLabel(r, open)}
        </span>
      </div>
      <p className="mt-6 flex items-baseline gap-1.5">
        <span className="font-mono text-[32px] font-semibold leading-none tracking-[-0.8px] tabular-nums text-[var(--ink)]">{formatIDR(r.price_idr)}</span>
        <span className="text-[13px] text-[var(--muted)]">/ {periodDays} hari</span>
      </p>
      <ul className="mt-5 space-y-1.5 text-[13.5px] text-[var(--muted)]">
        <li>1 container 9router</li>
        <li>Subdomain HTTPS acak</li>
        <li>Volume data persisten</li>
      </ul>
      <div className="mt-auto pt-6">
        {orderable ? (
          <a
            href={`/portal/hosting?region=${encodeURIComponent(r.id)}`}
            className={`inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg bg-[var(--action)] px-5 text-[14px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 ${FOCUS}`}
          >
            Pesan {r.name}
            <ArrowRight className="h-4 w-4" aria-hidden="true" />
          </a>
        ) : (
          <span
            aria-disabled="true"
            className="inline-flex h-11 w-full cursor-not-allowed items-center justify-center rounded-lg border border-[var(--line)] bg-[var(--soft)] px-5 text-[14px] font-medium text-[var(--muted)]"
          >
            {open ? "Sold out" : "Belum dibuka"}
          </span>
        )}
      </div>
    </li>
  );
}
