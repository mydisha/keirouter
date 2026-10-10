import { useEffect, useId, useRef, useState, useMemo, useCallback, memo, type ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Trash2, Search, X, Copy, Check, ChevronRight, ArrowDown, Download } from "lucide-react";
import { PageHeader } from "../components/Layout";
import { Card, Button, IconTile, Skeleton, Toggle } from "../components/ui";
import { ICONS } from "../lib/icons";
import { useConfirm } from "../components/ui/confirm-dialog";
import { useToast } from "../components/Toast";
import { cn } from "@/lib/utils";

// ── Types ────────────────────────────────────────────────────────────────────

import type { ConsoleLogEntry } from "../lib/api";

type LogLevel = "DEBUG" | "INFO" | "WARN" | "ERROR" | "LOG";

// ── Constants ────────────────────────────────────────────────────────────────

// Only the level marker carries colour; the rest of the line stays neutral.
const LEVEL_TEXT: Record<LogLevel, string> = {
  DEBUG: "text-fg-faint",
  INFO: "text-link",
  WARN: "text-warn",
  ERROR: "text-bad",
  LOG: "text-fg-faint",
};

// Dot shown inside the level filter chips.
const LEVEL_DOT: Record<LogLevel, string> = {
  DEBUG: "bg-fg-faint",
  INFO: "bg-accent-500",
  WARN: "bg-warn",
  ERROR: "bg-bad",
  LOG: "bg-fg-faint",
};

// Pressed level filter: a soft tint in the level's own colour (info = accent).
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

const LEVELS: LogLevel[] = ["DEBUG", "INFO", "WARN", "ERROR"];

const MAX_LINES = 500;

const ROW_HEIGHT = 24; // approximate px per collapsed row

// Column geometry shared by the header row, log rows and the expanded block so
// everything lines up on the same grid.
const COL_CHEVRON = "w-6";
const COL_TIME = "w-[88px]";
const COL_LEVEL = "w-11";
// px-3 (12) + chevron (24) + gap (12) + time (88) + gap (12) + level (44) + gap (12)
const DETAIL_INDENT = "pl-12 sm:pl-[204px]";

// Normalize an arbitrary server level string into a known LogLevel.
function normalizeLevel(level: string): LogLevel {
  const up = (level || "").toUpperCase();
  if (up === "DEBUG" || up === "INFO" || up === "WARN" || up === "ERROR") {
    return up;
  }
  return "LOG";
}

// ── Formatting helpers ───────────────────────────────────────────────────────

function formatEntry(l: ConsoleLogEntry): string {
  const head = `${l.time} ${normalizeLevel(l.level)} ${l.msg}`;
  if (l.detail && l.detail.trim()) {
    const indented = l.detail
      .split("\n")
      .map((line) => "    " + line)
      .join("\n");
    return `${head}\n${indented}`;
  }
  return head;
}

function formatEntries(list: ConsoleLogEntry[]): string {
  return list.map(formatEntry).join("\n");
}

function logFileName(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `keirouter-console-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}.log`;
}

// key=value tokens inside a message, e.g. "status=429 retry=2".
const ATTR_SPLIT = /(\b\w+=\S+)/g;
const ATTR_TOKEN = /^\w+=\S+$/;

function extractAttrs(message: string): [string, string][] {
  const out: [string, string][] = [];
  for (const m of message.match(ATTR_SPLIT) ?? []) {
    const eq = m.indexOf("=");
    out.push([m.slice(0, eq), m.slice(eq + 1)]);
  }
  return out;
}

type ParsedDetail = { fields: [string, string][]; body: string; json: boolean };

// The backend writes detail either as aligned "Key:   value" lines followed by
// free text (error messages, fallback chains) or as raw JSON. Pull the leading
// key/value lines into structured fields and keep the rest verbatim.
const FIELD_LINE = /^([A-Za-z][\w .\-/()]{0,30}?):\s+(\S.*)$/;

function parseDetail(detail: string): ParsedDetail {
  const trimmed = detail.trim();
  if (!trimmed) return { fields: [], body: "", json: false };
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      return { fields: [], body: JSON.stringify(JSON.parse(trimmed), null, 2), json: true };
    } catch {
      /* not JSON — fall through */
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
  const body = lines.slice(i).join("\n").replace(/^\s*\n/, "");
  return { fields, body, json: false };
}

// ── Search highlighting ──────────────────────────────────────────────────────

function Highlight({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text}</>;
  const lower = text.toLowerCase();
  const q = query.toLowerCase();
  const nodes: ReactNode[] = [];
  let from = 0;
  let idx = lower.indexOf(q, from);
  if (idx === -1) return <>{text}</>;
  while (idx !== -1) {
    if (idx > from) nodes.push(text.slice(from, idx));
    nodes.push(
      <mark key={idx} className="rounded-[2px] bg-accent-500/20 text-fg dark:bg-accent-400/25">
        {text.slice(idx, idx + q.length)}
      </mark>,
    );
    from = idx + q.length;
    idx = lower.indexOf(q, from);
  }
  if (from < text.length) nodes.push(text.slice(from));
  return <>{nodes}</>;
}

// Renders a message with key=value pairs de-emphasised on the key side and
// search matches marked.
const HighlightMessage = memo(function HighlightMessage({ message, query }: { message: string; query: string }) {
  const parts = message.split(ATTR_SPLIT);
  return (
    <>
      {parts.map((part, i) => {
        if (part.includes("=") && ATTR_TOKEN.test(part)) {
          const eqIdx = part.indexOf("=");
          return (
            <span key={i}>
              <span className="text-fg-muted">
                <Highlight text={part.slice(0, eqIdx)} query={query} />
              </span>
              <span className="text-fg-faint">=</span>
              <span className="font-medium text-fg">
                <Highlight text={part.slice(eqIdx + 1)} query={query} />
              </span>
            </span>
          );
        }
        return <Highlight key={i} text={part} query={query} />;
      })}
    </>
  );
});

// ── Memoized log row ─────────────────────────────────────────────────────────

const LogRow = memo(function LogRow({
  entry,
  expanded,
  query,
  onToggle,
  onCopyLine,
}: {
  entry: ConsoleLogEntry;
  expanded: boolean;
  query: string;
  onToggle: (seq: number) => void;
  onCopyLine: (entry: ConsoleLogEntry) => void;
}) {
  const level = normalizeLevel(entry.level);
  const hasDetail = !!entry.detail && entry.detail.trim().length > 0;
  const attrs = useMemo(() => extractAttrs(entry.msg), [entry.msg]);
  const expandable = hasDetail || attrs.length > 0;
  const parsed = useMemo(
    () => (expanded && hasDetail ? parseDetail(entry.detail ?? "") : null),
    [expanded, hasDetail, entry.detail],
  );

  // Clicking anywhere on the line toggles it for pointer users; the chevron is
  // the real button keyboard and screen reader users reach.
  const toggle = () => {
    // Don't collapse/expand when the user is selecting text to copy.
    const sel = window.getSelection();
    if (sel && sel.type === "Range" && sel.toString().length > 0) return;
    onToggle(entry.seq);
  };
  const detailId = `log-detail-${entry.seq}`;
  const summary = entry.msg.length > 80 ? `${entry.msg.slice(0, 80)}…` : entry.msg;

  const fields: [string, string][] = [...attrs, ...(parsed?.fields ?? [])];

  return (
    <div className={cn("border-b border-line/60", expanded && "bg-subtle")}>
      {/* Summary line */}
      <div
        onClick={expandable ? toggle : undefined}
        className={cn("flex items-start gap-3 px-3 py-[3px] transition-colors hover:bg-hover", expandable && "cursor-pointer")}
        style={{ minHeight: ROW_HEIGHT }}
      >
        {expandable ? (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onToggle(entry.seq);
            }}
            aria-expanded={expanded}
            aria-controls={expanded ? detailId : undefined}
            aria-label={`Details: ${entry.time} ${level !== "LOG" ? LEVEL_LABEL[level] : ""} ${summary}`}
            className={cn(
              "-my-[3px] flex h-6 shrink-0 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500",
              COL_CHEVRON,
            )}
          >
            <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-90")} strokeWidth={2} aria-hidden="true" />
          </button>
        ) : (
          <span className={cn("shrink-0", COL_CHEVRON)} aria-hidden="true" />
        )}
        <span className={cn("shrink-0 whitespace-nowrap tabular-nums text-fg-faint", COL_TIME)}>
          {entry.time || " "}
        </span>
        <span className={cn("shrink-0 font-medium", COL_LEVEL, LEVEL_TEXT[level])}>
          {level !== "LOG" ? level : ""}
        </span>
        <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-fg [overflow-wrap:anywhere]">
          <HighlightMessage message={entry.msg} query={query} />
        </span>
      </div>

      {/* Expanded structured block */}
      {expandable && expanded && (
        <div id={detailId} className={cn("pb-2.5 pr-4 pt-1", DETAIL_INDENT)}>
          <div className="space-y-2 border-l border-line-strong pl-3">
            {fields.length > 0 && (
              <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-0.5">
                {fields.map(([k, v], i) => (
                  <div key={`${k}-${i}`} className="contents">
                    <dt className="text-fg-faint">
                      <Highlight text={k} query={query} />
                    </dt>
                    <dd className="break-words text-fg [overflow-wrap:anywhere]">
                      <Highlight text={v} query={query} />
                    </dd>
                  </div>
                ))}
              </dl>
            )}
            {parsed && parsed.body && (
              <pre
                className={cn(
                  "whitespace-pre-wrap break-words font-mono text-[12px] leading-[1.55] [overflow-wrap:anywhere]",
                  parsed.json ? "text-fg" : "text-fg-muted",
                )}
              >
                <Highlight text={parsed.body} query={query} />
              </pre>
            )}
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onCopyLine(entry);
              }}
              className="inline-flex h-6 items-center gap-1 rounded-md px-1.5 font-sans text-[12px] text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <Copy className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
              Copy line
            </button>
          </div>
        </div>
      )}
    </div>
  );
});

// ── Small local pieces ───────────────────────────────────────────────────────

function IconButton({
  label,
  onClick,
  disabled,
  children,
  tone = "neutral",
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
  tone?: "neutral" | "danger";
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      title={label}
      className={cn(
        "inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-50 [&_svg]:h-4 [&_svg]:w-4",
        tone === "danger" ? "hover:text-bad" : "hover:text-fg",
      )}
    >
      {children}
    </button>
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

// ── Component ────────────────────────────────────────────────────────────────

export function ConsoleLogPage() {
  const confirm = useConfirm();
  const toast = useToast();
  const [entries, setEntries] = useState<ConsoleLogEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [activeLevels, setActiveLevels] = useState<Set<LogLevel>>(new Set(LEVELS));
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [autoScroll, setAutoScroll] = useState(true);
  const [pausedAtSeq, setPausedAtSeq] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const autoScrollId = useId();
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const isAutoScrolling = useRef(false);
  const lastSeqRef = useRef(0);

  // ── SSE stream ───────────────────────────────────────────────────────────

  useEffect(() => {
    const es = new EventSource("/api/console/stream");

    es.onopen = () => setConnected(true);

    es.onmessage = (e) => {
      const msg = JSON.parse(e.data);
      if (msg.type === "init") {
        setEntries(msg.logs || []);
        setLoading(false);
      } else if (msg.type === "line") {
        setEntries((prev) => {
          const next = [...prev, msg.log as ConsoleLogEntry];
          return next.length > MAX_LINES ? next.slice(-MAX_LINES) : next;
        });
      } else if (msg.type === "clear") {
        setEntries([]);
        setExpanded(new Set());
      }
    };

    // EventSource reconnects on its own; the server re-sends "init" history
    // on every (re)connect, which replaces the buffer above.
    es.onerror = () => setConnected(false);

    return () => es.close();
  }, []);

  useEffect(() => {
    lastSeqRef.current = entries.length > 0 ? entries[entries.length - 1].seq : 0;
  }, [entries]);

  // "/" focuses search, like the other list pages.
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

  // ── Filtering ────────────────────────────────────────────────────────────

  const filtered = useMemo(() => {
    let list = entries;
    if (activeLevels.size < LEVELS.length) {
      list = list.filter((l) => {
        const lvl = normalizeLevel(l.level);
        return activeLevels.has(lvl) || lvl === "LOG";
      });
    }
    if (search) {
      const q = search.toLowerCase();
      list = list.filter(
        (l) => l.msg.toLowerCase().includes(q) || (l.detail || "").toLowerCase().includes(q),
      );
    }
    return list;
  }, [entries, activeLevels, search]);

  const filtersActive = search.length > 0 || activeLevels.size < LEVELS.length;
  const lastFilteredSeq = filtered.length > 0 ? filtered[filtered.length - 1].seq : null;

  // Lines that arrived (and pass the filters) since the user stopped following.
  const newSincePause = useMemo(() => {
    if (autoScroll || pausedAtSeq === null) return 0;
    let n = 0;
    for (let i = filtered.length - 1; i >= 0 && filtered[i].seq > pausedAtSeq; i--) n++;
    return n;
  }, [autoScroll, pausedAtSeq, filtered]);

  // ── Stats ────────────────────────────────────────────────────────────────

  const stats = useMemo(() => {
    const counts: Record<LogLevel, number> = {
      DEBUG: 0,
      INFO: 0,
      WARN: 0,
      ERROR: 0,
      LOG: 0,
    };
    for (const l of entries) counts[normalizeLevel(l.level)]++;
    return counts;
  }, [entries]);

  // ── Virtualizer ──────────────────────────────────────────────────────────

  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 20,
    // Key measurements by the stable log seq, not the array index. The log
    // list mutates constantly (SSE appends + slice(-MAX_LINES) shifts every
    // index), so an index-keyed cache would attach an expanded row's tall
    // height to whatever entry later lands on that index, leaving neighboring
    // rows with stale offsets and causing overlapping "glitch" rows.
    getItemKey: (index) => filtered[index].seq,
  });

  // ── Auto-scroll ──────────────────────────────────────────────────────────

  const setFollow = useCallback((on: boolean) => {
    setAutoScroll(on);
    setPausedAtSeq((prev) => (on ? null : (prev ?? lastSeqRef.current)));
  }, []);

  // When new lines land and autoScroll is on, scroll to the end. Keyed on the
  // last seq as well as the length: once the buffer is full (MAX_LINES) the
  // length stops changing but new lines still arrive.
  useEffect(() => {
    if (!autoScroll || filtered.length === 0) return;
    isAutoScrolling.current = true;
    virtualizer.scrollToIndex(filtered.length - 1, { align: "end" });
    // Small delay to let the scroll settle before re-enabling manual detection
    const t = setTimeout(() => {
      isAutoScrolling.current = false;
    }, 50);
    return () => clearTimeout(t);
  }, [filtered.length, lastFilteredSeq, autoScroll, virtualizer]);

  // Detect manual scroll-up to pause auto-scroll
  const handleScroll = useCallback(() => {
    if (isAutoScrolling.current) return;
    const el = scrollContainerRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    setFollow(atBottom);
  }, [setFollow]);

  // ── Actions ──────────────────────────────────────────────────────────────

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

  const handleCopy = async () => {
    const text = formatEntries(filtered);
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
      toast.success(`Copied ${filtered.length.toLocaleString()} line${filtered.length === 1 ? "" : "s"}`);
    } catch {
      toast.error("Couldn't copy to the clipboard");
    }
  };

  const handleDownload = () => {
    const blob = new Blob([formatEntries(filtered) + "\n"], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = logFileName();
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  const handleCopyLine = useCallback(
    async (entry: ConsoleLogEntry) => {
      try {
        await navigator.clipboard.writeText(formatEntry(entry));
        toast.success("Line copied");
      } catch {
        toast.error("Couldn't copy to the clipboard");
      }
    },
    [toast],
  );

  const toggleExpand = useCallback((seq: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) {
        next.delete(seq);
      } else {
        next.add(seq);
      }
      return next;
    });
  }, []);

  const toggleLevel = (level: LogLevel) => {
    setActiveLevels((prev) => {
      const next = new Set(prev);
      if (next.has(level)) {
        next.delete(level);
      } else {
        next.add(level);
      }
      return next;
    });
  };

  const resetFilters = () => {
    setSearch("");
    setActiveLevels(new Set(LEVELS));
  };

  const scrollToBottom = () => {
    setFollow(true);
    if (filtered.length > 0) {
      virtualizer.scrollToIndex(filtered.length - 1, { align: "end" });
    }
  };

  // ── Render ───────────────────────────────────────────────────────────────

  const status = !connected
    ? loading
      ? { label: "Connecting", dot: "bg-fg-faint", live: false, say: "" }
      : { label: "Reconnecting", dot: "bg-warn", live: false, say: "Log stream disconnected, reconnecting" }
    : autoScroll
      ? { label: "Live", dot: "bg-ok", live: true, say: "Live, following new lines" }
      : { label: "Paused", dot: "bg-fg-faint", live: false, say: "Auto-scroll paused" };

  // Screen-reader announcements. The log region itself is aria-live="off" so
  // streamed lines never flood a screen reader; only state changes and
  // (debounced) filter results are spoken through the polite status below.
  const prevSayRef = useRef(status.say);
  useEffect(() => {
    if (status.say === prevSayRef.current) return;
    prevSayRef.current = status.say;
    if (status.say) setAnnouncement(status.say);
  }, [status.say]);

  const countsRef = useRef({ shown: 0, total: 0 });
  useEffect(() => {
    countsRef.current = { shown: filtered.length, total: entries.length };
  });
  const filtersMounted = useRef(false);
  useEffect(() => {
    if (!filtersMounted.current) {
      filtersMounted.current = true;
      return;
    }
    const t = setTimeout(() => {
      const { shown, total } = countsRef.current;
      setAnnouncement(`${shown.toLocaleString()} of ${total.toLocaleString()} lines shown`);
    }, 600);
    return () => clearTimeout(t);
  }, [search, activeLevels]);

  return (
    // Fill the viewport below the top bar (h-14) minus the Layout's py-6, so
    // the log list — not the page — is what scrolls.
    <div className="flex h-[calc(100dvh-6.5rem)] min-h-[560px] flex-col">
      <PageHeader
        title="Console log"
        description={`Live gateway output; this tab keeps the latest ${MAX_LINES} lines.`}
      />
      <p className="sr-only" role="status" aria-live="polite">
        {announcement}
      </p>

      <Card className="flex min-h-0 flex-1 flex-col">
        {/* ── Toolbar ─────────────────────────────────────────────────────── */}
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <div className="relative min-w-[220px] flex-1">
            <Search
              className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint"
              strokeWidth={1.75}
              aria-hidden="true"
            />
            <input
              ref={searchRef}
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search messages and details"
              aria-label="Search logs"
              aria-keyshortcuts="/"
              className="h-8 w-full rounded-lg border border-input bg-surface pl-9 pr-10 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [&::-webkit-search-cancel-button]:hidden"
            />
            {search ? (
              <button
                type="button"
                onClick={() => {
                  setSearch("");
                  searchRef.current?.focus();
                }}
                className="absolute right-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                aria-label="Clear search"
              >
                <X className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            ) : (
              <kbd className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 rounded border border-line bg-subtle px-1.5 font-mono text-[11px] text-fg-faint" aria-hidden="true">
                /
              </kbd>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Filter by level">
            {LEVELS.map((level) => {
              const active = activeLevels.has(level);
              const count = stats[level];
              return (
                <button
                  key={level}
                  type="button"
                  aria-pressed={active}
                  onClick={() => toggleLevel(level)}
                  className={cn(
                    "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                    active
                      ? LEVEL_PRESSED[level]
                      : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                  )}
                >
                  <span className={cn("h-1.5 w-1.5 rounded-full", LEVEL_DOT[level])} aria-hidden="true" />
                  {LEVEL_LABEL[level]}
                  <span className={cn("tabular-nums font-normal", !active && "text-fg-faint")}>{count.toLocaleString()}</span>
                </button>
              );
            })}
          </div>

          <div className="ml-auto flex items-center gap-2">
            <span className="inline-flex h-8 items-center gap-1.5 px-1 text-[12px] font-medium text-fg-muted">
              <span className={cn("h-1.5 w-1.5 rounded-full", status.dot, status.live && "live-dot")} aria-hidden="true" />
              {status.label}
            </span>
            <span className="inline-flex h-8 items-center gap-2 px-1 text-[12.5px] text-fg-muted">
              <Toggle
                id={`${autoScrollId}-toggle`}
                aria-labelledby={`${autoScrollId}-label`}
                checked={autoScroll}
                onChange={(v) => (v ? scrollToBottom() : setFollow(false))}
              />
              <label id={`${autoScrollId}-label`} htmlFor={`${autoScrollId}-toggle`} className="cursor-pointer">
                Auto-scroll
              </label>
            </span>
            <span className="mx-0.5 h-5 w-px bg-line" aria-hidden="true" />
            <IconButton label={copied ? "Copied" : "Copy visible lines"} onClick={handleCopy} disabled={filtered.length === 0}>
              {copied ? <Check className="text-ok" strokeWidth={1.75} aria-hidden="true" /> : <Copy strokeWidth={1.75} aria-hidden="true" />}
            </IconButton>
            <IconButton label="Download visible lines as .log" onClick={handleDownload} disabled={filtered.length === 0}>
              <Download strokeWidth={1.75} aria-hidden="true" />
            </IconButton>
            <IconButton label="Clear log" onClick={handleClear} disabled={entries.length === 0} tone="danger">
              <Trash2 strokeWidth={1.75} aria-hidden="true" />
            </IconButton>
          </div>
        </div>

        {/* ── Column header ───────────────────────────────────────────────── */}
        <div className="flex shrink-0 gap-3 border-b border-line bg-subtle px-3 py-1.5 text-[12px] font-medium text-fg-faint" aria-hidden="true">
          <span className={cn("shrink-0", COL_CHEVRON)} />
          <span className={cn("shrink-0", COL_TIME)}>Time</span>
          <span className={cn("shrink-0", COL_LEVEL)}>Level</span>
          <span className="min-w-0 flex-1">Message</span>
        </div>

        {/* ── Log output ──────────────────────────────────────────────────── */}
        <div className="relative min-h-0 flex-1">
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
                    <span className="w-3" />
                    <Skeleton className="h-3 w-[72px]" />
                    <Skeleton className="h-3 w-9" />
                    <Skeleton className={cn("h-3", ["w-2/3", "w-1/2", "w-3/4", "w-2/5"][i % 4])} />
                  </div>
                ))}
              </div>
            ) : entries.length === 0 ? (
              <BodyMessage
                title="No log lines yet"
                hint={connected ? "Lines appear as requests pass through KeiRouter." : "Waiting for the log stream to reconnect."}
              />
            ) : filtered.length === 0 ? (
              <BodyMessage
                title="No lines match"
                hint={
                  search
                    ? `Nothing matches “${search}” at the selected levels.`
                    : "Turn a level back on to see its lines."
                }
                action={
                  filtersActive ? (
                    <Button type="button" variant="primary" onClick={resetFilters}>
                      Reset filters
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <div
                style={{
                  height: `${virtualizer.getTotalSize()}px`,
                  width: "100%",
                  position: "relative",
                }}
              >
                {virtualizer.getVirtualItems().map((virtualRow) => {
                  const entry = filtered[virtualRow.index];
                  return (
                    <div
                      key={entry.seq}
                      data-index={virtualRow.index}
                      ref={virtualizer.measureElement}
                      style={{
                        position: "absolute",
                        top: 0,
                        left: 0,
                        width: "100%",
                        transform: `translateY(${virtualRow.start}px)`,
                      }}
                    >
                      <LogRow
                        entry={entry}
                        expanded={expanded.has(entry.seq)}
                        query={search}
                        onToggle={toggleExpand}
                        onCopyLine={handleCopyLine}
                      />
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          {/* Jump to latest */}
          {!autoScroll && filtered.length > 0 && (
            <div className="pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center">
              <button
                type="button"
                onClick={scrollToBottom}
                className="pointer-events-auto inline-flex h-8 items-center gap-1.5 rounded-full border border-transparent bg-action px-3 text-[12.5px] font-medium text-action-fg shadow-[var(--shadow-pop)] transition-colors hover:bg-action-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
              >
                <ArrowDown className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />
                Jump to latest
                {newSincePause > 0 && (
                  <span className="font-normal tabular-nums">· {newSincePause.toLocaleString()} new</span>
                )}
              </button>
            </div>
          )}
        </div>

        {/* ── Footer ──────────────────────────────────────────────────────── */}
        <div className="flex shrink-0 items-center border-t border-line bg-subtle px-3 py-1.5 text-[12px] tabular-nums text-fg-muted">
          {filtersActive
            ? `${filtered.length.toLocaleString()} of ${entries.length.toLocaleString()} lines`
            : `${entries.length.toLocaleString()} line${entries.length === 1 ? "" : "s"}`}
        </div>
      </Card>
    </div>
  );
}
