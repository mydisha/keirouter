// Public Bansos page — served at `/bansos`. Mirrors the landing page's visual
// language via PublicLayout and the .landing-root tokens. Reads the public
// bansos endpoints; the API key is masked until the user clicks Show, which
// fetches the plaintext on demand.

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Cpu, Eye, EyeOff, Gift, Gauge, Copy, Check, Globe } from "lucide-react";
import { fetchPublicBansos, fetchPublicBansosKey } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";

const fmtUSD = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 4 })}`;

function StatusBadge({ active }: { active: boolean }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px] font-medium ${
        active
          ? "bg-[var(--tag-bg)] text-[var(--tag-ink)]"
          : "bg-[var(--soft)] text-[var(--muted)]"
      }`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${active ? "landing-live-dot bg-[var(--ok)]" : "bg-[var(--muted)]"}`} />
      {active ? "Aktif" : "Nonaktif"}
    </span>
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

  return (
    <PublicLayout>
      <div className="text-center">
        <span className="inline-flex h-11 w-11 items-center justify-center rounded-lg bg-[var(--accent-bg)] text-[var(--green)]">
          <Gift className="h-6 w-6" aria-hidden="true" />
        </span>
        <h1 className="mt-4 text-[34px] font-semibold leading-[1.05] tracking-[-1px] text-[var(--ink)] sm:text-[46px]">
          BANSOS AI
        </h1>
        <p className="mt-2 text-base text-[var(--muted)] sm:text-lg">API key gratis untuk dicoba bersama</p>
        <div className="mt-3 flex justify-center">
          <StatusBadge active={active} />
        </div>
      </div>

      {!data?.exists ? (
        <div className="mt-10 rounded-xl border border-[var(--line)] bg-[var(--paper)] p-6 text-center text-sm text-[var(--muted)]">
          Belum tersedia saat ini. Silakan cek kembali nanti.
        </div>
      ) : (
        <div className="mt-10 space-y-4">
          <section className="rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-center gap-2 text-[var(--muted)]">
              <Globe className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
              <p className="font-mono text-[11px] uppercase tracking-[1px]">Base URL</p>
            </div>
            <div className="mt-3 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-lg border border-[var(--line)] bg-[var(--bg)] px-3 py-2 font-mono text-[13px] text-[var(--ink)]">
                {baseURL}
              </code>
              <button
                type="button"
                onClick={copyBase}
                className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-3 text-[13px] font-medium text-[var(--ink)] transition-colors hover:border-[var(--line-strong)]"
              >
                {copiedBase ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                {copiedBase ? "Tersalin" : "Copy"}
              </button>
            </div>
            <p className="mt-2 text-xs text-[var(--muted)]">
              Gunakan base URL ini dengan API key di bawah. Kompatibel dengan OpenAI, Anthropic, dan Gemini SDK.
            </p>
          </section>

          <div className="grid gap-4 md:grid-cols-2">
          <section className="rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-center gap-2 text-[var(--muted)]">
              <Cpu className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
              <p className="font-mono text-[11px] uppercase tracking-[1px]">API Key</p>
            </div>
            <div className="mt-3 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-lg border border-[var(--line)] bg-[var(--bg)] px-3 py-2 font-mono text-[13px] text-[var(--ink)]">
                {active && revealed ? revealed : data.masked_display}
              </code>
              {active && (
                <button
                  type="button"
                  onClick={toggleReveal}
                  disabled={busy}
                  className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-3 text-[13px] font-medium text-[var(--ink)] transition-colors hover:border-[var(--line-strong)] disabled:opacity-50"
                >
                  {revealed ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                  {revealed ? "Hide" : "Show"}
                </button>
              )}
            </div>
            {active && revealed && (
              <button
                type="button"
                onClick={copy}
                className="mt-3 inline-flex h-9 items-center gap-1.5 rounded-lg bg-[var(--action)] px-4 text-[13px] font-medium text-[var(--action-fg)] hover:opacity-90"
              >
                {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                {copied ? "Tersalin" : "Copy key"}
              </button>
            )}
            {!active && (
              <p className="mt-3 text-xs text-[var(--muted)]">Bansos sedang nonaktif. Key disembunyikan.</p>
            )}

            <div className="mt-5 border-t border-[var(--line)] pt-4">
              <div className="flex items-center gap-2 text-[var(--muted)]">
                <Gauge className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
                <p className="font-mono text-[11px] uppercase tracking-[1px]">Limit</p>
              </div>
              <p className="mt-2 text-sm text-[var(--ink)]">
                {data.mode === "unlimited"
                  ? "Unlimited"
                  : data.credit_remaining_usd != null
                    ? `Sisa kredit ${fmtUSD(data.credit_remaining_usd)}`
                    : "Kredit habis"}
              </p>
              {(data.rpm > 0 || data.tpm > 0) && (
                <p className="mt-1 text-xs text-[var(--muted)]">
                  {data.rpm > 0 && `${data.rpm} req/menit`}
                  {data.rpm > 0 && data.tpm > 0 && " · "}
                  {data.tpm > 0 && `${data.tpm.toLocaleString("id-ID")} token/menit`}
                </p>
              )}
            </div>
          </section>

          <section className="rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-center gap-2 text-[var(--muted)]">
              <Cpu className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
              <p className="font-mono text-[11px] uppercase tracking-[1px]">Model aktif</p>
            </div>
            <div className="mt-3 flex flex-wrap gap-2">
              {data.allowed_models.length === 0 ? (
                <p className="text-sm text-[var(--muted)]">Belum ada model.</p>
              ) : (
                data.allowed_models.map((m) => (
                  <span key={m} className="rounded-md border border-[var(--line)] bg-[var(--bg)] px-2 py-1 font-mono text-[12px] text-[var(--ink)]">
                    {m}
                  </span>
                ))
              )}
            </div>
          </section>
        </div>
        </div>
      )}

      <div className="mt-8 grid gap-4 md:grid-cols-2">
        <section className="rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Butuh lebih murah?</p>
          <h2 className="mt-1 text-[20px] font-semibold tracking-[-0.4px] text-[var(--ink)]">Model lain lebih hemat</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Lihat daftar lengkap model dan harga per 1 juta token. Banyak model lebih murah dari yang ada di bansos.
          </p>
          <a href="/model" className="mt-4 inline-flex h-10 items-center gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-4 text-[14px] font-medium text-[var(--ink)] no-underline hover:border-[var(--line-strong)]">
            Lihat harga model →
          </a>
        </section>

        <section className="rounded-xl border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Butuh model lain atau saldo?</p>
          <h2 className="mt-1 text-[20px] font-semibold tracking-[-0.4px] text-[var(--ink)]">Top up &amp; akses penuh</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Bansos terbatas. Hubungi kami untuk saldo PAYG dan akses semua model tanpa limit bansos.
          </p>
          <a href="/portal" className="mt-4 inline-flex h-10 items-center gap-1.5 rounded-lg bg-[var(--action)] px-4 text-[14px] font-medium text-[var(--action-fg)] no-underline hover:opacity-90">
            Buka portal →
          </a>
        </section>
      </div>
    </PublicLayout>
  );
}
