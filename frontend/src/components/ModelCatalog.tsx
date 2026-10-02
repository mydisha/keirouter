// Shared model-catalog presentational pieces used by the landing page and the
// dedicated /model page. Extracted verbatim from PublicLanding so both surfaces
// render identical cards and provider filters. No new dependency.

import { useMemo, useState } from "react";
import { Cpu } from "lucide-react";
import { ModelCapabilityIcons } from "./ModelCapabilityIcons";
import type { PublicModel } from "../lib/publicApi";

// Family logo detection for models whose provider has no brand PNG of its own
// (e.g. relay/custom providers). Each family slug maps to an existing PNG in
// frontend/public/providers/.
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

export const familySlug = (modelId: string): string | null => {
  const m = modelId.toLowerCase();
  for (const [re, slug] of FAMILY_RULES) if (re.test(m)) return slug;
  return null;
};

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

export const fmtRate = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 6 })}`;
export const fmtCount = (n: number) => (n > 0 ? fmtInt.format(n) : "0");
export const fmtShort = (n: number) => (n > 0 ? fmtCompact.format(n) : "0");

export const MODEL_PAGE = 24;

export function ModelCard({ model }: { model: PublicModel }) {
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
      className={`rounded-xl border px-4 py-2 text-sm font-[650] transition-colors ${
        active
          ? "border-[var(--green)] bg-[var(--green)] text-[var(--on-accent)]"
          : "border-[var(--line)] text-[var(--green)] hover:bg-[var(--soft)]"
      }`}
    >
      {children}
    </button>
  );
}
