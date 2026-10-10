import { useState, useCallback, useEffect, useMemo, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  Check,
  Copy,
  KeyRound,
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
  Button,
  Input,
  Select,
  Badge,
  Skeleton,
  Toggle,
  Modal,
  ErrorBanner,
  TablePagination,
  useClientPagination,
} from "../components/ui";
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

type KeySummary = {
  total: number;
  active: number;
  disabled: number;
  restricted: number;
  usedRecently: number;
  neverUsed: number;
};

const DAY_MS = 86_400_000;

function usedAt(iso?: string | null): number | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  // Go's zero time ("0001-01-01…") parses to a negative epoch: treat as never.
  return Number.isFinite(t) && t > 0 ? t : null;
}

function getKeySummary(keys: APIKey[] = []): KeySummary {
  const now = Date.now();
  return keys.reduce(
    (acc, key) => {
      acc.total += 1;
      if (key.disabled) acc.disabled += 1;
      else acc.active += 1;
      if ((key.allowed_models ?? []).length > 0) acc.restricted += 1;
      const t = usedAt(key.last_used_at);
      if (t === null) acc.neverUsed += 1;
      else if (now - t < DAY_MS) acc.usedRecently += 1;
      return acc;
    },
    { total: 0, active: 0, disabled: 0, restricted: 0, usedRecently: 0, neverUsed: 0 },
  );
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
          toast.error("Copy failed", "Your browser blocked clipboard access.");
          return false;
        },
      ),
    [toast],
  );
}

// ── Form primitives (match ConnectKit field styling) ─────────────────────────

function FormField({
  label,
  hint,
  optional,
  children,
}: {
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  children: ReactNode;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
      </span>
      {children}
      {hint && <span className="block text-[12px] leading-5 text-fg-muted">{hint}</span>}
    </label>
  );
}

/** Token count input that keeps the thousand separators visible while typing. */
function TokenInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <Input
      type="text"
      inputMode="numeric"
      value={formatTokenLimit(value)}
      onChange={(e) => onChange(e.target.value.replace(/[^\d]/g, ""))}
      placeholder={placeholder ? (/^\d+$/.test(placeholder) ? formatTokenLimit(placeholder) : placeholder) : undefined}
      className="tabular-nums"
    />
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
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys() });
  const access = useQuery({ queryKey: ["access-settings"], queryFn: () => api.accessSettings() });

  const [modalOpen, setModalOpen] = useState(false);
  const [step, setStep] = useState<1 | 2 | 3 | 4>(1);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("created_desc");

  const allKeys = keys.data?.keys ?? [];
  const summary = useMemo(() => getKeySummary(keys.data?.keys ?? []), [keys.data]);

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

  // Step 1 — name
  const [name, setName] = useState("");

  // Step 2 — budget
  const [budgetLimit, setBudgetLimit] = useState("");
  const [budgetLimitTokens, setBudgetLimitTokens] = useState("");
  const [budgetPeriod, setBudgetPeriod] = useState("monthly");
  const [budgetAlertPct, setBudgetAlertPct] = useState(80);
  const [budgetHardCutoff, setBudgetHardCutoff] = useState(true);
  const [allowedModels, setAllowedModels] = useState<string[]>([]);

  // Step 3 — result
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [copied, setCopied] = useState(false);

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
    setCreated(null);
    setCopied(false);
    setStep(1);
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    if (created) {
      setCreated(null);
      setCopied(false);
    }
  };

  // Plan selection
  const plans = useQuery({ queryKey: ["plans"], queryFn: () => api.listPlans() });
  const [selectedPlanId, setSelectedPlanId] = useState<string>("custom");
  const [customizePlan, setCustomizePlan] = useState(false);

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
      setStep(4);
      const planMsg = data.plan ? ` Plan: ${data.plan.name}.` : "";
      const parts = [];
      if (data.budget && data.budget.limit_micros > 0) parts.push(`$${(data.budget.limit_micros / 1_000_000).toFixed(2)}`);
      if (data.budget && data.budget.limit_tokens > 0) parts.push(`${(data.budget.limit_tokens / 1_000_000).toFixed(0)}M tokens`);
      const budgetMsg = parts.length > 0 ? ` Budget: ${parts.join(" + ")} / ${data.budget?.period}.` : "";
      const modelMsg = data.allowed_models?.length ? ` Models: ${data.allowed_models.join(", ")}.` : "";
      toast.success("Key created", `Copy the key below — it won't be shown again.${planMsg}${budgetMsg}${modelMsg}`);
    },
    onError: (e: Error) => toast.error("Key creation failed", e.message),
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
      toast.success(`${ids.length} key${ids.length > 1 ? "s" : ""} revoked`, "All selected keys have been permanently deleted.");
    },
    onError: (e: Error) => toast.error("Bulk revocation failed", e.message),
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
      toast.success("Key revoked", "The key has been permanently deleted and can no longer authenticate requests.");
    },
    onError: (e: Error) => toast.error("Revocation failed", e.message),
  });

  const toggleDisabled = useMutation({
    mutationFn: ({ id, disabled }: { id: string; disabled: boolean }) => api.updateKey(id, { disabled }),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success(
        data.disabled ? "Key disabled" : "Key enabled",
        data.disabled
          ? "Requests using this key will be rejected until re-enabled."
          : "This key can now authenticate requests again.",
      );
    },
    onError: (e: Error) => toast.error("Key update failed", e.message),
  });

  const revokeOne = async (k: APIKey) => {
    if (!(await confirm({ title: `Revoke ${k.name}?`, description: "Tools using this key stop authenticating immediately. This cannot be undone.", confirmLabel: "Revoke", tone: "danger" }))) return;
    remove.mutate(k.id);
  };

  const statusCounts: Record<StatusFilter, number> = { all: summary.total, active: summary.active, inactive: summary.disabled };
  const allVisibleSelected = visibleKeys.length > 0 && visibleKeys.every((k) => selectedIds.has(k.id));
  const someVisibleSelected = visibleKeys.some((k) => selectedIds.has(k.id));
  const filtering = searchQuery.trim() !== "" || statusFilter !== "all";

  return (
    <>
      <PageHeader
        title="API keys"
        icon={KeyRound}
        description="Keys your tools and teammates use to call KeiRouter. Each key carries a plan, an optional model allowlist and its own owner portal."
        action={
          <Button onClick={openModal}>
            <Plus />
            New key
          </Button>
        }
      />

      <Modal
        open={modalOpen}
        onClose={closeModal}
        maxWidth="max-w-xl"
        title={step === 4 ? "Key created" : "Create API key"}
        subtitle={
          step === 1
            ? "Step 1 of 3 · Name the key so you can recognise it in usage logs."
            : step === 2
              ? "Step 2 of 3 · Choose a plan or set custom limits."
              : step === 3
                ? selectedPlanId === "custom"
                  ? "Step 3 of 3 · Set limits and model access for this key."
                  : "Step 3 of 3 · Optionally override plan settings for this key."
                : undefined
        }
      >
        {step === 1 && <StepName name={name} setName={setName} onNext={() => setStep(2)} onCancel={closeModal} />}
        {step === 2 && (
          <StepPlanSelect
            plans={plans.data?.plans ?? []}
            loading={plans.isLoading}
            selectedPlanId={selectedPlanId}
            setSelectedPlanId={setSelectedPlanId}
            onBack={() => setStep(1)}
            onNext={() => setStep(3)}
          />
        )}
        {step === 3 && (
          <StepConfigure
            selectedPlanId={selectedPlanId}
            plans={plans.data?.plans ?? []}
            customizePlan={customizePlan}
            setCustomizePlan={setCustomizePlan}
            budgetLimit={budgetLimit}
            setBudgetLimit={setBudgetLimit}
            budgetLimitTokens={budgetLimitTokens}
            setBudgetLimitTokens={setBudgetLimitTokens}
            budgetPeriod={budgetPeriod}
            setBudgetPeriod={setBudgetPeriod}
            budgetAlertPct={budgetAlertPct}
            setBudgetAlertPct={setBudgetAlertPct}
            budgetHardCutoff={budgetHardCutoff}
            setBudgetHardCutoff={setBudgetHardCutoff}
            allowedModels={allowedModels}
            setAllowedModels={setAllowedModels}
            onBack={() => setStep(2)}
            onCreate={() => create.mutate()}
            isPending={create.isPending}
          />
        )}
        {step === 4 && created && (
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
        )}
      </Modal>

      {keys.isLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-[74px] w-full rounded-2xl" />
          <Skeleton className="h-9 w-full max-w-xl" />
          <Skeleton className="h-80 w-full rounded-2xl" />
        </div>
      ) : keys.isError ? (
        <ErrorBanner message={`Couldn't load API keys. ${keys.error instanceof Error ? keys.error.message : ""}`.trim()} />
      ) : allKeys.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <p className="text-[14px] font-medium text-fg">No API keys yet</p>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
            Create a key for a CLI tool, app or teammate. The full secret is shown once, then only its hash is stored.
          </p>
          <Button className="mt-4" onClick={openModal}>
            <Plus />
            Create first key
          </Button>
        </div>
      ) : (
        <>
          <section aria-label="Key summary" className="mb-5 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
            <div className="grid grid-cols-2 gap-px bg-line lg:grid-cols-4">
              <KpiCell label="Total keys" value={summary.total} hint={summary.neverUsed > 0 ? `${summary.neverUsed} never used` : "All have been used"} />
              <KpiCell label="Active" value={summary.active} hint={`${summary.usedRecently} used in the last 24 hours`} />
              <KpiCell label="Disabled" value={summary.disabled} hint="Rejected until re-enabled" muted={summary.disabled === 0} />
              <KpiCell label="Model-restricted" value={summary.restricted} hint="Own model allowlist" muted={summary.restricted === 0} />
            </div>
          </section>

          <div className="mb-3 flex flex-col gap-3 lg:flex-row lg:items-center">
            <div className="relative lg:w-80">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
              <input
                type="search"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search name, key or plan"
                aria-label="Search keys"
                className="h-9 w-full rounded-lg border border-line bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
              />
              {searchQuery && (
                <button
                  type="button"
                  onClick={() => setSearchQuery("")}
                  className="absolute right-2 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg"
                  aria-label="Clear search"
                >
                  <X className="h-3.5 w-3.5" />
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
                      "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
                      active ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
                    )}
                  >
                    {f.label}
                    <span className={cn("tabular-nums", active ? "opacity-70" : "text-fg-faint")}>{statusCounts[f.value]}</span>
                  </button>
                );
              })}
            </div>
            <div className="lg:ml-auto">
              <label className="sr-only" htmlFor="key-sort">Sort keys</label>
              <Select id="key-sort" className="h-9 w-full sm:w-40" value={sortKey} onChange={(e) => setSortKey(e.target.value as SortKey)}>
                <option value="created_desc">Newest first</option>
                <option value="created_asc">Oldest first</option>
                <option value="name_asc">Name A–Z</option>
                <option value="name_desc">Name Z–A</option>
              </Select>
            </div>
          </div>

          <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
            <div className="flex min-h-12 flex-wrap items-center gap-2 border-b border-line px-4 py-2">
              {selectedIds.size > 0 ? (
                <>
                  <span className="text-[13px] font-medium text-fg">{selectedIds.size} selected</span>
                  <div className="ml-auto flex items-center gap-1.5">
                    <Button variant="danger" onClick={handleBulkDelete} disabled={bulkRemove.isPending}>
                      <Trash2 />
                      Revoke {selectedIds.size}
                    </Button>
                    <button
                      type="button"
                      onClick={clearSelection}
                      aria-label="Clear selection"
                      className="flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg"
                    >
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                </>
              ) : (
                <span className="text-[12.5px] text-fg-muted">
                  {filtering ? `${visibleKeys.length} of ${allKeys.length} keys` : `${allKeys.length} key${allKeys.length === 1 ? "" : "s"}`} · open a key to change its models or guardrails
                </span>
              )}
            </div>

            {visibleKeys.length === 0 ? (
              <div className="px-6 py-10 text-center">
                <p className="text-[13px] font-medium text-fg">No keys match</p>
                <p className="mt-1 text-[12.5px] text-fg-muted">Clear the search or status filter to see every key.</p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[920px] text-[13px]">
                  <thead>
                    <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                      <th className="w-10 px-4 py-2">
                        <input
                          type="checkbox"
                          checked={allVisibleSelected}
                          ref={(el) => {
                            if (el) el.indeterminate = someVisibleSelected && !allVisibleSelected;
                          }}
                          onChange={toggleSelectAll}
                          className="h-4 w-4 rounded border-line accent-[var(--color-accent-500)]"
                          aria-label={`Select all ${visibleKeys.length} visible keys`}
                        />
                      </th>
                      <th className="px-2 py-2 font-medium">Name</th>
                      <th className="px-4 py-2 font-medium">Key</th>
                      <th className="px-4 py-2 font-medium">Plan</th>
                      <th className="px-4 py-2 font-medium">Status</th>
                      <th className="px-4 py-2 font-medium">Last used</th>
                      <th className="px-4 py-2 font-medium">Created</th>
                      <th className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
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
                        onCopyKey={() => copy(k.display, "Key copied", "Masked key identifier copied.")}
                        onCopyPortal={() => copy(portalUrlFor(k.id), "Portal link copied", "Owner usage portal link copied.")}
                        onRevoke={() => revokeOne(k)}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <TablePagination page={pagination.page} pages={pagination.pages} total={pagination.total} onPage={pagination.setPage} />
          </div>
        </>
      )}
    </>
  );
}

function KpiCell({ label, value, hint, muted }: { label: string; value: number; hint?: string; muted?: boolean }) {
  return (
    <div className="bg-surface px-4 py-3">
      <p className="text-[12px] font-medium text-fg-muted">{label}</p>
      <p className={cn("mt-1 text-[20px] font-semibold leading-tight tracking-[-0.01em] tabular-nums", muted ? "text-fg-faint" : "text-fg")}>{value.toLocaleString("en-US")}</p>
      {hint && <p className="mt-0.5 truncate text-[12px] text-fg-faint">{hint}</p>}
    </div>
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
    <tr
      className={cn("cursor-pointer transition-colors", selected ? "bg-accent-500/5" : "hover:bg-hover", k.disabled && !selected && "text-fg-muted")}
      onClick={onOpen}
    >
      <td className="px-4 py-2.5" onClick={stop}>
        <input
          type="checkbox"
          checked={selected}
          onChange={onSelect}
          className="h-4 w-4 rounded border-line accent-[var(--color-accent-500)]"
          aria-label={`Select ${k.name}`}
        />
      </td>
      <td className="max-w-[260px] px-2 py-2.5">
        <Link
          to={`/keys/${k.id}`}
          onClick={stop}
          className={cn("block truncate font-medium hover:underline focus:outline-none focus-visible:underline", k.disabled ? "text-fg-muted" : "text-fg")}
          title={k.name}
        >
          {k.name}
        </Link>
      </td>
      <td className="px-4 py-2.5" onClick={stop}>
        <button
          type="button"
          onClick={onCopyKey}
          className="group inline-flex max-w-[220px] items-center gap-1.5 rounded-md font-mono text-[12px] text-fg-muted transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
          title="Copy masked key"
        >
          <span className="truncate">{k.display}</span>
          <Copy className="h-3.5 w-3.5 shrink-0 text-fg-faint opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100" strokeWidth={1.75} />
        </button>
      </td>
      <td className="max-w-[220px] px-4 py-2.5">
        <span className="block truncate text-fg">{k.plan_name || "Custom"}</span>
        <span className="block truncate text-[12px] text-fg-faint">
          {modelCount > 0 ? `${modelCount} model${modelCount > 1 ? "s" : ""} allowed` : "Plan model access"}
        </span>
      </td>
      <td className="px-4 py-2.5" onClick={stop}>
        <span className={cn("inline-flex items-center gap-2", togglePending && "pointer-events-none opacity-50")} aria-label={k.disabled ? `Enable ${k.name}` : `Disable ${k.name}`}>
          <Toggle checked={!k.disabled} onChange={onToggle} />
          <span className={cn("text-[12.5px]", k.disabled ? "text-fg-faint" : "text-fg")}>{k.disabled ? "Disabled" : "Active"}</span>
        </span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5" title={lastUsed ? new Date(lastUsed).toLocaleString() : "This key has not authenticated a request yet"}>
        <span className={lastUsed ? "text-fg" : "text-fg-faint"}>{relativeTime(k.last_used_at)}</span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-fg-muted" title={new Date(k.created_at).toLocaleString()}>
        {new Date(k.created_at).toLocaleDateString()}
      </td>
      <td className="px-2 py-2.5" onClick={stop}>
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={`Actions for ${k.name}`}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
          >
            <MoreHorizontal className="h-4 w-4" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onOpen}>
              <Settings2 />
              Configure
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onCopyKey}>
              <Copy />
              Copy masked key
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onCopyPortal}>
              <Link2 />
              Copy portal link
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem tone="danger" onSelect={onRevoke}>
              <Trash2 />
              Revoke key
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </td>
    </tr>
  );
}

/* ── Step 1: Name ───────────────────────────────────────────────── */

function StepName({
  name,
  setName,
  onNext,
  onCancel,
}: {
  name: string;
  setName: (v: string) => void;
  onNext: () => void;
  onCancel: () => void;
}) {
  return (
    <>
      <div className="px-5 py-4">
        <FormField label="Key name" hint="Shown in usage logs and on the owner portal. Name it after the person, tool or machine that will use it.">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="laptop"
            autoFocus
            onKeyDown={(e) => {
              if (e.key === "Enter" && name.trim()) {
                e.preventDefault();
                onNext();
              }
            }}
          />
        </FormField>
      </div>
      <DialogFooter>
        <div className="flex-1" />
        <Button variant="ghost" onClick={onCancel}>Cancel</Button>
        <Button onClick={onNext} disabled={!name.trim()}>
          Next
          <ArrowRight />
        </Button>
      </DialogFooter>
    </>
  );
}

/* ── Step 2: Plan Select ────────────────────────────────────────── */

function planLimitText(p: Plan): string {
  const parts: string[] = [];
  if (p.limit_micros > 0) parts.push(`${microsToUSD(p.limit_micros)} / ${p.period}`);
  if (p.limit_tokens > 0) parts.push(`${formatTokens(p.limit_tokens)} tokens / ${p.period}`);
  if (parts.length === 0) parts.push("No spend limit");
  return parts.join(" · ");
}

function PlanOption({
  selected,
  onSelect,
  title,
  detail,
  aside,
}: {
  selected: boolean;
  onSelect: () => void;
  title: string;
  detail: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex w-full items-start gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
        selected ? "border-accent-500 bg-accent-500/5" : "border-line bg-surface hover:border-line-strong hover:bg-hover",
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border",
          selected ? "border-accent-500" : "border-line-strong",
        )}
      >
        {selected && <span className="h-2 w-2 rounded-full bg-accent-500" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-fg">{title}</span>
        <span className="mt-0.5 block text-[12px] leading-5 text-fg-muted">{detail}</span>
      </span>
      {aside && <span className="shrink-0 text-[12px] tabular-nums text-fg-faint">{aside}</span>}
    </button>
  );
}

function StepPlanSelect({
  plans,
  loading,
  selectedPlanId,
  setSelectedPlanId,
  onBack,
  onNext,
}: {
  plans: Plan[];
  loading: boolean;
  selectedPlanId: string;
  setSelectedPlanId: (v: string) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  return (
    <>
      <div className="px-5 py-4">
        <p className="mb-3 text-[12.5px] text-fg-muted">
          A plan sets the key's budget and model access. Pick Custom to configure this key on its own.
        </p>
        <div className="max-h-[50vh] space-y-1.5 overflow-y-auto pr-0.5" role="radiogroup" aria-label="Plan">
          {loading && (
            <>
              <Skeleton className="h-14 w-full rounded-lg" />
              <Skeleton className="h-14 w-full rounded-lg" />
            </>
          )}
          {plans.map((p) => {
            const restricted = (p.allowed_models ?? []).length;
            return (
              <PlanOption
                key={p.id}
                selected={selectedPlanId === p.id}
                onSelect={() => setSelectedPlanId(p.id)}
                title={p.name}
                detail={
                  <>
                    {planLimitText(p)}
                    {restricted > 0 && ` · ${restricted} model restriction${restricted === 1 ? "" : "s"}`}
                  </>
                }
                aside={`${p.key_count} key${p.key_count !== 1 ? "s" : ""}`}
              />
            );
          })}
          <PlanOption
            selected={selectedPlanId === "custom"}
            onSelect={() => setSelectedPlanId("custom")}
            title="Custom"
            detail="No preset. Set the budget and allowed models for this key yourself."
          />
        </div>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft />
          Back
        </Button>
        <div className="flex-1" />
        <Button onClick={onNext}>
          Next
          <ArrowRight />
        </Button>
      </DialogFooter>
    </>
  );
}

/* ── Step 3: Configure (plan details / custom) ──────────────────── */

function SettingRow({ label, description, children }: { label: string; description?: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-3 py-2.5">
      <div className="min-w-0">
        <p className="text-[13px] font-medium text-fg">{label}</p>
        {description && <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">{description}</p>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

function StepConfigure({
  selectedPlanId,
  plans,
  customizePlan,
  setCustomizePlan,
  budgetLimit,
  setBudgetLimit,
  budgetLimitTokens,
  setBudgetLimitTokens,
  budgetPeriod,
  setBudgetPeriod,
  budgetAlertPct,
  setBudgetAlertPct,
  budgetHardCutoff,
  setBudgetHardCutoff,
  allowedModels,
  setAllowedModels,
  onBack,
  onCreate,
  isPending,
}: {
  selectedPlanId: string;
  plans: Plan[];
  customizePlan: boolean;
  setCustomizePlan: (v: boolean) => void;
  budgetLimit: string;
  setBudgetLimit: (v: string) => void;
  budgetLimitTokens: string;
  setBudgetLimitTokens: (v: string) => void;
  budgetPeriod: string;
  setBudgetPeriod: (v: string) => void;
  budgetAlertPct: number;
  setBudgetAlertPct: (v: number) => void;
  budgetHardCutoff: boolean;
  setBudgetHardCutoff: (v: boolean) => void;
  allowedModels: string[];
  setAllowedModels: (v: string[]) => void;
  onBack: () => void;
  onCreate: () => void;
  isPending: boolean;
}) {
  const isCustom = selectedPlanId === "custom";
  const selectedPlan = plans.find((p) => p.id === selectedPlanId);
  const models = selectedPlan?.allowed_models ?? [];

  if (isCustom) {
    // Full custom config (same as old StepBudget)
    return (
      <>
        <div className="space-y-4 px-5 py-4">
          <div className="grid gap-3 sm:grid-cols-[1fr_1fr_8.5rem]">
            <FormField label="Limit (USD)" optional>
              <Input
                type="number"
                min="0"
                step="0.01"
                value={budgetLimit}
                onChange={(e) => setBudgetLimit(e.target.value)}
                placeholder="50.00"
                className="tabular-nums"
              />
            </FormField>
            <FormField label="Limit (tokens)" optional>
              <TokenInput value={budgetLimitTokens} onChange={setBudgetLimitTokens} placeholder="100000000" />
            </FormField>
            <FormField label="Period">
              <Select value={budgetPeriod} onChange={(e) => setBudgetPeriod(e.target.value)}>
                {budgetPeriods.map((p) => (
                  <option key={p.value} value={p.value}>{p.label}</option>
                ))}
              </Select>
            </FormField>
          </div>

          <div className="space-y-1.5">
            <span className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
              Allowed models
              <span className="text-[12px] font-normal text-fg-faint">Optional</span>
            </span>
            <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} />
            <span className="block text-[12px] leading-5 text-fg-muted">
              Leave empty to allow every model. Add custom patterns with a <span className="font-mono">*</span> wildcard, e.g. <span className="font-mono">claude-*</span>.
            </span>
          </div>

          <div className="divide-y divide-line rounded-lg border border-line">
            <SettingRow label="Alert threshold" description="Notify when this share of the budget is used. Applies once a limit is set.">
              <span className="flex items-center gap-1.5">
                <Input
                  type="number"
                  min="1"
                  max="100"
                  value={budgetAlertPct}
                  onChange={(e) => setBudgetAlertPct(parseInt(e.target.value) || 80)}
                  className="w-20 text-right tabular-nums"
                  aria-label="Alert threshold percent"
                />
                <span className="text-[13px] text-fg-muted">%</span>
              </span>
            </SettingRow>
            <SettingRow label="Hard cutoff" description="Block requests once the budget is used up. Off only sends alerts.">
              <Toggle checked={budgetHardCutoff} onChange={setBudgetHardCutoff} />
            </SettingRow>
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onBack}>
            <ArrowLeft />
            Back
          </Button>
          <div className="flex-1" />
          <Button variant="ghost" onClick={onCreate} disabled={isPending}>
            {isPending ? "Creating…" : "Skip budget"}
          </Button>
          <Button onClick={onCreate} disabled={isPending}>
            {isPending ? "Creating…" : "Create key"}
          </Button>
        </DialogFooter>
      </>
    );
  }

  // Plan selected — show summary + optional override toggle
  return (
    <>
      <div className="space-y-4 px-5 py-4">
        {selectedPlan && (
          <div className="rounded-lg border border-line bg-subtle px-3 py-2.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[13px] font-medium text-fg">{selectedPlan.name}</span>
              <Badge>{selectedPlan.period}</Badge>
              {selectedPlan.hard_cutoff ? <Badge tone="danger">Hard cutoff</Badge> : <Badge tone="neutral">Advisory</Badge>}
            </div>
            {selectedPlan.description && <p className="mt-1 text-[12px] leading-5 text-fg-muted">{selectedPlan.description}</p>}
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[12px]">
              <dt className="text-fg-faint">Budget</dt>
              <dd className="tabular-nums text-fg">
                {selectedPlan.limit_micros > 0 && microsToUSD(selectedPlan.limit_micros)}
                {selectedPlan.limit_micros > 0 && selectedPlan.limit_tokens > 0 && " · "}
                {selectedPlan.limit_tokens > 0 && `${formatTokens(selectedPlan.limit_tokens)} tokens`}
                {selectedPlan.limit_micros === 0 && selectedPlan.limit_tokens === 0 && "No spend limit"}
              </dd>
              <dt className="text-fg-faint">Alert</dt>
              <dd className="tabular-nums text-fg">At {selectedPlan.alert_pct}%</dd>
              <dt className="text-fg-faint">Models</dt>
              <dd className="min-w-0 break-words font-mono text-[11.5px] text-fg">
                {models.length > 0 ? models.join(", ") : <span className="font-sans text-[12px]">All models</span>}
              </dd>
            </dl>
          </div>
        )}

        <div className="divide-y divide-line rounded-lg border border-line">
          <SettingRow label="Customize for this key" description="Override the plan's limits or models for this key only.">
            <Toggle checked={customizePlan} onChange={setCustomizePlan} />
          </SettingRow>
          {customizePlan && (
            <div className="space-y-4 px-3 py-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <FormField label="USD limit" optional>
                  <Input
                    type="number"
                    min="0"
                    step="0.01"
                    value={budgetLimit}
                    onChange={(e) => setBudgetLimit(e.target.value)}
                    placeholder="Leave empty to use plan"
                    className="tabular-nums"
                  />
                </FormField>
                <FormField label="Token limit" optional>
                  <TokenInput value={budgetLimitTokens} onChange={setBudgetLimitTokens} placeholder="Leave empty to use plan" />
                </FormField>
              </div>
              <div className="space-y-1.5">
                <span className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
                  Allowed models
                  <span className="text-[12px] font-normal text-fg-faint">Optional</span>
                </span>
                <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} />
                <span className="block text-[12px] leading-5 text-fg-muted">Leave empty to use the plan's model restrictions.</span>
              </div>
            </div>
          )}
        </div>
      </div>

      <DialogFooter>
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft />
          Back
        </Button>
        <div className="flex-1" />
        <Button onClick={onCreate} disabled={isPending}>
          {isPending ? "Creating…" : "Create key"}
        </Button>
      </DialogFooter>
    </>
  );
}

/* ── Step 4: Success / Copy ─────────────────────────────────────── */

function CopyIconButton({ label, onCopy, copied }: { label: string; onCopy: () => void; copied: boolean }) {
  return (
    <button
      type="button"
      onClick={onCopy}
      aria-label={label}
      title={label}
      className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
    >
      {copied ? <Check className="h-4 w-4 text-ok" strokeWidth={1.75} /> : <Copy className="h-4 w-4" strokeWidth={1.75} />}
    </button>
  );
}

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
  const [copiedUrl, setCopiedUrl] = useState(false);
  const [copiedAll, setCopiedAll] = useState(false);
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

  return (
    <>
      <div className="space-y-4 px-5 py-4">
        <div role="note" className="flex items-start gap-2.5 rounded-lg border border-warn/30 bg-warn/5 px-3 py-2.5 text-[12.5px] leading-5 text-fg">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} />
          <span>
            <span className="font-medium">Copy this key now — you won't see it again.</span> KeiRouter only keeps a hash. If it's lost, revoke the key and create a new one.
          </span>
        </div>

        <div className="space-y-1.5">
          <p className="text-[12.5px] font-medium text-fg">Secret key</p>
          <div className="flex items-stretch gap-2">
            <code className="min-w-0 flex-1 select-all break-all rounded-lg border border-line-strong bg-subtle px-3 py-2.5 font-mono text-[13px] leading-5 text-fg">
              {created.key}
            </code>
            <Button
              className="shrink-0 self-start"
              onClick={() => {
                copy(created.key, "Key copied", "Store it somewhere safe — it won't be shown again.").then((ok) => {
                  if (ok) flash(setCopied, 1500);
                });
              }}
            >
              {copied ? <Check /> : <Copy />}
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
        </div>

        <dl className="divide-y divide-line rounded-lg border border-line text-[12.5px]">
          <div className="flex items-center gap-3 px-3 py-2">
            <dt className="w-24 shrink-0 text-fg-muted">Owner portal</dt>
            <dd className="flex min-w-0 flex-1 items-center gap-1">
              <span className="min-w-0 flex-1 truncate font-mono text-[12px] text-fg" title={portalUrl}>{portalUrl}</span>
              <CopyIconButton
                label="Copy portal link"
                copied={copiedUrl}
                onCopy={() => copy(portalUrl, "Portal link copied", "Share it with the key owner to let them track their usage.").then((ok) => ok && flash(setCopiedUrl, 1500))}
              />
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
                {created.budget.hard_cutoff ? " (hard cutoff)" : ""}
              </dd>
            </div>
          )}
          {created.allowed_models && created.allowed_models.length > 0 && (
            <div className="flex items-start gap-3 px-3 py-2">
              <dt className="w-24 shrink-0 text-fg-muted">Allowed models</dt>
              <dd className="min-w-0 flex-1 break-words font-mono text-[12px] text-fg">{created.allowed_models.join(", ")}</dd>
            </div>
          )}
        </dl>

        <div className="overflow-hidden rounded-lg border border-line">
          <div className="flex items-center justify-between gap-2 border-b border-line bg-subtle px-3 py-1.5">
            <p className="text-[12.5px] font-medium text-fg">Setup message</p>
            <button
              type="button"
              onClick={() => copy(shareText, "Setup message copied", "Endpoint, key, portal and plan in one block.").then((ok) => ok && flash(setCopiedAll, 2000))}
              className="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              {copiedAll ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
              {copiedAll ? "Copied" : "Copy all"}
            </button>
          </div>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all px-3 py-2.5 font-mono text-[12px] leading-5 text-fg">{shareText}</pre>
        </div>
      </div>

      <DialogFooter>
        <span className="text-[12px] text-fg-faint">{copied ? "Key copied to your clipboard." : "The key disappears when you close this dialog."}</span>
        <div className="flex-1" />
        <Button onClick={onClose}>Done</Button>
      </DialogFooter>
    </>
  );
}
