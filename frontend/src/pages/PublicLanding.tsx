// Public landing page — served at `/` by the router.
//
// Reads the two read-only public endpoints via TanStack Query. Every section
// degrades to a zeroed/empty state when a query is loading, errored, or returns
// nothing: no error cards, no blank screen.
//
// Styling is scoped through `.landing-root` tokens only; this module never
// imports lib/api.ts or the dashboard Layout.

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, Boxes, Coins, Cpu } from "lucide-react";
import { fetchPublicOverview, fetchPublicModels } from "../lib/publicApi";
import type { PublicModel } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";
import { ModelCapabilityIcons } from "../components/ModelCapabilityIcons";

// Family logo detection for models whose provider has no brand PNG of its own
// (e.g. relay/custom providers). Each family slug maps to an existing PNG in
// frontend/public/providers/. No new dependency.
const FAMILY_RULES: [RegExp, string][] = [
  [/claude/, "anthropic"],
  [/gpt|dall-e|whisper|text-embedding|(^|[^a-z])o[134](-|$)/, "openai"],
  [/gemini|gemma|palm|learnlm/, "gemini"],
  [/deepseek/, "deepseek"],
  [/kimi|moonshot/, "kimi"],
  [/qwen/, "qwen"],
  [/minimax/, "minimax"],
  [/glm/, "glm"],
  [/grok/, "xai"],
  [/mistral|codestral|pixtral|mixtral/, "mistral"],
  [/nemotron/, "nvidia"],
  [/sonar/, "perplexity"],
  [/qoder/, "qoder"],
  [/mimo/, "xiaomi-mimo"],
  [/command-[ra]/, "cohere"],
];

const familySlug = (modelId: string): string | null => {
  const m = modelId.toLowerCase();
  for (const [re, slug] of FAMILY_RULES) if (re.test(m)) return slug;
  return null;
};

// Tries the provider's own brand PNG first, then the detected model family.
// Falls back to a generic icon so a missing logo never leaves an empty box.
function ProviderLogo({ providerId, modelId }: { providerId: string; modelId: string }) {
  const candidates = useMemo(() => {
    const list = [`/providers/${providerId}.png`];
    const family = familySlug(modelId);
    if (family && family !== providerId) list.push(`/providers/${family}.png`);
    return list;
  }, [providerId, modelId]);
  const [idx, setIdx] = useState(0);
  return (
    <span className="flex h-9 w-9 shrink-0 items-center justify-center text-[var(--green)]">
      {idx < candidates.length ? (
        <img
          src={candidates[idx]}
          alt=""
          className="h-full w-full object-contain"
          loading="lazy"
          onError={() => setIdx((i) => i + 1)}
        />
      ) : (
        <Cpu className="h-6 w-6" aria-hidden="true" />
      )}
    </span>
  );
}

const fmtInt = new Intl.NumberFormat("id-ID");
const fmtCompact = new Intl.NumberFormat("id-ID", { notation: "compact", maximumFractionDigits: 1 });
const fmtRate = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 6 })}`;

const fmtCount = (n: number) => (n > 0 ? fmtInt.format(n) : "0");
const fmtShort = (n: number) => (n > 0 ? fmtCompact.format(n) : "0");

const WA_URL = "https://wa.me/84826240052";

const MODEL_PAGE = 24;

function Metric({
  icon,
  label,
  value,
  sub,
  hero = false,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  sub?: React.ReactNode;
  hero?: boolean;
}) {
  return (
    <div
      className="rounded-2xl border border-[var(--line)] p-5"
      style={hero ? { background: "linear-gradient(110deg, var(--soft), var(--paper))" } : undefined}
    >
      <div className="flex items-center gap-2 text-[var(--muted)]">
        <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-[var(--soft)] text-[var(--green)]" aria-hidden="true">
          {icon}
        </span>
        <span className="text-[11px] uppercase tracking-[0.7px]">{label}</span>
      </div>
      <p
        className="mt-3 font-[650] tabular-nums text-[var(--ink)]"
        style={{ fontSize: "clamp(23px,2.5vw,38px)", lineHeight: 1.1 }}
      >
        {value}
      </p>
      {sub && <div className="mt-2 text-xs text-[var(--muted)]">{sub}</div>}
    </div>
  );
}

function ModelCard({ model }: { model: PublicModel }) {
  return (
    <div className="flex flex-col rounded-2xl border border-[var(--line)] bg-[var(--paper)] p-4">
      <div className="flex items-start gap-3">
        <ProviderLogo providerId={model.provider_id} modelId={model.model_id} />
        <div className="min-w-0">
          <p className="truncate text-[15px] font-[650] text-[var(--ink)]">{model.name}</p>
          <p className="text-[11px] text-[var(--muted)]">{model.provider}</p>
        </div>
      </div>
      <ModelCapabilityIcons capabilities={model.capabilities} className="my-2" bare />
      <p className="break-all font-mono text-[10px] text-[var(--muted)]">{model.model_id}</p>
      <div className="mt-3 grid grid-cols-2 gap-x-3 gap-y-2 border-t border-[var(--line)] pt-3 text-xs">
        <div>
          <p className="text-[10px] uppercase tracking-[0.5px] text-[var(--muted)]">Input / 1M</p>
          <p className="tabular-nums font-[650] text-[var(--ink)]">{fmtRate(model.input_per_m)}</p>
        </div>
        <div>
          <p className="text-[10px] uppercase tracking-[0.5px] text-[var(--muted)]">Output / 1M</p>
          <p className="tabular-nums font-[650] text-[var(--ink)]">{fmtRate(model.output_per_m)}</p>
        </div>
        <div>
          <p className="text-[10px] uppercase tracking-[0.5px] text-[var(--muted)]">Cache Read / 1M</p>
          <p className="tabular-nums font-[650] text-[var(--ink)]">{fmtRate(model.cached_per_m)}</p>
        </div>
        <div>
          <p className="text-[10px] uppercase tracking-[0.5px] text-[var(--muted)]">Cache Write / 1M</p>
          <p className="tabular-nums font-[650] text-[var(--ink)]">{fmtRate(model.cache_write_per_m)}</p>
        </div>
      </div>
    </div>
  );
}

export default function PublicLanding() {
  const overview = useQuery({
    queryKey: ["public-overview"],
    queryFn: fetchPublicOverview,
    staleTime: 30_000,
    retry: false,
  });
  const models = useQuery({
    queryKey: ["public-models"],
    queryFn: fetchPublicModels,
    staleTime: 60_000,
    retry: false,
  });

  const modelList = models.data ?? [];
  const overviewData = overview.data;
  const [modelVisible, setModelVisible] = useState(MODEL_PAGE);

  // The portal branding provider rewrites document.title when /portal mounts;
  // re-assert the landing title on mount so it survives that navigation.
  useEffect(() => {
    document.title = "Tokenizer";
  }, []);

  return (
    <PublicLayout>
      <div id="overview" className="scroll-mt-28">
        <div className="text-center">
          <h1
            className="font-[750] tracking-[-1.5px] text-[var(--ink)]"
            style={{ fontSize: "clamp(38px,7vw,76px)", lineHeight: 1.02 }}
          >
            TOKENIZER
          </h1>
          <p className="mt-3 text-base text-[var(--muted)] sm:text-lg">Layanan PAYG AI Frontier</p>
        </div>

        <div className="mt-8 grid gap-4 md:grid-cols-3">
          <Metric
            icon={<Activity className="h-5 w-5" />}
            label="Total Request"
            value={fmtShort(overviewData?.total_requests ?? 0)}
            sub={<>Total request sepanjang waktu</>}
          />
          <Metric
            icon={<Coins className="h-5 w-5" />}
            label="Token"
            value={fmtCount(overviewData?.total_tokens ?? 0)}
            hero
            sub={<>total sepanjang waktu</>}
          />
          <Metric
            icon={<Boxes className="h-5 w-5" />}
            label="Model List"
            value={fmtCount(overviewData?.model_count ?? 0)}
            sub={<>model tersedia</>}
          />
        </div>
      </div>

      <section id="models" className="mt-14 scroll-mt-28">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <div className="flex items-center gap-2">
            <Cpu className="h-5 w-5 text-[var(--green)]" />
            <h2 className="text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">
              Model &amp; harga
            </h2>
          </div>
          <p className="text-xs text-[var(--muted)]">{fmtCount(modelList.length)} model tersedia</p>
        </div>
        <p className="mt-2 max-w-2xl text-sm text-[var(--muted)]">
          Model yang tersedia di gateway, diurutkan berdasarkan popularitas. Harga per 1 juta token.
        </p>
        {modelList.length === 0 ? (
          <p className="mt-6 text-sm text-[var(--muted)]">Belum ada model tersedia.</p>
        ) : (
          <>
            <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {modelList.slice(0, modelVisible).map((m) => (
                <ModelCard key={m.model_id} model={m} />
              ))}
            </div>
            {modelVisible < modelList.length && (
              <div className="mt-6 flex justify-center">
                <button
                  type="button"
                  onClick={() => setModelVisible((n) => n + MODEL_PAGE)}
                  className="rounded-xl border border-[var(--line)] px-5 py-3 text-sm font-[650] text-[var(--green)] transition-colors hover:bg-[var(--soft)]"
                >
                  Tampilkan lebih banyak ({fmtCount(modelList.length - modelVisible)} lagi)
                </button>
              </div>
            )}
          </>
        )}
      </section>

      <div className="mt-14 grid gap-4 md:grid-cols-2">
        <section id="purchase" className="scroll-mt-28 rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Beli / top up</p>
          <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">Tambah saldo</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Pengisian saldo dilakukan secara manual. Hubungi kami melalui WhatsApp untuk nominal dan metode
            pembayaran. Setelah pembayaran dikonfirmasi, saldo langsung aktif.
          </p>
          <a
            href={WA_URL}
            target="_blank"
            rel="noreferrer"
            className="mt-4 inline-flex items-center gap-1.5 rounded-xl bg-[var(--green)] px-4 py-3 text-sm font-[650] text-[var(--on-accent)] no-underline hover:opacity-90"
          >
            Hubungi WhatsApp ↗
          </a>
        </section>

        <section id="balance" className="scroll-mt-28 rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Saldo / akun</p>
          <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">Cek saldo</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Saldo dan limit terpakai terlihat dari portal API key masing-masing pengguna. Hubungi kami bila
            saldo tidak sesuai setelah top up.
          </p>
          <a
            href={WA_URL}
            target="_blank"
            rel="noreferrer"
            className="mt-4 inline-flex items-center gap-1.5 rounded-xl border border-[var(--line)] px-4 py-3 text-sm font-[650] text-[var(--green)] no-underline hover:bg-[var(--soft)]"
          >
            Tanya saldo ↗
          </a>
        </section>
      </div>
    </PublicLayout>
  );
}
