import { ChevronRight, CircleAlert, Loader2, RotateCcw, TriangleAlert } from "lucide-react";
import { useId, useState } from "react";
import { ProviderLogo } from "@/components/ProviderLogo";
import { Button } from "@/components/ui";
import { CopyButton } from "@/components/ui/code-block";
import { formatMs, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";
import { EFFORT_LABELS } from "../effort";
import { prettyJson } from "../output-format";
import type { AssistantMessage as Message, PlaygroundModel } from "../types";
import CodeFence from "./code-fence";
import MarkdownMessage from "./markdown";

function Thinking({ message }: { message: Message }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const thinking = message.status === "streaming" && message.content === "";
  const label = thinking
    ? "Thinking"
    : message.thinkingMs !== null
      ? `Thought for ${formatMs(message.thinkingMs)}`
      : "Thinking";

  return (
    <div className="mb-3">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((o) => !o)}
        className="press -ml-1 inline-flex h-8 items-center gap-1.5 rounded-md px-1 text-meta text-fg-muted hover:text-fg focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none pointer-coarse:h-11"
      >
        <ChevronRight
          aria-hidden
          className={cn("size-4 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")}
        />
        {label}
        {thinking && <Loader2 aria-hidden className="size-3.5 animate-spin" />}
      </button>
      {open && (
        <div id={id} className="mt-1 border-l-2 pl-4 text-sm whitespace-pre-wrap text-fg-muted">
          {message.reasoning}
        </div>
      )}
    </div>
  );
}

function Receipt({ message }: { message: Message }) {
  const parts: string[] = [];
  if (message.usage) {
    parts.push(`${formatTokens(message.usage.prompt)} in`);
    parts.push(
      message.usage.reasoning > 0
        ? `${formatTokens(message.usage.completion)} out (${formatTokens(message.usage.reasoning)} thinking)`
        : `${formatTokens(message.usage.completion)} out`,
    );
  }
  if (message.latencyMs !== null) {
    parts.push(formatMs(message.latencyMs));
  }

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs text-fg-muted">
      {parts.length > 0 && <span className="tnum">{parts.join(" · ")}</span>}
      {message.status === "stopped" && <span className="font-sans">Stopped</span>}
      {message.requestId && <span title="Request id">{message.requestId}</span>}
    </div>
  );
}

/** One reply: who answered, how it thought, the answer and its receipt. */
export default function AssistantMessage({
  message,
  models,
  isLast,
  canRegenerate,
  onRegenerate,
}: {
  message: Message;
  models: PlaygroundModel[];
  isLast: boolean;
  canRegenerate: boolean;
  onRegenerate: () => void;
}) {
  const shown = message.served ?? message.model;
  // A served model is the upstream name ("gpt-4o"); the list holds "openai/gpt-4o".
  const model = models.find((m) => m.id === shown || m.id.endsWith(`/${shown}`)) ?? models.find((m) => m.id === message.model);
  const streaming = message.status === "streaming";

  return (
    <article aria-label={`Reply from ${model?.name ?? shown}`} aria-busy={streaming} className="group/reply">
      <div className="mb-2 flex items-center gap-2 text-meta text-fg-muted">
        <ProviderLogo icon={model?.icon} name={model?.provider_name ?? model?.name ?? shown} />
        <span className="font-medium text-fg">{model?.name ?? shown}</span>
        {message.served && message.served !== message.model && <span>(fallback for {message.model})</span>}
        {message.effort && <span>· {EFFORT_LABELS[message.effort]} thinking</span>}
      </div>

      {message.reasoning !== "" && <Thinking message={message} />}

      {message.content !== "" ? (
        message.output ? (
          <JsonReply content={message.content} streaming={streaming} />
        ) : (
          <MarkdownMessage content={message.content} streaming={message.status === "streaming"} />
        )
      ) : (
        streaming &&
        message.reasoning === "" && (
          <p className="flex items-center gap-2 text-sm text-fg-muted">
            <Loader2 aria-hidden className="size-3.5 animate-spin" />
            Waiting for the first token
          </p>
        )
      )}

      {message.status === "error" && message.error && (
        <div role="alert" className="mt-3 flex items-start gap-2.5 rounded-xl bg-bad-soft px-3.5 py-2.5 text-[13px] text-bad">
          <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0" />
          <span>{message.error.message}</span>
        </div>
      )}

      {!streaming && (
        <div className="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1">
          {message.content !== "" && <CopyButton value={message.content} label="Copy reply" />}
          {isLast && (
            <Button variant="quiet" size="sm" onClick={onRegenerate} disabled={!canRegenerate}>
              <RotateCcw aria-hidden />
              {message.status === "error" ? "Try again" : "Regenerate"}
            </Button>
          )}
          <Receipt message={message} />
        </div>
      )}
    </article>
  );
}

/**
 * A reply asked for as JSON: indented in a code block with copy and download,
 * or the raw text with a note when the model did not return JSON.
 */
function JsonReply({ content, streaming }: { content: string; streaming: boolean }) {
  const pretty = streaming ? null : prettyJson(content);
  if (streaming || pretty !== null) {
    return <CodeFence code={pretty ?? content} info="json" streaming={streaming} />;
  }

  return (
    <>
      <p className="mb-2 flex items-center gap-1.5 text-xs text-warn">
        <TriangleAlert aria-hidden className="size-3.5" />
        This reply is not valid JSON.
      </p>
      <MarkdownMessage content={content} streaming={false} />
    </>
  );
}
