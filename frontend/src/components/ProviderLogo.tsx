import { useState } from "react";
import { cn } from "@/lib/utils";

// ProviderLogo is the single provider mark used across the dashboard. It shows
// the bundled /providers/*.png icon and falls back to a neutral monogram tile
// (never a random brand-coloured blob) when the icon is missing or fails.
export function ProviderLogo({
  icon,
  name,
  size = 20,
  className,
}: {
  icon?: string;
  name: string;
  size?: number;
  className?: string;
}) {
  const [failed, setFailed] = useState(false);
  const style = { width: size, height: size };
  if (icon && !failed) {
    return (
      <span
        className={cn("inline-flex shrink-0 items-center justify-center overflow-hidden rounded-md border border-line bg-white", className)}
        style={style}
        aria-hidden="true"
      >
        <img src={icon} alt="" className="h-full w-full object-contain p-[2px]" onError={() => setFailed(true)} />
      </span>
    );
  }
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-md border border-line bg-subtle font-mono font-medium uppercase text-fg-muted",
        className,
      )}
      style={{ ...style, fontSize: Math.max(8, Math.round(size * 0.42)) }}
      aria-hidden="true"
    >
      {monogram(name)}
    </span>
  );
}

function monogram(name: string): string {
  const words = name.replace(/[^A-Za-z0-9 ]+/g, " ").trim().split(/\s+/).filter(Boolean);
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  return (words[0] ?? "?").slice(0, 2).toUpperCase();
}
