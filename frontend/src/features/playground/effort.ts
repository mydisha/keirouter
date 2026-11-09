import type { Effort, PlaygroundModel } from "./types";

export const EFFORT_LABELS: Record<Effort, string> = {
  off: "Off",
  low: "Low",
  medium: "Medium",
  high: "High",
};

/** What each level trades, in the words the thinking menu shows. */
export const EFFORT_HINTS: Record<Effort, string> = {
  off: "Answers right away. Fastest and cheapest.",
  low: "A quick look before answering.",
  medium: "Balanced for most questions.",
  high: "Thinks longest. For math, code and multi-step problems.",
};

/** Filled bars (out of 3) in the level glyph. */
export const EFFORT_BARS: Record<Effort, number> = {
  off: 0,
  low: 1,
  medium: 2,
  high: 3,
};

/** The thinking levels a model offers; none for a model that does not think. */
export function effortOptions(model: PlaygroundModel | undefined): Effort[] {
  if (!model?.reasoning) {
    return [];
  }

  return model.can_disable_reasoning ? ["off", "low", "medium", "high"] : ["low", "medium", "high"];
}

/** The chosen level if the model offers it, else medium; null when it does not think. */
export function effectiveEffort(model: PlaygroundModel | undefined, chosen: Effort): Effort | null {
  const options = effortOptions(model);
  if (options.length === 0) {
    return null;
  }

  return options.includes(chosen) ? chosen : "medium";
}

/** `reasoning_effort` for the request; the gateway maps it per provider. */
export function reasoningEffortParam(effort: Effort | null): string | null {
  if (effort === null) {
    return null;
  }

  return effort === "off" ? "none" : effort;
}
