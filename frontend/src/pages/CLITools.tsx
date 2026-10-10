import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type CLITool } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { ErrorBanner, Skeleton } from "../components/ui";

// Tool metadata — descriptions and logos (bundled /providers/*.png assets).
const toolMeta: Record<string, { description: string; image: string }> = {
  claude:       { description: "Anthropic's CLI coding agent", image: "/providers/claude.png" },
  codex:        { description: "OpenAI Codex CLI", image: "/providers/codex.png" },
  cline:        { description: "VS Code AI coding assistant", image: "/providers/cline.png" },
  copilot:      { description: "GitHub Copilot Chat", image: "/providers/copilot.png" },
  droid:        { description: "Factory Droid CLI", image: "/providers/droid.png" },
  openclaw:     { description: "OpenClaw agent framework", image: "/providers/openclaw.png" },
  opencode:     { description: "OpenCode multi-model agent", image: "/providers/opencode.png" },
  kilo:         { description: "Kilo Code AI assistant", image: "/providers/kilocode.png" },
  hermes:       { description: "Hermes Agent CLI", image: "/providers/hermes.png" },
  deepseek:     { description: "DeepSeek TUI", image: "/providers/deepseek-tui.png" },
  jcode:        { description: "jcode coding agent", image: "/providers/jcode.png" },
};

// Connected tools first, then installed-but-unconfigured, then the rest.
const statusRank = (t: CLITool) => (t.configured ? 0 : t.installed ? 1 : 2);

export function CLIToolsPage() {
  const tools = useQuery({
    queryKey: ["cli-tools"],
    queryFn: () => api.cliTools(),
  });

  const list = useMemo(
    () => [...(tools.data?.tools ?? [])].sort((a, b) => statusRank(a) - statusRank(b)),
    [tools.data],
  );
  const connected = list.filter((t) => t.configured).length;

  return (
    <>
      <PageHeader title="CLI tools" description="Point your coding tools at this gateway." />

      {tools.isLoading ? (
        <div className="space-y-3" aria-busy="true" aria-label="Loading CLI tools">
          <Skeleton className="h-4 w-56" />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-[68px] rounded-2xl" />
            ))}
          </div>
        </div>
      ) : tools.isError ? (
        <ErrorBanner
          message={`Couldn't load CLI tools. ${tools.error instanceof Error ? tools.error.message : ""} Reload the page to try again.`}
        />
      ) : list.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <h2 className="text-[14px] font-medium text-fg">No CLI tools available</h2>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
            This KeiRouter build doesn&apos;t ship any CLI tool integrations.
          </p>
        </div>
      ) : (
        <section aria-labelledby="cli-tools-heading">
          <div className="mb-2.5 flex items-baseline gap-2.5">
            <h2 id="cli-tools-heading" className="text-[14px] font-semibold text-fg">
              Tools
            </h2>
            <span className="text-[12.5px] tabular-nums text-fg-muted">
              {connected} of {list.length} connected
            </span>
          </div>
          <ul className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {list.map((t) => (
              <li key={t.id}>
                <ToolCard tool={t} />
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function ToolCard({ tool: t }: { tool: CLITool }) {
  return (
    <Link
      to={`/cli-tools/${t.id}`}
      className="group flex h-full items-start gap-3 rounded-2xl border border-line bg-surface px-3.5 py-3 transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    >
      <ProviderLogo icon={toolMeta[t.id]?.image} name={t.name} size={28} className="mt-0.5" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center justify-between gap-2">
          <span className="truncate text-[13px] font-medium text-fg">{t.name}</span>
          <ToolStatus installed={t.installed} configured={t.configured} />
        </span>
        <span className="mt-0.5 block truncate text-[12px] text-fg-muted">
          {toolMeta[t.id]?.description ?? t.dialect}
        </span>
      </span>
    </Link>
  );
}

function ToolStatus({ installed, configured }: { installed: boolean; configured: boolean }) {
  const s = configured
    ? { dot: "bg-ok", label: "Connected" }
    : installed
      ? { dot: "bg-warn", label: "Not configured" }
      : { dot: "bg-fg-faint", label: "Not installed" };
  return (
    <span className="inline-flex shrink-0 items-center gap-1.5 text-[12px] text-fg-muted">
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} aria-hidden="true" />
      {s.label}
    </span>
  );
}
