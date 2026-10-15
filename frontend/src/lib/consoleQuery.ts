import type { ConsoleLogEntry } from "./api";

// Console search language. Plain words match anywhere in a line (message,
// detail, hint and every field); `field:value` narrows to one field; a leading
// `-` excludes; "double quotes" keep spaces together.
//
//   key:prod  model:sonnet  provider:openai  client:cursor  kind:oauth
//   status:429  status:5xx  status:>=400  level:error  req:42  #42
//   ms>5s  tokens>10000  is:failed  is:slow  is:fallback  is:inflight

type TextField = "key" | "model" | "provider" | "target" | "kind" | "client" | "endpoint" | "msg";
type NumField = "status" | "ms" | "tokens";
type Op = ">" | ">=" | "<" | "<=" | "=";

export type Clause =
  | { type: "text"; value: string; neg: boolean }
  | { type: "field"; field: TextField; value: string; neg: boolean }
  | { type: "num"; field: NumField; op: Op; value: number; cls?: number; neg: boolean }
  | { type: "level"; value: string; neg: boolean }
  | { type: "req"; value: string; neg: boolean }
  | { type: "is"; flag: string; neg: boolean };

export interface ParsedQuery {
  clauses: Clause[];
  /** Positive literal values worth highlighting in matched lines. */
  terms: string[];
}

const TEXT_FIELDS: Record<string, TextField> = {
  key: "key",
  model: "model",
  provider: "provider",
  target: "target",
  served: "target",
  kind: "kind",
  source: "kind",
  client: "client",
  endpoint: "endpoint",
  path: "endpoint",
  msg: "msg",
  message: "msg",
};

const NUM_FIELDS: Record<string, NumField> = {
  status: "status",
  code: "status",
  ms: "ms",
  latency: "ms",
  duration: "ms",
  tokens: "tokens",
};

export const IS_FLAGS = ["failed", "ok", "error", "warn", "slow", "fallback", "stream", "cache", "inflight", "system", "disconnected"] as const;

/** Requests slower than this count as `is:slow`. */
export const SLOW_MS = 10_000;

function tokenize(input: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quoted = false;
  let had = false;
  for (const ch of input) {
    if (ch === '"') {
      quoted = !quoted;
      had = true;
      continue;
    }
    if (!quoted && /\s/.test(ch)) {
      if (had || cur) out.push(cur);
      cur = "";
      had = false;
      continue;
    }
    cur += ch;
  }
  if (had || cur) out.push(cur);
  return out.filter((t) => t.length > 0);
}

// "5s" -> 5000, "250ms" -> 250, "2m" -> 120000, "300" -> 300.
function parseDuration(v: string): number | null {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m)?$/i.exec(v.trim());
  if (!m) return null;
  const n = parseFloat(m[1]);
  const unit = (m[2] || "ms").toLowerCase();
  return unit === "m" ? n * 60_000 : unit === "s" ? n * 1000 : n;
}

function parseNumberish(field: NumField, raw: string): { op: Op; value: number; cls?: number } | null {
  let v = raw;
  let op: Op = "=";
  const opm = /^(>=|<=|>|<|=)/.exec(v);
  if (opm) {
    op = opm[1] as Op;
    v = v.slice(opm[1].length);
  }
  if (field === "status") {
    const cls = /^([1-5])xx$/i.exec(v);
    if (cls && op === "=") return { op, value: 0, cls: Number(cls[1]) };
  }
  const n = field === "ms" ? parseDuration(v) : /^\d+$/.test(v) ? Number(v) : null;
  if (n === null || Number.isNaN(n)) return null;
  return { op, value: n };
}

export function parseQuery(input: string): ParsedQuery {
  const clauses: Clause[] = [];
  const terms: string[] = [];
  for (let tok of tokenize(input)) {
    let neg = false;
    if (tok.length > 1 && tok.startsWith("-")) {
      neg = true;
      tok = tok.slice(1);
    }
    // #42 -> one request
    if (/^#\d+$/.test(tok)) {
      clauses.push({ type: "req", value: tok.slice(1), neg });
      continue;
    }
    const m = /^([a-z]+)(:|>=|<=|>|<)(.*)$/i.exec(tok);
    if (m && m[3] !== "") {
      const name = m[1].toLowerCase();
      const sep = m[2];
      const value = m[3];
      if (name === "is" && sep === ":") {
        clauses.push({ type: "is", flag: value.toLowerCase(), neg });
        continue;
      }
      if (name === "level" || name === "lvl") {
        clauses.push({ type: "level", value: value.toUpperCase(), neg });
        continue;
      }
      if (name === "req" || name === "request" || name === "id") {
        clauses.push({ type: "req", value: value.replace(/^#/, ""), neg });
        continue;
      }
      if (name in NUM_FIELDS) {
        const field = NUM_FIELDS[name];
        const parsed = parseNumberish(field, sep === ":" ? value : sep + value);
        if (parsed) {
          clauses.push({ type: "num", field, neg, ...parsed });
          continue;
        }
      }
      if (sep === ":" && name in TEXT_FIELDS) {
        const v = value.toLowerCase();
        clauses.push({ type: "field", field: TEXT_FIELDS[name], value: v, neg });
        if (!neg) terms.push(v);
        continue;
      }
    }
    const v = tok.toLowerCase();
    clauses.push({ type: "text", value: v, neg });
    if (!neg) terms.push(v);
  }
  return { clauses, terms };
}

// ── Matching ───────────────────────────────────────────────────────────────

export function shortReq(id: string): string {
  const m = /-(\d+)$/.exec(id);
  return m ? `#${Number(m[1])}` : `#${id.slice(-6)}`;
}

const haystacks = new WeakMap<ConsoleLogEntry, string>();

function haystack(e: ConsoleLogEntry): string {
  let h = haystacks.get(e);
  if (h === undefined) {
    h = [
      e.msg,
      e.detail,
      e.hint,
      e.key,
      e.model,
      e.provider,
      e.target,
      e.endpoint,
      e.client,
      e.kind,
      e.req,
      e.req ? shortReq(e.req) : "",
      e.status ? String(e.status) : "",
    ]
      .filter(Boolean)
      .join("\n")
      .toLowerCase();
    haystacks.set(e, h);
  }
  return h;
}

function fieldValue(e: ConsoleLogEntry, f: TextField): string {
  switch (f) {
    case "msg":
      return e.msg;
    case "kind":
      return e.kind || "";
    default:
      return (e[f] as string | undefined) || "";
  }
}

function cmp(a: number, op: Op, b: number): boolean {
  switch (op) {
    case ">":
      return a > b;
    case ">=":
      return a >= b;
    case "<":
      return a < b;
    case "<=":
      return a <= b;
    default:
      return a === b;
  }
}

function reqMatches(e: ConsoleLogEntry, value: string): boolean {
  if (!e.req) return false;
  if (/^\d+$/.test(value)) return shortReq(e.req) === `#${Number(value)}`;
  return e.req.toLowerCase().includes(value.toLowerCase());
}

export function isFailed(e: ConsoleLogEntry): boolean {
  return !!e.final && (e.status ?? 0) >= 400 && e.status !== 499;
}

function flagMatches(e: ConsoleLogEntry, flag: string, inflight: boolean): boolean {
  switch (flag) {
    case "failed":
    case "fail":
      return isFailed(e);
    case "ok":
    case "success":
      return !!e.final && (e.status ?? 0) > 0 && (e.status ?? 0) < 400;
    case "error":
      return e.level === "ERROR";
    case "warn":
    case "warning":
      return e.level === "WARN";
    case "slow":
      return (e.ms ?? 0) >= SLOW_MS && !e.stream;
    case "fallback":
      return (e.fallbacks ?? 0) > 0;
    case "stream":
      return !!e.stream;
    case "cache":
      return !!e.cache;
    case "inflight":
    case "pending":
      return inflight;
    case "system":
      return !!e.kind && e.kind !== "request";
    case "disconnected":
    case "canceled":
      return e.status === 499;
  }
  return false;
}

// One clause against one line. `head` is the request's headline (final line,
// or latest line while in flight) and decides outcome-style clauses — level
// and is: — so `is:failed` means "the request failed", not "some line did".
function clauseHits(c: Clause, e: ConsoleLogEntry, head: ConsoleLogEntry, inflight: boolean): boolean {
  switch (c.type) {
    case "text":
      return haystack(e).includes(c.value);
    case "field":
      return fieldValue(e, c.field).toLowerCase().includes(c.value);
    case "num": {
      const v = e[c.field];
      if (v === undefined || v === 0) return false;
      if (c.cls !== undefined) return Math.floor(v / 100) === c.cls;
      return cmp(v, c.op, c.value);
    }
    case "level":
      return head.level.startsWith(c.value);
    case "req":
      return reqMatches(e, c.value);
    case "is":
      return flagMatches(head, c.flag, inflight);
  }
}

/**
 * Reports whether a row matches. `lines` are all lines of the row (one for a
 * standalone line, the whole timeline for a request): a positive clause needs
 * any line to hit it, a negative clause needs no line to hit it, so `529`
 * finds a request whose fallback attempt saw a 529 even though it succeeded.
 */
export function matches(q: ParsedQuery, lines: ConsoleLogEntry[], head: ConsoleLogEntry, inflight = false): boolean {
  for (const c of q.clauses) {
    let hit = false;
    if (c.type === "level" || c.type === "is") {
      hit = clauseHits(c, head, head, inflight);
    } else {
      for (const l of lines) {
        if (clauseHits(c, l, head, inflight)) {
          hit = true;
          break;
        }
      }
    }
    if (hit === c.neg) return false;
  }
  return true;
}

/** Quote a value for insertion into the search box. */
export function queryToken(field: string, value: string): string {
  return /[\s"]/.test(value) ? `${field}:"${value.replace(/"/g, "")}"` : `${field}:${value}`;
}
