import { cn } from "@/lib/utils";

// BrandMark is the built-in KeiRouter mark: an ink tile with a "K" drawn as a
// routing fork, ending in two nodes. White-label installs replace it with
// their own logo image (see Layout's SidebarBrand). The orange node
// (secondary-500) is the one sanctioned use of the brand orange. Source art
// lives in assets/brand/keirouter-mark.svg; keep the geometry in sync.
export function BrandMark({ size = 22, className }: { size?: number; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn("inline-flex shrink-0 bg-primary text-primary-fg", className)}
      style={{ width: size, height: size, borderRadius: size * 0.25 }}
    >
      <svg width={size} height={size} viewBox="0 0 64 64" fill="none">
        <g stroke="currentColor" strokeWidth="6" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21 15v34" />
          <path d="M21 37l17-17" />
          <path d="M29 30l11 13" />
        </g>
        <circle cx="43" cy="16" r="6" className="fill-secondary-500" />
        <circle cx="44" cy="47" r="6" fill="currentColor" />
      </svg>
    </span>
  );
}
