import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { ChevronRight, Download, Search, ShieldAlert, ShieldCheck, X } from "lucide-react";
import { api, type AuditEntry, type AuditOutcome, type AuditVerifyReport } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Badge, Button, ErrorBanner, IconTile, Input, Kpi, KpiGrid, SectionTitle, Select, Skeleton } from "../components/ui";
import { ICONS } from "../lib/icons";
import { formatDateTime } from "../lib/format";

// Action families an operator filters by. Values use the API's "prefix.*" form.
const ACTION_FILTERS: [string, string][] = [
  ["", "All actions"],
  ["auth.*", "Sign-ins"],
  ["keys.*", "API keys"],
  ["accounts.*", "Accounts"],
  ["chains.*", "Chains"],
  ["plans.*", "Plans"],
  ["budgets.*", "Budgets"],
  ["guardrails.*", "Guardrails"],
  ["settings.*", "Settings"],
  ["proxy-pools.*", "Proxy pools"],
  ["system.*", "System"],
];

const OUTCOME_LABEL: Record<AuditOutcome, string> = {
  success: "Success",
  failure: "Failed",
  denied: "Denied",
};

const OUTCOME_TONE: Record<AuditOutcome, "success" | "warning" | "danger"> = {
  success: "success",
  failure: "warning",
  denied: "danger",
};

const PAGE_SIZE = 100;

const iconButton =
  "flex h-9 w-9 shrink-0 items-center justify-center md:h-8 md:w-8 rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

function actorLabel(e: AuditEntry): string {
  if (e.actor_type === "system") return "System";
  if (e.actor_type === "dashboard") return e.actor_id ? `Dashboard · ${e.actor_id.replace(/^session:/, "")}` : "Dashboard (signed out)";
  return e.actor_id || "—";
}

function resourceLabel(e: AuditEntry): string {
  if (!e.resource_type) return "—";
  return e.resource_id ? `${e.resource_type}/${e.resource_id}` : e.resource_type;
}

function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function AuditPage() {
  const toast = useToast();
  const [action, setAction] = useState("");
  const [outcome, setOutcome] = useState("");
  const [search, setSearch] = useState("");
  const q = useDebounced(search.trim(), 300);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [report, setReport] = useState<AuditVerifyReport | null>(null);

  const status = useQuery({ queryKey: ["audit-status"], queryFn: api.auditStatus, refetchInterval: 10_000 });

  const entries = useInfiniteQuery({
    queryKey: ["audit", action, outcome, q],
    queryFn: ({ pageParam }) => api.listAudit({ action, outcome, q, cursor: pageParam, limit: PAGE_SIZE }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
    refetchInterval: 15_000,
  });
  const rows = useMemo(() => entries.data?.pages.flatMap((p) => p.entries) ?? [], [entries.data]);

  const verify = useMutation({
    mutationFn: api.verifyAudit,
    onSuccess: (rep) => setReport(rep),
    onError: (err) => toast.error(err instanceof Error ? err.message : "Verification failed"),
  });

  const exportLog = useMutation({
    mutationFn: () => api.exportAudit({ action, outcome, q }),
    onSuccess: (blob) => {
      const stamp = new Date().toISOString().replace(/[:T]/g, "-").slice(0, 19);
      downloadBlob(blob, `keirouter-audit-${stamp}.jsonl`);
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Export failed"),
  });

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const filtered = !!(action || outcome || q);
  const st = status.data?.stats;
  const otlp = st?.otlp;

  return (
    <div>
      <PageHeader
        title="Audit log"
        icon={ICONS.audit}
        description="Every dashboard sign-in and configuration change: who did it, from where, and whether it worked. Rows are hash-chained so tampering is detectable."
        action={
          <>
            <Button variant="ghost" onClick={() => verify.mutate()} disabled={verify.isPending}>
              <ShieldCheck aria-hidden="true" />
              {verify.isPending ? "Verifying…" : "Verify integrity"}
            </Button>
            <Button variant="secondary" onClick={() => exportLog.mutate()} disabled={exportLog.isPending}>
              <Download aria-hidden="true" />
              {exportLog.isPending ? "Exporting…" : "Export JSONL"}
            </Button>
          </>
        }
      />

      {report && <VerifyBanner report={report} onClose={() => setReport(null)} />}

      <div className="mb-6">
        <KpiGrid cols={4} label="Audit pipeline">
          <Kpi
            icon={ICONS.database}
            label="Recorded"
            value={st ? st.written.toLocaleString() : "—"}
            hint={st && !st.enabled ? "Auditing is off (audit.enabled)" : "since this instance started"}
          />
          <Kpi
            icon={ICONS.errors}
            label="Lost"
            value={st ? (st.dropped + st.failed).toLocaleString() : "—"}
            tone={st && st.dropped + st.failed > 0 ? "warn" : "section"}
            hint={st ? `${st.dropped} queue full · ${st.failed} write errors` : undefined}
          />
          <Kpi
            icon={ICONS.network}
            label="OpenTelemetry export"
            value={otlp ? otlp.exported.toLocaleString() : "Off"}
            tone={otlp?.last_error ? "warn" : "section"}
            hint={
              otlp ? (
                <span title={otlp.last_error || otlp.endpoint}>{otlp.last_error ? `Error: ${otlp.last_error}` : otlp.endpoint}</span>
              ) : (
                "Set audit.otlp.endpoint or OTEL_EXPORTER_OTLP_ENDPOINT"
              )
            }
          />
          <Kpi
            icon={ICONS.uptime}
            label="Retention"
            value={status.data ? (status.data.retention_days > 0 ? `${status.data.retention_days} days` : "Forever") : "—"}
            hint="audit.retention_days"
          />
        </KpiGrid>
      </div>

      <section aria-labelledby="audit-title" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="flex flex-col gap-3 border-b border-line px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
          <SectionTitle id="audit-title" icon={ICONS.audit} title="Events" />
          <div className="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center">
            <div className="relative col-span-2 sm:w-64">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" aria-hidden="true" />
              <label className="sr-only" htmlFor="audit-search">Search</label>
              <Input
                id="audit-search"
                className="w-full pl-8"
                placeholder="Search action, resource, actor, IP"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <label className="sr-only" htmlFor="audit-action">Action</label>
            <Select id="audit-action" className="w-full sm:w-40" value={action} onChange={(e) => setAction(e.target.value)}>
              {ACTION_FILTERS.map(([v, label]) => (
                <option key={v} value={v}>{label}</option>
              ))}
            </Select>
            <label className="sr-only" htmlFor="audit-outcome">Outcome</label>
            <Select id="audit-outcome" className="w-full sm:w-36" value={outcome} onChange={(e) => setOutcome(e.target.value)}>
              <option value="">All outcomes</option>
              <option value="success">Success</option>
              <option value="failure">Failed</option>
              <option value="denied">Denied</option>
            </Select>
          </div>
        </div>

        {entries.isLoading ? (
          <div className="space-y-2 px-4 py-4" aria-busy="true" aria-label="Loading audit log">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </div>
        ) : entries.isError && rows.length === 0 ? (
          <div className="px-4 py-4">
            <ErrorBanner message={`Couldn't load the audit log. ${entries.error instanceof Error ? entries.error.message : ""}`.trim()} />
          </div>
        ) : rows.length === 0 ? (
          <div className="px-6 py-12 text-center">
            <IconTile icon={filtered ? Search : ICONS.audit} size="lg" tone={filtered ? "slate" : "section"} className="mx-auto mb-3" />
            <p className="text-[13px] font-semibold text-fg">{filtered ? "No events match" : "No events yet"}</p>
            <p className="mx-auto mt-1 max-w-md text-[12.5px] text-fg-muted">
              {filtered ? "Try another action, outcome or search." : "Sign-ins and configuration changes appear here as they happen."}
            </p>
            {filtered && (
              <Button
                variant="ghost"
                className="mt-4"
                onClick={() => {
                  setAction("");
                  setOutcome("");
                  setSearch("");
                }}
              >
                Clear filters
              </Button>
            )}
          </div>
        ) : (
          <>
            <ul className="divide-y divide-line md:hidden" aria-label="Audit events">
              {rows.map((e) => (
                <li key={e.id}>
                  <button
                    type="button"
                    className="flex w-full flex-col gap-1 px-4 py-3 text-left hover:bg-hover"
                    aria-expanded={expanded.has(e.id)}
                    onClick={() => toggle(e.id)}
                  >
                    <span className="flex items-center justify-between gap-2">
                      <span className="truncate font-mono text-[12.5px] text-fg">{e.action}</span>
                      <Badge tone={OUTCOME_TONE[e.outcome] ?? "neutral"}>{OUTCOME_LABEL[e.outcome] ?? e.outcome}</Badge>
                    </span>
                    <span className="truncate text-[12px] text-fg-muted">
                      {new Date(e.time).toLocaleString()} · {actorLabel(e)}
                    </span>
                    {e.resource_type && <span className="truncate font-mono text-[12px] text-fg-muted">{resourceLabel(e)}</span>}
                  </button>
                  {expanded.has(e.id) && <EntryDetails entry={e} className="px-4 pb-4" />}
                </li>
              ))}
            </ul>
            <div className="hidden overflow-x-auto md:block">
              <table className="w-full min-w-[880px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="w-10 py-2 pl-2 pr-0"><span className="sr-only">Details</span></th>
                    <th scope="col" className="px-3 py-2 font-medium">Time</th>
                    <th scope="col" className="px-3 py-2 font-medium">Action</th>
                    <th scope="col" className="px-3 py-2 font-medium">Actor</th>
                    <th scope="col" className="px-3 py-2 font-medium">Resource</th>
                    <th scope="col" className="px-3 py-2 font-medium">Outcome</th>
                    <th scope="col" className="px-3 py-2 pr-4 font-medium">Client</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {rows.map((e) => {
                    const open = expanded.has(e.id);
                    const when = formatDateTime(e.time);
                    const detailsId = `audit-${e.id}`;
                    return (
                      <Fragment key={e.id}>
                        <tr className={cn("cursor-pointer transition-colors hover:bg-hover", open && "bg-hover")} onClick={() => toggle(e.id)}>
                          <td className="py-1.5 pl-2 pr-0">
                            <button
                              type="button"
                              aria-expanded={open}
                              aria-controls={open ? detailsId : undefined}
                              aria-label={`Details for ${e.action}, ${when}`}
                              onClick={(ev) => {
                                ev.stopPropagation();
                                toggle(e.id);
                              }}
                              className={iconButton}
                            >
                              <ChevronRight className={cn("h-4 w-4 text-fg-faint transition-transform", open && "rotate-90")} strokeWidth={1.75} aria-hidden="true" />
                            </button>
                          </td>
                          <td className="whitespace-nowrap px-3 py-2 tabular-nums text-fg-muted">{when}</td>
                          <td className="px-3 py-2 font-mono text-[12px] text-fg">{e.action}</td>
                          <td className="max-w-[220px] truncate px-3 py-2 text-fg-muted" title={e.actor_id}>{actorLabel(e)}</td>
                          <td className="max-w-[240px] truncate px-3 py-2 font-mono text-[12px] text-fg-muted" title={resourceLabel(e)}>{resourceLabel(e)}</td>
                          <td className="px-3 py-2">
                            <Badge tone={OUTCOME_TONE[e.outcome] ?? "neutral"}>{OUTCOME_LABEL[e.outcome] ?? e.outcome}</Badge>
                          </td>
                          <td className="whitespace-nowrap px-3 py-2 pr-4 font-mono text-[12px] text-fg-muted">{e.client_ip || "—"}</td>
                        </tr>
                        {open && (
                          <tr id={detailsId} className="bg-hover">
                            <td />
                            <td colSpan={6} className="px-3 pb-4 pt-1">
                              <EntryDetails entry={e} />
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {entries.hasNextPage && (
              <div className="border-t border-line px-4 py-3 text-center">
                <Button variant="ghost" onClick={() => entries.fetchNextPage()} disabled={entries.isFetchingNextPage}>
                  {entries.isFetchingNextPage ? "Loading…" : "Load older events"}
                </Button>
              </div>
            )}
          </>
        )}
      </section>
    </div>
  );
}

function EntryDetails({ entry: e, className }: { entry: AuditEntry; className?: string }) {
  const fields: [string, ReactNode][] = [
    ["Request", e.method ? `${e.method} ${e.route}${e.status_code ? ` → ${e.status_code}` : ""}` : null],
    ["Fields sent", e.details?.fields?.split(",").join(", ")],
    ["Actor", e.actor_id ? `${e.actor_type} ${e.actor_id}` : e.actor_type],
    ["Forwarded for", e.details?.forwarded_for],
    ["User agent", e.user_agent],
    ["Request ID", e.request_id],
    ["Trace", e.trace_id ? `${e.trace_id}${e.span_id ? ` / ${e.span_id}` : ""}` : null],
    ["Chain", e.chain_id ? `${e.chain_id} #${e.chain_seq}` : null],
    ["Hash", e.hash],
  ];
  const extra = Object.entries(e.details ?? {}).filter(([k]) => k !== "fields" && k !== "forwarded_for");
  return (
    <dl className={cn("grid gap-x-6 gap-y-1.5 text-[12.5px] sm:grid-cols-[140px_1fr]", className)}>
      {[...fields, ...extra.map(([k, v]) => [k.replace(/_/g, " "), v] as [string, ReactNode])]
        .filter(([, v]) => v)
        .map(([k, v]) => (
          <Fragment key={k}>
            <dt className="text-fg-faint first-letter:uppercase">{k}</dt>
            <dd className="min-w-0 break-all font-mono text-[12px] text-fg-muted">{v}</dd>
          </Fragment>
        ))}
    </dl>
  );
}

function VerifyBanner({ report, onClose }: { report: AuditVerifyReport; onClose: () => void }) {
  const ok = report.ok;
  return (
    <div
      role="status"
      className={cn(
        "mb-6 flex items-start gap-3 rounded-2xl border px-4 py-3",
        ok ? "border-ok/25 bg-ok/8" : "border-bad/30 bg-bad/8",
      )}
    >
      <IconTile icon={ok ? ShieldCheck : ShieldAlert} size="sm" tone={ok ? "ok" : "bad"} />
      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-semibold text-fg">
          {ok
            ? `Integrity verified: ${report.entries.toLocaleString()} events across ${report.chains} chain${report.chains === 1 ? "" : "s"}`
            : `Integrity check failed: ${report.breaks.length}${report.truncated ? "+" : ""} problem${report.breaks.length === 1 ? "" : "s"} found`}
        </p>
        {ok ? (
          <p className="mt-0.5 text-[12.5px] text-fg-muted">No event was edited, inserted or removed since it was written.</p>
        ) : (
          <ul className="mt-1.5 space-y-1 text-[12.5px] text-fg-muted">
            {report.breaks.slice(0, 8).map((b) => (
              <li key={`${b.chain_id}-${b.seq}`}>
                <span className="font-mono text-fg">{b.chain_id} #{b.seq}</span> — {b.reason}
              </li>
            ))}
          </ul>
        )}
      </div>
      <button type="button" onClick={onClose} className={iconButton} aria-label="Dismiss">
        <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
      </button>
    </div>
  );
}
