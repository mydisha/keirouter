import type { Chain, Provider } from "../../lib/api";

export const CHAIN_MODEL_KIND = "llm";

export type ChainStrategy = "priority" | "round_robin" | "latency" | "cost";

export const CACHE_WRITE_FACTOR = 1.25;
export const CACHE_READ_FACTOR = 0.1;

export interface DraftChainStep {
  id: string;
  provider: string;
  model: string;
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

export const strategyDescription = (strategy: string) => {
  switch (normalizeChainStrategy(strategy)) {
    case "round_robin": return "Starts with a different model on each request, then falls through the remaining steps.";
    case "latency": return "Ranks measured models by response time. Models without probe data remain after measured models.";
    case "cost": return "Ranks catalogued models by price. Models without pricing remain after priced models.";
    default: return "Uses the declared order and tries the next model only when the previous one cannot serve the request.";
  }
};

export const isLLMProvider = (provider: Provider) =>
  !provider.service_kinds?.length || provider.service_kinds.includes(CHAIN_MODEL_KIND);

export const providerIcon = (provider?: Provider, providerID?: string) =>
  provider?.icon || (providerID ? `/providers/${providerID}.png` : "");

export const makeDraftStep = (step?: {
  provider: string;
  model: string;
  input_per_m?: number;
  output_per_m?: number;
  cache_write_per_m?: number;
  cache_read_per_m?: number;
}): DraftChainStep => ({
  id: crypto.randomUUID(),
  provider: step?.provider ?? "",
  model: step?.model ?? "",
  inputPerM: step?.input_per_m ?? 0,
  outputPerM: step?.output_per_m ?? 0,
  cacheWritePerM: step?.cache_write_per_m ?? 0,
  cacheReadPerM: step?.cache_read_per_m ?? 0,
});

export const toDraftSteps = (chain?: Chain) =>
  chain?.steps.map((step) => makeDraftStep(step)) ?? [makeDraftStep()];

export const isValidChainName = (name: string) => /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(name);
