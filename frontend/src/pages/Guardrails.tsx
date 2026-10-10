import {
  useEffect,
  useId,
  useMemo,
  useState,
  useRef,
  Fragment,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery, useQueries, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ChevronDown,
  ChevronRight,
  Download,
  FileJson,
  KeyRound,
  MoreHorizontal,
  Pencil,
  Plus,
  Search,
  Sparkles,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import {
  api,
  connectGuardrailLogStream,
  type GuardrailAction,
  type GuardrailBundle,
  type GuardrailLogEntry,
  type GuardrailPolicy,
  type GuardrailPolicyConfig,
  type GuardrailScope,
  type GuardrailTemplate,
} from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { GuardrailEditor } from "../components/GuardrailEditor";
import { ScopeIDSelector, useScopeTargetLabels } from "../components/ScopeIDSelector";
import { Button, Input, Select, Badge, Toggle, Modal, Skeleton, ErrorBanner } from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../components/ui/dropdown-menu";

// ── Constants ────────────────────────────────────────────────────────────────

type Tab = "policies" | "logs";
type ScopeFilter = "all" | GuardrailScope;

// Order matters: this is the precedence chain, least to most specific.
const SCOPES: GuardrailScope[] = ["global", "provider", "model", "chain", "apikey"];

const SCOPE_LABEL: Record<GuardrailScope, string> = {
  global: "Global",
  provider: "Provider",
  model: "Model",
  chain: "Chain",
  apikey: "API key",
};

const TABS: [Tab, string][] = [
  ["policies", "Policies"],
  ["logs", "Audit log"],
];

// Legacy hash tabs (#providers, #logs …) map onto the new tab + scope filter so
// old bookmarks still land somewhere sensible.
const LEGACY_HASH: Record<string, { tab: Tab; scope?: GuardrailScope }> = {
  global: { tab: "policies", scope: "global" },
  providers: { tab: "policies", scope: "provider" },
  models: { tab: "policies", scope: "model" },
  chains: { tab: "policies", scope: "chain" },
  apikeys: { tab: "policies", scope: "apikey" },
  logs: { tab: "logs" },
};

function isScope(v: string | null): v is GuardrailScope {
  return !!v && (SCOPES as string[]).includes(v);
}

// ── Shared bits ──────────────────────────────────────────────────────────────

function DialogFooter({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center justify-end gap-2 rounded-b-2xl border-t border-line bg-subtle px-5 py-3">{children}</div>;
}

// FormField wires a visible label (and optional one-line hint) to its control.
function FormField({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: ReactNode;
  children: (ids: { id: string; "aria-describedby"?: string }) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="block text-[12.5px] font-medium text-fg">
        {label}
      </label>
      {children({ id, "aria-describedby": hint ? hintId : undefined })}
      {hint && (
        <p id={hintId} className="text-[12px] text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

const chipClass = (active: boolean) =>
  cn(
    "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
    active ? "border-transparent bg-primary text-primary-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg",
  );

const iconButton =
  "flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

const textareaClass =
  "w-full resize-y rounded-lg border border-input bg-surface px-3 py-2 font-mono text-[12px] leading-5 text-fg transition-[border-color,box-shadow] placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

// Arrow / Home / End move focus and selection within a role="radiogroup" or
// role="tablist" built from buttons.
function rovingKeys(selector: string) {
  return (event: ReactKeyboardEvent<HTMLElement>) => {
    const keys = ["ArrowRight", "ArrowLeft", "ArrowDown", "ArrowUp", "Home", "End"];
    if (!keys.includes(event.key)) return;
    const controls = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>(selector)).filter((b) => !b.disabled);
    const current = controls.indexOf(document.activeElement as HTMLButtonElement);
    if (current < 0 || controls.length === 0) return;
    event.preventDefault();
    const forward = event.key === "ArrowRight" || event.key === "ArrowDown";
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? controls.length - 1
          : (current + (forward ? 1 : -1) + controls.length) % controls.length;
    controls[next]?.focus();
    controls[next]?.click();
  };
}

// ── Policy summaries ─────────────────────────────────────────────────────────

const ACTION_RANK: Record<GuardrailAction, number> = { allow: 0, log_only: 1, warn: 2, mask: 3, block: 4 };

function enabledDetectorNames(cfg: GuardrailPolicyConfig): string[] {
  const out: string[] = [];
  if (cfg.pii?.enabled) out.push("PII");
  if (cfg.injection?.enabled) out.push("Injection");
  if (cfg.topics?.enabled) out.push("Topics");
  if (cfg.toxicity?.enabled) out.push("Toxicity");
  if (cfg.bias?.enabled) out.push("Bias");
  return out;
}

// strongestAction is the most severe response any enabled detector can take,
// using the same defaults the editor shows when a field is unset.
function strongestAction(cfg: GuardrailPolicyConfig): GuardrailAction | null {
  const actions: GuardrailAction[] = [];
  if (cfg.pii?.enabled) actions.push(cfg.pii.strategy === "block" ? "block" : "mask");
  if (cfg.injection?.enabled) actions.push(cfg.injection.action ?? "block");
  if (cfg.topics?.enabled) actions.push(cfg.topics.action ?? "warn");
  if (cfg.toxicity?.enabled) actions.push(cfg.toxicity.action ?? "warn");
  if (cfg.bias?.enabled) actions.push(cfg.bias.action ?? "log_only");
  if (actions.length === 0) return null;
  return actions.reduce((a, b) => (ACTION_RANK[b] > ACTION_RANK[a] ? b : a));
}

function EnforcementBadge({ action }: { action: GuardrailAction | null }) {
  if (!action) return <span className="text-fg-faint">—</span>;
  switch (action) {
    case "block":
      return <Badge tone="danger">Can block</Badge>;
    case "warn":
      return <Badge tone="warning">Warns</Badge>;
    case "mask":
      return <Badge tone="neutral">Masks</Badge>;
    case "log_only":
      return <Badge tone="neutral">Logs only</Badge>;
    default:
      return <Badge tone="neutral">Allows</Badge>;
  }
}

function actionBadgeTone(action: string): "danger" | "warning" | "neutral" {
  if (action === "block") return "danger";
  if (action === "warn") return "warning";
  return "neutral";
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function GuardrailsPage() {
  const [params] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const legacy = LEGACY_HASH[location.hash.replace("#", "")];
  const tab: Tab = params.get("tab") === "logs" ? "logs" : params.get("tab") ? "policies" : legacy?.tab ?? "policies";
  const scopeParam = params.get("scope");
  const scopeFilter: ScopeFilter = isScope(scopeParam) ? scopeParam : !params.get("tab") && legacy?.scope ? legacy.scope : "all";

  // Tab and scope filter live in the query string (?tab=logs, ?scope=model);
  // any legacy #hash is dropped on the first interaction.
  const update = (next: { tab?: Tab; scope?: ScopeFilter }) => {
    const p = new URLSearchParams(params);
    const t = next.tab ?? tab;
    const s = next.scope ?? scopeFilter;
    if (t === "policies") p.delete("tab");
    else p.set("tab", t);
    if (t === "policies" && s !== "all") p.set("scope", s);
    else p.delete("scope");
    const search = p.toString();
    navigate({ pathname: location.pathname, search: search ? `?${search}` : "", hash: "" }, { replace: true });
  };

  const [importing, setImporting] = useState(false);
  const [creating, setCreating] = useState(false);
  const onExport = useExportGuardrails();

  return (
    <div>
      <PageHeader
        title="Guardrails"
        description="The most specific matching policy wins."
        action={
          <>
            <DropdownMenu>
              <DropdownMenuTrigger className="inline-flex min-h-9 items-center justify-center gap-1.5 rounded-lg border border-line bg-surface px-3 py-1.5 text-[13px] font-medium text-fg transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas">
                Import / export
                <ChevronDown className="h-4 w-4 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setImporting(true)}>
                  <Upload />
                  Import bundle…
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={onExport}>
                  <Download />
                  Export all policies
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Button onClick={() => setCreating(true)}>
              <Plus aria-hidden="true" />
              New policy
            </Button>
          </>
        }
      />

      <div className="mb-5 flex gap-1 border-b border-line" role="tablist" aria-label="Guardrail sections" onKeyDown={rovingKeys('[role="tab"]')}>
        {TABS.map(([value, label]) => {
          const on = tab === value;
          return (
            <button
              key={value}
              type="button"
              role="tab"
              id={`guardrails-tab-${value}`}
              aria-selected={on}
              aria-controls="guardrails-panel"
              tabIndex={on ? 0 : -1}
              onClick={() => update({ tab: value })}
              className={cn(
                "relative -mb-px inline-flex items-center gap-1.5 px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                on ? "text-fg" : "text-fg-muted hover:text-fg",
              )}
            >
              {label}
              {on && <span aria-hidden="true" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
            </button>
          );
        })}
      </div>

      <div id="guardrails-panel" role="tabpanel" aria-labelledby={`guardrails-tab-${tab}`}>
        {tab === "logs" ? (
          <LogsTab />
        ) : (
          <PoliciesTab
            scopeFilter={scopeFilter}
            onScopeFilter={(s) => update({ scope: s })}
            creating={creating}
            onCreate={() => setCreating(true)}
            onCloseCreate={() => setCreating(false)}
          />
        )}
      </div>
      {tab === "logs" && creating && <CreatePolicyHost scopeFilter="all" onClose={() => setCreating(false)} />}
      {importing && <ImportModal onClose={() => setImporting(false)} />}
    </div>
  );
}

// usePolicies loads every scope with the same per-scope query keys the page
// has always used, so invalidating ["guardrails"] refreshes all of them.
function usePolicies() {
  const results = useQueries({
    queries: SCOPES.map((scope) => ({
      queryKey: ["guardrails", scope],
      queryFn: () => api.listGuardrails(scope),
    })),
  });
  const isLoading = results.some((r) => r.isLoading);
  const error = results.find((r) => r.isError)?.error;
  const byScope = {} as Record<GuardrailScope, GuardrailPolicy[]>;
  SCOPES.forEach((s, i) => {
    byScope[s] = results[i]?.data?.guardrails ?? [];
  });
  const all = SCOPES.flatMap((s) => byScope[s]);
  return { isLoading, error, byScope, all };
}

// CreatePolicyHost lets the header's "New policy" work from the audit-log tab.
function CreatePolicyHost({ scopeFilter, onClose }: { scopeFilter: ScopeFilter; onClose: () => void }) {
  const qc = useQueryClient();
  const { byScope, isLoading } = usePolicies();
  if (isLoading) return null;
  return (
    <CreatePolicyModal
      initialScope={pickInitialScope(scopeFilter, byScope.global.length > 0)}
      hasGlobal={byScope.global.length > 0}
      onClose={onClose}
      onCreated={() => {
        qc.invalidateQueries({ queryKey: ["guardrails"] });
        onClose();
      }}
    />
  );
}

function pickInitialScope(filter: ScopeFilter, hasGlobal: boolean): GuardrailScope {
  if (filter !== "all" && !(filter === "global" && hasGlobal)) return filter;
  return hasGlobal ? "provider" : "global";
}

// ── Policies tab ─────────────────────────────────────────────────────────────

function PoliciesTab({
  scopeFilter,
  onScopeFilter,
  creating,
  onCreate,
  onCloseCreate,
}: {
  scopeFilter: ScopeFilter;
  onScopeFilter: (s: ScopeFilter) => void;
  creating: boolean;
  onCreate: () => void;
  onCloseCreate: () => void;
}) {
  const confirmAction = useConfirm();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const { isLoading, error, byScope, all } = usePolicies();
  const targetLabel = useScopeTargetLabels(all.map((p) => p.scope));

  const [editing, setEditing] = useState<GuardrailPolicy | null>(null);
  const [search, setSearch] = useState("");
  const [toggling, setToggling] = useState<Set<string>>(new Set());
  // Set when "Add global policy" is used so the dialog opens on that scope
  // regardless of the active filter.
  const [forcedScope, setForcedScope] = useState<GuardrailScope | null>(null);

  const hasGlobal = byScope.global.length > 0;

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    return all
      .filter((p) => scopeFilter === "all" || p.scope === scopeFilter)
      .filter((p) => {
        if (!q) return true;
        return (
          p.name.toLowerCase().includes(q) ||
          (p.scope_id ?? "").toLowerCase().includes(q) ||
          (targetLabel(p.scope, p.scope_id) ?? "").toLowerCase().includes(q) ||
          enabledDetectorNames(p.config ?? {}).some((d) => d.toLowerCase().includes(q))
        );
      });
  }, [all, scopeFilter, search, targetLabel]);

  const onToggle = async (p: GuardrailPolicy, enabled: boolean) => {
    setToggling((s) => new Set(s).add(p.id));
    try {
      await api.updateGuardrail(p.id, { enabled });
      qc.invalidateQueries({ queryKey: ["guardrails"] });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
    } finally {
      setToggling((s) => {
        const next = new Set(s);
        next.delete(p.id);
        return next;
      });
    }
  };

  const onDelete = async (p: GuardrailPolicy) => {
    if (!(await confirmAction({ title: `Delete policy “${p.name}”?`, description: "Scopes using it fall back to the next policy up the chain.", confirmLabel: "Delete", tone: "danger" }))) return;
    try {
      await api.deleteGuardrail(p.id);
      qc.invalidateQueries({ queryKey: ["guardrails"] });
      toast.success("Policy deleted");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    }
  };

  const filtering = search.trim() !== "" || scopeFilter !== "all";
  const counts: Record<ScopeFilter, number> = {
    all: all.length,
    global: byScope.global.length,
    provider: byScope.provider.length,
    model: byScope.model.length,
    chain: byScope.chain.length,
    apikey: byScope.apikey.length,
  };

  const closeCreate = () => {
    setForcedScope(null);
    onCloseCreate();
  };
  const createGlobal = () => {
    setForcedScope("global");
    onCreate();
  };

  const modals = (
    <>
      {editing && (
        <EditPolicyModal
          policy={editing}
          targetLabel={targetLabel(editing.scope, editing.scope_id)}
          onClose={() => setEditing(null)}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["guardrails"] });
            setEditing(null);
          }}
        />
      )}
      {creating && (
        <CreatePolicyModal
          initialScope={forcedScope && !(forcedScope === "global" && hasGlobal) ? forcedScope : pickInitialScope(scopeFilter, hasGlobal)}
          hasGlobal={hasGlobal}
          onClose={closeCreate}
          onCreated={() => {
            qc.invalidateQueries({ queryKey: ["guardrails"] });
            closeCreate();
          }}
        />
      )}
    </>
  );

  if (isLoading) {
    return (
      <div className="space-y-3" aria-busy="true" aria-label="Loading policies">
        <Skeleton className="h-9 w-full max-w-2xl" />
        <Skeleton className="h-72 w-full rounded-2xl" />
      </div>
    );
  }

  if (error) {
    return <ErrorBanner message={`Couldn't load guardrail policies. ${error instanceof Error ? error.message : ""} Reload the page to try again.`.replace(/\s+/g, " ").trim()} />;
  }

  return (
    <>
      {all.length === 0 ? (
        <div className="mb-5 rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <h2 className="text-[14px] font-medium text-fg">No guardrail policies yet</h2>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">Requests pass through unchecked until a policy exists.</p>
          <Button className="mt-4" onClick={onCreate}>
            <Plus aria-hidden="true" />
            Create global policy
          </Button>
        </div>
      ) : (
        <>
          {!hasGlobal && (
            <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-warn/30 bg-warn/10 px-3 py-2 text-[12.5px]">
              <span className="text-warn">No global policy: traffic no override matches is unchecked.</span>
              <button type="button" onClick={createGlobal} className="font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
                Add global policy
              </button>
            </div>
          )}

          <div className="mb-3 flex flex-col gap-3 lg:flex-row lg:items-center">
            <div className="relative lg:w-72">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search name, target or detector"
                aria-label="Search policies"
                className="h-9 w-full rounded-lg border border-input bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              />
              {search && (
                <button
                  type="button"
                  onClick={() => setSearch("")}
                  className="absolute right-1.5 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                  aria-label="Clear search"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Filter by scope" onKeyDown={rovingKeys('[role="radio"]')}>
              {(["all", ...SCOPES] as ScopeFilter[]).map((s) => {
                const active = scopeFilter === s;
                return (
                  <button
                    key={s}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    tabIndex={active ? 0 : -1}
                    onClick={() => onScopeFilter(s)}
                    className={chipClass(active)}
                  >
                    {s === "all" ? "All" : SCOPE_LABEL[s]}
                    <span className={cn("tabular-nums", !active && "text-fg-faint")}>{counts[s]}</span>
                  </button>
                );
              })}
            </div>
            <p role="status" className="text-[12.5px] tabular-nums text-fg-muted lg:ml-auto">
              {filtering ? `${visible.length} of ${all.length} policies` : `${all.length} polic${all.length === 1 ? "y" : "ies"}`}
            </p>
          </div>

          <section aria-label="Policies" className="mb-5 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
            {visible.length === 0 ? (
              <div className="px-6 py-10 text-center">
                {search.trim() ? (
                  <>
                    <p className="text-[13px] font-medium text-fg">No policies match</p>
                    <p className="mt-1 text-[12.5px] text-fg-muted">Try another search or scope.</p>
                    <Button variant="ghost" className="mt-4" onClick={() => setSearch("")}>
                      Clear search
                    </Button>
                  </>
                ) : scopeFilter !== "all" ? (
                  <>
                    <p className="text-[13px] font-medium text-fg">No {SCOPE_LABEL[scopeFilter].toLowerCase()} policies yet</p>
                    <p className="mx-auto mt-1 max-w-md text-[12.5px] text-fg-muted">
                      {scopeFilter === "global" ? "A global policy applies to all traffic." : "Add one to override the global policy here."}
                    </p>
                    <Button className="mt-4" onClick={onCreate}>
                      <Plus aria-hidden="true" />
                      New {SCOPE_LABEL[scopeFilter].toLowerCase()} policy
                    </Button>
                  </>
                ) : null}
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[820px] text-[13px]">
                  <thead>
                    <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                      <th scope="col" className="px-4 py-2 font-medium">Policy</th>
                      <th scope="col" className="px-4 py-2 font-medium">Scope</th>
                      <th scope="col" className="px-4 py-2 font-medium">Detectors</th>
                      <th scope="col" className="px-4 py-2 font-medium">Enforcement</th>
                      <th scope="col" className="px-4 py-2 font-medium">Status</th>
                      <th scope="col" className="px-4 py-2 font-medium">Updated</th>
                      <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {visible.map((p) => (
                      <PolicyRow
                        key={p.id}
                        policy={p}
                        target={targetLabel(p.scope, p.scope_id)}
                        pending={toggling.has(p.id)}
                        onEdit={() => setEditing(p)}
                        onToggle={(enabled) => onToggle(p, enabled)}
                        onDelete={() => onDelete(p)}
                        onOpenKey={p.scope === "apikey" && p.scope_id ? () => navigate(`/keys/${p.scope_id}`) : undefined}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}

      <TenantFlagsCard />
      {modals}
    </>
  );
}

function PolicyRow({
  policy,
  target,
  pending,
  onEdit,
  onToggle,
  onDelete,
  onOpenKey,
}: {
  policy: GuardrailPolicy;
  target?: string;
  pending: boolean;
  onEdit: () => void;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
  onOpenKey?: () => void;
}) {
  const cfg = policy.config ?? {};
  const detectors = useMemo(() => enabledDetectorNames(cfg), [cfg]);
  const strongest = useMemo(() => strongestAction(cfg), [cfg]);
  const stop = (e: React.MouseEvent) => e.stopPropagation();

  return (
    <tr className={cn("cursor-pointer transition-colors hover:bg-hover", !policy.enabled && "text-fg-muted")} onClick={onEdit}>
      <td className="max-w-[240px] px-4 py-2.5">
        <button
          type="button"
          onClick={(e) => {
            stop(e);
            onEdit();
          }}
          className={cn(
            "block max-w-full truncate rounded-sm text-left font-medium hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
            policy.enabled ? "text-fg" : "text-fg-muted",
          )}
          title={policy.name}
        >
          {policy.name}
        </button>
      </td>
      <td className="max-w-[260px] px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <Badge tone="neutral">{SCOPE_LABEL[policy.scope] ?? policy.scope}</Badge>
          {policy.scope === "global" ? (
            <span className="truncate text-[12.5px] text-fg-muted">All traffic</span>
          ) : target ? (
            <span className="min-w-0 truncate" title={policy.scope_id}>
              <span className="text-fg">{target}</span>
              {policy.scope !== "apikey" && <span className="ml-1.5 font-mono text-[12px] text-fg-muted">{policy.scope_id}</span>}
            </span>
          ) : (
            <span className="truncate font-mono text-[12px] text-fg-muted" title={policy.scope_id}>{policy.scope_id || "—"}</span>
          )}
        </div>
      </td>
      <td className="px-4 py-2.5">
        {detectors.length === 0 ? (
          <span className="text-[12.5px] text-fg-muted">None</span>
        ) : (
          <div className="flex flex-wrap gap-1">
            {detectors.map((d) => (
              <Badge key={d} tone="neutral">{d}</Badge>
            ))}
          </div>
        )}
      </td>
      <td className="px-4 py-2.5">
        <EnforcementBadge action={strongest} />
      </td>
      <td className="px-4 py-2.5" onClick={stop}>
        <span className="inline-flex items-center gap-2">
          <Toggle checked={policy.enabled} onChange={onToggle} disabled={pending} label={`Enforce ${policy.name}`} />
          <span className={cn("text-[12.5px]", policy.enabled ? "text-fg" : "text-fg-muted")}>{policy.enabled ? "Active" : "Paused"}</span>
        </span>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted" title={policy.updated_at ? new Date(policy.updated_at).toLocaleString() : undefined}>
        {policy.updated_at ? new Date(policy.updated_at).toLocaleDateString() : "—"}
      </td>
      <td className="px-2 py-2.5" onClick={stop}>
        <DropdownMenu>
          <DropdownMenuTrigger aria-label={`Actions for ${policy.name}`} className={iconButton}>
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onEdit}>
              <Pencil />
              Edit policy
            </DropdownMenuItem>
            {onOpenKey && (
              <DropdownMenuItem onSelect={onOpenKey}>
                <KeyRound />
                Open key page
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem tone="danger" onSelect={onDelete}>
              <Trash2 />
              Delete policy
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </td>
    </tr>
  );
}

// ---- Tenant-wide GDPR / external-engines toggle -----------------------------

function TenantFlagsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const titleId = useId();
  const descId = useId();
  const flags = useQuery({
    queryKey: ["guardrails", "tenant-flags"],
    queryFn: () => api.getGuardrailTenantFlags(),
    staleTime: 30_000,
  });

  const update = useMutation({
    mutationFn: (allow: boolean) =>
      api.putGuardrailTenantFlags({ allow_external_engines: allow }),
    onSuccess: (_data, allow) => {
      qc.invalidateQueries({ queryKey: ["guardrails", "tenant-flags"] });
      toast.success(allow ? "External engines allowed" : "External engines disabled");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to update");
    },
  });

  const allow = flags.data?.allow_external_engines ?? true;

  return (
    <section
      aria-labelledby={titleId}
      className="flex flex-col gap-3 rounded-2xl border border-line bg-surface px-4 py-3 shadow-[var(--shadow-card)] sm:flex-row sm:items-center sm:justify-between sm:gap-8"
    >
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <h2 id={titleId} className="text-[13px] font-semibold text-fg">
            Allow external detector engines
          </h2>
          {!allow && <Badge tone="warning">Native only</Badge>}
        </div>
        <p id={descId} className="mt-0.5 text-[12px] text-fg-muted">
          {allow
            ? "Presidio, OpenAI Moderation and embeddings may receive prompt text"
            : "Prompt text never leaves KeiRouter; policies use native engines"}
        </p>
      </div>
      <Toggle
        checked={allow}
        onChange={(v) => update.mutate(v)}
        disabled={flags.isLoading || update.isPending}
        aria-labelledby={titleId}
        aria-describedby={descId}
      />
    </section>
  );
}

// ---- Export / Import --------------------------------------------------------

function useExportGuardrails() {
  const toast = useToast();
  return async () => {
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
}

function ImportModal({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const fileRef = useRef<HTMLInputElement>(null);
  const [raw, setRaw] = useState("");
  const [fileName, setFileName] = useState<string | null>(null);
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
    setFileName(f.name);
    setRaw(await f.text());
  };

  return (
    <Modal
      open
      onClose={onClose}
      title="Import guardrails bundle"
      subtitle="Same name and scope overwrites the existing policy"
      maxWidth="max-w-2xl"
    >
      <div className="max-h-[70vh] space-y-4 overflow-y-auto px-5 py-4">
        <div className="flex flex-wrap items-center gap-3">
          <input
            ref={fileRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            tabIndex={-1}
            aria-hidden="true"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) onFile(f);
            }}
          />
          <Button variant="secondary" onClick={() => fileRef.current?.click()}>
            <FileJson aria-hidden="true" />
            Choose file
          </Button>
          <span className="min-w-0 truncate text-[12px] text-fg-muted">
            {fileName ? <span className="font-mono text-fg">{fileName}</span> : "or paste below"}
          </span>
        </div>
        <FormField label="Bundle JSON">
          {(ids) => (
            <textarea
              {...ids}
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
              rows={12}
              spellCheck={false}
              placeholder='{ "version": 1, "policies": [ ... ] }'
              className={textareaClass}
            />
          )}
        </FormField>
        <div role="status" aria-live="polite">
          {result && (
            <div className="overflow-hidden rounded-lg border border-line bg-subtle text-[12.5px]">
              {result.imported.length > 0 && (
                <div className="px-3 py-2.5">
                  <p className="font-medium text-fg">Imported <span className="tabular-nums text-fg-muted">{result.imported.length}</span></p>
                  <ul className="mt-1 space-y-0.5 text-fg-muted">
                    {result.imported.map((p, i) => (
                      <li key={i}>
                        {p.name}{" "}
                        <span className="font-mono text-[12px] text-fg-muted">
                          {p.scope}
                          {p.scope_id ? `/${p.scope_id}` : ""}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {result.skipped.length > 0 && (
                <div className={cn("px-3 py-2.5", result.imported.length > 0 && "border-t border-line")}>
                  <p className="font-medium text-warn">Skipped <span className="tabular-nums">{result.skipped.length}</span></p>
                  <ul className="mt-1 space-y-0.5 text-fg-muted">
                    {result.skipped.map((p, i) => (
                      <li key={i}>
                        {p.name} — {p.reason}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {result.imported.length === 0 && result.skipped.length === 0 && (
                <p className="px-3 py-2.5 text-fg-muted">The bundle contained no policies.</p>
              )}
            </div>
          )}
        </div>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onClose}>
          {result ? "Close" : "Cancel"}
        </Button>
        {!result && (
          <Button
            onClick={() => submit.mutate()}
            disabled={!raw.trim() || submit.isPending}
          >
            {submit.isPending ? "Importing…" : "Import bundle"}
          </Button>
        )}
      </DialogFooter>
    </Modal>
  );
}

// ---- Edit / create ----------------------------------------------------------

function DetectorsHeading({ action }: { action?: ReactNode }) {
  return (
    <div className="mb-2 flex min-h-9 items-center justify-between gap-2">
      <h3 className="text-[13px] font-semibold text-fg">Detectors</h3>
      {action}
    </div>
  );
}

function EditPolicyModal({
  policy,
  targetLabel,
  onClose,
  onSaved,
}: {
  policy: GuardrailPolicy;
  targetLabel?: string;
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

  const scopeLabel = SCOPE_LABEL[policy.scope] ?? policy.scope;

  return (
    <Modal
      open
      onClose={onClose}
      title="Edit policy"
      subtitle={policy.scope === "global" ? "Global · all traffic" : `${scopeLabel} · ${targetLabel ?? policy.scope_id}`}
      maxWidth="max-w-3xl"
    >
      <div className="max-h-[70vh] space-y-5 overflow-y-auto px-5 py-4">
        <div className="max-w-md">
          <FormField label="Policy name">
            {(ids) => <Input {...ids} value={name} onChange={(e) => setName(e.target.value)} />}
          </FormField>
        </div>
        <div>
          <DetectorsHeading />
          <GuardrailEditor value={config} onChange={setConfig} />
        </div>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button onClick={() => save.mutate()} disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save policy"}
        </Button>
      </DialogFooter>
    </Modal>
  );
}

function CreatePolicyModal({
  initialScope,
  hasGlobal,
  onClose,
  onCreated,
}: {
  initialScope: GuardrailScope;
  hasGlobal: boolean;
  onClose: () => void;
  onCreated: () => void;
}) {
  const toast = useToast();
  const scopeLabelId = useId();
  const missingId = useId();
  const [scope, setScope] = useState<GuardrailScope>(initialScope);
  const [name, setName] = useState(initialScope === "global" ? "Global Guardrails" : "");
  const [scopeID, setScopeID] = useState("");
  const [config, setConfig] = useState<GuardrailPolicyConfig>({});
  const [pickingTemplate, setPickingTemplate] = useState(false);

  const changeScope = (next: GuardrailScope) => {
    if (next === scope) return;
    setScope(next);
    setScopeID("");
    if (next !== "global" && name === "Global Guardrails") setName("");
    if (next === "global" && !name.trim()) setName("Global Guardrails");
  };

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

  const missingTarget = scope !== "global" && !scopeID.trim();

  return (
    <Modal open onClose={onClose} title="New guardrail policy" subtitle="Enforced as soon as it is created" maxWidth="max-w-3xl">
      <div className="max-h-[70vh] space-y-5 overflow-y-auto px-5 py-4">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <FormField label="Policy name">
            {(ids) => <Input {...ids} value={name} onChange={(e) => setName(e.target.value)} placeholder={defaultNameFor(scope)} />}
          </FormField>
          <div className="space-y-1.5">
            <span id={scopeLabelId} className="block text-[12.5px] font-medium text-fg">
              Scope
            </span>
            <div
              className="inline-flex flex-wrap rounded-xl border border-line bg-subtle p-0.5"
              role="radiogroup"
              aria-labelledby={scopeLabelId}
              onKeyDown={rovingKeys('[role="radio"]')}
            >
              {SCOPES.map((s) => {
                const active = scope === s;
                const disabled = s === "global" && hasGlobal;
                return (
                  <button
                    key={s}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    tabIndex={active ? 0 : -1}
                    disabled={disabled}
                    title={disabled ? "A global policy already exists. Edit it instead." : undefined}
                    onClick={() => changeScope(s)}
                    className={cn(
                      "h-8 rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-40",
                      active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
                    )}
                  >
                    {SCOPE_LABEL[s]}
                  </button>
                );
              })}
            </div>
            {hasGlobal && <p className="text-[12px] text-fg-muted">Global already exists</p>}
          </div>
          {scope === "global" ? (
            <p className="text-[12.5px] text-fg-muted md:col-span-2">Applies to all traffic when no more specific policy matches.</p>
          ) : (
            <div className="md:col-span-1">
              <ScopeIDSelector scope={scope} value={scopeID} onChange={setScopeID} />
            </div>
          )}
        </div>

        <div>
          <DetectorsHeading
            action={
              <Button variant="ghost" onClick={() => setPickingTemplate(true)}>
                <Sparkles className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
                Start from template
              </Button>
            }
          />
          <GuardrailEditor value={config} onChange={setConfig} />
        </div>
      </div>
      <DialogFooter>
        {missingTarget && (
          <span id={missingId} className="mr-auto text-[12.5px] text-fg-muted">
            Pick a {SCOPE_LABEL[scope].toLowerCase()} to create this policy.
          </span>
        )}
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button
          onClick={() => create.mutate()}
          disabled={create.isPending || missingTarget}
          aria-describedby={missingTarget ? missingId : undefined}
        >
          {create.isPending ? "Creating…" : "Create policy"}
        </Button>
      </DialogFooter>
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
  const list = templates.data?.templates ?? [];
  return (
    <Modal open onClose={onClose} title="Start from a template" subtitle="Replaces the current detector settings" maxWidth="max-w-xl">
      <div className="max-h-[70vh] overflow-y-auto px-5 py-4" aria-busy={templates.isLoading}>
        {templates.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full rounded-lg" />
            ))}
          </div>
        ) : list.length === 0 ? (
          <p className="py-6 text-center text-[13px] text-fg-muted">No templates available.</p>
        ) : (
          <ul className="divide-y divide-line overflow-hidden rounded-lg border border-line">
            {list.map((tpl) => (
              <li key={tpl.id}>
                <button
                  type="button"
                  onClick={() => onPick(tpl)}
                  className="group flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
                >
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] font-medium text-fg">{tpl.name}</p>
                    <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">{tpl.description}</p>
                  </div>
                  <ChevronRight className="h-4 w-4 shrink-0 text-fg-faint transition-transform group-hover:translate-x-0.5" strokeWidth={1.75} aria-hidden="true" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
      </DialogFooter>
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

// ---- Audit log --------------------------------------------------------------

function LogsTab() {
  const [detector, setDetector] = useState("");
  const [action, setAction] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [liveOn, setLiveOn] = useState(true);
  const [liveStatus, setLiveStatus] = useState<"connected" | "off">("off");
  const [liveRows, setLiveRows] = useState<GuardrailLogEntry[]>([]);
  const liveLabelId = useId();
  const liveStatusId = useId();

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

  const connected = liveOn && liveStatus === "connected";
  const filtered = !!(detector || action);

  return (
    <section aria-labelledby="guardrail-log-title" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="flex flex-col gap-3 border-b border-line px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <h2 id="guardrail-log-title" className="text-[13px] font-semibold text-fg">Audit log</h2>
          <div className="flex items-center gap-2">
            <Toggle checked={liveOn} onChange={setLiveOn} aria-labelledby={liveLabelId} aria-describedby={liveStatusId} />
            <span id={liveLabelId} className="text-[12.5px] font-medium text-fg">Live</span>
            <span id={liveStatusId} role="status" className="inline-flex items-center gap-1.5 text-[12px] text-fg-muted">
              {liveOn && (
                <span className={cn("h-1.5 w-1.5 rounded-full", connected ? "bg-ok" : "animate-pulse bg-fg-faint")} aria-hidden="true" />
              )}
              {liveOn ? (connected ? "Connected" : "Connecting…") : "Refreshing every 5s"}
            </span>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="sr-only" htmlFor="guardrail-log-detector">Detector</label>
          <Select id="guardrail-log-detector" className="w-full sm:w-36" value={detector} onChange={(e) => setDetector(e.target.value)}>
            <option value="">All detectors</option>
            <option value="pii">PII</option>
            <option value="injection">Injection</option>
            <option value="topics">Topics</option>
            <option value="toxicity">Toxicity</option>
            <option value="bias">Bias</option>
          </Select>
          <label className="sr-only" htmlFor="guardrail-log-action">Action</label>
          <Select id="guardrail-log-action" className="w-full sm:w-36" value={action} onChange={(e) => setAction(e.target.value)}>
            <option value="">All actions</option>
            <option value="block">Block</option>
            <option value="mask">Mask</option>
            <option value="warn">Warn</option>
            <option value="log_only">Log only</option>
          </Select>
        </div>
      </div>

      {logs.isLoading ? (
        <div className="space-y-2 px-4 py-4" aria-busy="true" aria-label="Loading audit log">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : logs.isError && rows.length === 0 ? (
        <div className="px-4 py-4">
          <ErrorBanner message={`Couldn't load audit entries. ${logs.error instanceof Error ? logs.error.message : ""}`.trim()} />
        </div>
      ) : rows.length === 0 ? (
        <div className="px-6 py-12 text-center">
          <p className="text-[13px] font-medium text-fg">{filtered ? "No entries match" : "No audit entries yet"}</p>
          <p className="mx-auto mt-1 max-w-md text-[12.5px] text-fg-muted">
            {filtered ? "Try another detector or action." : "Decisions appear here as guardrails fire."}
          </p>
          {filtered && (
            <Button
              variant="ghost"
              className="mt-4"
              onClick={() => {
                setDetector("");
                setAction("");
              }}
            >
              Clear filters
            </Button>
          )}
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[860px] text-[13px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th scope="col" className="w-10 py-2 pl-2 pr-0"><span className="sr-only">Details</span></th>
                <th scope="col" className="px-3 py-2 font-medium">Time</th>
                <th scope="col" className="px-3 py-2 font-medium">Detector</th>
                <th scope="col" className="px-3 py-2 font-medium">Action</th>
                <th scope="col" className="px-3 py-2 font-medium">Source</th>
                <th scope="col" className="px-3 py-2 font-medium">Findings</th>
                <th scope="col" className="px-3 py-2 pr-4 font-medium">Reason</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((row) => {
                const isOpen = expanded.has(row.id);
                const findings = row.findings ?? [];
                const isTest = row.api_key_id === "test-panel";
                const entityCounts = countEntities(findings);
                const when = new Date(row.created_at).toLocaleString();
                const detailsId = `guardrail-log-${row.id}`;
                return (
                  <Fragment key={row.id}>
                    <tr
                      className={cn("cursor-pointer transition-colors hover:bg-hover", isOpen && "bg-hover")}
                      onClick={() => toggle(row.id)}
                    >
                      <td className="py-1.5 pl-2 pr-0">
                        <button
                          type="button"
                          aria-expanded={isOpen}
                          aria-controls={isOpen ? detailsId : undefined}
                          aria-label={`Details for ${row.detector} ${row.action}, ${when}`}
                          onClick={(e) => {
                            e.stopPropagation();
                            toggle(row.id);
                          }}
                          className={iconButton}
                        >
                          <ChevronRight className={cn("h-4 w-4 text-fg-faint transition-transform", isOpen && "rotate-90")} strokeWidth={1.75} aria-hidden="true" />
                        </button>
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 tabular-nums text-fg-muted">{when}</td>
                      <td className="px-3 py-2">
                        <span className="block font-mono text-[12px] text-fg">{row.detector}</span>
                        {row.severity && <span className="block text-[12px] text-fg-muted">{row.severity} severity</span>}
                      </td>
                      <td className="px-3 py-2">
                        <Badge tone={actionBadgeTone(row.action)}>{row.action}</Badge>
                      </td>
                      <td className="max-w-[220px] px-3 py-2">
                        {isTest ? (
                          <Badge tone="neutral">Test panel</Badge>
                        ) : row.provider || row.model ? (
                          <span className="block truncate font-mono text-[12px] text-fg-muted" title={`${row.provider || "?"}${row.model ? `/${row.model}` : ""}`}>
                            {row.provider || "?"}
                            {row.model ? `/${row.model}` : ""}
                          </span>
                        ) : (
                          <span className="text-fg-faint">—</span>
                        )}
                      </td>
                      <td className="px-3 py-2">
                        {entityCounts.length === 0 ? (
                          <span className="text-fg-faint">—</span>
                        ) : (
                          <div className="flex flex-wrap gap-1">
                            {entityCounts.map(([e, n]) => (
                              <Badge key={e} tone="neutral">
                                <span className="font-mono">{e}</span>
                                {n > 1 ? <span className="tabular-nums">×{n}</span> : null}
                              </Badge>
                            ))}
                          </div>
                        )}
                      </td>
                      <td className="max-w-xs truncate px-3 py-2 pr-4 text-fg-muted" title={row.reason}>
                        {row.reason}
                      </td>
                    </tr>
                    {isOpen && (
                      <tr id={detailsId} className="bg-subtle">
                        <td></td>
                        <td colSpan={6} className="px-3 py-3 pr-4">
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
    </section>
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
    <div className="space-y-2.5 text-[12px]">
      <dl className="flex flex-wrap gap-x-6 gap-y-1 text-fg-muted">
        <div className="flex gap-1.5">
          <dt>Request</dt>
          <dd className="font-mono text-fg">{row.request_id || "—"}</dd>
        </div>
        <div className="flex gap-1.5">
          <dt>Direction</dt>
          <dd className="text-fg">{row.direction}</dd>
        </div>
      </dl>
      {findings.length === 0 ? (
        <p className="text-fg-muted">No findings recorded.</p>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-line text-[12px] text-fg-faint">
                <th scope="col" className="px-3 py-1.5 font-medium">Entity</th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">Score</th>
                <th scope="col" className="px-3 py-1.5 font-medium">Original (truncated)</th>
                <th scope="col" className="px-3 py-1.5 font-medium">Replacement</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {findings.map((f, i) => (
                <tr key={i}>
                  <td className="px-3 py-1.5 font-mono text-fg">{f.entity}</td>
                  <td className="px-3 py-1.5 text-right tabular-nums text-fg-muted">{(f.score * 100).toFixed(0)}%</td>
                  <td className="px-3 py-1.5 font-mono">
                    {f.original ? (
                      <code className="rounded bg-bad/10 px-1.5 py-0.5 text-bad">
                        {f.original}
                      </code>
                    ) : (
                      <span className="text-fg-faint">—</span>
                    )}
                  </td>
                  <td className="px-3 py-1.5 font-mono">
                    {f.redacted ? (
                      <code className="rounded bg-ok/10 px-1.5 py-0.5 text-ok">
                        {f.redacted}
                      </code>
                    ) : (
                      <span className="text-fg-faint">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
