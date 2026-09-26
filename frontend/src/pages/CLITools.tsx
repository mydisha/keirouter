import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  TerminalSquare, ChevronRight, CheckCircle2, XCircle, CircleDot,
} from "lucide-react";
import { api } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { brandColor } from "../lib/brand-colors";
import { PageHeader } from "../components/Layout";
import { Card } from "../components/ui";

// Tool metadata — descriptions and images. Colors come from brand-colors.ts.
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

export function CLIToolsPage() {
  const navigate = useNavigate();
  const tools = useQuery({
    queryKey: ["cli-tools"],
    queryFn: () => api.cliTools(),
  });

  return (
    <>
      <PageHeader
        title="CLI Tools"
        icon={TerminalSquare}
        description="One-click configuration for coding tools, wired to this KeiRouter instance."
      />

      {tools.isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Card key={i} className="h-24 animate-pulse">&nbsp;</Card>
          ))}
        </div>
      ) : tools.isError ? (
        <Card className="px-6 py-10 text-center text-sm text-[color:var(--color-danger)]">
          Failed to load CLI tools.
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {(tools.data?.tools ?? []).map((t) => (
            <button
              key={t.id}
              onClick={() => navigate(dashboard(`/cli-tools/${t.id}`))}
              className="group flex items-center gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-5 py-4 text-left transition-colors hover:border-accent-500/40 hover:shadow-[var(--shadow-pop)]"
            >
              {/* Icon */}
              <ToolIcon id={t.id} />

              {/* Info */}
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate text-sm font-medium">{t.name}</span>
                  <StatusBadge installed={t.installed} configured={t.configured} />
                </div>
                <p className="mt-0.5 truncate text-xs text-[var(--text-muted)]">
                  {toolMeta[t.id]?.description ?? t.dialect}
                </p>
              </div>

              {/* Chevron */}
              <ChevronRight className="h-4 w-4 shrink-0 text-[var(--text-muted)] transition-transform group-hover:translate-x-0.5 group-hover:text-[var(--text)]" />
            </button>
          ))}
        </div>
      )}
    </>
  );
}

function ToolIcon({ id }: { id: string }) {
  const [errored, setErrored] = useState(false);
  const meta = toolMeta[id];
  if (errored || !meta?.image) {
    return (
      <div
        className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl text-sm font-bold text-white"
        style={{ backgroundColor: brandColor(id) }}
      >
        {id.slice(0, 2).toUpperCase()}
      </div>
    );
  }
  return (
    <img
      src={meta.image}
      alt={id}
      onError={() => setErrored(true)}
      className="h-10 w-10 shrink-0 rounded-xl object-contain"
    />
  );
}

function StatusBadge({ installed, configured }: { installed: boolean; configured: boolean }) {
  if (configured) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400">
        <CheckCircle2 className="h-3 w-3" />
        Connected
      </span>
    );
  }
  if (installed) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
        <XCircle className="h-3 w-3" />
        Not configured
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[var(--bg-subtle)] px-2 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
      <CircleDot className="h-3 w-3" />
      Not installed
    </span>
  );
}
