// Shared model-catalog presentational pieces used by the landing page and the
// dedicated /model page. Extracted verbatim from PublicLanding so both surfaces
// render identical cards and provider filters. No new dependency.

import { useMemo, useState } from "react";
import { Check, Copy, Cpu, Search } from "lucide-react";
import { ModelCapabilityIcons } from "./ModelCapabilityIcons";
import type { PublicModel } from "../lib/publicApi";
import { familySlug, resolveProvider } from "../lib/providerResolve";

export { familySlug, resolveProvider } from "../lib/providerResolve";

// Provider facets in the order models arrive (already sorted by popularity), so
// the busiest vendor leads. Deduplicated by resolved id.
export function buildProviders(models: PublicModel[]): { id: string; label: string }[] {
  const seen = new Map<string, string>();
  for (const m of models) {
    const { id, label } = resolveProvider(m);
    if (!seen.has(id)) seen.set(id, label);
  }
  return [...seen.entries()].map(([id, label]) => ({ id, label }));
}

// Tries the provider's own brand PNG first, then the detected model family.
// Falls back to a generic icon so a missing logo never leaves an empty box.
export function ProviderLogo({ providerId, modelId }: { providerId: string; modelId: string }) {
  const candidates = useMemo(() => {
    const list = [`/providers/${providerId}.png`];
    const family = familySlug(modelId);
    if (family && family !== providerId) list.push(`/providers/${family}.png`);
    return list;
  }, [providerId, modelId]);
  const [idx, setIdx] = useState(0);
  return (
    <span className="flex h-full w-full items-center justify-center text-[var(--muted)]">
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

export const fmtRate = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 6 })}`;
export const fmtCount = (n: number) => (n > 0 ? fmtInt.format(n) : "0");
export const fmtShort = (n: number) => (n > 0 ? fmtCompact.format(n) : "0");

// Context windows read as "1M", "1,05M" or "128K". Exact binary sizes
// (1048576, 131072) round to their familiar names.
export const fmtContext = (tokens: number) => {
  const fmt = (n: number, unit: string) => `${n.toLocaleString("id-ID", { maximumFractionDigits: 2 })}${unit}`;
  if (tokens >= 1_000_000) return tokens % 1_048_576 === 0 ? fmt(tokens / 1_048_576, "M") : fmt(tokens / 1_000_000, "M");
  if (tokens >= 1_000) return tokens % 1_024 === 0 ? fmt(tokens / 1_024, "K") : fmt(tokens / 1_000, "K");
  return fmtInt.format(tokens);
};

export const MODEL_PAGE = 24;

function CopyId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {
      /* clipboard unavailable */
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      title="Salin ID model"
      aria-label={`Salin ID model ${id}`}
      className="group flex w-full min-w-0 items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--bg)] px-2 py-1.5 text-left transition-colors hover:border-[var(--line-strong)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)]"
    >
      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-[var(--ink)]">{id}</span>
      {copied ? (
        <Check className="h-3.5 w-3.5 shrink-0 text-[var(--ok)]" aria-hidden="true" />
      ) : (
        <Copy className="h-3.5 w-3.5 shrink-0 text-[var(--muted)] group-hover:text-[var(--ink)]" aria-hidden="true" />
      )}
      <span className="sr-only" aria-live="polite">{copied ? "Tersalin" : ""}</span>
    </button>
  );
}

function Price({ label, value, strong = false, official }: { label: string; value: number; strong?: boolean; official?: number | null }) {
  return (
    <div className="min-w-0">
      <p className="text-[11px] text-[var(--muted)]">{label}</p>
      <p className={`truncate font-mono tabular-nums ${strong ? "text-[15px] font-semibold text-[var(--ink)]" : "text-[12.5px] text-[var(--ink)]"}`}>
        {fmtRate(value)}
      </p>
      {official != null && official > value && (
        <p className="truncate font-mono text-[11px] tabular-nums text-[var(--muted)]">
          <span className="sr-only">Harga resmi </span>
          <s>{fmtRate(official)}</s>
        </p>
      )}
    </div>
  );
}

export function ModelCard({ model }: { model: PublicModel }) {
  const discount = model.discount_pct ?? 0;
  return (
    <article className="group flex flex-col rounded-xl border border-[var(--line)] bg-[var(--paper)] p-4 transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-[var(--line-strong)] hover:shadow-[0_8px_24px_-12px_rgba(0,0,0,0.18)]">
      <div className="flex items-start gap-3">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-[var(--line)] bg-[var(--bg)] p-1.5">
          <ProviderLogo providerId={model.provider_id} modelId={model.model_id} />
        </span>
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-[15px] font-semibold tracking-[-0.2px] text-[var(--ink)]" title={model.name}>{model.name}</h3>
          <p className="truncate text-[12px] text-[var(--muted)]">
            {resolveProvider(model).label}
            {model.context_tokens ? (
              <span title={`Konteks ${fmtInt.format(model.context_tokens)} token`}>
                {" · "}
                <span className="font-mono tabular-nums text-[var(--ink)]">{fmtContext(model.context_tokens)}</span> konteks
              </span>
            ) : null}
          </p>
        </div>
        {model.sold_out ? (
          <span className="shrink-0 rounded-md border border-[var(--line)] bg-[var(--soft)] px-2 py-0.5 text-[11px] font-medium text-[var(--muted)]">
            Sold out
          </span>
        ) : (
          <span className="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-[var(--tag-bg)] px-2 py-0.5 text-[11px] font-medium text-[var(--tag-ink)]">
            <span className="h-1.5 w-1.5 rounded-full bg-[var(--ok)]" aria-hidden="true" />
            Tersedia
          </span>
        )}
      </div>
      <ModelCapabilityIcons capabilities={model.capabilities} className="mt-3 min-h-[18px]" bare />
      <div className="mt-3">
        <CopyId id={model.model_id} />
      </div>
      {model.sold_out ? (
        <p className="mt-4 border-t border-[var(--line)] pt-3 text-[12.5px] text-[var(--muted)]">
          Belum ada penawaran yang memenuhi harga. Coba lagi beberapa saat lagi.
        </p>
      ) : (
        <>
          {discount > 0 && (
            <p
              className="-mx-4 mt-4 border-y border-[var(--line)] bg-[var(--tag-bg)] py-2 text-center font-mono text-[14px] font-bold uppercase tracking-[1.5px] tabular-nums text-[var(--tag-ink)]"
              title="Dibanding harga resmi model"
            >
              {discount}% OFF
              <span className="sr-only"> dari harga resmi</span>
            </p>
          )}
          <div className={discount > 0 ? "pt-3" : "mt-4 border-t border-[var(--line)] pt-3"}>
            <div className="grid grid-cols-2 gap-3">
              <Price label="Input / 1M token" value={model.input_per_m} strong official={discount > 0 ? model.official_input_per_m : null} />
              <Price label="Output / 1M token" value={model.output_per_m} strong official={discount > 0 ? model.official_output_per_m : null} />
            </div>
            <div className="mt-3 grid grid-cols-2 gap-3 rounded-lg bg-[var(--bg)] px-2.5 py-2">
              <Price label="Cache read / 1M" value={model.cached_per_m} />
              <Price label="Cache write / 1M" value={model.cache_write_per_m} />
            </div>
          </div>
        </>
      )}
    </article>
  );
}

export function ProviderFilterButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={`h-8 shrink-0 rounded-lg border px-3 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)] ${
        active
          ? "border-[var(--action)] bg-[var(--action)] text-[var(--action-fg)]"
          : "border-[var(--line)] bg-[var(--paper)] text-[var(--muted)] hover:border-[var(--line-strong)] hover:text-[var(--ink)]"
      }`}
    >
      {children}
    </button>
  );
}

// ModelBrowser is the searchable, provider-filtered model grid shared by the
// landing page and /model. Search matches name, model id and provider label.
export function ModelBrowser({ models, loading = false }: { models: PublicModel[]; loading?: boolean }) {
  const [visible, setVisible] = useState(MODEL_PAGE);
  const [provider, setProvider] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const providers = useMemo(() => buildProviders(models), [models]);
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return models.filter((m) => {
      const p = resolveProvider(m);
      if (provider && p.id !== provider) return false;
      if (!q) return true;
      return m.name.toLowerCase().includes(q) || m.model_id.toLowerCase().includes(q) || p.label.toLowerCase().includes(q);
    });
  }, [models, provider, query]);

  // Changing a filter re-collapses pagination so a short result list can't
  // strand the user past the end of the new set.
  const reset = () => setVisible(MODEL_PAGE);

  if (loading) {
    return (
      <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true" aria-label="Memuat model">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="h-[232px] animate-pulse rounded-xl border border-[var(--line)] bg-[var(--paper)]" />
        ))}
      </div>
    );
  }
  if (models.length === 0) {
    return <p className="mt-6 rounded-xl border border-dashed border-[var(--line)] p-8 text-center text-sm text-[var(--muted)]">Belum ada model tersedia.</p>;
  }

  return (
    <>
      <div className="mt-6 flex flex-col gap-3 lg:flex-row lg:items-center">
        <label className="relative block lg:w-72">
          <span className="sr-only">Cari model</span>
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--muted)]" aria-hidden="true" />
          <input
            type="search"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              reset();
            }}
            placeholder="Cari model atau provider…"
            className="h-9 w-full rounded-lg border border-[var(--line)] bg-[var(--paper)] pl-9 pr-3 text-[14px] text-[var(--ink)] placeholder:text-[var(--muted)] focus:border-[var(--green)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-bg)]"
          />
        </label>
        <div className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1 lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0 lg:pb-0" role="group" aria-label="Filter model berdasarkan provider">
          <ProviderFilterButton active={provider === null} onClick={() => { setProvider(null); reset(); }}>
            Semua
          </ProviderFilterButton>
          {providers.map((p) => (
            <ProviderFilterButton key={p.id} active={provider === p.id} onClick={() => { setProvider(p.id); reset(); }}>
              {p.label}
            </ProviderFilterButton>
          ))}
        </div>
      </div>
      <p className="mt-3 text-[12px] text-[var(--muted)]" aria-live="polite">
        Menampilkan {fmtCount(Math.min(visible, filtered.length))} dari {fmtCount(filtered.length)} model
      </p>
      {filtered.length === 0 ? (
        <p className="mt-4 rounded-xl border border-dashed border-[var(--line)] p-8 text-center text-sm text-[var(--muted)]">
          Tidak ada model yang cocok dengan pencarian ini.
        </p>
      ) : (
        <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {filtered.slice(0, visible).map((m) => (
            <ModelCard key={m.model_id} model={m} />
          ))}
        </div>
      )}
      {visible < filtered.length && (
        <div className="mt-6 flex justify-center">
          <button
            type="button"
            onClick={() => setVisible((n) => n + MODEL_PAGE)}
            className="h-9 rounded-lg border border-[var(--line)] bg-[var(--paper)] px-4 text-[13px] font-medium text-[var(--ink)] transition-colors hover:border-[var(--line-strong)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)]"
          >
            Tampilkan lebih banyak ({fmtCount(filtered.length - visible)} lagi)
          </button>
        </div>
      )}
    </>
  );
}
