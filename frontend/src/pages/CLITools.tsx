import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { RefreshCw } from "lucide-react";
import { api, type CLITool } from "../lib/api";
import { cliConnection, cliConnectionRank, cliToolMeta, detectedViaLabel } from "../lib/cliTools";
import { PageHeader } from "../components/Layout";
import { ProviderLogo } from "../components/ProviderLogo";
import { Badge, Button, ErrorBanner, SectionTitle, Skeleton, EmptyState } from "../components/ui";
import { ICONS } from "../lib/icons";
import { cn } from "@/lib/utils";
import { dashboard } from "../lib/dashboardRoutes";

export function CLIToolsPage() {
  const tools = useQuery({
    queryKey: ["cli-tools"],
    queryFn: () => api.cliTools(),
  });

  const list = useMemo(
    () =>
      [...(tools.data?.tools ?? [])].sort(
        (a, b) => cliConnectionRank[cliConnection(a).kind] - cliConnectionRank[cliConnection(b).kind] || a.name.localeCompare(b.name),
      ),
    [tools.data],
  );
  const counts = useMemo(() => {
    const c = { connected: 0, attention: 0, ready: 0, missing: 0 };
    for (const t of list) c[cliConnection(t).kind]++;
    return c;
  }, [list]);
  const rescanning = tools.isFetching && !tools.isLoading;

  return (
    <>
      <PageHeader
        title="CLI tools"
        description="Point your coding tools at this gateway."
        action={
          <Button variant="secondary" onClick={() => tools.refetch()} disabled={tools.isFetching} aria-label="Scan this machine for CLI tools again">
            <RefreshCw className={cn(rescanning && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
            {rescanning ? "Scanning…" : "Rescan"}
          </Button>
        }
      />

      {tools.isLoading ? (
        <div className="space-y-3" aria-busy="true" aria-label="Loading CLI tools">
          <Skeleton className="h-4 w-56" />
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-[84px] rounded-2xl" />
            ))}
          </div>
        </div>
      ) : tools.isError ? (
        <ErrorBanner
          message={`Couldn't load CLI tools. ${tools.error instanceof Error ? tools.error.message : ""} Reload the page to try again.`}
        />
      ) : list.length === 0 ? (
        <EmptyState
          variant="card"
          titleAs="h2"
          icon={ICONS.cliTools}
          title="No CLI tools available"
          hint="This KeiRouter build doesn't ship any CLI tool integrations."
        />
      ) : (
        <section aria-labelledby="cli-tools-heading" aria-busy={rescanning}>
          <div className="mb-3">
            <SectionTitle
              id="cli-tools-heading"
              icon={ICONS.cliTools}
              title="Tools"
              subtitle={
                <span className="tabular-nums">
                  {counts.connected} of {list.length} connected
                  {counts.attention > 0 && <span className="text-warn"> · {counts.attention} need attention</span>}
                  {counts.ready > 0 && <> · {counts.ready} installed, not configured</>}
                </span>
              }
            />
          </div>
          <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
  const conn = cliConnection(t);
  const meta = cliToolMeta[t.id];
  const via = detectedViaLabel(t);
  // Second line: the problem when there is one, else what was detected.
  const facts = [t.version && `v${t.version}`, via, t.settings?.model].filter(Boolean).join(" · ");
  return (
    <Link
      to={dashboard(`/cli-tools/${t.id}`)}
      className={cn(
        "group flex h-full items-start gap-3 rounded-2xl border bg-surface px-3.5 py-3 transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
        conn.kind === "attention" ? "border-warn/35" : "border-line",
      )}
    >
      <ProviderLogo icon={meta?.image} name={t.name} size={28} className="mt-0.5" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center justify-between gap-2">
          <span className="truncate text-[13px] font-medium text-fg">{t.name}</span>
          <Badge tone={conn.tone}>{conn.label}</Badge>
        </span>
        <span className="mt-0.5 block truncate text-[12px] text-fg-muted">{meta?.description ?? t.dialect}</span>
        {conn.kind === "attention" ? (
          <span className="mt-1 block truncate text-[12px] text-warn" title={conn.detail}>
            {conn.detail}
          </span>
        ) : (
          facts && (
            <span className="mt-1 block truncate font-mono text-[11.5px] text-fg-faint" title={t.binary_path || t.extension || t.config_path}>
              {facts}
            </span>
          )
        )}
      </span>
    </Link>
  );
}
