import { describe, expect, it } from "vitest";
import { SYSTEM_PRESETS } from "./system-presets";

describe("system prompt presets", () => {
  it("have unique, short labels and non-empty text", () => {
    const labels = SYSTEM_PRESETS.map((p) => p.label);

    expect(new Set(labels).size).toBe(labels.length);
    for (const preset of SYSTEM_PRESETS) {
      expect(preset.label.length).toBeLessThanOrEqual(16);
      expect(preset.text.trim().length).toBeGreaterThan(40);
    }
  });

  it("spell out the JSON shape it asks for", () => {
    const json = SYSTEM_PRESETS.find((p) => p.label === "JSON extractor");
    const shape = json?.text.match(/\{.*\}/)?.[0] ?? "";

    expect(shape).toContain('"request"');
  });
});
