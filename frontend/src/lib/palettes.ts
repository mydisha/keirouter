/**
 * Color palette definitions for KeiRouter's theme system.
 *
 * Each palette defines an accent (primary action color) and a secondary
 * (highlight / CTA color). When applied, the palette system generates
 * full shade scales (50-900) and sets them as CSS custom properties.
 */

import { generateShades, type ShadeScale } from "./color-utils";

export interface PaletteDefinition {
  /** Unique identifier stored in settings (e.g. "sage-terra"). */
  id: string;
  /** Human-readable name shown in the picker. */
  name: string;
  /** Short description of the palette's character. */
  description: string;
  /** Core accent hex color. */
  accent: string;
  /** Core secondary hex color. */
  secondary: string;
}

/** All available palettes. Order matches the picker grid. */
export const PALETTES: PaletteDefinition[] = [
  {
    id: "kei",
    name: "KeiRouter",
    description: "Precise, neutral — signal blue with the logo's orange. The default identity.",
    accent: "#0a72ef",
    secondary: "#ec6a2c",
  },
  {
    id: "sage-terra",
    name: "Sage & Terra",
    description: "Warm, earthy, grounded — the original KeiRouter palette.",
    accent: "#6a7450",
    secondary: "#c3603a",
  },
  {
    id: "ocean",
    name: "Ocean Breeze",
    description: "Fresh, professional — blue with a coral accent.",
    accent: "#2563eb",
    secondary: "#f97316",
  },
  {
    id: "midnight",
    name: "Midnight Gold",
    description: "Premium, luxurious — deep indigo meets gold.",
    accent: "#4f46e5",
    secondary: "#d4a017",
  },
  {
    id: "forest",
    name: "Forest Amber",
    description: "Natural, vibrant — emerald green with warm amber.",
    accent: "#059669",
    secondary: "#d97706",
  },
  {
    id: "rose",
    name: "Rose Dusk",
    description: "Elegant, modern — rose paired with soft violet.",
    accent: "#be185d",
    secondary: "#6366f1",
  },
  {
    id: "lavender",
    name: "Lavender Teal",
    description: "Creative, bold — purple and teal interplay.",
    accent: "#7c3aed",
    secondary: "#0d9488",
  },
  {
    id: "mono",
    name: "Monochrome",
    description: "Clean, minimal — neutral slate and gray.",
    accent: "#374151",
    secondary: "#6b7280",
  },
  {
    id: "sunset",
    name: "Sunset Flame",
    description: "Warm, energetic — fiery orange with deep brown.",
    accent: "#ea580c",
    secondary: "#7c2d12",
  },
  {
    id: "arctic",
    name: "Arctic Frost",
    description: "Cool, modern — cyan paired with soft lavender.",
    accent: "#0891b2",
    secondary: "#a78bfa",
  },
  {
    id: "cherry",
    name: "Cherry Navy",
    description: "Strong, authoritative — classic red with navy.",
    accent: "#dc2626",
    secondary: "#1e3a5f",
  },
];

/** Lookup a palette by its ID, falling back to the default. */
export function getPalette(id: string): PaletteDefinition {
  return PALETTES.find((p) => p.id === id) ?? PALETTES[0];
}

/** Get the complete shade scales for a palette. */
export function getPaletteScales(id: string): {
  accent: ShadeScale;
  secondary: ShadeScale;
} {
  const p = getPalette(id);
  return {
    accent: generateShades(p.accent),
    secondary: generateShades(p.secondary),
  };
}