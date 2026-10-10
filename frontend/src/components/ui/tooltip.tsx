import { useId, useState, type ComponentProps, type ReactNode } from "react";
import { Tooltip as TooltipPrimitive } from "radix-ui";
import { Info } from "lucide-react";
import { cn } from "@/lib/utils";

export const TooltipProvider = TooltipPrimitive.Provider;
export const TooltipTrigger = TooltipPrimitive.Trigger;

// Tooltip carries its own provider so it works anywhere without an app-level
// <TooltipProvider>. Tooltips open on hover and keyboard focus, close on
// Escape, and stay open while the pointer moves onto them (WCAG 1.4.13).
export function Tooltip({ delayDuration = 200, ...props }: ComponentProps<typeof TooltipPrimitive.Root>) {
  return (
    <TooltipPrimitive.Provider delayDuration={delayDuration}>
      <TooltipPrimitive.Root {...props} />
    </TooltipPrimitive.Provider>
  );
}

export function TooltipContent({
  className,
  sideOffset = 6,
  ...props
}: ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        sideOffset={sideOffset}
        className={cn(
          "z-[110] max-w-xs rounded-lg bg-primary px-2 py-1 text-[12px] leading-snug text-primary-fg shadow-[var(--shadow-pop)]",
          "animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0",
          className,
        )}
        {...props}
      />
    </TooltipPrimitive.Portal>
  );
}

/**
 * InfoTip is the small (i) button placed next to a label for an explanation
 * that is too long for a hint line. It is a real button (focusable, 24×24
 * target) named "More about {label}"; the text is also its accessible
 * description so screen readers get it without opening the tooltip.
 *
 *   <span className="flex items-center gap-1">Alert threshold <InfoTip label="Alert threshold">Notify when…</InfoTip></span>
 */
export function InfoTip({
  label,
  children,
  side = "top",
  className,
}: {
  /** What the tip explains, used in the button's name. */
  label: string;
  children: ReactNode;
  side?: "top" | "right" | "bottom" | "left";
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const descId = useId();
  return (
    <>
      <Tooltip open={open} onOpenChange={setOpen}>
        <TooltipTrigger asChild>
          <button
            type="button"
            aria-label={`More about ${label}`}
            aria-describedby={descId}
            className={cn(
              "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
              className,
            )}
            // Touch has no hover: a tap toggles the tooltip.
            onClick={(e) => {
              e.preventDefault();
              setOpen((v) => !v);
            }}
          >
            <Info className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent side={side}>{children}</TooltipContent>
      </Tooltip>
      <span id={descId} hidden>
        {children}
      </span>
    </>
  );
}
