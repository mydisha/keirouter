import { Brain, Eye } from "lucide-react";
import type { ModelCapabilities } from "../lib/api";

export function ModelCapabilityIcons({
  capabilities,
  size = 15,
  className = "",
}: {
  capabilities?: ModelCapabilities;
  size?: number;
  className?: string;
}) {
  if (!capabilities?.vision && !capabilities?.reasoning) return null;
  return (
    <span className={`inline-flex shrink-0 items-center gap-1.5 ${className}`} role="group" aria-label="Model capabilities">
      {capabilities.vision && (
        <span
          title="Vision — supports image input"
          aria-label="Vision — supports image input"
          className="inline-flex h-6 w-6 items-center justify-center rounded-md bg-[color:var(--color-info)]/12 text-[color:var(--color-info)] dark:bg-[color:var(--color-info)]/20"
        >
          <Eye size={size} aria-hidden="true" />
        </span>
      )}
      {capabilities.reasoning && (
        <span
          title="Reasoning — supports extended thinking"
          aria-label="Reasoning — supports extended thinking"
          className="inline-flex h-6 w-6 items-center justify-center rounded-md border border-line bg-subtle text-fg-muted"
        >
          <Brain size={size} aria-hidden="true" />
        </span>
      )}
    </span>
  );
}
