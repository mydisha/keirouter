import type { Chain, Provider } from "../../lib/api";

export const CHAIN_MODEL_KIND = "llm";

export type ChainStrategy = "priority" | "round_robin" | "latency" | "cost";

export interface DraftChainStep {
  id: string;
  provider: string;
  model: string;
}

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

export const makeDraftStep = (step?: { provider: string; model: string }): DraftChainStep => ({
  id: crypto.randomUUID(),
  provider: step?.provider ?? "",
  model: step?.model ?? "",
});

export const toDraftSteps = (chain?: Chain) =>
  chain?.steps.map((step) => makeDraftStep(step)) ?? [makeDraftStep()];

export const isValidChainName = (name: string) => /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(name);
