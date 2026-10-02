// Dedicated model catalog at `/model` — the landing page's model list has its
// own full page. Reads the read-only public models endpoint via TanStack Query
// and shares cards/filters with the landing hero via components/ModelCatalog.
// Styling is scoped through `.landing-root` tokens only.

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Cpu } from "lucide-react";
import { fetchPublicModels } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";
import {
  ModelCard, ProviderFilterButton, MODEL_PAGE, fmtCount,
} from "../components/ModelCatalog";

export default function PublicModels() {
  const models = useQuery({
    queryKey: ["public-models"],
    queryFn: fetchPublicModels,
    staleTime: 60_000,
    retry: false,
  });

  const modelList = models.data ?? [];
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

  useEffect(() => {
    document.title = "Tokenizer - Model";
  }, []);

  return (
    <PublicLayout>
      <section className="scroll-mt-28">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <div className="flex items-center gap-2">
            <Cpu className="h-5 w-5 text-[var(--green)]" />
            <h1 className="text-[26px] font-[650] tracking-[-0.6px] text-[var(--ink)]">
              Model &amp; harga
            </h1>
          </div>
          <p className="text-xs text-[var(--muted)]">{fmtCount(modelList.length)} model tersedia</p>
        </div>
        <p className="mt-2 max-w-2xl text-sm text-[var(--muted)]">
          Model yang tersedia di gateway, diurutkan berdasarkan popularitas. Harga per 1 juta token.
        </p>

        {models.isLoading ? (
          <p className="mt-6 text-sm text-[var(--muted)]">Memuat model…</p>
        ) : modelList.length === 0 ? (
          <p className="mt-6 text-sm text-[var(--muted)]">Belum ada model tersedia.</p>
        ) : (
          <>
            <div className="mt-6 flex flex-wrap gap-2" role="group" aria-label="Filter model berdasarkan provider">
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
              <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
    </PublicLayout>
  );
}