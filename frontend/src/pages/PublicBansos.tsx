// Public Bansos page — served at `/bansos`. Mirrors the landing page's visual
// language via PublicLayout and the .landing-root tokens. Reads the public
// bansos endpoints; the API key is masked until the user clicks Show, which
// fetches the plaintext on demand.

import { useEffect, useState } from "react";
import type { ComponentType } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Boxes,
  Check,
  Copy,
  Eye,
  EyeOff,
  Gauge,
  Gift,
  Globe,
  KeyRound,
  Layers,
  Wallet,
} from "lucide-react";
import { fetchPublicBansos, fetchPublicBansosKey } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";

const FOCUS =
  "focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg)]";

const fmtUSD = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 4 })}`;
const fmtInt = (n: number) => n.toLocaleString("id-ID");

function StatusBadge({ active }: { active: boolean }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-[12px] font-medium ${
        active ? "bg-[var(--tag-bg)] text-[var(--tag-ink)]" : "bg-[var(--soft)] text-[var(--muted)]"
      }`}
    >
      <span
        className={`h-1.5 w-1.5 rounded-full ${active ? "landing-live-dot bg-[var(--ok)]" : "bg-[var(--muted)]"}`}
        aria-hidden="true"
      />
      {active ? "Aktif" : "Nonaktif"}
    </span>
  );
}

// A single stat in the metric strip. Values come straight from the bansos
// endpoint — tiles for rpm/tpm only render when the gateway reports a limit,
// so no placeholder numbers are ever shown.
function MetricTile({
  icon: Icon,
  label,
  value,
  sub,
}: {
  icon: ComponentType<{ className?: string; strokeWidth?: number; "aria-hidden"?: boolean }>;
  label: string;
  value: string;
  sub: string;
}) {
  return (
    <div className="rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-4 sm:p-5">
      <div className="flex items-center gap-2 text-[var(--muted)]">
        <Icon className="h-4 w-4 text-[var(--green)]" strokeWidth={1.75} aria-hidden={true} />
        <span className="font-mono text-[11px] uppercase tracking-[1px]">{label}</span>
      </div>
      <p className="mt-3 text-[26px] font-semibold leading-none tracking-[-0.6px] text-[var(--ink)]">{value}</p>
      <p className="mt-1.5 text-xs text-[var(--muted)]">{sub}</p>
    </div>
  );
}

// Label shown above a copyable credential value.
function FieldLabel({
  icon: Icon,
  children,
}: {
  icon: ComponentType<{ className?: string; strokeWidth?: number; "aria-hidden"?: boolean }>;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-2 text-[var(--muted)]">
      <Icon className="h-4 w-4 text-[var(--green)]" strokeWidth={1.75} aria-hidden={true} />
      <span className="font-mono text-[11px] uppercase tracking-[1px]">{children}</span>
    </div>
  );
}

export default function PublicBansos() {
  const state = useQuery({
    queryKey: ["public-bansos"],
    queryFn: fetchPublicBansos,
    staleTime: 30_000,
    retry: false,
  });
  const [revealed, setRevealed] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [copiedBase, setCopiedBase] = useState(false);
  const [busy, setBusy] = useState(false);

  const baseURL = `${window.location.origin}/v1`;

  useEffect(() => {
    document.title = "Bansos — Tokenizer";
  }, []);

  const data = state.data;
  const active = data?.active ?? false;

  const toggleReveal = async () => {
    if (revealed) {
      setRevealed(null);
      return;
    }
    setBusy(true);
    try {
      setRevealed(await fetchPublicBansosKey());
    } catch {
      setRevealed(null);
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!revealed) return;
    try {
      await navigator.clipboard.writeText(revealed);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  };

  const copyBase = async () => {
    try {
      await navigator.clipboard.writeText(baseURL);
      setCopiedBase(true);
      setTimeout(() => setCopiedBase(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  };

  // Metric strip — built from whatever the gateway actually reports.
  const tiles: { icon: typeof Wallet; label: string; value: string; sub: string }[] = [];
  if (data?.exists) {
    tiles.push({
      icon: Wallet,
      label: "Limit",
      value:
        data.mode === "unlimited"
          ? "Tanpa batas"
          : data.credit_remaining_usd != null
            ? fmtUSD(data.credit_remaining_usd)
            : "Habis",
      sub: data.mode === "unlimited" ? "Kredit tak terbatas" : "Sisa kredit bersama",
    });
    if (data.rpm > 0) tiles.push({ icon: Gauge, label: "Laju", value: fmtInt(data.rpm), sub: "Request per menit" });
    if (data.tpm > 0) tiles.push({ icon: Layers, label: "Token", value: fmtInt(data.tpm), sub: "Token per menit" });
    tiles.push({ icon: Boxes, label: "Model", value: fmtInt(data.allowed_models.length), sub: "Model bisa dipanggil" });
  }

  return (
    <PublicLayout>
      {/* Header */}
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-start gap-4">
          <span className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-[var(--accent-bg)] text-[var(--green)]">
            <Gift className="h-6 w-6" strokeWidth={1.75} aria-hidden="true" />
          </span>
          <div>
            <h1 className="text-[30px] font-semibold leading-[1.05] tracking-[-1px] text-[var(--ink)] sm:text-[38px]">
              Bansos AI
            </h1>
            <p className="mt-1.5 text-[15px] text-[var(--muted)] sm:text-base">
              Satu API key gratis, dipakai ramai-ramai untuk mencoba.
            </p>
          </div>
        </div>
        <StatusBadge active={active} />
      </header>

      {/* Loading */}
      {state.isLoading && (
        <div className="mt-10 rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-6 text-center text-sm text-[var(--muted)]">
          Memuat data bansos…
        </div>
      )}

      {/* Error */}
      {state.isError && !state.isLoading && (
        <div className="mt-10 rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-6 text-center">
          <p className="text-sm text-[var(--ink)]">Gagal memuat data bansos.</p>
          <button
            type="button"
            onClick={() => state.refetch()}
            className={`mt-4 inline-flex h-10 items-center gap-1.5 rounded-full border border-[var(--line)] bg-[var(--paper)] px-4 text-[14px] font-medium text-[var(--ink)] transition-colors hover:border-[var(--line-strong)] ${FOCUS}`}
          >
            Coba lagi
          </button>
        </div>
      )}

      {/* Empty */}
      {!state.isLoading && !state.isError && !data?.exists && (
        <div className="mt-10 rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-6 text-center text-sm text-[var(--muted)]">
          Belum tersedia saat ini. Silakan cek kembali nanti.
        </div>
      )}

      {/* Loaded */}
      {!state.isLoading && !state.isError && data?.exists && (
        <div className="mt-8 space-y-6">
          {/* Metric strip */}
          <div className="grid gap-3 [grid-template-columns:repeat(auto-fit,minmax(150px,1fr))] sm:gap-4">
            {tiles.map((t) => (
              <MetricTile key={t.label} icon={t.icon} label={t.label} value={t.value} sub={t.sub} />
            ))}
          </div>

          {/* Connection: base URL + API key */}
          <section className="rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <h2 className="text-[18px] font-medium tracking-[-0.3px] text-[var(--ink)]">Koneksi</h2>
            <p className="mt-1 text-sm text-[var(--muted)]">
              Pasang base URL dan API key di SDK OpenAI, Anthropic, atau Gemini.
            </p>

            <div className="mt-5 space-y-5">
              {/* Base URL */}
              <div>
                <FieldLabel icon={Globe}>Base URL</FieldLabel>
                <div className="mt-2.5 flex items-center gap-2">
                  <code className="min-w-0 flex-1 truncate rounded-lg border border-[var(--line)] bg-[var(--bg)] px-3 py-2.5 font-mono text-[13px] text-[var(--ink)]">
                    {baseURL}
                  </code>
                  <button
                    type="button"
                    onClick={copyBase}
                    aria-label="Salin base URL"
                    className={`inline-flex h-10 shrink-0 items-center gap-1.5 rounded-full border border-[var(--line)] bg-[var(--paper)] px-3.5 text-[13px] font-medium text-[var(--ink)] transition-colors hover:border-[var(--line-strong)] ${FOCUS}`}
                  >
                    {copiedBase ? <Check className="h-4 w-4 text-[var(--green)]" /> : <Copy className="h-4 w-4" />}
                    <span className="hidden sm:inline">{copiedBase ? "Tersalin" : "Salin"}</span>
                  </button>
                </div>
              </div>

              {/* API key */}
              <div className="border-t border-[var(--line)] pt-5">
                <FieldLabel icon={KeyRound}>API Key</FieldLabel>
                <div className="mt-2.5 flex items-center gap-2">
                  <code className="min-w-0 flex-1 truncate rounded-lg border border-[var(--line)] bg-[var(--bg)] px-3 py-2.5 font-mono text-[13px] text-[var(--ink)]">
                    {active && revealed ? revealed : data.masked_display}
                  </code>
                  {active && (
                    <button
                      type="button"
                      onClick={toggleReveal}
                      disabled={busy}
                      aria-label={revealed ? "Sembunyikan API key" : "Tampilkan API key"}
                      className={`inline-flex h-10 shrink-0 items-center gap-1.5 rounded-full border border-[var(--line)] bg-[var(--paper)] px-3.5 text-[13px] font-medium text-[var(--ink)] transition-colors hover:border-[var(--line-strong)] disabled:opacity-50 ${FOCUS}`}
                    >
                      {revealed ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      <span className="hidden sm:inline">{revealed ? "Sembunyikan" : "Tampilkan"}</span>
                    </button>
                  )}
                  {active && revealed && (
                    <button
                      type="button"
                      onClick={copy}
                      aria-label="Salin API key"
                      className={`inline-flex h-10 shrink-0 items-center gap-1.5 rounded-full bg-[var(--action)] px-3.5 text-[13px] font-medium text-[var(--action-fg)] transition-opacity hover:opacity-90 ${FOCUS}`}
                    >
                      {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                      <span className="hidden sm:inline">{copied ? "Tersalin" : "Salin"}</span>
                    </button>
                  )}
                </div>
                {!active && (
                  <p className="mt-2.5 text-xs text-[var(--muted)]">Bansos sedang nonaktif, key disembunyikan.</p>
                )}
              </div>
            </div>
          </section>

          {/* Model aktif */}
          <section className="rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-baseline justify-between gap-3">
              <h2 className="text-[18px] font-medium tracking-[-0.3px] text-[var(--ink)]">Model aktif</h2>
              <span className="font-mono text-[12px] text-[var(--muted)]">{fmtInt(data.allowed_models.length)} model</span>
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
              {data.allowed_models.length === 0 ? (
                <p className="text-sm text-[var(--muted)]">Belum ada model.</p>
              ) : (
                data.allowed_models.map((m) => (
                  <span
                    key={m}
                    className="rounded-lg border border-[var(--line)] bg-[var(--bg)] px-2.5 py-1.5 font-mono text-[12px] text-[var(--ink)]"
                  >
                    {m}
                  </span>
                ))
              )}
            </div>
          </section>
        </div>
      )}

      {/* Next steps — two routes out of the free tier */}
      <div className="mt-6 grid gap-4 md:grid-cols-2">
        <section className="rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Butuh lebih murah?</p>
          <h2 className="mt-1.5 text-[20px] font-semibold tracking-[-0.4px] text-[var(--ink)]">Model lain lebih hemat</h2>
          <p className="mt-2 text-sm leading-relaxed text-[var(--muted)]">
            Lihat daftar lengkap model dan harga per 1 juta token. Banyak yang lebih murah dari model di bansos.
          </p>
          <a
            href="/model"
            className={`mt-4 inline-flex h-10 items-center gap-1.5 rounded-full border border-[var(--line)] bg-[var(--paper)] px-4 text-[14px] font-medium text-[var(--ink)] no-underline transition-colors hover:border-[var(--line-strong)] ${FOCUS}`}
          >
            Lihat harga model
          </a>
        </section>

        <section className="rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Siap pakai sendiri?</p>
          <h2 className="mt-1.5 text-[20px] font-semibold tracking-[-0.4px] text-[var(--ink)]">Top up &amp; akses penuh</h2>
          <p className="mt-2 text-sm leading-relaxed text-[var(--muted)]">
            Bansos terbatas dan dipakai bersama. Isi saldo sendiri untuk semua model tanpa limit bansos.
          </p>
          <a
            href="/portal"
            className={`mt-4 inline-flex h-10 items-center gap-1.5 rounded-full bg-[var(--action)] px-4 text-[14px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 ${FOCUS}`}
          >
            Buka portal
          </a>
        </section>
      </div>
    </PublicLayout>
  );
}
