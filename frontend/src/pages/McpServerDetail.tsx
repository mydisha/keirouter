import { useCallback, useEffect, useId, useMemo, useState, type KeyboardEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ChevronRight, Copy, MoreHorizontal, Play, RefreshCw, Search, Trash2 } from "lucide-react";
import { api, type MCPInvokeResult, type MCPServer, type MCPServerInput, type MCPTool } from "../lib/api";
import { cn } from "@/lib/utils";
import { formatMs, formatRelative } from "../lib/format";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";
import { Badge, Button, EmptyState, ErrorBanner, Field, SectionTitle, Select, Skeleton, TabBar, TabPanel, Textarea, Toggle } from "../components/ui";
import { CodeBlock } from "../components/ui/code-block";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";
import { ICONS } from "../lib/icons";
import { ServerForm } from "../components/mcp/ServerForm";
import { CallStatusBadge, ServerMark, ServerStatusBadge, skeletonArgs } from "../components/mcp/shared";

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-ring";

type Tab = "tools" | "test" | "access" | "settings";
const TABS: { value: Tab; label: string }[] = [
  { value: "tools", label: "Tools" },
  { value: "test", label: "Test" },
  { value: "access", label: "Access" },
  { value: "settings", label: "Settings" },
];

export function McpServerDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const tabsId = useId();
  const [params, setParams] = useSearchParams();
  const raw = params.get("tab") as Tab | null;
  const tab: Tab = TABS.some((t) => t.value === raw) ? (raw as Tab) : "tools";
  const setTab = useCallback(
    (t: Tab) =>
      setParams((p) => {
        if (t === "tools") p.delete("tab");
        else p.set("tab", t);
        return p;
      }, { replace: true }),
    [setParams],
  );

  const server = useQuery({ queryKey: ["mcp-server", id], queryFn: () => api.mcpServer(id) });
  const refresh = (srv?: MCPServer) => {
    if (srv) qc.setQueryData(["mcp-server", id], srv);
    qc.invalidateQueries({ queryKey: ["mcp-servers"] });
  };

  const sync = useMutation({
    mutationFn: () => api.syncMcpServer(id),
    onSuccess: (srv) => {
      refresh(srv);
      if (srv.sync_error) toast.error(`Couldn't sync ${srv.name}`, srv.sync_error);
      else toast.success("Tools synced", `${srv.tool_count} tools from ${srv.name}.`);
    },
    onError: (e: Error) => toast.error("Couldn't sync the server", e.message),
  });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.updateMcpServer(id, { enabled }),
    onSuccess: (srv) => refresh(srv),
    onError: (e: Error) => toast.error("Couldn't change the server", e.message),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteMcpServer(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
      toast.success("Server removed");
      navigate("/mcp-servers");
    },
    onError: (e: Error) => toast.error("Couldn't remove the server", e.message),
  });

  if (server.isLoading) {
    return (
      <div className="space-y-3" aria-busy="true" aria-label="Loading MCP server">
        <Skeleton className="h-16 w-full rounded-2xl" />
        <Skeleton className="h-80 w-full rounded-2xl" />
      </div>
    );
  }
  if (server.isError || !server.data) {
    return (
      <EmptyState
        variant="card"
        titleAs="h1"
        title="This MCP server doesn't exist"
        hint={server.error instanceof Error ? server.error.message : "It may have been removed."}
        action={
          <Button variant="secondary" onClick={() => navigate("/mcp-servers")}>
            Back to MCP servers
          </Button>
        }
      />
    );
  }

  const s = server.data;
  const endpoint = `${window.location.origin}/mcp/${s.slug}`;
  const askRemove = async () => {
    const ok = await confirm({
      title: `Remove ${s.name}?`,
      description: "Clients lose its tools at once, and keys limited to this server lose that access. Call logs are kept.",
      confirmLabel: "Remove server",
      tone: "danger",
    });
    if (ok) remove.mutate();
  };

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 text-[13px] text-fg-muted">
        <ol className="flex items-center gap-1.5">
          <li>
            <Link to="/mcp-servers" className={cn("inline-flex items-center gap-1.5 rounded-md hover:text-fg", FOCUS_RING)}>
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
              MCP servers
            </Link>
          </li>
          <li aria-hidden="true" className="text-fg-faint">/</li>
          <li className="min-w-0">
            <span aria-current="page" className="block truncate text-fg">{s.name}</span>
          </li>
        </ol>
      </nav>

      <header className="mb-6 flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-center gap-3.5">
          <ServerMark server={s} size={40} />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="min-w-0 max-w-full truncate text-[22px] font-medium leading-tight tracking-[-0.02em] text-fg">{s.name}</h1>
              <ServerStatusBadge server={s} />
            </div>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
              <span className="max-w-full truncate font-mono text-[12.5px]" title={s.url}>{s.url}</span>
              {s.protocol_version && (
                <>
                  <span aria-hidden="true" className="text-fg-faint">·</span>
                  <span>
                    MCP <span className="font-mono">{s.protocol_version}</span>
                  </span>
                </>
              )}
              {s.tools_synced_at && (
                <>
                  <span aria-hidden="true" className="text-fg-faint">·</span>
                  <span>Synced {formatRelative(s.tools_synced_at)}</span>
                </>
              )}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="mr-1 inline-flex items-center gap-2 text-[13px] text-fg-muted">
            <Toggle checked={s.enabled} onChange={(v) => toggle.mutate(v)} disabled={toggle.isPending} label={s.enabled ? "Disable server" : "Enable server"} />
            {s.enabled ? "Enabled" : "Disabled"}
          </label>
          <Button variant="secondary" onClick={() => sync.mutate()} disabled={sync.isPending || !s.enabled}>
            <RefreshCw className={cn(sync.isPending && "animate-spin motion-reduce:animate-none")} aria-hidden="true" />
            {sync.isPending ? "Syncing…" : "Sync tools"}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label="More server actions"
              className={cn("flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted hover:bg-hover hover:text-fg pointer-coarse:h-11 pointer-coarse:w-11", FOCUS_RING)}
            >
              <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                onSelect={() =>
                  navigator.clipboard?.writeText(endpoint).then(
                    () => toast.success("Endpoint copied", "Clients that want only this server's tools can use it."),
                    () => toast.error("Couldn't copy", endpoint),
                  )
                }
              >
                <Copy aria-hidden="true" />
                Copy this server's endpoint
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem tone="danger" onSelect={askRemove} disabled={remove.isPending}>
                <Trash2 aria-hidden="true" />
                Remove server
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      {s.enabled && s.health_status === "failing" && (
        <div className="mb-5">
          <ErrorBanner message={`The last sync failed: ${s.health_error || "unknown error"}. Clients keep the tools from the last good sync.`} />
        </div>
      )}

      <div className="mb-5">
        <TabBar tabs={TABS} active={tab} onChange={setTab} label={`${s.name} sections`} idPrefix={tabsId} />
      </div>
      <TabPanel idPrefix={tabsId} value={tab}>
        {tab === "tools" && <ToolsTab server={s} onSaved={refresh} />}
        {tab === "test" && <TestTab server={s} />}
        {tab === "access" && <AccessTab server={s} />}
        {tab === "settings" && <SettingsTab server={s} onSaved={refresh} />}
      </TabPanel>
    </>
  );
}

// ── Tools ─────────────────────────────────────────────────────────────────────

function ToolsTab({ server, onSaved }: { server: MCPServer; onSaved: (s: MCPServer) => void }) {
  const toast = useToast();
  const headingId = useId();
  const tools = server.tools ?? [];
  const initial = useMemo(() => new Set(tools.filter((t) => t.enabled).map((t) => t.name)), [tools]);
  const [mode, setMode] = useState(server.tool_mode);
  const [selected, setSelected] = useState(initial);
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  useEffect(() => {
    setMode(server.tool_mode);
    setSelected(initial);
  }, [server.tool_mode, initial]);

  const dirty = mode !== server.tool_mode || (mode === "selected" && (selected.size !== initial.size || [...selected].some((n) => !initial.has(n))));
  const save = useMutation({
    mutationFn: () => api.setMcpTools(server.id, mode, mode === "selected" ? [...selected] : []),
    onSuccess: (srv) => {
      onSaved(srv);
      toast.success("Tools saved", `${srv.exposed_tool_count} of ${srv.tool_count} exposed.`);
    },
    onError: (e: Error) => toast.error("Couldn't save tools", e.message),
  });

  const needle = q.trim().toLowerCase();
  const visible = tools.filter((t) => !needle || t.name.toLowerCase().includes(needle) || (t.description ?? "").toLowerCase().includes(needle));
  const changed = tools.filter((t) => t.changed_at).length;
  const blockSave = mode === "selected" && selected.size === 0;

  if (tools.length === 0) {
    return (
      <EmptyState
        variant="card"
        title="No tools yet"
        hint={server.enabled ? "Sync the server to load its tools." : "Enable the server and sync it to load its tools."}
      />
    );
  }

  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <SectionTitle
          icon={ICONS.mcpServers}
          id={headingId}
          title="Tools"
          subtitle={changed > 0 ? `${changed} changed upstream since you last saved. Review them, then save.` : undefined}
        />
      </div>
      <div className="flex flex-col gap-3 border-b border-line px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Which tools this server exposes">
          {(
            [
              ["all", "Expose every tool"],
              ["selected", "Only selected tools"],
            ] as const
          ).map(([v, label]) => (
            <button
              key={v}
              type="button"
              role="radio"
              aria-checked={mode === v}
              onClick={() => setMode(v)}
              className={cn(
                "inline-flex h-8 items-center rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors pointer-coarse:h-11",
                FOCUS_RING,
                mode === v ? "border-accent-500/30 bg-brand-soft text-link" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
              )}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="relative sm:w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" aria-hidden="true" />
          <input
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Filter tools"
            aria-label="Filter tools"
            className={cn("h-9 w-full rounded-lg border border-input bg-surface pl-9 text-[13px] text-fg placeholder:text-fg-faint focus:border-ring pointer-coarse:h-11 pointer-coarse:text-base", FOCUS_RING)}
          />
        </div>
      </div>

      <ul className="divide-y divide-line">
        {visible.map((t) => {
          const checked = mode === "all" ? !t.invalid_reason : selected.has(t.name);
          const expanded = open === t.name;
          return (
            <li key={t.name}>
              <div className="flex items-start gap-3 px-4 py-2.5 sm:px-5">
                <input
                  type="checkbox"
                  className="mt-1 h-4 w-4 shrink-0 rounded border-input accent-accent-500 disabled:opacity-50"
                  checked={checked}
                  disabled={mode === "all" || !!t.invalid_reason}
                  aria-label={`Expose ${t.name}`}
                  onChange={(e) => {
                    const next = new Set(selected);
                    if (e.target.checked) next.add(t.name);
                    else next.delete(t.name);
                    setSelected(next);
                  }}
                />
                <button
                  type="button"
                  onClick={() => setOpen(expanded ? null : t.name)}
                  aria-expanded={expanded}
                  className={cn("flex min-w-0 flex-1 items-start gap-2 rounded-md text-left", FOCUS_RING)}
                >
                  <ChevronRight className={cn("mt-0.5 h-4 w-4 shrink-0 text-fg-faint transition-transform", expanded && "rotate-90")} aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="break-all font-mono text-[13px] text-fg">{t.name}</span>
                      {t.changed_at && <Badge tone="warning">Changed</Badge>}
                      {t.invalid_reason && <Badge tone="danger" title={t.invalid_reason}>Hidden: invalid header annotation</Badge>}
                      {t.exposed_name.length > 64 && <Badge tone="warning" title="Some clients reject tool names longer than 64 characters.">Long name</Badge>}
                    </span>
                    {t.description && <span className="mt-0.5 line-clamp-2 block text-[12.5px] leading-5 text-fg-muted">{t.description}</span>}
                  </span>
                </button>
              </div>
              {expanded && <ToolDetail tool={t} />}
            </li>
          );
        })}
        {visible.length === 0 && <li className="px-5 py-8 text-center text-[13px] text-fg-muted">No tool matches “{q}”.</li>}
      </ul>

      <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-3 sm:flex-row sm:flex-wrap sm:items-center sm:px-5">
        <div role="status" className="min-w-0 flex-1 text-[12.5px] text-fg-muted">
          {mode === "all" ? `${tools.filter((t) => !t.invalid_reason).length} of ${tools.length} exposed` : `${selected.size} of ${tools.length} selected`}
          {dirty && <span className="text-fg"> · Unsaved changes</span>}
          {blockSave && <span className="text-bad"> · Select at least one tool</span>}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row [&>button]:w-full sm:[&>button]:w-auto">
          <Button
            variant="quiet"
            disabled={!dirty && changed === 0}
            onClick={() => {
              setMode(server.tool_mode);
              setSelected(initial);
            }}
          >
            Discard
          </Button>
          <Button onClick={() => save.mutate()} disabled={(!dirty && changed === 0) || blockSave || save.isPending}>
            {save.isPending ? "Saving…" : changed > 0 && !dirty ? "Mark reviewed" : "Save tools"}
          </Button>
        </div>
      </div>
    </section>
  );
}

function ToolDetail({ tool }: { tool: MCPTool }) {
  return (
    <div className="space-y-2 border-t border-line bg-subtle px-4 py-3 sm:pl-14 sm:pr-5">
      <p className="text-[12.5px] text-fg-muted">
        Clients call it as <span className="font-mono text-fg">{tool.exposed_name}</span>
      </p>
      {tool.invalid_reason && <p className="text-[12.5px] text-bad">{tool.invalid_reason}</p>}
      <CodeBlock code={JSON.stringify(tool.input_schema ?? {}, null, 2)} language="json" title="Input schema" wrap />
    </div>
  );
}

// ── Test ──────────────────────────────────────────────────────────────────────

function TestTab({ server }: { server: MCPServer }) {
  const headingId = useId();
  const tools = (server.tools ?? []).filter((t) => t.enabled && !t.invalid_reason);
  const [tool, setTool] = useState(tools[0]?.name ?? "");
  const current = tools.find((t) => t.name === tool);
  const [args, setArgs] = useState(() => JSON.stringify(skeletonArgs(current?.input_schema), null, 2));
  const [parseError, setParseError] = useState("");
  const [result, setResult] = useState<MCPInvokeResult | null>(null);

  const run = useMutation({
    mutationFn: (parsed: unknown) => api.callMcpTool(server.id, tool, parsed),
    onSuccess: (r) => setResult(r),
    onError: (e: Error) => setResult({ status: "upstream_error", error: { code: 0, message: e.message }, latency_ms: 0 }),
  });
  const submit = () => {
    let parsed: unknown;
    try {
      parsed = args.trim() ? JSON.parse(args) : {};
    } catch (e) {
      setParseError(`Arguments are not valid JSON: ${(e as Error).message}`);
      return;
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      setParseError("Arguments must be a JSON object.");
      return;
    }
    setParseError("");
    run.mutate(parsed);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
    }
  };

  if (!server.enabled || tools.length === 0) {
    return <EmptyState variant="card" title="Nothing to test" hint={server.enabled ? "This server exposes no tools. Pick some on the Tools tab." : "Enable the server to test its tools."} />;
  }

  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
        <div className="border-b border-line px-4 py-3 sm:px-5">
          <SectionTitle icon={Play} id={headingId} title="Run a tool" subtitle="Calls the real server with its saved credential. Logged as a dashboard call." />
        </div>
        <div className="space-y-4 px-4 py-4 sm:px-5">
          <Field label="Tool">
            <Select
              value={tool}
              onChange={(e) => {
                const next = tools.find((t) => t.name === e.target.value);
                setTool(e.target.value);
                setArgs(JSON.stringify(skeletonArgs(next?.input_schema), null, 2));
                setResult(null);
              }}
              className="font-mono"
            >
              {tools.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name}
                </option>
              ))}
            </Select>
          </Field>
          {current?.description && <p className="text-[12.5px] leading-5 text-fg-muted">{current.description}</p>}
          <Field label="Arguments" hint="JSON object. ⌘/Ctrl + Enter runs it." error={parseError || undefined}>
            <Textarea value={args} onChange={(e) => setArgs(e.target.value)} onKeyDown={onKeyDown} rows={10} className="font-mono text-[12.5px]" spellCheck={false} />
          </Field>
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={submit} disabled={run.isPending}>
              <Play aria-hidden="true" />
              {run.isPending ? "Running…" : "Run tool"}
            </Button>
            <Button variant="quiet" onClick={() => setArgs(JSON.stringify(skeletonArgs(current?.input_schema), null, 2))}>
              Reset arguments
            </Button>
          </div>
        </div>
      </section>

      <section aria-label="Result" aria-live="polite" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-3 sm:px-5">
          <h2 className="text-heading font-medium text-fg">Result</h2>
          {result && (
            <div className="flex items-center gap-2 text-[12.5px] text-fg-muted">
              <CallStatusBadge status={result.status} />
              {result.latency_ms > 0 && <span className="font-mono tabular-nums">{formatMs(result.latency_ms)}</span>}
            </div>
          )}
        </div>
        <div className="px-4 py-4 sm:px-5">
          {!result ? (
            <p className="text-[13px] text-fg-muted">Run a tool to see what the server returns.</p>
          ) : result.error ? (
            <ErrorBanner message={`${result.error.message}${result.error.code ? ` (code ${result.error.code})` : ""}`} />
          ) : (
            <CodeBlock code={JSON.stringify(result.result, null, 2)} language="json" wrap />
          )}
        </div>
      </section>
    </div>
  );
}

// ── Access ────────────────────────────────────────────────────────────────────

function AccessTab({ server }: { server: MCPServer }) {
  const headingId = useId();
  const keys = useQuery({ queryKey: ["mcp-server-keys", server.id], queryFn: () => api.mcpServerKeys(server.id) });
  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <SectionTitle icon={ICONS.keys} id={headingId} title="API keys that can use this server" subtitle="Change a key's access on its MCP tab." />
      </div>
      {keys.isLoading ? (
        <div className="space-y-2 p-4" aria-busy="true">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      ) : keys.isError ? (
        <div className="space-y-3 p-4">
          <ErrorBanner message="Couldn't load the keys." />
          <Button variant="secondary" onClick={() => keys.refetch()}>Retry</Button>
        </div>
      ) : (keys.data?.keys.length ?? 0) === 0 ? (
        <p className="px-5 py-8 text-center text-[13px] text-fg-muted">No API key can use this server yet. Give a key access on its MCP tab.</p>
      ) : (
        <ul className="divide-y divide-line">
          {keys.data!.keys.map((k) => (
            <li key={k.id}>
              <Link to={`/keys/${k.id}?tab=mcp`} className={cn("flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 hover:bg-hover sm:px-5", FOCUS_RING)}>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-medium text-fg">{k.name}</span>
                  <span className="font-mono text-[12px] text-fg-muted">{k.display}</span>
                </span>
                {k.disabled && <Badge tone="neutral">Disabled</Badge>}
                <span className="text-[12.5px] text-fg-muted">{k.all_tools ? "All tools" : `${k.tools?.length ?? 0} tool${k.tools?.length === 1 ? "" : "s"}`}</span>
                <ChevronRight className="h-4 w-4 text-fg-faint" aria-hidden="true" />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ── Settings ──────────────────────────────────────────────────────────────────

function SettingsTab({ server, onSaved }: { server: MCPServer; onSaved: (s: MCPServer) => void }) {
  const toast = useToast();
  const headingId = useId();
  const [formKey, setFormKey] = useState(0);
  const update = useMutation({
    mutationFn: (input: MCPServerInput) => api.updateMcpServer(server.id, input),
    onSuccess: (srv) => {
      onSaved(srv);
      setFormKey((k) => k + 1);
      if (srv.sync_error) toast.error("Saved, but the sync failed", srv.sync_error);
      else toast.success("Server saved");
    },
    onError: (e: Error) => toast.error("Couldn't save the server", e.message),
  });
  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <SectionTitle icon={ICONS.settings} id={headingId} title="Connection" subtitle="Changing the URL or credentials reconnects and syncs the tools." />
      </div>
      <ServerForm
        key={formKey}
        mode="edit"
        server={server}
        submitLabel="Save changes"
        submitting={update.isPending}
        onSubmit={(input) => update.mutateAsync(input).catch(() => undefined)}
      />
    </section>
  );
}

