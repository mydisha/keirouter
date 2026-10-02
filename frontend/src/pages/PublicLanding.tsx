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
import { PublicLayout } from "../components/PublicLayout";
import {
  ModelCard, ProviderFilterButton, MODEL_PAGE, fmtCount, fmtShort,
} from "../components/ModelCatalog";

const WA_URL = "https://wa.me/84826240052";

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
  const [providerFilter, setProviderFilter] = useState<string | null>(null);

  // Providers in the order models arrive (already sorted by popularity), so the
  // busiest provider leads. Keyed by provider_id; label falls back to the id.
  const providers = useMemo(() => {
    const seen = new Map<string, string>();
    for (const m of modelList) {
      if (!seen.has(m.provider_id)) seen.set(m.provider_id, m.provider || m.provider_id);
    }
    return [...seen.entries()].map(([id, label]) => ({ id, label }));
  }, [modelList]);

  const filteredModels = useMemo(
    () => (providerFilter ? modelList.filter((m) => m.provider_id === providerFilter) : modelList),
    [modelList, providerFilter],
  );

  // Changing the filter re-collapses pagination so a short result list can't
  // strand the user past the end of the new set.
  const selectProvider = (id: string | null) => {
    setProviderFilter(id);
    setModelVisible(MODEL_PAGE);
  };

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
            <div className="mt-5 flex flex-wrap gap-2" role="group" aria-label="Filter model berdasarkan provider">
              <ProviderFilterButton active={providerFilter === null} onClick={() => selectProvider(null)}>
                Semua
              </ProviderFilterButton>
              {providers.map((p) => (
                <ProviderFilterButton
                  key={p.id}
                  active={providerFilter === p.id}
                  onClick={() => selectProvider(p.id)}
                >
                  {p.label}
                </ProviderFilterButton>
              ))}
            </div>
            {filteredModels.length === 0 ? (
              <p className="mt-6 text-sm text-[var(--muted)]">Belum ada model untuk provider ini.</p>
            ) : (
              <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {filteredModels.slice(0, modelVisible).map((m) => (
                  <ModelCard key={m.model_id} model={m} />
                ))}
              </div>
            )}
            {modelVisible < filteredModels.length && (
              <div className="mt-6 flex justify-center">
                <button
                  type="button"
                  onClick={() => setModelVisible((n) => n + MODEL_PAGE)}
                  className="rounded-xl border border-[var(--line)] px-5 py-3 text-sm font-[650] text-[var(--green)] transition-colors hover:bg-[var(--soft)]"
                >
                  Tampilkan lebih banyak ({fmtCount(filteredModels.length - modelVisible)} lagi)
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
