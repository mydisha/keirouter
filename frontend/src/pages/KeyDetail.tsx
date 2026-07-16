import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  CalendarDays,
  Check,
  Copy,
  Cpu,
  KeyRound,
  Layers3,
  Save,
  Settings as SettingsIcon,
  Shield,
} from "lucide-react";
import {
  api,
  type APIKey,
  type GuardrailPolicyConfig,
  type Plan,
} from "../lib/api";
import { useToast } from "../components/Toast";
import { GuardrailEditor } from "../components/GuardrailEditor";
import { ModelAccessList, ModelMultiSelect } from "../components/ModelSelect";
import {
  Badge,
  Button,
  Card,
  CardHeader,
  EmptyState,
  Spinner,
  TabBar,
  Toggle,
} from "../components/ui";

type Tab = "general" | "models" | "guardrails";

const TABS = [
  { value: "general" as const, label: "General", icon: SettingsIcon },
  { value: "models" as const, label: "Models", icon: Cpu },
  { value: "guardrails" as const, label: "Guardrails", icon: Shield },
];

function useHashTab(defaultTab: Tab): [Tab, (t: Tab) => void] {
  const valid: Tab[] = TABS.map((t) => t.value);
  const read = (): Tab => {
    const hash = window.location.hash.replace("#", "");
    return valid.includes(hash as Tab) ? (hash as Tab) : defaultTab;
  };
  const [tab, setTab] = useState<Tab>(read);
  useEffect(() => {
    const onHash = () => setTab(read());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return [
    tab,
    (next: Tab) => {
      window.history.replaceState(null, "", `#${next}`);
      setTab(next);
    },
  ];
}

export function KeyDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const toast = useToast();
  const [tab, setTab] = useHashTab("general");
  const [copied, setCopied] = useState(false);

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

  if (keys.isLoading) {
    return <Spinner />;
  }
  if (!key) {
    return (
      <Card>
        <EmptyState title="API key not found" hint="The key may have been revoked. Return to API Keys to choose another key." />
        <div className="flex justify-center pb-8">
          <Button variant="ghost" onClick={() => navigate("/keys")}>Back to API keys</Button>
        </div>
      </Card>
    );
  }

  const copyIdentifier = () => {
    navigator.clipboard.writeText(key.display).then(
      () => {
        setCopied(true);
        toast.success("Key identifier copied");
        window.setTimeout(() => setCopied(false), 1600);
      },
      () => toast.error("Copy failed", "Your browser blocked clipboard access."),
    );
  };

  return (
    <div className="space-y-4">
      <button
        type="button"
        onClick={() => navigate("/keys")}
        className="inline-flex min-h-10 items-center gap-2 rounded-lg px-2 text-sm font-medium text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
      >
        <ArrowLeft className="h-4 w-4" />
        API keys
      </button>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-4">
          <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl border border-secondary-200/70 bg-secondary-50 text-secondary-700 shadow-sm dark:border-secondary-900/60 dark:bg-secondary-950/30 dark:text-secondary-200">
            <KeyRound className="h-5 w-5" />
          </div>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-2xl font-semibold tracking-tight text-[var(--text)]">{key.name}</h1>
              <Badge tone={key.disabled ? "danger" : "success"}>{key.disabled ? "Disabled" : "Active"}</Badge>
            </div>
            <button
              type="button"
              onClick={copyIdentifier}
              className="group mt-1 inline-flex min-h-8 max-w-full items-center gap-2 rounded-lg text-sm text-[var(--text-muted)] transition-colors hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
            >
              <span className="truncate font-mono">{key.display}</span>
              {copied ? <Check className="h-3.5 w-3.5 text-emerald-600" /> : <Copy className="h-3.5 w-3.5 opacity-60 group-hover:opacity-100" />}
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-[var(--text-muted)]">
          <span className="inline-flex items-center gap-1.5"><Layers3 className="h-3.5 w-3.5" />{key.plan_name || "Custom plan"}</span>
          <span className="inline-flex items-center gap-1.5"><CalendarDays className="h-3.5 w-3.5" />Created {new Date(key.created_at).toLocaleDateString()}</span>
        </div>
      </div>

      <TabBar tabs={TABS} active={tab} onChange={setTab} />

      {tab === "general" && <GeneralTab apiKey={key} />}
      {tab === "models" && <ModelsTab apiKey={key} plan={plan} plansLoading={plans.isLoading} />}
      {tab === "guardrails" && <GuardrailsTab apiKey={key} />}
    </div>
  );
}

function InfoItem({ label, value, mono = false }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
      <dt className="text-[11px] font-semibold uppercase tracking-[0.14em] text-[var(--text-muted)]">{label}</dt>
      <dd className={`mt-1.5 break-words text-sm font-medium text-[var(--text)] ${mono ? "font-mono text-xs" : ""}`}>{value}</dd>
    </div>
  );
}

function GeneralTab({ apiKey }: { apiKey: APIKey }) {
  const qc = useQueryClient();
  const toast = useToast();
  const toggle = useMutation({
    mutationFn: () => api.updateKey(apiKey.id, { disabled: !apiKey.disabled }),
    onSuccess: (updated) => {
      qc.invalidateQueries({ queryKey: ["keys"] });
      toast.success(updated.disabled ? "Key disabled" : "Key enabled", updated.disabled ? "New requests using this key will be rejected." : "This key can authenticate requests again.");
    },
    onError: (error) => toast.error("Key update failed", error instanceof Error ? error.message : "Please try again."),
  });

  return (
    <Card>
      <CardHeader
        title="Key overview"
        description="Identity, assignment, and authentication status for this key."
        action={
          <Button variant={apiKey.disabled ? "primary" : "danger"} onClick={() => toggle.mutate()} disabled={toggle.isPending}>
            {apiKey.disabled ? "Enable key" : "Disable key"}
          </Button>
        }
      />
      <dl className="grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-3 sm:p-5">
        <InfoItem label="Name" value={apiKey.name} />
        <InfoItem label="Status" value={<Badge tone={apiKey.disabled ? "danger" : "success"}>{apiKey.disabled ? "Disabled" : "Active"}</Badge>} />
        <InfoItem label="Plan" value={apiKey.plan_name || "Custom plan"} />
        <InfoItem label="Key identifier" value={apiKey.display} mono />
        <InfoItem label="Internal ID" value={apiKey.id} mono />
        <InfoItem label="Created" value={new Date(apiKey.created_at).toLocaleString()} />
      </dl>
    </Card>
  );
}

function ModelsTab({ apiKey, plan, plansLoading }: { apiKey: APIKey; plan?: Plan; plansLoading: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const keyModels = apiKey.allowed_models ?? [];
  const inheritedModels = plan?.allowed_models ?? [];
  const effectiveModels = keyModels.length > 0 ? keyModels : inheritedModels;
  const source = keyModels.length > 0 ? "key" : inheritedModels.length > 0 ? "plan" : "all";
  const [models, setModels] = useState<string[]>(keyModels);
  const [editing, setEditing] = useState(false);

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

  if (plansLoading) return <Spinner />;

  return (
    <Card>
      <CardHeader
        title="Model access"
        description="Choose whether this key follows its plan or uses a narrower model allowlist."
        action={!editing ? (
          <Button variant="ghost" onClick={() => { setModels(effectiveModels); setEditing(true); }}>
            {source === "all" ? "Restrict models" : "Edit access"}
            <ArrowRight className="h-4 w-4" />
          </Button>
        ) : undefined}
      />
      <div className="space-y-5 p-4 sm:p-5">
        {!editing ? (
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
            <div className="flex items-start gap-3">
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-secondary-100 text-secondary-700 dark:bg-secondary-900/40 dark:text-secondary-200">
                <Cpu className="h-4 w-4" />
              </div>
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="text-sm font-semibold text-[var(--text)]">
                    {source === "all" ? "All available models" : `${effectiveModels.length} model${effectiveModels.length === 1 ? "" : "s"} available`}
                  </h3>
                  <Badge tone={source === "key" ? "warning" : "neutral"}>{source === "key" ? "Key override" : source === "plan" ? "Inherited from plan" : "No restriction"}</Badge>
                </div>
                <p className="mt-1 text-sm leading-5 text-[var(--text-muted)]">
                  {source === "all"
                    ? "This key can use every model available through its assigned plan."
                    : source === "plan"
                      ? `Access follows the ${plan?.name || "assigned"} plan. Add an override only when this key needs a narrower list.`
                      : "This key uses its own model allowlist instead of the plan default."}
                </p>
              </div>
            </div>
            {effectiveModels.length > 0 && (
              <div className="mt-4 border-t border-[var(--border)] pt-4">
                <ModelAccessList value={effectiveModels} />
              </div>
            )}
          </div>
        ) : (
          <>
            <div>
              <div className="mb-2 flex flex-wrap items-end justify-between gap-2">
                <div>
                  <h3 className="text-sm font-semibold">Allowed models</h3>
                  <p className="mt-0.5 text-xs text-[var(--text-muted)]">The catalog is loaded from your connected providers. Custom patterns are also supported.</p>
                </div>
                <Badge tone={models.length > 0 ? "warning" : "neutral"}>{models.length} selected</Badge>
              </div>
              <ModelMultiSelect value={models} onChange={setModels} />
              {models.length === 0 && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Select at least one model, or cancel to keep current access.</p>}
            </div>
            <div className="flex flex-col-reverse gap-2 border-t border-[var(--border)] pt-4 sm:flex-row sm:items-center sm:justify-between">
              <div>
                {keyModels.length > 0 && (
                  <Button variant="ghost" onClick={() => update.mutate([])} disabled={update.isPending}>Use plan defaults</Button>
                )}
              </div>
              <div className="flex gap-2 sm:justify-end">
                <Button variant="ghost" onClick={() => { setModels(keyModels); setEditing(false); }} disabled={update.isPending}>Cancel</Button>
                <Button onClick={() => update.mutate(models)} disabled={update.isPending || models.length === 0 || JSON.stringify(models) === JSON.stringify(keyModels)}>
                  <Save className="h-4 w-4" />
                  {update.isPending ? "Saving…" : "Save access"}
                </Button>
              </div>
            </div>
          </>
        )}
      </div>
    </Card>
  );
}

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

  if (policies.isLoading) return <Spinner />;
  if (policies.isError) {
    return (
      <Card>
        <EmptyState title="Unable to load key guardrails" hint="The existing override could not be verified. Retry before making changes." />
        <div className="flex justify-center pb-8">
          <Button variant="ghost" onClick={() => policies.refetch()}>Retry</Button>
        </div>
      </Card>
    );
  }

  const activeEffective = enabledDetectors(effective.data?.policy);
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader
          title="Per-key guardrails"
          description="Add a key-specific layer only when this key needs different protection from upstream policies."
          action={!editing ? (
            <Button variant="ghost" onClick={() => setEditing(true)}>{existing ? "Edit override" : "Create override"}</Button>
          ) : (
            <div className="flex items-center gap-2">
              {existing && (
                <Button variant="danger" onClick={() => { if (confirm("Remove this per-key override? The key will inherit upstream policies.")) remove.mutate(); }} disabled={remove.isPending}>Remove</Button>
              )}
              <Button onClick={() => save.mutate()} disabled={save.isPending || !dirty}>
                <Save className="h-4 w-4" />
                {save.isPending ? "Saving…" : "Save override"}
              </Button>
            </div>
          )}
        />
        <div className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5">
          <div className="flex items-center gap-3">
            <div className={`h-2.5 w-2.5 rounded-full ${existing && enabled ? "bg-emerald-500" : "bg-ink-300 dark:bg-ink-600"}`} />
            <div>
              <p className="text-sm font-semibold">{existing ? (enabled ? "Override active" : "Override paused") : editing ? "New override" : "Inherited policy"}</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">{existing ? "Changes here take priority for this API key." : editing ? "Configure detectors, test the policy, then save." : "Global, provider, model, and chain policies continue to apply."}</p>
            </div>
          </div>
          {editing && (
            <label className="flex min-h-10 items-center justify-between gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-3 text-sm font-medium">
              Apply this override
              <Toggle checked={enabled} onChange={setEnabled} />
            </label>
          )}
        </div>
      </Card>

      {editing && <GuardrailEditor value={config} onChange={setConfig} compact />}

      <Card>
        <CardHeader title="Effective protection" description="The final policy after all applicable guardrail layers are merged." />
        <div className="p-4 sm:p-5">
          {effective.isLoading ? (
            <Spinner />
          ) : effective.isError ? (
            <p className="text-sm text-red-600 dark:text-red-400">Unable to load the effective policy.</p>
          ) : (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                {activeEffective.length > 0 ? activeEffective.map((name) => <Badge key={name} tone="success">{name}</Badge>) : <Badge tone="neutral">No detectors active</Badge>}
                <span className="text-xs text-[var(--text-muted)]">{activeEffective.length} active detector{activeEffective.length === 1 ? "" : "s"}</span>
              </div>
              <details className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]">
                <summary className="flex min-h-10 cursor-pointer items-center px-4 text-sm font-medium text-[var(--text)]">View merged configuration</summary>
                <pre className="max-h-72 overflow-auto border-t border-[var(--border)] px-4 py-3 text-[11px] leading-5 text-[var(--text-muted)]">{JSON.stringify(effective.data?.policy ?? {}, null, 2)}</pre>
              </details>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
