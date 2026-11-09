import { useMemo, useRef } from "react";
import { useCanvasLoop } from "../canvas-loop";
import type { CanvasFrame } from "../canvas-loop";
import { cn } from "@/lib/utils";
import { CELL, intensity, matrix } from "../thinking-field";

/** Brightness steps: one path and one fill per step, not one per dot. */
const LEVELS = 8;
/** Opacity of a fully shown unlit cell. */
const UNLIT = 0.14;
/** How fast the braid eases to a new thinking level, per second. */
const EASE = 3;

/**
 * The empty chat's illustration: a halftone braid whose width and pace
 * follow the thinking level (`energy`, 0..1). A change of level eases in
 * rather than jumping; under reduced motion it is one still frame of the
 * chosen level. Decorative only.
 */
export default function ThinkingField({ energy, className }: { energy: number; className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  // Kept across level changes, which restart the canvas loop, so the braid
  // carries on from where it was instead of snapping back to t = 0.
  const state = useRef({ energy, start: 0, last: 0 });
  const draw = useMemo(() => {
    const buckets: number[][] = Array.from({ length: LEVELS }, () => []);
    const still = typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    return ({ ctx, width, height, ink }: CanvasFrame) => {
      const s = state.current;
      const now = performance.now() / 1000;
      s.start ||= now;
      const dt = s.last ? Math.min(0.1, now - s.last) : 0;
      s.last = now;
      s.energy = still ? energy : s.energy + (energy - s.energy) * Math.min(1, dt * EASE);
      const t = now - s.start;

      ctx.clearRect(0, 0, width, height);
      ctx.fillStyle = ink;
      buckets.forEach((b) => (b.length = 0));
      const unlit: number[] = [];
      const cols = Math.floor(width / CELL);
      const rows = Math.floor(height / CELL);
      const ox = (width - cols * CELL) / 2 + CELL / 2;
      const oy = (height - rows * CELL) / 2 + CELL / 2;
      for (let j = 0; j < rows; j++) {
        for (let i = 0; i < cols; i++) {
          const x = ox + i * CELL;
          const y = oy + j * CELL;
          const v = intensity(x, y, t, s.energy, width, height);
          if (v >= 1 / LEVELS / 2) {
            buckets[Math.min(LEVELS - 1, Math.floor(v * LEVELS))].push(x, y);
          } else if (matrix(x, y, width, height) > 0.5) {
            unlit.push(x, y);
          }
        }
      }
      ctx.globalAlpha = UNLIT;
      ctx.beginPath();
      for (let n = 0; n < unlit.length; n += 2) {
        ctx.moveTo(unlit[n] + 0.6, unlit[n + 1]);
        ctx.arc(unlit[n], unlit[n + 1], 0.6, 0, Math.PI * 2);
      }
      ctx.fill();
      buckets.forEach((dots, level) => {
        const v = (level + 1) / LEVELS;
        const r = 0.6 + 1.3 * v;
        ctx.globalAlpha = 0.25 + 0.75 * v;
        ctx.beginPath();
        for (let n = 0; n < dots.length; n += 2) {
          ctx.moveTo(dots[n] + r, dots[n + 1]);
          ctx.arc(dots[n], dots[n + 1], r, 0, Math.PI * 2);
        }
        ctx.fill();
      });
      ctx.globalAlpha = 1;
    };
  }, [energy]);
  useCanvasLoop(ref, draw, 30);

  return <canvas ref={ref} aria-hidden className={cn("pointer-events-none size-full", className)} />;
}
