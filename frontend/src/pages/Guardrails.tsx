import { useEffect, useMemo, useState, useRef, Fragment } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Shield,
  Globe,
  Boxes,
  Layers,
  Cpu,
  Key as KeyIcon,
  ScrollText,
  Plus,
  Trash2,
  Save,
  Sparkles,
  Download,
  Upload,
  Lock,
  Radio,
} from "lucide-react";
import {
  api,
  connectGuardrailLogStream,
  type GuardrailBundle,
  type GuardrailLogEntry,
  type GuardrailPolicy,
  type GuardrailPolicyConfig,
  type GuardrailScope,
  type GuardrailTemplate,
} from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { GuardrailEditor } from "../components/GuardrailEditor";
import { ScopeIDSelector } from "../components/ScopeIDSelector";
import {
  Card,
  CardHeader,
  Button,
  Input,
  Select,
  Field,
  Badge,
  TabBar,
  Toggle,
  Modal,
  EmptyState,
  Spinner,
} from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";

type Tab = "global" | "providers" | "models" | "chains" | "apikeys" | "logs";

const TABS = [
  { value: "global" as const, label: "Global", icon: Globe },
  { value: "providers" as const, label: "Providers", icon: Boxes },
  { value: "models" as const, label: "Models", icon: Cpu },
  { value: "chains" as const, label: "Chains", icon: Layers },
  { value: "apikeys" as const, label: "API Keys", icon: KeyIcon },
  { value: "logs" as const, label: "Audit Logs", icon: ScrollText },
];

function useHashTab(defaultTab: Tab): [Tab, (t: Tab) => void] {
  const valid: Tab[] = TABS.map((t) => t.value);
  const read = (): Tab => {
    const h = window.location.hash.replace("#", "");
    return valid.includes(h as Tab) ? (h as Tab) : defaultTab;
  };
  const [tab, setTab] = useState<Tab>(read);
  useEffect(() => {
    const onHash = () => setTab(read);
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return [
    tab,
    (t: Tab) => {
      window.history.replaceState(null, "", `#${t}`);
      setTab(t);
    },
  ];
}

const SCOPE_BY_TAB: Record<Exclude<Tab, "logs">, GuardrailScope> = {
  global: "global",
  providers: "provider",
  models: "model",
  chains: "chain",
  apikeys: "apikey",
};

export function GuardrailsPage() {
  const [tab, setTab] = useHashTab("global");
  const [importing, setImporting] = useState(false);

  return (
    <div>
      <PageHeader
        title="Guardrails"
        description="Content-safety policies layered global → provider → model → chain → API key. Most specific wins."
      />
      <div className="mb-3 flex items-center gap-2">
        <Button variant="secondary" onClick={() => setImporting(true)}>
          <Upload className="h-4 w-4 mr-1" /> Import
        </Button>
        <ExportButton />
      </div>
      <div className="mb-4">
        <TabBar tabs={TABS} active={tab} onChange={setTab} />
      </div>
      {tab === "global" && <TenantFlagsCard />}
      {tab === "logs" ? <LogsTab /> : <ScopeTab scope={SCOPE_BY_TAB[tab]} />}
      {importing && <ImportModal onClose={() => setImporting(false)} />}
    </div>
  );
}

// ---- Tenant-wide GDPR / external-engines toggle -----------------------------

function TenantFlagsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const flags = useQuery({
    queryKey: ["guardrails", "tenant-flags"],
    queryFn: () => api.getGuardrailTenantFlags(),
    staleTime: 30_000,
  });

  const update = useMutation({
    mutationFn: (allow: boolean) =>
      api.putGuardrailTenantFlags({ allow_external_engines: allow }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["guardrails", "tenant-flags"] });
      toast.success("Tenant flags updated");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to update");
    },
  });

  const allow = flags.data?.allow_external_engines ?? true;

  return (
    <Card className="mb-3">
      <div className="px-5 py-4 flex items-start justify-between gap-4">
        <div className="flex gap-3">
          <div className="mt-1">
            <Lock className="h-5 w-5 text-[var(--text-muted)]" />
          </div>
          <div>
            <div className="text-sm font-medium">Allow external detector engines</div>
            <p className="mt-1 text-xs text-[var(--text-muted)] max-w-2xl">
              When off, every policy is forced back to its native engine — OpenAI Moderation,
              Microsoft Presidio, and embedding-based topic matching are disabled tenant-wide.
              Use this for GDPR / data-residency setups where prompt content must never leave
              the KeiRouter process.
              {!allow && (
                <span className="block mt-1 text-bad font-medium">
                  External engines disabled — all detector traffic stays inside this container.
                </span>
              )}
            </p>
          </div>
        </div>
        <Toggle checked={allow} onChange={(v) => update.mutate(v)} />
      </div>
    </Card>
  );
}

// ---- Export / Import --------------------------------------------------------

function ExportButton() {
  const toast = useToast();
  const onClick = async () => {
    try {
      const bundle = await api.exportGuardrails();
      const blob = new Blob([JSON.stringify(bundle, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `keirouter-guardrails-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success(`Exported ${bundle.policies.length} policies`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Export failed");
    }
  };
  return (
    <Button variant="secondary" onClick={onClick}>
      <Download className="h-4 w-4 mr-1" /> Export all
    </Button>
  );
}

function ImportModal({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const fileRef = useRef<HTMLInputElement>(null);
  const [raw, setRaw] = useState("");
  const [result, setResult] = useState<{
    imported: Array<{ name: string; scope: string; scope_id?: string }>;
    skipped: Array<{ name: string; reason: string }>;
  } | null>(null);

  const submit = useMutation({
    mutationFn: async () => {
      let bundle: GuardrailBundle;
      try {
        bundle = JSON.parse(raw) as GuardrailBundle;
      } catch {
        throw new Error("invalid JSON");
      }
      if (!Array.isArray(bundle.policies)) {
        throw new Error("missing 'policies' array");
      }
      return api.importGuardrails(bundle);
    },
    onSuccess: (r) => {
      setResult(r);
      qc.invalidateQueries({ queryKey: ["guardrails"] });
      toast.success(
        `Imported ${r.imported.length}${r.skipped.length ? `, ${r.skipped.length} skipped` : ""}`,
      );
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Import failed"),
  });

  const onFile = async (f: File) => {
    setRaw(await f.text());
  };

  return (
    <Modal open onClose={onClose} title="Import guardrails bundle" maxWidth="max-w-2xl">
      <div className="px-6 py-5 space-y-4 max-h-[70vh] overflow-y-auto">
        <p className="text-xs text-[var(--text-muted)]">
          Paste a previously-exported JSON bundle, or load one from disk. Policies are upserted
          by name within their scope; existing rows with the same scope_id are overwritten.
        </p>
        <div className="flex gap-2">
          <input
            ref={fileRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) onFile(f);
            }}
          />
          <Button variant="secondary" onClick={() => fileRef.current?.click()}>
            <Upload className="h-4 w-4 mr-1" /> Choose file
          </Button>
        </div>
        <textarea
          value={raw}
          onChange={(e) => setRaw(e.target.value)}
          rows={12}
          placeholder='{ "version": 1, "policies": [ ... ] }'
          className="w-full font-mono text-xs px-3 py-2 rounded border border-[var(--border)] bg-transparent"
        />
        {result && (
          <div className="text-xs space-y-2">
            {result.imported.length > 0 && (
              <div>
                <div className="font-medium mb-1">Imported ({result.imported.length})</div>
                <ul className="space-y-0.5 text-[var(--text-muted)]">
                  {result.imported.map((p, i) => (
                    <li key={i}>
                      • {p.name} ({p.scope}
                      {p.scope_id ? `/${p.scope_id}` : ""})
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {result.skipped.length > 0 && (
              <div>
                <div className="font-medium mb-1 text-bad">
                  Skipped ({result.skipped.length})
                </div>
                <ul className="space-y-0.5 text-[var(--text-muted)]">
                  {result.skipped.map((p, i) => (
                    <li key={i}>
                      • {p.name} — {p.reason}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
      </div>
      <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
        <Button variant="secondary" onClick={onClose}>
          {result ? "Close" : "Cancel"}
        </Button>
        {!result && (
          <Button
            onClick={() => submit.mutate()}
            disabled={!raw.trim() || submit.isPending}
          >
            {submit.isPending ? "Importing..." : "Import bundle"}
          </Button>
        )}
      </div>
    </Modal>
  );
}

function ScopeTab({ scope }: { scope: GuardrailScope }) {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const toast = useToast();
  const policies = useQuery({
    queryKey: ["guardrails", scope],
    queryFn: () => api.listGuardrails(scope),
  });

  const [editing, setEditing] = useState<GuardrailPolicy | null>(null);
  const [creating, setCreating] = useState(false);

  const list = policies.data?.guardrails ?? [];

  return (
    <div>
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm text-[var(--text-muted)]">
          {scopeDescription(scope)}
        </div>
        {scope !== "global" && (
          <Button onClick={() => setCreating(true)}>
            <Plus className="h-4 w-4 mr-1" /> New Policy
          </Button>
        )}
        {scope === "global" && list.length === 0 && (
          <Button onClick={() => setCreating(true)}>
            <Plus className="h-4 w-4 mr-1" /> Create Global Policy
          </Button>
        )}
      </div>

      {policies.isLoading ? (
        <div className="py-8 flex justify-center">
          <Spinner />
        </div>
      ) : list.length === 0 ? (
        <EmptyState
          title={`No ${scope} policies yet`}
          hint={
            scope === "global"
              ? "Create one master policy that applies to all traffic."
              : "Add a policy to override the global config for this scope."
          }
        />
      ) : (
        <div className="space-y-3">
          {list.map((p) => (
            <PolicyRow
              key={p.id}
              policy={p}
              onEdit={() => setEditing(p)}
              onToggle={async (enabled) => {
                await api.updateGuardrail(p.id, { enabled });
                qc.invalidateQueries({ queryKey: ["guardrails"] });
              }}
              onDelete={async () => {
                if (!(await confirm({ title: `Delete policy “${p.name}”?`, description: "Scopes using it fall back to the next policy up the chain.", tone: "danger" }))) return;
                await api.deleteGuardrail(p.id);
                qc.invalidateQueries({ queryKey: ["guardrails"] });
                toast.success("Policy deleted");
              }}
            />
          ))}
        </div>
      )}

      {editing && (
        <EditPolicyModal
          policy={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["guardrails"] });
            setEditing(null);
          }}
        />
      )}
      {creating && (
        <CreatePolicyModal
          scope={scope}
          onClose={() => setCreating(false)}
          onCreated={() => {
            qc.invalidateQueries({ queryKey: ["guardrails"] });
            setCreating(false);
          }}
        />
      )}
    </div>
  );
}

function scopeDescription(scope: GuardrailScope): string {
  switch (scope) {
    case "global":
      return "Master policy that applies to every request when no more specific policy fires.";
    case "provider":
      return "Per-provider overrides (OpenAI, Anthropic, Gemini, …).";
    case "model":
      return "Per-model overrides (gpt-5, claude-opus, gemini-2.5, …).";
    case "chain":
      return "Per-chain overrides (Customer Service, HR Assistant, …).";
    case "apikey":
      return "Per-API-key overrides — edit from the API Key detail page for the best experience.";
  }
}

function PolicyRow({
  policy,
  onEdit,
  onToggle,
  onDelete,
}: {
  policy: GuardrailPolicy;
  onEdit: () => void;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
}) {
  const cfg = policy.config ?? {};
  const enabledDetectors = useMemo(() => {
    const out: string[] = [];
    if (cfg.pii?.enabled) out.push("PII");
    if (cfg.injection?.enabled) out.push("Injection");
    if (cfg.topics?.enabled) out.push("Topics");
    if (cfg.toxicity?.enabled) out.push("Toxicity");
    if (cfg.bias?.enabled) out.push("Bias");
    return out;
  }, [cfg]);

  return (
    <Card>
      <div className="px-5 py-4 flex items-center justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <Shield className="h-4 w-4 text-[var(--text-muted)]" />
            <span className="font-medium truncate">{policy.name}</span>
            <Badge tone={policy.enabled ? "success" : "neutral"}>
              {policy.enabled ? "Active" : "Disabled"}
            </Badge>
          </div>
          <div className="mt-1 text-xs text-[var(--text-muted)] truncate">
            scope: {policy.scope}
            {policy.scope_id && ` / ${policy.scope_id}`} ·{" "}
            {enabledDetectors.length === 0
              ? "no detectors enabled"
              : enabledDetectors.join(" · ")}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Toggle checked={policy.enabled} onChange={onToggle} />
          <Button variant="secondary" onClick={onEdit}>
            Edit
          </Button>
          <Button variant="ghost" onClick={onDelete}>
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </Card>
  );
}

function EditPolicyModal({
  policy,
  onClose,
  onSaved,
}: {
  policy: GuardrailPolicy;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [name, setName] = useState(policy.name);
  const [config, setConfig] = useState<GuardrailPolicyConfig>(policy.config ?? {});

  const save = useMutation({
    mutationFn: () => api.updateGuardrail(policy.id, { name, config }),
    onSuccess: () => {
      toast.success("Policy saved");
      onSaved();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Save failed");
    },
  });

  return (
    <Modal open onClose={onClose} title={`Edit policy · ${policy.scope}`} maxWidth="max-w-3xl">
      <div className="px-6 py-5 space-y-4 max-h-[70vh] overflow-y-auto">
        <Field label="Policy name">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <GuardrailEditor value={config} onChange={setConfig} />
      </div>
      <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button onClick={() => save.mutate()} disabled={save.isPending}>
          <Save className="h-4 w-4 mr-1" />
          {save.isPending ? "Saving..." : "Save policy"}
        </Button>
      </div>
    </Modal>
  );
}

function CreatePolicyModal({
  scope,
  onClose,
  onCreated,
}: {
  scope: GuardrailScope;
  onClose: () => void;
  onCreated: () => void;
}) {
  const toast = useToast();
  const [name, setName] = useState(scope === "global" ? "Global Guardrails" : "");
  const [scopeID, setScopeID] = useState("");
  const [config, setConfig] = useState<GuardrailPolicyConfig>({});
  const [pickingTemplate, setPickingTemplate] = useState(false);

  const onPickTemplate = (tpl: GuardrailTemplate) => {
    setConfig(tpl.config);
    if (!name.trim()) setName(tpl.name);
    setPickingTemplate(false);
    toast.success(`Loaded template: ${tpl.name}`);
  };

  const create = useMutation({
    mutationFn: () =>
      api.createGuardrail({
        scope,
        scope_id: scope === "global" ? "" : scopeID,
        name,
        enabled: true,
        config,
      }),
    onSuccess: () => {
      toast.success("Policy created");
      onCreated();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Create failed");
    },
  });

  return (
    <Modal open onClose={onClose} title={`New ${scope} policy`} maxWidth="max-w-3xl">
      <div className="px-6 py-5 space-y-4 max-h-[70vh] overflow-y-auto">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Policy name">
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={defaultNameFor(scope)}
            />
          </Field>
          {scope !== "global" && (
            <ScopeIDSelector scope={scope} value={scopeID} onChange={setScopeID} />
          )}
        </div>
        <div>
          <Button variant="secondary" onClick={() => setPickingTemplate(true)}>
            <Sparkles className="h-4 w-4 mr-1" /> Start from template…
          </Button>
        </div>
        <GuardrailEditor value={config} onChange={setConfig} />
      </div>
      <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button
          onClick={() => create.mutate()}
          disabled={create.isPending || (scope !== "global" && !scopeID.trim())}
        >
          <Save className="h-4 w-4 mr-1" />
          {create.isPending ? "Creating..." : "Create policy"}
        </Button>
      </div>
      {pickingTemplate && (
        <TemplatePickerModal
          onClose={() => setPickingTemplate(false)}
          onPick={onPickTemplate}
        />
      )}
    </Modal>
  );
}

function TemplatePickerModal({
  onClose,
  onPick,
}: {
  onClose: () => void;
  onPick: (tpl: GuardrailTemplate) => void;
}) {
  const templates = useQuery({
    queryKey: ["guardrail-templates"],
    queryFn: () => api.listGuardrailTemplates(),
    staleTime: Infinity,
  });
  return (
    <Modal open onClose={onClose} title="Start from a template" maxWidth="max-w-xl">
      <div className="px-6 py-5 space-y-3 max-h-[70vh] overflow-y-auto">
        {templates.isLoading ? (
          <div className="py-6 flex justify-center">
            <Spinner />
          </div>
        ) : (
          (templates.data?.templates ?? []).map((tpl) => (
            <button
              key={tpl.id}
              type="button"
              onClick={() => onPick(tpl)}
              className="w-full text-left rounded border border-[var(--border)] hover:border-indigo-400/60 hover:bg-indigo-500/[0.04] transition px-4 py-3"
            >
              <div className="flex items-center gap-2">
                <Sparkles className="h-4 w-4 text-indigo-500/80" />
                <span className="font-medium">{tpl.name}</span>
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">{tpl.description}</p>
            </button>
          ))
        )}
      </div>
      <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
      </div>
    </Modal>
  );
}

function defaultNameFor(scope: GuardrailScope): string {
  switch (scope) {
    case "global":
      return "Global Guardrails";
    case "provider":
      return "Provider policy";
    case "model":
      return "Model policy";
    case "chain":
      return "Chain policy";
    case "apikey":
      return "API key policy";
  }
}

function LogsTab() {
  const [detector, setDetector] = useState("");
  const [action, setAction] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [liveOn, setLiveOn] = useState(true);
  const [liveStatus, setLiveStatus] = useState<"connected" | "off">("off");
  const [liveRows, setLiveRows] = useState<GuardrailLogEntry[]>([]);

  // Initial / refreshed fetch. Polling is disabled once the SSE connection
  // is up so the database isn't hammered on top of the live stream.
  const logs = useQuery({
    queryKey: ["guardrail-logs", detector, action],
    queryFn: () =>
      api.listGuardrailLogs({
        detector: detector || undefined,
        action: action || undefined,
        limit: 200,
      }),
    refetchInterval: liveOn ? false : 5000,
  });

  // SSE subscription. We hold the streamed rows in local state and merge them
  // with the fetched page so newly-fired decisions appear instantly. Filters
  // are applied client-side to the live rows because the SSE endpoint emits
  // every audit row tenant-wide.
  useEffect(() => {
    if (!liveOn) {
      setLiveStatus("off");
      return;
    }
    const close = connectGuardrailLogStream((row) => {
      setLiveStatus("connected");
      setLiveRows((prev) => {
        // Drop matching id to dedupe with the initial fetch on reconnect.
        const filtered = prev.filter((r) => r.id !== row.id);
        return [row, ...filtered].slice(0, 200);
      });
    });
    return () => {
      setLiveStatus("off");
      close();
    };
  }, [liveOn]);

  const fetched = logs.data?.logs ?? [];
  // Merge: live rows first (newest), then fetched rows that aren't already in
  // live. Filter live rows on the client to honor the detector/action filters.
  const rows = useMemo(() => {
    const liveFiltered = liveRows.filter(
      (r) => (!detector || r.detector === detector) && (!action || r.action === action),
    );
    const liveIDs = new Set(liveFiltered.map((r) => r.id));
    return [...liveFiltered, ...fetched.filter((r) => !liveIDs.has(r.id))].slice(0, 200);
  }, [liveRows, fetched, detector, action]);
  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <Card>
      <CardHeader
        title="Audit Logs"
        description={
          liveOn
            ? "Recent guardrail decisions. Live-streaming via SSE — new rows appear instantly."
            : "Recent guardrail decisions. Polling every 5s. Toggle live to stream over SSE."
        }
        action={
          <div className="flex gap-2 items-center">
            <button
              type="button"
              onClick={() => setLiveOn((v) => !v)}
              className={`text-xs px-2 py-1 rounded border inline-flex items-center gap-1.5 ${
                liveOn
                  ? "bg-emerald-500/10 border-emerald-500/40 text-ok"
                  : "bg-white/5 border-white/10 text-gray-600 dark:text-gray-300"
              }`}
              title={
                liveOn
                  ? liveStatus === "connected"
                    ? "Connected to SSE stream"
                    : "Connecting…"
                  : "Click to enable live updates"
              }
            >
              <Radio
                className={`h-3 w-3 ${
                  liveOn && liveStatus === "connected" ? "animate-pulse" : ""
                }`}
              />
              {liveOn ? "Live" : "Polling"}
            </button>
            <Select value={detector} onChange={(e) => setDetector(e.target.value)}>
              <option value="">All detectors</option>
              <option value="pii">PII</option>
              <option value="injection">Injection</option>
              <option value="topics">Topics</option>
              <option value="toxicity">Toxicity</option>
              <option value="bias">Bias</option>
            </Select>
            <Select value={action} onChange={(e) => setAction(e.target.value)}>
              <option value="">All actions</option>
              <option value="block">Block</option>
              <option value="mask">Mask</option>
              <option value="warn">Warn</option>
              <option value="log_only">Log only</option>
            </Select>
          </div>
        }
      />
      <div className="px-5 pb-5">
        {logs.isLoading ? (
          <div className="py-8 flex justify-center">
            <Spinner />
          </div>
        ) : rows.length === 0 ? (
          <EmptyState
            title="No audit entries yet"
            hint="As guardrails fire, decisions land here. Try the Test Policy panel on a Global policy — your test will appear here within seconds."
          />
        ) : (
          <div className="text-xs">
            <table className="w-full text-left">
              <thead className="text-[var(--text-muted)]">
                <tr>
                  <th className="py-2 pr-3 w-6"></th>
                  <th className="py-2 pr-3">Time</th>
                  <th className="py-2 pr-3">Detector</th>
                  <th className="py-2 pr-3">Action</th>
                  <th className="py-2 pr-3">Severity</th>
                  <th className="py-2 pr-3">Source</th>
                  <th className="py-2 pr-3">Findings</th>
                  <th className="py-2 pr-3">Reason</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const isOpen = expanded.has(row.id);
                  const findings = row.findings ?? [];
                  const isTest = row.api_key_id === "test-panel";
                  const entityCounts = countEntities(findings);
                  return (
                    <Fragment key={row.id}>
                      <tr
                        className="border-t border-[var(--border)] cursor-pointer hover:bg-black/[0.02] dark:hover:bg-white/[0.02]"
                        onClick={() => toggle(row.id)}
                      >
                        <td className="py-2 pr-1 text-[var(--text-muted)]">{isOpen ? "▾" : "▸"}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          {new Date(row.created_at).toLocaleString()}
                        </td>
                        <td className="py-2 pr-3 font-mono">{row.detector}</td>
                        <td className="py-2 pr-3">
                          <Badge
                            tone={
                              row.action === "block"
                                ? "danger"
                                : row.action === "mask"
                                ? "warning"
                                : "neutral"
                            }
                          >
                            {row.action}
                          </Badge>
                        </td>
                        <td className="py-2 pr-3">{row.severity || "—"}</td>
                        <td className="py-2 pr-3">
                          {isTest ? (
                            <Badge tone="accent">test panel</Badge>
                          ) : row.provider || row.model ? (
                            <span className="font-mono text-[10px]">
                              {row.provider || "?"}
                              {row.model ? `/${row.model}` : ""}
                            </span>
                          ) : (
                            <span className="text-[var(--text-muted)]">—</span>
                          )}
                        </td>
                        <td className="py-2 pr-3">
                          {entityCounts.length === 0 ? (
                            <span className="text-[var(--text-muted)]">—</span>
                          ) : (
                            <div className="flex flex-wrap gap-1">
                              {entityCounts.map(([e, n]) => (
                                <Badge key={e} tone="neutral">
                                  {e}
                                  {n > 1 ? ` ×${n}` : ""}
                                </Badge>
                              ))}
                            </div>
                          )}
                        </td>
                        <td className="py-2 pr-3 max-w-md truncate" title={row.reason}>
                          {row.reason}
                        </td>
                      </tr>
                      {isOpen && (
                        <tr className="border-t border-[var(--border)] bg-black/[0.03] dark:bg-white/[0.03]">
                          <td></td>
                          <td colSpan={7} className="py-3 pr-3">
                            <FindingsDetails row={row} />
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Card>
  );
}

function countEntities(findings: { entity: string }[]): [string, number][] {
  const counts: Record<string, number> = {};
  for (const f of findings) {
    counts[f.entity] = (counts[f.entity] ?? 0) + 1;
  }
  return Object.entries(counts).sort((a, b) => b[1] - a[1]);
}

function FindingsDetails({
  row,
}: {
  row: {
    id: string;
    request_id: string;
    direction: string;
    findings: { entity: string; score: number; start: number; end: number; original?: string; redacted?: string }[] | null;
  };
}) {
  const findings = row.findings ?? [];
  return (
    <div className="space-y-2 text-[11px]">
      <div className="flex gap-4 text-[var(--text-muted)]">
        <span>
          <span className="font-semibold">Request:</span>{" "}
          <code className="font-mono">{row.request_id || "—"}</code>
        </span>
        <span>
          <span className="font-semibold">Direction:</span> {row.direction}
        </span>
      </div>
      {findings.length === 0 ? (
        <div className="italic text-[var(--text-muted)]">No findings recorded.</div>
      ) : (
        <table className="w-full text-left">
          <thead className="text-[var(--text-muted)]">
            <tr>
              <th className="py-1 pr-3">Entity</th>
              <th className="py-1 pr-3">Score</th>
              <th className="py-1 pr-3">Original (truncated)</th>
              <th className="py-1 pr-3">Replacement</th>
            </tr>
          </thead>
          <tbody>
            {findings.map((f, i) => (
              <tr key={i} className="border-t border-[var(--border)]">
                <td className="py-1 pr-3 font-mono">{f.entity}</td>
                <td className="py-1 pr-3">{(f.score * 100).toFixed(0)}%</td>
                <td className="py-1 pr-3 font-mono">
                  {f.original ? (
                    <code className="rounded bg-rose-500/10 px-1.5 py-0.5 text-bad">
                      {f.original}
                    </code>
                  ) : (
                    "—"
                  )}
                </td>
                <td className="py-1 pr-3 font-mono">
                  {f.redacted ? (
                    <code className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-ok">
                      {f.redacted}
                    </code>
                  ) : (
                    <span className="text-[var(--text-muted)]">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
