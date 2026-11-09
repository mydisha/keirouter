import { useLayoutEffect, useRef } from "react";
import { cn } from "@/lib/utils";

/** Glide time grows with distance so a long jump reads as one motion, capped at 360ms. */
function glideDuration(distance: number): number {
  return Math.round(Math.min(360, 180 + distance * 0.45));
}

/**
 * One pill per nav that slides under the current item (`aria-current="page"`).
 * It moves with a CSS transform transition (`.nav-indicator` in index.css),
 * which the compositor runs, so rendering the next page does not stall it.
 * Render it as the first child of a positioned container that holds the items.
 */
export function NavIndicator({ className }: { className?: string }) {
  const ref = useRef<HTMLSpanElement>(null);

  useLayoutEffect(() => {
    const pill = ref.current;
    const container = pill?.parentElement;
    if (!pill || !container) return;

    let shown: Element | null = null;
    let at = { x: 0, y: 0 };

    const place = () => {
      const active = container.querySelector('[aria-current="page"]');
      if (!active) {
        pill.style.opacity = "0";
        shown = null;
        return;
      }
      const box = container.getBoundingClientRect();
      const rect = active.getBoundingClientRect();
      const to = { x: rect.left - box.left + container.scrollLeft, y: rect.top - box.top + container.scrollTop };
      pill.style.width = `${rect.width}px`;
      pill.style.height = `${rect.height}px`;
      pill.style.opacity = "1";
      if (active === shown && to.x === at.x && to.y === at.y) return;
      // First paint, or the same item shifted (collapse, resize): no glide.
      const glide = shown !== null && shown !== active;
      pill.style.transitionDuration = glide ? `${glideDuration(Math.hypot(to.x - at.x, to.y - at.y))}ms` : "0ms";
      pill.style.transform = `translate3d(${to.x}px, ${to.y}px, 0)`;
      shown = active;
      at = to;
    };

    place();
    const mutations = new MutationObserver(place);
    mutations.observe(container, { subtree: true, childList: true, attributes: true, attributeFilter: ["aria-current"] });
    const resizes = new ResizeObserver(place);
    resizes.observe(container);
    return () => {
      mutations.disconnect();
      resizes.disconnect();
    };
  }, []);

  return (
    <span
      ref={ref}
      aria-hidden="true"
      className={cn("nav-indicator pointer-events-none absolute left-0 top-0 rounded-lg opacity-0", className)}
    />
  );
}
