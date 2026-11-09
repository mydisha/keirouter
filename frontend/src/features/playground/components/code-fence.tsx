import { Download, WrapText } from "lucide-react";
import { useState } from "react";
import HighlightedCode from "./highlighted-code";
import { CopyButton } from "@/components/ui/code-block";
import { Button } from "@/components/ui";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { languageLabel, resolveLanguage, snippetFileName } from "../highlight";
import { cn } from "@/lib/utils";

/** Longer blocks fold to this many lines once the reply has finished. */
export const FOLD_LINES = 30;

function download(code: string, fileName: string) {
  const url = URL.createObjectURL(new Blob([code], { type: "text/plain" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  link.click();
  // Revoke on the next task: the click starts the download asynchronously.
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

function ToolButton({
  label,
  pressed,
  onClick,
  children,
}: {
  label: string;
  pressed?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="icon"
          variant="quiet"
          aria-label={label}
          aria-pressed={pressed}
          onClick={onClick}
          className="size-8 aria-pressed:bg-hover aria-pressed:text-fg"
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

/** A fenced code block in a reply: highlighted, numbered, with wrap, download and copy. */
export default function CodeFence({
  code,
  info,
  streaming = false,
}: {
  code: string;
  /** The fence's info string (`ts`, `python`), if any. */
  info: string | null;
  streaming?: boolean;
}) {
  const language = resolveLanguage(info);
  const [wrap, setWrap] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const lineCount = code.split("\n").length;
  const folded = !streaming && !expanded && lineCount > FOLD_LINES;
  const label = language ? languageLabel(language) : (info ?? "Text");
  const body = cn("p-3", !wrap && "overflow-x-auto", folded && "max-h-[calc(30lh+1.5rem)] overflow-y-hidden");

  return (
    <figure className="my-4 overflow-hidden rounded-lg border bg-code-bg">
      <figcaption className="flex h-10 items-center justify-between gap-2 border-b pr-1 pl-3">
        <span className="truncate font-mono text-xs text-fg-muted">{label}</span>
        <div className="flex shrink-0 items-center">
          <ToolButton label="Wrap lines" pressed={wrap} onClick={() => setWrap((w) => !w)}>
            <WrapText aria-hidden />
          </ToolButton>
          <ToolButton
            label={`Download ${snippetFileName(language)}`}
            onClick={() => download(code, snippetFileName(language))}
          >
            <Download aria-hidden />
          </ToolButton>
          <CopyButton value={code} label="Copy code" />
        </div>
      </figcaption>
      {language ? (
        <HighlightedCode
          code={code}
          language={language}
          live={streaming}
          lineNumbers={lineCount > 1}
          wrap={wrap}
          className={body}
        />
      ) : (
        <pre
          className={cn(
            "font-mono text-meta leading-relaxed text-(--code-foreground)",
            wrap && "break-words whitespace-pre-wrap",
            body,
          )}
        >
          <code>{code}</code>
        </pre>
      )}
      {lineCount > FOLD_LINES && !streaming && (
        <button
          type="button"
          onClick={() => setExpanded((e) => !e)}
          aria-expanded={expanded}
          className="flex h-9 w-full items-center justify-center border-t text-xs text-fg-muted transition-colors hover:bg-hover hover:text-fg focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden focus-visible:ring-inset"
        >
          {expanded ? "Show fewer lines" : `Show all ${lineCount} lines`}
        </button>
      )}
    </figure>
  );
}
