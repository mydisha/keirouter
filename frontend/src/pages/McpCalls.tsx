import { Fragment, useEffect, useId, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ChevronRight, Search } from "lucide-react";
import { api, type MCPCallLog, type MCPPeriod } from "../lib/api";
import { cn } from "@/lib/utils";
import { formatDateTime, formatMs } from "../lib/format";
import { PageHeader } from "../components/Layout";
import { Button, EmptyState, ErrorBanner, Kpi, KpiGrid, SectionTitle, SegmentedControl, Select, Skeleton } from "../components/ui";
import { ICONS } from "../lib/icons";
import { CALL_STATUS_OPTIONS, CallStatusBadge } from "../components/mcp/shared";

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-ring";
const PAGE_SIZE = 50;
const PERIODS: { value: MCPPeriod; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return v;
}

function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export function McpCallsPage() {
  const fid = useId();
  const [period, setPeriod] = useState<MCPPeriod>("24h");
  const [serverId, setServerId] = useState("");
  const [keyId, setKeyId] = useState("");
  const [status, setStatus] = useState("");
  const [toolInput, setToolInput] = useState("");
  const tool = useDebounced(toolInput.trim(), 300);
  const [expanded, setExpanded] = useState<string | null>(null);

  const stats = useQuery({ queryKey: ["mcp-stats", period], queryFn: () => api.mcpStats(period), refetchInterval: 15_000 });
  const servers = useQuery({ queryKey: ["mcp-servers"], queryFn: api.mcpServers });
  const keys = useQuery({ queryKey: ["keys"], queryFn: api.listKeys });
  const logs = useInfiniteQuery({
    queryKey: ["mcp-logs", serverId, keyId, status, tool],
    queryFn: ({ pageParam }) => api.mcpLogs({ server_id: serverId, key_id: keyId, status, tool, cursor: pageParam, limit: PAGE_SIZE }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchInterval: 15_000,
  });
  const rows = useMemo(() => logs.data?.pages.flatMap((p) => p.logs) ?? [], [logs.data]);
  const keyName = useMemo(() => {
    const m = new Map<string, string>((keys.data?.keys ?? []).map((k) => [k.id, k.name]));
    m.set("dashboard", "Dashboard tester");
    return m;
  }, [keys.data]);
  const filtering = !!(serverId || keyId || status || tool);

  const st = stats.data?.stats;
  const denied = (st?.by_status.denied ?? 0) + (st?.by_status.rate_limited ?? 0);

  return (
    <>
      <PageHeader title="Tool calls" description="Every MCP tool call through KeiRouter: who called what, and how it ended." />

      <div className="mb-4 flex justify-end">
        <SegmentedControl value={period} onChange={setPeriod} options={PERIODS} label="Time range" />
      </div>
      {stats.isError ? (
        <div className="mb-6">
          <ErrorBanner message="Couldn't load the summary." />
        </div>
      ) : (
        <div className="mb-6">
          <KpiGrid cols={4} label="Tool call summary">
            <Kpi label="Calls" value={st ? st.calls.toLocaleString("en-US") : <Skeleton className="h-8 w-16" />} />
            <Kpi
              label="Error rate"
              value={st ? (st.error_rate === null ? <span className="text-fg-faint">n/a</span> : `${(st.error_rate * 100).toFixed(1)}%`) : <Skeleton className="h-8 w-16" />}
              hint={st && st.error_rate !== null ? "of calls that reached a server" : undefined}
            />
            <Kpi
              label="p95 latency"
              value={st ? (st.p95_latency_ms === null ? <span className="text-fg-faint">n/a</span> : <span className="font-mono">{formatMs(st.p95_latency_ms)}</span>) : <Skeleton className="h-8 w-16" />}
            />
            <Kpi label="Denied or limited" value={st ? denied.toLocaleString("en-US") : <Skeleton className="h-8 w-16" />} hint={st ? "never reached a server" : undefined} />
          </KpiGrid>
        </div>
      )}

      <section aria-labelledby={`${fid}-calls`} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
        <div className="border-b border-line px-4 py-3">
          <SectionTitle icon={ICONS.mcpCalls} title="Calls" id={`${fid}-calls`} subtitle="Arguments and results are never stored, only their sizes." />
        </div>
        <div className="grid grid-cols-1 gap-2 border-b border-line px-4 py-3 sm:grid-cols-2 lg:flex lg:flex-wrap lg:items-center">
          <div className="relative lg:w-60">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" aria-hidden="true" />
            <input
              type="search"
              value={toolInput}
              onChange={(e) => setToolInput(e.target.value)}
              placeholder="Filter by tool"
              aria-label="Filter by tool"
              className={cn("h-9 w-full rounded-lg border border-input bg-surface pl-9 text-[13px] text-fg placeholder:text-fg-faint focus:border-ring pointer-coarse:h-11 pointer-coarse:text-base", FOCUS_RING)}
            />
          </div>
          <Select aria-label="Server" value={serverId} onChange={(e) => setServerId(e.target.value)} className="lg:w-48">
            <option value="">All servers</option>
            {(servers.data?.servers ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
          <Select aria-label="API key" value={keyId} onChange={(e) => setKeyId(e.target.value)} className="lg:w-48">
            <option value="">All keys</option>
            <option value="dashboard">Dashboard tester</option>
            {(keys.data?.keys ?? []).map((k) => (
              <option key={k.id} value={k.id}>
                {k.name}
              </option>
            ))}
          </Select>
          <Select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)} className="lg:w-44">
            <option value="">Any status</option>
            {CALL_STATUS_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>
          {filtering && (
            <Button
              variant="quiet"
              onClick={() => {
                setServerId("");
                setKeyId("");
                setStatus("");
                setToolInput("");
              }}
            >
              Clear filters
            </Button>
          )}
        </div>

        {logs.isLoading ? (
          <div className="space-y-2 p-4" aria-busy="true" aria-label="Loading tool calls">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : logs.isError ? (
          <div className="space-y-3 p-4">
            <ErrorBanner message={`Couldn't load tool calls. ${logs.error instanceof Error ? logs.error.message : ""}`.trim()} />
            <Button variant="secondary" onClick={() => logs.refetch()}>
              Retry
            </Button>
          </div>
        ) : rows.length === 0 ? (
          filtering ? (
            <p className="px-6 py-10 text-center text-[13px] text-fg-muted">No calls match these filters.</p>
          ) : (
            <EmptyState
              title="No tool calls yet"
              hint={
                <>
                  Calls appear here once a client uses <span className="font-mono">/mcp</span>.{" "}
                  <Link to="/endpoints" className="text-link hover:underline">
                    Connect a client
                  </Link>
                </>
              }
            />
          )
        ) : (
          <>
            <ul className="divide-y divide-line md:hidden" aria-label="Tool calls">
              {rows.map((r) => (
                <li key={r.id} className="px-4 py-3">
                  <button
                    type="button"
                    onClick={() => setExpanded(expanded === r.id ? null : r.id)}
                    aria-expanded={expanded === r.id}
                    className={cn("w-full text-left", FOCUS_RING)}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="min-w-0 truncate font-mono text-[12.5px] text-fg">
                        {r.server_slug}__{r.tool}
                      </span>
                      <CallStatusBadge status={r.status} />
                    </div>
                    <div className="mt-1 flex flex-wrap gap-x-2 text-[12px] text-fg-muted">
                      <span>{formatDateTime(r.created_at, { seconds: true })}</span>
                      <span>{keyName.get(r.api_key_id) ?? r.api_key_id}</span>
                      <span className="font-mono tabular-nums">{formatMs(r.latency_ms)}</span>
                    </div>
                  </button>
                  {expanded === r.id && <CallDetails call={r} keyName={keyName} className="mt-3" />}
                </li>
              ))}
            </ul>
            <div className="hidden overflow-x-auto md:block">
              <table className="w-full min-w-[760px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="w-8 px-2 py-2"><span className="sr-only">Details</span></th>
                    <th scope="col" className="px-3 py-2 font-medium">Time</th>
                    <th scope="col" className="px-3 py-2 font-medium">Tool</th>
                    <th scope="col" className="px-3 py-2 font-medium">Key</th>
                    <th scope="col" className="px-3 py-2 font-medium">Status</th>
                    <th scope="col" className="px-3 py-2 text-right font-medium">Latency</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {rows.map((r) => {
                    const open = expanded === r.id;
                    return (
                      <Fragment key={r.id}>
                        <tr className="cursor-pointer hover:bg-hover" onClick={() => setExpanded(open ? null : r.id)}>
                          <td className="px-2 py-2">
                            <button
                              type="button"
                              aria-expanded={open}
                              aria-label={`Details of ${r.server_slug}__${r.tool} at ${formatDateTime(r.created_at)}`}
                              onClick={(e) => {
                                e.stopPropagation();
                                setExpanded(open ? null : r.id);
                              }}
                              className={cn("flex h-7 w-7 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg", FOCUS_RING)}
                            >
                              <ChevronRight className={cn("h-4 w-4 transition-transform", open && "rotate-90")} aria-hidden="true" />
                            </button>
                          </td>
                          <td className="whitespace-nowrap px-3 py-2 text-fg-muted">{formatDateTime(r.created_at, { seconds: true })}</td>
                          <td className="max-w-[280px] truncate px-3 py-2 font-mono text-[12.5px] text-fg" title={`${r.server_slug}__${r.tool}`}>
                            {r.server_slug}__{r.tool}
                          </td>
                          <td className="max-w-[180px] truncate px-3 py-2 text-fg-muted">{keyName.get(r.api_key_id) ?? <span className="font-mono text-[12px]">{r.api_key_id}</span>}</td>
                          <td className="px-3 py-2">
                            <CallStatusBadge status={r.status} />
                          </td>
                          <td className="px-3 py-2 text-right font-mono tabular-nums text-fg-muted">{formatMs(r.latency_ms)}</td>
                        </tr>
                        {open && (
                          <tr className="bg-subtle">
                            <td />
                            <td colSpan={5} className="px-3 py-3">
                              <CallDetails call={r} keyName={keyName} />
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {logs.hasNextPage && (
              <div className="border-t border-line px-4 py-3 text-center">
                <Button variant="ghost" onClick={() => logs.fetchNextPage()} disabled={logs.isFetchingNextPage}>
                  {logs.isFetchingNextPage ? "Loading…" : "Load older calls"}
                </Button>
              </div>
            )}
          </>
        )}
      </section>
    </>
  );
}

function CallDetails({ call: r, keyName, className }: { call: MCPCallLog; keyName: Map<string, string>; className?: string }) {
  const fields: [string, string | undefined][] = [
    ["Error", r.error || undefined],
    ["Server", r.server_slug],
    ["Key", `${keyName.get(r.api_key_id) ?? ""} ${r.api_key_id}`.trim()],
    ["Client", r.client || undefined],
    ["Upstream time", r.upstream_ms > 0 ? formatMs(r.upstream_ms) : undefined],
    ["Sizes", `${bytes(r.request_bytes)} in · ${bytes(r.response_bytes)} out`],
    ["Request ID", r.request_id || undefined],
    ["Trace", r.trace_id || undefined],
  ];
  return (
    <dl className={cn("grid gap-x-6 gap-y-1.5 text-[12.5px] sm:grid-cols-[140px_1fr]", className)}>
      {fields
        .filter(([, v]) => v)
        .map(([k, v]) => (
          <Fragment key={k}>
            <dt className="text-fg-faint">{k}</dt>
            <dd className={cn("min-w-0 break-all", k === "Error" ? "text-bad" : "font-mono text-[12px] text-fg-muted")}>{v}</dd>
          </Fragment>
        ))}
    </dl>
  );
}
