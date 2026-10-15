import { useEffect, useId, useRef, useState, useMemo, useCallback, useDeferredValue, memo, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Trash2, Search, X, Copy, Check, ChevronRight, ArrowDown, Download, HelpCircle, Lightbulb, RefreshCw } from "lucide-react";
import { PageHeader } from "../components/Layout";
import { Card, Button, IconTile, Skeleton, Toggle, SegmentedControl } from "../components/ui";
import { HighlightedCode } from "../components/ui/code-block";
import { ICONS } from "../lib/icons";
import { useConfirm } from "../components/ui/confirm-dialog";
import { useToast } from "../components/Toast";
import { cn } from "@/lib/utils";
import type { ConsoleLogEntry } from "../lib/api";
import { parseQuery, matches, shortReq, queryToken, isFailed, type ParsedQuery } from "../lib/consoleQuery";

// ── Types & constants ────────────────────────────────────────────────────────

type LogLevel = "DEBUG" | "INFO" | "WARN" | "ERROR" | "LOG";
type View = "requests" | "lines";
type StreamState = "connecting" | "live" | "reconnecting" | "signedout";

const LEVELS: LogLevel[] = ["ERROR", "WARN", "INFO", "DEBUG"];

// Mirrors the server's ring buffer (consolelog.MaxEntries).
const MAX_LINES = 1000;
// Incoming lines are applied to React state at most this often.
const FLUSH_MS = 200;
// The server sends a heartbeat every 15s; silence this long means the stream
// is dead even if the connection looks open (e.g. a proxy stopped forwarding).
const STREAM_SILENCE_MS = 40_000;
// A request with no new line for this long is shown as "no outcome".
const STALE_MS = 10 * 60_000;
const ROW_HEIGHT = 26;

const LEVEL_TEXT: Record<LogLevel, string> = {
  DEBUG: "text-fg-faint",
  INFO: "text-link",
  WARN: "text-warn",
  ERROR: "text-bad",
  LOG: "text-fg-faint",
};

const LEVEL_DOT: Record<LogLevel, string> = {
  DEBUG: "bg-fg-faint",
  INFO: "bg-accent-500",
  WARN: "bg-warn",
  ERROR: "bg-bad",
  LOG: "bg-fg-faint",
};

const LEVEL_PRESSED: Record<LogLevel, string> = {
  DEBUG: "border-line-strong bg-subtle text-fg",
  INFO: "border-accent-500/30 bg-accent-500/10 text-link",
  WARN: "border-warn/30 bg-warn/12 text-warn",
  ERROR: "border-bad/25 bg-bad/10 text-bad",
  LOG: "border-line-strong bg-subtle text-fg",
};

const LEVEL_LABEL: Record<LogLevel, string> = {
  DEBUG: "Debug",
  INFO: "Info",
  WARN: "Warn",
  ERROR: "Error",
  LOG: "Log",
};

// Requests view hides the trivial DEBUG outcomes (model listings, etc.) by
// default; All lines shows every step.
const DEFAULT_LEVELS: Record<View, LogLevel[]> = {
  requests: ["ERROR", "WARN", "INFO"],
  lines: ["ERROR", "WARN", "INFO", "DEBUG"],
};

const SEARCH_EXAMPLES: { q: string; label: string }[] = [
  { q: "is:failed", label: "Requests that failed" },
  { q: "status:429", label: "Exact status (or status:5xx)" },
  { q: "key:prod", label: "One API key (also model:, provider:, client:)" },
  { q: "ms>10s", label: "Slower than 10 seconds" },
  { q: "is:fallback", label: "Needed a retry or fallback" },
  { q: "is:inflight", label: "Still running" },
  { q: "is:system", label: "Background events (oauth, health, tunnel)" },
  { q: "#42", label: "One request by its number" },
  { q: '-key:test "rate-limited"', label: "Exclude with -, quote phrases" },
];

function normalizeLevel(level: string): LogLevel {
  const up = (level || "").toUpperCase();
  return up === "DEBUG" || up === "INFO" || up === "WARN" || up === "ERROR" ? up : "LOG";
}

// ── Rows ─────────────────────────────────────────────────────────────────────

interface Row {
  key: string;
  /** The line that speaks for the row: the request's final line, else its latest. */
  head: ConsoleLogEntry;
  lines: ConsoleLogEntry[];
  isRequest: boolean;
  inflight: boolean;
  stale: boolean;
  /** Changes whenever the row's content changes (memo key). */
  version: number;
}

interface Grouped {
  rows: Row[];
  byReq: Map<string, Row>;
  stats: { requests: number; failed: number; inflight: number };
}

// One row per request (ordered by arrival, updated in place as it progresses)
// plus one row per standalone line (system events, admin actions).
function groupEntries(entries: ConsoleLogEntry[], now: number): Grouped {
  const rows: Row[] = [];
  const byReq = new Map<string, Row>();
  for (const e of entries) {
    if (e.req) {
      let row = byReq.get(e.req);
      if (!row) {
        row = { key: `r:${e.req}`, head: e, lines: [], isRequest: true, inflight: true, stale: false, version: 0 };
        byReq.set(e.req, row);
        rows.push(row);
      }
      row.lines.push(e);
      row.version = e.seq;
      if (!row.head.final) row.head = e;
    } else {
      rows.push({ key: `s:${e.seq}`, head: e, lines: [e], isRequest: false, inflight: false, stale: false, version: e.seq });
    }
  }
  const stats = { requests: byReq.size, failed: 0, inflight: 0 };
  for (const row of byReq.values()) {
    row.inflight = !row.head.final;
    if (row.inflight) {
      const last = row.lines[row.lines.length - 1];
      row.stale = !!last.ts && now - last.ts > STALE_MS;
      if (!row.stale) stats.inflight++;
    } else if (isFailed(row.head)) {
      stats.failed++;
    }
  }
  return { rows, byReq, stats };
}

function lineRows(entries: ConsoleLogEntry[]): Row[] {
  return entries.map((e) => ({ key: `s:${e.seq}`, head: e, lines: [e], isRequest: false, inflight: false, stale: false, version: e.seq }));
}

// ── Formatting ───────────────────────────────────────────────────────────────

function fmtMs(ms?: number): string {
  if (ms === undefined) return "";
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
}

function fmtCost(micros?: number): string {
  if (!micros) return "";
  const usd = micros / 1_000_000;
  return usd < 0.01 ? `$${usd.toFixed(4)}` : `$${usd.toFixed(2)}`;
}

function fullTime(e: ConsoleLogEntry): string {
  return e.ts ? new Date(e.ts).toLocaleString() : e.time;
}

function formatLine(l: ConsoleLogEntry): string {
  const tag = l.req ? ` ${shortReq(l.req)}` : l.kind && l.kind !== "request" ? ` [${l.kind}]` : "";
  const meta = [
    l.key && `key=${l.key}`,
    l.model && `model=${l.model}`,
    l.target && `served=${l.target}`,
    l.status && `status=${l.status}`,
    l.ms !== undefined && l.final && `ms=${l.ms}`,
  ]
    .filter(Boolean)
    .join(" ");
  let out = `${l.time} ${normalizeLevel(l.level).padEnd(5)}${tag} ${l.msg}${meta ? `  | ${meta}` : ""}`;
  const indent = (s: string) => s.split("\n").map((line) => "    " + line).join("\n");
  if (l.hint) out += `\n${indent("Hint: " + l.hint)}`;
  if (l.detail && l.detail.trim()) out += `\n${indent(l.detail)}`;
  return out;
}

function formatRows(rows: Row[], view: View): string {
  return rows.map((r) => (view === "requests" && r.isRequest ? r.lines.map(formatLine).join("\n") : formatLine(r.head))).join("\n");
}

function logFileName(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `keirouter-console-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}.log`;
}

// Leading "Key: value" lines of a detail block become fields; the rest (raw
// error text, target chains) stays verbatim. JSON bodies are pretty-printed.
const FIELD_LINE = /^([A-Za-z][\w .\-/()]{0,30}?):\s+(\S.*)$/;

function parseDetail(detail: string): { fields: [string, string][]; body: string; json: boolean } {
  const trimmed = detail.trim();
  if (!trimmed) return { fields: [], body: "", json: false };
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      return { fields: [], body: JSON.stringify(JSON.parse(trimmed), null, 2), json: true };
    } catch {
      /* not JSON */
    }
  }
  const lines = detail.replace(/\s+$/, "").split("\n");
  const fields: [string, string][] = [];
  let i = 0;
  for (; i < lines.length; i++) {
    const m = FIELD_LINE.exec(lines[i]);
    if (!m) break;
    fields.push([m[1].trim(), m[2].trim()]);
  }
  return { fields, body: lines.slice(i).join("\n").replace(/^\s*\n/, ""), json: false };
}

// ── Highlighting ─────────────────────────────────────────────────────────────

function Highlight({ text, terms }: { text: string; terms: string[] }) {
  if (terms.length === 0 || !text) return <>{text}</>;
  const lower = text.toLowerCase();
  const ranges: [number, number][] = [];
  for (const t of terms) {
    if (!t) continue;
    let i = lower.indexOf(t);
    while (i !== -1) {
      ranges.push([i, i + t.length]);
      i = lower.indexOf(t, i + t.length);
    }
  }
  if (ranges.length === 0) return <>{text}</>;
  ranges.sort((a, b) => a[0] - b[0]);
  const nodes: ReactNode[] = [];
  let from = 0;
  for (const [s, e] of ranges) {
    if (e <= from) continue;
    const start = Math.max(s, from);
    if (start > from) nodes.push(text.slice(from, start));
    nodes.push(
      <mark key={start} className="rounded-[2px] bg-accent-500/20 text-fg dark:bg-accent-400/25">
        {text.slice(start, e)}
      </mark>,
    );
    from = e;
  }
  if (from < text.length) nodes.push(text.slice(from));
  return <>{nodes}</>;
}

// ── Badges ───────────────────────────────────────────────────────────────────

function statusTone(status: number): string {
  if (status === 499) return "border-line-strong bg-subtle text-fg-muted";
  if (status >= 500) return "border-bad/25 bg-bad/10 text-bad";
  if (status >= 400) return "border-warn/30 bg-warn/12 text-warn";
  return "border-ok/25 bg-ok/10 text-ok";
}

function RowBadge({ row, view }: { row: Row; view: View }) {
  const h = row.head;
  const level = normalizeLevel(h.level);
  if (view === "requests" && row.isRequest) {
    if (row.inflight) {
      return row.stale ? (
        <span className="inline-flex h-[18px] items-center rounded border border-line px-1 text-[11px] text-fg-faint" title="No outcome recorded (the request may have been cut off)">
          ?
        </span>
      ) : (
        <span className="inline-flex h-[18px] items-center gap-1 rounded border border-accent-500/30 bg-accent-500/10 px-1 text-[11px] text-link" title="In flight">
          <span className="live-dot h-1.5 w-1.5 rounded-full bg-accent-500" aria-hidden="true" />
          <span className="sr-only">In flight</span>…
        </span>
      );
    }
    if (h.status) {
      return (
        <span className={cn("inline-flex h-[18px] items-center rounded border px-1 text-[11px] font-medium tabular-nums", statusTone(h.status))} title={h.status === 499 ? "Client disconnected" : `HTTP ${h.status}`}>
          {h.status}
        </span>
      );
    }
  }
  return <span className={cn("font-medium", LEVEL_TEXT[level])}>{level !== "LOG" ? level : ""}</span>;
}

function SourceTag({ entry, onFilter }: { entry: ConsoleLogEntry; onFilter: (token: string) => void }) {
  if (entry.req) {
    const tag = shortReq(entry.req);
    return (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onFilter(tag);
        }}
        title={`Show only request ${tag}`}
        className="mr-1.5 rounded px-1 text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        {tag}
      </button>
    );
  }
  if (entry.kind && entry.kind !== "request") {
    return <span className="mr-1.5 rounded bg-subtle px-1 text-[11px] text-fg-muted ring-1 ring-inset ring-line">{entry.kind}</span>;
  }
  return null;
}

// ── Row ──────────────────────────────────────────────────────────────────────

interface RowProps {
  row: Row;
  view: View;
  expanded: boolean;
  terms: string[];
  onToggle: (key: string) => void;
  onFilter: (token: string) => void;
  onCopy: (row: Row) => void;
  onOpenRequest: (req: string) => void;
}

const LogRow = memo(
  function LogRow({ row, view, expanded, terms, onToggle, onFilter, onCopy, onOpenRequest }: RowProps) {
    const h = row.head;
    const first = row.lines[0];
    const showTimeline = view === "requests" && row.isRequest;
    const time = showTimeline ? first.time : h.time;
    const toggle = () => {
      const sel = window.getSelection();
      if (sel && sel.type === "Range" && sel.toString().length > 0) return;
      onToggle(row.key);
    };
    const detailId = `log-detail-${row.key}`;
    const meta = showTimeline && !row.inflight ? fmtMs(h.ms ?? 0) : "";

    return (
      <div className={cn("border-b border-line/60", expanded && "bg-subtle")}>
        <div onClick={toggle} className="flex cursor-pointer flex-wrap items-start gap-x-3 px-3 py-[4px] transition-colors hover:bg-hover sm:flex-nowrap" style={{ minHeight: ROW_HEIGHT }}>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onToggle(row.key);
            }}
            aria-expanded={expanded}
            aria-controls={expanded ? detailId : undefined}
            aria-label={`Details: ${time} ${h.msg.slice(0, 80)}`}
            className="-my-[3px] flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
          >
            <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-90")} strokeWidth={2} aria-hidden="true" />
          </button>
          <span className="w-[88px] shrink-0 whitespace-nowrap tabular-nums text-fg-faint" title={fullTime(showTimeline ? first : h)}>
            {time}
          </span>
          <span className="flex w-12 shrink-0 items-center">
            <RowBadge row={row} view={view} />
          </span>
          <span className="min-w-0 basis-full whitespace-pre-wrap break-words pl-9 text-fg [overflow-wrap:anywhere] sm:flex-1 sm:basis-0 sm:pl-0" title={h.hint || undefined}>
            <SourceTag entry={h} onFilter={onFilter} />
            <span className={cn(row.stale && "text-fg-muted")}>
              <Highlight text={h.msg} terms={terms} />
            </span>
          </span>
          {(h.key || meta) && (
            <span className="hidden shrink-0 items-center gap-2 whitespace-nowrap text-fg-faint md:flex">
              {h.key && (
                <span className="max-w-[140px] truncate" title={`API key: ${h.key}`}>
                  <Highlight text={h.key} terms={terms} />
                </span>
              )}
              {meta && <span className="w-14 text-right tabular-nums">{meta}</span>}
            </span>
          )}
        </div>

        {expanded && (
          <div id={detailId} className="pb-3 pl-9 pr-4 pt-1 sm:pl-[204px]">
            <RowDetail row={row} view={view} terms={terms} onFilter={onFilter} onCopy={onCopy} onOpenRequest={onOpenRequest} />
          </div>
        )}
      </div>
    );
  },
  (a, b) =>
    a.row.key === b.row.key &&
    a.row.version === b.row.version &&
    a.row.inflight === b.row.inflight &&
    a.row.stale === b.row.stale &&
    a.view === b.view &&
    a.expanded === b.expanded &&
    a.terms === b.terms &&
    a.onToggle === b.onToggle &&
    a.onFilter === b.onFilter,
);

// ── Expanded detail ──────────────────────────────────────────────────────────

function FilterValue({ field, value, label, terms, onFilter }: { field: string; value: string; label?: string; terms: string[]; onFilter: (t: string) => void }) {
  return (
    <button
      type="button"
      onClick={() => onFilter(field === "#" ? value : queryToken(field, value))}
      title={`Filter by ${field === "#" ? "this request" : `${field}: ${value}`}`}
      className="rounded text-left text-fg underline decoration-line-strong decoration-dotted underline-offset-2 hover:decoration-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    >
      <Highlight text={label ?? value} terms={terms} />
    </button>
  );
}

function RowDetail({ row, view, terms, onFilter, onCopy, onOpenRequest }: Omit<RowProps, "expanded" | "onToggle">) {
  const h = row.head;
  const showTimeline = view === "requests" && row.isRequest;
  const parsed = useMemo(() => parseDetail(h.detail ?? ""), [h.detail]);

  const facts: { label: string; node: ReactNode }[] = [];
  const add = (label: string, node: ReactNode | undefined | false) => {
    if (node !== undefined && node !== false && node !== "") facts.push({ label, node });
  };
  if (h.req) add("Request", <FilterValue field="#" value={shortReq(h.req)} label={`${shortReq(h.req)}  (${h.req})`} terms={terms} onFilter={onFilter} />);
  if (h.kind && h.kind !== "request") add("Source", <FilterValue field="kind" value={h.kind} terms={terms} onFilter={onFilter} />);
  add("Endpoint", h.endpoint);
  if (h.key) add("API key", <FilterValue field="key" value={h.key} terms={terms} onFilter={onFilter} />);
  if (h.client) add("Client", <FilterValue field="client" value={h.client} terms={terms} onFilter={onFilter} />);
  if (h.model) add("Model", <FilterValue field="model" value={h.model} terms={terms} onFilter={onFilter} />);
  if (h.target) add(h.final ? "Served by" : "Target", <FilterValue field="target" value={h.target} terms={terms} onFilter={onFilter} />);
  else if (h.provider) add("Provider", <FilterValue field="provider" value={h.provider} terms={terms} onFilter={onFilter} />);
  if (h.status) add("Status", <FilterValue field="status" value={String(h.status)} label={h.status === 499 ? "499 (client disconnected)" : String(h.status)} terms={terms} onFilter={onFilter} />);
  if (h.final) add("Duration", fmtMs(h.ms ?? 0));
  if (h.tokens) add("Tokens", h.tokens.toLocaleString());
  add("Cost", fmtCost(h.cost_micros));
  if (h.fallbacks) add("Attempts", String(h.fallbacks + 1));
  const flags = [h.stream && "streaming", h.cache && "cache hit"].filter(Boolean).join(", ");
  add("Mode", flags);
  add("Time", fullTime(row.lines[0]));

  // Detail fields already covered by the facts above are not repeated.
  const shown = new Set(facts.map((f) => f.label.toLowerCase()));
  const extraFields = parsed.fields.filter(([k]) => !shown.has(k.toLowerCase()) && !["key", "client", "duration"].includes(k.toLowerCase()));

  return (
    <div className="space-y-3 border-l border-line-strong pl-3 font-sans text-[12.5px]">
      {h.hint && (
        <p className="flex gap-2 rounded-lg border border-line bg-surface px-2.5 py-2 leading-5 text-fg">
          <Lightbulb className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
          <span>
            <span className="font-medium">What this means: </span>
            {h.hint}
          </span>
        </p>
      )}

      {facts.length > 0 && (
        <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 font-mono text-[12px]">
          {facts.map((f) => (
            <div key={f.label} className="contents">
              <dt className="text-fg-faint">{f.label}</dt>
              <dd className="break-words text-fg [overflow-wrap:anywhere]">{f.node}</dd>
            </div>
          ))}
          {extraFields.map(([k, v], i) => (
            <div key={`x-${k}-${i}`} className="contents">
              <dt className="text-fg-faint">{k}</dt>
              <dd className="break-words text-fg [overflow-wrap:anywhere]">
                <Highlight text={v} terms={terms} />
              </dd>
            </div>
          ))}
        </dl>
      )}

      {showTimeline && row.lines.length > 1 && <Timeline lines={row.lines} terms={terms} />}

      {!showTimeline && parsed.body && <DetailBody body={parsed.body} json={parsed.json} terms={terms} />}
      {showTimeline && parsed.body && row.lines.length <= 1 && <DetailBody body={parsed.body} json={parsed.json} terms={terms} />}

      <div className="flex flex-wrap gap-1">
        <SmallAction onClick={() => onCopy(row)} icon={<Copy className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />}>
          {showTimeline ? "Copy request" : "Copy line"}
        </SmallAction>
        {!showTimeline && h.req && <SmallAction onClick={() => onOpenRequest(h.req!)}>Open whole request {shortReq(h.req)}</SmallAction>}
      </div>
    </div>
  );
}

function DetailBody({ body, json, terms }: { body: string; json: boolean; terms: string[] }) {
  return (
    <pre className={cn("whitespace-pre-wrap break-words font-mono text-[12px] leading-[1.55] [overflow-wrap:anywhere]", json ? "text-fg" : "text-fg-muted")}>
      {json && terms.length === 0 ? <HighlightedCode code={body} language="json" /> : <Highlight text={body} terms={terms} />}
    </pre>
  );
}

function Timeline({ lines, terms }: { lines: ConsoleLogEntry[]; terms: string[] }) {
  const t0 = lines[0].ts ?? 0;
  return (
    <div>
      <p className="mb-1 text-[12px] font-medium text-fg-muted">Timeline</p>
      <ol className="space-y-0.5 font-mono text-[12px]">
        {lines.map((l) => {
          const level = normalizeLevel(l.level);
          // Request lines carry ms since the request arrived (before auth), so
          // time spent ahead of the first step stays visible.
          const offset = l.ms !== undefined || l.req ? `+${fmtMs(l.ms ?? 0)}` : l.ts && t0 ? `+${fmtMs(l.ts - t0)}` : l.time;
          const more = (l.detail && l.detail.trim()) || (l.hint && !l.final);
          const head = (
            <span className="flex min-w-0 gap-2">
              <span className="w-14 shrink-0 text-right tabular-nums text-fg-faint">{offset}</span>
              <span className={cn("mt-[5px] h-1.5 w-1.5 shrink-0 rounded-full", LEVEL_DOT[level])} title={level} aria-hidden="true" />
              <span className={cn("min-w-0 break-words [overflow-wrap:anywhere]", l.final ? "font-medium text-fg" : "text-fg")}>
                <Highlight text={l.msg} terms={terms} />
              </span>
            </span>
          );
          if (!more) return <li key={l.seq} className="pl-4">{head}</li>;
          return (
            <li key={l.seq}>
              <details className="group">
                <summary className="flex cursor-pointer list-none items-start gap-0 rounded hover:bg-hover [&::-webkit-details-marker]:hidden">
                  <ChevronRight className="mt-[3px] h-3 w-4 shrink-0 text-fg-faint transition-transform group-open:rotate-90" strokeWidth={2} aria-hidden="true" />
                  {head}
                </summary>
                <div className="ml-[88px] mt-0.5 space-y-1 border-l border-line pl-2">
                  {l.hint && !l.final && <p className="font-sans text-[12px] leading-5 text-fg-muted">{l.hint}</p>}
                  {l.detail && (
                    <pre className="whitespace-pre-wrap break-words text-fg-muted [overflow-wrap:anywhere]">
                      <Highlight text={l.detail} terms={terms} />
                    </pre>
                  )}
                </div>
              </details>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function SmallAction({ onClick, icon, children }: { onClick: () => void; icon?: ReactNode; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className="inline-flex h-9 items-center gap-1 rounded-md px-1.5 text-[12px] text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 sm:h-6"
    >
      {icon}
      {children}
    </button>
  );
}

// ── Small local pieces ───────────────────────────────────────────────────────

function IconButton({ label, onClick, disabled, children, tone = "neutral", pressed }: { label: string; onClick: () => void; disabled?: boolean; children: ReactNode; tone?: "neutral" | "danger"; pressed?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      aria-pressed={pressed}
      title={label}
      className={cn(
        "inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-50 sm:h-8 sm:w-8 [&_svg]:h-4 [&_svg]:w-4",
        tone === "danger" ? "hover:text-bad" : "hover:text-fg",
        pressed && "border-line-strong bg-subtle text-fg",
      )}
    >
      {children}
    </button>
  );
}

function StreamStatus({ status, className }: { status: { label: string; dot: string; live: boolean }; className?: string }) {
  return (
    <span className={cn("inline-flex h-8 items-center gap-1.5 px-1 text-[12px] font-medium text-fg-muted", className)}>
      <span className={cn("h-1.5 w-1.5 rounded-full", status.dot, status.live && "live-dot")} aria-hidden="true" />
      {status.label}
    </span>
  );
}

function BodyMessage({ title, hint, action }: { title: string; hint: string; action?: ReactNode }) {
  return (
    <div className="flex h-full min-h-[240px] flex-col items-center justify-center px-6 py-12 text-center font-sans">
      <IconTile icon={ICONS.console} size="lg" className="mb-3" />
      <p className="text-[13px] font-medium text-fg">{title}</p>
      <p className="mx-auto mt-1 max-w-sm text-[12.5px] leading-5 text-fg-muted">{hint}</p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

function loadLevels(view: View): Set<LogLevel> {
  try {
    const raw = localStorage.getItem(`consoleLevels:${view}`);
    if (raw) {
      const list = (JSON.parse(raw) as string[]).filter((l): l is LogLevel => (LEVELS as string[]).includes(l));
      return new Set(list);
    }
  } catch {
    /* storage unavailable */
  }
  return new Set(DEFAULT_LEVELS[view]);
}

function saveLevels(view: View, levels: Set<LogLevel>) {
  try {
    localStorage.setItem(`consoleLevels:${view}`, JSON.stringify([...levels]));
  } catch {
    /* storage unavailable */
  }
}

// ── Stream ───────────────────────────────────────────────────────────────────

// Owns the SSE connection. Lines are queued and applied in batches (and not at
// all while the tab is hidden), a sequence gap triggers a full resync, and a
// closed stream (e.g. after a server restart) reconnects with backoff.
function useConsoleStream() {
  const [entries, setEntries] = useState<ConsoleLogEntry[]>([]);
  const [state, setState] = useState<StreamState>("connecting");
  const [loaded, setLoaded] = useState(false);
  const [epoch, setEpoch] = useState("");
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    let es: EventSource | null = null;
    let disposed = false;
    let retryTimer: number | undefined;
    let flushTimer: number | undefined;
    let attempt = 0;
    let lastSeq = 0;
    let lastResync = 0;
    let lastMessageAt = Date.now();
    let pending: ConsoleLogEntry[] = [];

    const flush = () => {
      flushTimer = undefined;
      if (pending.length === 0 || document.hidden) return;
      const batch = pending;
      pending = [];
      setEntries((prev) => {
        const next = prev.concat(batch);
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
      });
    };
    const schedule = () => {
      if (flushTimer === undefined) flushTimer = window.setTimeout(flush, FLUSH_MS);
    };

    const connect = () => {
      if (disposed) return;
      es?.close();
      const src = new EventSource("/api/console/stream");
      es = src;
      lastMessageAt = Date.now();
      src.onopen = () => {
        attempt = 0;
        setState("live");
      };
      src.onmessage = (ev) => {
        lastMessageAt = Date.now();
        let msg: { type?: string; epoch?: string; logs?: ConsoleLogEntry[]; log?: ConsoleLogEntry };
        try {
          msg = JSON.parse(ev.data);
        } catch {
          return;
        }
        if (msg.type === "init") {
          const logs = msg.logs ?? [];
          pending = [];
          lastSeq = logs.length ? logs[logs.length - 1].seq : 0;
          setEntries(logs.length > MAX_LINES ? logs.slice(-MAX_LINES) : logs);
          setEpoch(msg.epoch ?? "");
          setLoaded(true);
          setState("live");
        } else if (msg.type === "lines" || msg.type === "line") {
          const batch = msg.type === "line" ? (msg.log ? [msg.log] : []) : (msg.logs ?? []);
          let gap = false;
          for (const l of batch) {
            if (lastSeq && l.seq > lastSeq + 1) gap = true;
            lastSeq = Math.max(lastSeq, l.seq);
          }
          // This tab fell behind and the server dropped lines for it: take a
          // fresh snapshot rather than show a timeline with holes.
          if (gap && Date.now() - lastResync > 5000) {
            lastResync = Date.now();
            connect();
            return;
          }
          pending.push(...batch);
          if (pending.length > MAX_LINES) pending = pending.slice(-MAX_LINES);
          schedule();
        } else if (msg.type === "clear") {
          pending = [];
          setEntries([]);
        }
      };
      src.onerror = () => {
        if (disposed || src !== es) return;
        setState("reconnecting");
        // The browser retries CONNECTING streams itself; a CLOSED one (HTTP
        // error, e.g. while the server restarts) needs a manual retry, unless
        // the dashboard session is gone, which retrying cannot fix.
        if (src.readyState === EventSource.CLOSED) {
          window.clearTimeout(retryTimer);
          fetch("/api/console", { method: "GET" })
            .then((res) => {
              if (disposed || src !== es) return;
              if (res.status === 401) {
                setState("signedout");
                return;
              }
              const delay = Math.min(15_000, 1000 * 2 ** attempt);
              attempt++;
              retryTimer = window.setTimeout(connect, delay);
            })
            .catch(() => {
              if (disposed || src !== es) return;
              const delay = Math.min(15_000, 1000 * 2 ** attempt);
              attempt++;
              retryTimer = window.setTimeout(connect, delay);
            });
        }
      };
    };

    const watchdog = window.setInterval(() => {
      if (es && es.readyState === EventSource.OPEN && Date.now() - lastMessageAt > STREAM_SILENCE_MS) {
        setState("reconnecting");
        connect();
      }
    }, 5000);

    const onVisible = () => {
      if (!document.hidden) flush();
    };
    document.addEventListener("visibilitychange", onVisible);
    connect();

    return () => {
      disposed = true;
      es?.close();
      window.clearInterval(watchdog);
      window.clearTimeout(retryTimer);
      window.clearTimeout(flushTimer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [nonce]);

  const reconnect = useCallback(() => {
    setState("connecting");
    setNonce((n) => n + 1);
  }, []);

  return { entries, state, loaded, epoch, reconnect };
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function ConsoleLogPage() {
  const confirm = useConfirm();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const { entries, state, loaded, epoch, reconnect } = useConsoleStream();

  const [view, setView] = useState<View>(() => (params.get("view") === "lines" ? "lines" : "requests"));
  const [search, setSearch] = useState(() => params.get("q") ?? "");
  const [levels, setLevels] = useState<Set<LogLevel>>(() => loadLevels(params.get("view") === "lines" ? "lines" : "requests"));
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [autoScroll, setAutoScroll] = useState(true);
  const [pausedAtKey, setPausedAtKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [showHelp, setShowHelp] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const [now, setNow] = useState(() => Date.now());
  const autoScrollId = useId();
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const isAutoScrolling = useRef(false);

  // Keep the filter in the URL so a view can be bookmarked or shared.
  const deferredSearch = useDeferredValue(search);
  useEffect(() => {
    const t = window.setTimeout(() => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (deferredSearch) next.set("q", deferredSearch);
          else next.delete("q");
          if (view === "lines") next.set("view", "lines");
          else next.delete("view");
          return next;
        },
        { replace: true },
      );
    }, 400);
    return () => window.clearTimeout(t);
  }, [deferredSearch, view, setParams]);

  // A server restart starts sequence numbers over: forget row state.
  useEffect(() => {
    setExpanded(new Set());
    setPausedAtKey(null);
  }, [epoch]);

  // Re-evaluate "stalled" in-flight requests now and then.
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(t);
  }, []);

  // "/" focuses search; Escape clears it.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
      e.preventDefault();
      searchRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // ── Rows & filtering ─────────────────────────────────────────────────────

  const grouped = useMemo(() => groupEntries(entries, now), [entries, now]);
  const allLineRows = useMemo(() => (view === "lines" ? lineRows(entries) : []), [entries, view]);
  const baseRows = view === "requests" ? grouped.rows : allLineRows;

  const query: ParsedQuery = useMemo(() => parseQuery(deferredSearch), [deferredSearch]);
  const terms = query.terms;

  const levelCounts = useMemo(() => {
    const c: Record<LogLevel, number> = { DEBUG: 0, INFO: 0, WARN: 0, ERROR: 0, LOG: 0 };
    for (const r of baseRows) c[normalizeLevel(r.head.level)]++;
    return c;
  }, [baseRows]);

  const filtered = useMemo(() => {
    const allLevels = levels.size === LEVELS.length;
    const hasQuery = query.clauses.length > 0;
    if (allLevels && !hasQuery) return baseRows;
    return baseRows.filter((r) => {
      const lvl = normalizeLevel(r.head.level);
      if (!allLevels && lvl !== "LOG" && !levels.has(lvl)) return false;
      return !hasQuery || matches(query, r.lines, r.head, r.inflight && !r.stale);
    });
  }, [baseRows, levels, query]);

  const filtersActive = search.length > 0 || levels.size !== DEFAULT_LEVELS[view].length || !DEFAULT_LEVELS[view].every((l) => levels.has(l));
  const lastKey = filtered.length > 0 ? filtered[filtered.length - 1].key : null;
  const lastVersion = filtered.length > 0 ? filtered[filtered.length - 1].version : 0;

  const newSincePause = useMemo(() => {
    if (autoScroll || pausedAtKey === null) return 0;
    const idx = filtered.findIndex((r) => r.key === pausedAtKey);
    return idx === -1 ? 0 : filtered.length - 1 - idx;
  }, [autoScroll, pausedAtKey, filtered]);

  // ── Virtualizer ──────────────────────────────────────────────────────────

  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 16,
    // Stable keys: rows shift as the buffer trims and requests update in place.
    getItemKey: (index) => filtered[index].key,
  });

  const lastKeyRef = useRef<string | null>(null);
  lastKeyRef.current = lastKey;
  const setFollow = useCallback((on: boolean) => {
    setAutoScroll(on);
    setPausedAtKey((prev) => (on ? null : (prev ?? lastKeyRef.current)));
  }, []);

  useEffect(() => {
    if (!autoScroll || filtered.length === 0) return;
    isAutoScrolling.current = true;
    virtualizer.scrollToIndex(filtered.length - 1, { align: "end" });
    const t = setTimeout(() => {
      isAutoScrolling.current = false;
    }, 50);
    return () => clearTimeout(t);
  }, [filtered.length, lastKey, lastVersion, autoScroll, virtualizer]);

  const handleScroll = useCallback(() => {
    if (isAutoScrolling.current) return;
    const el = scrollContainerRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    setFollow(atBottom);
  }, [setFollow]);

  // ── Actions ──────────────────────────────────────────────────────────────

  const changeView = (v: View) => {
    setView(v);
    setLevels(loadLevels(v));
    setExpanded(new Set());
    setFollow(true);
  };

  const toggleLevel = (level: LogLevel) => {
    setLevels((prev) => {
      const next = new Set(prev);
      if (next.has(level)) next.delete(level);
      else next.add(level);
      saveLevels(view, next);
      return next;
    });
  };

  const resetFilters = () => {
    setSearch("");
    const d = new Set(DEFAULT_LEVELS[view]);
    saveLevels(view, d);
    setLevels(d);
  };

  // Adds a token to the search, replacing a previous token on the same field.
  const addFilter = useCallback(
    (token: string) => {
      setSearch((prev) => {
        const field = /^([a-z]+):/i.exec(token)?.[1]?.toLowerCase();
        const kept = prev
          .split(/\s+(?=(?:[^"]*"[^"]*")*[^"]*$)/)
          .filter((t) => t && t !== token && (!field || !t.toLowerCase().startsWith(field + ":")) && !(token.startsWith("#") && /^#\d+$/.test(t)));
        return [...kept, token].join(" ");
      });
      setFollow(false);
    },
    [setFollow],
  );

  const openRequest = useCallback(
    (req: string) => {
      setView("requests");
      setLevels(loadLevels("requests"));
      setSearch(shortReq(req));
      setExpanded(new Set([`r:${req}`]));
      setFollow(false);
    },
    [setFollow],
  );

  const handleClear = async () => {
    const ok = await confirm({
      title: "Clear the console log?",
      description: "Empties the server's log buffer for everyone viewing this page. Download it first if you need a copy.",
      confirmLabel: "Clear log",
      tone: "danger",
    });
    if (!ok) return;
    try {
      const res = await fetch("/api/console", { method: "DELETE" });
      if (!res.ok) throw new Error(`Server responded ${res.status}`);
    } catch (e) {
      toast.error("Couldn't clear the log", e instanceof Error ? e.message : undefined);
    }
  };

  const copyText = useCallback(
    async (text: string, done: string) => {
      try {
        await navigator.clipboard.writeText(text);
        toast.success(done);
        return true;
      } catch {
        toast.error("Couldn't copy to the clipboard");
        return false;
      }
    },
    [toast],
  );

  const handleCopy = async () => {
    if (await copyText(formatRows(filtered, view), `Copied ${filtered.length.toLocaleString()} ${view === "requests" ? "row" : "line"}${filtered.length === 1 ? "" : "s"}`)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  };

  const handleCopyRow = useCallback(
    (row: Row) => {
      const text = view === "requests" && row.isRequest ? row.lines.map(formatLine).join("\n") : formatLine(row.head);
      void copyText(text, view === "requests" && row.isRequest ? "Request copied" : "Line copied");
    },
    [view, copyText],
  );

  const handleDownload = () => {
    const blob = new Blob([formatRows(filtered, view) + "\n"], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = logFileName();
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  const toggleExpand = useCallback((key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const scrollToBottom = () => {
    setFollow(true);
    if (filtered.length > 0) virtualizer.scrollToIndex(filtered.length - 1, { align: "end" });
  };

  // ── Status & announcements ───────────────────────────────────────────────

  const status =
    state === "connecting"
      ? { label: "Connecting", dot: "bg-fg-faint", live: false, say: "" }
      : state === "signedout"
        ? { label: "Signed out", dot: "bg-bad", live: false, say: "Dashboard session expired; sign in again to resume the log" }
        : state === "reconnecting"
        ? { label: "Reconnecting", dot: "bg-warn", live: false, say: "Log stream disconnected, reconnecting" }
        : autoScroll
          ? { label: "Live", dot: "bg-ok", live: true, say: "Live, following new lines" }
          : { label: "Paused", dot: "bg-fg-faint", live: false, say: "Auto-scroll paused" };

  const prevSayRef = useRef(status.say);
  useEffect(() => {
    if (status.say === prevSayRef.current) return;
    prevSayRef.current = status.say;
    if (status.say) setAnnouncement(status.say);
  }, [status.say]);

  const countsRef = useRef({ shown: 0, total: 0 });
  useEffect(() => {
    countsRef.current = { shown: filtered.length, total: baseRows.length };
  });
  const filtersMounted = useRef(false);
  useEffect(() => {
    if (!filtersMounted.current) {
      filtersMounted.current = true;
      return;
    }
    const t = setTimeout(() => {
      const { shown, total } = countsRef.current;
      setAnnouncement(`${shown.toLocaleString()} of ${total.toLocaleString()} shown`);
    }, 600);
    return () => clearTimeout(t);
  }, [deferredSearch, levels, view]);

  const unit = view === "requests" ? "row" : "line";
  const loading = !loaded;

  return (
    <div className="flex flex-col [@media(min-width:48rem)_and_(min-height:40rem)]:h-[calc(100dvh-6.5rem)]">
      <PageHeader title="Console" description="Live gateway activity: one row per API request with its outcome, plus background warnings. Expand a row to see why." />
      <p className="sr-only" role="status" aria-live="polite">
        {announcement}
      </p>

      <Card className="flex flex-col [@media(min-width:48rem)_and_(min-height:40rem)]:min-h-0 [@media(min-width:48rem)_and_(min-height:40rem)]:flex-1">
        {/* ── Toolbar ─────────────────────────────────────────────────────── */}
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <div className="relative w-full sm:w-auto sm:min-w-[260px] sm:flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
            <input
              ref={searchRef}
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape" && search) {
                  e.preventDefault();
                  setSearch("");
                }
              }}
              placeholder="Search, or filter: is:failed  key:prod  status:5xx  ms>10s"
              aria-label="Search and filter the console"
              aria-keyshortcuts="/"
              aria-describedby={showHelp ? `${autoScrollId}-help` : undefined}
              spellCheck={false}
              className="h-9 w-full rounded-lg border border-input bg-surface pl-9 pr-16 font-mono text-[12.5px] text-fg placeholder:font-sans placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 sm:h-8 [&::-webkit-search-cancel-button]:hidden"
            />
            <div className="absolute right-1 top-1/2 flex -translate-y-1/2 items-center">
              {search ? (
                <button
                  type="button"
                  onClick={() => {
                    setSearch("");
                    searchRef.current?.focus();
                  }}
                  className="flex h-7 w-7 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                  aria-label="Clear search"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              ) : (
                <kbd className="pointer-events-none mr-1 rounded border border-line bg-subtle px-1.5 font-mono text-[11px] text-fg-faint" aria-hidden="true">
                  /
                </kbd>
              )}
              <button
                type="button"
                onClick={() => setShowHelp((v) => !v)}
                aria-expanded={showHelp}
                aria-controls={`${autoScrollId}-help`}
                aria-label="Search syntax"
                title="Search syntax"
                className={cn("flex h-7 w-7 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500", showHelp && "bg-hover text-fg")}
              >
                <HelpCircle className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </div>
          </div>

          <SegmentedControl<View>
            value={view}
            onChange={changeView}
            label="View"
            options={[
              { value: "requests", label: "Requests" },
              { value: "lines", label: "All lines" },
            ]}
          />

          <div className="flex w-full items-center gap-2 sm:ml-auto sm:w-auto">
            <StreamStatus status={status} className="hidden sm:inline-flex" />
            <span className="inline-flex h-8 items-center gap-2 px-1 text-[12.5px] text-fg-muted">
              <Toggle id={`${autoScrollId}-toggle`} aria-labelledby={`${autoScrollId}-label`} checked={autoScroll} onChange={(v) => (v ? scrollToBottom() : setFollow(false))} />
              <label id={`${autoScrollId}-label`} htmlFor={`${autoScrollId}-toggle`} className="cursor-pointer">
                Follow
              </label>
            </span>
            <span className="ml-auto mr-0.5 h-5 w-px bg-line sm:ml-0.5" aria-hidden="true" />
            <IconButton label={copied ? "Copied" : `Copy visible ${unit}s`} onClick={handleCopy} disabled={filtered.length === 0}>
              {copied ? <Check className="text-ok" strokeWidth={1.75} aria-hidden="true" /> : <Copy strokeWidth={1.75} aria-hidden="true" />}
            </IconButton>
            <IconButton label={`Download visible ${unit}s as .log`} onClick={handleDownload} disabled={filtered.length === 0}>
              <Download strokeWidth={1.75} aria-hidden="true" />
            </IconButton>
            <IconButton label="Clear log" onClick={handleClear} disabled={entries.length === 0} tone="danger">
              <Trash2 strokeWidth={1.75} aria-hidden="true" />
            </IconButton>
          </div>

          {showHelp && (
            <div id={`${autoScrollId}-help`} className="w-full rounded-lg border border-line bg-subtle px-3 py-2.5 text-[12.5px]">
              <p className="mb-2 text-fg-muted">
                Words match anywhere (message, error text, key, model, provider). Combine filters with spaces; every one must match. In the Requests view a request matches if any of its lines does.
              </p>
              <ul className="grid gap-x-4 gap-y-1 sm:grid-cols-2 lg:grid-cols-3">
                {SEARCH_EXAMPLES.map((ex) => (
                  <li key={ex.q} className="flex min-w-0 items-baseline gap-2">
                    <button
                      type="button"
                      onClick={() => {
                        setSearch(ex.q);
                        searchRef.current?.focus();
                      }}
                      className="shrink-0 rounded border border-line bg-surface px-1.5 font-mono text-[12px] text-fg hover:border-line-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                    >
                      {ex.q}
                    </button>
                    <span className="min-w-0 truncate text-fg-muted" title={ex.label}>
                      {ex.label}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          <div className="flex w-full flex-wrap items-center gap-1.5" role="group" aria-label="Filter by level">
            {LEVELS.map((level) => {
              const active = levels.has(level);
              return (
                <button
                  key={level}
                  type="button"
                  aria-pressed={active}
                  onClick={() => toggleLevel(level)}
                  className={cn(
                    "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 sm:h-7",
                    active ? LEVEL_PRESSED[level] : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                  )}
                >
                  <span className={cn("h-1.5 w-1.5 rounded-full", LEVEL_DOT[level])} aria-hidden="true" />
                  {LEVEL_LABEL[level]}
                  <span className={cn("tabular-nums font-normal", !active && "text-fg-faint")}>{levelCounts[level].toLocaleString()}</span>
                </button>
              );
            })}
            {filtersActive && (
              <button type="button" onClick={resetFilters} className="ml-1 h-8 rounded-md px-1.5 text-[12.5px] text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 sm:h-7">
                Reset filters
              </button>
            )}
          </div>
        </div>

        {state === "signedout" && (
          <div className="flex flex-wrap items-center gap-2 border-b border-bad/25 bg-bad/10 px-3 py-2 text-[12.5px] text-fg" role="alert">
            Your dashboard session expired, so the live log stopped. Lines below are what was received before that.
            <Button type="button" variant="secondary" className="ml-auto" onClick={() => window.location.reload()}>
              Sign in again
            </Button>
          </div>
        )}

        {/* ── Column header ───────────────────────────────────────────────── */}
        <div className="flex shrink-0 gap-3 border-b border-line bg-subtle px-3 py-1.5 text-[12px] font-medium text-fg-faint" aria-hidden="true">
          <span className="w-6 shrink-0" />
          <span className="w-[88px] shrink-0">{view === "requests" ? "Started" : "Time"}</span>
          <span className="w-12 shrink-0">{view === "requests" ? "Status" : "Level"}</span>
          <span className="hidden min-w-0 flex-1 sm:block">{view === "requests" ? "What happened" : "Message"}</span>
          <span className="hidden shrink-0 md:block">{view === "requests" ? "Key · Duration" : "Key"}</span>
        </div>

        {/* ── Log output ──────────────────────────────────────────────────── */}
        <div className="relative h-[60dvh] min-h-[280px] [@media(min-width:48rem)_and_(min-height:40rem)]:h-auto [@media(min-width:48rem)_and_(min-height:40rem)]:min-h-0 [@media(min-width:48rem)_and_(min-height:40rem)]:flex-1">
          <div
            ref={scrollContainerRef}
            onScroll={handleScroll}
            className="h-full overflow-y-auto bg-surface font-mono text-[12px] leading-[18px] focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
            role="log"
            aria-live="off"
            aria-label="Console output"
            aria-busy={loading}
            tabIndex={0}
          >
            {loading ? (
              <div className="space-y-2.5 px-3 py-3" aria-hidden="true">
                {Array.from({ length: 14 }).map((_, i) => (
                  <div key={i} className="flex items-center gap-3">
                    <span className="w-6" />
                    <Skeleton className="h-3 w-[72px]" />
                    <Skeleton className="h-3 w-9" />
                    <Skeleton className={cn("h-3", ["w-2/3", "w-1/2", "w-3/4", "w-2/5"][i % 4])} />
                  </div>
                ))}
              </div>
            ) : entries.length === 0 ? (
              <BodyMessage
                title="Nothing logged yet"
                hint={state === "live" ? "Rows appear as requests pass through KeiRouter." : "Waiting for the log stream to reconnect."}
                action={
                  state !== "live" ? (
                    <Button type="button" variant="secondary" onClick={reconnect}>
                      <RefreshCw className="h-4 w-4" aria-hidden="true" /> Reconnect now
                    </Button>
                  ) : undefined
                }
              />
            ) : filtered.length === 0 ? (
              <BodyMessage
                title="Nothing matches"
                hint={search ? `No ${unit} matches “${search}” at the selected levels.` : "Turn a level back on to see its rows."}
                action={
                  filtersActive ? (
                    <Button type="button" variant="primary" onClick={resetFilters}>
                      Reset filters
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <div style={{ height: `${virtualizer.getTotalSize()}px`, width: "100%", position: "relative" }}>
                {virtualizer.getVirtualItems().map((virtualRow) => {
                  const row = filtered[virtualRow.index];
                  return (
                    <div
                      key={row.key}
                      data-index={virtualRow.index}
                      ref={virtualizer.measureElement}
                      style={{ position: "absolute", top: 0, left: 0, width: "100%", transform: `translateY(${virtualRow.start}px)` }}
                    >
                      <LogRow
                        row={row}
                        view={view}
                        expanded={expanded.has(row.key)}
                        terms={terms}
                        onToggle={toggleExpand}
                        onFilter={addFilter}
                        onCopy={handleCopyRow}
                        onOpenRequest={openRequest}
                      />
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          {!autoScroll && filtered.length > 0 && (
            <div className="pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center">
              <button
                type="button"
                onClick={scrollToBottom}
                className="pointer-events-auto inline-flex h-8 items-center gap-1.5 rounded-full border border-transparent bg-action px-3 text-[12.5px] font-medium text-action-fg shadow-[var(--shadow-pop)] transition-colors hover:bg-action-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
              >
                <ArrowDown className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />
                Jump to latest
                {newSincePause > 0 && <span className="font-normal tabular-nums">· {newSincePause.toLocaleString()} new</span>}
              </button>
            </div>
          )}
        </div>

        {/* ── Footer ──────────────────────────────────────────────────────── */}
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-x-3 gap-y-1 border-t border-line bg-subtle px-3 py-1.5 text-[12px] tabular-nums text-fg-muted">
          <span className="flex flex-wrap items-center gap-x-3">
            <span>
              {filtersActive ? `${filtered.length.toLocaleString()} of ${baseRows.length.toLocaleString()} ${unit}s` : `${baseRows.length.toLocaleString()} ${unit}${baseRows.length === 1 ? "" : "s"}`}
            </span>
            <span aria-hidden="true" className="text-fg-faint">
              ·
            </span>
            <span>{grouped.stats.requests.toLocaleString()} requests</span>
            {grouped.stats.failed > 0 && (
              <button type="button" onClick={() => addFilter("is:failed")} className="rounded text-bad hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
                {grouped.stats.failed.toLocaleString()} failed
              </button>
            )}
            {grouped.stats.inflight > 0 && (
              <button type="button" onClick={() => addFilter("is:inflight")} className="rounded text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
                {grouped.stats.inflight.toLocaleString()} in flight
              </button>
            )}
          </span>
          <span className="flex items-center gap-2">
            <span className="hidden text-fg-faint lg:inline">Keeps the latest {MAX_LINES.toLocaleString()} lines</span>
            <StreamStatus status={status} className="h-auto sm:hidden" />
          </span>
        </div>
      </Card>
    </div>
  );
}
