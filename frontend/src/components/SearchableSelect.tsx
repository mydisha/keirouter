import { useState, useRef, useEffect, useMemo } from "react";
import { Search, Check, ChevronDown, X } from "lucide-react";

export interface SearchableSelectOption {
  value: string;
  label: string;
  sublabel?: string;
}

interface SearchableSelectProps {
  options: SearchableSelectOption[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  /** When true, the user can type a custom value not in the options. */
  allowFreeText?: boolean;
  /** Optional suffix appended to each option label (e.g. provider alias). */
  valueSuffix?: string;
  disabled?: boolean;
  loading?: boolean;
  /** Optional custom no-results message. */
  noResultsText?: string;
}

/**
 * A combobox-style searchable select component. Supports filtering a list of
 * options by typing, keyboard navigation, and optional free-text entry for
 * values not in the predefined list.
 */
export function SearchableSelect({
  options,
  value,
  onChange,
  placeholder = "Search or type…",
  allowFreeText = true,
  disabled = false,
  loading = false,
  noResultsText = "No matching models — type to add a custom one",
}: SearchableSelectProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [highlightedIdx, setHighlightedIdx] = useState(0);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  // Sync input text when value changes externally.
  useEffect(() => {
    if (!open) {
      const selected = options.find((o) => o.value === value);
      setQuery(selected ? selected.label : value);
    }
  }, [value, options, open]);

  const filtered = useMemo(() => {
    if (!query.trim()) return options.slice(0, 100); // limit for perf
    const q = query.toLowerCase();
    return options
      .filter(
        (o) =>
          o.value.toLowerCase().includes(q) ||
          o.label.toLowerCase().includes(q) ||
          (o.sublabel?.toLowerCase().includes(q) ?? false),
      )
      .slice(0, 100);
  }, [options, query]);

  const selectOption = (opt: SearchableSelectOption) => {
    onChange(opt.value);
    setQuery(opt.label);
    setOpen(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setHighlightedIdx((prev) => Math.min(prev + 1, filtered.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlightedIdx((prev) => Math.max(prev - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (open && filtered[highlightedIdx]) {
        selectOption(filtered[highlightedIdx]);
      } else if (allowFreeText && query.trim()) {
        // Use typed value directly as the model id.
        onChange(query.trim());
        setOpen(false);
      }
    } else if (e.key === "Escape") {
      setOpen(false);
    }
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setQuery(e.target.value);
    setOpen(true);
    setHighlightedIdx(0);
    if (allowFreeText) {
      onChange(e.target.value);
    }
  };

  const handleClear = () => {
    setQuery("");
    onChange("");
    inputRef.current?.focus();
  };

  return (
    <div ref={containerRef} className="relative">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={handleInputChange}
          onFocus={() => setOpen(true)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={disabled}
          className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] py-2.5 pl-9 pr-9 text-sm placeholder:text-[var(--text-muted)] focus:border-accent-400 focus:outline-none focus:ring-2 focus:ring-accent-400/40 disabled:opacity-50"
          autoComplete="off"
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-0.5">
          {query && allowFreeText && (
            <button
              type="button"
              onClick={handleClear}
              className="rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
              title="Clear"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
          <button
            type="button"
            onClick={() => {
              setOpen((prev) => !prev);
              inputRef.current?.focus();
            }}
            className="rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
          >
            <ChevronDown className={`h-4 w-4 transition-transform ${open ? "rotate-180" : ""}`} />
          </button>
        </div>
      </div>

      {open && (
        <div className="absolute z-50 mt-1 max-h-72 w-full overflow-auto rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]">
          {loading ? (
            <div className="px-3 py-4 text-center text-xs text-[var(--text-muted)]">
              Loading models…
            </div>
          ) : filtered.length === 0 ? (
            <div className="px-3 py-4 text-center text-xs text-[var(--text-muted)]">
              {allowFreeText ? noResultsText : "No matching models found"}
            </div>
          ) : (
            <>
              {filtered.map((opt, idx) => (
                <button
                  key={opt.value}
                  type="button"
                  onClick={() => selectOption(opt)}
                  onMouseEnter={() => setHighlightedIdx(idx)}
                  className={`flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm transition-colors ${
                    idx === highlightedIdx
                      ? "bg-accent-50 dark:bg-accent-900/20"
                      : "hover:bg-[var(--bg-subtle)]"
                  }`}
                >
                  <div className="min-w-0 flex-1">
                    <span className="block truncate font-medium text-[var(--text)]">
                      {opt.label}
                    </span>
                    {opt.sublabel && (
                      <span className="block truncate text-xs text-[var(--text-muted)]">
                        {opt.sublabel}
                      </span>
                    )}
                  </div>
                  {value === opt.value && (
                    <Check className="h-4 w-4 shrink-0 text-accent-600" />
                  )}
                </button>
              ))}
              {allowFreeText && query.trim() && !filtered.some((o) => o.value === query.trim()) && (
                <button
                  type="button"
                  onClick={() => {
                    onChange(query.trim());
                    setOpen(false);
                  }}
                  className="flex w-full items-center gap-2 border-t border-[var(--border)] px-3 py-2 text-left text-xs text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-900/20"
                >
                  <span className="font-medium">+ Add "{query.trim()}" as custom model</span>
                </button>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}