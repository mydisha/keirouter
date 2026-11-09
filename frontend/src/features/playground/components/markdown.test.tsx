import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ToastProvider } from "@/components/Toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import MarkdownMessage from "./markdown";

const render = (content: string, streaming = false) =>
  renderToStaticMarkup(
    <ToastProvider>
      <TooltipProvider>
        <MarkdownMessage content={content} streaming={streaming} />
      </TooltipProvider>
    </ToastProvider>,
  );

describe("MarkdownMessage", () => {
  it("renders GFM task lists, strikethrough, footnotes and aligned tables", () => {
    const html = render(
      [
        "- [x] shipped",
        "- [ ] tested",
        "",
        "~~old~~ new[^1]",
        "",
        "| a | b |",
        "|:--|--:|",
        "| 1 | 2 |",
        "",
        "[^1]: the note",
      ].join("\n"),
    );

    expect(html).toMatch(/<input type="checkbox" disabled="" aria-label="Done"[^>]* checked=""\/>/);
    expect(html).toMatch(/<input type="checkbox" disabled="" aria-label="Not done"[^>]*class="[^"]*"\/>/);
    expect(html).toContain("<del>old</del>");
    expect(html).toContain('class="footnotes"');
    expect(html).toContain('style="text-align:right"');
  });

  it("shows a remote image as a link and never loads it", () => {
    const html = render("![a cat](https://tracker.test/pixel.png)");

    expect(html).not.toContain("<img");
    expect(html).toContain('href="https://tracker.test/pixel.png"');
    expect(html).toContain("Image: a cat");
  });

  it("drops images with other schemes to their alt text", () => {
    const html = render("![x](javascript:alert(1))");

    expect(html).not.toContain("javascript:");
    expect(html).not.toContain("<img");
  });

  it("renders raw HTML as text", () => {
    expect(render("<script>alert(1)</script>")).not.toContain("<script>");
  });

  it("gives fenced code a labelled header with its tools", () => {
    const html = render("```ts\nconst a = 1;\n```");

    expect(html).toContain("TypeScript");
    expect(html).toContain('aria-label="Wrap lines"');
    expect(html).toContain('aria-label="Download snippet.ts"');
    expect(html).toContain('aria-label="Copy code"');
    expect(html).toContain("const a = 1;");
  });

  it("folds long code once the reply is done, not while it streams", () => {
    const long = "```text\n" + Array.from({ length: 40 }, (_, i) => `line ${i}`).join("\n") + "\n```";

    expect(render(long)).toContain("Show all 40 lines");
    expect(render(long, true)).not.toContain("Show all");
  });

  it("shows a mermaid block as code while the reply streams", () => {
    const html = render("```mermaid\ngraph TD; A-->B\n```", true);

    expect(html).toContain("graph TD; A--&gt;B");
    expect(html).not.toContain("<img");
  });
});
