import { cn } from "@/lib/utils";

// BrandMark is the built-in KeiRouter mark: an ink tile with a routing fork
// and the logo's orange dot. White-label installs replace it with their own
// logo image (see Layout's SidebarBrand). The orange dot (secondary-500) is
// the one sanctioned use of the brand orange.
export function BrandMark({ size = 22, className }: { size?: number; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn("relative inline-flex shrink-0 items-center justify-center rounded-md bg-primary text-primary-fg", className)}
      style={{ width: size, height: size }}
    >
      <svg width={size * 0.64} height={size * 0.64} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3 8h4l3-4h3" />
        <path d="M7 8l3 4h3" />
      </svg>
      <span
        className="absolute -right-0.5 -top-0.5 rounded-full bg-secondary-500 ring-2 ring-surface"
        style={{ width: Math.round(size * 0.32), height: Math.round(size * 0.32) }}
      />
    </span>
  );
}
