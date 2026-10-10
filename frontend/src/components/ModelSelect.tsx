// Shared model multi-select and token formatting components used in
// both Keys.tsx and Endpoints.tsx API key creation flows.

import { useState, useEffect, useId, useRef, useMemo, useCallback, type InputHTMLAttributes, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueries } from "@tanstack/react-query";
import { X, Search, ChevronDown, Check, Eye, Brain } from "lucide-react";
import { api, type ModelCapabilities } from "../lib/api";
import { ProviderLogo } from "./ProviderLogo";

// ── Token Formatting ─────────────────────────────────────────────────

/** Format number with thousand separators: 1000000 → "1.000.000" */
export function formatTokenLimit(value: string): string {
  if (!value) return "";
  const n = parseInt(value.replace(/\D/g, ""), 10);
  if (isNaN(n)) return "";
  return n.toLocaleString("id-ID");
}

/** Text input that displays formatted token count continuously. */
export function FormattedTokenInput({
  value,
  onChange,
  placeholder,
  ...rest
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
} & Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "onChange" | "placeholder" | "type">) {
  const formatted = formatTokenLimit(value);

  return (
    <input
      {...rest}
      type="text"
      inputMode="numeric"
      value={formatted}
      onChange={(e) => {
        const raw = e.target.value.replace(/[^\d]/g, "");
        onChange(raw);
      }}
      placeholder={placeholder ? formatTokenLimit(placeholder) : undefined}
      className="h-9 w-full rounded-lg border border-input bg-surface px-3 text-[13px] tabular-nums text-fg placeholder:text-fg-faint transition-colors hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    />
  );
}

// ── Model Multi-Select ───────────────────────────────────────────────

export interface ModelCatalogOption {
  id: string;
  name: string;
  providerId: string;
  providerName: string;
  icon: string;
	capabilities?: ModelCapabilities;
}

export function useModelCatalog() {
  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const visibleProviders = useMemo(
    () => (providers.data?.providers ?? []).filter((provider) => !provider.hidden),
    [providers.data],
  );
  const modelQueries = useQueries({
    queries: visibleProviders.map((provider) => ({
      queryKey: ["providerModels", provider.id],
      queryFn: () => api.providerModels(provider.id),
      staleTime: 300_000,
    })),
  });
  const models = useMemo<ModelCatalogOption[]>(() => {
    const result: ModelCatalogOption[] = [];
    visibleProviders.forEach((provider, index) => {
      for (const model of modelQueries[index]?.data?.models ?? []) {
        result.push({
          id: model.id,
          name: model.name || model.id,
          providerId: provider.id,
          providerName: provider.display_name,
          icon: provider.icon || `/providers/${provider.id}.png`,
			capabilities: model.capabilities,
        });
      }
    });
    return result;
  }, [visibleProviders, modelQueries]);

  return {
    models,
    loading: providers.isLoading || modelQueries.some((query) => query.isLoading),
    error: providers.isError || modelQueries.some((query) => query.isError),
  };
}

function ProviderIcon({ option, size = 20 }: { option?: ModelCatalogOption; size?: number }) {
  return <ProviderLogo icon={option?.icon} name={option?.providerName || "Custom"} size={size} />;
}

// CapabilityGlyphs shows vision / reasoning support as quiet glyphs. Kept local
// so the picker stays calm; the shared ModelCapabilityIcons renders chips.
function CapabilityGlyphs({ capabilities }: { capabilities?: ModelCapabilities }) {
  if (!capabilities?.vision && !capabilities?.reasoning) return null;
  return (
    <span className="inline-flex shrink-0 items-center gap-1 text-fg-faint" role="group" aria-label="Model capabilities">
      {capabilities.vision && (
        <span title="Vision — supports image input" role="img" aria-label="Vision" className="inline-flex">
          <Eye className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
        </span>
      )}
      {capabilities.reasoning && (
        <span title="Reasoning — supports extended thinking" role="img" aria-label="Reasoning" className="inline-flex">
          <Brain className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
        </span>
      )}
    </span>
  );
}

export function ModelAccessList({ value, limit = 12 }: { value: string[]; limit?: number }) {
  const catalog = useModelCatalog();
  const lookup = useMemo(() => {
    const result = new Map<string, ModelCatalogOption>();
    for (const model of catalog.models) {
      if (!result.has(model.id)) result.set(model.id, model);
    }
    return result;
  }, [catalog.models]);

  return (
    <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
      {value.slice(0, limit).map((id) => {
        const option = lookup.get(id);
        return (
          <div key={id} className="flex min-w-0 items-center gap-2.5 rounded-lg border border-line bg-surface px-3 py-2">
            <ProviderIcon option={option} size={24} />
            <div className="min-w-0">
              <p className="truncate text-[13px] font-medium text-fg" title={id}>{option?.name || id}</p>
              <p className="mt-0.5 truncate text-[12px] text-fg-muted">
                {catalog.loading ? "Resolving provider…" : catalog.error ? "Provider unavailable" : option?.providerName || "Custom model pattern"}
                {option && option.name !== id ? <span className="font-mono"> · {id}</span> : null}
              </p>
            </div>
          </div>
        );
      })}
      {value.length > limit && (
        <div className="flex min-h-11 items-center justify-center rounded-lg border border-dashed border-line-strong bg-surface px-3 text-[13px] font-medium tabular-nums text-fg-muted">
          +{value.length - limit} more
        </div>
      )}
    </div>
  );
}

/**
 * Autocomplete multi-select for models, grouped by provider with logos.
 *
 * Keyboard: Enter / Space / ↓ on the trigger opens it; inside, the search box
 * is a combobox — ↑ ↓ move the active option (aria-activedescendant), Enter
 * toggles it, Escape closes and returns focus to the trigger.
 *
 * Name it with `id` + <label htmlFor>, `aria-labelledby`, or `label`.
 */
export function ModelMultiSelect({
  value,
  onChange,
  id,
  label,
  "aria-labelledby": labelledBy,
  "aria-describedby": describedBy,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  /** Id for the trigger button, so a <label htmlFor> can name it. */
  id?: string;
  /** Accessible name when no visible label points at it. Defaults to "Models". */
  label?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [customText, setCustomText] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);
  const triggerRef = useRef<HTMLDivElement>(null);
  const triggerButtonRef = useRef<HTMLButtonElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [rect, setRect] = useState<DOMRect | null>(null);
  const uid = useId();
  const listboxId = `${uid}-listbox`;
  const optionId = (index: number) => `${uid}-opt-${index}`;
  // The trigger's name is its label plus its visible value ("Allowed models,
  // 3 models selected"). A <label htmlFor={id}> names it when no label is given.
  const nameId = `${uid}-name`;
  const valueId = `${uid}-value`;
  const nameRef = labelledBy ?? (label || !id ? nameId : undefined);
  const focusedOnOpen = useRef(false);

  const catalog = useModelCatalog();
  const allModels = catalog.models;

  const modelLookup = useMemo(() => {
    const map = new Map<string, ModelCatalogOption>();
    allModels.forEach((model) => {
      if (!map.has(model.id)) map.set(model.id, model);
    });
    return map;
  }, [allModels]);

  const filtered = useMemo(() => {
    const providerModels = providerFilter === "all"
      ? allModels
      : allModels.filter((model) => model.providerId === providerFilter);
    if (!query.trim()) return providerModels;
    const q = query.toLowerCase();
    return providerModels.filter(
      (m) =>
        m.id.toLowerCase().includes(q) ||
        m.name.toLowerCase().includes(q) ||
        m.providerId.toLowerCase().includes(q) ||
		m.providerName.toLowerCase().includes(q) ||
		(m.capabilities?.vision ? "vision" : "").includes(q) ||
		(m.capabilities?.reasoning ? "reasoning" : "").includes(q),
    );
  }, [allModels, providerFilter, query]);

  const grouped = useMemo(() => {
    const map = new Map<string, { provider: string; providerName: string; models: ModelCatalogOption[] }>();
    filtered.forEach((m) => {
      if (!map.has(m.providerId)) {
        map.set(m.providerId, { provider: m.providerId, providerName: m.providerName, models: [] });
      }
      map.get(m.providerId)!.models.push(m);
    });
    return Array.from(map.values());
  }, [filtered]);

  // Options in visual (grouped) order, so arrow keys follow what is on screen.
  const flat = useMemo(() => grouped.flatMap((g) => g.models), [grouped]);

  const providerOptions = useMemo(() => {
    const map = new Map<string, string>();
    allModels.forEach((model) => map.set(model.providerId, model.providerName));
    return Array.from(map, ([id, name]) => ({ id, name }));
  }, [allModels]);

  const updateRect = useCallback(() => {
    if (triggerRef.current) setRect(triggerRef.current.getBoundingClientRect());
  }, []);

  const close = useCallback((returnFocus: boolean) => {
    setOpen(false);
    setQuery("");
    setActiveIndex(-1);
    if (returnFocus) triggerButtonRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!open) return;
    updateRect();
    const onScroll = () => updateRect();
    const onResize = () => updateRect();
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
    };
  }, [open, updateRect]);

  useEffect(() => {
    if (!open) return;
    const handleClick = (e: MouseEvent) => {
      const target = e.target as globalThis.Node;
      if (triggerRef.current?.contains(target)) return;
      if (dropdownRef.current?.contains(target)) return;
      close(false);
    };
    document.addEventListener("mousedown", handleClick);
    return () => {
      document.removeEventListener("mousedown", handleClick);
    };
  }, [open, close]);

  // Focus the search box once per opening (the popup mounts after its first
  // measurement, so this waits for `rect`; later rect updates must not steal focus).
  useEffect(() => {
    if (!open) {
      focusedOnOpen.current = false;
      return;
    }
    if (!focusedOnOpen.current && inputRef.current) {
      inputRef.current.focus();
      focusedOnOpen.current = true;
    }
  }, [open, rect]);

  // Reset the active option when the visible list changes.
  useEffect(() => {
    setActiveIndex(-1);
  }, [query, providerFilter]);

  // Keep the active option scrolled into view.
  useEffect(() => {
    if (activeIndex < 0) return;
    document.getElementById(`${uid}-opt-${activeIndex}`)?.scrollIntoView({ block: "nearest" });
  }, [activeIndex, uid]);

  const toggle = (id: string) => {
    onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id]);
  };

  const removeChip = (id: string) => {
    onChange(value.filter((v) => v !== id));
  };

  const addCustom = () => {
    const t = customText.trim();
    if (t && !value.includes(t)) {
      onChange([...value, t]);
      setCustomText("");
    }
  };

  const openPicker = () => {
    setOpen(true);
    setQuery("");
  };

  // Keys handled anywhere inside the popup.
  const onDropdownKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      // Handled here so an enclosing Modal / Sheet stays open.
      e.preventDefault();
      e.stopPropagation();
      e.nativeEvent.stopImmediatePropagation();
      close(true);
      return;
    }
    if (e.key === "Tab") {
      const focusables = Array.from(
        dropdownRef.current?.querySelectorAll<HTMLElement>("input, select, button:not([disabled]):not([tabindex='-1'])") ?? [],
      );
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      // Leaving the popup by keyboard closes it and puts focus back on the
      // trigger, so the page's tab order continues from where it opened.
      if ((e.shiftKey && document.activeElement === first) || (!e.shiftKey && document.activeElement === last)) {
        e.preventDefault();
        close(true);
      }
    }
  };

  const onSearchKeyDown = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (flat.length === 0) return;
      setActiveIndex((i) => {
        if (e.key === "ArrowDown") return i < 0 ? 0 : Math.min(flat.length - 1, i + 1);
        return i <= 0 ? 0 : i - 1;
      });
    } else if (e.key === "Home" && activeIndex >= 0) {
      e.preventDefault();
      setActiveIndex(0);
    } else if (e.key === "End" && activeIndex >= 0) {
      e.preventDefault();
      setActiveIndex(flat.length - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const target = flat[activeIndex] ?? (flat.length === 1 ? flat[0] : undefined);
      if (target) toggle(target.id);
    }
  };

  const anyLoading = catalog.loading;
  const dropdownWidth = rect ? Math.min(Math.max(rect.width, 420), window.innerWidth - 16, 760) : 420;
  const dropdownLeft = rect ? Math.max(8, Math.min(rect.left, window.innerWidth - dropdownWidth - 8)) : 8;
  const spaceBelow = rect ? window.innerHeight - rect.bottom - 8 : 0;
  const spaceAbove = rect ? rect.top - 8 : 0;
  const opensAbove = spaceBelow < 320 && spaceAbove > spaceBelow;
  const availableHeight = opensAbove ? spaceAbove : spaceBelow;
  const dropdownHeight = Math.max(180, Math.min(420, availableHeight - 6));
  const dropdownTop = rect
    ? opensAbove
      ? Math.max(8, rect.top - dropdownHeight - 6)
      : rect.bottom + 6
    : 8;
  const dropdownChromeHeight = window.innerWidth < 640 ? 178 : 138;
  const listHeight = Math.max(100, dropdownHeight - dropdownChromeHeight);
  const activeId = activeIndex >= 0 && activeIndex < flat.length ? optionId(activeIndex) : undefined;

  let optionCounter = -1;
  const dropdown = open && rect
    ? createPortal(
        <div
          ref={dropdownRef}
          data-floating-layer
          onMouseDown={(e) => e.stopPropagation()}
          onKeyDown={onDropdownKeyDown}
          className="fixed z-[100] flex flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-pop)]"
          style={{ top: dropdownTop, left: dropdownLeft, width: dropdownWidth, maxHeight: dropdownHeight }}
        >
          {/* Search */}
          <div className="space-y-2 border-b border-line p-2">
            <div className="flex flex-col gap-2 sm:flex-row">
              <div className="flex h-9 flex-1 items-center gap-2 rounded-lg border border-input bg-surface px-3 transition-colors focus-within:border-accent-500 focus-within:ring-2 focus-within:ring-accent-500">
                <Search className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
                <input
                  ref={inputRef}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={onSearchKeyDown}
                  placeholder="Search name or model ID…"
                  role="combobox"
                  aria-label="Search models"
                  aria-expanded="true"
                  aria-controls={listboxId}
                  aria-autocomplete="list"
                  aria-activedescendant={activeId}
                  className="w-full bg-transparent text-[13px] text-fg outline-none placeholder:text-fg-faint"
                  style={{ outline: "none" }}
                />
              </div>
              <select
                value={providerFilter}
                onChange={(event) => setProviderFilter(event.target.value)}
                aria-label="Filter models by provider"
                className="h-9 rounded-lg border border-input bg-surface px-2.5 text-[13px] text-fg outline-none transition-colors hover:border-fg-faint focus:border-accent-500 focus-visible:ring-2 focus-visible:ring-accent-500 sm:w-48"
              >
                <option value="all">All providers</option>
                {providerOptions.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
              </select>
            </div>
            <div className="flex items-center justify-between px-1 text-[12px] text-fg-faint" role="status" aria-live="polite">
              <span className="tabular-nums">{filtered.length} model{filtered.length === 1 ? "" : "s"}</span>
              <span className="tabular-nums">{value.length} selected</span>
            </div>
          </div>

          {/* Model list grouped by provider */}
          <div
            id={listboxId}
            role="listbox"
            aria-multiselectable="true"
            aria-label="Models"
            aria-busy={anyLoading || undefined}
            className="overflow-y-auto overscroll-contain p-1"
            style={{ maxHeight: listHeight }}
          >
            {anyLoading ? (
              <div className="flex items-center justify-center py-6" role="presentation">
                <div className="h-4 w-4 animate-spin rounded-full border-2 border-line-strong border-t-fg-muted" aria-hidden="true" />
                <span className="sr-only">Loading models</span>
              </div>
            ) : grouped.length === 0 ? (
              <p className="px-3 py-6 text-center text-[13px] text-fg-muted" role="presentation">No models match this search.</p>
            ) : (
              grouped.map((g) => (
                <div key={g.provider} role="group" aria-labelledby={`${uid}-grp-${g.provider}`}>
                  <div className="sticky top-0 z-10 flex items-center gap-2 bg-surface px-2.5 pb-1 pt-2.5" role="presentation">
                    <ProviderIcon option={g.models[0]} size={16} />
                    <span id={`${uid}-grp-${g.provider}`} className="text-[12px] font-medium text-fg-muted">
                      {g.providerName}
                    </span>
                    <span className="text-[12px] tabular-nums text-fg-faint" aria-hidden="true">{g.models.length}</span>
                  </div>
                  {g.models.map((m) => {
                    optionCounter += 1;
                    const index = optionCounter;
                    const selected = value.includes(m.id);
                    const active = index === activeIndex;
                    return (
                      <div
                        key={`${m.providerId}:${m.id}`}
                        id={optionId(index)}
                        role="option"
                        aria-selected={selected}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => {
                          setActiveIndex(index);
                          toggle(m.id);
                        }}
                        onMouseMove={() => activeIndex !== index && setActiveIndex(index)}
                        className={`flex min-h-9 w-full cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors ${
                          active ? "bg-hover ring-1 ring-inset ring-accent-500" : selected ? "bg-accent-500/[0.06] hover:bg-hover" : "hover:bg-hover"
                        }`}
                      >
                        {/* Selected state is a check mark, not just a tint. */}
                        <span
                          aria-hidden="true"
                          className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-md border transition-colors ${
                            selected
                              ? "border-accent-500 bg-accent-500 text-white"
                              : "border-input bg-surface"
                          }`}
                        >
                          {selected && <Check className="h-3 w-3" strokeWidth={2.5} />}
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="flex items-center gap-1.5">
                            <span className="block truncate font-medium text-fg">{m.name}</span>
                            <CapabilityGlyphs capabilities={m.capabilities} />
                          </span>
                          {m.id !== m.name && <span className="block truncate font-mono text-[12px] text-fg-muted">{m.id}</span>}
                        </span>
                      </div>
                    );
                  })}
                </div>
              ))
            )}
          </div>

          {/* Custom pattern input */}
          <div className="border-t border-line bg-subtle p-2">
            <div className="flex items-center gap-2">
              <input
                value={customText}
                onChange={(e) => setCustomText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addCustom();
                  }
                }}
                aria-label="Custom model pattern"
                placeholder="Add custom pattern (e.g. claude-*)"
                className="h-9 min-w-0 flex-1 rounded-lg border border-input bg-surface px-3 font-mono text-[12.5px] text-fg outline-none transition-colors placeholder:font-sans placeholder:text-[13px] placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 focus-visible:ring-2 focus-visible:ring-accent-500"
              />
              <button
                type="button"
                onClick={addCustom}
                disabled={!customText.trim()}
                className="h-9 rounded-lg border border-transparent bg-primary px-3 text-[13px] font-medium text-primary-fg transition-opacity hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-40 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                Add
              </button>
            </div>
          </div>
        </div>,
        document.body,
      )
    : null;

  return (
    <div onClick={(e) => e.stopPropagation()} onMouseDown={(e) => e.stopPropagation()}>
      {/* Selected chips */}
      {value.length > 0 && (
        <ul className="mb-2 flex flex-wrap gap-1.5" aria-label="Selected models">
          {value.map((id) => {
            const m = modelLookup.get(id);
            return (
              <li
                key={id}
                className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line bg-subtle pl-1.5 text-[12px] text-fg"
              >
                <ProviderIcon option={m} size={16} />
                <span className="max-w-[260px] truncate"><span className="text-fg-muted">{m?.providerName || "Custom"}</span> <span className="text-fg-faint" aria-hidden="true">·</span> <span className="font-mono">{id}</span></span>
                <button
                  type="button"
                  onClick={(e) => { e.stopPropagation(); removeChip(id); }}
                  aria-label={`Remove ${id}`}
                  className="flex h-7 w-7 items-center justify-center rounded-lg text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                >
                  <X className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                </button>
              </li>
            );
          })}
        </ul>
      )}

      {/* Trigger button */}
      <div ref={triggerRef}>
        <button
          ref={triggerButtonRef}
          type="button"
          id={id}
          onMouseDown={(e) => {
            e.stopPropagation();
            setOpen(!open);
            setQuery("");
          }}
          // Keyboard activation (Enter / Space) fires click with detail 0;
          // mouse clicks are already handled on mousedown.
          onClick={(e) => {
            if (e.detail === 0) {
              if (open) close(false);
              else openPicker();
            }
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" && !open) {
              e.preventDefault();
              openPicker();
            } else if (e.key === "Escape" && open) {
              e.preventDefault();
              e.stopPropagation();
              e.nativeEvent.stopImmediatePropagation();
              close(true);
            }
          }}
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-controls={open ? listboxId : undefined}
          aria-labelledby={nameRef ? `${nameRef} ${valueId}` : undefined}
          aria-describedby={describedBy}
          className="flex h-9 w-full items-center gap-2 rounded-lg border border-input bg-surface px-3 text-left text-[13px] text-fg transition-colors hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
        >
          <Search className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          {nameRef === nameId && <span id={nameId} hidden>{label ?? "Models"}</span>}
          <span id={valueId} className={`flex-1 truncate ${value.length > 0 ? "tabular-nums" : "text-fg-faint"}`}>
            {value.length > 0
              ? `${value.length} model${value.length !== 1 ? "s" : ""} selected`
              : "Search and select models…"}
          </span>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-fg-faint transition-transform ${open ? "rotate-180" : ""}`}
            strokeWidth={1.75}
            aria-hidden="true"
          />
        </button>
      </div>
      {dropdown}
    </div>
  );
}
