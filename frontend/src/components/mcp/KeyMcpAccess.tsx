import { useEffect, useId, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type MCPKeyAccess, type MCPMode, type MCPServer } from "../../lib/api";
import { cn } from "@/lib/utils";
import { useToast } from "../Toast";
import { Badge, Button, EmptyState, ErrorBanner, SectionTitle, Skeleton } from "../ui";
import { ICONS } from "../../lib/icons";
import { ServerMark } from "./shared";

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-ring";

type Grant = { toolMode: "all" | "selected"; tools: Set<string> };

const MODES: { value: MCPMode; label: string }[] = [
  { value: "none", label: "No access" },
  { value: "all", label: "Every server" },
  { value: "selected", label: "Selected servers" },
];

function toGrants(a: MCPKeyAccess | undefined): Map<string, Grant> {
  return new Map((a?.servers ?? []).map((g) => [g.server_id, { toolMode: g.tool_mode, tools: new Set(g.tools) }]));
}

function sameGrants(a: Map<string, Grant>, b: Map<string, Grant>): boolean {
  if (a.size !== b.size) return false;
  for (const [id, g] of a) {
    const o = b.get(id);
    if (!o || o.toolMode !== g.toolMode) return false;
    if (g.toolMode === "selected" && (o.tools.size !== g.tools.size || [...g.tools].some((t) => !o.tools.has(t)))) return false;
  }
  return true;
}

/**
 * KeyMcpAccess edits which MCP servers and tools one API key may use. The
 * mode is explicit: "no access", "every server" or "selected servers". In
 * selected mode each server is either fully allowed or limited to tools.
 */
export function KeyMcpAccess({ keyId }: { keyId: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const headingId = useId();
  const access = useQuery({ queryKey: ["key-mcp-access", keyId], queryFn: () => api.keyMcpAccess(keyId) });
  const servers = useQuery({ queryKey: ["mcp-servers"], queryFn: api.mcpServers });

  const [mode, setMode] = useState<MCPMode>("all");
  const [grants, setGrants] = useState<Map<string, Grant>>(new Map());
  useEffect(() => {
    if (access.data) {
      setMode(access.data.mode);
      setGrants(toGrants(access.data));
    }
  }, [access.data]);

  const saved = useMemo(() => toGrants(access.data), [access.data]);
  const dirty = !!access.data && (mode !== access.data.mode || (mode === "selected" && !sameGrants(grants, saved)));
  const problem =
    mode === "selected" && grants.size === 0
      ? "Pick at least one server, or choose every server or no access."
      : mode === "selected" && [...grants.values()].some((g) => g.toolMode === "selected" && g.tools.size === 0)
        ? "Pick at least one tool for each server limited to selected tools."
        : "";

  const save = useMutation({
    mutationFn: () =>
      api.setKeyMcpAccess(keyId, {
        mode,
        servers:
          mode === "selected"
            ? [...grants.entries()].map(([server_id, g]) => ({ server_id, tool_mode: g.toolMode, tools: g.toolMode === "selected" ? [...g.tools] : [] }))
            : [],
      }),
    onSuccess: (a) => {
      qc.setQueryData(["key-mcp-access", keyId], a);
      qc.invalidateQueries({ queryKey: ["mcp-server-keys"] });
      toast.success("MCP access saved", "Clients using this key see the change on their next request.");
    },
    onError: (e: Error) => toast.error("Couldn't save MCP access", e.message),
  });

  if (access.isLoading || servers.isLoading) {
    return (
      <div className="space-y-3" aria-busy="true" aria-label="Loading MCP access">
        <Skeleton className="h-40 w-full rounded-2xl" />
      </div>
    );
  }
  if (access.isError || servers.isError) {
    return (
      <div className="space-y-3">
        <ErrorBanner message="Couldn't load this key's MCP access." />
        <Button variant="secondary" onClick={() => (access.isError ? access.refetch() : servers.refetch())}>
          Retry
        </Button>
      </div>
    );
  }

  const list = servers.data?.servers ?? [];
  const setGrant = (id: string, g: Grant | null) => {
    const next = new Map(grants);
    if (g) next.set(id, g);
    else next.delete(id);
    setGrants(next);
  };

  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <SectionTitle icon={ICONS.mcpServers} id={headingId} title="MCP access" subtitle="Which MCP servers and tools clients using this key can call at /mcp." />
      </div>
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="MCP access mode">
          {MODES.map((m) => (
            <button
              key={m.value}
              type="button"
              role="radio"
              aria-checked={mode === m.value}
              onClick={() => setMode(m.value)}
              className={cn(
                "inline-flex h-8 items-center rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors pointer-coarse:h-11",
                FOCUS_RING,
                mode === m.value ? "border-accent-500/30 bg-brand-soft text-link" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
              )}
            >
              {m.label}
            </button>
          ))}
        </div>
      </div>

      {mode === "none" && <p className="px-5 py-6 text-[13px] text-fg-muted">This key cannot list or call any MCP tool.</p>}
      {mode === "all" && (
        <p className="px-5 py-6 text-[13px] text-fg-muted">
          This key can use every enabled MCP server, including servers added later.{" "}
          <Link to="/mcp-servers" className="text-link hover:underline">
            View servers
          </Link>
        </p>
      )}
      {mode === "selected" &&
        (list.length === 0 ? (
          <EmptyState
            title="No MCP servers yet"
            hint="Add a server first, then come back to pick it."
            action={
              <Link to="/mcp-servers?add=1" className="text-[13px] text-link hover:underline">
                Add a server
              </Link>
            }
          />
        ) : (
          <ul className="divide-y divide-line">
            {list.map((s) => (
              <ServerGrantRow key={s.id} server={s} grant={grants.get(s.id)} onChange={(g) => setGrant(s.id, g)} />
            ))}
          </ul>
        ))}

      <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-3 sm:flex-row sm:flex-wrap sm:items-center sm:px-5">
        <div role="status" className="min-w-0 flex-1 text-[12.5px] text-fg-muted">
          {mode === "selected" ? `${grants.size} server${grants.size === 1 ? "" : "s"}` : MODES.find((m) => m.value === mode)?.label}
          {dirty && <span className="text-fg"> · Unsaved changes</span>}
          {dirty && problem && <span className="text-bad"> · {problem}</span>}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row [&>button]:w-full sm:[&>button]:w-auto">
          <Button
            variant="quiet"
            disabled={!dirty}
            onClick={() => {
              setMode(access.data!.mode);
              setGrants(toGrants(access.data));
            }}
          >
            Discard
          </Button>
          <Button onClick={() => save.mutate()} disabled={!dirty || !!problem || save.isPending}>
            {save.isPending ? "Saving…" : "Save access"}
          </Button>
        </div>
      </div>
    </section>
  );
}

function ServerGrantRow({ server, grant, onChange }: { server: MCPServer; grant?: Grant; onChange: (g: Grant | null) => void }) {
  const checked = !!grant;
  const wantsTools = grant?.toolMode === "selected";
  const detail = useQuery({ queryKey: ["mcp-server", server.id], queryFn: () => api.mcpServer(server.id), enabled: wantsTools });
  const tools = (detail.data?.tools ?? []).filter((t) => t.enabled && !t.invalid_reason);
  const boxId = useId();

  return (
    <li className="px-4 py-3 sm:px-5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <input
          id={boxId}
          type="checkbox"
          className="h-4 w-4 rounded border-input accent-accent-500"
          checked={checked}
          onChange={(e) => onChange(e.target.checked ? { toolMode: "all", tools: new Set() } : null)}
        />
        <label htmlFor={boxId} className="flex min-w-[12rem] flex-1 cursor-pointer items-center gap-2.5">
          <ServerMark server={server} size={20} />
          <span className="truncate text-[13px] font-medium text-fg">{server.name}</span>
          <span className="font-mono text-[12px] text-fg-muted">{server.slug}</span>
          {!server.enabled && <Badge tone="neutral">Disabled</Badge>}
        </label>
        {checked && (
          <div className="flex gap-1.5" role="radiogroup" aria-label={`Tools of ${server.name}`}>
            {(
              [
                ["all", "All tools"],
                ["selected", `Selected${grant!.toolMode === "selected" ? ` · ${grant!.tools.size}` : ""}`],
              ] as const
            ).map(([v, label]) => (
              <button
                key={v}
                type="button"
                role="radio"
                aria-checked={grant!.toolMode === v}
                onClick={() => onChange({ toolMode: v, tools: grant!.tools })}
                className={cn(
                  "inline-flex h-7 items-center rounded-lg border px-2 text-[12px] font-medium pointer-coarse:h-11",
                  FOCUS_RING,
                  grant!.toolMode === v ? "border-accent-500/30 bg-brand-soft text-link" : "border-line bg-surface text-fg-muted hover:text-fg",
                )}
              >
                {label}
              </button>
            ))}
          </div>
        )}
      </div>
      {wantsTools && (
        <div className="mt-2.5 pl-7">
          {detail.isLoading ? (
            <Skeleton className="h-8 w-full" />
          ) : tools.length === 0 ? (
            <p className="text-[12.5px] text-fg-muted">This server exposes no tools yet.</p>
          ) : (
            <div className="flex flex-wrap gap-1.5" role="group" aria-label={`Tools of ${server.name} this key may call`}>
              {tools.map((t) => {
                const on = grant!.tools.has(t.name);
                return (
                  <button
                    key={t.name}
                    type="button"
                    aria-pressed={on}
                    title={t.description}
                    onClick={() => {
                      const next = new Set(grant!.tools);
                      if (on) next.delete(t.name);
                      else next.add(t.name);
                      onChange({ toolMode: "selected", tools: next });
                    }}
                    className={cn(
                      "inline-flex min-h-7 items-center rounded-md border px-2 font-mono text-[12px] transition-colors pointer-coarse:min-h-11",
                      FOCUS_RING,
                      on ? "border-accent-500/30 bg-brand-soft text-link" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                    )}
                  >
                    {t.name}
                  </button>
                );
              })}
            </div>
          )}
        </div>
      )}
    </li>
  );
}
