import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
  Search,
  LayoutGrid,
  Boxes,
  Network,
  Layers,
  BarChart3,
  Clock,
  TerminalSquare,
  Image,
  Waypoints,
  Sparkles,
  ScrollText,
  Wallet,
  Key,
  KeyRound,
  Plus,
  Settings,
  Shield,
  Activity,
  History,
  CornerDownLeft,
  type LucideIcon,
} from "lucide-react";

// CommandKind classifies each entry so results can be badged and (when a query
// is active) ranked in a single flat list rather than only grouped by section.
type CommandKind =
  | "navigation"
  | "action"
  | "provider"
  | "account"
  | "chain"
  | "key"
  | "plan"
  | "skill"
  | "pool";

interface CommandItem {
  id: string;
  label: string;
  description?: string;
  /** Short right-aligned tag, e.g. "Provider", "Disabled", or a plan name. */
  badge?: string;
  icon: LucideIcon;
  section: string;
  kind: CommandKind;
  action: () => void;
  keywords?: string[];
}

interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
}

const RECENT_KEY = "kei-cmdk-recent";
const RECENT_MAX = 6;
const RESULT_LIMIT = 40;

// ---------------------------------------------------------------------------
// Fuzzy matching
// ---------------------------------------------------------------------------

// fuzzyScore ranks how well `q` matches `text`, returning a higher number for a
// better match or null for no match. Ordering, from best to worst: exact match,
// prefix match, word-boundary substring, plain substring, then a subsequence
// fallback so "opr" still finds "OpenAI Provider". Returns 0 for an empty query.
function fuzzyScore(text: string, q: string): number | null {
  if (!q) return 0;
  const t = text.toLowerCase();
  if (t === q) return 1000;

  const idx = t.indexOf(q);
  if (idx === 0) return 900 - (t.length - q.length) * 0.5;
  if (idx > 0) {
    const boundary = /[^a-z0-9]/.test(t[idx - 1]);
    return (boundary ? 700 : 500) - idx;
  }

  // Subsequence fallback with a consecutive-run bonus.
  let ti = 0;
  let qi = 0;
  let streak = 0;
  let score = 0;
  let first = -1;
  while (ti < t.length && qi < q.length) {
    if (t[ti] === q[qi]) {
      if (first < 0) first = ti;
      streak += 1;
      score += 10 + streak * 2;
      qi += 1;
    } else {
      streak = 0;
    }
    ti += 1;
  }
  if (qi < q.length) return null;
  return Math.max(40, 220 - first) + score - t.length * 0.1;
}

// matchIndices returns the character positions in `text` that matched `q`, used
// to highlight the matched portion of a label. Prefers a contiguous substring
// hit, falling back to the subsequence positions.
function matchIndices(text: string, q: string): number[] {
  if (!q) return [];
  const t = text.toLowerCase();
  const idx = t.indexOf(q);
  if (idx >= 0) return Array.from({ length: q.length }, (_, i) => idx + i);
  const out: number[] = [];
  let qi = 0;
  for (let i = 0; i < t.length && qi < q.length; i += 1) {
    if (t[i] === q[qi]) {
      out.push(i);
      qi += 1;
    }
  }
  return qi === q.length ? out : [];
}

// scoreItem returns the best weighted score across an item's searchable fields.
// The label matters most; keywords, description and section contribute at a
// discount so a keyword hit ranks below a direct label hit.
function scoreItem(item: CommandItem, q: string): number | null {
  let best: number | null = null;
  const consider = (text: string | undefined, weight: number) => {
    if (!text) return;
    const s = fuzzyScore(text, q);
    if (s == null) return;
    const w = s * weight;
    if (best == null || w > best) best = w;
  };
  consider(item.label, 1);
  consider(item.badge, 0.75);
  item.keywords?.forEach((k) => consider(k, 0.7));
  consider(item.description, 0.6);
  consider(item.section, 0.45);
  return best;
}

function Highlight({ text, query }: { text: string; query: string }) {
  const idxs = useMemo(() => matchIndices(text, query.toLowerCase()), [text, query]);
  if (!idxs.length) return <>{text}</>;
  const set = new Set(idxs);
  return (
    <>
      {text.split("").map((ch, i) =>
        set.has(i) ? (
          <mark key={i} className="bg-transparent font-semibold text-accent-600 dark:text-accent-300">
            {ch}
          </mark>
        ) : (
          <span key={i}>{ch}</span>
        ),
      )}
    </>
  );
}

function loadRecent(): string[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((x) => typeof x === "string").slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function CommandPalette({ open, onClose }: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [recent, setRecent] = useState<string[]>(loadRecent);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  // Entity data is fetched only while the palette is open, and reuses the same
  // React Query cache keys the pages use so an already-visited page's data is
  // served instantly with no extra request.
  const providersQ = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), enabled: open, staleTime: 60_000 });
  const accountsQ = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts(), enabled: open, staleTime: 30_000 });
  const chainsQ = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains(), enabled: open, staleTime: 30_000 });
  const keysQ = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys(), enabled: open, staleTime: 30_000 });
  const plansQ = useQuery({ queryKey: ["plans"], queryFn: () => api.listPlans(), enabled: open, staleTime: 30_000 });
  const skillsQ = useQuery({ queryKey: ["skills"], queryFn: () => api.listSkills(), enabled: open, staleTime: 30_000 });
  const poolsQ = useQuery({ queryKey: ["proxy-pools"], queryFn: () => api.listProxyPools(), enabled: open, staleTime: 30_000 });

  const entitiesLoading =
    providersQ.isLoading ||
    accountsQ.isLoading ||
    chainsQ.isLoading ||
    keysQ.isLoading ||
    plansQ.isLoading ||
    skillsQ.isLoading ||
    poolsQ.isLoading;

  const rememberRecent = useCallback((id: string) => {
    setRecent((prev) => {
      const next = [id, ...prev.filter((x) => x !== id)].slice(0, RECENT_MAX);
      try {
        localStorage.setItem(RECENT_KEY, JSON.stringify(next));
      } catch {
        // ignore storage failures (private mode / quota)
      }
      return next;
    });
  }, []);

  const go = useCallback(
    (path: string, id?: string) => {
      if (id) rememberRecent(id);
      navigate(path);
      onClose();
    },
    [navigate, onClose, rememberRecent],
  );

  // Static navigation + quick actions. Kept in sync with the sidebar nav.
  const staticItems: CommandItem[] = useMemo(
    () => [
      // Navigation
      { id: "nav-overview", label: "Overview", icon: LayoutGrid, section: "Overview", kind: "navigation", action: () => go("/", "nav-overview"), keywords: ["home", "dashboard", "start"] },
      { id: "nav-endpoints", label: "Endpoints", icon: Network, section: "Traffic & Logic", kind: "navigation", action: () => go("/endpoints", "nav-endpoints"), keywords: ["proxy", "url", "tunnel", "tailscale", "base url"] },
      { id: "nav-chains", label: "Chains", icon: Layers, section: "Traffic & Logic", kind: "navigation", action: () => go("/chains", "nav-chains"), keywords: ["chain", "routing", "fallback", "failover"] },
      { id: "nav-skills", label: "Skills", icon: Sparkles, section: "Traffic & Logic", kind: "navigation", action: () => go("/skills", "nav-skills"), keywords: ["prompt", "system prompt", "custom"] },
      { id: "nav-keys", label: "API Keys", icon: Key, section: "Connections", kind: "navigation", action: () => go("/keys", "nav-keys"), keywords: ["auth", "token", "secret", "bearer"] },
      { id: "nav-providers", label: "Providers", icon: Boxes, section: "Connections", kind: "navigation", action: () => go("/providers", "nav-providers"), keywords: ["accounts", "openai", "anthropic", "upstream", "credentials"] },
      { id: "nav-media", label: "Media", icon: Image, section: "Connections", kind: "navigation", action: () => go("/media", "nav-media"), keywords: ["image", "video", "tts", "stt", "embedding", "audio"] },
      { id: "nav-proxy-pools", label: "Proxy Pools", icon: Waypoints, section: "Connections", kind: "navigation", action: () => go("/proxy-pools", "nav-proxy-pools"), keywords: ["proxy", "residential", "egress", "vercel", "cloudflare"] },
      { id: "nav-guardrails", label: "Guardrails", icon: Shield, section: "Safety", kind: "navigation", action: () => go("/guardrails", "nav-guardrails"), keywords: ["pii", "injection", "toxicity", "bias", "policy", "moderation"] },
      { id: "nav-usage", label: "Usage", icon: BarChart3, section: "Cost & Analytics", kind: "navigation", action: () => go("/usage", "nav-usage"), keywords: ["analytics", "stats", "tokens", "spend", "insights"] },
      { id: "nav-plans", label: "Plans", icon: Wallet, section: "Cost & Analytics", kind: "navigation", action: () => go("/plans", "nav-plans"), keywords: ["cost", "limit", "spend", "budget", "quota"] },
      { id: "nav-quota", label: "Quota Tracker", icon: Clock, section: "Cost & Analytics", kind: "navigation", action: () => go("/quota", "nav-quota"), keywords: ["limits", "upstream", "remaining"] },
      { id: "nav-system", label: "System", icon: Activity, section: "Cost & Analytics", kind: "navigation", action: () => go("/system", "nav-system"), keywords: ["cpu", "memory", "health", "monitor", "goroutines"] },
      { id: "nav-settings", label: "Settings", icon: Settings, section: "Cost & Analytics", kind: "navigation", action: () => go("/settings", "nav-settings"), keywords: ["config", "preferences", "token saving", "rtk", "caveman", "terse", "cache"] },
      { id: "nav-console", label: "Console Log", icon: ScrollText, section: "Developer", kind: "navigation", action: () => go("/console", "nav-console"), keywords: ["logs", "debug", "output", "stream"] },
      { id: "nav-cli-tools", label: "CLI Tools", icon: TerminalSquare, section: "Developer", kind: "navigation", action: () => go("/cli-tools", "nav-cli-tools"), keywords: ["claude", "codex", "cline", "copilot", "cursor", "terminal"] },

      // Quick actions
      { id: "action-new-key", label: "Create API Key", icon: Plus, section: "Actions", kind: "action", action: () => go("/keys", "action-new-key"), keywords: ["new", "generate", "add key"] },
      { id: "action-new-account", label: "Add Provider Account", icon: Plus, section: "Actions", kind: "action", action: () => go("/providers", "action-new-account"), keywords: ["new", "connect", "credential", "oauth"] },
      { id: "action-new-chain", label: "Create Chain", icon: Plus, section: "Actions", kind: "action", action: () => go("/chains/new", "action-new-chain"), keywords: ["new", "chain", "routing", "fallback"] },
      { id: "action-new-plan", label: "Create Plan", icon: Plus, section: "Actions", kind: "action", action: () => go("/plans", "action-new-plan"), keywords: ["new", "budget", "limit"] },
      { id: "action-new-skill", label: "Create Skill", icon: Plus, section: "Actions", kind: "action", action: () => go("/skills", "action-new-skill"), keywords: ["new", "prompt"] },
      { id: "action-new-pool", label: "Add Proxy Pool", icon: Plus, section: "Actions", kind: "action", action: () => go("/proxy-pools", "action-new-pool"), keywords: ["new", "proxy"] },
      { id: "action-settings", label: "Open Settings", icon: Settings, section: "Actions", kind: "action", action: () => go("/settings", "action-settings"), keywords: ["config", "preferences", "token saving"] },
    ],
    [go],
  );

  // Live entities pulled from the API. These only surface when there's a query
  // (they're excluded from the empty state to keep it focused on navigation).
  const entityItems: CommandItem[] = useMemo(() => {
    const items: CommandItem[] = [];

    for (const p of providersQ.data?.providers ?? []) {
      if (p.hidden) continue;
      items.push({
        id: `provider-${p.id}`,
        label: p.display_name,
        description: p.custom ? "Custom provider" : undefined,
        badge: p.custom ? "Custom" : "Provider",
        icon: Boxes,
        section: "Providers",
        kind: "provider",
        action: () => go(`/providers/${p.id}`, `provider-${p.id}`),
        keywords: [p.alias, p.dialect, p.id, ...(p.service_kinds ?? [])].filter(Boolean),
      });
    }

    const providerName = (id: string) =>
      providersQ.data?.providers.find((p) => p.id === id)?.display_name ?? id;

    for (const a of accountsQ.data?.accounts ?? []) {
      const status = a.disabled ? "Disabled" : a.needs_reconnect ? "Reconnect" : "Account";
      items.push({
        id: `account-${a.id}`,
        label: a.label || providerName(a.provider),
        description: `${providerName(a.provider)} account`,
        badge: status,
        icon: KeyRound,
        section: "Accounts",
        kind: "account",
        action: () => go(`/providers/${a.provider}`, `account-${a.id}`),
        keywords: [a.provider, a.auth_kind, a.label].filter(Boolean),
      });
    }

    for (const c of chainsQ.data?.chains ?? []) {
      items.push({
        id: `chain-${c.id}`,
        label: c.name,
        description: `${c.steps?.length ?? 0} step${(c.steps?.length ?? 0) === 1 ? "" : "s"} · ${c.strategy}`,
        badge: "Chain",
        icon: Layers,
        section: "Chains",
        kind: "chain",
        action: () => go("/chains", `chain-${c.id}`),
        keywords: [c.strategy, c.fallback_provider, c.fallback_model, ...(c.steps?.map((s) => s.model) ?? [])].filter(Boolean) as string[],
      });
    }

    for (const k of keysQ.data?.keys ?? []) {
      items.push({
        id: `key-${k.id}`,
        label: k.name,
        description: k.display,
        badge: k.disabled ? "Disabled" : k.plan_name || "API Key",
        icon: Key,
        section: "API Keys",
        kind: "key",
        action: () => go(`/keys/${k.id}`, `key-${k.id}`),
        keywords: [k.plan_name, k.display].filter(Boolean) as string[],
      });
    }

    for (const pl of plansQ.data?.plans ?? []) {
      items.push({
        id: `plan-${pl.id}`,
        label: pl.name,
        description: pl.description || `${pl.key_count} key${pl.key_count === 1 ? "" : "s"}`,
        badge: "Plan",
        icon: Wallet,
        section: "Plans",
        kind: "plan",
        action: () => go("/plans", `plan-${pl.id}`),
        keywords: [pl.period].filter(Boolean),
      });
    }

    for (const s of skillsQ.data?.skills ?? []) {
      items.push({
        id: `skill-${s.id}`,
        label: s.name,
        description: s.description || undefined,
        badge: s.enabled ? "Skill" : "Disabled",
        icon: Sparkles,
        section: "Skills",
        kind: "skill",
        action: () => go("/skills", `skill-${s.id}`),
      });
    }

    for (const pool of poolsQ.data?.pools ?? []) {
      items.push({
        id: `pool-${pool.id}`,
        label: pool.name,
        description: `${pool.type} · ${pool.is_active ? "active" : "inactive"}`,
        badge: "Proxy Pool",
        icon: Waypoints,
        section: "Proxy Pools",
        kind: "pool",
        action: () => go("/proxy-pools", `pool-${pool.id}`),
        keywords: [pool.type, pool.proxy_url].filter(Boolean),
      });
    }

    return items;
  }, [providersQ.data, accountsQ.data, chainsQ.data, keysQ.data, plansQ.data, skillsQ.data, poolsQ.data, go]);

  const allItems = useMemo(() => [...staticItems, ...entityItems], [staticItems, entityItems]);
  const itemById = useMemo(() => new Map(allItems.map((i) => [i.id, i])), [allItems]);

  const trimmed = query.trim();

  // groupsToRender drives the visible layout; flat is the parallel ordered list
  // used for keyboard selection so indices line up with what's on screen.
  const { groupsToRender, flat } = useMemo(() => {
    const groups: { section: string; heading: boolean; items: CommandItem[] }[] = [];

    if (!trimmed) {
      // Empty state: recents first, then static navigation grouped by section.
      const recentItems = recent.map((id) => itemById.get(id)).filter((x): x is CommandItem => !!x);
      if (recentItems.length) groups.push({ section: "Recent", heading: true, items: recentItems });
      for (const item of staticItems) {
        const last = groups[groups.length - 1];
        if (last && last.section === item.section && last.heading) last.items.push(item);
        else groups.push({ section: item.section, heading: true, items: [item] });
      }
    } else {
      // Query state: everything is ranked into one flat, badge-annotated list.
      const q = trimmed.toLowerCase();
      const scored: { item: CommandItem; score: number }[] = [];
      for (const item of allItems) {
        const s = scoreItem(item, q);
        if (s != null) scored.push({ item, score: s });
      }
      scored.sort((a, b) => b.score - a.score || a.item.label.length - b.item.label.length);
      groups.push({ section: "", heading: false, items: scored.slice(0, RESULT_LIMIT).map((s) => s.item) });
    }

    const flatList = groups.flatMap((g) => g.items);
    return { groupsToRender: groups, flat: flatList };
  }, [trimmed, recent, itemById, staticItems, allItems]);

  // Reset selection when the result set changes.
  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      setRecent(loadRecent());
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  useEffect(() => {
    if (!listRef.current) return;
    const selected = listRef.current.querySelector(`[data-index="${selectedIndex}"]`);
    selected?.scrollIntoView({ block: "nearest" });
  }, [selectedIndex, groupsToRender]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          if (flat.length) setSelectedIndex((i) => (i + 1) % flat.length);
          break;
        case "ArrowUp":
          e.preventDefault();
          if (flat.length) setSelectedIndex((i) => (i - 1 + flat.length) % flat.length);
          break;
        case "Home":
          e.preventDefault();
          setSelectedIndex(0);
          break;
        case "End":
          e.preventDefault();
          if (flat.length) setSelectedIndex(flat.length - 1);
          break;
        case "Enter":
          e.preventDefault();
          flat[selectedIndex]?.action();
          break;
        case "Escape":
          e.preventDefault();
          onClose();
          break;
      }
    },
    [flat, selectedIndex, onClose],
  );

  if (!open) return null;

  let flatIndex = 0;

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center pt-[15vh]" role="dialog" aria-modal="true" aria-label="Command palette">
      {/* Backdrop */}
      <div className="fixed inset-0 bg-black/40 backdrop-blur-sm" onClick={onClose} />

      {/* Panel */}
      <div
        className="relative w-full max-w-xl overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]"
        style={{ animation: "page-in 0.15s ease-out" }}
        onKeyDown={handleKeyDown}
      >
        {/* Input */}
        <div className="flex items-center gap-3 border-b border-[var(--border)] px-4">
          <Search className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search pages, providers, keys, chains…"
            className="h-12 flex-1 bg-transparent text-sm text-[var(--text)] placeholder:text-[var(--text-muted)] focus:outline-none"
            autoComplete="off"
            spellCheck={false}
          />
          {entitiesLoading && (
            <div className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent text-[var(--text-muted)]" />
          )}
          <kbd className="shrink-0 rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
            esc
          </kbd>
        </div>

        {/* Results */}
        <div ref={listRef} className="max-h-[22rem] overflow-y-auto py-2">
          {flat.length === 0 ? (
            <div className="px-4 py-10 text-center text-sm text-[var(--text-muted)]">
              {trimmed ? (
                <>No results for &quot;{trimmed}&quot;</>
              ) : (
                <>Type to search across pages and your data</>
              )}
            </div>
          ) : (
            groupsToRender.map((group, gi) => (
              <div key={group.section || `results-${gi}`}>
                {group.heading && group.section && (
                  <p className="flex items-center gap-1.5 px-4 pb-1 pt-2 text-[11.5px] font-medium text-[var(--text-muted)]">
                    {group.section === "Recent" && <History className="h-3 w-3" />}
                    {group.section}
                  </p>
                )}
                {group.items.map((item) => {
                  const idx = flatIndex++;
                  const selected = idx === selectedIndex;
                  return (
                    <button
                      key={`${group.section}-${item.id}`}
                      data-index={idx}
                      onClick={item.action}
                      onMouseMove={() => setSelectedIndex(idx)}
                      className={`flex w-full items-center gap-3 px-4 py-2 text-left text-sm transition-colors ${
                        selected
                          ? "bg-accent-100 text-accent-700 dark:bg-accent-800/40 dark:text-accent-200"
                          : "text-[var(--text)] hover:bg-[var(--bg-subtle)]"
                      }`}
                    >
                      <item.icon className="h-4 w-4 shrink-0 text-[var(--text-muted)]" strokeWidth={2} />
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="truncate">
                          <Highlight text={item.label} query={trimmed} />
                        </span>
                        {item.description && (
                          <span className="truncate text-xs text-[var(--text-muted)]">{item.description}</span>
                        )}
                      </span>
                      {trimmed && item.badge && (
                        <span className="shrink-0 rounded-md border border-[var(--border)] bg-[var(--bg-subtle)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
                          {item.badge}
                        </span>
                      )}
                      {selected && (
                        <CornerDownLeft className="h-3.5 w-3.5 shrink-0 text-[var(--text-muted)]" />
                      )}
                    </button>
                  );
                })}
              </div>
            ))
          )}
        </div>

        {/* Footer hint */}
        <div className="flex items-center gap-4 border-t border-[var(--border)] px-4 py-2 text-[10px] text-[var(--text-muted)]">
          <span className="flex items-center gap-1">
            <kbd className="rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-1 py-px font-mono">↑↓</kbd>
            navigate
          </span>
          <span className="flex items-center gap-1">
            <kbd className="rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-1 py-px font-mono">↵</kbd>
            select
          </span>
          <span className="flex items-center gap-1">
            <kbd className="rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-1 py-px font-mono">esc</kbd>
            close
          </span>
          {flat.length > 0 && (
            <span className="ml-auto tabular-nums">{flat.length} result{flat.length === 1 ? "" : "s"}</span>
          )}
        </div>
      </div>
    </div>
  );
}
