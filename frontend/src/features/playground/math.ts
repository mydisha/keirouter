/**
 * rehype-katex options for model replies. `trust: false` keeps \href and
 * \url from making links; errors render as the source instead of throwing.
 */
export const KATEX_OPTIONS = {
  throwOnError: false,
  strict: "ignore",
  trust: false,
  output: "htmlAndMathml",
} as const;

const FENCE = /^ {0,3}(`{3,}|~{3,})/;

/** True when a reply may hold math, so KaTeX is worth loading. */
export function mayHaveMath(text: string): boolean {
  return /\$|\\\(|\\\[/.test(text);
}

/**
 * A `$` before a digit is a price ("$5 and $10") unless a closing `$` on the
 * same line ends what reads as a formula ("$2x + 1 = 5$"): escape prices so
 * remark-math leaves them alone.
 */
function escapePrices(line: string): string {
  let out = "";
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (ch === "\\") {
      out += ch + (line[i + 1] ?? "");
      i++;
      continue;
    }
    if (ch !== "$") {
      out += ch;
      continue;
    }
    if (line[i + 1] === "$") {
      out += "$$";
      i++;
      continue;
    }
    if (!/\d/.test(line[i + 1] ?? "")) {
      out += ch;
      continue;
    }
    const close = line.indexOf("$", i + 1);
    const isFormula = close > 0 && line[close - 1] !== " " && !/[\d\w]/.test(line[close + 1] ?? "");
    if (isFormula) {
      out += line.slice(i, close + 1);
      i = close;
    } else {
      out += "\\$";
    }
  }

  return out;
}

/** Prose outside inline code: `\(…\)`, `\[…\]` and prices. */
function normalizeProse(text: string): string {
  return text
    .split(/(`+[^`]*`+)/)
    .map((part, i) => {
      if (i % 2 === 1) {
        return part;
      }

      return part
        .replace(/\\\[([\s\S]+?)\\\]/g, (_, body: string) => `\n$$\n${body.trim()}\n$$\n`)
        .replace(/\\\(([\s\S]+?)\\\)/g, (_, body: string) => `$${body.trim()}$`)
        .split("\n")
        .map(escapePrices)
        .join("\n");
    })
    .join("");
}

/**
 * Rewrites the math delimiters models use into the ones remark-math reads,
 * leaving fenced and inline code untouched.
 */
export function normalizeMath(markdown: string): string {
  if (!mayHaveMath(markdown)) {
    return markdown;
  }
  const out: string[] = [];
  let prose: string[] = [];
  let fence: string | null = null;
  const flush = () => {
    if (prose.length > 0) {
      out.push(normalizeProse(prose.join("\n")));
      prose = [];
    }
  };
  for (const line of markdown.split("\n")) {
    const marker = FENCE.exec(line)?.[1];
    if (fence === null && marker) {
      flush();
      fence = marker;
      out.push(line);
    } else if (fence !== null) {
      out.push(line);
      if (marker && marker[0] === fence[0] && marker.length >= fence.length && line.trim() === marker) {
        fence = null;
      }
    } else {
      prose.push(line);
    }
  }
  flush();

  return out.join("\n");
}
