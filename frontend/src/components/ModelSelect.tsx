// Shared model multi-select and token formatting components used in
// both Keys.tsx and Endpoints.tsx API key creation flows.

import { memo, useState, useEffect, useDeferredValue, useId, useRef, useMemo, useCallback, type InputHTMLAttributes, type KeyboardEvent as ReactKeyboardEvent, type MutableRefObject, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueries, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { defaultRangeExtractor, useVirtualizer, type Range } from "@tanstack/react-virtual";
import { X, Search, ChevronDown, Check, Eye, Brain, Plus } from "lucide-react";
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

type ProviderModelsResult = Awaited<ReturnType<typeof api.providerModels>>;

// Module-level so useQueries memoizes it: the combined result (and the catalog
// built from it) only changes when a provider's query does, not every render.
function combineProviderModels(results: UseQueryResult<ProviderModelsResult>[]) {
  return {
    data: results.map((result) => result.data),
    pending: results.filter((result) => result.isLoading).length,
    error: results.some((result) => result.isError),
  };
}

export function useModelCatalog() {
  const queryClient = useQueryClient();
  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers(), staleTime: 300_000 });
  const chains = useQuery({ queryKey: ["chains"], queryFn: () => api.listChains(), staleTime: 300_000 });
  // One request for every provider's models; per-provider requests would
  // queue behind the browser's per-host connection limit.
  const batch = useQuery({
    queryKey: ["providerModels", "all"],
    queryFn: async () => {
      const res = await api.allProviderModels();
      // Seed the per-provider entries other screens read.
      for (const [id, models] of Object.entries(res.providers)) {
        if (!res.pending.includes(id)) queryClient.setQueryData(["providerModels", id], { models });
      }
      return res;
    },
    staleTime: 300_000,
  });
  const visibleProviders = useMemo(
    () => (providers.data?.providers ?? []).filter((provider) => !provider.hidden),
    [providers.data],
  );
  // Providers whose live discovery outlasted the batch are fetched on their own.
  const pendingIds = batch.data?.pending;
  const pendingQueries = useQueries({
    queries: (pendingIds ?? []).map((id) => ({
      queryKey: ["providerModels", id],
      queryFn: () => api.providerModels(id),
      staleTime: 300_000,
    })),
    combine: combineProviderModels,
  });
  const models = useMemo<ModelCatalogOption[]>(() => {
    const result: ModelCatalogOption[] = [];
    // Chains are callable virtual models (bare name or "chain:" prefix), so they
    // belong in the same allow/deny picker as provider models.
    for (const chain of chains.data?.chains ?? []) {
      result.push({
        id: chain.name,
        name: chain.name,
        providerId: "chains",
        providerName: "Chains",
        icon: "",
      });
    }
    const pendingData = new Map((pendingIds ?? []).map((id, index) => [id, pendingQueries.data[index]]));
    for (const provider of visibleProviders) {
      const list = pendingData.get(provider.id)?.models ?? batch.data?.providers[provider.id] ?? [];
      for (const model of list) {
        result.push({
          id: model.id,
          name: model.name || model.id,
          providerId: provider.id,
          providerName: provider.display_name,
          icon: provider.icon || `/providers/${provider.id}.png`,
          capabilities: model.capabilities,
        });
      }
    }
    return result;
  }, [visibleProviders, batch.data, pendingIds, pendingQueries.data, chains.data]);

  return {
    models,
    loading: providers.isLoading || batch.isLoading || pendingQueries.pending > 0 || chains.isLoading,
    /** Provider model lists still in flight; the catalog fills in as they land. */
    pendingProviders: batch.isLoading ? visibleProviders.length || 1 : pendingQueries.pending,
    error: providers.isError || batch.isError || pendingQueries.error || chains.isError,
  };
}

function readViewport() {
  if (typeof window === "undefined") return { width: 1024, height: 768, top: 0, left: 0 };
  const vv = window.visualViewport;
  return vv
    ? { width: vv.width, height: vv.height, top: vv.offsetTop, left: vv.offsetLeft }
    : { width: window.innerWidth, height: window.innerHeight, top: 0, left: 0 };
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

// Rows have fixed heights so the virtualizer never has to measure the DOM
// (measuring forces a layout per newly mounted row while scrolling).
const ROW_HEIGHT = { header: 34, single: 36, double: 50, touch: 44 } as const;

function rowHeight(row: PickerRow, coarsePointer: boolean): number {
  if (row.kind === "header") return ROW_HEIGHT.header;
  if (row.model.id !== row.model.name) return ROW_HEIGHT.double;
  return coarsePointer ? ROW_HEIGHT.touch : ROW_HEIGHT.single;
}

type PickerRow =
  | { kind: "header"; key: string; providerId: string; model: ModelCatalogOption; count: number }
  | { kind: "option"; key: string; providerId: string; model: ModelCatalogOption; index: number };

function ModelGroupHeader({ model, count }: { model: ModelCatalogOption; count: number }) {
  return (
    <div className="flex h-full items-end gap-2 bg-surface px-2.5 pb-1">
      <ProviderIcon option={model} size={16} />
      <span className="text-[12px] font-medium text-fg-muted">{model.providerName}</span>
      <span className="text-[12px] tabular-nums text-fg-faint" aria-hidden="true">{count}</span>
    </div>
  );
}

// Memoized so moving the active option or toggling one re-renders only the
// rows whose state changed.
const ModelOptionRow = memo(function ModelOptionRow({
  model,
  index,
  domId,
  selected,
  active,
  onSelect,
  onHover,
}: {
  model: ModelCatalogOption;
  index: number;
  domId: string;
  selected: boolean;
  active: boolean;
  onSelect: (index: number, id: string) => void;
  onHover: (index: number) => void;
}) {
  return (
    <div
      id={domId}
      role="option"
      aria-selected={selected}
      aria-label={`${model.name}, ${model.providerName}`}
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => onSelect(index, model.id)}
      onMouseMove={active ? undefined : () => onHover(index)}
      className={`flex h-full w-full cursor-pointer items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] ${
        active ? "bg-hover ring-1 ring-inset ring-accent-500" : selected ? "bg-accent-500/10 hover:bg-accent-500/15" : "hover:bg-hover"
      }`}
    >
      {/* Selected state is a check mark, not just a tint. */}
      <span
        aria-hidden="true"
        className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-md border ${
          selected ? "border-accent-500 bg-accent-500 text-white" : "border-input bg-surface"
        }`}
      >
        {selected && <Check className="h-3 w-3" strokeWidth={2.5} />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="block truncate font-medium text-fg">{model.name}</span>
          <CapabilityGlyphs capabilities={model.capabilities} />
        </span>
        {model.id !== model.name && <span className="block truncate font-mono text-[12px] text-fg-muted">{model.id}</span>}
      </span>
    </div>
  );
});

// VirtualModelList is the picker's listbox. It mounts only the rows in view
// (the catalog can hold thousands) and keeps the active group's header pinned
// to the top. It is its own memoized component so scrolling re-renders just the
// list, not the picker and its catalog queries; it also owns the scroll element
// so the virtualizer sees it on the first layout pass.
const VirtualModelList = memo(function VirtualModelList({
  listboxId,
  loading,
  compact,
  maxHeight,
  rows,
  headerRows,
  scrollRef,
  scrollToRowRef,
  uid,
  selectedSet,
  activeIndex,
  onSelect,
  onHover,
}: {
  listboxId: string;
  loading: boolean;
  compact: boolean;
  maxHeight?: number;
  rows: PickerRow[];
  headerRows: number[];
  scrollRef: RefObject<HTMLDivElement | null>;
  scrollToRowRef: MutableRefObject<((row: number) => void) | null>;
  uid: string;
  selectedSet: Set<string>;
  activeIndex: number;
  onSelect: (index: number, id: string) => void;
  onHover: (index: number) => void;
}) {
  const activeHeaderRef = useRef(0);
  const rangeExtractor = useCallback(
    (range: Range) => {
      let active = headerRows[0] ?? 0;
      for (const row of headerRows) {
        if (row > range.startIndex) break;
        active = row;
      }
      activeHeaderRef.current = active;
      const indexes = defaultRangeExtractor(range);
      return indexes.includes(active) ? indexes : [active, ...indexes];
    },
    [headerRows],
  );
  const coarsePointer = useMemo(() => typeof window !== "undefined" && window.matchMedia("(pointer: coarse)").matches, []);
  // Stable per row list: the virtualizer rebuilds every row offset whenever
  // these identities change.
  const estimateSize = useCallback((i: number) => rowHeight(rows[i], coarsePointer), [rows, coarsePointer]);
  const getItemKey = useCallback((i: number) => rows[i].key, [rows]);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize,
    getItemKey,
    rangeExtractor,
    overscan: 8,
    paddingStart: 4,
    paddingEnd: 4,
  });

  useEffect(() => {
    scrollToRowRef.current = (row) => virtualizer.scrollToIndex(row, { align: "auto" });
    return () => {
      scrollToRowRef.current = null;
    };
  }, [virtualizer, scrollToRowRef]);

  return (
    <div
      ref={scrollRef}
      id={listboxId}
      role="listbox"
      aria-multiselectable="true"
      aria-label="Models"
      aria-busy={loading || undefined}
      className={`overflow-y-auto overscroll-contain ${compact ? "min-h-0 flex-1" : ""}`}
      style={maxHeight === undefined ? undefined : { maxHeight }}
    >
      {loading && rows.length === 0 ? (
        <div className="flex items-center justify-center py-6" role="presentation">
          <div className="h-4 w-4 animate-spin rounded-full border-2 border-line-strong border-t-fg-muted" aria-hidden="true" />
          <span className="sr-only">Loading models</span>
        </div>
      ) : rows.length === 0 ? (
        <p className="px-3 py-6 text-center text-[13px] text-fg-muted" role="presentation">No models match this search.</p>
      ) : (
        <div className="relative mx-1" style={{ height: virtualizer.getTotalSize() }} role="presentation">
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index];
            const pinned = row.kind === "header" && item.index === activeHeaderRef.current;
            return (
              <div
                key={item.key}
                role="presentation"
                className={pinned ? "sticky top-0 z-10" : "absolute inset-x-0 top-0"}
                style={pinned ? { height: item.size } : { height: item.size, transform: `translateY(${item.start}px)` }}
              >
                {row.kind === "header" ? (
                  <ModelGroupHeader model={row.model} count={row.count} />
                ) : (
                  <ModelOptionRow
                    model={row.model}
                    index={row.index}
                    domId={`${uid}-opt-${row.index}`}
                    selected={selectedSet.has(row.model.id)}
                    active={row.index === activeIndex}
                    onSelect={onSelect}
                    onHover={onHover}
                  />
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
});

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
  const listRef = useRef<HTMLDivElement>(null);
  const [rect, setRect] = useState<DOMRect | null>(null);
  // The visual viewport shrinks when a phone's on-screen keyboard opens; the
  // compact (phone) popup sizes itself to it.
  const [viewport, setViewport] = useState(() => readViewport());
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

  // Lower-cased search text per model, built once per catalog rather than on
  // every keystroke (the catalog runs to thousands of models).
  const searchIndex = useMemo(
    () =>
      allModels.map((m) =>
        [m.id, m.name, m.providerId, m.providerName, m.capabilities?.vision ? "vision" : "", m.capabilities?.reasoning ? "reasoning" : ""]
          .join("\n")
          .toLowerCase(),
      ),
    [allModels],
  );

  // Typing stays responsive: filtering a large catalog runs at low priority.
  const deferredQuery = useDeferredValue(query);

  const filtered = useMemo(() => {
    const q = deferredQuery.trim().toLowerCase();
    if (!q && providerFilter === "all") return allModels;
    return allModels.filter(
      (m, i) => (providerFilter === "all" || m.providerId === providerFilter) && (!q || searchIndex[i].includes(q)),
    );
  }, [allModels, searchIndex, providerFilter, deferredQuery]);

  // One flat row list (group headers + options) for the virtualizer. `flat`
  // holds the options in visual order, so arrow keys follow what is on screen;
  // `optionRow` maps an option index to its row.
  const { rows, flat, optionRow, headerRows } = useMemo(() => {
    const groups = new Map<string, ModelCatalogOption[]>();
    for (const m of filtered) {
      const list = groups.get(m.providerId);
      if (list) list.push(m);
      else groups.set(m.providerId, [m]);
    }
    const rows: PickerRow[] = [];
    const flat: ModelCatalogOption[] = [];
    const optionRow: number[] = [];
    const headerRows: number[] = [];
    for (const [providerId, models] of groups) {
      headerRows.push(rows.length);
      rows.push({ kind: "header", key: `h:${providerId}`, providerId, model: models[0], count: models.length });
      for (const model of models) {
        optionRow.push(rows.length);
        rows.push({ kind: "option", key: `o:${providerId}:${model.id}`, providerId, model, index: flat.length });
        flat.push(model);
      }
    }
    return { rows, flat, optionRow, headerRows };
  }, [filtered]);

  const selectedSet = useMemo(() => new Set(value), [value]);

  const providerOptions = useMemo(() => {
    const map = new Map<string, string>();
    allModels.forEach((model) => map.set(model.providerId, model.providerName));
    return Array.from(map, ([id, name]) => ({ id, name }));
  }, [allModels]);

  // Only commit a new position when it actually moved: an unchanged rect must
  // not re-render the whole picker.
  const updateRect = useCallback(() => {
    if (triggerRef.current) {
      const next = triggerRef.current.getBoundingClientRect();
      setRect((prev) =>
        prev && prev.top === next.top && prev.left === next.left && prev.width === next.width && prev.height === next.height
          ? prev
          : next,
      );
    }
    const nextViewport = readViewport();
    setViewport((prev) =>
      prev.width === nextViewport.width && prev.height === nextViewport.height && prev.top === nextViewport.top && prev.left === nextViewport.left
        ? prev
        : nextViewport,
    );
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
    // Coalesce to one measurement per frame, and ignore scrolling inside the
    // popup itself (the model list) — it cannot move the trigger.
    let frame = 0;
    const schedule = () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        updateRect();
      });
    };
    const onScroll = (e: Event) => {
      if (e.target instanceof globalThis.Node && dropdownRef.current?.contains(e.target)) return;
      schedule();
    };
    const onResize = schedule;
    const vv = window.visualViewport;
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onResize);
    vv?.addEventListener("resize", onResize);
    vv?.addEventListener("scroll", onResize);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
      vv?.removeEventListener("resize", onResize);
      vv?.removeEventListener("scroll", onResize);
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

  // Set by VirtualModelRows so keyboard navigation can scroll a row into view.
  const scrollToRowRef = useRef<((row: number) => void) | null>(null);

  // Reset the active option and scroll position when the visible list changes.
  useEffect(() => {
    setActiveIndex(-1);
    listRef.current?.scrollTo({ top: 0 });
  }, [deferredQuery, providerFilter]);

  // Keep the keyboard-active option scrolled into view (hover never scrolls).
  const moveActive = (index: number) => {
    setActiveIndex(index);
    const row = optionRow[index];
    if (row !== undefined) scrollToRowRef.current?.(row);
  };

  // Stable handlers for the memoized rows; they read the latest value.
  const valueRef = useRef(value);
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    valueRef.current = value;
    onChangeRef.current = onChange;
  });
  const toggle = useCallback((id: string) => {
    const current = valueRef.current;
    onChangeRef.current(current.includes(id) ? current.filter((v) => v !== id) : [...current, id]);
  }, []);
  const onOptionClick = useCallback(
    (index: number, id: string) => {
      setActiveIndex(index);
      toggle(id);
    },
    [toggle],
  );
  const onOptionHover = useCallback((index: number) => setActiveIndex(index), []);

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
      const i = activeIndex;
      moveActive(e.key === "ArrowDown" ? (i < 0 ? 0 : Math.min(flat.length - 1, i + 1)) : i <= 0 ? 0 : i - 1);
    } else if (e.key === "Home" && activeIndex >= 0) {
      e.preventDefault();
      moveActive(0);
    } else if (e.key === "End" && activeIndex >= 0) {
      e.preventDefault();
      moveActive(flat.length - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const target = flat[activeIndex] ?? (flat.length === 1 ? flat[0] : undefined);
      if (target) toggle(target.id);
    }
  };

  const anyLoading = catalog.loading;
  // Phones: the picker becomes a near full-screen panel inside the visible
  // viewport (8px gutters), with the list taking the remaining height.
  const compact = viewport.width < 640;
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
  const panelStyle = compact
    ? { top: viewport.top + 8, left: viewport.left + 8, width: viewport.width - 16, height: Math.max(240, viewport.height - 16) }
    : { top: dropdownTop, left: dropdownLeft, width: dropdownWidth, maxHeight: dropdownHeight };
  const activeId = activeIndex >= 0 && activeIndex < flat.length ? optionId(activeIndex) : undefined;

  const dropdown = open && rect
    ? createPortal(
        <div
          ref={dropdownRef}
          data-floating-layer
          onMouseDown={(e) => e.stopPropagation()}
          onKeyDown={onDropdownKeyDown}
          className="fixed z-[100] flex flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-pop)]"
          style={panelStyle}
        >
          {/* Search */}
          <div className="shrink-0 space-y-2 border-b border-line p-2">
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
            <div className="flex items-center gap-2">
              <div className="flex flex-1 items-center justify-between px-1 text-[12px] text-fg-faint" role="status" aria-live="polite">
                <span className="tabular-nums">
                  {filtered.length.toLocaleString()} model{filtered.length === 1 ? "" : "s"}
                  {catalog.pendingProviders > 0 && rows.length > 0 && (
                    <span className="text-fg-faint"> · loading {catalog.pendingProviders} more provider{catalog.pendingProviders === 1 ? "" : "s"}…</span>
                  )}
                </span>
                <span className="tabular-nums">{value.length} selected</span>
              </div>
              {compact && (
                // The panel covers the screen on phones, so it needs a visible way out.
                <button
                  type="button"
                  onClick={() => close(true)}
                  className="inline-flex h-9 shrink-0 items-center rounded-lg border border-line-strong bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                >
                  Done
                </button>
              )}
            </div>
          </div>

          {/* Model list grouped by provider (virtualized) */}
          <VirtualModelList
            listboxId={listboxId}
            loading={anyLoading}
            compact={compact}
            maxHeight={compact ? undefined : listHeight}
            rows={rows}
            headerRows={headerRows}
            scrollRef={listRef}
            scrollToRowRef={scrollToRowRef}
            uid={uid}
            selectedSet={selectedSet}
            activeIndex={activeIndex}
            onSelect={onOptionClick}
            onHover={onOptionHover}
          />

          {/* Custom pattern input */}
          <div className="shrink-0 border-t border-line bg-subtle p-2">
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
                className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-transparent bg-action px-3 text-[13px] font-medium text-action-fg transition-colors hover:bg-action-hover disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-subtle"
              >
                <Plus className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
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
                className="inline-flex h-7 max-w-full items-center gap-1.5 rounded-lg border border-line bg-subtle pl-1.5 text-[12px] text-fg"
              >
                <ProviderIcon option={m} size={16} />
                <span className="min-w-0 max-w-[260px] truncate"><span className="text-fg-muted">{m?.providerName || "Custom"}</span> <span className="text-fg-faint" aria-hidden="true">·</span> <span className="font-mono">{id}</span></span>
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
