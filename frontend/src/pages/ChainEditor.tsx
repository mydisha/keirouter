import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { AlertTriangle, ArrowDown, ArrowLeft, ArrowUp, Check, ChevronDown, Clock3, DollarSign, GripVertical, Layers, Loader2, Plus, Repeat2, Shield, X, Zap } from "lucide-react";
import { api, type Chain, type ChainStep } from "../lib/api";
import { dashboard } from "../lib/dashboardRoutes";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Badge, Button, Card, ErrorCard, Field, Input, Modal, Spinner } from "../components/ui";
import { ChainModelPicker } from "../components/chains/ChainModelPicker";
import { ChainRoutePreview } from "../components/chains/ChainRoutePreview";
import { type ChainStrategy, type DraftChainStep, CACHE_READ_FACTOR, CACHE_WRITE_FACTOR, deriveCacheRates, isValidChainName, makeDraftStep, normalizeChainStrategy, strategyDescription, strategyLabel, toDraftSteps } from "../components/chains/chainUtils";

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

const strategyOptions: { value: ChainStrategy; label: string; icon: typeof Zap }[] = [
  { value: "priority", label: "Priority", icon: Zap },
  { value: "round_robin", label: "Round robin", icon: Repeat2 },
  { value: "latency", label: "Latency", icon: Clock3 },
  { value: "cost", label: "Cost", icon: DollarSign },
];

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

export function ChainEditorPage() {
  const { id } = useParams();
  const isEdit = Boolean(id);
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();
  const chainsQuery = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains() });
  const providersQuery = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const categoriesQuery = useQuery({ queryKey: ["provider-categories"], queryFn: () => api.listProviderCategories(), staleTime: 300_000 });
  const existing = (chainsQuery.data?.chains ?? []).find((chain) => chain.id === id);
  const [hydrated, setHydrated] = useState(!isEdit);
  const [dirty, setDirty] = useState(false);
  const [confirmExit, setConfirmExit] = useState(false);
  const [name, setName] = useState("");
  const [displayProvider, setDisplayProvider] = useState("");
  const [capOverrides, setCapOverrides] = useState<Record<CapKey, CapState>>({ reasoning: "auto", vision: "auto", tools: "auto" });
  const [newProviderOpen, setNewProviderOpen] = useState(false);
  const [strategy, setStrategy] = useState<ChainStrategy>("priority");
  const [steps, setSteps] = useState<DraftChainStep[]>(() => [makeDraftStep()]);
  const [fallbackEnabled, setFallbackEnabled] = useState(false);
  const [fallback, setFallback] = useState<DraftChainStep>(() => makeDraftStep());
  const [chainPrice, setChainPrice] = useState({ inputPerM: 0, outputPerM: 0, cacheWritePerM: 0, cacheReadPerM: 0 });
  const [marketSlugs, setMarketSlugs] = useState("");
  const [reorderByMarket, setReorderByMarket] = useState(false);
  const [markupEnabled, setMarkupEnabled] = useState(false);
  const [markupValue, setMarkupValue] = useState("");
  const marketSettings = useQuery({ queryKey: ["market-pricing-settings"], queryFn: () => api.marketPricingSettings() });
  const [priceOpen, setPriceOpen] = useState(false);
  const [priceDrafts, setPriceDrafts] = useState<Map<string, string>>(() => new Map());
  const [error, setError] = useState("");

  useEffect(() => {
    if (!existing || hydrated) return;
    setName(existing.name);
    setDisplayProvider(existing.display_provider ?? "");
    setCapOverrides(parseCapOverrides(existing.capability_overrides));
    setStrategy(normalizeChainStrategy(existing.strategy));
    setSteps(toDraftSteps(existing));
    setFallbackEnabled(Boolean(existing.fallback_provider && existing.fallback_model));
    setFallback(makeDraftStep(existing.fallback_provider && existing.fallback_model ? { provider: existing.fallback_provider, model: existing.fallback_model } : undefined));
    setChainPrice({ inputPerM: existing.input_per_m ?? 0, outputPerM: existing.output_per_m ?? 0, cacheWritePerM: existing.cache_write_per_m ?? 0, cacheReadPerM: existing.cache_read_per_m ?? 0 });
    setMarketSlugs((existing.market_slugs ?? []).join("\n"));
    setReorderByMarket(existing.reorder_by_market ?? false);
    setMarkupEnabled(existing.markup_percent != null);
    setMarkupValue(existing.markup_percent != null ? String(existing.markup_percent) : "");
    setPriceOpen((existing.input_per_m ?? 0) > 0 || (existing.output_per_m ?? 0) > 0 || (existing.cache_write_per_m ?? 0) > 0 || (existing.cache_read_per_m ?? 0) > 0);
    setHydrated(true);
  }, [existing, hydrated]);

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
  const priced = !hasMarketSlugs && (chainPrice.inputPerM > 0 || chainPrice.outputPerM > 0 || chainPrice.cacheWritePerM > 0 || chainPrice.cacheReadPerM > 0);
  const needsPricing = priced && (chainPrice.inputPerM <= 0 || chainPrice.outputPerM <= 0);
  const markupNumber = Number(markupValue);
  const markupInvalid = markupEnabled && (markupValue.trim() === "" || !Number.isFinite(markupNumber) || markupNumber < 0 || markupNumber > 1000);
  const validationMessage = markupInvalid ? "Chain markup must be between 0 and 1000%." : !name.trim() ? "Add a chain name to continue." : !isValidChainName(name.trim()) ? "Use up to 128 letters, numbers, hyphens, underscores, or dots; begin with a letter or number." : completeSteps.length === 0 ? "Add at least one model to the route." : incompleteSteps ? "Complete or remove every model row before saving." : duplicate ? "Each route step must be a different provider/model target." : fallbackEnabled && (!fallback.provider || !fallback.model) ? "Choose the final fallback model or turn it off." : needsPricing ? "Set both input and output price, or clear all price fields." : "";
  const valid = !validationMessage;
  const routeChain = useMemo(() => ({ id: existing?.id ?? "draft", name, strategy, steps: completeSteps.map((step, position) => ({ provider: step.provider, model: step.model, position } as ChainStep)), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" } as Chain), [completeSteps, existing?.id, fallback.model, fallback.provider, fallbackEnabled, name, strategy]);

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

  const updateStep = (stepID: string, next: Partial<DraftChainStep>) => { setSteps((current) => current.map((step) => step.id === stepID ? { ...step, ...next } : step)); setDirty(true); };
  const updateChainRate = (field: "inputPerM" | "outputPerM" | "cacheWritePerM" | "cacheReadPerM", raw: string) => {
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
  const rateDraft = (field: "inputPerM" | "outputPerM" | "cacheWritePerM" | "cacheReadPerM") => priceDrafts.get(field) ?? (chainPrice[field] ? String(chainPrice[field]) : "");
  const moveStep = (index: number, direction: -1 | 1) => { setSteps((current) => { const target = index + direction; if (target < 0 || target >= current.length) return current; const next = [...current]; [next[index], next[target]] = [next[target], next[index]]; return next; }); setDirty(true); };
  const removeStep = (stepID: string) => { setSteps((current) => current.length === 1 ? current : current.filter((step) => step.id !== stepID)); setDirty(true); };
  const exit = () => { if (dirty) setConfirmExit(true); else navigate(dashboard("/chains")); };

  if (chainsQuery.isLoading || (isEdit && !hydrated)) return <Spinner />;
  if (chainsQuery.isError) return <ErrorCard message="Could not load this chain. Please return to Chains and try again." />;
  if (isEdit && !existing) return <ErrorCard message="This chain no longer exists." />;

  return <>
    <PageHeader title={isEdit ? `Edit ${existing?.name ?? "chain"}` : "Create chain"} icon={Layers} description="Set the routing rule, then build the model path that requests follow." action={<><Button variant="ghost" onClick={exit}><ArrowLeft className="h-4 w-4" />Back to chains</Button><Button onClick={() => saveMutation.mutate()} disabled={!valid || saveMutation.isPending}>{saveMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}{isEdit ? "Save changes" : "Create chain"}</Button></>} />
    <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_320px] xl:items-start">
      <div className="space-y-5">
        <Card className="p-5 sm:p-6"><Field label="Chain name"><Input value={name} onChange={(event) => { setName(event.target.value); setDirty(true); }} placeholder="production-fallback" className="font-mono" data-modal-autofocus /><p className={`text-xs ${name && !isValidChainName(name) ? "text-[color:var(--color-danger)]" : "text-[var(--text-muted)]"}`}>Use as <span className="font-mono">chain:{name || "your-chain"}</span> or the bare name as a model target.</p></Field></Card>
        <Card className="p-5 sm:p-6">
          <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 className="text-base font-semibold">Display provider</h2>
              <p className="mt-1 text-sm text-[var(--text-muted)]">The vendor category shown on the public model page. Leave as combo to use the default label.</p>
            </div>
            <Button variant="ghost" type="button" onClick={() => setNewProviderOpen(true)}><Plus className="h-4 w-4" />Add provider</Button>
          </div>
          <Field label="Provider">
            <select
              value={displayProvider}
              onChange={(event) => { setDisplayProvider(event.target.value); setDirty(true); }}
              className="min-h-10 w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 text-sm"
            >
              <option value="">combo (default)</option>
              {(categoriesQuery.data?.categories ?? []).map((c) => (
                <option key={c.id} value={c.id}>{c.label}</option>
              ))}
            </select>
          </Field>
        </Card>
        <Card className="p-5 sm:p-6"><div className="mb-3"><h2 className="text-base font-semibold">Routing strategy</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Choose how KeiRouter decides which route step starts first.</p></div><div className="grid gap-2 sm:grid-cols-2"><div className="grid grid-cols-2 gap-2 sm:col-span-2 lg:grid-cols-4">{strategyOptions.map((option) => { const Icon = option.icon; const selected = strategy === option.value; return <button key={option.value} type="button" onClick={() => { setStrategy(option.value); setDirty(true); }} className={`flex min-h-11 items-center justify-center gap-2 rounded-xl border px-3 text-sm font-semibold transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40 ${selected ? "border-accent-500 bg-accent-500/10 text-accent-700 dark:text-accent-200" : "border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] hover:border-[var(--border-strong)] hover:text-[var(--text)]"}`}><Icon className="h-4 w-4" />{option.label}</button>; })}</div><p className="sm:col-span-2 text-sm leading-6 text-[var(--text-muted)]">{strategyDescription(strategy)}</p></div></Card>
        <Card className="p-5 sm:p-6">
          <div className="mb-3">
            <h2 className="text-base font-semibold">Market slugs</h2>
            <p className="mt-1 text-sm text-[var(--text-muted)]">One slug per line. Use an inferhub.dev slug (e.g. <span className="font-mono">cbcn/deepseek-v4.1-flash</span>) or a Surplus Intelligence model as <span className="font-mono">surplus:&lt;model&gt;</span>. Use <span className="font-mono">surplus-trusted:&lt;model&gt;</span> to price only from trusted, available, healthy Surplus offers; requests on it are billed Surplus's reported cost plus markup. KeiRouter prices this chain from the cheapest of these plus markup. Leave empty to set the price manually.</p>
          </div>
          <Field label="Slugs">
            <textarea
              value={marketSlugs}
              onChange={(event) => { setMarketSlugs(event.target.value); setDirty(true); }}
              rows={4}
              spellCheck={false}
              placeholder={"cbcn/deepseek-v4.1-flash\nali/deepseek-v4.1-flash\nsurplus:deepseek-v4.1-flash\nsurplus-trusted:deepseek-v4.1-flash"}
              className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] p-3 font-mono text-sm text-[var(--text)] outline-none focus:border-accent-500"
            />
          </Field>
          {hasMarketSlugs && <p className="mt-2 text-xs text-[var(--text-muted)]">Prices below are managed automatically from the market.</p>}
          <div className="mt-4 flex flex-wrap items-start gap-3 border-t border-[var(--border)] pt-4">
            <label className="relative mt-0.5 inline-flex h-6 w-11 shrink-0 cursor-pointer items-center">
              <input type="checkbox" checked={markupEnabled} onChange={(event) => { setMarkupEnabled(event.target.checked); if (event.target.checked && markupValue === "") setMarkupValue(String(marketSettings.data?.markup_percent ?? 0)); setDirty(true); }} className="peer sr-only" aria-label="Custom markup for this chain" />
              <span className="absolute inset-0 rounded-full bg-ink-300 transition-colors peer-checked:bg-accent-600 peer-focus-visible:ring-2 peer-focus-visible:ring-accent-400/50 dark:bg-ink-700" />
              <span className="relative ml-1 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-5" />
            </label>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium">Custom markup for this chain</p>
              <p className="text-xs text-[var(--text-muted)]">
                {markupEnabled
                  ? "Overrides the global markup for this chain's market price and Surplus-billed requests. 0 = no markup."
                  : `Off: follows the global markup (${marketSettings.data?.markup_percent ?? "…"}%).`}
              </p>
            </div>
            {markupEnabled && (
              <div className="flex items-center gap-2">
                <Input type="number" min={0} max={1000} step="any" value={markupValue} onChange={(event) => { setMarkupValue(event.target.value); setDirty(true); }} className="w-28" aria-label="Chain markup percent" />
                <span className="text-sm text-[var(--text-muted)]">%</span>
              </div>
            )}
          </div>
        </Card>
        <Card className="p-5 sm:p-6">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div><h2 className="text-base font-semibold">Model pricing</h2><p className="mt-1 text-sm text-[var(--text-muted)]">One price for the whole chain model, applied to every route step. Leave blank to use the catalog price.</p></div>
            <button type="button" onClick={() => { setPriceOpen((open) => !open); setDirty(true); }} aria-expanded={priceOpen} className={`flex h-9 items-center gap-1 rounded-lg px-2 text-xs font-medium ${priced ? "text-accent-700 dark:text-accent-300" : "text-[var(--text-muted)]"} hover:bg-[var(--bg-elevated)]`}><DollarSign className="h-3.5 w-3.5" /><span>Pricing</span><ChevronDown className={`h-3.5 w-3.5 transition-transform ${priceOpen ? "rotate-180" : ""}`} /></button>
          </div>
          {priceOpen && <div className="mt-4 grid gap-2 border-t border-[var(--border)] pt-4 sm:grid-cols-4">
            <Field label="Input $/M"><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("inputPerM")} onChange={(event) => updateChainRate("inputPerM", event.target.value)} placeholder="0" /></Field>
            <Field label="Output $/M"><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("outputPerM")} onChange={(event) => updateChainRate("outputPerM", event.target.value)} placeholder="0" /></Field>
            <Field label={`Cache write $/M${chainPrice.cacheWritePerM === +(chainPrice.inputPerM * CACHE_WRITE_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("cacheWritePerM")} onChange={(event) => updateChainRate("cacheWritePerM", event.target.value)} placeholder="auto" /></Field>
            <Field label={`Cache read $/M${chainPrice.cacheReadPerM === +(chainPrice.inputPerM * CACHE_READ_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" disabled={hasMarketSlugs} value={rateDraft("cacheReadPerM")} onChange={(event) => updateChainRate("cacheReadPerM", event.target.value)} placeholder="auto" /></Field>
          </div>}
        </Card>
        <Card className="overflow-visible p-5 sm:p-6"><div className="mb-4 flex flex-wrap items-start justify-between gap-3"><div><h2 className="text-base font-semibold">Model route</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Each completed row is an eligible target. Reorder the path to set its declared priority.</p></div><Badge tone="neutral">{completeSteps.length} configured</Badge></div><div className="mb-3 flex items-center gap-2"><label className="relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center"><input type="checkbox" checked={reorderByMarket} onChange={(event) => { setReorderByMarket(event.target.checked); setDirty(true); }} className="peer sr-only" aria-label="Order by market price" /><span className="absolute inset-0 rounded-full bg-ink-300 transition-colors peer-checked:bg-accent-600 peer-focus-visible:ring-2 peer-focus-visible:ring-accent-400/50 dark:bg-ink-700" /><span className="relative ml-1 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-5" /></label><div><p className="text-sm font-medium">Order by market price</p><p className="text-xs text-[var(--text-muted)]">Tries the cheapest bound slug first. Requires steps to have market slugs.</p></div></div><div className="space-y-2">{steps.map((step, index) => { return <div key={step.id} className="flex flex-col gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/35 p-3"><div className="grid gap-2 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-center"><div className="flex items-center gap-2"><GripVertical className="h-4 w-4 text-[var(--text-muted)]" aria-hidden="true" /><span className="flex h-7 w-7 items-center justify-center rounded-full bg-[var(--bg-elevated)] text-xs font-semibold text-[var(--text-muted)]">{index + 1}</span></div><ChainModelPicker value={step} providers={providersQuery.data?.providers ?? []} onChange={(next) => updateStep(step.id, next)} autoFocus={!isEdit && index === 0 && !step.model} /><div className="flex items-center justify-end gap-1"><button type="button" disabled={index === 0} onClick={() => moveStep(index, -1)} className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] disabled:cursor-not-allowed disabled:opacity-30" aria-label={`Move step ${index + 1} up`}><ArrowUp className="h-4 w-4" /></button><button type="button" disabled={index === steps.length - 1} onClick={() => moveStep(index, 1)} className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] disabled:cursor-not-allowed disabled:opacity-30" aria-label={`Move step ${index + 1} down`}><ArrowDown className="h-4 w-4" /></button><button type="button" disabled={steps.length === 1} onClick={() => removeStep(step.id)} className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[color:var(--color-danger)]/10 hover:text-[color:var(--color-danger)] disabled:cursor-not-allowed disabled:opacity-30" aria-label={`Remove step ${index + 1}`}><X className="h-4 w-4" /></button></div></div><Field label="Market slug"><Input value={step.marketSlug} onChange={(event) => updateStep(step.id, { marketSlug: event.target.value })} placeholder="e.g. cbcn/deepseek-v4.1-flash" className="font-mono" /><p className="mt-1 text-xs text-[var(--text-muted)]">Inferhub market slug for this step (optional)</p></Field></div>; })}</div><Button variant="ghost" className="mt-3 w-full border-dashed" onClick={() => { setSteps((current) => [...current, makeDraftStep()]); setDirty(true); }}><Plus className="h-4 w-4" />Add model</Button></Card>
        <Card className="overflow-visible p-5 sm:p-6"><div className="mb-4 flex flex-wrap items-start justify-between gap-3"><div><h2 className="text-base font-semibold">Capability badges</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Force the reasoning, vision, and tools badges shown on the public model page. Auto keeps the detected value.</p></div></div><div className="space-y-3">{CAP_KEYS.map((key) => (<div key={key} className="flex items-center justify-between gap-3"><span className="text-sm font-medium capitalize">{key}</span><div className="inline-flex overflow-hidden rounded-lg border border-[var(--border)]">{(['auto', 'on', 'off'] as CapState[]).map((state) => (<button key={state} type="button" onClick={() => { setCapOverrides((current) => ({ ...current, [key]: state })); setDirty(true); }} className={`px-3 py-1.5 text-xs font-medium capitalize transition-colors ${capOverrides[key] === state ? 'bg-accent-600 text-white' : 'text-[var(--text-muted)] hover:bg-[var(--bg-subtle)]'}`}>{state}</button>))}</div></div>))}</div></Card>
        <Card className="overflow-visible p-5 sm:p-6"><div className="flex items-start justify-between gap-4"><div className="flex gap-3"><div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-[color:var(--color-warning)]/10 text-[color:var(--color-warning)]"><Shield className="h-4.5 w-4.5" /></div><div><h2 className="text-base font-semibold">Final fallback</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Optional. This model is always tried last after every route step fails.</p></div></div><label className="relative mt-1 inline-flex h-6 w-11 shrink-0 cursor-pointer items-center"><input type="checkbox" checked={fallbackEnabled} onChange={(event) => { setFallbackEnabled(event.target.checked); setDirty(true); }} className="peer sr-only" aria-label="Enable final fallback" /><span className="absolute inset-0 rounded-full bg-ink-300 transition-colors peer-checked:bg-accent-600 peer-focus-visible:ring-2 peer-focus-visible:ring-accent-400/50 dark:bg-ink-700" /><span className="relative ml-1 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-5" /></label></div>{fallbackEnabled && <div className="mt-4 border-t border-[var(--border)] pt-4"><ChainModelPicker value={fallback} providers={providersQuery.data?.providers ?? []} onChange={(next) => { setFallback((current) => ({ ...current, ...next })); setDirty(true); }} /></div>}</Card>
        {error && <div role="alert" className="flex items-start gap-2 rounded-xl border border-[color:var(--color-danger)]/30 bg-[color:var(--color-danger)]/10 px-3.5 py-3 text-sm text-[color:var(--color-danger)]"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />{error}</div>}
      </div>
      <aside className="xl:sticky xl:top-5"><Card className="p-5"><p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Route summary</p><div className="mt-3"><p className="truncate font-mono text-base font-semibold">chain:{name || "your-chain"}</p><p className="mt-1 text-sm text-[var(--text-muted)]">{strategyLabel(strategy)} · {completeSteps.length} configured model{completeSteps.length === 1 ? "" : "s"}</p></div><div className="my-5 border-t border-[var(--border)]" /><p className="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Effective route</p><ChainRoutePreview chain={routeChain} providers={providersQuery.data?.providers ?? []} /><div className="mt-5 rounded-lg bg-[var(--bg-subtle)] px-3 py-2.5 text-xs leading-5 text-[var(--text-muted)]">{strategyDescription(strategy)}</div>{validationMessage && <p className="mt-4 flex gap-2 text-xs leading-5 text-[color:var(--color-warning)]"><AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />{validationMessage}</p>}</Card></aside>
    </div>
    <Modal open={confirmExit} onClose={() => setConfirmExit(false)} title="Discard unsaved changes" subtitle="Your route edits have not been saved."><div className="flex justify-end gap-2 px-6 py-4"><Button variant="ghost" onClick={() => setConfirmExit(false)}>Keep editing</Button><Button variant="danger" onClick={() => navigate(dashboard("/chains"))}>Discard changes</Button></div></Modal>
    <NewProviderCategoryModal
      open={newProviderOpen}
      onClose={() => setNewProviderOpen(false)}
      onCreated={(id) => { setDisplayProvider(id); setDirty(true); setNewProviderOpen(false); }}
    />
  </>;
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
  return (
    <Modal open={open} onClose={() => { setLabel(""); setError(""); onClose(); }} title="New display provider" subtitle="A label for the public model page filter. The id is derived from the name.">
      <form className="space-y-4 px-6 py-5" onSubmit={(e) => { e.preventDefault(); if (canSubmit) create.mutate(); }}>
        <Field label="Name">
          <Input value={label} onChange={(e) => { setLabel(e.target.value); setError(""); }} placeholder="e.g. DeepSeek" autoFocus />
        </Field>
        {error && <p className="text-sm text-[color:var(--color-danger)]">{error}</p>}
        <div className="flex items-center justify-end gap-2 pt-1">
          <Button type="button" variant="ghost" onClick={() => { setLabel(""); setError(""); onClose(); }}>Cancel</Button>
          <Button type="submit" disabled={!canSubmit}>{create.isPending ? "Adding…" : "Add provider"}</Button>
        </div>
      </form>
    </Modal>
  );
}
