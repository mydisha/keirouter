import { useEffect, useRef, useState } from "react";
import { tokenize } from "../highlight";
import type { CodeLanguage, CodeLine } from "../highlight";
import { cn } from "@/lib/utils";

const ITALIC = 1;
const BOLD = 2;
/** While code streams in, tokenize at most this often. */
const LIVE_INTERVAL_MS = 120;

type Tokens = { code: string; lines: CodeLine[] };

/**
 * Lines to draw: the tokens when they match the code, else the tokens of the
 * part that has not changed plus the rest as plain text (code that is still
 * streaming only grows), else plain text.
 */
export function displayLines(code: string, tokens: Tokens | null): CodeLine[] {
  if (tokens?.code === code) {
    return tokens.lines;
  }
  if (!tokens || !code.startsWith(tokens.code) || tokens.lines.length === 0) {
    return code.split("\n").map((content) => [{ content }]);
  }
  const [first, ...rest] = code.slice(tokens.code.length).split("\n");
  const lines = tokens.lines.slice();
  lines[lines.length - 1] = [...lines[lines.length - 1], { content: first }];

  return [...lines, ...rest.map((content) => [{ content }])];
}

/**
 * Renders the plain code first (server render, no layout shift) and swaps in
 * Shiki tokens once the block is near the viewport and the highlighter has
 * loaded, so a sample below the fold costs nothing on first paint. Tokens
 * become React spans, never injected HTML. Line numbers are CSS counters, so
 * copying the text leaves them out.
 */
export default function HighlightedCode({
  code,
  language,
  live = false,
  lineNumbers = false,
  wrap = false,
  className,
  ...props
}: {
  code: string;
  language: CodeLanguage;
  /** The code is still streaming in: highlight on an interval, not every change. */
  live?: boolean;
  lineNumbers?: boolean;
  wrap?: boolean;
  className?: string;
} & Omit<React.ComponentProps<"pre">, "children">) {
  const [tokens, setTokens] = useState<Tokens | null>(null);
  const lastRun = useRef(0);
  const ref = useRef<HTMLPreElement>(null);
  const [near, setNear] = useState(false);

  useEffect(() => {
    const element = ref.current;

    if (!element || near) {
      return;
    }

    const observer = new IntersectionObserver(([entry]) => entry?.isIntersecting && setNear(true), {
      rootMargin: "300px 0px",
    });
    observer.observe(element);

    return () => observer.disconnect();
  }, [near]);

  useEffect(() => {
    if (!near) {
      return;
    }

    let active = true;
    const run = () => {
      lastRun.current = Date.now();
      tokenize(code, language)
        .then((lines) => active && setTokens({ code, lines }))
        .catch(() => {});
    };
    if (!live) {
      run();

      return () => {
        active = false;
      };
    }
    const timer = window.setTimeout(run, Math.max(0, LIVE_INTERVAL_MS - (Date.now() - lastRun.current)));

    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [near, code, language, live]);

  const lines = displayLines(code, tokens);

  return (
    <pre
      ref={ref}
      className={cn(
        "font-mono text-meta leading-relaxed text-(--code-foreground)",
        wrap && "break-words whitespace-pre-wrap",
        className,
      )}
      {...props}
    >
      <code data-language={language} className={cn(lineNumbers && "[counter-reset:line]")}>
        {lines.map((line, i) => (
          <span
            key={i}
            className={cn(
              lineNumbers &&
                "[counter-increment:line] before:mr-4 before:inline-block before:w-[3ch] before:text-right before:text-(--code-token-comment) before:content-[counter(line)] before:select-none",
            )}
          >
            {line.map((token, j) => (
              <span
                key={j}
                style={token.color ? { color: token.color } : undefined}
                className={cn(
                  token.fontStyle && token.fontStyle & ITALIC && "italic",
                  token.fontStyle && token.fontStyle & BOLD && "font-medium",
                )}
              >
                {token.content}
              </span>
            ))}
            {i < lines.length - 1 && "\n"}
          </span>
        ))}
      </code>
    </pre>
  );
}
