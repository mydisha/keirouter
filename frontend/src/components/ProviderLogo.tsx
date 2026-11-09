import { cn } from "@/lib/utils";
import { markFor, markKey } from "./provider-mark/marks";
import spriteUrl from "./provider-mark/sprite.svg?url";

// ProviderLogo is the single provider (and CLI tool) mark across the
// dashboard: the vendor's LobeHub mark drawn in currentColor, so it follows
// the ink and the theme. Ids without a vendored mark get a monogram. From 28px
// the mark sits on a paper tile (page headers, connect dialogs); smaller, it
// stands bare next to the name, as icons do.
export function ProviderLogo({
  icon,
  name,
  size = 16,
  className,
  label,
}: {
  /** The gateway's icon path ("/providers/<id>.png"); its basename picks the mark. */
  icon?: string;
  name: string;
  size?: number;
  className?: string;
  /** Expose the logo to assistive tech with this name. By default it is
   *  decorative, because the provider name is written next to it. */
  label?: string;
}) {
  const key = markKey(icon);
  const id = key ? markFor(key) : undefined;
  const tiled = size >= 28;
  const glyph = tiled ? Math.round(size * 0.55) : size;
  const a11y = label ? ({ role: "img", "aria-label": label } as const) : ({ "aria-hidden": true } as const);

  const inner = id ? (
    <svg viewBox="0 0 24 24" width={glyph} height={glyph} className="shrink-0" aria-hidden="true">
      {/* One cached sprite (scripts/provider-sprite.mjs) instead of the markup
          inlined into the bundle. */}
      <use href={`${spriteUrl}#${id}`} />
    </svg>
  ) : (
    <span
      className={cn("inline-flex shrink-0 items-center justify-center font-medium uppercase leading-none", !tiled && "rounded-[4px] bg-subtle")}
      style={{ width: glyph, height: glyph, fontSize: Math.max(9, Math.round(glyph * 0.6)) }}
    >
      {name.trim().charAt(0) || "?"}
    </span>
  );

  if (!tiled) {
    return (
      <span className={cn("inline-flex shrink-0 items-center justify-center text-fg", !id && "text-fg-muted", className)} style={{ width: size, height: size }} {...a11y}>
        {inner}
      </span>
    );
  }
  return (
    <span
      className={cn("inline-flex shrink-0 items-center justify-center rounded-xl border border-line bg-surface text-fg shadow-paper", !id && "text-fg-muted", className)}
      style={{ width: size, height: size }}
      {...a11y}
    >
      {inner}
    </span>
  );
}
