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
  Activity, ArrowRight, Boxes, Check, Coins, Copy, Gauge, KeyRound, LogIn, Plug, Receipt, Send, ShieldCheck, Wallet,
  type LucideIcon,
} from "lucide-react";
import { fetchPublicOverview, fetchPublicModels } from "../lib/publicApi";
import { PublicLayout, TELEGRAM_URL } from "../components/PublicLayout";
import { ModelBrowser, fmtCount, fmtShort } from "../components/ModelCatalog";

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
      <p className="mt-2 font-mono text-[28px] font-semibold leading-none tracking-[-0.5px] tabular-nums text-[var(--ink)] sm:text-[32px]">
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
          <pre className="mt-1.5 overflow-x-auto rounded-md bg-black/30 p-3 font-mono text-[12px] leading-relaxed text-[var(--terminal-ink)]">
            <code>{snippet}</code>
          </pre>
        </div>
      </div>
    </div>
  );
}

const STEPS: { icon: LucideIcon; title: string; body: string }[] = [
  { icon: LogIn, title: "Masuk ke portal", body: "Login dengan akun Google, API key pribadimu langsung disiapkan." },
  { icon: Wallet, title: "Isi saldo", body: "Tambah saldo dari portal. Saldo hanya terpotong sesuai token yang benar-benar dipakai." },
  { icon: Plug, title: "Pasang di tools favorit", body: "Pakai base URL dan API key di Claude Code, Cursor, Cline, SDK OpenAI, atau aplikasi apa pun." },
];

const FEATURES: { icon: LucideIcon; title: string; body: string }[] = [
  { icon: Receipt, title: "Bayar per token", body: "Tanpa langganan dan tanpa biaya bulanan. Harga per 1 juta token tertera jelas untuk tiap model." },
  { icon: KeyRound, title: "Satu API key", body: "Format OpenAI dan Anthropic didukung, jadi satu key cukup untuk banyak model." },
  { icon: Gauge, title: "Pantau real-time", body: "Pemakaian, sisa saldo, dan riwayat request bisa dicek kapan saja dari portal." },
  { icon: ShieldCheck, title: "Login aman", body: "Portal memakai Google Sign-In; key kamu terikat ke akunmu sendiri." },
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
    retry: false,
  });

  const modelList = useMemo(() => models.data ?? [], [models.data]);
  const overviewData = overview.data;
  const requestsCount = useCountUp(overviewData?.total_requests ?? 0);
  const tokensCount = useCountUp(overviewData?.total_tokens ?? 0);
  const available = modelList.filter((m) => !m.sold_out).length;
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
        <div className="landing-hero-bg pointer-events-none absolute inset-x-[-50vw] -top-12 h-[560px]" aria-hidden="true" />
        <div className="relative grid items-center gap-10 py-6 lg:grid-cols-[1.05fr_1fr] lg:py-12">
          <div className="text-center lg:text-left">
            <span className="inline-flex items-center gap-2 rounded-full border border-[var(--line)] bg-[var(--paper)] px-3 py-1 text-[12px] text-[var(--muted)]">
              <span className="landing-live-dot h-1.5 w-1.5 rounded-full bg-[var(--ok)]" aria-hidden="true" />
              {fmtCount(available)} model aktif sekarang
            </span>
            <h1 className="mt-5 text-[38px] font-semibold leading-[1.05] tracking-[-1.4px] text-[var(--ink)] sm:text-[52px] lg:text-[58px]">
              Akses AI frontier,
              <br />
              <span className="text-[var(--green)]">bayar sesuai pakai.</span>
            </h1>
            <p className="mx-auto mt-5 max-w-xl text-[16px] leading-relaxed text-[var(--muted)] sm:text-[17px] lg:mx-0">
              Satu API key untuk model-model terbaik. Kompatibel dengan format OpenAI, tanpa langganan, saldo terpotong per
              token.
            </p>
            <div className="mt-7 flex flex-col items-center gap-3 sm:flex-row sm:justify-center lg:justify-start">
              <a
                href="/portal"
                className={`inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg bg-[var(--action)] px-5 text-[14px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 sm:w-auto ${FOCUS}`}
              >
                Mulai sekarang
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
          Data langsung, diperbarui tiap 5 detik
        </div>
        <div className="grid gap-px overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--line)] sm:grid-cols-3">
          <Metric icon={Activity} label="Total request" value={fmtShort(Math.round(requestsCount))} hint="Sepanjang waktu" />
          <Metric icon={Coins} label="Token diproses" value={fmtShort(Math.round(tokensCount))} hint={`${fmtCount(Math.round(tokensCount))} token`} />
          <Metric icon={Boxes} label="Model tersedia" value={fmtCount(overviewData?.model_count ?? 0)} hint="Siap dipanggil lewat satu API" />
        </div>
      </section>

      {/* How it works */}
      <section className="mt-20">
        <SectionHeading eyebrow="Cara kerja" title="Mulai dalam tiga langkah">
          Dari daftar sampai request pertama hanya butuh beberapa menit.
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
        <SectionHeading eyebrow="Kenapa Tokenizer" title="Dibuat untuk dipakai setiap hari" />
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
          <SectionHeading eyebrow="Model & harga" title="Pilih model, lihat harganya">
            Diurutkan berdasarkan popularitas. Semua harga dalam USD per 1 juta token.
          </SectionHeading>
          <a href="/model" className={`inline-flex items-center gap-1 rounded-md text-[13px] font-medium text-[var(--green)] no-underline hover:underline ${FOCUS}`}>
            Halaman katalog lengkap
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </a>
        </div>
        <ModelBrowser models={modelList} loading={models.isLoading} />
      </section>

      {/* CTA */}
      <section id="purchase" className="mt-20 scroll-mt-20">
        <div className="relative overflow-hidden rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-6 sm:p-10">
          <div className="landing-hero-bg pointer-events-none absolute inset-0 opacity-70" aria-hidden="true" />
          <div className="relative flex flex-col gap-6 md:flex-row md:items-center md:justify-between">
            <div className="max-w-xl">
              <h2 className="text-[24px] font-semibold tracking-[-0.5px] text-[var(--ink)] sm:text-[28px]">Siap mencoba?</h2>
              <p className="mt-2 text-[15px] leading-relaxed text-[var(--muted)]">
                Isi saldo, cek sisa saldo, dan pantau pemakaian langsung dari portal. Ada pertanyaan? Tim kami siap
                membantu di Telegram.
              </p>
            </div>
            <div className="flex flex-col gap-3 sm:flex-row">
              <a
                href="/portal"
                className={`inline-flex h-11 items-center justify-center gap-2 rounded-lg bg-[var(--action)] px-5 text-[14px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 ${FOCUS}`}
              >
                <Wallet className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                Buka portal
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
