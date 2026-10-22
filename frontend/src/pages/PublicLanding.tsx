// Public landing page — served at `/` by the router.
//
// Reads the two read-only public endpoints via TanStack Query. Every section
// degrades to a zeroed/empty state when a query is loading, errored, or returns
// nothing: no error cards, no blank screen.
//
// Styling is scoped through `.landing-root` tokens only (they mirror the
// dashboard design system); this module never imports lib/api.ts or the
// dashboard Layout.

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity, ArrowRight, Boxes, Check, Coins, Copy, Gauge, KeyRound, LogIn, Plug, Route, Send,
  SlidersHorizontal, Wallet,
  type LucideIcon,
} from "lucide-react";
import { fetchPublicOverview, fetchPublicModels } from "../lib/publicApi";
import { PublicLayout, TELEGRAM_URL } from "../components/PublicLayout";
import { ModelCard, fmtCount } from "../components/ModelCatalog";

const FOCUS = "focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg)]";

// Smoothly counts a displayed metric toward its latest value. Formatting stays
// with the callers' fmt helpers; this only animates the raw number.
function useCountUp(target: number, duration = 900): number {
  const [display, setDisplay] = useState(target);
  const displayRef = useRef(target);
  const rafRef = useRef<number | undefined>(undefined);
  useEffect(() => {
    const from = displayRef.current;
    if (target === from) return;
    const start = performance.now();
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / duration);
      const eased = 1 - Math.pow(1 - t, 3);
      const value = from + (target - from) * eased;
      displayRef.current = value;
      setDisplay(value);
      if (t < 1) rafRef.current = requestAnimationFrame(tick);
    };
    rafRef.current = requestAnimationFrame(tick);
    return () => { if (rafRef.current) cancelAnimationFrame(rafRef.current); };
  }, [target, duration]);
  return display;
}

function useCopy(): [string | null, (key: string, text: string) => void] {
  const [copied, setCopied] = useState<string | null>(null);
  const copy = async (key: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      setTimeout(() => setCopied((c) => (c === key ? null : c)), 1400);
    } catch {
      /* clipboard unavailable */
    }
  };
  return [copied, copy];
}

function Metric({ icon: Icon, label, value, hint }: { icon: LucideIcon; label: string; value: string; hint: string }) {
  return (
    <div className="bg-[var(--paper)] p-5">
      <div className="flex items-center gap-2 text-[12px] text-[var(--muted)]">
        <Icon className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        {label}
      </div>
      <p className="mt-2 font-mono text-[26px] font-semibold leading-none tracking-[-0.5px] tabular-nums text-[var(--ink)] [overflow-wrap:anywhere] sm:text-[22px] lg:text-[30px]">
        {value}
      </p>
      <p className="mt-2 text-[12px] text-[var(--muted)]">{hint}</p>
    </div>
  );
}

function SectionHeading({ eyebrow, title, children }: { eyebrow: string; title: string; children?: ReactNode }) {
  return (
    <div className="max-w-2xl">
      <p className="font-mono text-[11px] uppercase tracking-[1.2px] text-[var(--green)]">{eyebrow}</p>
      <h2 className="mt-2 text-[24px] font-semibold tracking-[-0.5px] text-[var(--ink)] sm:text-[28px]">{title}</h2>
      {children && <p className="mt-2 text-[15px] leading-relaxed text-[var(--muted)]">{children}</p>}
    </div>
  );
}

function QuickStart({ model }: { model: string }) {
  const [copied, copy] = useCopy();
  const baseURL = `${window.location.origin}/v1`;
  const snippet = `curl ${baseURL}/chat/completions \\
  -H "Authorization: Bearer $TOKENIZER_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${model}", "messages": [{"role": "user", "content": "Halo!"}]}'`;
  return (
    <div className="overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--terminal)] text-left shadow-[0_24px_60px_-28px_rgba(0,0,0,0.45)]">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2.5">
        <div className="flex items-center gap-1.5" aria-hidden="true">
          <span className="h-2.5 w-2.5 rounded-full bg-white/15" />
          <span className="h-2.5 w-2.5 rounded-full bg-white/15" />
          <span className="h-2.5 w-2.5 rounded-full bg-white/15" />
        </div>
        <span className="font-mono text-[11px] text-[var(--terminal-muted)]">OpenAI-compatible</span>
      </div>
      <div className="space-y-3 p-4">
        <div>
          <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--terminal-muted)]">Base URL</p>
          <button
            type="button"
            onClick={() => copy("base", baseURL)}
            className="mt-1.5 flex w-full items-center gap-2 rounded-md border border-white/10 bg-white/5 px-3 py-2 text-left transition-colors hover:bg-white/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent-400)]"
            aria-label="Salin base URL"
          >
            <span className="min-w-0 flex-1 truncate font-mono text-[13px] text-[var(--terminal-ink)]">{baseURL}</span>
            {copied === "base" ? <Check className="h-4 w-4 text-emerald-400" aria-hidden="true" /> : <Copy className="h-4 w-4 text-[var(--terminal-muted)]" aria-hidden="true" />}
          </button>
        </div>
        <div>
          <div className="flex items-center justify-between gap-2">
            <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--terminal-muted)]">Contoh request</p>
            <button
              type="button"
              onClick={() => copy("curl", snippet)}
              className="inline-flex h-7 items-center gap-1 rounded-md border border-white/10 bg-white/10 px-2 font-mono text-[11px] text-[var(--terminal-ink)] transition-colors hover:bg-white/20 focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent-400)]"
              aria-label="Salin contoh curl"
            >
              {copied === "curl" ? <Check className="h-3.5 w-3.5 text-emerald-400" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
              {copied === "curl" ? "Tersalin" : "Salin"}
            </button>
          </div>
          <pre className="mt-1.5 whitespace-pre-wrap rounded-md bg-black/30 p-3 font-mono text-[12px] leading-relaxed text-[var(--terminal-ink)] [overflow-wrap:anywhere]">
            <code>{snippet}</code>
          </pre>
        </div>
      </div>
    </div>
  );
}

const STEPS: { icon: LucideIcon; title: string; body: string }[] = [
  { icon: LogIn, title: "Login dengan Google", body: "Masuk ke portal dengan akun Google. API key-mu langsung tersedia di sana." },
  { icon: Wallet, title: "Isi saldo lewat QRIS", body: "Mulai dari Rp10.000. Saldo tidak hangus dan hanya berkurang saat kamu memakai model." },
  { icon: Plug, title: "Pasang di tools-mu", body: "Masukkan base URL dan API key ke Claude Code, Cursor, Cline, SDK OpenAI, atau aplikasi lain yang mendukung format OpenAI." },
];

const FEATURES: { icon: LucideIcon; title: string; body: string }[] = [
  { icon: Route, title: "Otomatis ke yang termurah", body: "Kami mengirim request-mu ke penyedia termurah saat itu, jadi kamu tidak perlu membandingkan harga sendiri. Kalau penyedia itu penuh, kami alihkan ke penyedia termurah berikutnya." },
  { icon: SlidersHorizontal, title: "Batas harga kamu yang atur", body: "Harga ikut pasar. Supaya tetap sesuai anggaranmu, pasang diskon minimum di portal. Kalau tidak ada harga semurah itu, request ditolak dan saldo tidak terpotong. Belum tersedia untuk semua model." },
  { icon: KeyRound, title: "Satu key, banyak model", body: "Bisa dipakai dengan format API OpenAI maupun Anthropic. Untuk ganti model, cukup ubah nama model di request." },
  { icon: Gauge, title: "Pantau dari portal", body: "Sisa saldo, token yang terpakai, dan riwayat request ada di portal. Kalau key bocor, buat key baru dari sana." },
];

export default function PublicLanding() {
  const overview = useQuery({
    queryKey: ["public-overview"],
    queryFn: fetchPublicOverview,
    staleTime: 30_000,
    refetchInterval: 5_000,
    retry: false,
  });
  const models = useQuery({
    queryKey: ["public-models"],
    queryFn: fetchPublicModels,
    staleTime: 60_000,
    // Prices follow the market sync; the server caches this response for
    // 12s, so a 15s poll picks up each change without hitting the DB per tab.
    refetchInterval: 15_000,
    retry: false,
  });

  const modelList = useMemo(() => models.data ?? [], [models.data]);
  const overviewData = overview.data;
  const requestsCount = useCountUp(overviewData?.total_requests ?? 0);
  const tokensCount = useCountUp(overviewData?.total_tokens ?? 0);
  const available = modelList.filter((m) => !m.sold_out).length;
  // Rows arrive sorted by request volume; the landing previews the top three
  // routable models and sends the rest to the full catalog.
  const topModels = modelList.filter((m) => !m.sold_out).slice(0, 3);
  const sampleModel = modelList.find((m) => !m.sold_out)?.model_id ?? "nama-model";

  // The portal branding provider rewrites document.title when /portal mounts;
  // re-assert the landing title on mount so it survives that navigation.
  useEffect(() => {
    document.title = "Tokenizer";
  }, []);

  return (
    <PublicLayout>
      {/* Hero */}
      <section id="overview" className="relative scroll-mt-20">
        <div className="relative grid items-center gap-10 py-6 lg:grid-cols-[1.05fr_1fr] lg:py-12">
          <div className="text-center lg:text-left">
            <span className="inline-flex items-center gap-2 rounded-full border border-[var(--line)] bg-[var(--paper)] px-3 py-1 text-[12px] text-[var(--muted)]">
              <span className="landing-live-dot h-1.5 w-1.5 rounded-full bg-[var(--ok)]" aria-hidden="true" />
              {fmtCount(available)} model siap dipakai
            </span>
            <h1 className="mt-5 text-[38px] font-semibold leading-[1.05] tracking-[-1.4px] text-[var(--ink)] sm:text-[52px] lg:text-[58px]">
              Satu API key,
              <br />
              <span className="text-[var(--green)]">bayar per token.</span>
            </h1>
            <p className="mx-auto mt-5 max-w-xl text-[16px] leading-relaxed text-[var(--muted)] sm:text-[17px] lg:mx-0">
              Bisa dipakai di aplikasi yang mendukung API OpenAI atau Anthropic. Cukup ganti base URL, API key, dan
              nama model. Tidak ada biaya bulanan.
            </p>
            <div className="mt-7 flex flex-col items-center gap-3 sm:flex-row sm:justify-center lg:justify-start">
              <a
                href="/portal"
                className={`inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg bg-[var(--action)] px-5 text-[14px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 sm:w-auto ${FOCUS}`}
              >
                Buat API key
                <ArrowRight className="h-4 w-4" aria-hidden="true" />
              </a>
              <a
                href="/model"
                className={`inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-5 text-[14px] font-medium text-[var(--ink)] no-underline transition-colors hover:border-[var(--line-strong)] sm:w-auto ${FOCUS}`}
              >
                <Boxes className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                Lihat model &amp; harga
              </a>
            </div>
          </div>
          <QuickStart model={sampleModel} />
        </div>
      </section>

      {/* Live metrics */}
      <section aria-label="Statistik layanan" className="mt-6">
        <div className="mb-3 flex items-center gap-2 text-[12px] text-[var(--muted)]">
          <span className="landing-live-dot h-1.5 w-1.5 rounded-full bg-[var(--ok)]" aria-hidden="true" />
          Data langsung dari server kami, diperbarui otomatis
        </div>
        <div className="grid gap-px overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--line)] sm:grid-cols-3">
          <Metric icon={Activity} label="Total request" value={fmtCount(Math.round(requestsCount))} hint="Sepanjang waktu" />
          <Metric icon={Coins} label="Token diproses" value={fmtCount(Math.round(tokensCount))} hint="Sepanjang waktu" />
          <Metric icon={Boxes} label="Model tersedia" value={fmtCount(overviewData?.model_count ?? 0)} hint="Semuanya bisa dipakai dengan satu key" />
        </div>
      </section>

      {/* How it works */}
      <section className="mt-20">
        <SectionHeading eyebrow="Cara kerja" title="Mulai dalam tiga langkah">
          Tidak perlu kartu kredit. Isi saldo lewat QRIS dari aplikasi bank atau e-wallet.
        </SectionHeading>
        <ol className="mt-8 grid gap-4 md:grid-cols-3">
          {STEPS.map((step, i) => (
            <li key={step.title} className="relative rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5">
              <div className="flex items-center justify-between">
                <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--accent-bg)] text-[var(--green)]">
                  <step.icon className="h-4.5 w-4.5" strokeWidth={1.75} aria-hidden="true" />
                </span>
                <span className="font-mono text-[12px] text-[var(--muted)]">0{i + 1}</span>
              </div>
              <h3 className="mt-4 text-[15px] font-semibold text-[var(--ink)]">{step.title}</h3>
              <p className="mt-1.5 text-[14px] leading-relaxed text-[var(--muted)]">{step.body}</p>
            </li>
          ))}
        </ol>
      </section>

      {/* Features */}
      <section className="mt-20">
        <SectionHeading eyebrow="Kenapa Tokenizer" title="Satu saldo untuk semua model, tanpa biaya bulanan" />
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

      {/* Models */}
      <section id="models" className="mt-20 scroll-mt-20">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <SectionHeading eyebrow="Model & harga" title="Model yang paling sering dipakai">
            Harga dalam USD per 1 juta token, sesuai harga pasar terbaru. Daftar lengkap ada di katalog.
          </SectionHeading>
        </div>
        {models.isLoading ? (
          <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true" aria-label="Memuat model">
            {Array.from({ length: 3 }, (_, i) => (
              <div key={i} className="h-[232px] animate-pulse rounded-xl border border-[var(--line)] bg-[var(--paper)]" />
            ))}
          </div>
        ) : topModels.length === 0 ? (
          <p className="mt-6 rounded-xl border border-dashed border-[var(--line)] p-8 text-center text-sm text-[var(--muted)]">Belum ada model tersedia.</p>
        ) : (
          <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {topModels.map((m) => <ModelCard key={m.model_id} model={m} />)}
          </div>
        )}
        <div className="mt-6 flex justify-center">
          <a
            href="/model"
            className={`inline-flex h-11 items-center justify-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-5 text-[14px] font-medium text-[var(--ink)] no-underline transition-colors hover:border-[var(--line-strong)] ${FOCUS}`}
          >
            Lihat semua {fmtCount(modelList.length)} model
            <ArrowRight className="h-4 w-4" aria-hidden="true" />
          </a>
        </div>
      </section>

      {/* CTA */}
      <section id="purchase" className="mt-20 scroll-mt-20">
        <div className="relative overflow-hidden rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-6 sm:p-10">
          <div className="relative flex flex-col gap-6 md:flex-row md:items-center md:justify-between">
            <div className="max-w-xl">
              <h2 className="text-[24px] font-semibold tracking-[-0.5px] text-[var(--ink)] sm:text-[28px]">Mulai dengan saldo Rp10.000</h2>
              <p className="mt-2 text-[15px] leading-relaxed text-[var(--muted)]">
                Login dengan Google, isi saldo lewat QRIS, lalu pasang API key-mu. Kalau ada kendala, tanya langsung di
                grup Telegram.
              </p>
            </div>
            <div className="flex flex-col gap-3 sm:flex-row">
              <a
                href="/portal"
                className={`inline-flex h-11 items-center justify-center gap-2 rounded-lg bg-[var(--action)] px-5 text-[14px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 ${FOCUS}`}
              >
                <Wallet className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                Masuk ke portal
              </a>
              <a
                href={TELEGRAM_URL}
                target="_blank"
                rel="noopener noreferrer"
                className={`inline-flex h-11 items-center justify-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-5 text-[14px] font-medium text-[var(--ink)] no-underline transition-colors hover:border-[var(--line-strong)] ${FOCUS}`}
              >
                <Send className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                Gabung Telegram
              </a>
            </div>
          </div>
        </div>
      </section>
    </PublicLayout>
  );
}
