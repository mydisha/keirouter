import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { AlertTriangle, ArrowDown, ArrowLeft, ArrowUp, Copy, Loader2, Plus, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type Chain } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Button, ErrorBanner, Input, Skeleton, Toggle } from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import { ChainModelPicker } from "../components/chains/ChainModelPicker";
import { ChainRoutePreview } from "../components/chains/ChainRoutePreview";
import { type ChainStrategy, type DraftChainStep, isValidChainName, makeDraftStep, normalizeChainStrategy, strategyDescription, strategyLabel, toDraftSteps } from "../components/chains/chainUtils";

const strategyOptions: { value: ChainStrategy; label: string }[] = [
  { value: "priority", label: "Priority" },
  { value: "round_robin", label: "Round robin" },
  { value: "latency", label: "Latency" },
  { value: "cost", label: "Cost" },
];

const focusRing = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

function Panel({ title, titleId, subtitle, action, children, className }: { title: ReactNode; titleId?: string; subtitle?: ReactNode; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section aria-labelledby={titleId} className={cn("rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3 sm:px-5">
        <div className="min-w-0">
          <h2 id={titleId} className="text-[13px] font-semibold text-fg">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
        </div>
        {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
      </div>
      {children}
    </section>
  );
}

function FieldMessage({ id, tone = "bad", children }: { id?: string; tone?: "bad" | "muted"; children: ReactNode }) {
  return (
    <p id={id} className={cn("flex items-start gap-1.5 text-[12px] leading-5", tone === "bad" ? "text-bad" : "text-fg-muted")}>
      {tone === "bad" && <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />}
      {children}
    </p>
  );
}

// StrategyRadios is a segmented radiogroup with roving focus: Tab enters the
// selected option, arrow keys move and select.
function StrategyRadios({ value, onChange, labelledBy, describedBy }: {
  value: ChainStrategy;
  onChange: (next: ChainStrategy) => void;
  labelledBy: string;
  describedBy?: string;
}) {
  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const keys = ["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp", "Home", "End"];
    if (!keys.includes(event.key)) return;
    event.preventDefault();
    const index = strategyOptions.findIndex((option) => option.value === value);
    const last = strategyOptions.length - 1;
    const next = event.key === "Home" ? 0
      : event.key === "End" ? last
        : event.key === "ArrowRight" || event.key === "ArrowDown" ? (index + 1) % strategyOptions.length
          : (index - 1 + strategyOptions.length) % strategyOptions.length;
    onChange(strategyOptions[next].value);
    event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus();
  };
  return (
    <div
      role="radiogroup"
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      onKeyDown={onKeyDown}
      className="inline-flex max-w-full flex-wrap rounded-xl border border-line bg-subtle p-0.5"
    >
      {strategyOptions.map((option) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(option.value)}
            className={cn(
              "h-7 rounded-lg px-2.5 text-[12px] font-medium transition-colors",
              focusRing,
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
            )}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

const iconButton = cn("flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-30", focusRing);

function EditorSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading chain">
      <Skeleton className="mb-3 h-4 w-40" />
      <Skeleton className="mb-6 h-7 w-64" />
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-start">
        <div className="space-y-5">
          <Skeleton className="h-40 w-full rounded-2xl" />
          <Skeleton className="h-72 w-full rounded-2xl" />
        </div>
        <Skeleton className="h-64 w-full rounded-2xl" />
      </div>
    </div>
  );
}

export function ChainEditorPage() {
  const { id } = useParams();
  const isEdit = Boolean(id);
  const navigate = useNavigate();
  const toast = useToast();
  const confirmAction = useConfirm();
  const queryClient = useQueryClient();
  const chainsQuery = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains() });
  const providersQuery = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const existing = (chainsQuery.data?.chains ?? []).find((chain) => chain.id === id);
  const [hydrated, setHydrated] = useState(!isEdit);
  const [dirty, setDirty] = useState(false);
  const [name, setName] = useState("");
  const [strategy, setStrategy] = useState<ChainStrategy>("priority");
  const [steps, setSteps] = useState<DraftChainStep[]>(() => [makeDraftStep()]);
  const [fallbackEnabled, setFallbackEnabled] = useState(false);
  const [fallback, setFallback] = useState<DraftChainStep>(() => makeDraftStep());
  const [error, setError] = useState("");
  // Reorder announcements and focus follow-up for keyboard users.
  const [moveAnnouncement, setMoveAnnouncement] = useState("");
  const [pendingFocus, setPendingFocus] = useState<{ stepID: string; direction: -1 | 1 } | null>(null);
  const moveButtons = useRef(new Map<string, { up: HTMLButtonElement | null; down: HTMLButtonElement | null }>());

  useEffect(() => {
    if (!existing || hydrated) return;
    setName(existing.name);
    setStrategy(normalizeChainStrategy(existing.strategy));
    setSteps(toDraftSteps(existing));
    setFallbackEnabled(Boolean(existing.fallback_provider && existing.fallback_model));
    setFallback(makeDraftStep(existing.fallback_provider && existing.fallback_model ? { provider: existing.fallback_provider, model: existing.fallback_model } : undefined));
    setHydrated(true);
  }, [existing, hydrated]);

  // After a move, keep focus on the moved step's button. If that button is now
  // disabled (the step reached an end), focus the opposite move button.
  useEffect(() => {
    if (!pendingFocus) return;
    const buttons = moveButtons.current.get(pendingFocus.stepID);
    const preferred = pendingFocus.direction === -1 ? buttons?.up : buttons?.down;
    const other = pendingFocus.direction === -1 ? buttons?.down : buttons?.up;
    (preferred && !preferred.disabled ? preferred : other)?.focus();
    setPendingFocus(null);
  }, [pendingFocus, steps]);

  const completeSteps = steps.filter((step) => step.provider && step.model);
  const incompleteSteps = steps.some((step) => !step.provider || !step.model);
  const duplicateKeys = new Set<string>();
  const duplicate = completeSteps.some((step) => {
    const key = `${step.provider}/${step.model}`;
    if (duplicateKeys.has(key)) return true;
    duplicateKeys.add(key);
    return false;
  });
  const validationMessage = !name.trim() ? "Add a chain name to continue." : !isValidChainName(name.trim()) ? "Use up to 128 letters, numbers, hyphens, or underscores; begin with a letter or number." : completeSteps.length === 0 ? "Add at least one model to the route." : incompleteSteps ? "Complete or remove every model row before saving." : duplicate ? "Each route step must be a different provider/model target." : fallbackEnabled && (!fallback.provider || !fallback.model) ? "Choose the final fallback model or turn it off." : "";
  const valid = !validationMessage;
  const routeChain = useMemo(() => ({ id: existing?.id ?? "draft", name, strategy, steps: completeSteps.map((step, position) => ({ provider: step.provider, model: step.model, position })), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" } as Chain), [completeSteps, existing?.id, fallback.model, fallback.provider, fallbackEnabled, name, strategy]);

  // Field-level messages mirror validationMessage so the reason a save is
  // blocked appears next to the field that causes it.
  const trimmedName = name.trim();
  const nameError = trimmedName && !isValidChainName(trimmedName)
    ? "Use up to 128 letters, numbers, hyphens, or underscores; begin with a letter or number."
    : dirty && !trimmedName ? "Add a chain name to continue." : "";
  const stepsError = completeSteps.length === 0
    ? (dirty ? "Add at least one model to the route." : "")
    : incompleteSteps ? "Complete or remove every model row before saving."
    : duplicate ? "Each route step must be a different provider/model target." : "";
  const duplicateRowKeys = useMemo(() => {
    const seen = new Map<string, number>();
    for (const step of steps) if (step.provider && step.model) seen.set(`${step.provider}/${step.model}`, (seen.get(`${step.provider}/${step.model}`) ?? 0) + 1);
    return new Set([...seen].filter(([, count]) => count > 1).map(([key]) => key));
  }, [steps]);
  const fallbackError = fallbackEnabled && (!fallback.provider || !fallback.model) ? "Choose the final fallback model or turn it off." : "";

  const saveMutation = useMutation({
    mutationFn: () => {
      const payload = { name: name.trim(), strategy, steps: completeSteps.map((step) => ({ provider: step.provider, model: step.model })), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" };
      return isEdit ? api.updateChain(id!, payload) : api.createChain(payload);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["chains"] });
      queryClient.invalidateQueries({ queryKey: ["health-chains"] });
      toast.success(isEdit ? "Chain updated" : "Chain created", `chain:${name.trim()} is ready to use.`);
      setDirty(false);
      navigate("/chains");
    },
    onError: (saveError: Error) => { setError(saveError.message); toast.error(isEdit ? "Save failed" : "Creation failed", saveError.message); },
  });

  const stepName = (step: DraftChainStep) => step.model || "empty step";
  const updateStep = (stepID: string, next: Pick<DraftChainStep, "provider" | "model">) => { setSteps((current) => current.map((step) => step.id === stepID ? { ...step, ...next } : step)); setDirty(true); };
  const moveStep = (index: number, direction: -1 | 1, followFocus = true) => {
    const target = index + direction;
    if (target < 0 || target >= steps.length) return;
    const moved = steps[index];
    setSteps((current) => { const to = index + direction; if (to < 0 || to >= current.length) return current; const next = [...current]; [next[index], next[to]] = [next[to], next[index]]; return next; });
    setDirty(true);
    if (followFocus) setPendingFocus({ stepID: moved.id, direction });
    setMoveAnnouncement(`Moved ${stepName(moved)} to position ${target + 1} of ${steps.length}.`);
  };
  const removeStep = (stepID: string) => {
    const removed = steps.find((step) => step.id === stepID);
    setSteps((current) => current.length === 1 ? current : current.filter((step) => step.id !== stepID));
    setDirty(true);
    if (removed && steps.length > 1) setMoveAnnouncement(`Removed ${stepName(removed)}.`);
  };
  const addStep = () => { setSteps((current) => [...current, makeDraftStep()]); setDirty(true); };
  // Alt+Arrow on a step row reorders it without reaching for the buttons.
  const onStepKeyDown = (event: ReactKeyboardEvent<HTMLLIElement>, index: number) => {
    if (!event.altKey || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
    event.preventDefault();
    moveStep(index, event.key === "ArrowUp" ? -1 : 1, false);
  };

  const exit = async () => {
    if (dirty) {
      const ok = await confirmAction({
        title: "Discard unsaved changes?",
        description: "Your route edits have not been saved. Leaving now throws them away.",
        confirmLabel: "Discard changes",
        cancelLabel: "Keep editing",
        tone: "danger",
      });
      if (!ok) return;
    }
    navigate("/chains");
  };
  const onBackLink = (event: MouseEvent) => {
    if (!dirty) return;
    event.preventDefault();
    void exit();
  };

  // Discard restores the last saved chain (or a blank draft when creating).
  const discard = () => {
    setName(existing?.name ?? "");
    setStrategy(existing ? normalizeChainStrategy(existing.strategy) : "priority");
    setSteps(existing ? toDraftSteps(existing) : [makeDraftStep()]);
    setFallbackEnabled(Boolean(existing?.fallback_provider && existing?.fallback_model));
    setFallback(makeDraftStep(existing?.fallback_provider && existing?.fallback_model ? { provider: existing.fallback_provider, model: existing.fallback_model } : undefined));
    setError("");
    setDirty(false);
  };

  const target = `chain:${trimmedName || "your-chain"}`;
  const copyTarget = async () => {
    try {
      await navigator.clipboard.writeText(`chain:${trimmedName}`);
      toast.success("Chain target copied", `Use chain:${trimmedName} as the model.`);
    } catch {
      toast.error("Copy failed", "Your browser did not allow access to the clipboard.");
    }
  };

  const providers = providersQuery.data?.providers ?? [];

  if (chainsQuery.isLoading || (isEdit && existing && !hydrated)) return <EditorSkeleton />;
  if (chainsQuery.isError || (isEdit && !existing)) {
    return (
      <>
        <nav aria-label="Breadcrumb" className="mb-3 flex items-center gap-1.5 text-[13px] text-fg-muted">
          <Link to="/chains" className={cn("inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg", focusRing)}>
            <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
            Chains
          </Link>
        </nav>
        <ErrorBanner message={chainsQuery.isError ? "Couldn't load this chain. Return to Chains and try again." : "This chain no longer exists. It may have been deleted in another tab."} />
      </>
    );
  }

  const previewSummary = `${strategyLabel(strategy)} · ${completeSteps.length} model${completeSteps.length === 1 ? "" : "s"}${fallbackEnabled && fallback.model ? " + fallback" : ""}`;

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 flex items-center gap-1.5 text-[13px] text-fg-muted">
        <Link to="/chains" onClick={onBackLink} className={cn("inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg", focusRing)}>
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Chains
        </Link>
        <span aria-hidden="true" className="text-fg-faint">/</span>
        <span className="truncate text-fg" aria-current="page">{isEdit ? existing?.name ?? "Chain" : "New chain"}</span>
      </nav>
      <PageHeader title={isEdit ? `Edit ${existing?.name ?? "chain"}` : "Create chain"} />

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-start">
        <div className="min-w-0 space-y-5">
          <Panel title="Name and strategy" titleId="chain-basics-heading">
            <div className="space-y-5 px-4 py-4 sm:px-5">
              <div className="space-y-1.5">
                <label htmlFor="chain-name" className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
                  Chain name
                  <span className="text-[12px] font-normal text-fg-muted">Required</span>
                </label>
                <Input
                  id="chain-name"
                  value={name}
                  onChange={(event) => { setName(event.target.value); setDirty(true); }}
                  placeholder="production-fallback"
                  className={cn("font-mono", nameError && "border-bad")}
                  aria-required="true"
                  aria-invalid={Boolean(nameError)}
                  aria-describedby="chain-name-hint"
                />
                {nameError ? (
                  <FieldMessage id="chain-name-hint">{nameError}</FieldMessage>
                ) : (
                  <FieldMessage id="chain-name-hint" tone="muted">Letters, numbers, hyphens and underscores.</FieldMessage>
                )}
              </div>
              <div className="space-y-2">
                <span id="chain-strategy-label" className="block text-[12.5px] font-medium text-fg">Routing strategy</span>
                <StrategyRadios
                  value={strategy}
                  onChange={(next) => { setStrategy(next); setDirty(true); }}
                  labelledBy="chain-strategy-label"
                  describedBy="chain-strategy-hint"
                />
                <p id="chain-strategy-hint" className="text-[12px] leading-5 text-fg-muted">{strategyDescription(strategy)}</p>
              </div>
            </div>
          </Panel>

          <Panel title="Route steps" titleId="chain-steps-heading">
            <p id="chain-steps-keyboard-hint" className="sr-only">Press Alt plus Up or Down arrow inside a step to move it.</p>
            <ol className="divide-y divide-line" aria-labelledby="chain-steps-heading" aria-describedby="chain-steps-keyboard-hint">
              {steps.map((step, index) => {
                const isDuplicate = Boolean(step.provider && step.model && duplicateRowKeys.has(`${step.provider}/${step.model}`));
                const position = `step ${index + 1} of ${steps.length}`;
                const model = step.model ? `, ${step.model}` : "";
                return (
                  <li key={step.id} className="flex items-center gap-2 px-4 py-2.5 sm:px-5" onKeyDown={(event) => onStepKeyDown(event, index)}>
                    <span className="w-5 shrink-0 text-right text-[12px] font-medium tabular-nums text-fg-muted" aria-hidden="true">{index + 1}</span>
                    <div className="min-w-0 flex-1">
                      <ChainModelPicker
                        value={step}
                        providers={providers}
                        onChange={(next) => updateStep(step.id, next)}
                        autoFocus={!isEdit && index === 0 && !step.model}
                        invalid={isDuplicate}
                        label={`Step ${index + 1} model`}
                        describedBy={stepsError ? "chain-steps-error" : undefined}
                      />
                    </div>
                    <div className="flex shrink-0 items-center">
                      <button
                        type="button"
                        ref={(element) => { const entry = moveButtons.current.get(step.id) ?? { up: null, down: null }; entry.up = element; moveButtons.current.set(step.id, entry); }}
                        disabled={index === 0}
                        onClick={() => moveStep(index, -1)}
                        className={iconButton}
                        aria-label={`Move ${position}${model} up`}
                        title="Move up"
                      >
                        <ArrowUp className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                      </button>
                      <button
                        type="button"
                        ref={(element) => { const entry = moveButtons.current.get(step.id) ?? { up: null, down: null }; entry.down = element; moveButtons.current.set(step.id, entry); }}
                        disabled={index === steps.length - 1}
                        onClick={() => moveStep(index, 1)}
                        className={iconButton}
                        aria-label={`Move ${position}${model} down`}
                        title="Move down"
                      >
                        <ArrowDown className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                      </button>
                      <button
                        type="button"
                        disabled={steps.length === 1}
                        onClick={() => removeStep(step.id)}
                        className={cn(iconButton, "hover:bg-bad/10 hover:text-bad")}
                        aria-label={`Remove ${position}${model}`}
                        title="Remove step"
                      >
                        <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                      </button>
                    </div>
                  </li>
                );
              })}
            </ol>
            <span className="sr-only" role="status">{moveAnnouncement}</span>
            <div className="space-y-2 border-t border-line px-4 py-3 sm:px-5">
              {stepsError && <FieldMessage id="chain-steps-error">{stepsError}</FieldMessage>}
              <Button variant="ghost" className="w-full border border-dashed border-line-strong" onClick={addStep}>
                <Plus aria-hidden="true" />
                Add model
              </Button>
            </div>

            <div className="border-t border-line">
              <div className="flex items-center justify-between gap-4 px-4 py-3 sm:px-5">
                <span className="min-w-0">
                  <label htmlFor="chain-fallback-toggle" id="chain-fallback-label" className="block cursor-pointer text-[13px] font-medium text-fg">Final fallback</label>
                  <span id="chain-fallback-hint" className="mt-0.5 block text-[12px] leading-5 text-fg-muted">Tried last, after every step fails.</span>
                </span>
                <Toggle aria-labelledby="chain-fallback-label" aria-describedby="chain-fallback-hint" id="chain-fallback-toggle" checked={fallbackEnabled} onChange={(next) => { setFallbackEnabled(next); setDirty(true); }} />
              </div>
              {fallbackEnabled && (
                <div className="space-y-2 px-4 pb-3 sm:px-5">
                  <ChainModelPicker
                    value={fallback}
                    providers={providers}
                    onChange={(next) => { setFallback((current) => ({ ...current, ...next })); setDirty(true); }}
                    invalid={Boolean(fallbackError && dirty)}
                    label="Final fallback model"
                    describedBy={fallbackError ? "chain-fallback-error" : undefined}
                  />
                  {fallbackError && <FieldMessage id="chain-fallback-error" tone={dirty ? "bad" : "muted"}>{fallbackError}</FieldMessage>}
                </div>
              )}
            </div>
          </Panel>

          {error && <ErrorBanner message={error} />}
        </div>

        <aside className="min-w-0 lg:sticky lg:top-4" aria-label="Route preview">
          <Panel title="Route preview" titleId="chain-preview-heading" subtitle={previewSummary}>
            <div className="px-4 py-4">
              <ChainRoutePreview chain={routeChain} providers={providers} />
            </div>
            <div className="flex items-center gap-2 border-t border-line bg-subtle px-4 py-2.5">
              <span className="min-w-0 flex-1">
                <span className="block text-[12px] text-fg-muted">Model target</span>
                <span className="block truncate font-mono text-[13px] text-fg" title={target}>{target}</span>
              </span>
              <button
                type="button"
                onClick={copyTarget}
                disabled={!trimmedName}
                className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-40", focusRing)}
                aria-label={`Copy model target ${target}`}
                title="Copy model target"
              >
                <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
              </button>
            </div>
          </Panel>
        </aside>
      </div>

      <div className="sticky bottom-4 z-20 mt-5 flex flex-wrap items-center gap-2 rounded-2xl border border-line bg-subtle px-5 py-3 shadow-[var(--shadow-float)]">
        <span className="mr-auto flex min-w-0 items-center gap-1.5 text-[12.5px]" role="status">
          {saveMutation.isPending ? (
            <span className="text-fg-muted">Saving…</span>
          ) : !valid && dirty ? (
            <>
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
              <span className="truncate text-fg-muted">{validationMessage}</span>
            </>
          ) : dirty ? (
            <span className="text-fg">Unsaved changes</span>
          ) : (
            <span className="text-fg-muted">{isEdit ? "No changes yet" : valid ? "Ready to create" : validationMessage}</span>
          )}
        </span>
        {dirty && !saveMutation.isPending && (
          <Button variant="ghost" onClick={discard}>Discard</Button>
        )}
        <Button onClick={() => saveMutation.mutate()} disabled={!valid || saveMutation.isPending} aria-busy={saveMutation.isPending || undefined}>
          {saveMutation.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
          {isEdit ? "Save changes" : "Create chain"}
        </Button>
      </div>
    </>
  );
}
