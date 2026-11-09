import type { ComponentProps } from "react";
import { Dialog as SheetPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

// Sheet is a Radix dialog that slides in from an edge: the mobile navigation
// drawer today, request-detail and edit panels later.
//
// Right-side sheets are full width on phones by default; pass a max width for
// larger screens (e.g. `sm:max-w-[640px]`, or `max-w-[640px]`, which is a
// no-op below that width). Both sides pad for the device safe areas.

export const Sheet = SheetPrimitive.Root;
export const SheetTrigger = SheetPrimitive.Trigger;
export const SheetClose = SheetPrimitive.Close;

export function SheetContent({
  className,
  side = "left",
  children,
  ...props
}: ComponentProps<typeof SheetPrimitive.Content> & { side?: "left" | "right" }) {
  return (
    <SheetPrimitive.Portal>
      <SheetPrimitive.Overlay className="fixed inset-0 z-50 bg-overlay data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out" />
      <SheetPrimitive.Content
        className={cn(
          "fixed inset-y-0 z-50 flex max-w-full flex-col bg-surface pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-lift outline-none ease-out-soft data-[state=closed]:duration-150 data-[state=open]:duration-250",
          "data-[state=open]:animate-in data-[state=closed]:animate-out",
          side === "left"
            ? "left-0 border-r border-line data-[state=open]:slide-in-from-left data-[state=closed]:slide-out-to-left"
            : "right-0 w-full border-l border-line data-[state=open]:slide-in-from-right data-[state=closed]:slide-out-to-right",
          className,
        )}
        {...props}
      >
        {children}
      </SheetPrimitive.Content>
    </SheetPrimitive.Portal>
  );
}

export const SheetTitle = SheetPrimitive.Title;
export const SheetDescription = SheetPrimitive.Description;
