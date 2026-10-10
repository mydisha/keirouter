import { useCallback, useEffect, useId, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Check,
  ChevronDown,
  Copy,
  ExternalLink,
  History,
  Link2,
  MoreHorizontal,
  Plus,
  Search,
  Trash2,
  X,
  type LucideIcon,
} from "lucide-react";
import {
  api,
  fetchKeyUsageById,
  type APIKey,
  type GuardrailPolicyConfig,
  type KeyUsageData,
  type Plan,
} from "../lib/api";
import { microsToUSD, formatTokens } from "../lib/format";
import { cn } from "@/lib/utils";
import { useToast } from "../components/Toast";
import { GuardrailEditor } from "../components/GuardrailEditor";
import { useModelCatalog, type ModelCatalogOption } from "../components/ModelSelect";
import { ProviderLogo } from "../components/ProviderLogo";
import {
  Badge,
  Button,
  IconTile,
  Kpi,
  KpiGrid,
  SectionTitle,
  Select,
  Skeleton,
  TablePagination,
  Toggle,
  useClientPagination,
} from "../components/ui";
import { ICONS } from "../lib/icons";
import { useConfirm } from "../components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";

type Tab = "general" | "models" | "guardrails";
const TAB_VALUES: Tab[] = ["general", "models", "guardrails"];
const TAB_LABELS: Record<Tab, string> = { general: "Overview", models: "Models", guardrails: "Guardrails" };

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";
const USAGE_DAYS = 30;

// ── Local helpers ────────────────────────────────────────────────────────────

function usedAt(iso?: string | null): number | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  // Go's zero time ("0001-01-01…") parses to a negative epoch: treat as never.
  return Number.isFinite(t) && t > 0 ? t : null;
}

function relativeTime(iso?: string | null): string {
  const t = usedAt(iso);
  if (t === null) return "Never";
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 45) return "Just now";
  const m = s / 60;
  if (m < 60) return `${Math.max(1, Math.round(m))} min ago`;
  const h = m / 60;
  if (h < 24) return `${Math.round(h)} h ago`;
  const d = h / 24;
  if (d < 30) return `${Math.round(d)} d ago`;
  return new Date(t).toLocaleDateString();
}

function useCopy() {
  const toast = useToast();
  return useCallback(
    (value: string, title: string, description?: string) =>
      navigator.clipboard.writeText(value).then(
        () => {
          toast.success(title, description);
          return true;
        },
        () => {
          toast.error("Copy failed", "Your browser blocked clipboard access. Select the text and copy it manually.");
          return false;
        },
      ),
    [toast],
  );
}

function fmtUSD(v: number): string {
  const n = v || 0;
  if (n === 0) return "$0.00";
  if (n < 0.01) return `$${n.toPrecision(2)}`;
  return `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function periodLabel(period: string): string {
  if (period === "total") return "All-time";
  return period.charAt(0).toUpperCase() + period.slice(1);
}

function CopyIconButton({ label, value, title, description }: { label: string; value: string; title: string; description?: string }) {
  const copy = useCopy();
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() =>
        copy(value, title, description).then((ok) => {
          if (!ok) return;
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1400);
        })
      }
      aria-label={label}
      title={label}
      className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
    >
      {copied ? <Check className="h-3.5 w-3.5 text-ok" strokeWidth={1.75} aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />}
    </button>
  );
}

function SettingsRow({ label, labelId, description, children }: { label: string; labelId?: string; description?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-8 sm:px-5">
      <div className="min-w-0 sm:max-w-[45%]">
        <p id={labelId} className="text-[13px] font-medium text-fg">{label}</p>
        {description && <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">{description}</p>}
      </div>
      <div className="flex min-w-0 items-center gap-2 sm:justify-end">{children}</div>
    </div>
  );
}

function PanelCard({ icon, title, action, children, footer }: { icon: LucideIcon; title: ReactNode; action?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3 sm:px-5">
        <SectionTitle icon={icon} title={title} id={headingId} action={action} />
      </div>
      {children}
      {footer}
    </section>
  );
}

function SaveBar({ status, children }: { status: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-t border-line bg-subtle px-4 py-3 sm:px-5">
      <div role="status" className="min-w-0 flex-1 text-[12.5px]">{status}</div>
      <div className="flex flex-wrap items-center gap-2">{children}</div>
    </div>
  );
}

function planBudgetText(plan: Plan): string {
  const parts: string[] = [];
  if (plan.limit_micros > 0) parts.push(microsToUSD(plan.limit_micros));
  if (plan.limit_tokens > 0) parts.push(`${formatTokens(plan.limit_tokens)} tokens`);
  if (parts.length === 0) return "No spend limit";
  return `${parts.join(" + ")} / ${plan.period}${plan.hard_cutoff ? " · hard cutoff" : ""}`;
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function KeyDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const copy = useCopy();
  const tabsId = useId();
  const [params, setParams] = useSearchParams();
  const rawTab = params.get("tab");
  const tab: Tab = TAB_VALUES.includes(rawTab as Tab) ? (rawTab as Tab) : "general";
  const setTab = useCallback(
    (t: Tab) =>
      setParams((p) => {
        if (t === "general") p.delete("tab");
        else p.set("tab", t);
        return p;
      }, { replace: true }),
    [setParams],
  );

  // Older links used #models / #guardrails; carry them over to ?tab=.
  useEffect(() => {
    const hash = window.location.hash.replace("#", "") as Tab;
    if (!params.get("tab") && hash !== "general" && TAB_VALUES.includes(hash)) {
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
      setTab(hash);
    }
  }, []);

  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys() });
  const plans = useQuery({ queryKey: ["plans"], queryFn: () => api.listPlans() });
  const key = useMemo(
    () => keys.data?.keys.find((candidate) => candidate.id === id) ?? null,
    [keys.data, id],
  );
  const plan = useMemo(
    () => plans.data?.plans.find((candidate) => candidate.id === key?.plan_id),
    [plans.data, key?.plan_id],
  );

  const toggle = useMutation({
    mutationFn: (k: APIKey) => api.updateKey(k.id, { disabled: !k.disabled }),
    onSuccess: (updated) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success(updated.disabled ? "Key disabled" : "Key enabled", updated.disabled ? "Requests using this key are rejected." : "This key can authenticate requests again.");
    },
    onError: (error) => toast.error("Couldn't update key", error instanceof Error ? error.message : "Please try again."),
  });

  const revoke = useMutation({
    mutationFn: (keyId: string) => api.deleteKey(keyId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success("Key revoked", "It can no longer authenticate requests.");
      navigate("/keys");
    },
    onError: (e: Error) => toast.error("Couldn't revoke key", e.message),
  });

  if (keys.isLoading) {
    return (
      <div className="space-y-4" aria-busy="true" aria-label="Loading key">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-14 w-full max-w-lg" />
        <Skeleton className="h-9 w-72" />
        <Skeleton className="h-80 w-full rounded-2xl" />
      </div>
    );
  }
  if (!key) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <IconTile icon={ICONS.keys} size="lg" className="mx-auto mb-3" />
        <h1 className="text-[14px] font-semibold text-fg">API key not found</h1>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">It may have been revoked.</p>
        <Button className="mt-4" onClick={() => navigate("/keys")}>Back to API keys</Button>
      </div>
    );
  }

  const portalUrl = `${window.location.origin}/portal?id=${key.id}`;

  const revokeKey = async () => {
    if (!(await confirm({ title: `Revoke ${key.name}?`, description: "Tools using this key stop authenticating immediately. This cannot be undone.", confirmLabel: "Revoke", tone: "danger" }))) return;
    revoke.mutate(key.id);
  };

  const onTabKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const i = TAB_VALUES.indexOf(tab);
    const next =
      event.key === "Home" ? 0
        : event.key === "End" ? TAB_VALUES.length - 1
          : (i + (event.key === "ArrowRight" ? 1 : -1) + TAB_VALUES.length) % TAB_VALUES.length;
    setTab(TAB_VALUES[next]);
    document.getElementById(`${tabsId}-tab-${TAB_VALUES[next]}`)?.focus();
  };

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 text-[13px] text-fg-muted">
        <ol className="flex items-center gap-1.5">
          <li>
            <Link to="/keys" className={cn("inline-flex items-center gap-1.5 rounded-md hover:text-fg", FOCUS_RING)}>
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
              API keys
            </Link>
          </li>
          <li aria-hidden="true" className="text-fg-faint">/</li>
          <li className="min-w-0">
            <span aria-current="page" className="block truncate text-fg">{key.name}</span>
          </li>
        </ol>
      </nav>

      <header className="mb-6 flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-center gap-3.5">
          <IconTile icon={ICONS.keys} size="lg" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{key.name}</h1>
              {key.disabled ? <Badge tone="neutral">Disabled</Badge> : <Badge tone="success">Active</Badge>}
            </div>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
              <button
                type="button"
                onClick={() => copy(key.display, "Masked key copied")}
                aria-label={`Copy masked key ${key.display}`}
                className={cn("group inline-flex min-h-6 max-w-full items-center gap-1.5 rounded-md font-mono text-[12.5px] hover:text-fg", FOCUS_RING)}
              >
                <span className="truncate">{key.display}</span>
                <Copy className="h-3 w-3 shrink-0 text-fg-faint" aria-hidden="true" />
              </button>
              <span aria-hidden="true" className="text-fg-faint">·</span>
              <span title={new Date(key.created_at).toLocaleString()}>Created {new Date(key.created_at).toLocaleDateString()}</span>
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" onClick={() => copy(portalUrl, "Portal link copied", "Share it with the key owner.")}>
            <Link2 aria-hidden="true" />
            Copy portal link
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label="More key actions"
              className={cn("flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
            >
              <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => copy(key.id, "Key ID copied")}>
                <Copy aria-hidden="true" />
                Copy internal ID
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => window.open(portalUrl, "_blank", "noopener,noreferrer")}>
                <ExternalLink aria-hidden="true" />
                Open owner portal
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem tone="danger" onSelect={revokeKey} disabled={revoke.isPending}>
                <Trash2 aria-hidden="true" />
                Revoke key
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      <div className="mb-5 flex gap-1 overflow-x-auto border-b border-line" role="tablist" aria-label={`${key.name} sections`} onKeyDown={onTabKeyDown}>
        {TAB_VALUES.map((value) => {
          const active = tab === value;
          return (
            <button
              key={value}
              id={`${tabsId}-tab-${value}`}
              type="button"
              role="tab"
              aria-selected={active}
              aria-controls={`${tabsId}-panel-${value}`}
              tabIndex={active ? 0 : -1}
              onClick={() => setTab(value)}
              className={cn(
                "relative -mb-px inline-flex items-center gap-1.5 whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors",
                FOCUS_RING,
                active ? "text-fg" : "text-fg-muted hover:text-fg",
              )}
            >
              {TAB_LABELS[value]}
              {active && <span aria-hidden="true" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
            </button>
          );
        })}
      </div>

      <div role="tabpanel" id={`${tabsId}-panel-${tab}`} aria-labelledby={`${tabsId}-tab-${tab}`}>
        {tab === "general" && (
          <GeneralTab apiKey={key} plan={plan} portalUrl={portalUrl} onToggle={() => toggle.mutate(key)} togglePending={toggle.isPending} />
        )}
        {tab === "models" && <ModelsTab apiKey={key} plan={plan} plansLoading={plans.isLoading} />}
        {tab === "guardrails" && <GuardrailsTab apiKey={key} />}
      </div>
    </>
  );
}

// ── Overview: usage, limits, settings ────────────────────────────────────────

type LimitRow = { id: string; title: string; used: string; limit: string; pct: number; alert: boolean };

function limitRows(budgets: KeyUsageData["budgets"]): LimitRow[] {
  return budgets.flatMap((b, i) => {
    const out: LimitRow[] = [];
    if (b.limit_usd > 0) {
      out.push({ id: `${i}-usd`, title: `${periodLabel(b.period)} spend`, used: fmtUSD(b.spent_usd), limit: fmtUSD(b.limit_usd), pct: b.usd_pct_used, alert: b.alert });
    }
    if (b.limit_tokens > 0) {
      out.push({ id: `${i}-tokens`, title: `${periodLabel(b.period)} tokens`, used: formatTokens(b.tokens_used), limit: formatTokens(b.limit_tokens), pct: b.tokens_pct_used, alert: b.alert });
    }
    return out;
  });
}

function GeneralTab({
  apiKey,
  plan,
  portalUrl,
  onToggle,
  togglePending,
}: {
  apiKey: APIKey;
  plan?: Plan;
  portalUrl: string;
  onToggle: () => void;
  togglePending: boolean;
}) {
  const acceptId = useId();
  const usageId = useId();
  const lastUsed = usedAt(apiKey.last_used_at);
  // Same query (and cache entry) the owner portal uses for a shared portal ID.
  const usage = useQuery({
    queryKey: ["key-usage", apiKey.id, true, USAGE_DAYS],
    queryFn: () => fetchKeyUsageById(apiKey.id, USAGE_DAYS),
    retry: false,
  });

  const totals = useMemo(
    () =>
      (usage.data?.daily ?? []).reduce(
        (acc, dp) => ({
          requests: acc.requests + dp.requests,
          prompt: acc.prompt + dp.prompt_tokens,
          completion: acc.completion + dp.completion_tokens,
          cost: acc.cost + dp.cost_usd,
        }),
        { requests: 0, prompt: 0, completion: 0, cost: 0 },
      ),
    [usage.data],
  );
  const rows = limitRows(usage.data?.budgets ?? []);
  const dash = usage.isLoading ? "…" : usage.isError ? "—" : null;

  const kpis: { label: string; icon: LucideIcon; value: string; hint?: string; title?: string }[] = [
    { label: "Requests", icon: ICONS.requests, value: dash ?? totals.requests.toLocaleString() },
    { label: "Spend", icon: ICONS.spend, value: dash ?? fmtUSD(totals.cost) },
    {
      label: "Tokens",
      icon: ICONS.tokens,
      value: dash ?? formatTokens(totals.prompt + totals.completion),
      hint: dash ? undefined : `${formatTokens(totals.prompt)} in · ${formatTokens(totals.completion)} out`,
    },
    {
      label: "Last used",
      icon: History,
      value: relativeTime(apiKey.last_used_at),
      title: lastUsed ? new Date(lastUsed).toLocaleString() : undefined,
    },
  ];

  return (
    <div className="space-y-6">
      <section aria-labelledby={usageId} className="space-y-3">
        <SectionTitle
          icon={ICONS.usage}
          title="Usage"
          subtitle={`Last ${USAGE_DAYS} days`}
          id={usageId}
          action={
            <a href={portalUrl} target="_blank" rel="noopener noreferrer" className={cn("inline-flex min-h-6 items-center gap-1 rounded-md text-[12.5px] font-medium text-link hover:underline", FOCUS_RING)}>
              Owner portal
              <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" />
              <span className="sr-only">(opens in a new tab)</span>
            </a>
          }
        />
        <div aria-busy={usage.isLoading}>
          <KpiGrid cols={4} label={`Usage, last ${USAGE_DAYS} days`}>
            {kpis.map((k) => (
              <Kpi
                key={k.label}
                icon={k.icon}
                label={k.label}
                value={<span title={k.title}>{k.value}</span>}
                hint={k.hint}
              />
            ))}
          </KpiGrid>
        </div>
        {usage.isError && (
          <p role="status" className="text-[12.5px] text-fg-muted">Usage couldn't be loaded. Try again later.</p>
        )}
      </section>

      <PanelCard icon={ICONS.budget} title="Limits">
        <div className="divide-y divide-line">
          <SettingsRow
            label="Plan"
            description={
              plan
                ? planBudgetText(plan)
                : apiKey.plan_id || usage.isLoading
                  ? undefined
                  : rows.length > 0
                    ? "Limits set on this key"
                    : "No spend or token limit"
            }
          >
            <span className="truncate text-[13px] text-fg">{apiKey.plan_name || "Custom"}</span>
            <Link to="/plans" className={cn("shrink-0 rounded-md text-[12.5px] font-medium text-link hover:underline", FOCUS_RING)}>
              Manage plans
            </Link>
          </SettingsRow>
          {usage.isLoading ? (
            <div className="px-4 py-3 sm:px-5" aria-hidden="true">
              <Skeleton className="h-8 w-full" />
            </div>
          ) : (
            rows.map((r) => {
              const pct = Math.min(Math.max(r.pct, 0), 100);
              const tone: "bad" | "warn" | "ok" = r.pct >= 100 ? "bad" : r.alert || r.pct > 80 ? "warn" : "ok";
              const pctText = r.pct >= 100 ? "Limit reached" : `${r.pct.toFixed(r.pct < 10 ? 1 : 0)}%`;
              return (
                <div key={r.id} className="space-y-1.5 px-4 py-3 sm:px-5">
                  <div className="flex items-baseline gap-2 text-[13px]">
                    <span className="font-medium text-fg">{r.title}</span>
                    <span className="tabular-nums text-fg-muted">
                      {r.used} of {r.limit}
                    </span>
                    <span className={cn("ml-auto text-[12.5px] font-medium tabular-nums", tone === "bad" ? "text-bad" : tone === "warn" ? "text-warn" : "text-fg")}>
                      {pctText}
                    </span>
                  </div>
                  <div
                    className="h-1.5 overflow-hidden rounded-full bg-track"
                    role="progressbar"
                    aria-label={r.title}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={Math.round(pct)}
                    aria-valuetext={`${r.used} of ${r.limit} · ${pctText}`}
                  >
                    <div className={cn("h-full rounded-full", tone === "bad" ? "bg-bad" : tone === "warn" ? "bg-warn" : "bg-accent-500")} style={{ width: `${pct}%` }} />
                  </div>
                </div>
              );
            })
          )}
        </div>
      </PanelCard>

      <PanelCard icon={ICONS.settings} title="Settings">
        <div className="divide-y divide-line">
          <SettingsRow label="Accept requests" labelId={acceptId} description="Off rejects every request with this key.">
            <Toggle checked={!apiKey.disabled} onChange={onToggle} disabled={togglePending} aria-labelledby={acceptId} />
          </SettingsRow>
          <SettingsRow label="Internal ID">
            <span className="truncate font-mono text-[12.5px] text-fg">{apiKey.id}</span>
            <CopyIconButton label="Copy internal ID" value={apiKey.id} title="Key ID copied" />
          </SettingsRow>
          <SettingsRow label="Owner portal">
            <span className="truncate font-mono text-[12.5px] text-fg" title={portalUrl}>{portalUrl}</span>
            <CopyIconButton label="Copy portal link" value={portalUrl} title="Portal link copied" description="Share it with the key owner." />
          </SettingsRow>
        </div>
      </PanelCard>
    </div>
  );
}

// ── Models ───────────────────────────────────────────────────────────────────

type ModelRowData = { id: string; option?: ModelCatalogOption };

function ModelsTab({ apiKey, plan, plansLoading }: { apiKey: APIKey; plan?: Plan; plansLoading: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const catalog = useModelCatalog();
  const uid = useId();
  const keyModels = apiKey.allowed_models ?? [];
  const inheritedModels = plan?.allowed_models ?? [];
  const effectiveModels = keyModels.length > 0 ? keyModels : inheritedModels;
  const source = keyModels.length > 0 ? "key" : inheritedModels.length > 0 ? "plan" : "all";
  const [models, setModels] = useState<string[]>(keyModels);
  const [editing, setEditing] = useState(false);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [show, setShow] = useState<"all" | "selected">("all");
  const [pattern, setPattern] = useState("");

  useEffect(() => {
    setModels(keyModels);
  }, [apiKey.id, keyModels.join("\u0000")]);

  const update = useMutation({
    mutationFn: (next: string[]) => api.updateKey(apiKey.id, { allowed_models: next }),
    onSuccess: (_, next) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      setModels(next);
      setEditing(false);
      toast.success(next.length > 0 ? "Model access updated" : "Model override removed", next.length > 0 ? `${next.length} models allowed.` : "This key now follows its plan.");
    },
    onError: (error) => toast.error("Couldn't update model access", error instanceof Error ? error.message : "Please try again."),
  });

  const lookup = useMemo(() => {
    const map = new Map<string, ModelCatalogOption>();
    for (const m of catalog.models) if (!map.has(m.id)) map.set(m.id, m);
    return map;
  }, [catalog.models]);

  const providerOptions = useMemo(() => {
    const map = new Map<string, string>();
    catalog.models.forEach((m) => map.set(m.providerId, m.providerName));
    return Array.from(map, ([pid, pname]) => ({ id: pid, name: pname }));
  }, [catalog.models]);

  const selectedSet = useMemo(() => new Set(models), [models]);

  // Rows: read mode lists the effective allowlist; edit mode lists selected
  // patterns that aren't in the catalog first, then the whole catalog.
  const rows = useMemo<ModelRowData[]>(() => {
    const base: ModelRowData[] = editing
      ? [
          ...models.filter((m) => !lookup.has(m)).map((m) => ({ id: m })),
          ...Array.from(lookup.values()).map((option) => ({ id: option.id, option })),
        ]
      : effectiveModels.map((m) => ({ id: m, option: lookup.get(m) }));
    const q = query.trim().toLowerCase();
    return base.filter((r) => {
      if (editing && show === "selected" && !selectedSet.has(r.id)) return false;
      if (providerFilter !== "all" && r.option?.providerId !== providerFilter) return false;
      if (!q) return true;
      return (
        r.id.toLowerCase().includes(q) ||
        (r.option?.name ?? "").toLowerCase().includes(q) ||
        (r.option?.providerName ?? "").toLowerCase().includes(q)
      );
    });
  }, [editing, models, lookup, effectiveModels.join("\u0000"), query, show, providerFilter, selectedSet]);

  const { page, pages, paged, setPage, total } = useClientPagination(rows, 25);
  useEffect(() => setPage(1), [query, show, providerFilter, editing, setPage]);

  if (plansLoading) return <Skeleton className="h-72 w-full rounded-2xl" />;

  const toggleModel = (mid: string) => setModels(models.includes(mid) ? models.filter((v) => v !== mid) : [...models, mid]);
  const allRowsSelected = rows.length > 0 && rows.every((r) => selectedSet.has(r.id));
  const someRowsSelected = rows.some((r) => selectedSet.has(r.id));
  const toggleAllRows = () => {
    if (allRowsSelected) {
      const drop = new Set(rows.map((r) => r.id));
      setModels(models.filter((m) => !drop.has(m)));
    } else {
      const next = [...models];
      rows.forEach((r) => {
        if (!next.includes(r.id)) next.push(r.id);
      });
      setModels(next);
    }
  };
  const addPattern = () => {
    const t = pattern.trim();
    if (t && !models.includes(t)) setModels([...models, t]);
    setPattern("");
  };
  const startEditing = () => {
    setModels(effectiveModels);
    setQuery("");
    setShow("all");
    setProviderFilter("all");
    setEditing(true);
  };
  const cancelEditing = () => {
    setModels(keyModels);
    setQuery("");
    setShow("all");
    setProviderFilter("all");
    setEditing(false);
  };
  const dirty = JSON.stringify(models) !== JSON.stringify(keyModels);

  const sourceBadge = (
    <Badge tone={source === "key" ? "warning" : "neutral"}>
      {source === "key" ? "Key override" : source === "plan" ? `From ${plan?.name || "plan"}` : "No restriction"}
    </Badge>
  );

  return (
    <PanelCard
      icon={ICONS.model}
      title={
        <span className="inline-flex flex-wrap items-center gap-2">
          {editing ? "Edit allowed models" : source === "all" ? "All models allowed" : `${effectiveModels.length} model${effectiveModels.length === 1 ? "" : "s"} allowed`}
          {!editing && sourceBadge}
        </span>
      }
      action={!editing ? (
        <Button variant="secondary" onClick={startEditing}>
          {source === "all" ? "Restrict models" : "Edit access"}
        </Button>
      ) : undefined}
      footer={editing ? (
        <SaveBar
          status={
            models.length === 0 ? (
              <span className="text-warn">Select at least one model.</span>
            ) : (
              <span className="text-fg-muted">
                <span className="tabular-nums text-fg">{models.length}</span> selected{dirty ? " · Unsaved changes" : ""}
              </span>
            )
          }
        >
          {keyModels.length > 0 && (
            <Button variant="ghost" onClick={() => update.mutate([])} disabled={update.isPending}>Use plan defaults</Button>
          )}
          <Button variant="ghost" onClick={cancelEditing} disabled={update.isPending}>Discard</Button>
          <Button onClick={() => update.mutate(models)} disabled={update.isPending || models.length === 0 || JSON.stringify(models) === JSON.stringify(keyModels)}>
            {update.isPending ? "Saving…" : "Save access"}
          </Button>
        </SaveBar>
      ) : undefined}
    >
      {!editing && source === "all" ? (
        <div className="px-6 py-10 text-center">
          <IconTile icon={ICONS.model} size="lg" className="mx-auto mb-3" />
          <h3 className="text-[13px] font-semibold text-fg">No model restriction</h3>
          <p className="mx-auto mt-1 max-w-md text-[12.5px] text-fg-muted">Every routable model works with this key.</p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5 sm:px-5">
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter models"
                aria-label="Filter models"
                className={cn("h-8 w-full rounded-lg border border-input bg-surface pl-8 pr-3 text-[13px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500", FOCUS_RING)}
              />
            </div>
            {providerOptions.length > 1 && (
              <Select value={providerFilter} onChange={(e) => setProviderFilter(e.target.value)} aria-label="Filter by provider" className="h-8 min-h-8 w-full py-0 text-[12.5px] sm:w-44">
                <option value="all">All providers</option>
                {providerOptions.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </Select>
            )}
            {editing && (
              <div className="flex gap-1" role="radiogroup" aria-label="Show">
                {(["all", "selected"] as const).map((f) => (
                  <button
                    key={f}
                    type="button"
                    role="radio"
                    aria-checked={show === f}
                    onClick={() => setShow(f)}
                    className={cn(
                      "h-8 rounded-lg border px-2.5 text-[12.5px] font-medium",
                      FOCUS_RING,
                      show === f ? "border-accent-500/30 bg-accent-500/10 text-link" : "border-line bg-surface text-fg-muted hover:text-fg",
                    )}
                  >
                    {f === "all" ? "All" : `Selected · ${models.length}`}
                  </button>
                ))}
              </div>
            )}
            <span role="status" className="ml-auto text-[12.5px] tabular-nums text-fg-muted">
              {catalog.loading ? "Loading catalog…" : `${rows.length} shown`}
            </span>
          </div>

          {editing && (
            <div className="flex flex-wrap items-center gap-2 border-b border-line bg-subtle px-4 py-2 sm:px-5">
              <label htmlFor={`${uid}-pattern`} className="text-[12.5px] font-medium text-fg">Add pattern</label>
              <input
                id={`${uid}-pattern`}
                value={pattern}
                onChange={(e) => setPattern(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addPattern();
                  }
                }}
                placeholder="e.g. gpt-4o*"
                className={cn("h-8 w-full min-w-0 flex-1 rounded-lg border border-input bg-surface px-2.5 font-mono text-[12.5px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 sm:max-w-xs", FOCUS_RING)}
              />
              <Button variant="ghost" className="min-h-8 py-1" onClick={addPattern} disabled={!pattern.trim()}>
                <Plus aria-hidden="true" />
                Add
              </Button>
            </div>
          )}

          {catalog.loading && editing && rows.length === 0 ? (
            <div className="space-y-2 p-4" aria-busy="true" aria-label="Loading models">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : rows.length === 0 ? (
            <p className="px-6 py-10 text-center text-[13px] text-fg-muted">
              {editing && show === "selected" ? "Nothing selected yet." : "No models match."}
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[620px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    {editing && (
                      <th scope="col" className="w-10 px-4 py-2 sm:pl-5">
                        <input
                          type="checkbox"
                          aria-label="Select all shown models"
                          className="h-4 w-4 rounded border-input accent-accent-500"
                          checked={allRowsSelected}
                          ref={(el) => {
                            if (el) el.indeterminate = someRowsSelected && !allRowsSelected;
                          }}
                          onChange={toggleAllRows}
                        />
                      </th>
                    )}
                    <th scope="col" className={cn("py-2 font-medium", editing ? "px-2" : "px-4 sm:px-5")}>Model</th>
                    <th scope="col" className="px-4 py-2 font-medium">Provider</th>
                    {editing && <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Remove</span></th>}
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {paged.map((r) => {
                    const checked = selectedSet.has(r.id);
                    const isPattern = !r.option;
                    return (
                      <tr
                        key={`${r.option?.providerId ?? "custom"}:${r.id}`}
                        className={cn("transition-colors", editing && "cursor-pointer", editing && checked ? "bg-accent-500/5" : "hover:bg-hover")}
                        onClick={editing ? () => toggleModel(r.id) : undefined}
                      >
                        {editing && (
                          <td className="px-4 py-2 sm:pl-5" onClick={(e) => e.stopPropagation()}>
                            <input
                              type="checkbox"
                              checked={checked}
                              onChange={() => toggleModel(r.id)}
                              aria-label={`Allow ${r.option?.name || r.id}`}
                              className="h-4 w-4 rounded border-input accent-accent-500"
                            />
                          </td>
                        )}
                        <td className={cn("max-w-[380px] py-2", editing ? "px-2" : "px-4 sm:px-5")}>
                          <span className="block truncate font-medium text-fg" title={r.option?.name || r.id}>
                            {isPattern ? <span className="font-mono text-[12.5px]">{r.id}</span> : r.option!.name}
                          </span>
                          {!isPattern && r.option!.name !== r.id && <span className="block truncate font-mono text-[11.5px] text-fg-faint">{r.id}</span>}
                        </td>
                        <td className="px-4 py-2">
                          {r.option ? (
                            <span className="inline-flex min-w-0 items-center gap-2 text-fg-muted">
                              <ProviderLogo icon={r.option.icon} name={r.option.providerName} size={18} />
                              <span className="truncate">{r.option.providerName}</span>
                            </span>
                          ) : (
                            <span className="text-[12.5px] text-fg-muted">
                              {catalog.loading ? "Resolving…" : r.id.includes("*") ? "Wildcard pattern" : "Custom model"}
                            </span>
                          )}
                        </td>
                        {editing && (
                          <td className="px-2 py-2" onClick={(e) => e.stopPropagation()}>
                            {isPattern && (
                              <button
                                type="button"
                                onClick={() => setModels(models.filter((m) => m !== r.id))}
                                aria-label={`Remove ${r.id}`}
                                className={cn("flex h-7 w-7 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg", FOCUS_RING)}
                              >
                                <X className="h-3.5 w-3.5" aria-hidden="true" />
                              </button>
                            )}
                          </td>
                        )}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          {pages > 1 && <TablePagination page={page} pages={pages} total={total} onPage={setPage} />}
        </>
      )}
    </PanelCard>
  );
}

// ── Guardrails ───────────────────────────────────────────────────────────────

function enabledDetectors(config: GuardrailPolicyConfig | undefined) {
  if (!config) return [];
  return [
    ["PII", config.pii?.enabled],
    ["Prompt injection", config.injection?.enabled],
    ["Topics", config.topics?.enabled],
    ["Toxicity", config.toxicity?.enabled],
    ["Bias", config.bias?.enabled],
  ].filter((entry): entry is [string, true] => entry[1] === true).map(([name]) => name);
}

function GuardrailsTab({ apiKey }: { apiKey: APIKey }) {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const toast = useToast();
  const uid = useId();
  const policies = useQuery({
    queryKey: ["guardrails", "apikey"],
    queryFn: () => api.listGuardrails("apikey"),
  });
  const effective = useQuery({
    queryKey: ["guardrails", "effective", apiKey.id],
    queryFn: () => api.effectiveGuardrail({ apikey: apiKey.id }),
  });
  const existing = policies.data?.guardrails.find((policy) => policy.scope_id === apiKey.id);
  const [config, setConfig] = useState<GuardrailPolicyConfig>({});
  const [enabled, setEnabled] = useState(true);
  const [editing, setEditing] = useState(false);
  const [showMerged, setShowMerged] = useState(false);

  useEffect(() => {
    if (existing) {
      setConfig(existing.config ?? {});
      setEnabled(existing.enabled);
    } else if (!policies.isLoading) {
      setConfig({});
      setEnabled(true);
      setEditing(false);
    }
  }, [existing?.id, existing?.updated_at, policies.isLoading]);

  const dirty = existing
    ? enabled !== existing.enabled || JSON.stringify(config) !== JSON.stringify(existing.config ?? {})
    : editing && Object.keys(config).length > 0;

  const save = useMutation({
    mutationFn: () => existing
      ? api.updateGuardrail(existing.id, { enabled, config })
      : api.createGuardrail({ scope: "apikey", scope_id: apiKey.id, name: `Guardrails for ${apiKey.name}`, enabled, config }),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["guardrails"] }),
        qc.invalidateQueries({ queryKey: ["guardrails", "effective", apiKey.id] }),
      ]);
      setEditing(false);
      toast.success("Guardrails saved", enabled ? "The override is active." : "The override is saved but paused.");
    },
    onError: (error) => toast.error("Couldn't save guardrails", error instanceof Error ? error.message : "Please try again."),
  });

  const remove = useMutation({
    mutationFn: () => api.deleteGuardrail(existing!.id),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["guardrails"] }),
        qc.invalidateQueries({ queryKey: ["guardrails", "effective", apiKey.id] }),
      ]);
      setConfig({});
      setEnabled(true);
      setEditing(false);
      toast.success("Override removed", "This key now inherits upstream policies.");
    },
    onError: (error) => toast.error("Couldn't remove override", error instanceof Error ? error.message : "Please try again."),
  });

  if (policies.isLoading) {
    return (
      <div className="space-y-4" aria-busy="true" aria-label="Loading guardrails">
        <Skeleton className="h-36 w-full rounded-2xl" />
        <Skeleton className="h-40 w-full rounded-2xl" />
      </div>
    );
  }
  if (policies.isError) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <IconTile icon={ICONS.errors} size="lg" tone="bad" className="mx-auto mb-3" />
        <h2 className="text-[14px] font-semibold text-fg">Couldn't load key guardrails</h2>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">Retry before making changes.</p>
        <Button variant="secondary" className="mt-4" onClick={() => policies.refetch()}>Retry</Button>
      </div>
    );
  }

  const discard = () => {
    setConfig(existing?.config ?? {});
    setEnabled(existing?.enabled ?? true);
    setEditing(false);
  };
  const removeOverride = async () => {
    if (await confirm({ title: "Remove this per-key override?", description: "The key will inherit upstream policies (global, provider, model and chain). This cannot be undone.", confirmLabel: "Remove", tone: "danger" })) remove.mutate();
  };

  const activeEffective = enabledDetectors(effective.data?.policy);
  const statusTitle = existing ? (enabled ? "Override active" : "Override paused") : editing ? "New override" : "Inherited";
  const statusHint = existing ? "Takes priority for this key" : editing ? undefined : "Global, provider, model and chain policies apply";
  const applyId = `${uid}-apply`;
  const mergedId = `${uid}-merged`;

  return (
    <div className="space-y-4">
      <PanelCard
        icon={ICONS.guardrails}
        title="Per-key override"
        action={!editing ? (
          <Button variant="secondary" onClick={() => setEditing(true)}>{existing ? "Edit override" : "Create override"}</Button>
        ) : undefined}
        footer={editing ? (
          <SaveBar status={dirty ? <span className="text-fg">Unsaved changes</span> : <span className="text-fg-muted">No changes yet</span>}>
            {existing && (
              <Button variant="danger" onClick={removeOverride} disabled={remove.isPending}>
                <Trash2 aria-hidden="true" />
                Remove override
              </Button>
            )}
            <Button variant="ghost" onClick={discard} disabled={save.isPending}>Discard</Button>
            <Button onClick={() => save.mutate()} disabled={save.isPending || !dirty}>
              {save.isPending ? "Saving…" : "Save override"}
            </Button>
          </SaveBar>
        ) : undefined}
      >
        <div className="divide-y divide-line">
          <SettingsRow label="Status" description={statusHint}>
            <Badge tone={existing && enabled ? "success" : existing ? "warning" : "neutral"}>{statusTitle}</Badge>
          </SettingsRow>
          {editing && (
            <SettingsRow label="Apply this override" labelId={applyId} description="Paused overrides are kept but not enforced.">
              <Toggle checked={enabled} onChange={setEnabled} aria-labelledby={applyId} />
            </SettingsRow>
          )}
        </div>
      </PanelCard>

      {editing && <GuardrailEditor value={config} onChange={setConfig} compact />}

      <PanelCard icon={ICONS.checklist} title="Effective protection">
        <div className="px-4 py-3 sm:px-5">
          {effective.isLoading ? (
            <Skeleton className="h-6 w-64" />
          ) : effective.isError ? (
            <p className="text-[13px] text-bad">Couldn't load the effective policy.</p>
          ) : (
            <ul className="flex flex-wrap items-center gap-2" aria-label="Active detectors">
              {activeEffective.length > 0 ? (
                activeEffective.map((name) => (
                  <li key={name}>
                    <Badge tone="success">{name}</Badge>
                  </li>
                ))
              ) : (
                <li>
                  <Badge tone="neutral">No detectors active</Badge>
                </li>
              )}
            </ul>
          )}
        </div>
        {!effective.isLoading && !effective.isError && (
          <div className="border-t border-line">
            <button
              type="button"
              onClick={() => setShowMerged((v) => !v)}
              aria-expanded={showMerged}
              aria-controls={mergedId}
              className={cn("flex min-h-10 w-full items-center justify-between px-4 py-2.5 text-left text-[12.5px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg sm:px-5", FOCUS_RING)}
            >
              Merged configuration
              <ChevronDown className={cn("h-4 w-4 text-fg-faint transition-transform", showMerged && "rotate-180")} strokeWidth={1.75} aria-hidden="true" />
            </button>
            {showMerged && (
              <pre id={mergedId} className="max-h-72 overflow-auto border-t border-line bg-subtle px-4 py-3 font-mono text-[11.5px] leading-5 text-fg-muted sm:px-5">{JSON.stringify(effective.data?.policy ?? {}, null, 2)}</pre>
            )}
          </div>
        )}
      </PanelCard>
    </div>
  );
}
