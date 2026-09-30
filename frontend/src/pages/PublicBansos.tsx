// Public Bansos page — served at `/bansos`. Mirrors the landing page's visual
// language via PublicLayout and the .landing-root tokens. Reads the public
// bansos endpoints; the API key is masked until the user clicks Show, which
// fetches the plaintext on demand.

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Cpu, Eye, EyeOff, Gift, Gauge, Copy, Check, Globe } from "lucide-react";
import { fetchPublicBansos, fetchPublicBansosKey } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";

const WA_URL = "https://wa.me/84826240052";

const fmtUSD = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 4 })}`;

function StatusBadge({ active }: { active: boolean }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-[650] ${
        active
          ? "bg-[var(--accent-bg)] text-[var(--green)]"
          : "bg-[var(--soft)] text-[var(--muted)]"
      }`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${active ? "bg-[var(--green)]" : "bg-[var(--muted)]"}`} />
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
        <span className="inline-flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--soft)] text-[var(--green)]">
          <Gift className="h-6 w-6" aria-hidden="true" />
        </span>
        <h1 className="mt-3 font-[750] tracking-[-1.5px] text-[var(--ink)]" style={{ fontSize: "clamp(32px,6vw,56px)", lineHeight: 1.05 }}>
          BANSOS AI
        </h1>
        <p className="mt-2 text-base text-[var(--muted)] sm:text-lg">API key gratis untuk dicoba bersama</p>
        <div className="mt-3 flex justify-center">
          <StatusBadge active={active} />
        </div>
      </div>

      {!data?.exists ? (
        <div className="mt-10 rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-6 text-center text-sm text-[var(--muted)]">
          Belum tersedia saat ini. Silakan cek kembali nanti.
        </div>
      ) : (
        <div className="mt-10 space-y-4">
          <section className="rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-center gap-2 text-[var(--muted)]">
              <Globe className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
              <p className="text-[11px] uppercase tracking-[0.7px]">Base URL</p>
            </div>
            <div className="mt-3 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-xl border border-[var(--line)] bg-[var(--soft)] px-3 py-2 font-mono text-sm text-[var(--ink)]">
                {baseURL}
              </code>
              <button
                type="button"
                onClick={copyBase}
                className="inline-flex items-center gap-1.5 rounded-xl border border-[var(--line)] px-3 py-2 text-sm font-[650] text-[var(--green)] hover:bg-[var(--soft)]"
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
          <section className="rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-center gap-2 text-[var(--muted)]">
              <Cpu className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
              <p className="text-[11px] uppercase tracking-[0.7px]">API Key</p>
            </div>
            <div className="mt-3 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-xl border border-[var(--line)] bg-[var(--soft)] px-3 py-2 font-mono text-sm text-[var(--ink)]">
                {active && revealed ? revealed : data.masked_display}
              </code>
              {active && (
                <button
                  type="button"
                  onClick={toggleReveal}
                  disabled={busy}
                  className="inline-flex items-center gap-1.5 rounded-xl border border-[var(--line)] px-3 py-2 text-sm font-[650] text-[var(--green)] hover:bg-[var(--soft)] disabled:opacity-50"
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
                className="mt-3 inline-flex items-center gap-1.5 rounded-xl bg-[var(--green)] px-4 py-2 text-sm font-[650] text-[var(--on-accent)] hover:opacity-90"
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
                <p className="text-[11px] uppercase tracking-[0.7px]">Limit</p>
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

          <section className="rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
            <div className="flex items-center gap-2 text-[var(--muted)]">
              <Cpu className="h-4 w-4 text-[var(--green)]" aria-hidden="true" />
              <p className="text-[11px] uppercase tracking-[0.7px]">Model aktif</p>
            </div>
            <div className="mt-3 flex flex-wrap gap-2">
              {data.allowed_models.length === 0 ? (
                <p className="text-sm text-[var(--muted)]">Belum ada model.</p>
              ) : (
                data.allowed_models.map((m) => (
                  <span key={m} className="rounded-lg border border-[var(--line)] bg-[var(--soft)] px-2.5 py-1 font-mono text-xs text-[var(--ink)]">
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
        <section className="rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Butuh lebih murah?</p>
          <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">Model lain lebih hemat</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Lihat daftar lengkap model dan harga per 1 juta token. Banyak model lebih murah dari yang ada di bansos.
          </p>
          <a href="/#models" className="mt-4 inline-flex items-center gap-1.5 rounded-xl border border-[var(--line)] px-4 py-3 text-sm font-[650] text-[var(--green)] no-underline hover:bg-[var(--soft)]">
            Lihat harga model ↗
          </a>
        </section>

        <section className="rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Butuh model lain atau saldo?</p>
          <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">Top up &amp; akses penuh</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Bansos terbatas. Hubungi kami untuk saldo PAYG dan akses semua model tanpa limit bansos.
          </p>
          <a href={WA_URL} target="_blank" rel="noreferrer" className="mt-4 inline-flex items-center gap-1.5 rounded-xl bg-[var(--green)] px-4 py-3 text-sm font-[650] text-[var(--on-accent)] no-underline hover:opacity-90">
            Hubungi WhatsApp ↗
          </a>
        </section>
      </div>
    </PublicLayout>
  );
}
