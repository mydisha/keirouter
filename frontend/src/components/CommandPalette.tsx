import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Command } from "cmdk";
import { Dialog } from "radix-ui";
import { Copy, CornerDownLeft, History, Monitor, Moon, Plus, Search, Sun, type LucideIcon } from "lucide-react";
import { api } from "../lib/api";
import { ICONS } from "../lib/icons";
import { cn } from "@/lib/utils";
import { IconTile } from "./ui";
import { useTheme } from "./ThemeProvider";
import { useToast } from "./Toast";
import { ProviderLogo } from "./ProviderLogo";

// The palette is built on cmdk (keyboard navigation, selection, ARIA listbox
// semantics) inside a Radix dialog (focus trap, scroll lock, Escape). Ranking
// stays ours: cmdk's filter is disabled so the fuzzy scorer below decides
// order and the matched characters can be highlighted.

interface CommandItem {
  id: string;
  label: string;
  description?: string;
  /** Short right-aligned tag shown while searching ("Provider", "Disabled"). */
  badge?: string;
  icon?: LucideIcon;
  /** Sidebar group hue of the page this item leads to (icon tile colour). */
  tone: PaletteTone;
  /** Provider logo, used instead of `icon` for provider and account rows. */
  logo?: { icon?: string; name: string };
  section: string;
  run: () => void;
  keywords?: string[];
}

// Same hue per sidebar group as Layout: Monitor blue, Routing violet,
// Providers teal, Access orange, Workspace slate.
type PaletteTone = "blue" | "violet" | "teal" | "orange" | "slate";

const RECENT_KEY = "kei-cmdk-recent";
const RECENT_MAX = 6;
const RESULT_LIMIT = 40;

// fuzzyScore ranks how well `q` matches `text`: exact, prefix, word-boundary
// substring, substring, then a subsequence fallback so "opr" still finds
// "OpenRouter". Null means no match.
function fuzzyScore(text: string, q: string): number | null {
  if (!q) return 0;
  const t = text.toLowerCase();
  if (t === q) return 1000;
  const idx = t.indexOf(q);
  if (idx === 0) return 900 - (t.length - q.length) * 0.5;
  if (idx > 0) return (/[^a-z0-9]/.test(t[idx - 1]) ? 700 : 500) - idx;
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

// Label hits outrank keyword, description and section hits.
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
  const idxs = useMemo(() => new Set(matchIndices(text, query.toLowerCase())), [text, query]);
  if (!idxs.size) return <>{text}</>;
  return (
    <>
      {text.split("").map((ch, i) =>
        idxs.has(i) ? (
          <mark key={i} className="bg-transparent font-semibold text-fg">
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
    const parsed = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]");
    return Array.isArray(parsed) ? parsed.filter((x) => typeof x === "string").slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [recent, setRecent] = useState<string[]>(loadRecent);
  const navigate = useNavigate();
  const { setTheme } = useTheme();
  const toast = useToast();

  // Entity data loads only while open and shares the pages' query keys, so an
  // already-visited page's data is served from cache.
  const providersQ = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), enabled: open, staleTime: 60_000 });
  const accountsQ = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts(), enabled: open, staleTime: 30_000 });
  const chainsQ = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains(), enabled: open, staleTime: 30_000 });
  const keysQ = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys(), enabled: open, staleTime: 30_000 });
  const plansQ = useQuery({ queryKey: ["plans"], queryFn: () => api.listPlans(), enabled: open, staleTime: 30_000 });
  const skillsQ = useQuery({ queryKey: ["skills"], queryFn: () => api.listSkills(), enabled: open, staleTime: 30_000 });
  const poolsQ = useQuery({ queryKey: ["proxy-pools"], queryFn: () => api.listProxyPools(), enabled: open, staleTime: 30_000 });
  const loading = [providersQ, accountsQ, chainsQ, keysQ, plansQ, skillsQ, poolsQ].some((q) => q.isLoading);

  useEffect(() => {
    if (open) {
      setQuery("");
      setRecent(loadRecent());
    }
  }, [open]);

  const remember = useCallback((id: string) => {
    setRecent((prev) => {
      const next = [id, ...prev.filter((x) => x !== id)].slice(0, RECENT_MAX);
      try {
        localStorage.setItem(RECENT_KEY, JSON.stringify(next));
      } catch {
        /* private mode / quota: recents are a convenience */
      }
      return next;
    });
  }, []);

  const go = useCallback(
    (path: string) => () => {
      navigate(path);
    },
    [navigate],
  );

  // Pages and actions, grouped to mirror the sidebar.
  const staticItems: CommandItem[] = useMemo(
    () => [
      { id: "nav-overview", label: "Overview", icon: ICONS.overview, tone: "blue", section: "Go to", run: go("/"), keywords: ["home", "dashboard"] },
      { id: "nav-usage", label: "Usage", icon: ICONS.usage, tone: "blue", section: "Go to", run: go("/usage"), keywords: ["analytics", "tokens", "spend", "cost"] },
      { id: "nav-console", label: "Console", icon: ICONS.console, tone: "blue", section: "Go to", run: go("/console"), keywords: ["logs", "debug", "stream"] },
      { id: "nav-endpoints", label: "Endpoints", icon: ICONS.endpoints, tone: "violet", section: "Go to", run: go("/endpoints"), keywords: ["base url", "tunnel", "tailscale", "cloudflare"] },
      { id: "nav-chains", label: "Chains", icon: ICONS.chains, tone: "violet", section: "Go to", run: go("/chains"), keywords: ["routing", "fallback", "failover"] },
      { id: "nav-skills", label: "Skills", icon: ICONS.skills, tone: "violet", section: "Go to", run: go("/skills"), keywords: ["prompt", "system prompt"] },
      { id: "nav-providers", label: "Providers", icon: ICONS.providers, tone: "teal", section: "Go to", run: go("/providers"), keywords: ["accounts", "upstream", "credentials"] },
      { id: "nav-media", label: "Media", icon: ICONS.media, tone: "teal", section: "Go to", run: go("/media"), keywords: ["image", "video", "tts", "stt"] },
      { id: "nav-health", label: "Provider health", icon: ICONS.health, tone: "teal", section: "Go to", run: go("/provider-health"), keywords: ["uptime", "errors", "latency", "status"] },
      { id: "nav-quota", label: "Quota", icon: ICONS.quota, tone: "teal", section: "Go to", run: go("/quota"), keywords: ["limits", "remaining", "upstream"] },
      { id: "nav-proxy-pools", label: "Proxy pools", icon: ICONS.proxyPools, tone: "teal", section: "Go to", run: go("/proxy-pools"), keywords: ["proxy", "egress"] },
      { id: "nav-keys", label: "API keys", icon: ICONS.keys, tone: "orange", section: "Go to", run: go("/keys"), keywords: ["auth", "token", "bearer"] },
      { id: "nav-plans", label: "Plans & budgets", icon: ICONS.plans, tone: "orange", section: "Go to", run: go("/plans"), keywords: ["budget", "limit", "rate limit"] },
      { id: "nav-guardrails", label: "Guardrails", icon: ICONS.guardrails, tone: "orange", section: "Go to", run: go("/guardrails"), keywords: ["pii", "injection", "moderation"] },
      { id: "nav-cli-tools", label: "CLI tools", icon: ICONS.cliTools, tone: "slate", section: "Go to", run: go("/cli-tools"), keywords: ["claude code", "codex", "cursor", "configure"] },
      { id: "nav-system", label: "System", icon: ICONS.system, tone: "slate", section: "Go to", run: go("/system"), keywords: ["cpu", "memory", "monitor"] },
      { id: "nav-settings", label: "Settings", icon: ICONS.settings, tone: "slate", section: "Go to", run: go("/settings"), keywords: ["token saving", "rtk", "caveman", "branding", "backup"] },

      { id: "action-connect", label: "Connect a provider", icon: Plus, tone: "teal", section: "Actions", run: go("/providers"), keywords: ["add account", "oauth", "api key"] },
      { id: "action-new-key", label: "Create API key", icon: Plus, tone: "orange", section: "Actions", run: go("/keys"), keywords: ["new key", "generate"] },
      { id: "action-new-chain", label: "Create chain", icon: Plus, tone: "violet", section: "Actions", run: go("/chains/new"), keywords: ["new chain", "fallback"] },
      { id: "action-new-plan", label: "Create plan", icon: Plus, tone: "orange", section: "Actions", run: go("/plans"), keywords: ["budget", "limit"] },
      {
        id: "action-copy-base-url",
        label: "Copy base URL",
        icon: Copy,
        tone: "violet",
        section: "Actions",
        description: `${window.location.origin}/v1`,
        keywords: ["endpoint", "openai", "anthropic"],
        run: () => {
          navigator.clipboard
            .writeText(`${window.location.origin}/v1`)
            .then(() => toast.success("Base URL copied", `${window.location.origin}/v1`))
            .catch(() => toast.error("Couldn't copy", "Your browser blocked clipboard access."));
        },
      },
      { id: "theme-light", label: "Use light theme", icon: Sun, tone: "slate", section: "Theme", run: () => setTheme("light"), keywords: ["appearance"] },
      { id: "theme-dark", label: "Use dark theme", icon: Moon, tone: "slate", section: "Theme", run: () => setTheme("dark"), keywords: ["appearance", "night"] },
      { id: "theme-system", label: "Match system theme", icon: Monitor, tone: "slate", section: "Theme", run: () => setTheme("system"), keywords: ["appearance", "auto"] },
    ],
    [go, setTheme, toast],
  );

  // Live entities surface only while searching, keeping the empty state short.
  const entityItems: CommandItem[] = useMemo(() => {
    const items: CommandItem[] = [];
    const providers = providersQ.data?.providers ?? [];
    const byId = new Map(providers.map((p) => [p.id, p]));
    for (const p of providers) {
      if (p.hidden) continue;
      items.push({
        id: `provider-${p.id}`,
        label: p.display_name,
        description: p.id,
        badge: p.custom ? "Custom" : "Provider",
        icon: ICONS.providers,
        tone: "teal",
        logo: { icon: p.icon, name: p.display_name },
        section: "Providers",
        run: go(`/providers/${p.id}`),
        keywords: [p.alias, p.dialect, p.id, ...(p.service_kinds ?? [])].filter(Boolean),
      });
    }
    for (const a of accountsQ.data?.accounts ?? []) {
      const p = byId.get(a.provider);
      items.push({
        id: `account-${a.id}`,
        label: a.label || p?.display_name || a.provider,
        description: `${p?.display_name ?? a.provider} account`,
        badge: a.disabled ? "Paused" : a.needs_reconnect ? "Reconnect" : "Account",
        icon: ICONS.account,
        tone: "teal",
        logo: p ? { icon: p.icon, name: p.display_name } : undefined,
        section: "Accounts",
        run: go(`/providers/${a.provider}`),
        keywords: [a.provider, a.auth_kind, a.label].filter(Boolean),
      });
    }
    for (const c of chainsQ.data?.chains ?? []) {
      items.push({
        id: `chain-${c.id}`,
        label: c.name,
        description: `${c.steps?.length ?? 0} step${(c.steps?.length ?? 0) === 1 ? "" : "s"} · ${c.strategy}`,
        badge: "Chain",
        icon: ICONS.chains,
        tone: "violet",
        section: "Chains",
        run: go(`/chains/${c.id}/edit`),
        keywords: [c.strategy, c.fallback_model, ...(c.steps?.map((s) => s.model) ?? [])].filter(Boolean) as string[],
      });
    }
    for (const k of keysQ.data?.keys ?? []) {
      items.push({
        id: `key-${k.id}`,
        label: k.name,
        description: k.display,
        badge: k.disabled ? "Disabled" : k.plan_name || "API key",
        icon: ICONS.keys,
        tone: "orange",
        section: "API keys",
        run: go(`/keys/${k.id}`),
        keywords: [k.plan_name, k.display].filter(Boolean) as string[],
      });
    }
    for (const pl of plansQ.data?.plans ?? []) {
      items.push({
        id: `plan-${pl.id}`,
        label: pl.name,
        description: pl.description || `${pl.key_count} key${pl.key_count === 1 ? "" : "s"}`,
        badge: "Plan",
        icon: ICONS.plans,
        tone: "orange",
        section: "Plans",
        run: go("/plans"),
      });
    }
    for (const s of skillsQ.data?.skills ?? []) {
      items.push({
        id: `skill-${s.id}`,
        label: s.name,
        description: s.description || undefined,
        badge: s.enabled ? "Skill" : "Disabled",
        icon: ICONS.skills,
        tone: "violet",
        section: "Skills",
        run: go("/skills"),
      });
    }
    for (const pool of poolsQ.data?.pools ?? []) {
      items.push({
        id: `pool-${pool.id}`,
        label: pool.name,
        description: `${pool.type} · ${pool.is_active ? "active" : "inactive"}`,
        badge: "Proxy pool",
        icon: ICONS.proxyPools,
        tone: "teal",
        section: "Proxy pools",
        run: go("/proxy-pools"),
        keywords: [pool.type],
      });
    }
    return items;
  }, [providersQ.data, accountsQ.data, chainsQ.data, keysQ.data, plansQ.data, skillsQ.data, poolsQ.data, go]);

  const allItems = useMemo(() => [...staticItems, ...entityItems], [staticItems, entityItems]);
  const trimmed = query.trim();

  const groups = useMemo(() => {
    if (!trimmed) {
      const byId = new Map(allItems.map((i) => [i.id, i]));
      const recents = recent.map((id) => byId.get(id)).filter((x): x is CommandItem => !!x);
      const out: { heading: string; items: CommandItem[] }[] = [];
      if (recents.length) out.push({ heading: "Recent", items: recents });
      for (const section of ["Go to", "Actions", "Theme"]) {
        out.push({ heading: section, items: staticItems.filter((i) => i.section === section && !recent.includes(i.id)) });
      }
      return out;
    }
    const q = trimmed.toLowerCase();
    const scored = allItems
      .map((item) => ({ item, score: scoreItem(item, q) }))
      .filter((x): x is { item: CommandItem; score: number } => x.score != null)
      .sort((a, b) => b.score - a.score || a.item.label.length - b.item.label.length)
      .slice(0, RESULT_LIMIT)
      .map((x) => x.item);
    return [{ heading: "", items: scored }];
  }, [trimmed, allItems, staticItems, recent]);

  const resultCount = groups.reduce((n, g) => n + g.items.length, 0);

  const select = (item: CommandItem) => {
    remember(item.id);
    onClose();
    item.run();
  };

  return (
    <Command.Dialog
      open={open}
      onOpenChange={(next) => !next && onClose()}
      label="Search pages, providers, keys and chains"
      shouldFilter={false}
      loop
      overlayClassName="fixed inset-0 z-[100] bg-black/45 data-[state=open]:animate-in data-[state=open]:fade-in-0"
      contentClassName="fixed left-1/2 top-[14vh] z-[100] w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-float)] data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-[0.98]"
    >
      <Dialog.Title className="sr-only">Command palette</Dialog.Title>
      <Dialog.Description className="sr-only">
        Type to search. Use the up and down arrow keys to move, Enter to open, Escape to close.
      </Dialog.Description>
      <div className="flex items-center gap-2.5 border-b border-line px-4">
        <Search className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
        <Command.Input
          value={query}
          onValueChange={setQuery}
          placeholder="Search pages, providers, keys, chains…"
          className="h-12 flex-1 bg-transparent text-[14px] text-fg placeholder:text-fg-faint focus:outline-none"
          style={{ outline: "none" }}
        />
        {loading && <span className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-line-strong border-t-fg-muted" aria-hidden="true" />}
        <kbd className="shrink-0 rounded border border-line bg-subtle px-1.5 font-mono text-[10.5px] text-fg-faint" aria-hidden="true">esc</kbd>
      </div>

      {/* Result count for screen readers; always mounted so changes are read. */}
      <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
        {trimmed ? (loading && resultCount === 0 ? "Searching…" : `${resultCount} result${resultCount === 1 ? "" : "s"}`) : ""}
      </div>

      <Command.List label="Results" aria-busy={loading || undefined} className="max-h-[min(24rem,60vh)] overflow-y-auto p-1.5">
        <Command.Empty className="px-4 py-10 text-center text-[13px] text-fg-muted">
          {trimmed ? `No results for “${trimmed}”` : "Type to search pages and your data"}
        </Command.Empty>
        {groups.map((group, gi) =>
          group.items.length === 0 ? null : (
            <Command.Group
              key={group.heading || `results-${gi}`}
              heading={group.heading ? <GroupHeading recent={group.heading === "Recent"}>{group.heading}</GroupHeading> : undefined}
              className="[&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:pt-2"
            >
              {group.items.map((item) => (
                <Command.Item
                  key={`${group.heading}-${item.id}`}
                  value={`${group.heading}-${item.id}`}
                  onSelect={() => select(item)}
                  data-tone={item.tone}
                  className="group flex cursor-pointer items-center gap-2.5 rounded-lg px-2 py-1.5 text-[13px] text-fg outline-none data-[selected=true]:bg-hover"
                >
                  {item.logo ? (
                    <span className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-subtle ring-1 ring-inset ring-line" aria-hidden="true">
                      <ProviderLogo icon={item.logo.icon} name={item.logo.name} size={16} />
                    </span>
                  ) : item.icon ? (
                    <IconTile icon={item.icon} size="sm" />
                  ) : null}
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate">
                      <Highlight text={item.label} query={trimmed} />
                    </span>
                    {item.description && <span className={cn("truncate text-[12px] text-fg-faint", item.logo && "font-mono")}>{item.description}</span>}
                  </span>
                  {trimmed && item.badge && (
                    <span className="shrink-0 rounded-md border border-line bg-subtle px-1.5 py-px text-[11px] text-fg-muted">{item.badge}</span>
                  )}
                  <CornerDownLeft className="hidden h-3.5 w-3.5 shrink-0 text-fg-faint group-data-[selected=true]:block" aria-hidden="true" />
                </Command.Item>
              ))}
            </Command.Group>
          ),
        )}
      </Command.List>

      <div className="flex items-center gap-4 border-t border-line px-4 py-2 text-[11px] text-fg-faint" aria-hidden="true">
        <Hint keys="↑↓">navigate</Hint>
        <Hint keys="↵">open</Hint>
        <Hint keys="esc">close</Hint>
        {trimmed && <span className="ml-auto tabular-nums">{resultCount} result{resultCount === 1 ? "" : "s"}</span>}
      </div>
    </Command.Dialog>
  );
}

function GroupHeading({ children, recent }: { children: ReactNode; recent?: boolean }) {
  return (
    <span className="flex items-center gap-1.5 text-[11.5px] font-medium text-fg-faint">
      {recent && <History className="h-3 w-3" aria-hidden="true" />}
      {children}
    </span>
  );
}

function Hint({ keys, children }: { keys: string; children: ReactNode }) {
  return (
    <span className="flex items-center gap-1">
      <kbd className="rounded border border-line bg-subtle px-1 font-mono">{keys}</kbd>
      {children}
    </span>
  );
}
