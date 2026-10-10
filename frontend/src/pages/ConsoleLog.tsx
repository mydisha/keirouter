import { useEffect, useRef, useState, useMemo, useCallback, memo } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  ScrollText,
  Trash2,
  Search,
  X,
  Pause,
  Play,
  Copy,
  Check,
  ChevronDown,
  ChevronRight,
} from "lucide-react";
import { PageHeader } from "../components/Layout";
import { Card, Button, EmptyState } from "../components/ui";

// ── Types ────────────────────────────────────────────────────────────────────

import type { ConsoleLogEntry } from "../lib/api";

type LogLevel = "DEBUG" | "INFO" | "WARN" | "ERROR" | "LOG";

// ── Constants ────────────────────────────────────────────────────────────────

// Badge text — high contrast on tinted backgrounds
const LEVEL_TEXT: Record<LogLevel, string> = {
  DEBUG: "text-purple-700 dark:text-purple-300",
  INFO: "text-blue-700 dark:text-blue-300",
  WARN: "text-warn",
  ERROR: "text-bad",
  LOG: "text-ok",
};

// Badge background — visible but not loud
const LEVEL_BG: Record<LogLevel, string> = {
  DEBUG: "bg-purple-100 dark:bg-purple-500/20",
  INFO: "bg-blue-100 dark:bg-blue-500/20",
  WARN: "bg-amber-100 dark:bg-amber-500/20",
  ERROR: "bg-red-100 dark:bg-red-500/20",
  LOG: "bg-emerald-100 dark:bg-emerald-500/20",
};

// Left border accent for each row — quick visual scan of severity
const LEVEL_BORDER: Record<LogLevel, string> = {
  DEBUG: "border-l-purple-400 dark:border-l-purple-500",
  INFO: "border-l-blue-400 dark:border-l-blue-500",
  WARN: "border-l-amber-400 dark:border-l-amber-500",
  ERROR: "border-l-red-400 dark:border-l-red-500",
  LOG: "border-l-transparent",
};

const LEVELS: LogLevel[] = ["DEBUG", "INFO", "WARN", "ERROR"];

const MAX_LINES = 500;

const ROW_HEIGHT = 24; // approximate px per collapsed row

// Normalize an arbitrary server level string into a known LogLevel.
function normalizeLevel(level: string): LogLevel {
  const up = (level || "").toUpperCase();
  if (up === "DEBUG" || up === "INFO" || up === "WARN" || up === "ERROR") {
    return up;
  }
  return "LOG";
}

// ── Memoized log row ─────────────────────────────────────────────────────────

const LogRow = memo(function LogRow({
  entry,
  index,
  expanded,
  onToggle,
}: {
  entry: ConsoleLogEntry;
  index: number;
  expanded: boolean;
  onToggle: (seq: number) => void;
}) {
  const level = normalizeLevel(entry.level);
  const hasDetail = !!entry.detail && entry.detail.trim().length > 0;

  return (
    <div
      className={`border-b border-l-2 border-[var(--border)]/40 transition-colors ${LEVEL_BORDER[level]}`}
    >
      {/* Summary line */}
      <div
        className={`flex ${hasDetail ? "cursor-pointer" : ""} hover:bg-[var(--bg-subtle)]`}
        style={{ minHeight: ROW_HEIGHT }}
        onClick={hasDetail ? () => onToggle(entry.seq) : undefined}
      >
        {/* Line number */}
        <div className="w-[3.5rem] shrink-0 select-none px-3 py-[3px] text-right text-[11px] text-[var(--text-muted)]/50">
          {index + 1}
        </div>
        {/* Timestamp */}
        <div className="w-[6.5rem] shrink-0 px-2 py-[3px] whitespace-nowrap text-[var(--text-muted)]">
          {entry.time || "\u00A0"}
        </div>
        {/* Level badge */}
        <div className="w-[3.5rem] shrink-0 px-1 py-[3px]">
          {level !== "LOG" && (
            <span
              className={`inline-block rounded px-1.5 text-[11px] font-bold leading-normal ${LEVEL_TEXT[level]} ${LEVEL_BG[level]}`}
            >
              {level}
            </span>
          )}
        </div>
        {/* Message */}
        <div className="min-w-0 flex-1 px-2 py-[3px] break-words text-[var(--text)]">
          <HighlightMessage message={entry.msg} />
        </div>
        {/* Detail toggle */}
        <div className="w-[5rem] shrink-0 px-2 py-[3px] text-right">
          {hasDetail && (
            <span className="inline-flex items-center gap-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text)]">
              <ChevronRight
                className={`h-3 w-3 transition-transform ${expanded ? "rotate-90" : ""}`}
              />
              detail
            </span>
          )}
        </div>
      </div>

      {/* Expanded detail block */}
      {hasDetail && expanded && (
        <div className="border-t border-[var(--border)]/30 bg-[var(--bg-subtle)]/60 py-2 pr-4 pl-[13.5rem]">
          <pre className="whitespace-pre-wrap break-words text-[12px] leading-[1.5] text-[var(--text-muted)]">
            {entry.detail}
          </pre>
        </div>
      )}
    </div>
  );
});

// ── Message highlighting ─────────────────────────────────────────────────────

// Highlights key=value pairs and important tokens in the message
const HighlightMessage = memo(function HighlightMessage({
  message,
}: {
  message: string;
}) {
  // Split on key=value patterns and highlight them
  const parts = message.split(/(\b\w+=\S+)/g);
  return (
    <>
      {parts.map((part, i) => {
        if (part.includes("=") && /^\w+=\S+$/.test(part)) {
          const eqIdx = part.indexOf("=");
          const key = part.slice(0, eqIdx);
          const val = part.slice(eqIdx + 1);
          return (
            <span key={i}>
              <span className="text-[var(--text-muted)]">{key}</span>
              <span className="text-[var(--text-muted)]/60">=</span>
              <span className="font-medium text-[var(--text)]">{val}</span>
            </span>
          );
        }
        return <span key={i}>{part}</span>;
      })}
    </>
  );
});

// ── Component ────────────────────────────────────────────────────────────────

export function ConsoleLogPage() {
  const [entries, setEntries] = useState<ConsoleLogEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [activeLevels, setActiveLevels] = useState<Set<LogLevel>>(
    new Set(LEVELS),
  );
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [autoScroll, setAutoScroll] = useState(true);
  const [copied, setCopied] = useState(false);
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const isAutoScrolling = useRef(false);

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

    es.onerror = () => setConnected(false);

    return () => es.close();
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
        (l) =>
          l.msg.toLowerCase().includes(q) ||
          (l.detail || "").toLowerCase().includes(q),
      );
    }
    return list;
  }, [entries, activeLevels, search]);

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

  // When filtered list grows and autoScroll is on, scroll to the end
  useEffect(() => {
    if (!autoScroll || filtered.length === 0) return;
    isAutoScrolling.current = true;
    virtualizer.scrollToIndex(filtered.length - 1, { align: "end" });
    // Small delay to let the scroll settle before re-enabling manual detection
    const t = setTimeout(() => {
      isAutoScrolling.current = false;
    }, 50);
    return () => clearTimeout(t);
  }, [filtered.length, autoScroll, virtualizer]);

  // Detect manual scroll-up to pause auto-scroll
  const handleScroll = useCallback(() => {
    if (isAutoScrolling.current) return;
    const el = scrollContainerRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    setAutoScroll(atBottom);
  }, []);

  // ── Actions ──────────────────────────────────────────────────────────────

  const handleClear = async () => {
    try {
      await fetch("/api/console", { method: "DELETE" });
    } catch {
      /* ignore */
    }
  };

  const handleCopy = async () => {
    const text = filtered
      .map((l) => {
        const head = `${l.time} ${normalizeLevel(l.level)} ${l.msg}`;
        if (l.detail && l.detail.trim()) {
          const indented = l.detail
            .split("\n")
            .map((line) => "    " + line)
            .join("\n");
          return `${head}\n${indented}`;
        }
        return head;
      })
      .join("\n");
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  };

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

  const scrollToBottom = () => {
    setAutoScroll(true);
    if (filtered.length > 0) {
      virtualizer.scrollToIndex(filtered.length - 1, { align: "end" });
    }
  };

  // ── Render ───────────────────────────────────────────────────────────────

  return (
    <>
      <PageHeader
        title="Console Log"
        icon={ScrollText}
        description={
          connected
            ? `${entries.length} lines · live`
            : "Connecting…"
        }
        action={
          <div className="flex items-center gap-2">
            <span
              className={`h-2 w-2 rounded-full ${
                connected
                  ? "bg-green-500 dark:bg-green-400"
                  : "bg-[var(--text-muted)]"
              }`}
            />
            <Button variant="ghost" onClick={handleCopy} disabled={filtered.length === 0}>
              {copied ? (
                <Check className="h-4 w-4 text-accent-500" />
              ) : (
                <Copy className="h-4 w-4" />
              )}
              {copied ? "Copied" : "Copy"}
            </Button>
            <Button variant="ghost" onClick={handleClear} disabled={entries.length === 0}>
              <Trash2 className="h-4 w-4" />
              Clear
            </Button>
          </div>
        }
      />

      {/* ── Toolbar ──────────────────────────────────────────────────────── */}
      <Card className="mb-3">
        <div className="flex flex-wrap items-center gap-3 px-4 py-3">
          {/* Search */}
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search logs…"
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg)] py-1.5 pl-9 pr-8 text-sm placeholder:text-[var(--text-muted)] focus:border-accent-400 focus:outline-none focus:ring-1 focus:ring-accent-400/40"
            />
            {search && (
              <button
                onClick={() => setSearch("")}
                className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--text)]"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>

          {/* Level filters */}
          <div className="flex items-center gap-1.5">
            {LEVELS.map((level) => {
              const active = activeLevels.has(level);
              const count = stats[level];
              return (
                <button
                  key={level}
                  onClick={() => toggleLevel(level)}
                  className={`flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                    active
                      ? `${LEVEL_BG[level]} ${LEVEL_TEXT[level]} ring-1 ring-current/20`
                      : "text-[var(--text-muted)] opacity-50 hover:opacity-75"
                  }`}
                >
                  {level}
                  {count > 0 && (
                    <span className="ml-0.5 tabular-nums opacity-70">
                      {count}
                    </span>
                  )}
                </button>
              );
            })}
          </div>

          {/* Stats & scroll control */}
          <div className="flex items-center gap-2 text-xs text-[var(--text-muted)]">
            {search && (
              <span>
                {filtered.length}/{entries.length} matched
              </span>
            )}
            <button
              onClick={() => setAutoScroll(!autoScroll)}
              className={`flex items-center gap-1 rounded-md px-2 py-1 transition-colors ${
                autoScroll
                  ? "bg-accent-500/10 text-accent-500"
                  : "hover:bg-[var(--bg-subtle)]"
              }`}
              title={autoScroll ? "Pause auto-scroll" : "Resume auto-scroll"}
            >
              {autoScroll ? (
                <Pause className="h-3 w-3" />
              ) : (
                <Play className="h-3 w-3" />
              )}
              {autoScroll ? "Live" : "Paused"}
            </button>
          </div>
        </div>
      </Card>

      {/* ── Log output ───────────────────────────────────────────────────── */}
      <Card className="relative overflow-hidden">
        {loading ? (
          <div className="flex items-center justify-center py-12">
            <div className="h-5 w-5 animate-spin rounded-full border-2 border-[var(--border)] border-t-accent-500" />
          </div>
        ) : filtered.length === 0 && entries.length === 0 ? (
          <EmptyState
            title="No console logs yet"
            hint="Requests will appear here once traffic flows through KeiRouter."
          />
        ) : filtered.length === 0 ? (
          <EmptyState
            title="No matching logs"
            hint="Try adjusting your search or filters."
          />
        ) : (
          <div
            ref={scrollContainerRef}
            onScroll={handleScroll}
            className="overflow-y-auto bg-[var(--bg)] font-mono text-[13px] leading-[1.6]"
            style={{ height: "calc(100vh - 310px)" }}
          >
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
                      index={virtualRow.index}
                      expanded={expanded.has(entry.seq)}
                      onToggle={toggleExpand}
                    />
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {/* Scroll-to-bottom fab */}
        {!autoScroll && filtered.length > 0 && (
          <div className="absolute bottom-4 right-4 z-10">
            <button
              onClick={scrollToBottom}
              className="flex h-8 w-8 items-center justify-center rounded-full bg-accent-600 text-white shadow-lg transition-colors hover:bg-accent-700"
            >
              <ChevronDown className="h-4 w-4" />
            </button>
          </div>
        )}
      </Card>
    </>
  );
}