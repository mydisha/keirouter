import { flushSync } from "react-dom";

// Swaps the theme with every element transition held for one style flush, so
// buttons and links do not each fade on their own clock.
function swapWithoutTransitions(apply: () => void): void {
  const root = document.documentElement;
  root.classList.add("theme-switching");
  apply();
  // Commits the new colours while transitions are off; removing the class
  // afterwards changes no value, so nothing starts animating.
  void root.offsetWidth;
  root.classList.remove("theme-switching");
}

/**
 * Applies a theme change as one cross-fade of the whole page (View
 * Transitions, `::view-transition-*(root)` in index.css). Without support, or
 * under reduced motion, it swaps at once.
 */
export function switchTheme(apply: () => void): void {
  if (typeof document.startViewTransition !== "function" || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    swapWithoutTransitions(apply);
    return;
  }
  // flushSync: React must render the new toggle icon before the browser takes
  // the "after" snapshot.
  document.startViewTransition(() => swapWithoutTransitions(() => flushSync(apply)));
}
