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

const strategyOptions: { value: ChainStrategy; label: string; icon: typeof Zap }[] = [
  { value: "priority", label: "Priority", icon: Zap },
  { value: "round_robin", label: "Round robin", icon: Repeat2 },
  { value: "latency", label: "Latency", icon: Clock3 },
  { value: "cost", label: "Cost", icon: DollarSign },
];

export function ChainEditorPage() {
  const { id } = useParams();
  const isEdit = Boolean(id);
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();
  const chainsQuery = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains() });
  const providersQuery = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const existing = (chainsQuery.data?.chains ?? []).find((chain) => chain.id === id);
  const [hydrated, setHydrated] = useState(!isEdit);
  const [dirty, setDirty] = useState(false);
  const [confirmExit, setConfirmExit] = useState(false);
  const [name, setName] = useState("");
  const [strategy, setStrategy] = useState<ChainStrategy>("priority");
  const [steps, setSteps] = useState<DraftChainStep[]>(() => [makeDraftStep()]);
  const [fallbackEnabled, setFallbackEnabled] = useState(false);
  const [fallback, setFallback] = useState<DraftChainStep>(() => makeDraftStep());
  const [chainPrice, setChainPrice] = useState({ inputPerM: 0, outputPerM: 0, cacheWritePerM: 0, cacheReadPerM: 0 });
  const [priceOpen, setPriceOpen] = useState(false);
  const [priceDrafts, setPriceDrafts] = useState<Map<string, string>>(() => new Map());
  const [error, setError] = useState("");

  useEffect(() => {
    if (!existing || hydrated) return;
    setName(existing.name);
    setStrategy(normalizeChainStrategy(existing.strategy));
    setSteps(toDraftSteps(existing));
    setFallbackEnabled(Boolean(existing.fallback_provider && existing.fallback_model));
    setFallback(makeDraftStep(existing.fallback_provider && existing.fallback_model ? { provider: existing.fallback_provider, model: existing.fallback_model } : undefined));
    setChainPrice({ inputPerM: existing.input_per_m ?? 0, outputPerM: existing.output_per_m ?? 0, cacheWritePerM: existing.cache_write_per_m ?? 0, cacheReadPerM: existing.cache_read_per_m ?? 0 });
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
  const priced = chainPrice.inputPerM > 0 || chainPrice.outputPerM > 0 || chainPrice.cacheWritePerM > 0 || chainPrice.cacheReadPerM > 0;
  const needsPricing = priced && (chainPrice.inputPerM <= 0 || chainPrice.outputPerM <= 0);
  const validationMessage = !name.trim() ? "Add a chain name to continue." : !isValidChainName(name.trim()) ? "Use up to 128 letters, numbers, hyphens, underscores, or dots; begin with a letter or number." : completeSteps.length === 0 ? "Add at least one model to the route." : incompleteSteps ? "Complete or remove every model row before saving." : duplicate ? "Each route step must be a different provider/model target." : fallbackEnabled && (!fallback.provider || !fallback.model) ? "Choose the final fallback model or turn it off." : needsPricing ? "Set both input and output price, or clear all price fields." : "";
  const valid = !validationMessage;
  const routeChain = useMemo(() => ({ id: existing?.id ?? "draft", name, strategy, steps: completeSteps.map((step, position) => ({ provider: step.provider, model: step.model, position } as ChainStep)), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" } as Chain), [completeSteps, existing?.id, fallback.model, fallback.provider, fallbackEnabled, name, strategy]);

  const saveMutation = useMutation({
    mutationFn: () => {
      const payload = { name: name.trim(), strategy, input_per_m: chainPrice.inputPerM, output_per_m: chainPrice.outputPerM, cache_write_per_m: chainPrice.cacheWritePerM, cache_read_per_m: chainPrice.cacheReadPerM, steps: completeSteps.map((step) => ({ provider: step.provider, model: step.model })), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" };
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
        <Card className="p-5 sm:p-6"><div className="mb-3"><h2 className="text-base font-semibold">Routing strategy</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Choose how KeiRouter decides which route step starts first.</p></div><div className="grid gap-2 sm:grid-cols-2"><div className="grid grid-cols-2 gap-2 sm:col-span-2 lg:grid-cols-4">{strategyOptions.map((option) => { const Icon = option.icon; const selected = strategy === option.value; return <button key={option.value} type="button" onClick={() => { setStrategy(option.value); setDirty(true); }} className={`flex min-h-11 items-center justify-center gap-2 rounded-xl border px-3 text-sm font-semibold transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40 ${selected ? "border-accent-500 bg-accent-500/10 text-accent-700 dark:text-accent-200" : "border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)] hover:border-[var(--border-strong)] hover:text-[var(--text)]"}`}><Icon className="h-4 w-4" />{option.label}</button>; })}</div><p className="sm:col-span-2 text-sm leading-6 text-[var(--text-muted)]">{strategyDescription(strategy)}</p></div></Card>
        <Card className="p-5 sm:p-6">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div><h2 className="text-base font-semibold">Model pricing</h2><p className="mt-1 text-sm text-[var(--text-muted)]">One price for the whole chain model, applied to every route step. Leave blank to use the catalog price.</p></div>
            <button type="button" onClick={() => { setPriceOpen((open) => !open); setDirty(true); }} aria-expanded={priceOpen} className={`flex h-9 items-center gap-1 rounded-lg px-2 text-xs font-medium ${priced ? "text-accent-700 dark:text-accent-300" : "text-[var(--text-muted)]"} hover:bg-[var(--bg-elevated)]`}><DollarSign className="h-3.5 w-3.5" /><span>Pricing</span><ChevronDown className={`h-3.5 w-3.5 transition-transform ${priceOpen ? "rotate-180" : ""}`} /></button>
          </div>
          {priceOpen && <div className="mt-4 grid gap-2 border-t border-[var(--border)] pt-4 sm:grid-cols-4">
            <Field label="Input $/M"><Input type="number" min={0} step="0.01" value={rateDraft("inputPerM")} onChange={(event) => updateChainRate("inputPerM", event.target.value)} placeholder="0" /></Field>
            <Field label="Output $/M"><Input type="number" min={0} step="0.01" value={rateDraft("outputPerM")} onChange={(event) => updateChainRate("outputPerM", event.target.value)} placeholder="0" /></Field>
            <Field label={`Cache write $/M${chainPrice.cacheWritePerM === +(chainPrice.inputPerM * CACHE_WRITE_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" value={rateDraft("cacheWritePerM")} onChange={(event) => updateChainRate("cacheWritePerM", event.target.value)} placeholder="auto" /></Field>
            <Field label={`Cache read $/M${chainPrice.cacheReadPerM === +(chainPrice.inputPerM * CACHE_READ_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" value={rateDraft("cacheReadPerM")} onChange={(event) => updateChainRate("cacheReadPerM", event.target.value)} placeholder="auto" /></Field>
          </div>}
        </Card>
        <Card className="p-5 sm:p-6"><div className="mb-4 flex flex-wrap items-start justify-between gap-3"><div><h2 className="text-base font-semibold">Model route</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Each completed row is an eligible target. Reorder the path to set its declared priority.</p></div><Badge tone="neutral">{completeSteps.length} configured</Badge></div><div className="space-y-2">{steps.map((step, index) => { return <div key={step.id} className="flex flex-col gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/35 p-3"><div className="grid gap-2 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-center"><div className="flex items-center gap-2"><GripVertical className="h-4 w-4 text-[var(--text-muted)]" aria-hidden="true" /><span className="flex h-7 w-7 items-center justify-center rounded-full bg-[var(--bg-elevated)] text-xs font-semibold text-[var(--text-muted)]">{index + 1}</span></div><ChainModelPicker value={step} providers={providersQuery.data?.providers ?? []} onChange={(next) => updateStep(step.id, next)} autoFocus={!isEdit && index === 0 && !step.model} /><div className="flex items-center justify-end gap-1"><button type="button" disabled={index === 0} onClick={() => moveStep(index, -1)} className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] disabled:cursor-not-allowed disabled:opacity-30" aria-label={`Move step ${index + 1} up`}><ArrowUp className="h-4 w-4" /></button><button type="button" disabled={index === steps.length - 1} onClick={() => moveStep(index, 1)} className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] disabled:cursor-not-allowed disabled:opacity-30" aria-label={`Move step ${index + 1} down`}><ArrowDown className="h-4 w-4" /></button><button type="button" disabled={steps.length === 1} onClick={() => removeStep(step.id)} className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] hover:bg-[color:var(--color-danger)]/10 hover:text-[color:var(--color-danger)] disabled:cursor-not-allowed disabled:opacity-30" aria-label={`Remove step ${index + 1}`}><X className="h-4 w-4" /></button></div></div></div>; })}</div><Button variant="ghost" className="mt-3 w-full border-dashed" onClick={() => { setSteps((current) => [...current, makeDraftStep()]); setDirty(true); }}><Plus className="h-4 w-4" />Add model</Button></Card>
        <Card className="overflow-visible p-5 sm:p-6"><div className="flex items-start justify-between gap-4"><div className="flex gap-3"><div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-[color:var(--color-warning)]/10 text-[color:var(--color-warning)]"><Shield className="h-4.5 w-4.5" /></div><div><h2 className="text-base font-semibold">Final fallback</h2><p className="mt-1 text-sm text-[var(--text-muted)]">Optional. This model is always tried last after every route step fails.</p></div></div><label className="relative mt-1 inline-flex h-6 w-11 shrink-0 cursor-pointer items-center"><input type="checkbox" checked={fallbackEnabled} onChange={(event) => { setFallbackEnabled(event.target.checked); setDirty(true); }} className="peer sr-only" aria-label="Enable final fallback" /><span className="absolute inset-0 rounded-full bg-ink-300 transition-colors peer-checked:bg-accent-600 peer-focus-visible:ring-2 peer-focus-visible:ring-accent-400/50 dark:bg-ink-700" /><span className="relative ml-1 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-5" /></label></div>{fallbackEnabled && <div className="mt-4 border-t border-[var(--border)] pt-4"><ChainModelPicker value={fallback} providers={providersQuery.data?.providers ?? []} onChange={(next) => { setFallback((current) => ({ ...current, ...next })); setDirty(true); }} /></div>}</Card>
        {error && <div role="alert" className="flex items-start gap-2 rounded-xl border border-[color:var(--color-danger)]/30 bg-[color:var(--color-danger)]/10 px-3.5 py-3 text-sm text-[color:var(--color-danger)]"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />{error}</div>}
      </div>
      <aside className="xl:sticky xl:top-5"><Card className="p-5"><p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Route summary</p><div className="mt-3"><p className="truncate font-mono text-base font-semibold">chain:{name || "your-chain"}</p><p className="mt-1 text-sm text-[var(--text-muted)]">{strategyLabel(strategy)} · {completeSteps.length} configured model{completeSteps.length === 1 ? "" : "s"}</p></div><div className="my-5 border-t border-[var(--border)]" /><p className="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Effective route</p><ChainRoutePreview chain={routeChain} providers={providersQuery.data?.providers ?? []} /><div className="mt-5 rounded-lg bg-[var(--bg-subtle)] px-3 py-2.5 text-xs leading-5 text-[var(--text-muted)]">{strategyDescription(strategy)}</div>{validationMessage && <p className="mt-4 flex gap-2 text-xs leading-5 text-[color:var(--color-warning)]"><AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />{validationMessage}</p>}</Card></aside>
    </div>
    <Modal open={confirmExit} onClose={() => setConfirmExit(false)} title="Discard unsaved changes" subtitle="Your route edits have not been saved."><div className="flex justify-end gap-2 px-6 py-4"><Button variant="ghost" onClick={() => setConfirmExit(false)}>Keep editing</Button><Button variant="danger" onClick={() => navigate(dashboard("/chains"))}>Discard changes</Button></div></Modal>
  </>;
}
