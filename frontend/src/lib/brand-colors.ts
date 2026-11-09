// Shared brand color map for CLI tools and providers.
// Single source of truth — import in CLITools, CLIToolDetail, MediaProviderDetail, etc.
//
// Values reference CSS custom properties defined in index.css (--color-brand-*).
// Fallback is ink-400 (warm neutral) for unknown tools.

const FALLBACK = "var(--color-neutral-500)";

const RAW: Record<string, string> = {
  claude: "var(--color-brand-claude)",
  codex: "var(--color-brand-codex)",
  cline: "var(--color-brand-cline)",
  copilot: "var(--color-brand-copilot)",
  droid: "var(--color-brand-droid)",
  openclaw: "var(--color-brand-openclaw)",
  opencode: "var(--color-brand-opencode)",
  kilo: "var(--color-brand-kilo)",
  hermes: "var(--color-brand-hermes)",
  deepseek: "var(--color-brand-deepseek)",
  jcode: "var(--color-brand-jcode)",
};

/** Get a brand color by tool/provider id. Case-insensitive, falls back to warm neutral. */
export function brandColor(id: string): string {
  return RAW[id.toLowerCase()] ?? FALLBACK;
}

/** All known brand colors for iteration (e.g., legend rendering). */
export const BRAND_COLORS = RAW;
