import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Check,
  ChevronDown,
  Copy,
  ExternalLink,
  Link2,
  MoreHorizontal,
  Plus,
  Search,
  Trash2,
  X,
} from "lucide-react";
import {
  api,
  type APIKey,
  type GuardrailPolicyConfig,
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
  Select,
  Skeleton,
  TablePagination,
  Toggle,
  useClientPagination,
} from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";

type Tab = "general" | "models" | "guardrails";
const TAB_VALUES: Tab[] = ["general", "models", "guardrails"];

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
          toast.error("Copy failed", "Your browser blocked clipboard access.");
          return false;
        },
      ),
    [toast],
  );
}

function Dot() {
  return (
    <span aria-hidden="true" className="text-fg-faint">
      ·
    </span>
  );
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
      className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
    >
      {copied ? <Check className="h-3.5 w-3.5 text-ok" strokeWidth={1.75} /> : <Copy className="h-3.5 w-3.5" strokeWidth={1.75} />}
    </button>
  );
}

function SettingsRow({ label, description, children }: { label: string; description?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-8 sm:px-5">
      <div className="min-w-0 sm:max-w-[45%]">
        <p className="text-[13px] font-medium text-fg">{label}</p>
        {description && <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">{description}</p>}
      </div>
      <div className="flex min-w-0 items-center gap-2 sm:justify-end">{children}</div>
    </div>
  );
}

function PanelCard({ title, subtitle, action, children, footer }: { title: ReactNode; subtitle?: ReactNode; action?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  return (
    <section className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3 sm:px-5">
        <div className="min-w-0">
          <h2 className="text-[13px] font-semibold text-fg">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
        </div>
        {action && <div className="flex shrink-0 flex-wrap items-center gap-2">{action}</div>}
      </div>
      {children}
      {footer}
    </section>
  );
}

function SaveBar({ status, children }: { status: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-t border-line bg-subtle px-4 py-3 sm:px-5">
      <div className="min-w-0 flex-1 text-[12.5px]">{status}</div>
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
      toast.success(updated.disabled ? "Key disabled" : "Key enabled", updated.disabled ? "New requests using this key will be rejected." : "This key can authenticate requests again.");
    },
    onError: (error) => toast.error("Key update failed", error instanceof Error ? error.message : "Please try again."),
  });

  const revoke = useMutation({
    mutationFn: (keyId: string) => api.deleteKey(keyId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success("Key revoked", "The key has been permanently deleted and can no longer authenticate requests.");
      navigate("/keys");
    },
    onError: (e: Error) => toast.error("Revocation failed", e.message),
  });

  if (keys.isLoading) {
    return (
      <div className="space-y-4">
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
        <p className="text-[14px] font-medium text-fg">API key not found</p>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">It may have been revoked. Go back to API keys to pick another one.</p>
        <Button className="mt-4" onClick={() => navigate("/keys")}>Back to API keys</Button>
      </div>
    );
  }

  const portalUrl = `${window.location.origin}/portal?id=${key.id}`;
  const keyModels = key.allowed_models ?? [];
  const effectiveCount = keyModels.length > 0 ? keyModels.length : (plan?.allowed_models ?? []).length;
  const lastUsed = usedAt(key.last_used_at);

  const revokeKey = async () => {
    if (!(await confirm({ title: `Revoke ${key.name}?`, description: "Tools using this key stop authenticating immediately. This cannot be undone.", confirmLabel: "Revoke", tone: "danger" }))) return;
    revoke.mutate(key.id);
  };

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 flex items-center gap-1.5 text-[13px] text-fg-muted">
        <Link to="/keys" className="inline-flex items-center gap-1.5 rounded-md hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40">
          <ArrowLeft className="h-3.5 w-3.5" />
          API keys
        </Link>
        <span aria-hidden="true" className="text-fg-faint">/</span>
        <span className="truncate text-fg">{key.name}</span>
      </nav>

      <header className="mb-5 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="truncate text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{key.name}</h1>
            {key.disabled ? <Badge tone="neutral">Disabled</Badge> : <Badge tone="success">Active</Badge>}
          </div>
          <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
            <button
              type="button"
              onClick={() => copy(key.display, "Key identifier copied")}
              className="group inline-flex max-w-full items-center gap-1.5 rounded-md font-mono text-[12.5px] hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
              title="Copy masked key"
            >
              <span className="truncate">{key.display}</span>
              <Copy className="h-3 w-3 shrink-0 text-fg-faint" />
            </button>
            <Dot />
            <span>{key.plan_name || "Custom plan"}</span>
            <Dot />
            <span title={new Date(key.created_at).toLocaleString()}>Created {new Date(key.created_at).toLocaleDateString()}</span>
            <Dot />
            <span title={lastUsed ? new Date(lastUsed).toLocaleString() : undefined}>{lastUsed ? `Last used ${relativeTime(key.last_used_at)}` : "Never used"}</span>
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" onClick={() => copy(portalUrl, "Portal link copied", "Owner usage portal link copied.")}>
            <Link2 />
            Copy portal link
          </Button>
          <Button variant="secondary" onClick={() => toggle.mutate(key)} disabled={toggle.isPending}>
            {key.disabled ? "Enable key" : "Disable key"}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label="More key actions"
              className="flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-surface text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              <MoreHorizontal className="h-4 w-4" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => copy(key.display, "Key identifier copied")}>
                <Copy />
                Copy masked key
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => copy(key.id, "Key ID copied")}>
                <Copy />
                Copy internal ID
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => window.open(portalUrl, "_blank", "noopener,noreferrer")}>
                <ExternalLink />
                Open owner portal
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem tone="danger" onSelect={revokeKey} disabled={revoke.isPending}>
                <Trash2 />
                Revoke key
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      <div className="mb-5 flex gap-1 overflow-x-auto border-b border-line" role="tablist" aria-label={`${key.name} sections`}>
        {(
          [
            ["general", "General", null],
            ["models", "Models", effectiveCount > 0 ? effectiveCount : "All"],
            ["guardrails", "Guardrails", null],
          ] as [Tab, string, number | string | null][]
        ).map(([value, label, count]) => (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={tab === value}
            onClick={() => setTab(value)}
            className={cn(
              "relative -mb-px inline-flex items-center gap-1.5 whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
              tab === value ? "text-fg" : "text-fg-muted hover:text-fg",
            )}
          >
            {label}
            {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{count}</span>}
            {tab === value && <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
          </button>
        ))}
      </div>

      {tab === "general" && (
        <GeneralTab apiKey={key} plan={plan} portalUrl={portalUrl} onToggle={() => toggle.mutate(key)} togglePending={toggle.isPending} />
      )}
      {tab === "models" && <ModelsTab apiKey={key} plan={plan} plansLoading={plans.isLoading} />}
      {tab === "guardrails" && <GuardrailsTab apiKey={key} />}
    </>
  );
}

// ── General ──────────────────────────────────────────────────────────────────

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
  const lastUsed = usedAt(apiKey.last_used_at);
  return (
    <div className="space-y-4">
      <PanelCard title="Access" subtitle="Whether this key can authenticate, and which plan governs it.">
        <div className="divide-y divide-line">
          <SettingsRow label="Accept requests" description="Disabled keys reject every request until they are enabled again. Nothing else changes.">
            <span className={cn("inline-flex items-center gap-2", togglePending && "pointer-events-none opacity-50")}>
              <span className={cn("text-[12.5px]", apiKey.disabled ? "text-fg-faint" : "text-fg")}>{apiKey.disabled ? "Disabled" : "Active"}</span>
              <Toggle checked={!apiKey.disabled} onChange={onToggle} />
            </span>
          </SettingsRow>
          <SettingsRow
            label="Plan"
            description={plan ? planBudgetText(plan) : apiKey.plan_id ? "Budget and limits come from the assigned plan." : "No plan assigned. Limits are set on the key itself."}
          >
            <span className="truncate text-[13px] text-fg">{apiKey.plan_name || "Custom plan"}</span>
            <Link to="/plans" className="shrink-0 text-[12.5px] font-medium text-accent-500 hover:underline dark:text-accent-400">
              Manage plans
            </Link>
          </SettingsRow>
          <SettingsRow label="Last used" description="The most recent request this key authenticated.">
            <span className={cn("text-[13px]", lastUsed ? "text-fg" : "text-fg-faint")} title={lastUsed ? new Date(lastUsed).toLocaleString() : undefined}>
              {relativeTime(apiKey.last_used_at)}
            </span>
          </SettingsRow>
        </div>
      </PanelCard>

      <PanelCard title="Identity" subtitle="Identifiers for logs, support and the owner portal. The full secret is never stored.">
        <div className="divide-y divide-line">
          <SettingsRow label="Name">
            <span className="truncate text-[13px] text-fg">{apiKey.name}</span>
          </SettingsRow>
          <SettingsRow label="Key identifier" description="Masked form of the secret, as shown in usage logs.">
            <span className="truncate font-mono text-[12.5px] text-fg">{apiKey.display}</span>
            <CopyIconButton label="Copy masked key" value={apiKey.display} title="Key identifier copied" />
          </SettingsRow>
          <SettingsRow label="Internal ID">
            <span className="truncate font-mono text-[12.5px] text-fg">{apiKey.id}</span>
            <CopyIconButton label="Copy internal ID" value={apiKey.id} title="Key ID copied" />
          </SettingsRow>
          <SettingsRow label="Owner portal" description="Share with the key owner so they can track their own usage and budget.">
            <a href={portalUrl} target="_blank" rel="noopener noreferrer" className="truncate font-mono text-[12.5px] text-accent-500 hover:underline dark:text-accent-400" title={portalUrl}>
              {portalUrl}
            </a>
            <CopyIconButton label="Copy portal link" value={portalUrl} title="Portal link copied" description="Owner usage portal link copied." />
          </SettingsRow>
          <SettingsRow label="Created">
            <span className="text-[13px] tabular-nums text-fg">{new Date(apiKey.created_at).toLocaleString()}</span>
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
      toast.success(next.length > 0 ? "Model access updated" : "Model override removed", next.length > 0 ? `${next.length} models are available to this key.` : "This key now follows its plan's model access.");
    },
    onError: (error) => toast.error("Model access update failed", error instanceof Error ? error.message : "Please try again."),
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

  const sourceBadge = <Badge tone={source === "key" ? "warning" : "neutral"}>{source === "key" ? "Key override" : source === "plan" ? "Inherited from plan" : "No restriction"}</Badge>;

  return (
    <PanelCard
      title={
        <span className="inline-flex flex-wrap items-center gap-2">
          {editing ? "Edit allowed models" : source === "all" ? "All available models" : `${effectiveModels.length} model${effectiveModels.length === 1 ? "" : "s"} allowed`}
          {!editing && sourceBadge}
        </span>
      }
      subtitle={
        editing
          ? "Tick the models this key may call. The catalog comes from your connected providers; wildcard patterns are supported."
          : source === "all"
            ? "This key can use every model available through its assigned plan."
            : source === "plan"
              ? `Access follows the ${plan?.name || "assigned"} plan. Add an override only when this key needs a narrower list.`
              : "This key uses its own model allowlist instead of the plan default."
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
              <span className="text-warn">Select at least one model, or discard to keep current access.</span>
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
          <p className="text-[13px] font-medium text-fg">No model restriction</p>
          <p className="mx-auto mt-1 max-w-md text-[12.5px] text-fg-muted">Neither the key nor its plan limits models, so every routable model works. Restrict it when this key should only reach a few.</p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5 sm:px-5">
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter models"
                aria-label="Filter models"
                className="h-8 w-full rounded-lg border border-line bg-surface pl-8 pr-3 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
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
                      show === f ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:text-fg",
                    )}
                  >
                    {f === "all" ? "All" : `Selected · ${models.length}`}
                  </button>
                ))}
              </div>
            )}
            <span className="ml-auto text-[12.5px] tabular-nums text-fg-faint">
              {catalog.loading ? "Loading catalog…" : `${rows.length} shown`}
            </span>
          </div>

          {editing && (
            <div className="flex flex-wrap items-center gap-2 border-b border-line bg-subtle px-4 py-2 sm:px-5">
              <span className="text-[12.5px] text-fg-muted">Add a pattern</span>
              <input
                value={pattern}
                onChange={(e) => setPattern(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addPattern();
                  }
                }}
                placeholder="claude-*"
                aria-label="Custom model pattern"
                className="h-8 w-full min-w-0 flex-1 rounded-lg border border-line bg-surface px-2.5 font-mono text-[12.5px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25 sm:max-w-xs"
              />
              <Button variant="ghost" className="min-h-8 py-1" onClick={addPattern} disabled={!pattern.trim()}>
                <Plus />
                Add
              </Button>
              <span className="text-[12px] text-fg-faint">Use <span className="font-mono">*</span> to match many models, e.g. <span className="font-mono">gpt-4o*</span>.</span>
            </div>
          )}

          {catalog.loading && editing && rows.length === 0 ? (
            <div className="space-y-2 p-4">
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
                      <th className="w-10 px-4 py-2 sm:pl-5">
                        <input
                          type="checkbox"
                          aria-label="Select all shown models"
                          className="h-4 w-4 rounded border-line accent-[var(--color-accent-500)]"
                          checked={allRowsSelected}
                          ref={(el) => {
                            if (el) el.indeterminate = someRowsSelected && !allRowsSelected;
                          }}
                          onChange={toggleAllRows}
                        />
                      </th>
                    )}
                    <th className={cn("py-2 font-medium", editing ? "px-2" : "px-4 sm:px-5")}>Model</th>
                    <th className="px-4 py-2 font-medium">Provider</th>
                    {editing && <th className="w-10 px-2 py-2"><span className="sr-only">Remove</span></th>}
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {paged.map((r) => {
                    const checked = selectedSet.has(r.id);
                    const isPattern = !r.option;
                    return (
                      <tr
                        key={`${r.option?.providerId ?? "custom"}:${r.id}`}
                        className={cn("transition-colors", editing && "cursor-pointer", editing && checked ? "bg-accent-500/5" : "hover:bg-hover/60")}
                        onClick={editing ? () => toggleModel(r.id) : undefined}
                      >
                        {editing && (
                          <td className="px-4 py-2 sm:pl-5" onClick={(e) => e.stopPropagation()}>
                            <input
                              type="checkbox"
                              checked={checked}
                              onChange={() => toggleModel(r.id)}
                              aria-label={`Allow ${r.option?.name || r.id}`}
                              className="h-4 w-4 rounded border-line accent-[var(--color-accent-500)]"
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
                            <span className="text-[12.5px] text-fg-faint">
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
                                className="flex h-7 w-7 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg"
                              >
                                <X className="h-3.5 w-3.5" />
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
      toast.success("Key guardrails saved", enabled ? "The per-key override is active." : "The override is saved but currently paused.");
    },
    onError: (error) => toast.error("Guardrail save failed", error instanceof Error ? error.message : "Please try again."),
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
      toast.success("Override removed", "This key now inherits the upstream guardrail policy.");
    },
    onError: (error) => toast.error("Override removal failed", error instanceof Error ? error.message : "Please try again."),
  });

  if (policies.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-36 w-full rounded-2xl" />
        <Skeleton className="h-40 w-full rounded-2xl" />
      </div>
    );
  }
  if (policies.isError) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <p className="text-[14px] font-medium text-fg">Unable to load key guardrails</p>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">The existing override could not be verified. Retry before making changes.</p>
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
  const statusTitle = existing ? (enabled ? "Override active" : "Override paused") : editing ? "New override" : "Inherited policy";
  const statusHint = existing ? "Changes here take priority for this API key." : editing ? "Configure detectors, test the policy, then save." : "Global, provider, model, and chain policies continue to apply.";

  return (
    <div className="space-y-4">
      <PanelCard
        title="Per-key guardrails"
        subtitle="Add a key-specific layer only when this key needs different protection from upstream policies."
        action={!editing ? (
          <Button variant="secondary" onClick={() => setEditing(true)}>{existing ? "Edit override" : "Create override"}</Button>
        ) : undefined}
        footer={editing ? (
          <SaveBar status={dirty ? <span className="text-fg">Unsaved changes</span> : <span className="text-fg-muted">No changes yet</span>}>
            {existing && (
              <Button variant="danger" onClick={removeOverride} disabled={remove.isPending}>
                <Trash2 />
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
            <span className="inline-flex items-center gap-2 text-[13px] text-fg">
              <span className={cn("h-2 w-2 rounded-full", existing && enabled ? "bg-ok" : "bg-fg-faint")} aria-hidden="true" />
              {statusTitle}
            </span>
          </SettingsRow>
          {editing && (
            <SettingsRow label="Apply this override" description="Paused overrides are kept but not enforced; upstream policies apply instead.">
              <Toggle checked={enabled} onChange={setEnabled} />
            </SettingsRow>
          )}
        </div>
      </PanelCard>

      {editing && <GuardrailEditor value={config} onChange={setConfig} compact />}

      <PanelCard title="Effective protection" subtitle="The final policy after all applicable guardrail layers are merged.">
        <div className="px-4 py-3 sm:px-5">
          {effective.isLoading ? (
            <Skeleton className="h-6 w-64" />
          ) : effective.isError ? (
            <p className="text-[13px] text-bad">Unable to load the effective policy.</p>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              {activeEffective.length > 0 ? activeEffective.map((name) => <Badge key={name} tone="success">{name}</Badge>) : <Badge tone="neutral">No detectors active</Badge>}
              <span className="text-[12px] tabular-nums text-fg-faint">{activeEffective.length} active detector{activeEffective.length === 1 ? "" : "s"}</span>
            </div>
          )}
        </div>
        {!effective.isLoading && !effective.isError && (
          <div className="border-t border-line">
            <button
              type="button"
              onClick={() => setShowMerged((v) => !v)}
              aria-expanded={showMerged}
              className="flex w-full items-center justify-between px-4 py-2.5 text-left text-[12.5px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg sm:px-5"
            >
              Merged configuration
              <ChevronDown className={cn("h-4 w-4 text-fg-faint transition-transform", showMerged && "rotate-180")} strokeWidth={1.75} />
            </button>
            {showMerged && (
              <pre className="max-h-72 overflow-auto border-t border-line bg-subtle px-4 py-3 font-mono text-[11.5px] leading-5 text-fg-muted sm:px-5">{JSON.stringify(effective.data?.policy ?? {}, null, 2)}</pre>
            )}
          </div>
        )}
      </PanelCard>
    </div>
  );
}
