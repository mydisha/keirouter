import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Wallet,
  Plus,
  Pencil,
  Trash2,
  KeyRound,
  ShieldCheck,
  ShieldAlert,
  DollarSign,
  Gauge,
  Bell,
  Cpu,
  type LucideIcon,
} from "lucide-react";
import { api, type Plan } from "../lib/api";
import { FormattedTokenInput, ModelMultiSelect } from "../components/ModelSelect";
import { microsToUSD, formatTokens } from "../lib/format";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import {
  Card,
  Button,
  Input,
  Select,
  Field,
  Badge,
  Spinner,
  ErrorBanner,
  Toggle,
  Modal,
} from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";

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

export function PlansPage() {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const toast = useToast();

  const plans = useQuery({
    queryKey: ["plans"],
    queryFn: () => api.listPlans(),
  });

  const [showCreate, setShowCreate] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);

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

  const totalKeys = planList.reduce((sum, p) => sum + p.key_count, 0);
  const enforcedCount = planList.filter((p) => p.hard_cutoff).length;

  return (
    <>
      <PageHeader
        title="Plans"
        icon={Wallet}
        description="Reusable templates for API key budget limits, rate limits, and model restrictions."
        action={
          <Button onClick={() => setShowCreate(true)}>
            <Plus className="h-4 w-4" />
            New plan
          </Button>
        }
      />

      <Modal
        open={showCreate}
        onClose={() => setShowCreate(false)}
        title="Create plan"
        subtitle="Define budget, rate limit, and model rules as a reusable template."
        maxWidth="max-w-xl"
      >
        <PlanForm onClose={() => setShowCreate(false)} />
      </Modal>

      <Modal
        open={!!editingId}
        onClose={() => setEditingId(null)}
        title="Edit plan"
        subtitle={editingPlan ? `Editing "${editingPlan.name}"` : undefined}
        maxWidth="max-w-xl"
      >
        {editingPlan && (
          <PlanForm
            key={editingPlan.id}
            plan={editingPlan}
            onClose={() => setEditingId(null)}
          />
        )}
      </Modal>

      {/* Overview summary — at-a-glance context before the plan list */}
      {!plans.isLoading && planList.length > 0 && (
        <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <OverviewStat
            icon={Wallet}
            tone="secondary"
            label="Total plans"
            value={planList.length.toString()}
            hint={planList.length === 1 ? "template" : "templates"}
          />
          <OverviewStat
            icon={KeyRound}
            tone="accent"
            label="Keys assigned"
            value={totalKeys.toString()}
            hint={`across ${planList.length} plan${planList.length === 1 ? "" : "s"}`}
          />
          <OverviewStat
            icon={ShieldAlert}
            tone="warning"
            label="Hard cutoff"
            value={enforcedCount.toString()}
            hint={`${planList.length - enforcedCount} advisory`}
          />
        </div>
      )}

      <div className="mb-4 flex items-center gap-2.5">
        <ShieldCheck className="h-4 w-4 text-[var(--text-muted)]" />
        <h2 className="text-sm font-medium text-[var(--text-muted)]">
          All plans
        </h2>
        <div className="flex-1 border-t border-[var(--border)]" />
        <span className="text-xs text-[var(--text-muted)]">API keys inherit rules from their assigned plan</span>
      </div>

      {plans.isLoading ? (
        <div className="py-12">
          <Spinner />
        </div>
      ) : planList.length === 0 ? (
        <div className="rounded-2xl border-2 border-dashed border-[var(--border)] bg-[var(--bg)] px-6 py-20 text-center">
          <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--border)] bg-[var(--bg-subtle)]">
            <ShieldCheck className="h-8 w-8 text-[var(--text-muted)]" strokeWidth={1.5} />
          </div>
          <h3 className="text-lg font-medium text-[var(--text)]">No plans configured</h3>
          <p className="mx-auto mt-2 max-w-sm text-sm text-[var(--text-muted)]">
            Plans define budget constraints, rate limits, and model access. Create your first plan to start organizing API keys.
          </p>
          <Button className="mt-6" onClick={() => setShowCreate(true)}>
            <Plus className="h-4 w-4" />
            Create your first plan
          </Button>
        </div>
      ) : (
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {planList.map((p) => (
            <PlanRow
              key={p.id}
              plan={p}
              onEdit={() => setEditingId(p.id)}
              onDelete={async () => {
                if (p.key_count > 0) {
                  toast.error("Cannot delete", `This plan has ${p.key_count} key(s) assigned. Reassign them first.`);
                  return;
                }
                if (await confirm({ title: `Delete plan “${p.name}”?`, tone: "danger" })) {
                  remove.mutate(p.id);
                }
              }}
            />
          ))}
        </div>
      )}
    </>
  );
}

/* ── Overview stat ───────────────────────────────────────────────── */

function OverviewStat({
  icon: Icon,
  tone,
  label,
  value,
  hint,
}: {
  icon: LucideIcon;
  tone: "secondary" | "accent" | "warning";
  label: string;
  value: string;
  hint?: string;
}) {
  const toneClasses: Record<string, string> = {
    secondary: "bg-secondary-100 text-secondary-700 dark:bg-secondary-800/40 dark:text-secondary-200",
    accent: "bg-accent-100 text-accent-700 dark:bg-accent-800/40 dark:text-accent-200",
    warning: "bg-[color:var(--color-warning)]/15 text-[color:var(--color-warning)]",
  };
  return (
    <div className="flex items-center gap-3 rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] px-4 py-3.5 shadow-sm">
      <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ${toneClasses[tone]}`}>
        <Icon className="h-5 w-5" strokeWidth={2} />
      </div>
      <div className="min-w-0">
        <p className="text-[12px] font-medium text-[var(--text-muted)]">{label}</p>
        <p className="mt-0.5 flex items-baseline gap-1.5">
          <span className="text-2xl font-semibold tabular-nums text-[var(--text)]">{value}</span>
          {hint && <span className="truncate text-xs text-[var(--text-muted)]">{hint}</span>}
        </p>
      </div>
    </div>
  );
}

/* ── Limit stat (inside a plan card) ─────────────────────────────── */

function LimitStat({
  label,
  value,
  suffix,
}: {
  label: string;
  value: string | null;
  suffix?: string;
}) {
  const unlimited = value === null;
  return (
    <div className="min-w-0">
      <dt className="truncate text-[11.5px] font-medium text-[var(--text-muted)]">
        {label}
      </dt>
      <dd className={`mt-0.5 truncate font-mono text-sm ${unlimited ? "text-[var(--text-muted)]" : "font-medium text-[var(--text)]"}`}>
        {unlimited ? "Unlimited" : value}
        {!unlimited && suffix && <span className="ml-1 font-sans text-xs font-normal text-[var(--text-muted)]">{suffix}</span>}
      </dd>
    </div>
  );
}

function SectionLabel({ icon: Icon, children }: { icon: LucideIcon; children: React.ReactNode }) {
  return (
    <div className="mb-2.5 flex items-center gap-1.5 text-[12px] font-medium text-[var(--text-muted)]">
      <Icon className="h-3.5 w-3.5 shrink-0" strokeWidth={2} />
      {children}
    </div>
  );
}

/* ── Plan row ────────────────────────────────────────────────────── */

function PlanRow({
  plan: p,
  onEdit,
  onDelete,
}: {
  plan: Plan;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const hasUSD = p.limit_micros > 0;
  const hasTokens = p.limit_tokens > 0;
  const models = p.allowed_models ?? [];
  const period = periodLabels[p.period] ?? p.period;

  return (
    <Card className="flex flex-col">
      {/* Header */}
      <div className="flex items-start justify-between gap-3 px-5 pt-5 pb-4">
        <div className="flex min-w-0 items-start gap-3">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-line bg-subtle text-fg-muted">
            <Wallet className="h-5 w-5" strokeWidth={2} />
          </div>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="truncate text-base font-semibold text-[var(--text)]">{p.name}</h3>
              {p.hard_cutoff ? (
                <Badge tone="danger" title="Requests are blocked once the budget is exhausted">hard cutoff</Badge>
              ) : (
                <Badge tone="neutral" title="Usage is tracked but requests are never blocked">advisory</Badge>
              )}
            </div>
            {p.description ? (
              <p className="mt-0.5 line-clamp-2 text-xs text-[var(--text-muted)]">{p.description}</p>
            ) : (
              <p className="mt-0.5 text-xs italic text-[var(--text-muted)] opacity-70">No description</p>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button variant="ghost" onClick={onEdit} className="px-2.5 py-2" title="Edit plan">
            <Pencil className="h-4 w-4 text-[var(--text-muted)]" />
          </Button>
          <Button variant="ghost" onClick={onDelete} className="px-2.5 py-2 text-[var(--text-muted)] hover:bg-[color:var(--color-danger)]/10 hover:text-[color:var(--color-danger)]" title="Delete plan">
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* Body: budget + rate limits, clearly separated */}
      <div className="flex-1 divide-y divide-[var(--border)] border-t border-[var(--border)]">
        <div className="px-5 py-4">
          <SectionLabel icon={DollarSign}>Budget · per {period}</SectionLabel>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-3">
            <LimitStat
              label="Spend"
              value={hasUSD ? microsToUSD(p.limit_micros) : null}
            />
            <LimitStat
              label="Tokens"
              value={hasTokens ? formatTokens(p.limit_tokens) : null}
            />
          </dl>
        </div>

        <div className="px-5 py-4">
          <SectionLabel icon={Gauge}>Rate limits · per key</SectionLabel>
          <dl className="grid grid-cols-3 gap-x-4 gap-y-3">
            <LimitStat
              label="RPM"
              value={p.rpm_limit > 0 ? p.rpm_limit.toLocaleString() : null}
            />
            <LimitStat
              label="TPM"
              value={p.tpm_limit > 0 ? formatTokens(p.tpm_limit) : null}
            />
            <LimitStat
              label="Concurrent"
              value={p.concurrency_limit > 0 ? p.concurrency_limit.toLocaleString() : null}
            />
          </dl>
        </div>

        <div className="px-5 py-4">
          <SectionLabel icon={Cpu}>Allowed models</SectionLabel>
          <div className="flex flex-wrap gap-1.5">
            {models.length > 0 ? (
              models.map((m) => (
                <span
                  key={m}
                  className="rounded-md border border-[var(--border)] bg-[var(--bg-subtle)] px-2 py-0.5 font-mono text-[11px] text-[var(--text)]"
                >
                  {m}
                </span>
              ))
            ) : (
              <span className="text-xs text-[var(--text-muted)]">All models allowed</span>
            )}
          </div>
        </div>
      </div>

      {/* Footer: keys + alert */}
      <div className="mt-auto flex items-center justify-between border-t border-[var(--border)] bg-[var(--bg-subtle)] px-5 py-3 text-xs">
        <div className="flex items-center gap-1.5 text-[var(--text-muted)]">
          <KeyRound className="h-3.5 w-3.5" />
          <span>
            <span className="font-medium text-[var(--text)]">{p.key_count}</span>{" "}
            {p.key_count === 1 ? "key" : "keys"} assigned
          </span>
        </div>
        <div className="flex items-center gap-1.5 text-[var(--text-muted)]">
          <Bell className="h-3.5 w-3.5" />
          Alert at {p.alert_pct}%
        </div>
      </div>
    </Card>
  );
}

/* ── Plan form (create / edit) ───────────────────────────────────── */

function PlanForm({ plan, onClose }: { plan?: Plan; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();

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

  const usdLimit = parseUSD(limit);
  const tokenLimit = parseTokens(limitTokens);
  const rpm = parseNonNegativeInt(rpmLimit);
  const tpm = parseNonNegativeInt(tpmLimit);
  const concurrency = parseNonNegativeInt(concurrencyLimit);
  const validationError = validateRateLimits(rpmLimit, tpmLimit, concurrencyLimit);
  const canSubmit = name.trim().length > 0 && !validationError;

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

  return (
    <form
      className="flex flex-col max-h-[calc(100vh-10rem)]"
      onSubmit={handleSubmit}
    >
      <div className="px-5 py-4 space-y-6 overflow-y-auto min-h-0">
        {/* Name & Description */}
        <section className="space-y-3">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text-strong)]">Plan Details</h3>
            <p className="text-xs text-[var(--text-muted)]">Name this plan so you can identify it when assigning to keys.</p>
          </div>
          <Field label="Name">
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Pro"
              autoFocus
            />
          </Field>
          <Field label="Description (optional)">
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Full access with $100/month budget"
            />
          </Field>
        </section>

        <div className="h-px bg-[var(--border)] w-full" />

        {/* Budget limits */}
        <section className="space-y-3">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text-strong)]">Budget Limits</h3>
            <p className="text-xs text-[var(--text-muted)]">Keys assigned to this plan inherit these limits.</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Limit (USD)">
              <div className="relative">
                <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                  <DollarSign className="h-4 w-4 text-[var(--text-muted)]" />
                </div>
                <Input
                  type="number"
                  min="0"
                  step="0.01"
                  value={limit}
                  onChange={(e) => setLimit(e.target.value)}
                  placeholder="50.00"
                  className="pl-9"
                />
              </div>
            </Field>
            <Field label="Limit (Tokens)">
              <FormattedTokenInput
                value={limitTokens}
                onChange={setLimitTokens}
                placeholder="100000000"
              />
            </Field>
            <Field label="Period">
              <Select value={period} onChange={(e) => setPeriod(e.target.value)}>
                {periods.map((p) => (
                  <option key={p.value} value={p.value}>
                    {p.label}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
        </section>

        <div className="h-px bg-[var(--border)] w-full" />

        {/* Rate limits */}
        <section className="space-y-3">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text-strong)]">Rate Limits</h3>
            <p className="text-xs text-[var(--text-muted)]">Control burst traffic per API key. Leave blank or 0 for unlimited.</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Requests / min">
              <Input
                type="number"
                min="0"
                max={rateLimitRules.rpm.max}
                step="1"
                value={rpmLimit}
                onChange={(e) => setRpmLimit(e.target.value)}
                placeholder="60"
              />
            </Field>
            <Field label="Tokens / min">
              <FormattedTokenInput
                value={tpmLimit}
                onChange={setTpmLimit}
                placeholder="100000"
              />
            </Field>
            <Field label="Concurrent requests">
              <Input
                type="number"
                min="0"
                max={rateLimitRules.concurrency.max}
                step="1"
                value={concurrencyLimit}
                onChange={(e) => setConcurrencyLimit(e.target.value)}
                placeholder="5"
              />
            </Field>
          </div>
        </section>

        <div className="h-px bg-[var(--border)] w-full" />

        {/* Allowed models */}
        <section className="space-y-3">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text-strong)]">Model Restrictions</h3>
            <p className="text-xs text-[var(--text-muted)]">Leave empty to allow all models. Keys can override this.</p>
          </div>
          <Field label="Allowed models">
            <ModelMultiSelect value={allowedModels} onChange={setAllowedModels} />
            <p className="mt-1 text-[10px] text-[var(--text-muted)]">
              Select models or add custom patterns with * wildcard (e.g. claude-*)
            </p>
          </Field>
        </section>

        <div className="h-px bg-[var(--border)] w-full" />

        {/* Enforcement */}
        <section className="space-y-3">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text-strong)]">Enforcement</h3>
            <p className="text-xs text-[var(--text-muted)]">How to handle budget exhaustion.</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Alert threshold (%)">
              <div className="relative">
                <Input
                  type="number"
                  min="1"
                  max="100"
                  value={alertPct}
                  onChange={(e) => setAlertPct(clampAlertPct(parseInt(e.target.value, 10)))}
                  className="pr-8"
                />
                <div className="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-3">
                  <span className="text-sm text-[var(--text-muted)]">%</span>
                </div>
              </div>
            </Field>
            <div className="flex items-center justify-between gap-4 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
              <div className="min-w-0">
                <p className="text-sm font-medium">Hard cutoff</p>
                <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                  {hardCutoff ? "Block requests when exhausted." : "Track usage only."}
                </p>
              </div>
              <Toggle checked={hardCutoff} onChange={setHardCutoff} />
            </div>
          </div>
        </section>

        {validationError && <ErrorBanner message={validationError} />}
        {error && error !== validationError && <ErrorBanner message={error} />}
      </div>

      <div className="shrink-0 flex gap-3 border-t border-[var(--border)] bg-[var(--bg-subtle)] px-6 py-4 rounded-b-xl">
        <div className="flex-1" />
        <Button variant="ghost" type="button" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" disabled={isPending || !canSubmit}>
          {isPending ? "Saving…" : isEdit ? "Save changes" : "Create plan"}
        </Button>
      </div>
    </form>
  );
}