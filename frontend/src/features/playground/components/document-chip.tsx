import { FileText, Loader2 } from "lucide-react";

import { cn } from "@/lib/utils";

/** A PDF as a small card: its name and one line on what was read from it. */
export default function DocumentChip({
  name,
  detail,
  busy = false,
  className,
}: {
  name: string;
  detail: string;
  busy?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex h-14 w-56 items-center gap-2.5 rounded-md border bg-subtle py-1 pr-7 pl-2.5 text-left",
        className,
      )}
    >
      <span className="flex size-8 shrink-0 items-center justify-center rounded-sm bg-surface text-fg-muted shadow-paper">
        {busy ? <Loader2 aria-hidden className="size-3.5 animate-spin" /> : <FileText aria-hidden className="size-4" />}
      </span>
      <span className="min-w-0 text-xs">
        <span className="block truncate font-medium">{name}</span>
        <span className="tnum block truncate text-fg-muted" aria-live={busy ? "polite" : undefined}>
          {detail}
        </span>
      </span>
    </div>
  );
}
