import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import OutputControl from "./output-control";

describe("OutputControl", () => {
  it("names the current format on the trigger", () => {
    const html = renderToStaticMarkup(
      <OutputControl value={{ mode: "schema", name: "invoice", schema: "{}" }} onChange={() => {}} />,
    );

    expect(html).toContain('aria-label="Output: invoice"');
  });

  it("flags a broken schema on the trigger", () => {
    const html = renderToStaticMarkup(
      <OutputControl value={{ mode: "schema", name: "invoice", schema: "{" }} onChange={() => {}} />,
    );

    expect(html).toContain("bg-bad");
  });
});
