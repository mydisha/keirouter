// CodeBlock is the one surface for every snippet, config file and JSON body in
// the dashboard: syntax-highlighted (see lib/highlight), monospace, scrollable
// on its own, with a copy button that confirms in place.
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";
import { detectLanguage, tokenize, type CodeLanguage } from "@/lib/highlight";
import { useToast } from "../Toast";

const LANGUAGE_LABEL: Record<CodeLanguage, string> = {
  bash: "Shell",
  env: "Env",
  json: "JSON",
  toml: "TOML",
  yaml: "YAML",
  python: "Python",
  js: "JavaScript",
  text: "Text",
};

/** Renders `code` as highlighted spans. Use inside your own <pre>/<code>. */
export const HighlightedCode = memo(function HighlightedCode({ code, language }: { code: string; language: CodeLanguage }) {
  const tokens = useMemo(() => tokenize(code, language), [code, language]);
  return (
    <>
      {tokens.map((t, i) => (t.type === "plain" ? t.value : <span key={i} className={`tok-${t.type}`}>{t.value}</span>))}
    </>
  );
});

/**
 * CopyButton copies `value` and swaps its icon for a check for a moment, so
 * the click is confirmed where the pointer is. `toast` additionally raises a
 * toast (useful when the button is icon-only and far from the value).
 */
export function CopyButton({
  value,
  label = "Copy",
  showLabel = false,
  toast: toastTitle,
  className,
  size = "md",
}: {
  value: string;
  /** Accessible name, e.g. "Copy base URL". */
  label?: string;
  /** Show "Copy" / "Copied" text next to the icon. */
  showLabel?: boolean;
  /** Toast title shown on success, e.g. "Base URL copied". */
  toast?: string;
  className?: string;
  size?: "sm" | "md";
}) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 1600);
      if (toastTitle) toast.success(toastTitle, value.length < 120 ? value : undefined);
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access. Select the text and copy it manually.");
    }
  };

  const box = size === "sm" ? "h-7 min-w-7" : "h-8 min-w-8";
  return (
    <button
      type="button"
      onClick={copy}
      disabled={!value}
      aria-label={showLabel ? undefined : label}
      title={showLabel ? undefined : label}
      className={cn(
        "inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg px-1.5 text-[12px] font-medium text-fg-muted transition-[background-color,color,transform] duration-150 hover:bg-hover hover:text-fg active:scale-95 disabled:pointer-events-none disabled:opacity-40",
        "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [@media(pointer:coarse)]:h-9 [@media(pointer:coarse)]:min-w-9",
        box,
        copied && "text-ok hover:text-ok",
        className,
      )}
    >
      <span className="relative inline-flex h-3.5 w-3.5" aria-hidden="true">
        <Copy
          className={cn("absolute inset-0 h-3.5 w-3.5 transition-[opacity,transform] duration-150", copied ? "scale-50 opacity-0" : "scale-100 opacity-100")}
          strokeWidth={1.75}
        />
        <Check
          className={cn("absolute inset-0 h-3.5 w-3.5 transition-[opacity,transform] duration-150", copied ? "scale-100 opacity-100" : "scale-50 opacity-0")}
          strokeWidth={2.25}
        />
      </span>
      {showLabel && <span>{copied ? "Copied" : label}</span>}
      <span className="sr-only" role="status">
        {copied ? "Copied to clipboard" : ""}
      </span>
    </button>
  );
}

/**
 * CodeBlock: a highlighted, copyable snippet.
 *
 * - `language` defaults to a guess from `filename` / the content.
 * - `title` (or `filename`) renders a header bar with the language badge and
 *   the copy button; without one the copy button floats top-right.
 * - `wrap` soft-wraps long lines (logs, JSON bodies) instead of scrolling.
 */
export function CodeBlock({
  code,
  language,
  filename,
  title,
  actions,
  copy = true,
  copyToast,
  copyValue,
  wrap = false,
  bare = false,
  className,
  bodyClassName,
  "aria-label": ariaLabel,
}: {
  code: string;
  language?: CodeLanguage;
  filename?: string;
  title?: ReactNode;
  /** Extra header controls, rendered before the copy button. */
  actions?: ReactNode;
  copy?: boolean;
  /** Toast title on copy (e.g. "Snippet copied"). */
  copyToast?: string;
  /** What the copy button copies when it differs from what is shown (e.g. a truncated body). */
  copyValue?: string;
  wrap?: boolean;
  /** No border / radius: for embedding inside an existing card section. */
  bare?: boolean;
  className?: string;
  /** Classes for the scrolling <pre>, e.g. a max height. */
  bodyClassName?: string;
  "aria-label"?: string;
}) {
  const lang = language ?? detectLanguage(code, filename);
  const heading = title ?? (filename ? <span className="font-mono">{filename}</span> : null);
  const name = ariaLabel ?? (typeof heading === "string" ? heading : filename) ?? `${LANGUAGE_LABEL[lang]} snippet`;

  return (
    <div
      className={cn(
        "group/code relative min-w-0 overflow-hidden bg-[var(--code-bg)] text-[var(--code-fg)]",
        !bare && "rounded-lg border border-line",
        className,
      )}
    >
      {heading != null && (
        <div className="flex min-h-9 items-center gap-2 border-b border-line bg-subtle py-1 pl-3.5 pr-1.5">
          <span className="min-w-0 flex-1 truncate text-[12px] font-medium text-fg-muted">{heading}</span>
          {lang !== "text" && (
            <span className="rounded-md border border-line bg-surface px-1.5 font-mono text-[10.5px] uppercase leading-[18px] tracking-wide text-fg-faint">
              {LANGUAGE_LABEL[lang]}
            </span>
          )}
          {actions}
          {copy && <CopyButton value={copyValue ?? code} label={`Copy ${typeof heading === "string" ? heading : LANGUAGE_LABEL[lang].toLowerCase()}`} toast={copyToast} size="sm" />}
        </div>
      )}
      <pre
        tabIndex={0}
        role="region"
        aria-label={name}
        className={cn(
          "overflow-auto px-3.5 py-3 font-mono text-[12.5px] leading-[1.65] focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500",
          wrap ? "whitespace-pre-wrap break-words [overflow-wrap:anywhere]" : "whitespace-pre",
          heading == null && copy && "pr-12",
          bodyClassName,
        )}
      >
        <code>
          <HighlightedCode code={code} language={lang} />
        </code>
      </pre>
      {heading == null && copy && (
        <div className="absolute right-1.5 top-1.5">
          <CopyButton value={copyValue ?? code} label={`Copy ${LANGUAGE_LABEL[lang].toLowerCase()} snippet`} toast={copyToast} size="sm" className="bg-[var(--code-bg)]" />
        </div>
      )}
    </div>
  );
}
