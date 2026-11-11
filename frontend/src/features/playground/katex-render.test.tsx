import Markdown from "react-markdown";
import { renderToStaticMarkup } from "react-dom/server";
import rehypeKatex from "rehype-katex";
import remarkMath from "remark-math";
import { afterEach, describe, expect, it } from "vitest";
import { KATEX_OPTIONS } from "./math";

const render = (content: string) =>
  renderToStaticMarkup(
    <Markdown remarkPlugins={[remarkMath]} rehypePlugins={[[rehypeKatex, KATEX_OPTIONS]]}>
      {content}
    </Markdown>,
  );

describe("KaTeX rendering of model replies", () => {
  afterEach(() => {
    delete (Object.prototype as Record<string, unknown>).trust;
  });

  it("renders inline and display math as KaTeX HTML plus MathML", () => {
    const html = render("Euler: $e^{i\\pi} + 1 = 0$\n\n$$\n\\int_0^1 x^2\\,dx = \\tfrac{1}{3}\n$$");

    expect(html).toContain('class="katex"');
    expect(html).toContain('class="katex-display"');
    expect(html).toContain("<math");
  });

  it("renders invalid TeX as source instead of throwing", () => {
    const html = render("$\\frac{1}{$");

    expect(html).toContain("katex-error");
  });

  it("never turns \\href into a link", () => {
    const html = render("$\\href{javascript:alert(1)}{click}$");

    expect(html).not.toMatch(/<a\b/);
    expect(html).not.toContain("href=");
  });

  it("keeps \\href untrusted when Object.prototype is polluted with trust", () => {
    (Object.prototype as Record<string, unknown>).trust = true;

    const html = render("$\\href{https://evil.example}{click}$");

    expect(html).not.toMatch(/<a\b/);
    expect(html).not.toContain("href=");
  });
});
