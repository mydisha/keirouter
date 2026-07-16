import { useState, useCallback, useEffect, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Copy, Check, ToggleLeft, ToggleRight, ArrowLeft, ArrowRight, Trash2, Wallet, Wrench, DollarSign, Gauge, Link2, Activity, Ban, ListFilter, Search, X } from "lucide-react";
import { api, type APIKey, type CreatedKey, type Plan } from "../lib/api";
import { microsToUSD, formatTokens } from "../lib/format";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { formatTokenLimit, FormattedTokenInput, ModelMultiSelect } from "../components/ModelSelect";
import {
  Card,
  CardHeader,
  Button,
  Input,
  Select,
  Field,
  Badge,
  Spinner,
  Toggle,
  Modal,
  TablePagination,
  useClientPagination,
} from "../components/ui";

const budgetPeriods = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "total", label: "All time" },
];

type KeySummary = {
  total: number;
  active: number;
  disabled: number;
  restricted: number;
};

function getKeySummary(keys: APIKey[] = []): KeySummary {
  return keys.reduce(
    (acc, key) => {
      acc.total += 1;
      if (key.disabled) acc.disabled += 1;
      else acc.active += 1;
      if ((key.allowed_models ?? []).length > 0) acc.restricted += 1;
      return acc;
    },
    { total: 0, active: 0, disabled: 0, restricted: 0 },
  );
}

function StatusPill({ disabled }: { disabled: boolean }) {
  if (disabled) {
    return (
      <span className="inline-flex items-center gap-1.5 px-1 py-0.5 text-[11px] font-medium text-[var(--text-muted)]">
        <span className="relative flex h-1.5 w-1.5">
          <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-red-500 opacity-60"></span>
        </span>
        Inactive
      </span>
    );
  }

  return (
    <span className="inline-flex items-center gap-1.5 px-1 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
      <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
      Active
    </span>
  );
}

function SummaryItem({
  icon: Icon,
  label,
  value,
  tone = "default",
}: {
  icon: typeof KeyRound;
  label: string;
  value: number;
  tone?: "default" | "danger" | "warning";
}) {
  const iconTone =
    tone === "danger"
      ? "text-red-600 dark:text-red-400"
      : tone === "warning"
        ? "text-amber-600 dark:text-amber-400"
        : "text-secondary-600 dark:text-secondary-300";
  return (
    <div className="flex min-w-0 items-center gap-3 px-4 py-3 sm:px-5">
      <Icon className={`h-4 w-4 shrink-0 ${iconTone}`} strokeWidth={2} />
      <div className="min-w-0">
        <p className="truncate text-[11px] font-semibold uppercase tracking-[0.14em] text-[var(--text-muted)]">{label}</p>
        <p className="mt-0.5 text-lg font-semibold leading-none tabular-nums text-[var(--text)]">{value}</p>
      </div>
    </div>
  );
}

function KeyCopyButton({
  icon,
  label,
  value,
  copiedMessage,
  className = "",
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  copiedMessage: string;
  className?: string;
}) {
  const toast = useToast();

  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard.writeText(value).then(
          () => toast.success(label, copiedMessage),
          () => toast.error("Copy failed", "Your browser blocked clipboard access."),
        );
      }}
      className={`group inline-flex min-h-10 min-w-0 items-center gap-2 rounded-lg px-2 text-left text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50 ${className}`}
    >
      <span className="shrink-0">{icon}</span>
      <span className="min-w-0 truncate font-mono text-[11px] font-medium">
        {value}
      </span>
      <Copy className="h-3.5 w-3.5 shrink-0 opacity-60 transition-opacity group-hover:opacity-100" />
    </button>
  );
}

function KeyEmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="px-6 py-14">
      <div className="mx-auto max-w-md text-center">
        <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-2xl bg-secondary-100 text-secondary-700 dark:bg-secondary-900/40 dark:text-secondary-200">
          <KeyRound className="h-5 w-5" />
        </div>
        <h3 className="mt-4 text-base font-semibold tracking-tight text-[var(--text)]">No API keys yet</h3>
        <p className="mt-2 text-sm leading-relaxed text-[var(--text-muted)]">
          Create a key for CLI tools, apps, or teammates. Full secrets are shown once, then stored hashed.
        </p>
        <div className="mt-5 flex justify-center">
          <Button onClick={onCreate}>
            <Plus className="h-4 w-4" />
            Create first key
          </Button>
        </div>
      </div>
    </div>
  );
}

function KeyRow({
  apiKey,
  selected,
  onSelect,
  onToggle,
  onConfigure,
  onRevoke,
  togglePending,
}: {
  apiKey: APIKey;
  selected: boolean;
  onSelect: () => void;
  onToggle: () => void;
  onConfigure: () => void;
  onRevoke: () => void;
  togglePending: boolean;
}) {
  const portalUrl = `${window.location.origin}/portal?id=${apiKey.id}`;
  const modelCount = apiKey.allowed_models?.length ?? 0;

  return (
    <article
      className={`grid gap-2 px-4 py-3 transition-colors md:grid-cols-[minmax(190px,1.2fr)_minmax(160px,0.9fr)_minmax(120px,0.7fr)_auto] md:items-center md:px-5 ${
        selected ? "bg-secondary-50/50 dark:bg-secondary-950/20" : "hover:bg-[var(--bg-subtle)]/70"
      }`}
    >
      <div className="flex min-w-0 items-center gap-2">
        <label className="flex h-10 w-10 shrink-0 cursor-pointer items-center justify-center rounded-lg hover:bg-[var(--bg-subtle)]">
          <input
            type="checkbox"
            checked={selected}
            onChange={onSelect}
            className="h-4 w-4 rounded border-[var(--border)] accent-[var(--color-accent)]"
            aria-label={`Select ${apiKey.name}`}
          />
        </label>
        <button type="button" onClick={onConfigure} className="group min-w-0 flex-1 rounded-lg py-1 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-sm font-semibold tracking-tight text-[var(--text)] group-hover:text-secondary-700 dark:group-hover:text-secondary-300">{apiKey.name}</h3>
            <StatusPill disabled={apiKey.disabled} />
          </div>
          <p className="mt-0.5 truncate text-xs text-[var(--text-muted)]">Created {new Date(apiKey.created_at).toLocaleDateString()}</p>
        </button>
      </div>

      <div className="min-w-0 pl-12 md:pl-0">
        <KeyCopyButton icon={<KeyRound className="h-3 w-3" />} label="Key copied" value={apiKey.display} copiedMessage="Masked key identifier copied." />
      </div>

      <div className="flex min-w-0 items-center gap-2 pl-12 text-xs md:pl-0">
        <span className="truncate font-medium text-[var(--text)]">{apiKey.plan_name || "Custom plan"}</span>
        <span className="text-[var(--text-muted)]">·</span>
        <span className={modelCount > 0 ? "truncate text-amber-600 dark:text-amber-400" : "truncate text-[var(--text-muted)]"}>
          {modelCount > 0 ? `${modelCount} model${modelCount > 1 ? "s" : ""}` : "Plan defaults"}
        </span>
      </div>

      <div className="flex items-center gap-1 pl-11 md:justify-end md:pl-0">
        <KeyCopyButton
          icon={<Link2 className="h-4 w-4" />}
          label="Portal link copied"
          value={portalUrl}
          copiedMessage="Owner usage portal link copied."
          className="w-10 justify-center px-0 [&_span:nth-child(2)]:hidden [&_svg:last-child]:hidden"
        />
        <button
          type="button"
          onClick={onToggle}
          disabled={togglePending}
          aria-label={apiKey.disabled ? "Enable key" : "Disable key"}
          className="flex h-10 w-10 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
          title={apiKey.disabled ? "Enable key" : "Disable key"}
        >
          {apiKey.disabled ? <ToggleLeft className="h-4 w-4" /> : <ToggleRight className="h-4 w-4" />}
        </button>
        <button
          type="button"
          onClick={onRevoke}
          aria-label={`Revoke ${apiKey.name}`}
          className="flex h-10 w-10 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-red-100 hover:text-red-600 dark:hover:bg-red-900/30 focus:outline-none focus-visible:ring-2 focus-visible:ring-red-400/50"
          title="Revoke key"
        >
          <Trash2 className="h-4 w-4" />
        </button>
        <Button variant="ghost" onClick={onConfigure} className="ml-1">
          Details
          <ArrowRight className="h-4 w-4" />
        </Button>
      </div>
    </article>
  );
}

export function KeysPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys() });
  const access = useQuery({ queryKey: ["access-settings"], queryFn: () => api.accessSettings() });

  const [modalOpen, setModalOpen] = useState(false);
  const [step, setStep] = useState<1 | 2 | 3 | 4>(1);
  const [statusFilter, setStatusFilter] = useState<"all" | "active" | "inactive">("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [sortKey, setSortKey] = useState<"created_desc" | "created_asc" | "name_asc" | "name_desc">("created_desc");

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

  const handleBulkDelete = () => {
    const ids = Array.from(selectedIds);
    if (!ids.length) return;
    if (!confirm(`Revoke ${ids.length} key${ids.length > 1 ? "s" : ""}? This cannot be undone.`)) return;
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

  return (
    <>
      <PageHeader
        title="API Keys"
        icon={KeyRound}
        description="Manage authentication keys, owner portal links, model access, and spend controls."
        action={
          <Button onClick={openModal}>
            <Plus className="h-4 w-4" />
            New key
          </Button>
        }
      />

      <Modal
        open={modalOpen}
        onClose={closeModal}
        title={step === 4 ? "Key created" : "Create API key"}
        subtitle={
          step === 1
            ? "Name your key so you can identify it later."
            : step === 2
              ? "Choose a plan or set custom limits."
              : step === 3
                ? "Optionally override plan settings for this key."
                : undefined
        }
      >
        {step === 1 && <StepName name={name} setName={setName} onNext={() => setStep(2)} />}
        {step === 2 && (
          <StepPlanSelect
            plans={plans.data?.plans ?? []}
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
              created.allowed_models && created.allowed_models.length >0
                ? created.allowed_models.join(", ")
                : selectedPlanId !== "custom"
                  ? (plans.data?.plans ?? []).find((p) => p.id === selectedPlanId)?.allowed_models?.join(", ") || "all models"
                  : "all models"
            }
          />
        )}
      </Modal>

      {(() => {
        const summary = getKeySummary(keys.data?.keys ?? []);
        return (
          <Card className="mb-4 shadow-none">
            <div className="grid grid-cols-2 divide-x divide-y divide-[var(--border)] sm:grid-cols-4 sm:divide-y-0">
              <SummaryItem label="Total keys" value={summary.total} icon={KeyRound} />
              <SummaryItem label="Active" value={summary.active} icon={Activity} />
              <SummaryItem label="Disabled" value={summary.disabled} icon={Ban} tone="danger" />
              <SummaryItem label="Restricted" value={summary.restricted} icon={ListFilter} tone="warning" />
            </div>
          </Card>
        );
      })()}

      <Card className="overflow-hidden">
        <CardHeader
          title={selectedIds.size > 0 ? `${selectedIds.size} selected` : "Key inventory"}
          description="Copy identifiers, share owner portals, and control access from one place."
          action={
            selectedIds.size > 0 ? (
              <div className="flex items-center gap-2">
                <button
                  onClick={clearSelection}
                  className="rounded-lg px-3 py-1.5 text-xs font-medium text-[var(--text-muted)] transition-colors hover:text-[var(--text)]"
                >
                  Clear
                </button>
                <Button variant="danger" onClick={handleBulkDelete} disabled={bulkRemove.isPending}>
                  <Trash2 className="h-3.5 w-3.5" />
                  Revoke {selectedIds.size}
                </Button>
              </div>
            ) : undefined
          }
        />
        {keys.isLoading ? (
          <Spinner />
        ) : !keys.data?.keys?.length ? (
          <KeyEmptyState onCreate={openModal} />
        ) : (
          <div>
            <div className="flex flex-col gap-3 border-b border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3 lg:flex-row lg:items-center lg:justify-between lg:px-5">
              <label className="flex min-h-10 cursor-pointer items-center gap-3 rounded-lg pr-3">
                <input
                  type="checkbox"
                  checked={visibleKeys.length >0 && visibleKeys.every((k) => selectedIds.has(k.id))}
                  onChange={toggleSelectAll}
                  className="h-4 w-4 rounded border-[var(--border)] accent-[var(--color-accent)]"
                  aria-label="Select all visible API keys"
                />
                <span className="text-xs font-semibold text-[var(--text-muted)]">
                  Select all <span className="tabular-nums">({visibleKeys.length})</span>
                </span>
              </label>
              <div className="flex flex-1 flex-col gap-2 sm:flex-row lg:max-w-3xl lg:justify-end">
                <div className="relative min-w-0 flex-1 lg:max-w-sm">
                  <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
                  <input
                    type="text"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder="Search keys…"
                    className="min-h-10 w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] py-2 pl-9 pr-10 text-sm transition-[border-color,box-shadow] placeholder:text-[var(--text-muted)] hover:border-[var(--border-strong)] focus:border-accent-400 focus:outline-none focus:ring-2 focus:ring-accent-400/30"
                  />
                  {searchQuery && (
                    <button
                      type="button"
                      onClick={() => setSearchQuery("")}
                      className="absolute right-0 top-1/2 flex h-10 w-10 -translate-y-1/2 items-center justify-center rounded-lg text-[var(--text-muted)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
                      aria-label="Clear search"
                    >
                      <X className="h-4 w-4" />
                    </button>
                  )}
                </div>
                <label className="sr-only" htmlFor="key-sort">Sort keys</label>
                <Select id="key-sort" className="w-full sm:w-40" value={sortKey} onChange={(e) => setSortKey(e.target.value as typeof sortKey)}>
                  <option value="created_desc">Newest first</option>
                  <option value="created_asc">Oldest first</option>
                  <option value="name_asc">Name A–Z</option>
                  <option value="name_desc">Name Z–A</option>
                </Select>
                <label className="sr-only" htmlFor="key-status">Filter by status</label>
                <Select id="key-status" className="w-full sm:w-32" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value as typeof statusFilter)}>
                  <option value="all">All status</option>
                  <option value="active">Active</option>
                  <option value="inactive">Inactive</option>
                </Select>
              </div>
            </div>
            {visibleKeys.length ===0 ? (
              <div className="px-6 py-10 text-center text-sm text-[var(--text-muted)]">
                No keys match your search or filters.
              </div>
            ) : (
              <div className="divide-y divide-[var(--border)]">
                {pagination.paged.map((k) => (
                  <KeyRow
                    key={k.id}
                    apiKey={k}
                    selected={selectedIds.has(k.id)}
                    onSelect={() => toggleSelect(k.id)}
                    onToggle={() => toggleDisabled.mutate({ id: k.id, disabled: !k.disabled })}
                    onConfigure={() => navigate(`/keys/${k.id}`)}
                    onRevoke={() => {
                      if (!confirm(`Revoke ${k.name}? This cannot be undone.`)) return;
                      remove.mutate(k.id);
                    }}
                    togglePending={toggleDisabled.isPending}
                  />
                ))}
              </div>
            )}
            <TablePagination
              page={pagination.page}
              pages={pagination.pages}
              total={pagination.total}
              onPage={pagination.setPage}
            />
          </div>
        )}
      </Card>
    </>
  );
}

/* ── Step 1: Name ───────────────────────────────────────────────── */

function StepName({
  name,
  setName,
  onNext,
}: {
  name: string;
  setName: (v: string) => void;
  onNext: () => void;
}) {
  return (
    <div className="space-y-4 px-6 py-5">
      <Field label="Key name">
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
      </Field>
      <div className="flex gap-2 pt-1">
        <Button className="flex-1" onClick={onNext} disabled={!name.trim()}>
          Next
          <ArrowRight className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}

/* ── Step 2: Plan Select ────────────────────────────────────────── */

function StepPlanSelect({
  plans,
  selectedPlanId,
  setSelectedPlanId,
  onBack,
  onNext,
}: {
  plans: Plan[];
  selectedPlanId: string;
  setSelectedPlanId: (v: string) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  return (
    <div className="space-y-4 px-6 py-5">
      <p className="text-xs text-[var(--text-muted)]">
        Select a plan to inherit its budget rules, or choose Custom to set everything yourself.
      </p>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {plans.map((p) => (
          <button
            key={p.id}
            type="button"
            onClick={() => setSelectedPlanId(p.id)}
            className={`rounded-xl border px-4 py-3 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40 ${
              selectedPlanId === p.id
                ? "border-accent-400 bg-accent-500/10"
                : "border-[var(--border)] bg-[var(--bg-subtle)] hover:bg-[var(--bg)]"
            }`}
          >
            <div className="flex items-center gap-2">
              <Wallet className="h-4 w-4 text-[var(--text-muted)]" />
              <span className="text-sm font-medium">{p.name}</span>
            </div>
            <div className="mt-1.5 space-y-0.5 text-xs text-[var(--text-muted)]">
              {p.limit_micros > 0 && <p>{microsToUSD(p.limit_micros)} / {p.period}</p>}
              {p.limit_tokens > 0 && <p>{formatTokens(p.limit_tokens)} tokens / {p.period}</p>}
              {p.limit_micros === 0 && p.limit_tokens === 0 && <p>No spend limit</p>}
              {(p.allowed_models ?? []).length > 0 && <p>{(p.allowed_models ?? []).length} model restriction(s)</p>}
              <p>{p.key_count} key{p.key_count !== 1 ? "s" : ""}</p>
            </div>
          </button>
        ))}

        {/* Custom option */}
        <button
          type="button"
          onClick={() => setSelectedPlanId("custom")}
          className={`rounded-xl border border-dashed px-4 py-3 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40 ${
            selectedPlanId === "custom"
              ? "border-accent-400 bg-accent-500/10"
              : "border-[var(--border)] bg-[var(--bg-subtle)] hover:bg-[var(--bg)]"
          }`}
        >
          <div className="flex items-center gap-2">
            <Wrench className="h-4 w-4 text-[var(--text-muted)]" />
            <span className="text-sm font-medium">Custom</span>
          </div>
          <div className="mt-1.5 text-xs text-[var(--text-muted)]">
            <p>No preset</p>
            <p>Set everything yourself</p>
          </div>
        </button>
      </div>

      <div className="flex gap-2 pt-2 border-t border-[var(--border)]">
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft className="h-4 w-4" />
          Back
        </Button>
        <div className="flex-1" />
        <Button onClick={onNext}>
          Next
          <ArrowRight className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}

/* ── Step 3: Configure (plan details / custom) ──────────────────── */

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
      <div className="space-y-4 px-6 py-5">
        <div className="flex gap-3">
          <div className="flex-1">
            <Field label="Limit (USD)">
              <Input
                type="number"
                min="0"
                step="0.01"
                value={budgetLimit}
                onChange={(e) => setBudgetLimit(e.target.value)}
                placeholder="50.00"
              />
            </Field>
          </div>
          <div className="flex-1">
            <Field label="Limit (Tokens)">
              <FormattedTokenInput
                value={budgetLimitTokens}
                onChange={setBudgetLimitTokens}
                placeholder="100000000"
              />
            </Field>
          </div>
          <div className="w-36">
            <Field label="Period">
              <Select value={budgetPeriod} onChange={(e) => setBudgetPeriod(e.target.value)}>
                {budgetPeriods.map((p) => (
                  <option key={p.value} value={p.value}>{p.label}</option>
                ))}
              </Select>
            </Field>
          </div>
        </div>

        <Field label="Allowed models">
          <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} />
          <p className="mt-1 text-[10px] text-[var(--text-muted)]">
            Select models or add custom patterns with * wildcard (e.g. claude-*)
          </p>
        </Field>

        <div className="flex items-end gap-6">
          <div className="w-40">
            <Field label="Alert threshold (%)">
              <Input
                type="number"
                min="1"
                max="100"
                value={budgetAlertPct}
                onChange={(e) => setBudgetAlertPct(parseInt(e.target.value) || 80)}
              />
            </Field>
          </div>
          <div className="flex items-center gap-2 pb-0.5">
            <Toggle checked={budgetHardCutoff} onChange={setBudgetHardCutoff} />
            <span className="text-sm">Hard cutoff (block when exhausted)</span>
          </div>
        </div>

        <div className="flex gap-2 pt-2 border-t border-[var(--border)]">
          <Button variant="ghost" onClick={onBack}>
            <ArrowLeft className="h-4 w-4" />
            Back
          </Button>
          <div className="flex-1" />
          <Button variant="ghost" onClick={onCreate} disabled={isPending}>
            {isPending ? "Creating…" : "Skip budget"}
          </Button>
          <Button onClick={onCreate} disabled={isPending}>
            {isPending ? "Creating…" : "Create key"}
          </Button>
        </div>
      </div>
    );
  }

  // Plan selected — show summary + optional override toggle
  return (
    <div className="space-y-4 px-6 py-5">
      {/* Plan summary card */}
      {selectedPlan && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
          <div className="flex items-center gap-2">
            <Wallet className="h-4 w-4 text-accent-500" />
            <span className="text-sm font-medium">{selectedPlan.name}</span>
            <Badge>{selectedPlan.period}</Badge>
            {selectedPlan.hard_cutoff ? <Badge tone="danger">hard cutoff</Badge> : <Badge tone="neutral">advisory</Badge>}
          </div>
          {selectedPlan.description && (
            <p className="mt-1 text-xs text-[var(--text-muted)]">{selectedPlan.description}</p>
          )}
          <div className="mt-2 flex flex-wrap items-center gap-3 text-xs text-[var(--text-muted)]">
            {selectedPlan.limit_micros > 0 && (
              <span className="flex items-center gap-1"><DollarSign className="h-3 w-3" />{microsToUSD(selectedPlan.limit_micros)}</span>
            )}
            {selectedPlan.limit_tokens > 0 && (
              <span className="flex items-center gap-1"><Gauge className="h-3 w-3" />{formatTokens(selectedPlan.limit_tokens)} tok</span>
            )}
            {selectedPlan.limit_micros === 0 && selectedPlan.limit_tokens === 0 && <span>No spend limit</span>}
            <span>Alert at {selectedPlan.alert_pct}%</span>
          </div>
          {models.length > 0 && (
            <p className="mt-1.5 text-xs text-[var(--text-muted)]">Models: {models.join(", ")}</p>
          )}
        </div>
      )}

      {/* Override toggle */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
        <div className="flex items-center justify-between gap-4">
          <div>
            <p className="text-sm font-medium">Customize for this key</p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">
              Override plan limits with per-key settings.
            </p>
          </div>
          <Toggle checked={customizePlan} onChange={setCustomizePlan} />
        </div>
      </div>

      {/* Override fields */}
      {customizePlan && (
        <div className="space-y-4 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
          <div className="flex gap-3">
            <div className="flex-1">
              <Field label="Override USD limit">
                <Input
                  type="number"
                  min="0"
                  step="0.01"
                  value={budgetLimit}
                  onChange={(e) => setBudgetLimit(e.target.value)}
                  placeholder="Leave empty to use plan"
                />
              </Field>
            </div>
            <div className="flex-1">
              <Field label="Override token limit">
                <FormattedTokenInput
                  value={budgetLimitTokens}
                  onChange={setBudgetLimitTokens}
                  placeholder="Leave empty to use plan"
                />
              </Field>
            </div>
          </div>
          <Field label="Override allowed models">
            <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} />
            <p className="mt-1 text-[10px] text-[var(--text-muted)]">
              Leave empty to use plan's model restrictions.
            </p>
          </Field>
        </div>
      )}

      <div className="flex gap-2 pt-2 border-t border-[var(--border)]">
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft className="h-4 w-4" />
          Back
        </Button>
        <div className="flex-1" />
        <Button onClick={onCreate} disabled={isPending}>
          {isPending ? "Creating…" : "Create key"}
        </Button>
      </div>
    </div>
  );
}

/* ── Step 4: Success / Copy ─────────────────────────────────────── */

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
  const [copiedUrl, setCopiedUrl] = useState(false);
  const [copiedAll, setCopiedAll] = useState(false);
  const portalUrl = `${window.location.origin}/portal?id=${created.id}`;

  const shareText = [
    "Keirouter",
    `Endpoint : ${endpointUrl}`,
    `API Key : ${created.key}`,
    `Portal Monitoring : ${portalUrl}`,
    `Plan : ${planName}`,
    `Available model : ${availableModelsText}`,
  ].join("\n");

  return (
    <div className="space-y-4 px-6 py-5">
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
        <p className="text-xs font-medium text-[var(--text-muted)]">Your new key — copy it now, it won't be shown again.</p>
        <div className="mt-2 flex items-center gap-2">
          <code className="flex-1 overflow-x-auto rounded-lg bg-[var(--bg-elevated)] px-3 py-2.5 font-mono text-sm">
            {created.key}
          </code>
          <Button
            onClick={() => {
              navigator.clipboard.writeText(created.key);
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            }}
          >
            {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            {copied ? "Copied" : "Copy"}
          </Button>
        </div>
      </div>

      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
        <p className="text-xs font-medium text-[var(--text-muted)]">Telemetry Portal URL — share this with the key owner to let them track their usage.</p>
        <div className="mt-2 flex items-center gap-2">
          <code className="flex-1 overflow-x-auto rounded-lg bg-[var(--bg-elevated)] px-3 py-2.5 font-mono text-xs whitespace-nowrap">
            {portalUrl}
          </code>
          <Button
            onClick={() => {
              navigator.clipboard.writeText(portalUrl);
              setCopiedUrl(true);
              setTimeout(() => setCopiedUrl(false), 1500);
            }}
          >
            {copiedUrl ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            {copiedUrl ? "Copied" : "Copy"}
          </Button>
        </div>
      </div>

      {created.budget && (
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
          <p className="text-xs font-medium text-[var(--text-muted)]">Plan attached</p>
          <p className="mt-0.5 text-sm">
            {created.budget.limit_micros > 0 && `$${(created.budget.limit_micros / 1_000_000).toFixed(2)}`}
            {created.budget.limit_micros > 0 && created.budget.limit_tokens > 0 && " + "}
            {created.budget.limit_tokens > 0 &&
              `${formatTokenLimit(String(created.budget.limit_tokens))} tokens`}
            {` / ${created.budget.period}`}
            {created.budget.hard_cutoff ? " (hard cutoff)" : ""}
          </p>
        </div>
      )}

      {created.allowed_models && created.allowed_models.length > 0 && (
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
          <p className="text-xs font-medium text-[var(--text-muted)]">Allowed models</p>
          <p className="mt-0.5 text-sm">{created.allowed_models.join(", ")}</p>
        </div>
      )}

      <div className="rounded-xl border border-accent-400/40 bg-accent-500/5 p-4">
        <div className="flex items-center justify-between gap-2">
          <p className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)]">Ready to copy</p>
          <Button
            variant="ghost"
            onClick={() => {
              navigator.clipboard.writeText(shareText);
              setCopiedAll(true);
              setTimeout(() => setCopiedAll(false),2000);
            }}
          >
            {copiedAll ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            {copiedAll ? "Copied" : "Copy all"}
          </Button>
        </div>
        <pre className="mt-2 overflow-x-auto rounded-lg bg-[var(--bg-elevated)] px-3 py-2.5 font-mono text-xs leading-relaxed text-[var(--text)] whitespace-pre-wrap">{shareText}</pre>
      </div>

      <Button className="w-full" onClick={onClose}>
        Done
      </Button>
    </div>
  );
}
