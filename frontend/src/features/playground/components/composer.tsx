import { ArrowUp, Loader2, Paperclip, Square, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { PDF_TYPE, documentSummary, isPdf } from "../documents";
import DocumentChip from "./document-chip";
import { ACCEPTED_IMAGE_TYPES } from "../images";
import type { useAttachments } from "../use-attachments";

/** Images and PDFs in a paste or drop; other items (text, links) are left alone. */
export function attachableFiles(list: FileList | null | undefined): File[] {
  return Array.from(list ?? []).filter((f) => f.type.startsWith("image/") || isPdf(f));
}

/**
 * Enter sends, Shift+Enter adds a line; while a reply streams the button stops
 * it. Images and PDFs come from the attach button, a paste, or a drop on the
 * page. PDFs work with every model; images only where the model reads them.
 */
export default function Composer({
  streaming,
  disabled,
  waiting = false,
  attachments,
  vision,
  visionHint,
  blocked,
  onSend,
  onStop,
  seed,
}: {
  streaming: boolean;
  disabled: boolean;
  /** The box takes text, but Send waits (the model list is still loading). */
  waiting?: boolean;
  attachments: ReturnType<typeof useAttachments>;
  vision: boolean;
  /** Why images are off, e.g. "GPT-x can't read images." */
  visionHint: string;
  /** Why sending is refused right now, shown above the field. */
  blocked: string | null;
  onSend: (text: string) => void;
  onStop: () => void;
  /** Text put in the box from outside (a sample prompt); nonce repeats it. */
  seed?: { text: string; nonce: number };
}) {
  const [text, setText] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (seed) {
      setText(seed.text);
      box.current?.focus();
    }
  }, [seed?.nonce]);
  const empty = !text.trim() && attachments.images.length === 0 && attachments.documents.length === 0;
  const canSend = !empty && !streaming && !disabled && !waiting && !attachments.processing && !blocked;
  const submit = () => {
    if (!canSend) {
      return;
    }
    onSend(text);
    setText("");
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      {blocked && (
        <p role="alert" className="mb-2 text-sm text-bad">
          {blocked}
        </p>
      )}
      <div
        className={cn(
          "rounded-xl border border-input bg-surface shadow-paper transition-[border-color,box-shadow] duration-150 hover:border-fg-faint",
          // One quiet ring while the message box has focus, like every input;
          // the attach and send buttons draw their own.
          "has-[textarea:focus]:border-ring has-[textarea:focus]:ring-[3px] has-[textarea:focus]:ring-tone-ring",
          disabled && "opacity-50",
        )}
      >
        {attachments.items.length > 0 && (
          <ul aria-label="Attachments" className="flex flex-wrap gap-2 px-4 pt-3.5">
            {attachments.items.map((a) => (
              <li key={a.key} className="relative">
                {a.status === "error" ? (
                  <div className="flex h-14 max-w-64 items-center rounded-md border border-bad/60 py-1 pr-8 pl-2.5 text-xs">
                    <span className="line-clamp-2">
                      <span className="font-medium">{a.name}</span>: {a.error}
                    </span>
                  </div>
                ) : a.kind === "document" ? (
                  <DocumentChip
                    name={a.name}
                    detail={
                      a.status === "ready"
                        ? documentSummary(a.document)
                        : a.progress
                          ? `Reading page ${a.progress.done} of ${a.progress.total}`
                          : "Opening"
                    }
                    busy={a.status === "processing"}
                  />
                ) : (
                  <div className="flex size-14 items-center justify-center overflow-hidden rounded-md border bg-subtle">
                    {a.status === "ready" ? (
                      <img src={a.preview} alt={a.name} className="size-full object-cover" />
                    ) : (
                      <>
                        <Loader2 aria-hidden className="size-4 animate-spin" />
                        <span className="sr-only">Preparing {a.name}</span>
                      </>
                    )}
                  </div>
                )}
                <button
                  type="button"
                  onClick={() => attachments.remove(a.key)}
                  aria-label={`Remove ${a.name}`}
                  className="absolute -top-1.5 -right-1.5 flex size-6 items-center justify-center rounded-full border bg-canvas text-fg-muted shadow-paper transition-colors hover:text-fg focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden"
                >
                  <X aria-hidden className="size-3.5" />
                </button>
              </li>
            ))}
          </ul>
        )}
        <label htmlFor="playground-message" className="sr-only">
          Message
        </label>
        <textarea
          ref={box}
          id="playground-message"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              submit();
            }
          }}
          onPaste={(e) => {
            const files = attachableFiles(e.clipboardData.files);
            if (files.length > 0) {
              e.preventDefault();
              attachments.add(files, { images: vision });
            }
          }}
          placeholder="Send a message"
          rows={1}
          disabled={disabled}
          aria-describedby="playground-hint"
          className="block field-sizing-content max-h-60 min-h-12 w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-base outline-none placeholder:text-fg-muted disabled:cursor-not-allowed md:text-sm"
        />
        <div className="flex items-center justify-between px-2.5 pb-2.5">
          <input
            ref={picker}
            type="file"
            accept={[...(vision ? ACCEPTED_IMAGE_TYPES : []), PDF_TYPE].join(",")}
            multiple
            hidden
            onChange={(e) => {
              attachments.add(attachableFiles(e.target.files), {
                images: vision,
              });
              e.target.value = "";
            }}
          />
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                size="icon"
                variant="quiet"
                aria-label={vision ? "Attach images or PDFs" : "Attach PDFs"}
                aria-disabled={disabled}
                onClick={() => !disabled && picker.current?.click()}
                className="rounded-lg aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
              >
                <Paperclip aria-hidden />
              </Button>
            </TooltipTrigger>
            <TooltipContent className="max-w-64">
              {vision ? "Attach images or PDFs (or paste, or drop them here)" : `Attach PDFs. ${visionHint}`}
            </TooltipContent>
          </Tooltip>
          {streaming ? (
            <Button
              type="button"
              size="icon"
              variant="secondary"
              onClick={onStop}
              className="rounded-lg"
              aria-label="Stop the reply"
            >
              <Square aria-hidden className="size-3.5 fill-current" />
            </Button>
          ) : (
            <Button
              type="submit"
              size="icon"
              variant="primary"
              disabled={!canSend}
              aria-label="Send"
              className="rounded-lg"
            >
              <ArrowUp aria-hidden />
            </Button>
          )}
        </div>
      </div>
    </form>
  );
}
