// Vision / Reasoning / Tools capability badges.
//
// `CapabilityFlags` is a structural subset satisfied by both the admin
// `ModelCapabilities` (lib/api) and the public `PublicCapabilities`
// (lib/publicApi), so one component serves the dashboard and the landing page
// without duplicating renderers or importing the admin client type.
//
// Paths are the exact 24x24 stroke icons from design.md §7.2 (brain-turn, eye,
// wrench), rendered with `stroke-width:1.65` round caps/joins per §7 — not
// lucide, whose geometry differs.
interface CapabilityFlags {
  vision?: boolean;
  reasoning?: boolean;
  tools?: boolean;
}

const ICONS: Record<"reasoning" | "vision" | "tools", { d: string; title: string }> = {
  reasoning: {
    d: "M12 4v16M12 6C5-3 0 10 6 12c-5 5 5 13 6 5m0-11c7-9 12 4 6 6 5 5-5 13-6 5",
    title: "Reasoning — supports extended thinking",
  },
  vision: {
    d: "M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12Zm13 0a3 3 0 1 1-6 0 3 3 0 0 1 6 0",
    title: "Vision — supports image input",
  },
  tools: {
    d: "m14 6 4 4m-9 4-6 6 2 2 6-6m-2-2c-5-6 1-12 5-10l-3 3 4 4 3-3c2 4-4 10-9 6",
    title: "Tools — supports function calling",
  },
};

const STYLES: Record<"reasoning" | "vision" | "tools", string> = {
  vision: "bg-[color:var(--color-info)]/12 text-[color:var(--color-info)] dark:bg-[color:var(--color-info)]/20",
  reasoning: "bg-secondary-100 text-secondary-700 dark:bg-secondary-800/40 dark:text-secondary-200",
  tools: "bg-accent-100 text-accent-700 dark:bg-accent-800/40 dark:text-accent-200",
};

export function ModelCapabilityIcons({
  capabilities,
  size = 15,
  className = "",
}: {
  capabilities?: CapabilityFlags;
  size?: number;
  className?: string;
}) {
  if (!capabilities?.vision && !capabilities?.reasoning && !capabilities?.tools) return null;
  return (
    <span className={`inline-flex shrink-0 items-center gap-1.5 ${className}`} role="group" aria-label="Model capabilities">
      {(["reasoning", "vision", "tools"] as const).map((key) =>
        capabilities[key] ? (
          <span
            key={key}
            title={ICONS[key].title}
            aria-label={ICONS[key].title}
            className={`inline-flex h-6 w-6 items-center justify-center rounded-md ${STYLES[key]}`}
          >
            <svg
              viewBox="0 0 24 24"
              width={size}
              height={size}
              fill="none"
              stroke="currentColor"
              strokeWidth={1.65}
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d={ICONS[key].d} />
            </svg>
          </span>
        ) : null,
      )}
    </span>
  );
}
