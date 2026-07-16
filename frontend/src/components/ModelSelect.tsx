// Shared model multi-select and token formatting components used in
// both Keys.tsx and Endpoints.tsx API key creation flows.

import { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueries } from "@tanstack/react-query";
import { X, Search, ChevronDown, Check, Cpu } from "lucide-react";
import { api } from "../lib/api";

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
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  const formatted = formatTokenLimit(value);

  return (
    <input
      type="text"
      inputMode="numeric"
      value={formatted}
      onChange={(e) => {
        const raw = e.target.value.replace(/[^\d]/g, "");
        onChange(raw);
      }}
      placeholder={placeholder ? formatTokenLimit(placeholder) : undefined}
      className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-sm placeholder:text-[var(--text-muted)] focus:border-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40"
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

function ProviderIcon({ option, className = "h-7 w-7" }: { option?: ModelCatalogOption; className?: string }) {
  return (
    <span className={`relative flex shrink-0 items-center justify-center overflow-hidden rounded-lg bg-[var(--bg-subtle)] text-[var(--text-muted)] outline outline-1 -outline-offset-1 outline-black/10 dark:outline-white/10 ${className}`}>
      <Cpu className="h-3.5 w-3.5" aria-hidden="true" />
      {option?.icon && (
        <img
          src={option.icon}
          alt=""
          className="absolute inset-0 h-full w-full object-contain p-1"
          onError={(event) => { (event.currentTarget as HTMLImageElement).style.display = "none"; }}
        />
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
          <div key={id} className="flex min-w-0 items-center gap-3 rounded-xl bg-[var(--bg-elevated)] px-3 py-2.5 shadow-[var(--shadow-card)]">
            <ProviderIcon option={option} className="h-8 w-8" />
            <div className="min-w-0">
              <p className="truncate text-sm font-semibold text-[var(--text)]" title={id}>{option?.name || id}</p>
              <p className="mt-0.5 truncate text-xs text-[var(--text-muted)]">
                {catalog.loading ? "Resolving provider…" : catalog.error ? "Provider unavailable" : option?.providerName || "Custom model pattern"}
                {option && option.name !== id ? <span className="font-mono"> · {id}</span> : null}
              </p>
            </div>
          </div>
        );
      })}
      {value.length > limit && (
        <div className="flex min-h-12 items-center justify-center rounded-xl bg-[var(--bg-elevated)] px-3 text-sm font-semibold tabular-nums text-[var(--text-muted)] shadow-[var(--shadow-card)]">
          +{value.length - limit} more
        </div>
      )}
    </div>
  );
}

/** Autocomplete multi-select for models, grouped by provider with logos. */
export function ModelMultiSelect({
  value,
  onChange,
}: {
  value: string[];
  onChange: (v: string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [customText, setCustomText] = useState("");
  const triggerRef = useRef<HTMLDivElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [rect, setRect] = useState<DOMRect | null>(null);

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
        m.providerName.toLowerCase().includes(q),
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

  const providerOptions = useMemo(() => {
    const map = new Map<string, string>();
    allModels.forEach((model) => map.set(model.providerId, model.providerName));
    return Array.from(map, ([id, name]) => ({ id, name }));
  }, [allModels]);

  const updateRect = useCallback(() => {
    if (triggerRef.current) setRect(triggerRef.current.getBoundingClientRect());
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
      setOpen(false);
      setQuery("");
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        setQuery("");
      }
    };
    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKey);
    };
  }, [open]);

  useEffect(() => {
    if (open && inputRef.current) inputRef.current.focus();
  }, [open]);

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

  const dropdown = open && rect
    ? createPortal(
        <div
          ref={dropdownRef}
          onMouseDown={(e) => e.stopPropagation()}
          className="fixed z-[100] overflow-hidden rounded-2xl bg-[var(--bg-elevated)] shadow-[var(--shadow-float)] outline outline-1 -outline-offset-1 outline-black/10 dark:outline-white/10"
          style={{ top: dropdownTop, left: dropdownLeft, width: dropdownWidth, maxHeight: dropdownHeight }}
        >
          {/* Search */}
          <div className="space-y-2 border-b border-[var(--border)] p-2">
            <div className="flex flex-col gap-2 sm:flex-row">
              <div className="flex min-h-10 flex-1 items-center gap-2 rounded-lg bg-[var(--bg-subtle)] px-3">
                <Search className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
                <input
                  ref={inputRef}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Search name or model ID…"
                  className="w-full bg-transparent text-sm outline-none placeholder:text-[var(--text-muted)]"
                />
              </div>
              <select
                value={providerFilter}
                onChange={(event) => setProviderFilter(event.target.value)}
                aria-label="Filter models by provider"
                className="min-h-10 rounded-lg bg-[var(--bg-subtle)] px-3 text-sm font-medium text-[var(--text)] outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40 sm:w-48"
              >
                <option value="all">All providers</option>
                {providerOptions.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
              </select>
            </div>
            <div className="flex items-center justify-between px-1 text-[11px] text-[var(--text-muted)]">
              <span>{filtered.length} model{filtered.length === 1 ? "" : "s"}</span>
              <span className="tabular-nums">{value.length} selected</span>
            </div>
          </div>

          {/* Model list grouped by provider */}
          <div role="listbox" aria-multiselectable="true" className="overflow-y-auto overscroll-contain p-1" style={{ maxHeight: listHeight }}>
            {anyLoading ? (
              <div className="flex items-center justify-center py-6">
                <div className="h-4 w-4 animate-spin rounded-full border-2 border-ink-300 border-t-accent-500" />
              </div>
            ) : grouped.length === 0 ? (
              <p className="px-3 py-2.5 text-center text-xs text-[var(--text-muted)]">No models found</p>
            ) : (
              grouped.map((g) => (
                <div key={g.provider}>
                  <div className="sticky top-0 z-10 flex items-center gap-2 bg-[var(--bg-elevated)] px-3 pb-1.5 pt-2.5">
                    <ProviderIcon option={g.models[0]} className="h-5 w-5 rounded-md" />
                    <span className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                      {g.providerName}
                    </span>
                  </div>
                  {g.models.map((m) => (
                    <button
                      key={`${m.providerId}:${m.id}`}
                      type="button"
                      role="option"
                      aria-selected={value.includes(m.id)}
                      onClick={() => toggle(m.id)}
                      className={`flex min-h-10 w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm transition-colors hover:bg-[var(--bg-subtle)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50 ${
                        value.includes(m.id) ? "bg-accent-500/10" : ""
                      }`}
                    >
                      <div
                        className={`flex h-4 w-4 shrink-0 items-center justify-center rounded border ${
                          value.includes(m.id)
                            ? "border-accent-500 bg-accent-500"
                            : "border-[var(--border)]"
                        }`}
                      >
                        {value.includes(m.id) && <Check className="h-3 w-3 text-white" />}
                      </div>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium">{m.name}</span>
                        {m.id !== m.name && <span className="block truncate font-mono text-[11px] text-[var(--text-muted)]">{m.id}</span>}
                      </span>
                    </button>
                  ))}
                </div>
              ))
            )}
          </div>

          {/* Custom pattern input */}
          <div className="border-t border-[var(--border)] p-2">
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
                placeholder="Add custom pattern (e.g. claude-*)"
                className="min-h-10 flex-1 rounded-lg bg-[var(--bg-subtle)] px-2.5 py-2 text-sm outline-none placeholder:text-[var(--text-muted)] focus-visible:ring-2 focus-visible:ring-accent-400/40"
              />
              <button
                type="button"
                onClick={addCustom}
                disabled={!customText.trim()}
                className="min-h-10 rounded-lg bg-accent-600 px-3 py-2 text-xs font-medium text-white transition-colors hover:bg-accent-700 disabled:opacity-40 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
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
        <div className="mb-2 flex flex-wrap gap-1.5">
          {value.map((id) => {
            const m = modelLookup.get(id);
            return (
              <span
                key={id}
                className="inline-flex min-h-10 items-center gap-2 rounded-xl bg-accent-500/10 pl-2 text-xs font-medium text-accent-700 dark:text-accent-300"
              >
                <ProviderIcon option={m} className="h-6 w-6" />
                <span className="max-w-[240px] truncate"><span className="text-[var(--text-muted)]">{m?.providerName || "Custom"}</span> · {id}</span>
                <button
                  type="button"
                  onClick={(e) => { e.stopPropagation(); removeChip(id); }}
                  aria-label={`Remove ${id}`}
                  className="flex h-10 w-10 items-center justify-center rounded-xl transition-colors hover:bg-accent-500/20 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            );
          })}
        </div>
      )}

      {/* Trigger button */}
      <div ref={triggerRef}>
        <button
          type="button"
          onMouseDown={(e) => {
            e.stopPropagation();
            setOpen(!open);
            setQuery("");
          }}
          aria-expanded={open}
          aria-haspopup="listbox"
          className="flex min-h-10 w-full items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-left text-sm transition-colors hover:border-[var(--border-strong)] focus:border-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40"
        >
          <Search className="h-4 w-4 shrink-0 text-[var(--text-muted)]" />
          <span className={`flex-1 ${value.length > 0 ? "" : "text-[var(--text-muted)]"}`}>
            {value.length > 0
              ? `${value.length} model${value.length !== 1 ? "s" : ""} selected`
              : "Search and select models…"}
          </span>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-[var(--text-muted)] transition-transform ${open ? "rotate-180" : ""}`}
          />
        </button>
      </div>
      {dropdown}
    </div>
  );
}
