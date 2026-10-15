import { ListOrdered, Repeat2, type LucideIcon } from "lucide-react";
import type { Chain, Provider } from "../../lib/api";
// Explicit extension: the node:test runner (npm test) resolves ESM literally.
import { ICONS } from "../../lib/icons.ts";

export const CHAIN_MODEL_KIND = "llm";

export type ChainStrategy = "priority" | "round_robin" | "latency" | "cost";

export const CACHE_WRITE_FACTOR = 1.25;
export const CACHE_READ_FACTOR = 0.1;

export interface DraftChainStep {
  id: string;
  provider: string;
  model: string;
  marketSlug: string;
  inputPerM: number;
  outputPerM: number;
  cacheWritePerM: number;
  cacheReadPerM: number;
}

// deriveCacheRates recomputes cache write/read from the input rate, but keeps a
// field the operator edited by hand. A field is "auto" when it is unset (0) or
// still equals the formula result for the previous input; a manual override is
// left untouched.
export const deriveCacheRates = (
  input: number,
  prev: { cacheWritePerM: number; cacheReadPerM: number; prevInput?: number },
): { cacheWritePerM: number; cacheReadPerM: number } => {
  const oldInput = prev.prevInput ?? input;
  const autoWrite = prev.cacheWritePerM === 0 || prev.cacheWritePerM === +(oldInput * CACHE_WRITE_FACTOR).toFixed(8);
  const autoRead = prev.cacheReadPerM === 0 || prev.cacheReadPerM === +(oldInput * CACHE_READ_FACTOR).toFixed(8);
  return {
    cacheWritePerM: autoWrite ? +(input * CACHE_WRITE_FACTOR).toFixed(8) : prev.cacheWritePerM,
    cacheReadPerM: autoRead ? +(input * CACHE_READ_FACTOR).toFixed(8) : prev.cacheReadPerM,
  };
};

export const isRoundRobinStrategy = (strategy: string) =>
  strategy === "round_robin" || strategy === "round-robin";

export const normalizeChainStrategy = (strategy: string): ChainStrategy => {
  if (isRoundRobinStrategy(strategy)) return "round_robin";
  if (strategy === "latency" || strategy === "cost") return strategy;
  return "priority";
};

export const strategyLabel = (strategy: string) => {
  switch (normalizeChainStrategy(strategy)) {
    case "round_robin": return "Round robin";
    case "latency": return "Latency";
    case "cost": return "Cost";
    default: return "Priority";
  }
};

// strategyIcon gives each strategy one glyph, used by the editor picker and
// the strategy badge in the chain list.
export const strategyIcon = (strategy: string): LucideIcon => {
  switch (normalizeChainStrategy(strategy)) {
    case "round_robin": return Repeat2;
    case "latency": return ICONS.latency;
    case "cost": return ICONS.spend;
    default: return ListOrdered;
  }
};

// strategyDescription is the one-line explanation shown under the strategy
// picker. Keep it short: the route preview shows the actual order.
export const strategyDescription = (strategy: string) => {
  switch (normalizeChainStrategy(strategy)) {
    case "round_robin": return "Rotates the first model on each request.";
    case "latency": return "Fastest measured model first; unmeasured ones after.";
    case "cost": return "Cheapest priced model first; unpriced ones after.";
    default: return "Tries models in the order listed.";
  }
};

export const isLLMProvider = (provider: Provider) =>
  !provider.service_kinds?.length || provider.service_kinds.includes(CHAIN_MODEL_KIND);

export const providerIcon = (provider?: Provider, providerID?: string) =>
  provider?.icon || (providerID ? `/providers/${providerID}.png` : "");

export const makeDraftStep = (step?: {
  provider: string;
  model: string;
  market_slug?: string;
}): DraftChainStep => ({
  id: crypto.randomUUID(),
  provider: step?.provider ?? "",
  model: step?.model ?? "",
  marketSlug: step?.market_slug ?? "",
  inputPerM: 0,
  outputPerM: 0,
  cacheWritePerM: 0,
  cacheReadPerM: 0,
});

export const toDraftSteps = (chain?: Chain) =>
  chain?.steps.map((step) => makeDraftStep(step)) ?? [makeDraftStep()];

export const isValidChainName = (name: string) => /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(name);
