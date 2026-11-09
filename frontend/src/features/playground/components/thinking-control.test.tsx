import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { PlaygroundModel } from "../types";
import ThinkingField from "./thinking-field";
import ThinkingControl, { EffortBars, ThinkingControlSkeleton } from "./thinking-control";

const model = (reasoning: boolean): PlaygroundModel => ({
  id: "v/m",
  name: "M",
  reasoning,
  can_disable_reasoning: true,
  vision: false,
  pdf: false,
});

describe("ThinkingControl", () => {
  it("names the current level on the trigger", () => {
    const html = renderToStaticMarkup(<ThinkingControl model={model(true)} value="high" onChange={() => {}} />);

    expect(html).toContain('aria-label="Thinking: High"');
  });

  it("renders nothing for a model that does not think", () => {
    const html = renderToStaticMarkup(<ThinkingControl model={model(false)} value="high" onChange={() => {}} />);

    expect(html).toBe("");
  });

  it("holds its place with a hidden placeholder while models load", () => {
    const html = renderToStaticMarkup(<ThinkingControlSkeleton />);

    expect(html).toContain("skeleton-shimmer");
    expect(html).toContain('aria-hidden="true"');
  });

  it("fills one bar per level", () => {
    const html = renderToStaticMarkup(<EffortBars level={2} />);

    expect(html.match(/opacity-25/g)).toHaveLength(1);
  });
});

describe("ThinkingField", () => {
  it("renders a decorative canvas on the server", () => {
    const html = renderToStaticMarkup(<ThinkingField energy={0.5} className="ink" />);

    expect(html).toBe('<canvas aria-hidden="true" class="pointer-events-none size-full ink"></canvas>');
  });
});
