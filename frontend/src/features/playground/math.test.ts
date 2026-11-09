import { describe, expect, it } from "vitest";
import { mayHaveMath, normalizeMath } from "./math";

describe("normalizeMath", () => {
  it("turns \\[ \\] into display math and \\( \\) into inline math", () => {
    expect(normalizeMath("Area: \\(\\pi r^2\\) and\n\\[\n  E = mc^2\n\\]")).toBe(
      "Area: $\\pi r^2$ and\n\n$$\nE = mc^2\n$$\n",
    );
  });

  it("escapes prices but keeps formulas that start with a digit", () => {
    expect(normalizeMath("It costs $5 and $10.50 a month.")).toBe("It costs \\$5 and \\$10.50 a month.");
    expect(normalizeMath("Solve $2x + 1 = 5$ for x.")).toBe("Solve $2x + 1 = 5$ for x.");
    expect(normalizeMath("From $5 to $x$")).toBe("From \\$5 to $x$");
  });

  it("leaves $$ blocks, letters after $ and escaped dollars alone", () => {
    const text = "$$\n\\sum_{i=1}^n i\n$$\nwhere $n$ is \\$3 not $3";
    expect(normalizeMath(text)).toBe("$$\n\\sum_{i=1}^n i\n$$\nwhere $n$ is \\$3 not \\$3");
  });

  it("never touches fenced or inline code", () => {
    const text = [
      "Run `echo $5 \\(x\\)` then:",
      "```bash",
      'echo "$1 costs $5" \\(not math\\)',
      "```",
      "~~~",
      "price = $9",
      "~~~",
      "Total $9",
    ].join("\n");

    expect(normalizeMath(text)).toBe(text.replace("Total $9", "Total \\$9"));
  });

  it("treats an unclosed fence as code to the end, as markdown does", () => {
    const text = 'Streaming:\n```python\nprint("$5")';
    expect(normalizeMath(text)).toBe(text);
  });

  it("leaves text without math markers untouched", () => {
    expect(mayHaveMath("plain ü text")).toBe(false);
    expect(normalizeMath("plain ü text")).toBe("plain ü text");
  });
});
