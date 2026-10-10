import { useId, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import { useQuery, useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { ChevronDown, MoreHorizontal, Pencil, Plus, Trash2 } from "lucide-react";
import { api, type BudgetStatus, type Plan } from "../lib/api";
import { formatTokenLimit, ModelMultiSelect } from "../components/ModelSelect";
import { microsToUSD, formatTokens, formatSpendUSD } from "../lib/format";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Button, Input, Select, Skeleton, ErrorBanner, Toggle, Modal } from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";

const periodLabels: Record<string, string> = {
  daily: "day",
  weekly: "week",
  monthly: "month",
  total: "all time",
};

const periods = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "total", label: "All time" },
];

const rateLimitRules = {
  rpm: { label: "Requests / min", max: 60_000 },
  tpm: { label: "Tokens / min", max: 100_000_000 },
  concurrency: { label: "Concurrent requests", max: 1_000 },
};

function parseUSD(value: string): number {
  const n = parseFloat(value);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function parseTokens(value: string): number {
  const n = parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function parseNonNegativeInt(value: string): number {
  const n = parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function validateRateLimitInput(label: string, value: string, max: number): string | null {
  const trimmed = value.trim();
  if (trimmed === "") return null;

  const n = Number(trimmed);
  if (!Number.isFinite(n)) return `${label} must be a number.`;
  if (!Number.isInteger(n)) return `${label} must be a whole number.`;
  if (n < 0) return `${label} cannot be negative.`;
  if (n > max) return `${label} is too high. Maximum is ${max.toLocaleString()}.`;

  return null;
}

function validateRateLimits(rpmValue: string, tpmValue: string, concurrencyValue: string): string | null {
  const fieldErrors = [
    validateRateLimitInput(rateLimitRules.rpm.label, rpmValue, rateLimitRules.rpm.max),
    validateRateLimitInput(rateLimitRules.tpm.label, tpmValue, rateLimitRules.tpm.max),
    validateRateLimitInput(rateLimitRules.concurrency.label, concurrencyValue, rateLimitRules.concurrency.max),
  ].filter(Boolean);

  if (fieldErrors.length > 0) return fieldErrors[0] ?? null;

  const rpm = parseNonNegativeInt(rpmValue);
  const tpm = parseNonNegativeInt(tpmValue);
  const concurrency = parseNonNegativeInt(concurrencyValue);

  if (rpm > 0 && concurrency > rpm) {
    return "Concurrent requests cannot be higher than requests per minute.";
  }

  if (rpm > 0 && tpm > 0 && tpm < rpm) {
    return "Tokens per minute cannot be lower than requests per minute.";
  }

  return null;
}

function clampAlertPct(value: number): number {
  if (!Number.isFinite(value)) return 80;
  return Math.min(100, Math.max(1, value));
}

// ── Budget helpers ───────────────────────────────────────────────────────────

/** The binding percentage: whichever of spend / tokens is closer to its limit. */
function usedPct(b: BudgetStatus): number {
  return Math.max(b.limit_micros > 0 ? b.pct_used : 0, b.limit_tokens > 0 ? b.tokens_pct_used : 0);
}

function relativeFuture(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "now";
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  if (h >= 48) return `in ${Math.round(h / 24)}d`;
  if (h > 0) return `in ${h}h ${m}m`;
  return `in ${Math.max(1, m)}m`;
}

const scopeLabels: Record<string, string> = {
  api_key: "Key",
  project: "Project",
  tenant: "Global",
};

// Scope kinds the backend accepts on POST /budgets (store.BudgetScope).
const scopeKinds = [
  { value: "api_key", label: "API key" },
  { value: "project", label: "Project" },
  { value: "tenant", label: "Global (all traffic)" },
] as const;

type ScopeKind = (typeof scopeKinds)[number]["value"];
type ScopeFilter = "all" | ScopeKind;
type Tab = "plans" | "budgets";
type BudgetModal = { mode: "create" } | { mode: "edit"; budget: BudgetStatus } | null;

/** Display name for a budget's scope. The backend only resolves names for keys; for
 *  other scopes scope_name is just the kind, so fall back to the id / a label. */
function budgetName(b: BudgetStatus): string {
  if (b.scope_kind === "tenant") return "All traffic";
  if (b.scope_kind === "project") return b.scope_id || "Project";
  if (b.scope_name && b.scope_name !== b.scope_kind) return b.scope_name;
  return b.scope_id || scopeLabels[b.scope_kind] || b.scope_kind;
}

/** Everything that renders budget data: the Budgets tab, Overview, Keys and the key owner portal. */
function invalidateBudgets(qc: QueryClient) {
  qc.invalidateQueries({ queryKey: ["budget-status"] });
  qc.invalidateQueries({ queryKey: ["budgets"] });
  qc.invalidateQueries({ queryKey: ["key-usage"] });
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function PlansPage() {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const toast = useToast();
  const location = useLocation();
  const [params, setParams] = useSearchParams();

  // /budgets opens on the budgets tab; /plans on plans. ?tab= overrides either.
  const defaultTab: Tab = location.pathname.startsWith("/budgets") ? "budgets" : "plans";
  const tabParam = params.get("tab");
  const tab: Tab = tabParam === "plans" || tabParam === "budgets" ? tabParam : defaultTab;
  const setTab = (t: Tab) =>
    setParams((p) => {
      if (t === defaultTab) p.delete("tab");
      else p.set("tab", t);
      return p;
    }, { replace: true });

  const plans = useQuery({
    queryKey: ["plans"],
    queryFn: () => api.listPlans(),
  });

  const budgets = useQuery({
    queryKey: ["budget-status"],
    queryFn: () => api.budgetStatus(),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [budgetModal, setBudgetModal] = useState<BudgetModal>(null);

  const remove = useMutation({
    mutationFn: (id: string) => api.deletePlan(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["plans"] });
      toast.success("Plan deleted", "The plan has been removed.");
    },
    onError: (e: Error) => toast.error("Delete failed", e.message),
  });

  const planList = plans.data?.plans ?? [];
  const editingPlan = planList.find((p) => p.id === editingId);
  const budgetList = budgets.data?.budgets ?? [];

  const deletePlan = async (p: Plan) => {
    if (p.key_count > 0) {
      toast.error("Cannot delete", `This plan has ${p.key_count} key(s) assigned. Reassign them first.`);
      return;
    }
    if (
      await confirm({
        title: `Delete plan “${p.name}”?`,
        description: "The template is removed permanently. No keys use it, so no traffic is affected.",
        confirmLabel: "Delete plan",
        tone: "danger",
      })
    ) {
      remove.mutate(p.id);
    }
  };

  // One primary action per view: it follows the active tab, and steps aside
  // when the tab's empty state already offers the same action.
  const plansEmpty = !plans.isLoading && !plans.isError && planList.length === 0;
  const budgetsEmpty = !budgets.isLoading && !budgets.isError && budgetList.length === 0;
  const headerAction =
    tab === "plans"
      ? !plansEmpty && (
          <Button onClick={() => setShowCreate(true)}>
            <Plus aria-hidden="true" />
            New plan
          </Button>
        )
      : !budgetsEmpty && (
          <Button onClick={() => setBudgetModal({ mode: "create" })}>
            <Plus aria-hidden="true" />
            New budget
          </Button>
        );

  const tabs: [Tab, string, number | null][] = [
    ["plans", "Plans", plans.isLoading ? null : planList.length],
    ["budgets", "Budgets", budgets.isLoading ? null : budgetList.length],
  ];

  const onTabKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const index = tabs.findIndex(([value]) => value === tab);
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? tabs.length - 1
          : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
    const value = tabs[next][0];
    setTab(value);
    document.getElementById(`plans-tab-${value}`)?.focus();
  };

  return (
    <>
      <PageHeader title="Plans & budgets" description="Spend, rate and model limits for your API keys." action={headerAction || undefined} />

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Create plan" maxWidth="max-w-2xl">
        <PlanForm onClose={() => setShowCreate(false)} />
      </Modal>

      <Modal
        open={!!editingId}
        onClose={() => setEditingId(null)}
        title={editingPlan ? `Edit plan “${editingPlan.name}”` : "Edit plan"}
        subtitle={
          editingPlan && editingPlan.key_count > 0
            ? `Rate limits apply to its ${editingPlan.key_count} key${editingPlan.key_count === 1 ? "" : "s"} now; spend limits apply to new keys.`
            : undefined
        }
        maxWidth="max-w-2xl"
      >
        {editingPlan && <PlanForm key={editingPlan.id} plan={editingPlan} onClose={() => setEditingId(null)} />}
      </Modal>

      <Modal
        open={budgetModal !== null}
        onClose={() => setBudgetModal(null)}
        title={budgetModal?.mode === "edit" ? `Edit budget for “${budgetName(budgetModal.budget)}”` : "New budget"}
        maxWidth="max-w-xl"
      >
        {budgetModal && (
          <BudgetForm
            key={budgetModal.mode === "edit" ? budgetModal.budget.id : "new"}
            budget={budgetModal.mode === "edit" ? budgetModal.budget : undefined}
            existing={budgetList}
            onClose={() => setBudgetModal(null)}
          />
        )}
      </Modal>

      <div className="mb-5 flex gap-1 border-b border-line" role="tablist" aria-label="Plans and budgets" onKeyDown={onTabKeyDown}>
        {tabs.map(([value, label, count]) => {
          const active = tab === value;
          return (
            <button
              key={value}
              id={`plans-tab-${value}`}
              type="button"
              role="tab"
              aria-selected={active}
              aria-controls={`plans-panel-${value}`}
              tabIndex={active ? 0 : -1}
              onClick={() => setTab(value)}
              className={cn(
                "relative -mb-px inline-flex items-center gap-1.5 px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                active ? "text-fg" : "text-fg-muted hover:text-fg",
              )}
            >
              {label}
              {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{count}</span>}
              {active && <span aria-hidden="true" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
            </button>
          );
        })}
      </div>

      <div id={`plans-panel-${tab}`} role="tabpanel" aria-labelledby={`plans-tab-${tab}`}>
        {tab === "plans" && (
          <PlansPanel
            plans={planList}
            loading={plans.isLoading}
            error={plans.isError ? (plans.error instanceof Error ? plans.error.message : "") : null}
            onCreate={() => setShowCreate(true)}
            onEdit={(p) => setEditingId(p.id)}
            onDelete={deletePlan}
          />
        )}
        {tab === "budgets" && (
          <BudgetsPanel
            budgets={budgetList}
            loading={budgets.isLoading}
            error={budgets.isError ? (budgets.error instanceof Error ? budgets.error.message : "") : null}
            onCreate={() => setBudgetModal({ mode: "create" })}
            onEdit={(b) => setBudgetModal({ mode: "edit", budget: b })}
          />
        )}
      </div>
    </>
  );
}

// ── Shared bits ──────────────────────────────────────────────────────────────

function KpiCell({ label, value, hint, tone }: { label: string; value: string; hint?: string; tone?: "muted" | "warn" | "bad" }) {
  return (
    <div className="bg-surface px-4 py-3">
      <p className="text-[12px] font-medium text-fg-muted">{label}</p>
      <p
        className={cn(
          "mt-1 text-[20px] font-semibold leading-tight tracking-[-0.01em] tabular-nums",
          tone === "muted" ? "text-fg-faint" : tone === "warn" ? "text-warn" : tone === "bad" ? "text-bad" : "text-fg",
        )}
      >
        {value}
      </p>
      {hint && <p className="mt-0.5 truncate text-[12px] tabular-nums text-fg-faint">{hint}</p>}
    </div>
  );
}

function EmptyPanel({ title, body, action }: { title: string; body: string; action?: ReactNode }) {
  return (
    <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
      <h2 className="text-[14px] font-medium text-fg">{title}</h2>
      <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">{body}</p>
      {action && <div className="mt-4 flex justify-center">{action}</div>}
    </div>
  );
}

function PanelSkeleton() {
  return (
    <div className="space-y-4" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-[74px] w-full rounded-2xl" />
      <Skeleton className="h-72 w-full rounded-2xl" />
    </div>
  );
}

function RowMenu({ label, editLabel, deleteLabel, onEdit, onDelete }: { label: string; editLabel: string; deleteLabel: string; onEdit: () => void; onDelete: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={label}
        className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        <MoreHorizontal className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onEdit}>
          <Pencil aria-hidden="true" />
          {editLabel}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem tone="danger" onSelect={onDelete}>
          <Trash2 aria-hidden="true" />
          {deleteLabel}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const thClass = "px-4 py-2 font-medium";

// ── Plans tab ────────────────────────────────────────────────────────────────

function PlansPanel({
  plans,
  loading,
  error,
  onCreate,
  onEdit,
  onDelete,
}: {
  plans: Plan[];
  loading: boolean;
  error: string | null;
  onCreate: () => void;
  onEdit: (p: Plan) => void;
  onDelete: (p: Plan) => void;
}) {
  if (loading) return <PanelSkeleton />;
  if (error !== null) return <ErrorBanner message={`Couldn't load plans. ${error}`.trim()} />;
  if (plans.length === 0) {
    return (
      <EmptyPanel
        title="No plans yet"
        body="A plan gives new keys the same budget, rate limits and models in one click."
        action={
          <Button onClick={onCreate}>
            <Plus aria-hidden="true" />
            Create first plan
          </Button>
        }
      />
    );
  }

  const totalKeys = plans.reduce((sum, p) => sum + p.key_count, 0);
  const enforcedCount = plans.filter((p) => p.hard_cutoff).length;
  const unusedCount = plans.filter((p) => p.key_count === 0).length;

  return (
    <>
      <section aria-label="Plan summary" className="mb-5 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="grid grid-cols-1 gap-px bg-line sm:grid-cols-3">
          <KpiCell label="Keys assigned" value={totalKeys.toLocaleString()} />
          <KpiCell label="Hard cutoff" value={enforcedCount.toLocaleString()} hint={`of ${plans.length} plans`} />
          <KpiCell label="Unused plans" value={unusedCount.toLocaleString()} hint={`of ${plans.length}`} tone={unusedCount === 0 ? "muted" : undefined} />
        </div>
      </section>

      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[820px] text-[13px]">
            <caption className="sr-only">Plans</caption>
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th scope="col" className={thClass}>Plan</th>
                <th scope="col" className={thClass}>Budget</th>
                <th scope="col" className={thClass}>Rate limits</th>
                <th scope="col" className={thClass}>Models</th>
                <th scope="col" className={thClass}>Enforcement</th>
                <th scope="col" className={cn(thClass, "text-right")}>Keys</th>
                <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {plans.map((p) => (
                <PlanRow key={p.id} plan={p} onEdit={() => onEdit(p)} onDelete={() => onDelete(p)} />
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}

function PlanRow({ plan: p, onEdit, onDelete }: { plan: Plan; onEdit: () => void; onDelete: () => void }) {
  const models = p.allowed_models ?? [];
  const period = periodLabels[p.period] ?? p.period;
  const stop = (e: React.MouseEvent) => e.stopPropagation();
  const budgetParts = [p.limit_micros > 0 ? microsToUSD(p.limit_micros) : "", p.limit_tokens > 0 ? `${formatTokens(p.limit_tokens)} tokens` : ""].filter(Boolean);
  const rateParts = [
    p.rpm_limit > 0 ? `${p.rpm_limit.toLocaleString()} rpm` : "",
    p.tpm_limit > 0 ? `${formatTokens(p.tpm_limit)} tpm` : "",
    p.concurrency_limit > 0 ? `${p.concurrency_limit.toLocaleString()} concurrent` : "",
  ].filter(Boolean);

  return (
    <tr className="cursor-pointer transition-colors hover:bg-hover" onClick={onEdit}>
      <td className="max-w-[260px] px-4 py-2.5">
        <button
          type="button"
          onClick={(e) => {
            stop(e);
            onEdit();
          }}
          className="block max-w-full truncate rounded text-left font-medium text-fg hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          title={p.name}
          aria-label={`Edit plan ${p.name}`}
        >
          {p.name}
        </button>
        {p.description && (
          <span className="block truncate text-[12px] text-fg-faint" title={p.description}>
            {p.description}
          </span>
        )}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5">
        {budgetParts.length > 0 ? (
          <span className="tabular-nums text-fg">{budgetParts.join(" · ")}</span>
        ) : (
          <span className="text-fg-faint">Unlimited</span>
        )}
        <span className="block text-[12px] text-fg-faint">Per {period}</span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5">
        {rateParts.length > 0 ? (
          <span className="tabular-nums text-fg-muted">{rateParts.join(" · ")}</span>
        ) : (
          <span className="text-fg-faint">None</span>
        )}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5">
        {models.length > 0 ? (
          <span className="tabular-nums text-fg" title={models.join("\n")}>
            {models.length} model{models.length === 1 ? "" : "s"}
          </span>
        ) : (
          <span className="text-fg-faint">All models</span>
        )}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5">
        <span
          className={cn(
            "inline-flex h-5 items-center rounded-md border px-1.5 text-[11.5px] font-medium",
            p.hard_cutoff ? "border-line-strong bg-surface text-fg" : "border-line bg-subtle text-fg-muted",
          )}
        >
          {p.hard_cutoff ? "Hard cutoff" : "Advisory"}
        </span>
        <span className="block text-[12px] tabular-nums text-fg-faint">Alert at {p.alert_pct}%</span>
      </td>
      <td className={cn("whitespace-nowrap px-4 py-2.5 text-right tabular-nums", p.key_count > 0 ? "text-fg" : "text-fg-faint")}>
        {p.key_count.toLocaleString()}
      </td>
      <td className="px-2 py-2.5" onClick={stop}>
        <RowMenu label={`Actions for plan ${p.name}`} editLabel="Edit plan" deleteLabel="Delete plan" onEdit={onEdit} onDelete={onDelete} />
      </td>
    </tr>
  );
}

// ── Budgets tab ──────────────────────────────────────────────────────────────

function BudgetsPanel({
  budgets,
  loading,
  error,
  onCreate,
  onEdit,
}: {
  budgets: BudgetStatus[];
  loading: boolean;
  error: string | null;
  onCreate: () => void;
  onEdit: (b: BudgetStatus) => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [scope, setScope] = useState<ScopeFilter>("all");

  const sorted = useMemo(() => [...budgets].sort((a, b) => usedPct(b) - usedPct(a)), [budgets]);

  const remove = useMutation({
    mutationFn: (b: BudgetStatus) => api.deleteBudget(b.id),
    onSuccess: (_data, b) => {
      invalidateBudgets(qc);
      toast.success("Budget deleted", `“${budgetName(b)}” is no longer tracked against this limit.`);
    },
    onError: (e: Error) => toast.error("Couldn't delete budget", e.message),
  });

  const deleteBudget = async (b: BudgetStatus) => {
    const name = budgetName(b);
    const subject =
      b.scope_kind === "api_key" ? "Requests on this key" : b.scope_kind === "project" ? "Requests in this project" : "Requests across all keys";
    const ok = await confirm({
      title: `Delete budget for “${name}”?`,
      description: b.hard_cutoff
        ? `${subject} are no longer capped by this ${periodLabels[b.period] ?? b.period} limit. Usage history is kept.`
        : `${subject} stop raising alerts for this limit. Usage history is kept.`,
      confirmLabel: "Delete budget",
      tone: "danger",
    });
    if (ok) remove.mutate(b);
  };

  if (loading) return <PanelSkeleton />;
  if (error !== null) return <ErrorBanner message={`Couldn't load budgets. ${error}`.trim()} />;
  if (budgets.length === 0) {
    return (
      <EmptyPanel
        title="No budgets yet"
        body="Cap spend or tokens for a key, a project or all traffic."
        action={
          <Button onClick={onCreate}>
            <Plus aria-hidden="true" />
            New budget
          </Button>
        }
      />
    );
  }

  const exhausted = budgets.filter((b) => usedPct(b) >= 100).length;
  const nearLimit = budgets.filter((b) => {
    const pct = usedPct(b);
    return pct < 100 && pct >= b.alert_pct;
  }).length;
  const blocking = budgets.filter((b) => b.hard_cutoff && usedPct(b) >= 100).length;

  const scopeCounts: Record<ScopeFilter, number> = {
    all: budgets.length,
    api_key: budgets.filter((b) => b.scope_kind === "api_key").length,
    project: budgets.filter((b) => b.scope_kind === "project").length,
    tenant: budgets.filter((b) => b.scope_kind === "tenant").length,
  };
  const scopeOptions = (["all", "api_key", "project", "tenant"] as ScopeFilter[]).filter((s) => s === "all" || scopeCounts[s] > 0);
  const visible = scope === "all" ? sorted : sorted.filter((b) => b.scope_kind === scope);

  return (
    <>
      <section aria-label="Budget summary" className="mb-5 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="grid grid-cols-1 gap-px bg-line sm:grid-cols-3">
          <KpiCell label="Past alert threshold" value={nearLimit.toLocaleString()} hint={`of ${budgets.length}`} tone={nearLimit > 0 ? "warn" : "muted"} />
          <KpiCell label="Exhausted" value={exhausted.toLocaleString()} tone={exhausted > 0 ? "bad" : "muted"} />
          <KpiCell label="Blocking requests" value={blocking.toLocaleString()} tone={blocking > 0 ? "bad" : "muted"} />
        </div>
      </section>

      {scopeOptions.length > 2 && (
        <div className="mb-3 flex flex-wrap gap-1.5" role="radiogroup" aria-label="Filter by scope">
          {scopeOptions.map((s) => {
            const active = scope === s;
            return (
              <button
                key={s}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setScope(s)}
                className={cn(
                  "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                  active ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                )}
              >
                {s === "all" ? "All" : scopeLabels[s]}
                <span className={cn("tabular-nums", !active && "text-fg-faint")}>{scopeCounts[s]}</span>
              </button>
            );
          })}
        </div>
      )}

      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[820px] text-[13px]">
            <caption className="sr-only">Budgets, closest to their limit first</caption>
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th scope="col" className={thClass}>Scope</th>
                <th scope="col" className={cn(thClass, "w-[34%]")}>Usage</th>
                <th scope="col" className={cn(thClass, "text-right")}>Used</th>
                <th scope="col" className={thClass}>Period</th>
                <th scope="col" className={thClass}>Enforcement</th>
                <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {visible.map((b) => (
                <BudgetRow key={b.id} budget={b} onEdit={() => onEdit(b)} onDelete={() => deleteBudget(b)} />
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}

function BudgetRow({ budget: b, onEdit, onDelete }: { budget: BudgetStatus; onEdit: () => void; onDelete: () => void }) {
  const pct = usedPct(b);
  const over = pct >= 100;
  const warn = !over && pct >= b.alert_pct;
  const fill = over ? "bg-bad" : warn ? "bg-warn" : "bg-accent-500";
  const parts: string[] = [];
  if (b.limit_micros > 0) parts.push(`${formatSpendUSD(b.spent_micros / 1e6)} of ${microsToUSD(b.limit_micros)}`);
  if (b.limit_tokens > 0) parts.push(`${formatTokens(b.spent_tokens)} of ${formatTokens(b.limit_tokens)} tokens`);
  const period = periodLabels[b.period] ?? b.period;
  const name = budgetName(b);

  return (
    <tr className="transition-colors hover:bg-hover">
      <td className="max-w-[240px] px-4 py-2.5">
        {b.scope_kind === "api_key" && b.scope_id ? (
          <Link to={`/keys/${b.scope_id}`} className="block truncate font-medium text-fg hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500" title={name}>
            {name}
          </Link>
        ) : (
          <span className={cn("block truncate font-medium text-fg", b.scope_kind === "project" && "font-mono text-[12.5px]")} title={name}>
            {name}
          </span>
        )}
        <span className="block text-[12px] text-fg-faint">{scopeLabels[b.scope_kind] ?? b.scope_kind}</span>
      </td>
      <td className="px-4 py-2.5">
        <div
          className="h-1.5 overflow-hidden rounded-full bg-track"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(Math.min(100, pct))}
          aria-label={`${name} budget used`}
        >
          <div className={cn("h-full rounded-full", fill)} style={{ width: `${Math.min(100, pct)}%` }} />
        </div>
        <p className="mt-1 truncate text-[12px] tabular-nums text-fg-muted">{parts.join(" · ") || "No limit"}</p>
      </td>
      <td className={cn("whitespace-nowrap px-4 py-2.5 text-right font-medium tabular-nums", over ? "text-bad" : warn ? "text-warn" : "text-fg")}>
        {Math.round(pct)}%
        <span className="block text-[12px] font-normal text-fg-faint">Alert at {b.alert_pct}%</span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5" title={b.resets_at ? new Date(b.resets_at).toLocaleString() : undefined}>
        <span className="text-fg-muted">{b.period === "total" ? "All time" : `Per ${period}`}</span>
        <span className="block text-[12px] tabular-nums text-fg-faint">{b.resets_at ? `Resets ${relativeFuture(b.resets_at)}` : "Never resets"}</span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5">
        {b.hard_cutoff ? (
          <span className={cn("text-[12.5px]", over ? "font-medium text-bad" : "text-fg")}>{over ? "Blocking requests" : "Hard cutoff"}</span>
        ) : (
          <span className="text-[12.5px] text-fg-muted">Advisory</span>
        )}
      </td>
      <td className="px-2 py-2.5">
        <RowMenu label={`Actions for ${name} budget`} editLabel="Edit budget" deleteLabel="Delete budget" onEdit={onEdit} onDelete={onDelete} />
      </td>
    </tr>
  );
}

// ── Form primitives ──────────────────────────────────────────────────────────

type FieldA11y = { id: string; "aria-describedby"?: string; "aria-invalid"?: true };

/** Label + control + one line of hint or error, wired up with ids for assistive tech. */
function FormField({
  label,
  hint,
  error,
  marker,
  children,
}: {
  label: string;
  hint?: ReactNode;
  error?: string | null;
  marker?: "Optional" | "Required";
  children: (a11y: FieldA11y) => ReactNode;
}) {
  const id = useId();
  const messageId = `${id}-message`;
  const hasMessage = !!error || !!hint;
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <label htmlFor={id} className="text-[12.5px] font-medium text-fg">
          {label}
        </label>
        {marker && <span className="text-[12px] text-fg-faint">{marker}</span>}
      </div>
      {children({ id, "aria-describedby": hasMessage ? messageId : undefined, "aria-invalid": error ? true : undefined })}
      {error ? (
        <p id={messageId} className="text-[12px] leading-5 text-bad">
          {error}
        </p>
      ) : (
        hint && (
          <p id={messageId} className="text-[12px] leading-5 text-fg-muted">
            {hint}
          </p>
        )
      )}
    </div>
  );
}

function FormSection({ title, id, children }: { title: string; id?: string; children: ReactNode }) {
  return (
    <section className="space-y-3 px-5 py-4" aria-labelledby={id}>
      <h3 id={id} className="text-[13px] font-semibold text-fg">
        {title}
      </h3>
      {children}
    </section>
  );
}

/** Token count input that keeps the thousand separators visible while typing. */
function TokenInput({
  value,
  onChange,
  placeholder,
  ...a11y
}: { value: string; onChange: (v: string) => void; placeholder?: string } & Partial<FieldA11y>) {
  return (
    <Input
      {...a11y}
      type="text"
      inputMode="numeric"
      value={formatTokenLimit(value)}
      onChange={(e) => onChange(e.target.value.replace(/[^\d]/g, ""))}
      placeholder={placeholder}
      className="tabular-nums"
    />
  );
}

function USDInput({ value, onChange, ...a11y }: { value: string; onChange: (v: string) => void } & FieldA11y) {
  return (
    <div className="relative">
      <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-[13px] text-fg-faint">
        $
      </span>
      <Input
        {...a11y}
        type="number"
        min="0"
        step="0.01"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Unlimited"
        className="pl-7 tabular-nums"
      />
    </div>
  );
}

function PercentInput({ value, onChange, ...a11y }: { value: number; onChange: (v: number) => void } & FieldA11y) {
  return (
    <div className="relative">
      <Input
        {...a11y}
        type="number"
        min="1"
        max="100"
        value={value}
        onChange={(e) => onChange(clampAlertPct(parseInt(e.target.value, 10)))}
        className="pr-8 tabular-nums"
      />
      <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-3 text-[13px] text-fg-faint">
        %
      </span>
    </div>
  );
}

function PeriodSelect({ value, onChange, ...a11y }: { value: string; onChange: (v: string) => void } & FieldA11y) {
  return (
    <Select {...a11y} value={value} onChange={(e) => onChange(e.target.value)}>
      {periods.map((p) => (
        <option key={p.value} value={p.value}>
          {p.label}
        </option>
      ))}
    </Select>
  );
}

/** Settings-style row: label + one-line consequence on the left, switch on the right. */
function HardCutoffRow({ checked, onChange, noun }: { checked: boolean; onChange: (v: boolean) => void; noun: string }) {
  const id = useId();
  return (
    <div className="flex items-center justify-between gap-4 rounded-lg border border-line px-3 py-2.5">
      <div className="min-w-0">
        <p id={`${id}-label`} className="text-[12.5px] font-medium text-fg">
          Hard cutoff
        </p>
        <p id={`${id}-desc`} className="mt-0.5 text-[12px] leading-5 text-fg-muted">
          {checked ? `Reject requests once ${noun} reaches the limit.` : "Track and alert only. Requests are never blocked."}
        </p>
      </div>
      <Toggle checked={checked} onChange={onChange} aria-labelledby={`${id}-label`} aria-describedby={`${id}-desc`} />
    </div>
  );
}

function FormFooter({ onClose, pending, submitLabel, status }: { onClose: () => void; pending: boolean; submitLabel: string; status?: ReactNode }) {
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 rounded-b-2xl border-t border-line bg-subtle px-5 py-3">
      <span role="status" className="text-[12px] text-fg-muted">
        {status}
      </span>
      <div className="ml-auto flex items-center gap-2">
        <Button variant="ghost" type="button" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? "Saving…" : submitLabel}
        </Button>
      </div>
    </div>
  );
}

// ── Plan form (create / edit) ────────────────────────────────────────────────

function PlanForm({ plan, onClose }: { plan?: Plan; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const ids = useId();

  const isEdit = !!plan;

  const [name, setName] = useState(plan?.name ?? "");
  const [description, setDescription] = useState(plan?.description ?? "");
  const [limit, setLimit] = useState(
    plan && plan.limit_micros > 0 ? (plan.limit_micros / 1_000_000).toFixed(2) : ""
  );
  const [limitTokens, setLimitTokens] = useState(
    plan && plan.limit_tokens > 0 ? plan.limit_tokens.toString() : ""
  );
  const [rpmLimit, setRpmLimit] = useState(
    plan && plan.rpm_limit > 0 ? plan.rpm_limit.toString() : ""
  );
  const [tpmLimit, setTpmLimit] = useState(
    plan && plan.tpm_limit > 0 ? plan.tpm_limit.toString() : ""
  );
  const [concurrencyLimit, setConcurrencyLimit] = useState(
    plan && plan.concurrency_limit > 0 ? plan.concurrency_limit.toString() : ""
  );
  const [period, setPeriod] = useState(plan?.period ?? "monthly");
  const [alertPct, setAlertPct] = useState(plan?.alert_pct ?? 80);
  const [hardCutoff, setHardCutoff] = useState(plan?.hard_cutoff ?? true);
  const [allowedModels, setAllowedModels] = useState<string[]>(plan?.allowed_models ?? []);
  const [error, setError] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(
    !!plan && (plan.rpm_limit > 0 || plan.tpm_limit > 0 || plan.concurrency_limit > 0),
  );

  const usdLimit = parseUSD(limit);
  const tokenLimit = parseTokens(limitTokens);
  const rpm = parseNonNegativeInt(rpmLimit);
  const tpm = parseNonNegativeInt(tpmLimit);
  const concurrency = parseNonNegativeInt(concurrencyLimit);
  const validationError = validateRateLimits(rpmLimit, tpmLimit, concurrencyLimit);
  const canSubmit = name.trim().length > 0 && !validationError;

  // Per-field messages shown inline; cross-field rules surface under the group.
  const rpmError = validateRateLimitInput(rateLimitRules.rpm.label, rpmLimit, rateLimitRules.rpm.max);
  const tpmError = validateRateLimitInput(rateLimitRules.tpm.label, tpmLimit, rateLimitRules.tpm.max);
  const concurrencyError = validateRateLimitInput(rateLimitRules.concurrency.label, concurrencyLimit, rateLimitRules.concurrency.max);
  const crossFieldError = validationError && !rpmError && !tpmError && !concurrencyError ? validationError : null;
  const nameError = submitted && !name.trim() ? "Enter a plan name." : null;
  // Never hide an invalid rate limit behind the collapsed section.
  const showAdvanced = advancedOpen || !!validationError;
  const rateSummary = [rpm > 0 && `${rpm.toLocaleString()} rpm`, tpm > 0 && `${formatTokens(tpm)} tpm`, concurrency > 0 && `${concurrency} concurrent`]
    .filter(Boolean)
    .join(" · ");

  const create = useMutation({
    mutationFn: () =>
      api.createPlan({
        name: name.trim(),
        description: description.trim() || undefined,
        limit_usd: usdLimit > 0 ? usdLimit : undefined,
        limit_tokens: tokenLimit > 0 ? tokenLimit : undefined,
        rpm_limit: rpm,
        tpm_limit: tpm,
        concurrency_limit: concurrency,
        period,
        alert_pct: alertPct,
        hard_cutoff: hardCutoff,
        allowed_models: allowedModels.length > 0 ? allowedModels : undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["plans"] });
      toast.success("Plan created", `"${name}" is ready to assign to API keys.`);
      onClose();
    },
    onError: (e: Error) => {
      setError(e.message);
      toast.error("Plan creation failed", e.message);
    },
  });

  const update = useMutation({
    mutationFn: () =>
      api.updatePlan(plan!.id, {
        name: name.trim(),
        description: description.trim() || undefined,
        limit_usd: usdLimit > 0 ? usdLimit : undefined,
        limit_tokens: tokenLimit > 0 ? tokenLimit : undefined,
        rpm_limit: rpm,
        tpm_limit: tpm,
        concurrency_limit: concurrency,
        period,
        alert_pct: alertPct,
        hard_cutoff: hardCutoff,
        allowed_models: allowedModels.length > 0 ? allowedModels : [],
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["plans"] });
      toast.success("Plan updated", `"${name}" has been updated.`);
      onClose();
    },
    onError: (e: Error) => {
      setError(e.message);
      toast.error("Plan update failed", e.message);
    },
  });

  const isPending = create.isPending || update.isPending;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setSubmitted(true);
    if (validationError) {
      setError(validationError);
      return;
    }
    if (!canSubmit) return;
    if (isEdit) {
      update.mutate();
    } else {
      create.mutate();
    }
  };

  const periodNoun = periodLabels[period] ?? period;

  return (
    <form className="flex max-h-[calc(100vh-10rem)] flex-col" onSubmit={handleSubmit} noValidate>
      <div className="min-h-0 divide-y divide-line overflow-y-auto">
        <FormSection title="Details">
          <div className="grid gap-3 sm:grid-cols-2">
            <FormField label="Name" marker="Required" error={nameError}>
              {(a11y) => (
                <Input {...a11y} aria-required="true" value={name} onChange={(e) => setName(e.target.value)} placeholder="Pro" autoFocus />
              )}
            </FormField>
            <FormField label="Description" marker="Optional">
              {(a11y) => (
                <Input {...a11y} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Full access, $100 a month" />
              )}
            </FormField>
          </div>
        </FormSection>

        <FormSection title="Budget per key">
          <div className="grid gap-3 sm:grid-cols-3">
            <FormField label="Spend limit (USD)" hint={`Per ${periodNoun}`}>
              {(a11y) => <USDInput {...a11y} value={limit} onChange={setLimit} />}
            </FormField>
            <FormField label="Token limit" hint={`Per ${periodNoun}`}>
              {(a11y) => <TokenInput {...a11y} value={limitTokens} onChange={setLimitTokens} placeholder="Unlimited" />}
            </FormField>
            <FormField label="Period">
              {(a11y) => <PeriodSelect {...a11y} value={period} onChange={setPeriod} />}
            </FormField>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <FormField label="Alert at">
              {(a11y) => <PercentInput {...a11y} value={alertPct} onChange={setAlertPct} />}
            </FormField>
            <div className="sm:col-span-2 sm:self-end">
              <HardCutoffRow checked={hardCutoff} onChange={setHardCutoff} noun="a key" />
            </div>
          </div>
        </FormSection>

        <FormSection title="Models" id={`${ids}-models`}>
          <div role="group" aria-labelledby={`${ids}-models`} aria-describedby={`${ids}-models-hint`} className="space-y-1.5">
            <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} aria-labelledby={`${ids}-models`} aria-describedby={`${ids}-models-hint`} />
            <p id={`${ids}-models-hint`} className="text-[12px] leading-5 text-fg-muted">
              Empty allows every model. <span className="font-mono">*</span> wildcards work, e.g. <span className="font-mono">claude-*</span>.
            </p>
          </div>
        </FormSection>

        <section className="px-5 py-4">
          <h3>
            <button
              type="button"
              aria-expanded={showAdvanced}
              aria-controls={`${ids}-advanced`}
              onClick={() => setAdvancedOpen((v) => !v)}
              className="flex w-full items-center justify-between gap-3 rounded-lg text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <span className="text-[13px] font-semibold text-fg">Advanced: rate limits</span>
              <span className="flex items-center gap-2 text-[12px] font-normal tabular-nums text-fg-muted">
                {!showAdvanced && (rateSummary || "None")}
                <ChevronDown className={cn("h-4 w-4 text-fg-faint transition-transform", showAdvanced && "rotate-180")} strokeWidth={1.75} aria-hidden="true" />
              </span>
            </button>
          </h3>
          {showAdvanced && (
            <div id={`${ids}-advanced`} className="mt-3 space-y-3">
              <div className="grid gap-3 sm:grid-cols-3">
                <FormField label="Requests / min" error={rpmError}>
                  {(a11y) => (
                    <Input
                      {...a11y}
                      type="number"
                      min="0"
                      max={rateLimitRules.rpm.max}
                      step="1"
                      value={rpmLimit}
                      onChange={(e) => setRpmLimit(e.target.value)}
                      placeholder="Unlimited"
                      className="tabular-nums"
                    />
                  )}
                </FormField>
                <FormField label="Tokens / min" error={tpmError}>
                  {(a11y) => <TokenInput {...a11y} value={tpmLimit} onChange={setTpmLimit} placeholder="Unlimited" />}
                </FormField>
                <FormField label="Concurrent requests" error={concurrencyError}>
                  {(a11y) => (
                    <Input
                      {...a11y}
                      type="number"
                      min="0"
                      max={rateLimitRules.concurrency.max}
                      step="1"
                      value={concurrencyLimit}
                      onChange={(e) => setConcurrencyLimit(e.target.value)}
                      placeholder="Unlimited"
                      className="tabular-nums"
                    />
                  )}
                </FormField>
              </div>
              {crossFieldError && <ErrorBanner message={crossFieldError} />}
            </div>
          )}
        </section>

        {error && error !== validationError && (
          <div className="px-5 py-4">
            <ErrorBanner message={error} />
          </div>
        )}
      </div>

      <FormFooter onClose={onClose} pending={isPending} submitLabel={isEdit ? "Save changes" : "Create plan"} />
    </form>
  );
}

// ── Budget form (create / edit) ──────────────────────────────────────────────

function BudgetForm({ budget, existing, onClose }: { budget?: BudgetStatus; existing: BudgetStatus[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const isEdit = !!budget;

  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys(), enabled: !isEdit });
  const keyList = keys.data?.keys ?? [];

  const [scopeKind, setScopeKind] = useState<ScopeKind>((budget?.scope_kind as ScopeKind) ?? "api_key");
  const [scopeId, setScopeId] = useState(budget?.scope_id ?? "");
  const [limit, setLimit] = useState(budget && budget.limit_micros > 0 ? (budget.limit_micros / 1_000_000).toFixed(2) : "");
  const [limitTokens, setLimitTokens] = useState(budget && budget.limit_tokens > 0 ? budget.limit_tokens.toString() : "");
  const [period, setPeriod] = useState(budget?.period ?? "monthly");
  const [alertPct, setAlertPct] = useState(budget?.alert_pct ?? 80);
  // Matches the backend default for new budgets.
  const [hardCutoff, setHardCutoff] = useState(budget?.hard_cutoff ?? true);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState("");

  const usdLimit = parseUSD(limit);
  const tokenLimit = parseTokens(limitTokens);
  const target = scopeKind === "tenant" ? "" : scopeId.trim();

  const targetError =
    isEdit || scopeKind === "tenant" || target
      ? null
      : scopeKind === "api_key"
        ? "Choose a key."
        : "Enter a project ID.";
  const usdError = limit.trim() !== "" && usdLimit <= 0 ? "Enter an amount above 0, or leave blank." : null;
  const limitError = usdLimit <= 0 && tokenLimit <= 0 ? "Set a spend limit, a token limit or both." : null;
  const showTarget = submitted ? targetError : null;
  const showUsd = submitted ? usdError ?? limitError : null;
  const showTokens = submitted && !usdError ? limitError : null;
  const valid = !targetError && !usdError && !limitError;

  // Not an error — two budgets on one scope both apply — but worth saying.
  const duplicate = existing.some(
    (b) => b.id !== budget?.id && b.scope_kind === scopeKind && (scopeKind === "tenant" || b.scope_id === target) && b.period === period,
  );
  const periodNoun = periodLabels[period] ?? period;
  const noun = scopeKind === "api_key" ? "the key" : scopeKind === "project" ? "the project" : "total traffic";

  const onSuccess = (title: string) => {
    invalidateBudgets(qc);
    toast.success(title, budget ? `“${budgetName(budget)}” now uses the new limits.` : "Usage is tracked against it from now on.");
    onClose();
  };
  const onError = (title: string) => (e: Error) => {
    setError(e.message);
    toast.error(title, e.message);
  };

  const create = useMutation({
    mutationFn: () =>
      api.createBudget({
        scope_kind: scopeKind,
        scope_id: scopeKind === "tenant" ? undefined : target,
        limit_usd: usdLimit > 0 ? usdLimit : undefined,
        limit_tokens: tokenLimit > 0 ? tokenLimit : undefined,
        period,
        alert_pct: alertPct,
        hard_cutoff: hardCutoff,
      }),
    onSuccess: () => onSuccess("Budget created"),
    onError: onError("Couldn't create budget"),
  });

  const update = useMutation({
    // Send both limits so clearing one (0) is persisted.
    mutationFn: () =>
      api.updateBudget(budget!.id, {
        limit_usd: usdLimit,
        limit_tokens: tokenLimit,
        period,
        alert_pct: alertPct,
        hard_cutoff: hardCutoff,
      }),
    onSuccess: () => onSuccess("Budget updated"),
    onError: onError("Couldn't update budget"),
  });

  const pending = create.isPending || update.isPending;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setSubmitted(true);
    if (!valid) return;
    if (isEdit) update.mutate();
    else create.mutate();
  };

  return (
    <form className="flex max-h-[calc(100vh-10rem)] flex-col" onSubmit={handleSubmit} noValidate>
      <div className="min-h-0 divide-y divide-line overflow-y-auto">
        <FormSection title="Applies to">
          {budget ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-lg bg-subtle px-3 py-2.5 text-[13px]">
              <dt className="text-fg-muted">Scope</dt>
              <dd className="text-fg">{scopeKinds.find((s) => s.value === budget.scope_kind)?.label ?? budget.scope_kind}</dd>
              {budget.scope_kind !== "tenant" && (
                <>
                  <dt className="text-fg-muted">{budget.scope_kind === "api_key" ? "Key" : "Project"}</dt>
                  <dd className={cn("min-w-0 truncate text-fg", budget.scope_kind === "project" && "font-mono text-[12.5px]")}>{budgetName(budget)}</dd>
                </>
              )}
            </dl>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2">
              <FormField label="Scope">
                {(a11y) => (
                  <Select
                    {...a11y}
                    value={scopeKind}
                    onChange={(e) => {
                      setScopeKind(e.target.value as ScopeKind);
                      setScopeId("");
                    }}
                  >
                    {scopeKinds.map((s) => (
                      <option key={s.value} value={s.value}>
                        {s.label}
                      </option>
                    ))}
                  </Select>
                )}
              </FormField>
              {scopeKind === "api_key" && (
                <FormField
                  label="Key"
                  marker="Required"
                  error={showTarget ?? (keys.isError ? "Couldn't load keys. Close and try again." : null)}
                  hint={!keys.isLoading && keyList.length === 0 ? "Create an API key first." : duplicate ? `This key already has a ${periodNoun} budget. Both apply.` : undefined}
                >
                  {(a11y) => (
                    <Select {...a11y} aria-required="true" value={scopeId} onChange={(e) => setScopeId(e.target.value)} disabled={keys.isLoading}>
                      <option value="">{keys.isLoading ? "Loading keys…" : "Choose a key"}</option>
                      {keyList.map((k) => (
                        <option key={k.id} value={k.id}>
                          {k.name}
                          {k.disabled ? " (disabled)" : ""}
                        </option>
                      ))}
                    </Select>
                  )}
                </FormField>
              )}
              {scopeKind === "project" && (
                <FormField label="Project ID" marker="Required" error={showTarget} hint={duplicate ? `This project already has a ${periodNoun} budget.` : undefined}>
                  {(a11y) => (
                    <Input {...a11y} aria-required="true" value={scopeId} onChange={(e) => setScopeId(e.target.value)} placeholder="proj_…" className="font-mono" spellCheck={false} autoComplete="off" />
                  )}
                </FormField>
              )}
              {scopeKind === "tenant" && (
                <p className="self-end pb-2 text-[12px] leading-5 text-fg-muted">
                  {duplicate ? `A global ${periodNoun} budget already exists. Both apply.` : "Counts every request through the gateway."}
                </p>
              )}
            </div>
          )}
        </FormSection>

        <FormSection title="Limit">
          <div className="grid gap-3 sm:grid-cols-3">
            <FormField label="Spend limit (USD)" error={showUsd}>
              {(a11y) => <USDInput {...a11y} value={limit} onChange={setLimit} />}
            </FormField>
            <FormField label="Token limit" error={showTokens}>
              {(a11y) => <TokenInput {...a11y} value={limitTokens} onChange={setLimitTokens} placeholder="Unlimited" />}
            </FormField>
            <FormField label="Period" hint={period === "total" ? "Never resets" : undefined}>
              {(a11y) => <PeriodSelect {...a11y} value={period} onChange={setPeriod} />}
            </FormField>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <FormField label="Alert at">
              {(a11y) => <PercentInput {...a11y} value={alertPct} onChange={setAlertPct} />}
            </FormField>
            <div className="sm:col-span-2 sm:self-end">
              <HardCutoffRow checked={hardCutoff} onChange={setHardCutoff} noun={noun} />
            </div>
          </div>
        </FormSection>

        {error && (
          <div className="px-5 py-4">
            <ErrorBanner message={error} />
          </div>
        )}
      </div>

      <FormFooter
        onClose={onClose}
        pending={pending}
        submitLabel={isEdit ? "Save changes" : "Create budget"}
        status={submitted && !valid ? "Fix the highlighted fields." : undefined}
      />
    </form>
  );
}
