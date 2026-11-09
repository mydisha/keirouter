import { ChevronDown } from "lucide-react";
import { Button } from "@/components/ui";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui";
import { cn } from "@/lib/utils";
import { EFFORT_BARS, EFFORT_HINTS, EFFORT_LABELS, effectiveEffort, effortOptions } from "../effort";
import type { Effort, PlaygroundModel } from "../types";

/** Three bars of rising height, filled up to the level, like a signal meter. */
export function EffortBars({ level, className }: { level: number; className?: string }) {
  return (
    <svg viewBox="0 0 14 12" aria-hidden className={cn("h-3 w-3.5 shrink-0", className)}>
      {[0, 1, 2].map((i) => (
        <rect
          key={i}
          x={i * 5}
          y={8 - i * 4}
          width={4}
          height={4 + i * 4}
          rx={1}
          className={cn(
            "transition-[fill,opacity] duration-150",
            i < level ? "fill-current" : "fill-current opacity-25",
          )}
        />
      ))}
    </svg>
  );
}

/**
 * How much the model thinks before answering, as a menu that says what each
 * level trades. Shown only for models that think; "Off" only where thinking
 * really turns off on every provider.
 */
/**
 * The control's footprint while the catalog loads, so the toolbar keeps the
 * rows it will have once the model arrives.
 */
export function ThinkingControlSkeleton() {
  return <Skeleton className="h-8 w-24 sm:w-44 pointer-coarse:h-11" />;
}

export default function ThinkingControl({
  model,
  value,
  onChange,
  disabled,
}: {
  model: PlaygroundModel | undefined;
  value: Effort;
  onChange: (effort: Effort) => void;
  disabled?: boolean;
}) {
  const options = effortOptions(model);
  const current = effectiveEffort(model, value);
  if (options.length === 0 || current === null) {
    return null;
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="quiet" size="sm" disabled={disabled} aria-label={`Thinking: ${EFFORT_LABELS[current]}`}>
          <EffortBars
            level={EFFORT_BARS[current]}
            className={current === "off" ? "text-fg-muted" : "text-brand-ink"}
          />
          <span className="hidden sm:inline">Thinking</span>
          <span className="text-xs text-fg-muted">{EFFORT_LABELS[current]}</span>
          <ChevronDown aria-hidden className="text-fg-muted" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[min(calc(100vw-2rem),20rem)]">
        <DropdownMenuLabel className="space-y-0.5">
          <span className="block">Thinking before answering</span>
          <span className="block text-xs font-normal text-pretty text-fg-muted">
            More thinking gives better answers to hard questions, but takes longer and costs more: thinking is billed as
            output tokens.
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup value={current} onValueChange={(v) => onChange(v as Effort)}>
          {options.map((effort) => (
            <DropdownMenuRadioItem key={effort} value={effort} className="items-start">
              <span className="grid flex-1 gap-0.5">
                <span className="font-medium">{EFFORT_LABELS[effort]}</span>
                <span className="text-xs text-pretty text-fg-muted">{EFFORT_HINTS[effort]}</span>
              </span>
              <EffortBars level={EFFORT_BARS[effort]} className="mt-1 text-fg-muted" />
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        {!options.includes("off") && (
          <>
            <DropdownMenuSeparator />
            <p className="px-2 py-1.5 text-xs text-pretty text-fg-muted">
              {model?.name ?? "This model"} always thinks, so thinking can&rsquo;t be turned off.
            </p>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
