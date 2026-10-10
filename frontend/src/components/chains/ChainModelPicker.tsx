import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { ArrowLeft, Check, ChevronDown, Plus, Search, X } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Provider } from "../../lib/api";
import { ModelCapabilityIcons } from "../ModelCapabilityIcons";
import { useModelCatalog } from "../ModelSelect";
import { ProviderLogo } from "../ProviderLogo";
import { Input, Select } from "../ui";
import type { DraftChainStep } from "./chainUtils";
import { isLLMProvider, providerIcon } from "./chainUtils";

const focusRing = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

export function ChainModelPicker({ value, providers, onChange, autoFocus = false, invalid = false, label, describedBy }: {
  value: DraftChainStep;
  providers: Provider[];
  onChange: (next: Pick<DraftChainStep, "provider" | "model">) => void;
  autoFocus?: boolean;
  invalid?: boolean;
  /** Accessible name prefix for the trigger, e.g. "Step 2 model". */
  label?: string;
  /** Id of a hint or error element describing this picker. */
  describedBy?: string;
}) {
  const catalog = useModelCatalog();
  const uid = useId();
  const popoverId = `${uid}-popover`;
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [custom, setCustom] = useState(false);
  const availableProviderIDs = useMemo(() => new Set(providers.filter(isLLMProvider).map((provider) => provider.id)), [providers]);
  const selected = catalog.models.find((model) => model.providerId === value.provider && model.id === value.model);
  const currentProvider = providers.find((provider) => provider.id === value.provider);
  const filtered = useMemo(() => {
    const term = query.trim().toLowerCase();
    return catalog.models.filter((model) => availableProviderIDs.has(model.providerId) && (!term || `${model.name} ${model.id} ${model.providerName} ${model.providerId}`.toLowerCase().includes(term))).slice(0, 60);
  }, [availableProviderIDs, catalog.models, query]);

  const close = (returnFocus = true) => {
    setOpen(false);
    if (returnFocus) triggerRef.current?.focus();
  };
  const select = (provider: string, model: string) => {
    onChange({ provider, model });
    setQuery("");
    setCustom(false);
    close();
  };

  // Close on outside click and Escape so the popover behaves like a menu.
  // Escape returns focus to the trigger.
  useEffect(() => {
    if (!open) return;
    const onPointer = (event: MouseEvent) => {
      if (rootRef.current && event.target instanceof Node && !rootRef.current.contains(event.target)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Arrow keys move between options; ArrowDown from the search box enters the list.
  const options = () => Array.from(listRef.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? []);
  const onSearchKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      options()[0]?.focus();
    }
  };
  const onListKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    const items = options();
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    if (items.length === 0) return;
    event.preventDefault();
    const next = event.key === "Home" ? 0
      : event.key === "End" ? items.length - 1
        : event.key === "ArrowDown" ? Math.min(items.length - 1, current + 1)
          : current - 1;
    if (next < 0) rootRef.current?.querySelector<HTMLInputElement>("input[type='search']")?.focus();
    else items[next]?.focus();
  };

  const displayName = selected?.name || value.model;
  const showId = Boolean(selected && selected.name !== value.model);
  const providerLabel = currentProvider?.display_name || value.provider;
  const triggerLabel = `${label ?? "Model"}: ${value.model ? `${displayName}${providerLabel ? ` from ${providerLabel}` : ""}` : "not chosen"}`;

  return (
    <div ref={rootRef} className="relative min-w-0">
      <button
        ref={triggerRef}
        type="button"
        autoFocus={autoFocus}
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-controls={open ? popoverId : undefined}
        aria-label={triggerLabel}
        aria-invalid={invalid || undefined}
        aria-describedby={describedBy}
        className={cn(
          "flex h-9 w-full items-center gap-2 rounded-lg border bg-surface px-2.5 text-left text-[13px] transition-[border-color,box-shadow] hover:border-fg-faint focus:border-accent-500",
          focusRing,
          invalid ? "border-bad" : "border-input",
          open && "border-accent-500",
        )}
      >
        {selected?.icon || value.provider ? (
          <ProviderLogo
            icon={selected?.icon || providerIcon(currentProvider, value.provider)}
            name={providerLabel}
            size={18}
          />
        ) : null}
        {value.model ? (
          <span className="flex min-w-0 flex-1 items-baseline gap-2">
            <span className={cn("truncate text-fg", !selected && "font-mono text-[12.5px]")}>{displayName}</span>
            {showId && <span className="hidden min-w-0 truncate font-mono text-[12px] text-fg-muted sm:inline">{value.model}</span>}
          </span>
        ) : (
          <span className="min-w-0 flex-1 truncate text-fg-muted">Choose a model…</span>
        )}
        <ChevronDown className={cn("h-4 w-4 shrink-0 text-fg-faint transition-transform", open && "rotate-180")} strokeWidth={1.75} aria-hidden="true" />
      </button>
      {open && (
        <div
          id={popoverId}
          role="dialog"
          aria-label={`${label ?? "Model"} picker`}
          className="absolute left-0 z-30 mt-1 w-full min-w-[min(320px,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-float)]"
        >
          <div className="flex items-center gap-1.5 border-b border-line p-1.5">
            <div className="flex h-8 min-w-0 flex-1 items-center gap-2 rounded-lg border border-input bg-surface px-2.5 focus-within:border-accent-500 focus-within:ring-2 focus-within:ring-accent-500">
              <Search className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <input
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={onSearchKeyDown}
                placeholder="Search models and providers"
                aria-label="Search models and providers"
                aria-controls={custom ? undefined : `${uid}-list`}
                className="w-full bg-transparent text-[13px] text-fg outline-none placeholder:text-fg-faint"
                autoFocus
              />
            </div>
            <button
              type="button"
              onClick={() => close()}
              className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg", focusRing)}
              aria-label="Close model picker"
            >
              <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            </button>
          </div>
          {custom ? (
            <div className="space-y-3 p-3">
              <label className="block space-y-1.5">
                <span className="text-[12.5px] font-medium text-fg">Provider</span>
                <Select value={value.provider} onChange={(event) => onChange({ provider: event.target.value, model: "" })}>
                  <option value="">Choose provider…</option>
                  {providers.filter(isLLMProvider).map((provider) => <option key={provider.id} value={provider.id}>{provider.display_name}</option>)}
                </Select>
              </label>
              <label className="block space-y-1.5">
                <span className="text-[12.5px] font-medium text-fg">Model ID</span>
                <Input
                  value={value.model}
                  onChange={(event) => onChange({ provider: value.provider, model: event.target.value })}
                  placeholder="Exact model ID"
                  className="font-mono"
                />
              </label>
              <button
                type="button"
                onClick={() => setCustom(false)}
                className={cn("inline-flex min-h-6 items-center gap-1 rounded-md text-[12.5px] font-medium text-link hover:underline", focusRing)}
              >
                <ArrowLeft className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                Back to catalog
              </button>
            </div>
          ) : (
            <>
              <div id={`${uid}-list`} ref={listRef} role="listbox" aria-label="Models" onKeyDown={onListKeyDown} aria-busy={catalog.loading || undefined} className="max-h-64 overflow-y-auto p-1">
                {catalog.loading ? (
                  <p role="status" className="px-3 py-4 text-center text-[12.5px] text-fg-muted">Loading models…</p>
                ) : filtered.length === 0 ? (
                  <p role="status" className="px-3 py-4 text-center text-[12.5px] text-fg-muted">No models match.</p>
                ) : (
                  filtered.map((model) => {
                    const isSelected = model.providerId === value.provider && model.id === value.model;
                    return (
                      <button
                        key={`${model.providerId}/${model.id}`}
                        type="button"
                        role="option"
                        aria-selected={isSelected}
                        onClick={() => select(model.providerId, model.id)}
                        className={cn(
                          "flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-hover focus:outline-none focus-visible:bg-hover focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500",
                          isSelected && "bg-accent-500/10",
                        )}
                      >
                        <ProviderLogo icon={model.icon} name={model.providerName} size={20} />
                        <span className="min-w-0 flex-1">
                          <span className="flex items-center gap-1">
                            <span className="truncate text-[13px] font-medium text-fg">{model.name}</span>
                            <ModelCapabilityIcons capabilities={model.capabilities} size={13} />
                          </span>
                          <span className="block truncate text-[12px] text-fg-muted">
                            {model.providerName} · <span className="font-mono">{model.id}</span>
                          </span>
                        </span>
                        {isSelected && <Check className="h-4 w-4 shrink-0 text-link" strokeWidth={1.75} aria-hidden="true" />}
                      </button>
                    );
                  })
                )}
              </div>
              {!catalog.loading && filtered.length > 0 && (
                <span className="sr-only" role="status">{filtered.length} models shown</span>
              )}
              <div className="border-t border-line p-1">
                <button
                  type="button"
                  onClick={() => setCustom(true)}
                  className="flex h-8 w-full items-center gap-2 rounded-lg px-2 text-left text-[12.5px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:bg-hover focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
                >
                  <Plus className="h-3.5 w-3.5 text-tone" strokeWidth={1.75} aria-hidden="true" />
                  Use a custom model ID
                </button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
