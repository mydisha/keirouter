import { useState, useCallback, useEffect, useId, useMemo, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  ChevronDown,
  Copy,
  Link2,
  MoreHorizontal,
  Plus,
  Search,
  Settings2,
  Trash2,
  X,
} from "lucide-react";
import { api, type APIKey, type CreatedKey, type Plan } from "../lib/api";
import { microsToUSD, formatTokens } from "../lib/format";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { formatTokenLimit, ModelMultiSelect } from "../components/ModelSelect";
import {
  Badge,
  Button,
  IconTile,
  Input,
  SectionTitle,
  Select,
  Skeleton,
  Toggle,
  Modal,
  ErrorBanner,
  TablePagination,
  useClientPagination,
} from "../components/ui";
import { ICONS } from "../lib/icons";
import { useConfirm } from "../components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";

const budgetPeriods = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "total", label: "All time" },
];

type StatusFilter = "all" | "active" | "inactive";
type SortKey = "created_desc" | "created_asc" | "name_asc" | "name_desc";

const STATUS_FILTERS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "active", label: "Active" },
  { value: "inactive", label: "Disabled" },
];

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

function usedAt(iso?: string | null): number | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  // Go's zero time ("0001-01-01…") parses to a negative epoch: treat as never.
  return Number.isFinite(t) && t > 0 ? t : null;
}

function getStatusCounts(keys: APIKey[] = []): Record<StatusFilter, number> {
  const disabled = keys.filter((k) => k.disabled).length;
  return { all: keys.length, active: keys.length - disabled, inactive: disabled };
}

// relativeTime renders "3 min ago" style labels for last-used timestamps.
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

function portalUrlFor(id: string) {
  return `${window.location.origin}/portal?id=${id}`;
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

function planLimitText(p: Plan): string {
  const parts: string[] = [];
  if (p.limit_micros > 0) parts.push(`${microsToUSD(p.limit_micros)} / ${p.period}`);
  if (p.limit_tokens > 0) parts.push(`${formatTokens(p.limit_tokens)} tokens / ${p.period}`);
  if (parts.length === 0) parts.push("No spend limit");
  return parts.join(" · ");
}

function planSummary(p: Plan): string {
  const models = (p.allowed_models ?? []).length;
  return [
    planLimitText(p),
    models > 0 ? `${models} model${models === 1 ? "" : "s"}` : "All models",
    p.hard_cutoff ? "Hard cutoff" : "Alerts only",
  ].join(" · ");
}

// ── Form primitives (match ConnectKit field styling) ─────────────────────────

function FormField({
  id,
  label,
  hint,
  optional,
  required,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  required?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <label htmlFor={id} className="text-[12.5px] font-medium text-fg">
          {label}
        </label>
        {optional && <span className="text-[12px] text-fg-faint">Optional</span>}
        {required && <span className="text-[12px] text-fg-faint">Required</span>}
      </div>
      {children}
      {hint && (
        <p id={`${id}-hint`} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

/** Token count input that keeps the thousand separators visible while typing. */
function TokenInput({ id, value, onChange, placeholder }: { id: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <Input
      id={id}
      type="text"
      inputMode="numeric"
      value={formatTokenLimit(value)}
      onChange={(e) => onChange(e.target.value.replace(/[^\d]/g, ""))}
      placeholder={placeholder ? (/^\d+$/.test(placeholder) ? formatTokenLimit(placeholder) : placeholder) : undefined}
      className="tabular-nums"
    />
  );
}

function SettingRow({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-3 py-2.5">
      <span id={id} className="min-w-0 text-[13px] font-medium text-fg">
        {label}
      </span>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

function DialogFooter({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center gap-2 rounded-b-2xl border-t border-line bg-subtle px-5 py-3">{children}</div>;
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function KeysPage() {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const toast = useToast();
  const copy = useCopy();
  const navigate = useNavigate();
  const fid = useId();
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys() });
  const access = useQuery({ queryKey: ["access-settings"], queryFn: () => api.accessSettings() });

  const [modalOpen, setModalOpen] = useState(false);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("created_desc");

  const allKeys = keys.data?.keys ?? [];
  const statusCounts = useMemo(() => getStatusCounts(keys.data?.keys ?? []), [keys.data]);

  const visibleKeys = useMemo(() => {
    const all = keys.data?.keys ?? [];
    return all
      .filter((k) => {
        if (statusFilter === "active") return !k.disabled;
        if (statusFilter === "inactive") return k.disabled;
        return true;
      })
      .filter((k) => {
        if (!searchQuery.trim()) return true;
        const q = searchQuery.toLowerCase();
        return (
          k.name.toLowerCase().includes(q) ||
          k.id.toLowerCase().includes(q) ||
          k.display.toLowerCase().includes(q) ||
          (k.plan_name ?? "").toLowerCase().includes(q)
        );
      })
      .sort((a, b) => {
        switch (sortKey) {
          case "name_asc":
            return a.name.localeCompare(b.name);
          case "name_desc":
            return b.name.localeCompare(a.name);
          case "created_asc":
            return new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
          case "created_desc":
          default:
            return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
        }
      });
  }, [keys.data, statusFilter, searchQuery, sortKey]);
  const pagination = useClientPagination(visibleKeys, 10);

  useEffect(() => {
    pagination.setPage(1);
  }, [statusFilter, searchQuery, sortKey]);

  // Essentials
  const [name, setName] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);

  // Advanced — limits and models
  const [budgetLimit, setBudgetLimit] = useState("");
  const [budgetLimitTokens, setBudgetLimitTokens] = useState("");
  const [budgetPeriod, setBudgetPeriod] = useState("monthly");
  const [budgetAlertPct, setBudgetAlertPct] = useState(80);
  const [budgetHardCutoff, setBudgetHardCutoff] = useState(true);
  const [allowedModels, setAllowedModels] = useState<string[]>([]);

  // Result
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [copied, setCopied] = useState(false);

  // Plan selection
  const plans = useQuery({ queryKey: ["plans"], queryFn: () => api.listPlans() });
  const [selectedPlanId, setSelectedPlanId] = useState<string>("custom");
  const [customizePlan, setCustomizePlan] = useState(false);

  const openModal = () => {
    setName("");
    setSelectedPlanId("custom");
    setCustomizePlan(false);
    setBudgetLimit("");
    setBudgetLimitTokens("");
    setBudgetPeriod("monthly");
    setBudgetAlertPct(80);
    setBudgetHardCutoff(true);
    setAllowedModels([]);
    setShowAdvanced(false);
    setCreated(null);
    setCopied(false);
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    if (created) {
      setCreated(null);
      setCopied(false);
    }
  };

  const create = useMutation({
    mutationFn: () => {
      const isCustom = selectedPlanId === "custom";
      const hasLimit = parseFloat(budgetLimit) > 0;
      const hasTokenLimit = parseInt(budgetLimitTokens) > 0;

      const opts: Record<string, unknown> = {};
      if (!isCustom && selectedPlanId) {
        opts.plan_id = selectedPlanId;
      }
      if (isCustom || customizePlan) {
        if (hasLimit) opts.budget_limit_usd = parseFloat(budgetLimit);
        if (hasTokenLimit) opts.budget_limit_tokens = parseInt(budgetLimitTokens);
        if (hasLimit || hasTokenLimit) {
          opts.budget_period = budgetPeriod;
          opts.budget_alert_pct = budgetAlertPct;
          opts.budget_hard_cutoff = budgetHardCutoff;
        }
        if (allowedModels.length > 0) opts.allowed_models = allowedModels;
      }
      return api.createKey(name, Object.keys(opts).length > 0 ? opts as any : undefined);
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      qc.invalidateQueries({ queryKey: ["budgets"] });
      qc.invalidateQueries({ queryKey: ["budget-status"] });
      qc.invalidateQueries({ queryKey: ["plans"] });
      setCreated(data);
      toast.success("Key created", "Copy it now. It won't be shown again.");
    },
    onError: (e: Error) => toast.error("Couldn't create key", e.message),
  });

  // Multi-select state
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const toggleSelect = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    if (!visibleKeys.length) return;
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (visibleKeys.every((key) => next.has(key.id))) {
        visibleKeys.forEach((key) => next.delete(key.id));
      } else {
        visibleKeys.forEach((key) => next.add(key.id));
      }
      return next;
    });
  }, [visibleKeys]);

  const clearSelection = useCallback(() => setSelectedIds(new Set()), []);

  const bulkRemove = useMutation({
    mutationFn: (ids: string[]) => api.deleteKeys(ids),
    onSuccess: (_, ids) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      clearSelection();
      toast.success(`${ids.length} key${ids.length > 1 ? "s" : ""} revoked`, "They can no longer authenticate requests.");
    },
    onError: (e: Error) => toast.error("Couldn't revoke keys", e.message),
  });

  const handleBulkDelete = async () => {
    const ids = Array.from(selectedIds);
    if (!ids.length) return;
    if (!(await confirm({ title: `Revoke ${ids.length} key${ids.length > 1 ? "s" : ""}?`, description: "Tools using them stop authenticating immediately. This cannot be undone.", confirmLabel: "Revoke", tone: "danger" }))) return;
    bulkRemove.mutate(ids);
  };

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteKey(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success("Key revoked", "It can no longer authenticate requests.");
    },
    onError: (e: Error) => toast.error("Couldn't revoke key", e.message),
  });

  const toggleDisabled = useMutation({
    mutationFn: ({ id, disabled }: { id: string; disabled: boolean }) => api.updateKey(id, { disabled }),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success(
        data.disabled ? "Key disabled" : "Key enabled",
        data.disabled ? "Requests using this key are rejected." : "This key can authenticate requests again.",
      );
    },
    onError: (e: Error) => toast.error("Couldn't update key", e.message),
  });

  const revokeOne = async (k: APIKey) => {
    if (!(await confirm({ title: `Revoke ${k.name}?`, description: "Tools using this key stop authenticating immediately. This cannot be undone.", confirmLabel: "Revoke", tone: "danger" }))) return;
    remove.mutate(k.id);
  };

  const allVisibleSelected = visibleKeys.length > 0 && visibleKeys.every((k) => selectedIds.has(k.id));
  const someVisibleSelected = visibleKeys.some((k) => selectedIds.has(k.id));
  const filtering = searchQuery.trim() !== "" || statusFilter !== "all";
  const isEmpty = !keys.isLoading && !keys.isError && allKeys.length === 0;

  // ── Create form ────────────────────────────────────────────────────────────
  const planList = plans.data?.plans ?? [];
  const isCustom = selectedPlanId === "custom";
  const selectedPlan = planList.find((p) => p.id === selectedPlanId);
  const ids = {
    name: `${fid}-name`,
    plan: `${fid}-plan`,
    advanced: `${fid}-advanced`,
    usd: `${fid}-usd`,
    tokens: `${fid}-tokens`,
    period: `${fid}-period`,
    models: `${fid}-models`,
    alert: `${fid}-alert`,
    cutoff: `${fid}-cutoff`,
    override: `${fid}-override`,
  };

  const createForm = (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (name.trim() && !create.isPending) create.mutate();
      }}
    >
      <div className="space-y-4 px-5 py-4">
        <FormField id={ids.name} label="Name" required>
          <Input
            id={ids.name}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. laptop, ci-runner, alice"
            required
            aria-required="true"
            autoFocus
          />
        </FormField>

        <FormField
          id={ids.plan}
          label="Plan"
          hint={isCustom ? "No plan. Set limits under Advanced." : selectedPlan ? planSummary(selectedPlan) : undefined}
        >
          <Select
            id={ids.plan}
            value={selectedPlanId}
            onChange={(e) => setSelectedPlanId(e.target.value)}
            disabled={plans.isLoading}
            aria-describedby={isCustom || selectedPlan ? `${ids.plan}-hint` : undefined}
          >
            <option value="custom">Custom</option>
            {plans.isLoading && <option disabled>Loading plans…</option>}
            {planList.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </Select>
        </FormField>

        <div className="rounded-lg border border-line">
          <button
            type="button"
            onClick={() => setShowAdvanced((v) => !v)}
            aria-expanded={showAdvanced}
            aria-controls={ids.advanced}
            className={cn(
              "flex min-h-10 w-full items-center justify-between gap-2 rounded-lg px-3 text-left text-[13px] font-medium text-fg transition-colors hover:bg-hover",
              FOCUS_RING,
            )}
          >
            <span>
              Advanced <span className="font-normal text-fg-muted">· limits and models</span>
            </span>
            <ChevronDown className={cn("h-4 w-4 text-fg-faint transition-transform", showAdvanced && "rotate-180")} strokeWidth={1.75} aria-hidden="true" />
          </button>

          {showAdvanced && (
            <div id={ids.advanced} className="space-y-4 border-t border-line px-3 py-3">
              {!isCustom && (
                <div className="-mx-3 -mt-3 border-b border-line">
                  <SettingRow id={ids.override} label="Override plan for this key">
                    <Toggle checked={customizePlan} onChange={setCustomizePlan} aria-labelledby={ids.override} />
                  </SettingRow>
                </div>
              )}

              {(isCustom || customizePlan) && (
                <>
                  <div className={cn("grid gap-3", isCustom ? "sm:grid-cols-[1fr_1fr_8.5rem]" : "sm:grid-cols-2")}>
                    <FormField id={ids.usd} label="Limit (USD)" optional>
                      <Input
                        id={ids.usd}
                        type="number"
                        min="0"
                        step="0.01"
                        value={budgetLimit}
                        onChange={(e) => setBudgetLimit(e.target.value)}
                        placeholder={isCustom ? "50.00" : "Plan default"}
                        className="tabular-nums"
                      />
                    </FormField>
                    <FormField id={ids.tokens} label="Limit (tokens)" optional>
                      <TokenInput id={ids.tokens} value={budgetLimitTokens} onChange={setBudgetLimitTokens} placeholder={isCustom ? "100000000" : "Plan default"} />
                    </FormField>
                    {isCustom && (
                      <FormField id={ids.period} label="Period">
                        <Select id={ids.period} value={budgetPeriod} onChange={(e) => setBudgetPeriod(e.target.value)}>
                          {budgetPeriods.map((p) => (
                            <option key={p.value} value={p.value}>{p.label}</option>
                          ))}
                        </Select>
                      </FormField>
                    )}
                  </div>

                  <div className="space-y-1.5" role="group" aria-labelledby={ids.models} aria-describedby={`${ids.models}-hint`}>
                    <div className="flex items-baseline justify-between gap-2">
                      <span id={ids.models} className="text-[12.5px] font-medium text-fg">Allowed models</span>
                      <span className="text-[12px] text-fg-faint">Optional</span>
                    </div>
                    <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} aria-labelledby={ids.models} />
                    <p id={`${ids.models}-hint`} className="text-[12px] leading-5 text-fg-muted">
                      {isCustom ? (
                        <>Empty allows every model. <span className="font-mono">*</span> matches many, e.g. <span className="font-mono">claude-*</span>.</>
                      ) : (
                        "Empty keeps the plan's models."
                      )}
                    </p>
                  </div>

                  {isCustom && (
                    <div className="divide-y divide-line rounded-lg border border-line">
                      <SettingRow id={ids.alert} label="Alert at">
                        <span className="flex items-center gap-1.5">
                          <Input
                            type="number"
                            min="1"
                            max="100"
                            value={budgetAlertPct}
                            onChange={(e) => setBudgetAlertPct(parseInt(e.target.value) || 80)}
                            className="w-20 text-right tabular-nums"
                            aria-labelledby={ids.alert}
                            aria-describedby={`${ids.alert}-unit`}
                          />
                          <span id={`${ids.alert}-unit`} className="text-[13px] text-fg-muted">% of budget</span>
                        </span>
                      </SettingRow>
                      <SettingRow id={ids.cutoff} label="Block requests when the budget is used up">
                        <Toggle checked={budgetHardCutoff} onChange={setBudgetHardCutoff} aria-labelledby={ids.cutoff} />
                      </SettingRow>
                    </div>
                  )}
                </>
              )}
            </div>
          )}
        </div>
      </div>

      <DialogFooter>
        <div className="flex-1" />
        <Button type="button" variant="ghost" onClick={closeModal}>Cancel</Button>
        <Button type="submit" disabled={!name.trim() || create.isPending}>
          {create.isPending ? "Creating…" : "Create key"}
        </Button>
      </DialogFooter>
    </form>
  );

  return (
    <>
      <PageHeader
        title="API keys"
        description="Keys your tools and teammates use to call KeiRouter."
        action={
          isEmpty ? undefined : (
            <Button onClick={openModal}>
              <Plus aria-hidden="true" />
              New key
            </Button>
          )
        }
      />

      <Modal open={modalOpen} onClose={closeModal} maxWidth="max-w-lg" title={created ? "Key created" : "Create API key"}>
        {created ? (
          <StepSuccess
            created={created}
            copied={copied}
            setCopied={setCopied}
            onClose={closeModal}
            endpointUrl={access.data?.endpoint_url ?? window.location.origin}
            planName={created.plan?.name ?? "Custom"}
            availableModelsText={
              created.allowed_models && created.allowed_models.length > 0
                ? created.allowed_models.join(", ")
                : selectedPlanId !== "custom"
                  ? (plans.data?.plans ?? []).find((p) => p.id === selectedPlanId)?.allowed_models?.join(", ") || "all models"
                  : "all models"
            }
          />
        ) : (
          createForm
        )}
      </Modal>

      {keys.isLoading ? (
        <div className="space-y-3" aria-busy="true" aria-label="Loading API keys">
          <Skeleton className="h-12 w-full rounded-2xl" />
          <Skeleton className="h-80 w-full rounded-2xl" />
        </div>
      ) : keys.isError ? (
        <div className="space-y-3">
          <ErrorBanner message={`Couldn't load API keys. ${keys.error instanceof Error ? keys.error.message : ""}`.trim()} />
          <Button variant="secondary" onClick={() => keys.refetch()}>Retry</Button>
        </div>
      ) : allKeys.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <IconTile icon={ICONS.keys} size="lg" className="mx-auto mb-3" />
          <h2 className="text-[14px] font-semibold text-fg">No API keys yet</h2>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">Create a key for each tool, app or teammate that calls KeiRouter.</p>
          <Button className="mt-4" onClick={openModal}>
            <Plus aria-hidden="true" />
            Create key
          </Button>
        </div>
      ) : (
        <section aria-labelledby={`${fid}-list`} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
          <div className="border-b border-line px-4 py-3">
            <SectionTitle
              icon={ICONS.keys}
              title="Keys"
              id={`${fid}-list`}
              action={
                <>
                  <span role="status" className="text-[12.5px] tabular-nums text-fg-muted">
                    {filtering ? `${visibleKeys.length} of ${allKeys.length}` : ""}
                  </span>
                  <label className="sr-only" htmlFor={`${fid}-sort`}>Sort keys</label>
                  <Select id={`${fid}-sort`} className="h-9 w-40" value={sortKey} onChange={(e) => setSortKey(e.target.value as SortKey)}>
                    <option value="created_desc">Newest first</option>
                    <option value="created_asc">Oldest first</option>
                    <option value="name_asc">Name A–Z</option>
                    <option value="name_desc">Name Z–A</option>
                  </Select>
                </>
              }
            />
          </div>

          <div className="flex flex-col gap-2 border-b border-line px-4 py-3 sm:flex-row sm:flex-wrap sm:items-center">
            <div className="relative sm:w-72">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                type="search"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search name, key or plan"
                aria-label="Search keys"
                className={cn(
                  "h-9 w-full rounded-lg border border-input bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500",
                  FOCUS_RING,
                )}
              />
              {searchQuery && (
                <button
                  type="button"
                  onClick={() => setSearchQuery("")}
                  className={cn("absolute right-1.5 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg", FOCUS_RING)}
                  aria-label="Clear search"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Filter by status">
              {STATUS_FILTERS.map((f) => {
                const active = statusFilter === f.value;
                return (
                  <button
                    key={f.value}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    onClick={() => setStatusFilter(f.value)}
                    className={cn(
                      "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors",
                      FOCUS_RING,
                      active ? "border-accent-500/30 bg-accent-500/10 text-link" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                    )}
                  >
                    {f.label}
                    <span className={cn("font-normal tabular-nums", active ? "text-link" : "text-fg-faint")}>{statusCounts[f.value]}</span>
                  </button>
                );
              })}
            </div>
          </div>

          <p role="status" className="sr-only">
            {selectedIds.size > 0 ? `${selectedIds.size} key${selectedIds.size === 1 ? "" : "s"} selected` : ""}
          </p>
          {selectedIds.size > 0 && (
            <div className="flex flex-wrap items-center gap-2 border-b border-line bg-subtle px-4 py-2">
              <span className="text-[13px] font-medium tabular-nums text-fg">{selectedIds.size} selected</span>
              <div className="ml-auto flex items-center gap-1.5">
                <Button variant="danger" onClick={handleBulkDelete} disabled={bulkRemove.isPending}>
                  <Trash2 aria-hidden="true" />
                  Revoke {selectedIds.size}
                </Button>
                <button
                  type="button"
                  onClick={clearSelection}
                  aria-label="Clear selection"
                  className={cn("flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg", FOCUS_RING)}
                >
                  <X className="h-4 w-4" aria-hidden="true" />
                </button>
              </div>
            </div>
          )}

          {visibleKeys.length === 0 ? (
            <div className="px-6 py-10 text-center">
              <h3 className="text-[13px] font-semibold text-fg">No keys match</h3>
              <Button
                variant="ghost"
                className="mt-3"
                onClick={() => {
                  setSearchQuery("");
                  setStatusFilter("all");
                }}
              >
                Clear filters
              </Button>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="w-10 px-4 py-2">
                      <input
                        type="checkbox"
                        checked={allVisibleSelected}
                        ref={(el) => {
                          if (el) el.indeterminate = someVisibleSelected && !allVisibleSelected;
                        }}
                        onChange={toggleSelectAll}
                        className="h-4 w-4 rounded border-input accent-accent-500"
                        aria-label={`Select all ${visibleKeys.length} shown keys`}
                      />
                    </th>
                    <th scope="col" className="px-2 py-2 font-medium">Name</th>
                    <th scope="col" className="px-4 py-2 font-medium">Plan</th>
                    <th scope="col" className="px-4 py-2 font-medium">Status</th>
                    <th scope="col" className="px-4 py-2 font-medium">Last used</th>
                    <th scope="col" className="px-4 py-2 font-medium">Created</th>
                    <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {pagination.paged.map((k) => (
                    <KeyRow
                      key={k.id}
                      apiKey={k}
                      selected={selectedIds.has(k.id)}
                      onSelect={() => toggleSelect(k.id)}
                      onOpen={() => navigate(`/keys/${k.id}`)}
                      onToggle={() => toggleDisabled.mutate({ id: k.id, disabled: !k.disabled })}
                      togglePending={toggleDisabled.isPending && toggleDisabled.variables?.id === k.id}
                      onCopyKey={() => copy(k.display, "Masked key copied")}
                      onCopyPortal={() => copy(portalUrlFor(k.id), "Portal link copied")}
                      onRevoke={() => revokeOne(k)}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <TablePagination page={pagination.page} pages={pagination.pages} total={pagination.total} onPage={pagination.setPage} />
        </section>
      )}
    </>
  );
}

function KeyRow({
  apiKey: k,
  selected,
  onSelect,
  onOpen,
  onToggle,
  togglePending,
  onCopyKey,
  onCopyPortal,
  onRevoke,
}: {
  apiKey: APIKey;
  selected: boolean;
  onSelect: () => void;
  onOpen: () => void;
  onToggle: () => void;
  togglePending: boolean;
  onCopyKey: () => void;
  onCopyPortal: () => void;
  onRevoke: () => void;
}) {
  const modelCount = k.allowed_models?.length ?? 0;
  const lastUsed = usedAt(k.last_used_at);
  const stop = (e: React.MouseEvent) => e.stopPropagation();

  return (
    <tr className={cn("cursor-pointer transition-colors", selected ? "bg-accent-500/5" : "hover:bg-hover")} onClick={onOpen}>
      <td className="px-4 py-2.5" onClick={stop}>
        <input
          type="checkbox"
          checked={selected}
          onChange={onSelect}
          className="h-4 w-4 rounded border-input accent-accent-500"
          aria-label={`Select ${k.name}`}
        />
      </td>
      <td className="max-w-[300px] px-2 py-2.5">
        <Link
          to={`/keys/${k.id}`}
          onClick={stop}
          className={cn("block truncate rounded-sm font-medium hover:underline", FOCUS_RING, k.disabled ? "text-fg-muted" : "text-fg")}
          title={k.name}
        >
          {k.name}
        </Link>
        <span className="block truncate font-mono text-[12px] text-fg-faint">{k.display}</span>
      </td>
      <td className="max-w-[220px] px-4 py-2.5">
        <span className="block truncate text-fg">{k.plan_name || "Custom"}</span>
        {modelCount > 0 && (
          <span className="block truncate text-[12px] text-fg-muted">
            {modelCount} model{modelCount > 1 ? "s" : ""}
          </span>
        )}
      </td>
      <td className="px-4 py-2.5" onClick={stop}>
        <span className="inline-flex items-center gap-2">
          <Toggle checked={!k.disabled} onChange={onToggle} disabled={togglePending} label={`${k.name} active`} />
          <span aria-hidden="true">
            {k.disabled ? <Badge tone="neutral">Disabled</Badge> : <Badge tone="success">Active</Badge>}
          </span>
        </span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5" title={lastUsed ? new Date(lastUsed).toLocaleString() : undefined}>
        <span className={lastUsed ? "text-fg" : "text-fg-muted"}>{relativeTime(k.last_used_at)}</span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-fg-muted" title={new Date(k.created_at).toLocaleString()}>
        {new Date(k.created_at).toLocaleDateString()}
      </td>
      <td className="px-2 py-2.5" onClick={stop}>
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={`Actions for ${k.name}`}
            className={cn("flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onOpen}>
              <Settings2 aria-hidden="true" />
              Configure
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onCopyKey}>
              <Copy aria-hidden="true" />
              Copy masked key
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onCopyPortal}>
              <Link2 aria-hidden="true" />
              Copy portal link
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem tone="danger" onSelect={onRevoke}>
              <Trash2 aria-hidden="true" />
              Revoke key
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </td>
    </tr>
  );
}

/* ── Created: one-time secret reveal ─────────────────────────────── */

function StepSuccess({
  created,
  copied,
  setCopied,
  onClose,
  endpointUrl,
  planName,
  availableModelsText,
}: {
  created: CreatedKey;
  copied: boolean;
  setCopied: (v: boolean) => void;
  onClose: () => void;
  endpointUrl: string;
  planName: string;
  availableModelsText: string;
}) {
  const copy = useCopy();
  const uid = useId();
  const [justCopied, setJustCopied] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState(false);
  const [copiedAll, setCopiedAll] = useState(false);
  const [showSetup, setShowSetup] = useState(false);
  const portalUrl = portalUrlFor(created.id);

  const shareText = [
    "Keirouter",
    `Endpoint : ${endpointUrl}`,
    `API Key : ${created.key}`,
    `Portal Monitoring : ${portalUrl}`,
    `Plan : ${planName}`,
    `Available model : ${availableModelsText}`,
  ].join("\n");

  const flash = (set: (v: boolean) => void, ms: number) => {
    set(true);
    setTimeout(() => set(false), ms);
  };

  const secretLabelId = `${uid}-secret`;
  const warnId = `${uid}-warn`;
  const setupId = `${uid}-setup`;

  return (
    <>
      <div className="space-y-4 px-5 py-4">
        <div className="space-y-3">
          <div className="flex items-center gap-3" id={warnId}>
            <IconTile icon={Check} size="md" tone="ok" />
            <p className="min-w-0 text-[13px] leading-5">
              <span className="block font-medium text-fg">Copy this key now</span>
              <span className="block text-fg-muted">It won't be shown again.</span>
            </p>
          </div>

          <p id={secretLabelId} className="sr-only">Secret key</p>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-stretch">
            <code
              aria-labelledby={secretLabelId}
              className="min-w-0 flex-1 select-all break-all rounded-lg border border-line-strong bg-subtle px-3 py-2.5 font-mono text-[13px] leading-5 text-fg"
            >
              {created.key}
            </code>
            <Button
              variant="primary"
              className="shrink-0 sm:self-start"
              aria-describedby={warnId}
              onClick={() => {
                copy(created.key, "Key copied", "Store it somewhere safe.").then((ok) => {
                  if (!ok) return;
                  setCopied(true);
                  flash(setJustCopied, 1500);
                });
              }}
            >
              {justCopied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
              {justCopied ? "Copied" : "Copy key"}
            </Button>
          </div>

          <p role="status" className="min-h-5 text-[12.5px]">
            {copied && (
              <span className="inline-flex items-center gap-1.5 text-ok">
                <Check className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />
                Key copied to your clipboard.
              </span>
            )}
          </p>
        </div>

        <dl className="divide-y divide-line rounded-lg border border-line text-[12.5px]">
          <div className="flex items-center gap-3 px-3 py-1.5">
            <dt className="w-24 shrink-0 text-fg-muted">Owner portal</dt>
            <dd className="flex min-w-0 flex-1 items-center gap-1">
              <span className="min-w-0 flex-1 truncate font-mono text-[12px] text-fg" title={portalUrl}>{portalUrl}</span>
              <button
                type="button"
                onClick={() => copy(portalUrl, "Portal link copied", "Share it with the key owner.").then((ok) => ok && flash(setCopiedUrl, 1500))}
                aria-label="Copy portal link"
                title="Copy portal link"
                className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-faint transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
              >
                {copiedUrl ? <Check className="h-4 w-4 text-ok" strokeWidth={1.75} aria-hidden="true" /> : <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />}
              </button>
            </dd>
          </div>
          <div className="flex items-center gap-3 px-3 py-2">
            <dt className="w-24 shrink-0 text-fg-muted">Plan</dt>
            <dd className="min-w-0 flex-1 text-fg">{planName}</dd>
          </div>
          {created.budget && (
            <div className="flex items-center gap-3 px-3 py-2">
              <dt className="w-24 shrink-0 text-fg-muted">Budget</dt>
              <dd className="min-w-0 flex-1 tabular-nums text-fg">
                {created.budget.limit_micros > 0 && `$${(created.budget.limit_micros / 1_000_000).toFixed(2)}`}
                {created.budget.limit_micros > 0 && created.budget.limit_tokens > 0 && " + "}
                {created.budget.limit_tokens > 0 && `${formatTokenLimit(String(created.budget.limit_tokens))} tokens`}
                {` / ${created.budget.period}`}
                {created.budget.hard_cutoff ? " · hard cutoff" : ""}
              </dd>
            </div>
          )}
          {created.allowed_models && created.allowed_models.length > 0 && (
            <div className="flex items-start gap-3 px-3 py-2">
              <dt className="w-24 shrink-0 text-fg-muted">Models</dt>
              <dd className="min-w-0 flex-1 break-words font-mono text-[12px] text-fg">{created.allowed_models.join(", ")}</dd>
            </div>
          )}
        </dl>

        <div className="rounded-lg border border-line">
          <div className="flex items-center justify-between gap-2 px-3 py-1.5">
            <button
              type="button"
              onClick={() => setShowSetup((v) => !v)}
              aria-expanded={showSetup}
              aria-controls={setupId}
              className={cn("inline-flex min-h-8 items-center gap-1.5 rounded-md text-[12.5px] font-medium text-fg hover:underline", FOCUS_RING)}
            >
              <ChevronDown className={cn("h-4 w-4 text-fg-faint transition-transform", showSetup && "rotate-180")} strokeWidth={1.75} aria-hidden="true" />
              Setup message
            </button>
            <button
              type="button"
              onClick={() => copy(shareText, "Setup message copied", "Endpoint, key, portal and plan.").then((ok) => ok && flash(setCopiedAll, 2000))}
              className={cn("inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-[12.5px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg", FOCUS_RING)}
            >
              {copiedAll ? <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
              {copiedAll ? "Copied" : "Copy setup message"}
            </button>
          </div>
          {showSetup && (
            <pre id={setupId} className="max-h-48 overflow-auto whitespace-pre-wrap break-all border-t border-line px-3 py-2.5 font-mono text-[12px] leading-5 text-fg">
              {shareText}
            </pre>
          )}
        </div>
      </div>

      <DialogFooter>
        <div className="flex-1" />
        <Button variant="secondary" onClick={onClose}>Done</Button>
      </DialogFooter>
    </>
  );
}
