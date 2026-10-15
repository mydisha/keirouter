import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { AlertTriangle, ArrowDown, ArrowLeft, ArrowUp, BadgeCheck, ChevronDown, Copy, DollarSign, Eye, Loader2, Plus, Shield, Store, Tag, X, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type Chain, type ChainStep } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Button, ErrorBanner, Field, IconTile, Input, Modal, ModalFooter, SectionTitle, Select, Skeleton, Textarea, Toggle } from "../components/ui";
import { ICONS } from "../lib/icons";
import { useConfirm } from "../components/ui/confirm-dialog";
import { ChainModelPicker } from "../components/chains/ChainModelPicker";
import { ChainRoutePreview, StepNumber } from "../components/chains/ChainRoutePreview";
import { type ChainStrategy, type DraftChainStep, CACHE_READ_FACTOR, CACHE_WRITE_FACTOR, deriveCacheRates, isValidChainName, makeDraftStep, normalizeChainStrategy, strategyDescription, strategyIcon, strategyLabel, toDraftSteps } from "../components/chains/chainUtils";

const parseMarketSlugs = (raw: string): string[] => {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const line of raw.split("\n")) {
    const s = line.trim();
    if (!s || seen.has(s)) continue;
    seen.add(s);
    out.push(s);
  }
  return out;
};

// Tri-state capability override: "auto" keeps the heuristic, "on"/"off" force
// the public badge.
export type CapState = "auto" | "on" | "off";
type CapKey = "reasoning" | "vision" | "tools";
const CAP_KEYS: CapKey[] = ["reasoning", "vision", "tools"];

const parseCapOverrides = (raw?: string): Record<CapKey, CapState> => {
  const out: Record<CapKey, CapState> = { reasoning: "auto", vision: "auto", tools: "auto" };
  if (!raw) return out;
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    for (const key of CAP_KEYS) {
      if (typeof parsed[key] === "boolean") out[key] = parsed[key] ? "on" : "off";
    }
  } catch {
    /* malformed override: fall back to auto */
  }
  return out;
};

const serializeCapOverrides = (state: Record<CapKey, CapState>): string => {
  const out: Record<string, boolean> = {};
  for (const key of CAP_KEYS) {
    if (state[key] !== "auto") out[key] = state[key] === "on";
  }
  return Object.keys(out).length > 0 ? JSON.stringify(out) : "";
};

type RateField = "inputPerM" | "outputPerM" | "cacheWritePerM" | "cacheReadPerM";
const ZERO_PRICE = { inputPerM: 0, outputPerM: 0, cacheWritePerM: 0, cacheReadPerM: 0 };
const chainPriceOf = (chain?: Chain) => chain
  ? { inputPerM: chain.input_per_m ?? 0, outputPerM: chain.output_per_m ?? 0, cacheWritePerM: chain.cache_write_per_m ?? 0, cacheReadPerM: chain.cache_read_per_m ?? 0 }
  : ZERO_PRICE;
const hasAnyPrice = (p: typeof ZERO_PRICE) => p.inputPerM > 0 || p.outputPerM > 0 || p.cacheWritePerM > 0 || p.cacheReadPerM > 0;

const strategyOptions: { value: ChainStrategy; label: string }[] = [
  { value: "priority", label: "Priority" },
  { value: "round_robin", label: "Round robin" },
  { value: "latency", label: "Latency" },
  { value: "cost", label: "Cost" },
];

const focusRing = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

function Panel({ title, titleId, icon, subtitle, action, children, className }: { title: ReactNode; titleId?: string; icon?: LucideIcon; subtitle?: ReactNode; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section aria-labelledby={titleId} className={cn("rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="border-b border-line px-4 py-3">
        <SectionTitle icon={icon} title={title} id={titleId} subtitle={subtitle} action={action} />
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
      className="grid grid-cols-2 gap-0.5 rounded-xl border border-line bg-subtle p-0.5 sm:inline-flex sm:max-w-full sm:flex-wrap sm:gap-0"
    >
      {strategyOptions.map((option) => {
        const active = option.value === value;
        const Icon = strategyIcon(option.value);
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(option.value)}
            className={cn(
              "inline-flex h-9 items-center justify-center gap-1.5 rounded-lg px-3 text-[12.5px] font-medium transition-colors sm:h-8 sm:justify-start",
              focusRing,
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
            )}
          >
            <Icon className={cn("h-3.5 w-3.5 shrink-0", active ? "text-tone" : "text-fg-faint")} strokeWidth={1.75} aria-hidden="true" />
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

const iconButton = cn("flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-30 sm:h-8 sm:w-8", focusRing);

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
  const categoriesQuery = useQuery({ queryKey: ["provider-categories"], queryFn: () => api.listProviderCategories(), staleTime: 300_000 });
  const marketSettings = useQuery({ queryKey: ["market-pricing-settings"], queryFn: () => api.marketPricingSettings() });
  const existing = (chainsQuery.data?.chains ?? []).find((chain) => chain.id === id);
  const [hydrated, setHydrated] = useState(!isEdit);
  const [dirty, setDirty] = useState(false);
  const [name, setName] = useState("");
  const [strategy, setStrategy] = useState<ChainStrategy>("priority");
  const [steps, setSteps] = useState<DraftChainStep[]>(() => [makeDraftStep()]);
  const [fallbackEnabled, setFallbackEnabled] = useState(false);
  const [fallback, setFallback] = useState<DraftChainStep>(() => makeDraftStep());
  const [displayProvider, setDisplayProvider] = useState("");
  const [capOverrides, setCapOverrides] = useState<Record<CapKey, CapState>>({ reasoning: "auto", vision: "auto", tools: "auto" });
  const [newProviderOpen, setNewProviderOpen] = useState(false);
  const [chainPrice, setChainPrice] = useState(ZERO_PRICE);
  const [marketSlugs, setMarketSlugs] = useState("");
  const [reorderByMarket, setReorderByMarket] = useState(false);
  const [markupEnabled, setMarkupEnabled] = useState(false);
  const [markupValue, setMarkupValue] = useState("");
  const [priceOpen, setPriceOpen] = useState(false);
  const [priceDrafts, setPriceDrafts] = useState<Map<string, string>>(() => new Map());
  const [error, setError] = useState("");
  // Reorder announcements and focus follow-up for keyboard users.
  const [moveAnnouncement, setMoveAnnouncement] = useState("");
  const [pendingFocus, setPendingFocus] = useState<{ stepID: string; direction: -1 | 1 } | null>(null);
  const moveButtons = useRef(new Map<string, { up: HTMLButtonElement | null; down: HTMLButtonElement | null }>());

  // loadCustomFields restores the fork's chain settings (display provider,
  // badges, pricing, market binding, markup) from a saved chain or defaults.
  const loadCustomFields = (chain?: Chain) => {
    setDisplayProvider(chain?.display_provider ?? "");
    setCapOverrides(parseCapOverrides(chain?.capability_overrides));
    setChainPrice(chainPriceOf(chain));
    setPriceDrafts(new Map());
    setMarketSlugs((chain?.market_slugs ?? []).join("\n"));
    setReorderByMarket(chain?.reorder_by_market ?? false);
    setMarkupEnabled(chain?.markup_percent != null);
    setMarkupValue(chain?.markup_percent != null ? String(chain.markup_percent) : "");
    setPriceOpen(hasAnyPrice(chainPriceOf(chain)));
  };

  useEffect(() => {
    if (!existing || hydrated) return;
    setName(existing.name);
    loadCustomFields(existing);
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

  // The sticky save bar must never hide the focused field (WCAG 2.4.11) or the
  // last rows: reserve its height as scroll padding on the page scroller.
  const saveBarRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const bar = saveBarRef.current;
    const scroller = document.getElementById("main");
    if (!bar || !scroller) return;
    const previous = scroller.style.scrollPaddingBottom;
    const sync = () => { scroller.style.scrollPaddingBottom = `${bar.offsetHeight + 24}px`; };
    sync();
    const observer = new ResizeObserver(sync);
    observer.observe(bar);
    return () => { observer.disconnect(); scroller.style.scrollPaddingBottom = previous; };
  }, [hydrated, chainsQuery.isLoading]);

  const completeSteps = steps.filter((step) => step.provider && step.model);
  const incompleteSteps = steps.some((step) => !step.provider || !step.model);
  const duplicateKeys = new Set<string>();
  const duplicate = completeSteps.some((step) => {
    const key = `${step.provider}/${step.model}`;
    if (duplicateKeys.has(key)) return true;
    duplicateKeys.add(key);
    return false;
  });
  const hasMarketSlugs = parseMarketSlugs(marketSlugs).length > 0;
  const priced = !hasMarketSlugs && hasAnyPrice(chainPrice);
  const needsPricing = priced && (chainPrice.inputPerM <= 0 || chainPrice.outputPerM <= 0);
  const markupNumber = Number(markupValue);
  const markupInvalid = markupEnabled && (markupValue.trim() === "" || !Number.isFinite(markupNumber) || markupNumber < 0 || markupNumber > 1000);
  const validationMessage = markupInvalid ? "Chain markup must be between 0 and 1000%." : !name.trim() ? "Add a chain name to continue." : !isValidChainName(name.trim()) ? "Use up to 128 letters, numbers, hyphens, underscores, or dots; begin with a letter or number." : completeSteps.length === 0 ? "Add at least one model to the route." : incompleteSteps ? "Complete or remove every model row before saving." : duplicate ? "Each route step must be a different provider/model target." : fallbackEnabled && (!fallback.provider || !fallback.model) ? "Choose the final fallback model or turn it off." : needsPricing ? "Set both input and output price, or clear all price fields." : "";
  const valid = !validationMessage;
  const routeChain = useMemo(() => ({ id: existing?.id ?? "draft", name, strategy, steps: completeSteps.map((step, position) => ({ provider: step.provider, model: step.model, position } as ChainStep)), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" } as Chain), [completeSteps, existing?.id, fallback.model, fallback.provider, fallbackEnabled, name, strategy]);

  // Field-level messages mirror validationMessage so the reason a save is
  // blocked appears next to the field that causes it.
  const trimmedName = name.trim();
  const nameError = trimmedName && !isValidChainName(trimmedName)
    ? "Use up to 128 letters, numbers, hyphens, underscores, or dots; begin with a letter or number."
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
      const payload = { name: name.trim(), strategy, input_per_m: chainPrice.inputPerM, output_per_m: chainPrice.outputPerM, cache_write_per_m: chainPrice.cacheWritePerM, cache_read_per_m: chainPrice.cacheReadPerM, market_slugs: parseMarketSlugs(marketSlugs), reorder_by_market: reorderByMarket, markup_percent: markupEnabled ? markupNumber : null, steps: completeSteps.map((step) => ({ provider: step.provider, model: step.model, market_slug: step.marketSlug ?? "" })), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "", display_provider: displayProvider, capability_overrides: serializeCapOverrides(capOverrides) };
      return isEdit ? api.updateChain(id!, payload) : api.createChain(payload);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["chains"] });
      queryClient.invalidateQueries({ queryKey: ["health-chains"] });
      toast.success(isEdit ? "Chain updated" : "Chain created", `chain:${name.trim()} is ready to use.`);
      setDirty(false);
      navigate(dashboard("/chains"));
    },
    onError: (saveError: Error) => { setError(saveError.message); toast.error(isEdit ? "Save failed" : "Creation failed", saveError.message); },
  });

  const stepName = (step: DraftChainStep) => step.model || "empty step";
  const updateStep = (stepID: string, next: Partial<DraftChainStep>) => { setSteps((current) => current.map((step) => step.id === stepID ? { ...step, ...next } : step)); setDirty(true); };
  const updateChainRate = (field: RateField, raw: string) => {
    const value = raw === "" ? 0 : Number(raw);
    const validValue = Number.isFinite(value) && value >= 0;
    setPriceDrafts((current) => {
      const next = new Map(current).set(field, raw);
      if (field === "inputPerM") {
        if (chainPrice.cacheWritePerM === 0 || chainPrice.cacheWritePerM === +(chainPrice.inputPerM * CACHE_WRITE_FACTOR).toFixed(8)) next.delete("cacheWritePerM");
        if (chainPrice.cacheReadPerM === 0 || chainPrice.cacheReadPerM === +(chainPrice.inputPerM * CACHE_READ_FACTOR).toFixed(8)) next.delete("cacheReadPerM");
      }
      return next;
    });
    setDirty(true);
    if (!validValue) return;
    setChainPrice((current) => {
      if (field === "inputPerM") return { ...current, inputPerM: value, ...deriveCacheRates(value, { cacheWritePerM: current.cacheWritePerM, cacheReadPerM: current.cacheReadPerM, prevInput: current.inputPerM }) };
      return { ...current, [field]: value };
    });
  };
  const rateDraft = (field: RateField) => priceDrafts.get(field) ?? (chainPrice[field] ? String(chainPrice[field]) : "");
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
    navigate(dashboard("/chains"));
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
    loadCustomFields(existing);
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
          <Link to={dashboard("/chains")} className={cn("inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg", focusRing)}>
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
        <Link to={dashboard("/chains")} onClick={onBackLink} className={cn("inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg", focusRing)}>
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Chains
        </Link>
        <span aria-hidden="true" className="text-fg-faint">/</span>
        <span className="truncate text-fg" aria-current="page">{isEdit ? existing?.name ?? "Chain" : "New chain"}</span>
      </nav>
      <PageHeader title={isEdit ? `Edit ${existing?.name ?? "chain"}` : "Create chain"} />

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-start">
        <div className="min-w-0 space-y-4">
          <Panel title="Name and strategy" titleId="chain-basics-heading" icon={ICONS.chains}>
            <div className="space-y-5 p-4">
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
                  <FieldMessage id="chain-name-hint" tone="muted">Letters, numbers, hyphens, underscores and dots. Call it as <span className="font-mono">chain:{trimmedName || "your-chain"}</span> or by the bare name.</FieldMessage>
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

          <Panel
            title="Display provider"
            titleId="chain-display-provider-heading"
            icon={Tag}
            subtitle="Vendor category on the public model page."
            action={<Button variant="ghost" type="button" onClick={() => setNewProviderOpen(true)}><Plus aria-hidden="true" />Add provider</Button>}
          >
            <div className="p-4">
              <Field label="Provider" hint="Leave as combo to use the default label.">
                <Select value={displayProvider} onChange={(event) => { setDisplayProvider(event.target.value); setDirty(true); }}>
                  <option value="">combo (default)</option>
                  {(categoriesQuery.data?.categories ?? []).map((c) => (
                    <option key={c.id} value={c.id}>{c.label}</option>
                  ))}
                </Select>
              </Field>
            </div>
          </Panel>

          <Panel title="Market slugs" titleId="chain-market-heading" icon={Store} subtitle="Price this chain from the market.">
            <div className="space-y-3 p-4">
              <p className="text-[12px] leading-5 text-fg-muted">One slug per line. Use an inferhub.dev slug (e.g. <span className="font-mono text-fg">cbcn/deepseek-v4.1-flash</span>) or a Surplus Intelligence model as <span className="font-mono text-fg">surplus:&lt;model&gt;</span>. Use <span className="font-mono text-fg">surplus-trusted:&lt;model&gt;</span> to price only from trusted, available, healthy Surplus offers; requests on it are billed Surplus's reported cost plus markup. KeiRouter prices this chain from the cheapest of these plus markup. Leave empty to set the price manually.</p>
              <Field label="Slugs">
                <Textarea
                  value={marketSlugs}
                  onChange={(event) => { setMarketSlugs(event.target.value); setDirty(true); }}
                  rows={4}
                  spellCheck={false}
                  placeholder={"cbcn/deepseek-v4.1-flash\nali/deepseek-v4.1-flash\nsurplus:deepseek-v4.1-flash\nsurplus-trusted:deepseek-v4.1-flash"}
                  className="font-mono"
                />
              </Field>
              {hasMarketSlugs && <FieldMessage tone="muted">Prices below are managed automatically from the market.</FieldMessage>}
            </div>
            <div className="flex flex-wrap items-center gap-3 border-t border-line px-4 py-3">
              <span className="min-w-0 flex-1">
                <label htmlFor="chain-markup-toggle" id="chain-markup-label" className="block cursor-pointer text-[13px] font-medium text-fg">Custom markup for this chain</label>
                <span id="chain-markup-hint" className="mt-0.5 block text-[12px] leading-5 text-fg-muted">
                  {markupEnabled
                    ? "Overrides the global markup for this chain's market price and Surplus-billed requests. 0 = no markup."
                    : `Off: follows the global markup (${marketSettings.data?.markup_percent ?? "…"}%).`}
                </span>
              </span>
              {markupEnabled && (
                <span className="flex items-center gap-2">
                  <Input type="number" min={0} max={1000} step="any" value={markupValue} onChange={(event) => { setMarkupValue(event.target.value); setDirty(true); }} className="w-28" aria-label="Chain markup percent" />
                  <span className="text-[13px] text-fg-muted">%</span>
                </span>
              )}
              <Toggle
                id="chain-markup-toggle"
                aria-labelledby="chain-markup-label"
                aria-describedby="chain-markup-hint"
                checked={markupEnabled}
                onChange={(next) => { setMarkupEnabled(next); if (next && markupValue === "") setMarkupValue(String(marketSettings.data?.markup_percent ?? 0)); setDirty(true); }}
              />
            </div>
          </Panel>

          <Panel
            title="Model pricing"
            titleId="chain-pricing-heading"
            icon={DollarSign}
            subtitle="One price for the whole chain, applied to every step. Blank uses the catalog price."
            action={(
              <button type="button" onClick={() => { setPriceOpen((open) => !open); setDirty(true); }} aria-expanded={priceOpen} className={cn("inline-flex min-h-8 items-center gap-1 rounded-lg px-2 text-[12.5px] font-medium hover:bg-hover", priced ? "text-tone" : "text-fg-muted", focusRing)}>
                Pricing
                <ChevronDown className={cn("h-3.5 w-3.5 transition-transform", priceOpen && "rotate-180")} aria-hidden="true" />
              </button>
            )}
          >
            {priceOpen && (
              <div className="grid gap-3 p-4 sm:grid-cols-4">
                <Field label="Input $/M"><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("inputPerM")} onChange={(event) => updateChainRate("inputPerM", event.target.value)} placeholder="0" /></Field>
                <Field label="Output $/M"><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("outputPerM")} onChange={(event) => updateChainRate("outputPerM", event.target.value)} placeholder="0" /></Field>
                <Field label={`Cache write $/M${chainPrice.cacheWritePerM === +(chainPrice.inputPerM * CACHE_WRITE_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("cacheWritePerM")} onChange={(event) => updateChainRate("cacheWritePerM", event.target.value)} placeholder="auto" /></Field>
                <Field label={`Cache read $/M${chainPrice.cacheReadPerM === +(chainPrice.inputPerM * CACHE_READ_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("cacheReadPerM")} onChange={(event) => updateChainRate("cacheReadPerM", event.target.value)} placeholder="auto" /></Field>
              </div>
            )}
          </Panel>

          <Panel title="Route steps" titleId="chain-steps-heading" icon={ICONS.route}>
            <div className="flex items-center gap-3 border-b border-line px-4 py-3">
              <span className="min-w-0 flex-1">
                <label htmlFor="chain-reorder-toggle" id="chain-reorder-label" className="block cursor-pointer text-[13px] font-medium text-fg">Order by market price</label>
                <span id="chain-reorder-hint" className="mt-0.5 block text-[12px] leading-5 text-fg-muted">Tries the cheapest bound slug first. Requires steps to have market slugs.</span>
              </span>
              <Toggle id="chain-reorder-toggle" aria-labelledby="chain-reorder-label" aria-describedby="chain-reorder-hint" checked={reorderByMarket} onChange={(next) => { setReorderByMarket(next); setDirty(true); }} />
            </div>
            <p id="chain-steps-keyboard-hint" className="sr-only">Press Alt plus Up or Down arrow inside a step to move it.</p>
            <ol className="divide-y divide-line" aria-labelledby="chain-steps-heading" aria-describedby="chain-steps-keyboard-hint">
              {steps.map((step, index) => {
                const isDuplicate = Boolean(step.provider && step.model && duplicateRowKeys.has(`${step.provider}/${step.model}`));
                const position = `step ${index + 1} of ${steps.length}`;
                const model = step.model ? `, ${step.model}` : "";
                return (
                  <li key={step.id} className="space-y-2 px-4 py-2.5" onKeyDown={(event) => onStepKeyDown(event, index)}>
                    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2 sm:flex-nowrap">
                      <span aria-hidden="true"><StepNumber n={index + 1} /></span>
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
                      <div className="flex w-full shrink-0 items-center justify-end gap-2 sm:w-auto sm:gap-0">
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
                    </div>
                    <div className="sm:pl-8">
                      <Field label="Market slug" hint="Inferhub market slug for this step (optional)">
                        <Input value={step.marketSlug} onChange={(event) => updateStep(step.id, { marketSlug: event.target.value })} placeholder="e.g. cbcn/deepseek-v4.1-flash" className="font-mono" />
                      </Field>
                    </div>
                  </li>
                );
              })}
            </ol>
            <span className="sr-only" role="status">{moveAnnouncement}</span>
            <div className="space-y-2 border-t border-line px-4 py-3">
              {stepsError && <FieldMessage id="chain-steps-error">{stepsError}</FieldMessage>}
              <button
                type="button"
                onClick={addStep}
                className={cn("inline-flex min-h-9 w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-line-strong bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:border-tone-ring hover:bg-tone-soft hover:text-tone", focusRing)}
              >
                <Plus className="h-4 w-4 shrink-0" strokeWidth={1.75} aria-hidden="true" />
                Add model
              </button>
            </div>

            <div className="border-t border-line">
              <div className="flex items-center gap-3 px-4 py-3">
                <IconTile icon={Shield} size="sm" tone="warn" />
                <span className="min-w-0 flex-1">
                  <label htmlFor="chain-fallback-toggle" id="chain-fallback-label" className="block cursor-pointer text-[13px] font-medium text-fg">Final fallback</label>
                  <span id="chain-fallback-hint" className="mt-0.5 block text-[12px] leading-5 text-fg-muted">Tried last, after every step fails.</span>
                </span>
                <Toggle aria-labelledby="chain-fallback-label" aria-describedby="chain-fallback-hint" id="chain-fallback-toggle" checked={fallbackEnabled} onChange={(next) => { setFallbackEnabled(next); setDirty(true); }} />
              </div>
              {fallbackEnabled && (
                <div className="mx-4 mb-3 space-y-2 rounded-lg border border-dashed border-warn/50 bg-warn/5 p-2.5">
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

          <Panel title="Capability badges" titleId="chain-caps-heading" icon={BadgeCheck} subtitle="Force the badges on the public model page. Auto keeps the detected value.">
            <ul className="divide-y divide-line">
              {CAP_KEYS.map((key) => (
                <li key={key} className="flex items-center justify-between gap-3 px-4 py-2.5">
                  <span id={`chain-cap-${key}`} className="text-[13px] font-medium capitalize text-fg">{key}</span>
                  <div role="radiogroup" aria-labelledby={`chain-cap-${key}`} className="inline-flex rounded-xl border border-line bg-subtle p-0.5">
                    {(["auto", "on", "off"] as CapState[]).map((state) => {
                      const active = capOverrides[key] === state;
                      return (
                        <button
                          key={state}
                          type="button"
                          role="radio"
                          aria-checked={active}
                          onClick={() => { setCapOverrides((current) => ({ ...current, [key]: state })); setDirty(true); }}
                          className={cn("inline-flex h-7 items-center rounded-lg px-2.5 text-[12px] font-medium capitalize transition-colors", focusRing, active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg")}
                        >
                          {state}
                        </button>
                      );
                    })}
                  </div>
                </li>
              ))}
            </ul>
          </Panel>

          {error && <ErrorBanner message={error} />}
        </div>

        <aside className="min-w-0 lg:sticky lg:top-4" aria-label="Route preview">
          <Panel title="Route preview" titleId="chain-preview-heading" icon={Eye} subtitle={previewSummary}>
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
                className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-40 sm:h-8 sm:w-8", focusRing)}
                aria-label={`Copy model target ${target}`}
                title="Copy model target"
              >
                <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
              </button>
            </div>
          </Panel>
        </aside>
      </div>

      <div ref={saveBarRef} className="sticky bottom-4 z-20 mt-5 flex flex-wrap items-center gap-2 rounded-2xl border border-line bg-subtle px-4 py-3 shadow-[var(--shadow-float)] sm:px-5">
        <span className="flex w-full min-w-0 items-center gap-1.5 text-[12.5px] sm:mr-auto sm:w-auto sm:flex-1" role="status">
          {saveMutation.isPending ? (
            <span className="text-fg-muted">Saving…</span>
          ) : !valid && dirty ? (
            <>
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
              <span className="text-fg-muted sm:truncate">{validationMessage}</span>
            </>
          ) : dirty ? (
            <span className="text-fg">Unsaved changes</span>
          ) : (
            <span className="text-fg-muted">{isEdit ? "No changes yet" : valid ? "Ready to create" : validationMessage}</span>
          )}
        </span>
        {dirty && !saveMutation.isPending && (
          <Button variant="ghost" onClick={discard} className="flex-1 sm:flex-none">Discard</Button>
        )}
        <Button onClick={() => saveMutation.mutate()} disabled={!valid || saveMutation.isPending} aria-busy={saveMutation.isPending || undefined} className="flex-1 sm:flex-none">
          {saveMutation.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
          {isEdit ? "Save changes" : "Create chain"}
        </Button>
      </div>
      <NewProviderCategoryModal
        open={newProviderOpen}
        onClose={() => setNewProviderOpen(false)}
        onCreated={(id) => { setDisplayProvider(id); setDirty(true); setNewProviderOpen(false); }}
      />
    </>
  );
}

function NewProviderCategoryModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [label, setLabel] = useState("");
  const [error, setError] = useState("");
  const create = useMutation({
    mutationFn: () => api.createProviderCategory({ label: label.trim() }),
    onSuccess: (cat) => {
      qc.invalidateQueries({ queryKey: ["provider-categories"] });
      toast.success("Provider added", `${cat.label} is ready to use.`);
      setLabel(""); setError("");
      onCreated(cat.id);
    },
    onError: (e: Error) => setError(e.message),
  });
  const canSubmit = label.trim().length > 0 && !create.isPending;
  const close = () => { setLabel(""); setError(""); onClose(); };
  return (
    <Modal open={open} onClose={close} title="New display provider" subtitle="A label for the public model page filter. The id is derived from the name.">
      <form onSubmit={(e) => { e.preventDefault(); if (canSubmit) create.mutate(); }}>
        <div className="px-5 py-4">
          <Field label="Name" error={error || undefined}>
            <Input value={label} onChange={(e) => { setLabel(e.target.value); setError(""); }} placeholder="e.g. DeepSeek" autoFocus />
          </Field>
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={close}>Cancel</Button>
          <Button type="submit" disabled={!canSubmit}>{create.isPending ? "Adding…" : "Add provider"}</Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
