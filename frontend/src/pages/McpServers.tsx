import { useEffect, useId, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, Plus, RefreshCw, Search, Trash2, X } from "lucide-react";
import { api, type MCPPreset, type MCPServer, type MCPServerInput } from "../lib/api";
import { cn } from "@/lib/utils";
import { formatRelative } from "../lib/format";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";
import { Button, EmptyState, ErrorBanner, Modal, SectionTitle, Skeleton, Toggle } from "../components/ui";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";
import { ICONS } from "../lib/icons";
import { ServerForm } from "../components/mcp/ServerForm";
import { hostOf, ServerMark, ServerStatusBadge } from "../components/mcp/shared";
import { ProviderLogo } from "../components/ProviderLogo";

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-ring";

type Filter = "all" | "failing" | "off";

const FILTERS: { value: Filter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "failing", label: "Failing" },
  { value: "off", label: "Disabled" },
];

export function McpServersPage() {
  const fid = useId();
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [picking, setPicking] = useState(false);
  const [preset, setPreset] = useState<MCPPreset | "custom" | null>(null);

  const servers = useQuery({ queryKey: ["mcp-servers"], queryFn: api.mcpServers });
  const catalog = useQuery({ queryKey: ["mcp-catalog"], queryFn: api.mcpCatalog, staleTime: 5 * 60_000 });
  const stats = useQuery({ queryKey: ["mcp-stats", "24h"], queryFn: () => api.mcpStats("24h") });

  const openAdd = () => {
    const presets = catalog.data?.presets ?? [];
    if (presets.length === 0 && !catalog.data?.catalog_only) setPreset("custom");
    else setPicking(true);
  };

  // The command palette opens the add flow with ?add=1.
  useEffect(() => {
    if (params.get("add") === "1" && catalog.data) {
      openAdd();
      setParams((p) => {
        p.delete("add");
        return p;
      }, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, catalog.data]);

  const create = useMutation({
    mutationFn: (input: MCPServerInput) => api.createMcpServer(input),
    onSuccess: (srv) => {
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
      setPreset(null);
      if (srv.sync_error) toast.error(`${srv.name} was saved, but KeiRouter could not load its tools`, srv.sync_error);
      else toast.success(`${srv.name} added`, `${srv.exposed_tool_count} tools available.`);
      navigate(`/mcp-servers/${srv.id}`);
    },
    onError: (e: Error) => toast.error("Couldn't add the server", e.message),
  });

  const toggle = useMutation({
    mutationFn: (s: MCPServer) => api.updateMcpServer(s.id, { enabled: !s.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp-servers"] }),
    onError: (e: Error) => toast.error("Couldn't change the server", e.message),
  });
  const sync = useMutation({
    mutationFn: (s: MCPServer) => api.syncMcpServer(s.id),
    onSuccess: (srv) => {
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
      if (srv.sync_error) toast.error(`Couldn't sync ${srv.name}`, srv.sync_error);
      else toast.success(`${srv.name} synced`, `${srv.tool_count} tools.`);
    },
    onError: (e: Error) => toast.error("Couldn't sync the server", e.message),
  });
  const remove = useMutation({
    mutationFn: (s: MCPServer) => api.deleteMcpServer(s.id),
    onSuccess: (_, s) => {
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
      toast.success(`${s.name} removed`);
    },
    onError: (e: Error) => toast.error("Couldn't remove the server", e.message),
  });
  const askRemove = async (s: MCPServer) => {
    const ok = await confirm({
      title: `Remove ${s.name}?`,
      description: `Clients lose its tools at once, and keys limited to this server lose that access. Call logs are kept.`,
      confirmLabel: "Remove server",
      tone: "danger",
    });
    if (ok) remove.mutate(s);
  };

  const all = servers.data?.servers ?? [];
  const counts = {
    all: all.length,
    failing: all.filter((s) => s.enabled && s.health_status === "failing").length,
    off: all.filter((s) => !s.enabled).length,
  };
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return all.filter((s) => {
      if (filter === "failing" && !(s.enabled && s.health_status === "failing")) return false;
      if (filter === "off" && s.enabled) return false;
      return !q || s.name.toLowerCase().includes(q) || s.slug.includes(q) || s.url.toLowerCase().includes(q);
    });
  }, [all, query, filter]);
  const calls24h = stats.data?.stats.by_server ?? {};
  const statsReady = stats.isSuccess;

  const formPreset = preset && preset !== "custom" ? preset : undefined;

  return (
    <>
      <PageHeader
        title="MCP servers"
        description="Tool servers your API keys can call through one endpoint."
        action={
          all.length > 0 ? (
            <Button onClick={openAdd} disabled={catalog.isLoading}>
              <Plus aria-hidden="true" />
              Add server
            </Button>
          ) : undefined
        }
      />

      <PresetPicker
        open={picking}
        presets={catalog.data?.presets ?? []}
        catalogOnly={!!catalog.data?.catalog_only}
        onClose={() => setPicking(false)}
        onPick={(p) => {
          setPicking(false);
          setPreset(p);
        }}
      />

      <Modal
        open={preset !== null}
        onClose={() => setPreset(null)}
        maxWidth="max-w-2xl"
        title={formPreset ? `Add ${formPreset.name}` : "Add MCP server"}
        subtitle={formPreset?.description ?? "Any server that speaks MCP over Streamable HTTP."}
      >
        {preset !== null && (
          <ServerForm
            key={formPreset?.id ?? "custom"}
            mode="create"
            preset={formPreset}
            keysWithAllAccess={servers.data?.keys_with_all_access}
            submitLabel="Add server"
            submitting={create.isPending}
            onSubmit={(input) => create.mutateAsync(input).catch(() => undefined)}
            onCancel={() => setPreset(null)}
          />
        )}
      </Modal>

      {servers.isLoading ? (
        <div className="space-y-3" aria-busy="true" aria-label="Loading MCP servers">
          <Skeleton className="h-12 w-full rounded-2xl" />
          <Skeleton className="h-72 w-full rounded-2xl" />
        </div>
      ) : servers.isError ? (
        <div className="space-y-3">
          <ErrorBanner message={`Couldn't load MCP servers. ${servers.error instanceof Error ? servers.error.message : ""}`.trim()} />
          <Button variant="secondary" onClick={() => servers.refetch()}>
            Retry
          </Button>
        </div>
      ) : all.length === 0 ? (
        <EmptyState
          variant="card"
          titleAs="h2"
          title="No MCP servers yet"
          hint="Add a server and every API key with MCP access can call its tools at /mcp."
          action={
            <Button onClick={openAdd} disabled={catalog.isLoading}>
              <Plus aria-hidden="true" />
              Add server
            </Button>
          }
        />
      ) : (
        <section aria-labelledby={`${fid}-list`} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
          <div className="border-b border-line px-4 py-3">
            <SectionTitle icon={ICONS.mcpServers} title="Servers" id={`${fid}-list`} />
          </div>
          <div className="flex flex-col gap-2 border-b border-line px-4 py-3 sm:flex-row sm:flex-wrap sm:items-center">
            <div className="relative sm:w-72">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" aria-hidden="true" />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search name, slug or URL"
                aria-label="Search MCP servers"
                className={cn(
                  "h-9 w-full rounded-lg border border-input bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-ring pointer-coarse:h-11 pointer-coarse:text-base",
                  FOCUS_RING,
                )}
              />
              {query && (
                <button
                  type="button"
                  onClick={() => setQuery("")}
                  aria-label="Clear search"
                  className={cn("absolute right-1.5 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg", FOCUS_RING)}
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Filter servers">
              {FILTERS.map((f) => {
                const active = filter === f.value;
                return (
                  <button
                    key={f.value}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    onClick={() => setFilter(f.value)}
                    className={cn(
                      "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors pointer-coarse:h-11",
                      FOCUS_RING,
                      active ? "border-accent-500/30 bg-brand-soft text-link" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                    )}
                  >
                    {f.label}
                    <span className={cn("font-normal tabular-nums", active ? "text-link" : "text-fg-faint")}>{counts[f.value]}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {visible.length === 0 ? (
            <div className="px-6 py-10 text-center">
              <h3 className="text-[13px] font-medium text-fg">No servers match</h3>
              <Button
                variant="ghost"
                className="mt-3"
                onClick={() => {
                  setQuery("");
                  setFilter("all");
                }}
              >
                Clear filters
              </Button>
            </div>
          ) : (
            <>
              <ul className="divide-y divide-line md:hidden" aria-label="MCP servers">
                {visible.map((s) => (
                  <li key={s.id} className="flex items-start gap-3 px-4 py-3">
                    <ServerMark server={s} size={28} />
                    <div className="min-w-0 flex-1">
                      <Link to={`/mcp-servers/${s.id}`} className={cn("block truncate text-[13px] font-medium text-fg hover:underline", FOCUS_RING)}>
                        {s.name}
                      </Link>
                      <p className="truncate font-mono text-[12px] text-fg-muted">{s.slug} · {hostOf(s.url)}</p>
                      <div className="mt-1.5 flex flex-wrap items-center gap-2 text-[12px] text-fg-muted">
                        <ServerStatusBadge server={s} />
                        <span className="tabular-nums">{s.exposed_tool_count}/{s.tool_count} tools</span>
                        {statsReady && (
                          <span className="tabular-nums">
                            {(calls24h[s.id] ?? 0).toLocaleString("en-US")} call{(calls24h[s.id] ?? 0) === 1 ? "" : "s"} 24h
                          </span>
                        )}
                      </div>
                      {s.enabled && s.health_status === "failing" && s.health_error && <p className="mt-1 text-[12px] leading-5 text-bad">{s.health_error}</p>}
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Toggle checked={s.enabled} onChange={() => toggle.mutate(s)} label={`${s.enabled ? "Disable" : "Enable"} ${s.name}`} disabled={toggle.isPending} />
                      <RowMenu server={s} onSync={() => sync.mutate(s)} onRemove={() => askRemove(s)} />
                    </div>
                  </li>
                ))}
              </ul>
              <div className="hidden overflow-x-auto md:block">
                <table className="w-full min-w-[720px] text-[13px]">
                  <thead>
                    <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                      <th scope="col" className="px-4 py-2 font-medium">Server</th>
                      <th scope="col" className="px-4 py-2 font-medium">URL</th>
                      <th scope="col" className="px-4 py-2 text-right font-medium">Tools</th>
                      <th scope="col" className="px-4 py-2 font-medium">Status</th>
                      <th scope="col" className="px-4 py-2 text-right font-medium">Calls 24h</th>
                      <th scope="col" className="w-24 px-2 py-2"><span className="sr-only">Enabled and actions</span></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {visible.map((s) => (
                      <tr key={s.id} className="cursor-pointer hover:bg-hover" onClick={() => navigate(`/mcp-servers/${s.id}`)}>
                        <td className="px-4 py-2.5">
                          <div className="flex items-center gap-3">
                            <ServerMark server={s} size={28} />
                            <div className="min-w-0">
                              <Link
                                to={`/mcp-servers/${s.id}`}
                                onClick={(e) => e.stopPropagation()}
                                className={cn("block truncate font-medium text-fg hover:underline", FOCUS_RING)}
                              >
                                {s.name}
                              </Link>
                              <span className="font-mono text-[12px] text-fg-muted">{s.slug}</span>
                            </div>
                          </div>
                        </td>
                        <td className="max-w-[220px] truncate px-4 py-2.5 font-mono text-[12px] text-fg-muted" title={s.url}>
                          {hostOf(s.url)}
                        </td>
                        <td className="px-4 py-2.5 text-right font-mono tabular-nums">
                          {s.exposed_tool_count}/{s.tool_count}
                          {s.changed_tool_count > 0 && <span className="ml-1.5 font-sans text-[12px] text-warn">· {s.changed_tool_count} changed</span>}
                        </td>
                        <td className="px-4 py-2.5">
                          <div className="flex flex-col items-start gap-0.5">
                            <ServerStatusBadge server={s} />
                            {s.health_checked_at && <span className="text-[12px] text-fg-faint">{formatRelative(s.health_checked_at)}</span>}
                          </div>
                        </td>
                        <td className="px-4 py-2.5 text-right font-mono tabular-nums text-fg-muted">
                          {statsReady ? (calls24h[s.id] ?? 0).toLocaleString("en-US") : <span className="text-fg-faint">n/a</span>}
                        </td>
                        <td className="px-2 py-2.5" onClick={(e) => e.stopPropagation()}>
                          <div className="flex items-center justify-end gap-1">
                            <Toggle checked={s.enabled} onChange={() => toggle.mutate(s)} label={`${s.enabled ? "Disable" : "Enable"} ${s.name}`} disabled={toggle.isPending} />
                            <RowMenu server={s} onSync={() => sync.mutate(s)} onRemove={() => askRemove(s)} />
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </section>
      )}
    </>
  );
}

function RowMenu({ server, onSync, onRemove }: { server: MCPServer; onSync: () => void; onRemove: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={`Actions for ${server.name}`}
        className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg pointer-coarse:h-11 pointer-coarse:w-11", FOCUS_RING)}
      >
        <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onSync} disabled={!server.enabled}>
          <RefreshCw aria-hidden="true" />
          Sync tools
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem tone="danger" onSelect={onRemove}>
          <Trash2 aria-hidden="true" />
          Remove server
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** PresetPicker lists company presets first, then built-in ones by category. */
function PresetPicker({
  open,
  presets,
  catalogOnly,
  onClose,
  onPick,
}: {
  open: boolean;
  presets: MCPPreset[];
  catalogOnly: boolean;
  onClose: () => void;
  onPick: (p: MCPPreset | "custom") => void;
}) {
  const [q, setQ] = useState("");
  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const match = presets.filter((p) => !needle || `${p.name} ${p.description} ${p.category}`.toLowerCase().includes(needle));
    const out: { title: string; items: MCPPreset[] }[] = [];
    const company = match.filter((p) => p.source === "company");
    if (company.length) out.push({ title: "Your company", items: company });
    for (const p of match.filter((x) => x.source !== "company")) {
      const g = out.find((x) => x.title === p.category);
      if (g) g.items.push(p);
      else out.push({ title: p.category || "Other", items: [p] });
    }
    return out;
  }, [presets, q]);

  return (
    <Modal open={open} onClose={onClose} maxWidth="max-w-3xl" title="Add MCP server" subtitle="Start from a known server, or connect any other.">
      <div className="space-y-5 px-5 py-4">
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" aria-hidden="true" />
          <input
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search servers"
            aria-label="Search presets"
            autoFocus
            className={cn("h-9 w-full rounded-lg border border-input bg-surface pl-9 text-[13px] text-fg placeholder:text-fg-faint focus:border-ring pointer-coarse:h-11 pointer-coarse:text-base", FOCUS_RING)}
          />
        </div>
        {groups.length === 0 && <p className="text-[13px] text-fg-muted">No preset matches “{q}”.</p>}
        {groups.map((g) => (
          <section key={g.title} aria-label={g.title}>
            <h3 className="mb-2 text-[12.5px] font-medium text-fg-muted">{g.title}</h3>
            <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
              {g.items.map((p) => (
                <li key={p.id}>
                  <button
                    type="button"
                    onClick={() => onPick(p)}
                    className={cn("flex h-full w-full items-start gap-3 rounded-xl border border-line bg-surface px-3 py-2.5 text-left transition-colors hover:border-line-strong hover:bg-hover", FOCUS_RING)}
                  >
                    <ProviderLogo icon={`/providers/${p.id}.png`} name={p.name} size={28} />
                    <span className="min-w-0">
                      <span className="block text-[13px] font-medium text-fg">{p.name}</span>
                      <span className="mt-0.5 line-clamp-2 block text-[12px] leading-5 text-fg-muted">{p.description}</span>
                      {p.auth_types.length === 1 && p.auth_types[0] === "none" && <span className="mt-1 block text-[12px] text-fg-faint">No key needed</span>}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
      {!catalogOnly && (
        <div className="flex flex-col gap-2 border-t border-line bg-subtle px-5 py-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-[13px] text-fg-muted">Don't see it? Connect any server by URL.</p>
          <Button variant="secondary" onClick={() => onPick("custom")}>
            Custom server
          </Button>
        </div>
      )}
    </Modal>
  );
}
