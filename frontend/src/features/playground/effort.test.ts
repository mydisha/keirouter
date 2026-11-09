import { describe, expect, it } from "vitest";
import { effectiveEffort, effortOptions, reasoningEffortParam } from "./effort";
import type { PlaygroundModel } from "./types";

const model = (reasoning: boolean, canDisable: boolean): PlaygroundModel => ({
  id: "v/m",
  name: "M",
  reasoning,
  can_disable_reasoning: canDisable,
  vision: false,
  pdf: false,
});

describe("effortOptions", () => {
  it("offers nothing for a model that does not think", () => {
    expect(effortOptions(model(false, false))).toEqual([]);
    expect(effortOptions(undefined)).toEqual([]);
  });

  it("offers off only when thinking can be turned off", () => {
    expect(effortOptions(model(true, true))).toEqual(["off", "low", "medium", "high"]);
    expect(effortOptions(model(true, false))).toEqual(["low", "medium", "high"]);
  });
});

describe("effectiveEffort", () => {
  it("falls back to medium when the model cannot turn thinking off", () => {
    expect(effectiveEffort(model(true, false), "off")).toBe("medium");
    expect(effectiveEffort(model(true, true), "off")).toBe("off");
    expect(effectiveEffort(model(false, false), "high")).toBeNull();
  });
});

describe("reasoningEffortParam", () => {
  it("sends none for off and nothing for a model that does not think", () => {
    expect(reasoningEffortParam("off")).toBe("none");
    expect(reasoningEffortParam("high")).toBe("high");
    expect(reasoningEffortParam(null)).toBeNull();
  });
});
