// Dedicated model catalog at `/model`. Reads the read-only public models
// endpoint via TanStack Query and shares the searchable grid with the landing
// page via components/ModelCatalog. Styling is scoped through `.landing-root`
// tokens only.

import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchPublicModels } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";
import { ModelBrowser, fmtCount } from "../components/ModelCatalog";

export default function PublicModels() {
  const models = useQuery({
    queryKey: ["public-models"],
    queryFn: fetchPublicModels,
    staleTime: 60_000,
    // Prices follow the market sync; the server caches this response for
    // 12s, so a 15s poll picks up each change without hitting the DB per tab.
    refetchInterval: 15_000,
    retry: false,
  });
  const modelList = models.data ?? [];
  const available = modelList.filter((m) => !m.sold_out).length;

  useEffect(() => {
    document.title = "Tokenizer - Model";
  }, []);

  return (
    <PublicLayout>
      <section>
        <p className="font-mono text-[11px] uppercase tracking-[1.2px] text-[var(--green)]">Katalog</p>
        <div className="mt-2 flex flex-wrap items-end justify-between gap-3">
          <h1 className="text-[28px] font-semibold tracking-[-0.6px] text-[var(--ink)] sm:text-[34px]">Model &amp; harga</h1>
          <p className="inline-flex items-center gap-2 text-[13px] text-[var(--muted)]">
            <span className="landing-live-dot h-1.5 w-1.5 rounded-full bg-[var(--ok)]" aria-hidden="true" />
            {fmtCount(available)} dari {fmtCount(modelList.length)} model tersedia
          </p>
        </div>
        <p className="mt-2 max-w-2xl text-[15px] leading-relaxed text-[var(--muted)]">
          Semua model yang bisa kamu pakai, diurutkan dari yang paling sering dipakai. Harga dalam USD per 1 juta token
          dan bisa berubah mengikuti pasar. Klik ID model untuk menyalinnya.
        </p>
        <ModelBrowser models={modelList} loading={models.isLoading} />
      </section>
    </PublicLayout>
  );
}
