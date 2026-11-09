import { ImageOff, ImagePlus } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useLayoutEffect, useState } from "react";
import type { RefObject } from "react";
import { cn } from "@/lib/utils";

type Frame = { top: number; left: number; width: number; height: number };

/**
 * Covers the conversation (below the model toolbar, which stays readable so
 * the person sees which model the drop is for) while files hover the page.
 * The column scrolls with the window, so the frame is measured, not inset.
 */
export default function DropZone({
  tone,
  count,
  title,
  detail,
  meta,
  column,
  toolbar,
}: {
  tone: "accept" | "reject";
  /** Images the drop would attach, drawn as up to three stacked tiles. */
  count: number;
  title: string;
  detail: string | null;
  meta: string | null;
  column: RefObject<HTMLElement | null>;
  toolbar: RefObject<HTMLElement | null>;
}) {
  const [frame, setFrame] = useState<Frame | null>(null);

  useLayoutEffect(() => {
    const c = column.current?.getBoundingClientRect();
    if (!c) {
      return;
    }
    const top = Math.max(c.top, toolbar.current?.getBoundingClientRect().bottom ?? 0);
    const bottom = Math.min(c.bottom, window.innerHeight);
    setFrame({
      top,
      left: c.left,
      width: c.width,
      height: Math.max(0, bottom - top),
    });
  }, [column, toolbar]);

  if (!frame) {
    return null;
  }

  return (
    <div
      aria-hidden
      data-testid="drop-zone"
      style={frame}
      className="pointer-events-none fixed z-40 animate-overlay-in p-3"
    >
      <div
        className={cn(
          "flex h-full flex-col items-center justify-center rounded-xl border-[1.5px] border-dashed px-6 text-center",
          tone === "accept" ? "border-brand bg-brand-soft" : "border-input bg-subtle",
        )}
      >
        <Tiles tone={tone} count={count} />
        <p className="mt-5 text-title font-medium text-balance">{title}</p>
        {detail && <p className="mt-1.5 max-w-sm text-sm text-pretty text-fg-muted">{detail}</p>}
        {meta && <p className="mt-4 font-mono text-xs text-balance text-fg-muted">{meta}</p>}
      </div>
    </div>
  );
}

/** Spread apart on arrival: the drop is picked up, and how many it holds. */
const FAN = [
  { x: 0, y: 0 },
  { x: -18, y: 6 },
  { x: 18, y: 6 },
];

function Tiles({ tone, count }: { tone: "accept" | "reject"; count: number }) {
  const reduced = useReducedMotion();
  const shown = tone === "accept" ? Math.min(Math.max(count, 1), 3) : 1;
  const Icon = tone === "accept" ? ImagePlus : ImageOff;

  return (
    <div className="relative h-14 w-12">
      {FAN.slice(0, shown)
        .map((to, i) => (
          <motion.div
            key={i}
            initial={reduced || i === 0 ? false : { x: 0, y: 0 }}
            animate={to}
            transition={{
              duration: 0.22,
              ease: [0.22, 1, 0.36, 1],
            }}
            className={cn(
              "absolute inset-0 grid place-items-center rounded-md border bg-surface shadow-paper",
              i > 0 && "scale-90",
            )}
          >
            {i === 0 && (
              <Icon
                aria-hidden
                className={cn("size-5", tone === "accept" ? "text-brand-ink" : "text-fg-muted")}
              />
            )}
          </motion.div>
        ))
        .reverse()}
    </div>
  );
}
