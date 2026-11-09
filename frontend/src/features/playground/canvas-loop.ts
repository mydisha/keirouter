import { useEffect } from "react";
import type { RefObject } from "react";

export interface CanvasFrame {
  ctx: CanvasRenderingContext2D;
  /** Canvas size in CSS pixels; the context is already scaled for DPR. */
  width: number;
  height: number;
  /** The element's text colour, for drawing in the current theme's ink. */
  ink: string;
  /** Seconds since the canvas mounted. */
  t: number;
}

/**
 * Runs `draw` on a decorative canvas at `fps` while it is visible, redraws
 * it on resize and theme change, holds a single frame under reduced motion
 * and stops while the tab or the element is hidden.
 */
export function useCanvasLoop(
  ref: RefObject<HTMLCanvasElement | null>,
  draw: (frame: CanvasFrame) => void,
  fps: number,
) {
  useEffect(() => {
    const canvas = ref.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) {
      return;
    }

    const still = window.matchMedia("(prefers-reduced-motion: reduce)");
    let width = 0;
    let height = 0;
    let frame = 0;
    let last = 0;
    let visible = true;
    // Read once per theme change, not per frame: getComputedStyle forces
    // a style recalc every call.
    let ink = getComputedStyle(canvas).color;
    const start = performance.now();

    const paint = (now: number) => draw({ ctx, width, height, ink, t: (now - start) / 1000 });

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      width = canvas.clientWidth;
      height = canvas.clientHeight;
      canvas.width = Math.round(width * dpr);
      canvas.height = Math.round(height * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      paint(performance.now());
    };

    const tick = (now: number) => {
      frame = requestAnimationFrame(tick);
      if (now - last < 1000 / fps) {
        return;
      }
      last = now;
      paint(now);
    };

    const sync = () => {
      cancelAnimationFrame(frame);
      if (visible && !document.hidden && !still.matches) {
        frame = requestAnimationFrame(tick);
      }
    };

    const theme = new MutationObserver(() => {
      ink = getComputedStyle(canvas).color;
      paint(performance.now());
    });
    theme.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    const sizes = new ResizeObserver(resize);
    sizes.observe(canvas);
    const seen = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      sync();
    });
    seen.observe(canvas);
    document.addEventListener("visibilitychange", sync);
    still.addEventListener("change", sync);
    resize();
    sync();

    return () => {
      cancelAnimationFrame(frame);
      theme.disconnect();
      sizes.disconnect();
      seen.disconnect();
      document.removeEventListener("visibilitychange", sync);
      still.removeEventListener("change", sync);
    };
  }, [ref, draw, fps]);
}
