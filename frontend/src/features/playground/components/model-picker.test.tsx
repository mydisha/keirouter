import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import ModelPicker from "./model-picker";

describe("ModelPicker", () => {
  it("holds the picker’s place while the model list loads", () => {
    const html = renderToStaticMarkup(<ModelPicker models={undefined} value="" onChange={() => {}} />);

    expect(html).toContain("Loading models");
    expect(html).toContain("skeleton-shimmer");
    expect(html).not.toContain('role="combobox"');
  });

  it("is the combobox once the list is there", () => {
    const html = renderToStaticMarkup(<ModelPicker models={[]} value="" onChange={() => {}} />);

    expect(html).toContain('role="combobox"');
    expect(html).toContain("Choose a model");
  });
});
