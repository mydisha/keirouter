import { lazy, Suspense, useState, useRef, useEffect, useId } from "react";
import { useQuery } from "@tanstack/react-query";
import { X, ExternalLink, ArrowUpCircle, FileText } from "lucide-react";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { IconTile } from "./ui";
import { dashboard } from "../lib/dashboardRoutes";

// Only needed once an update is available and the panel is opened.
const ChangelogMarkdown = lazy(() => import("./ChangelogMarkdown").then((m) => ({ default: m.ChangelogMarkdown })));

// useUpdateInfo is a shared hook so the TopBar badge and the Settings page
// read from the same cached query. The check hits GitHub at most every few
// hours (the backend caches), so a long stale time keeps it cheap.
export function useUpdateInfo() {
  return useQuery({
    queryKey: ["update-check"],
    queryFn: () => api.updateCheck(),
    staleTime: 1000 * 60 * 30, // 30 min — backend caches longer anyway
    refetchOnWindowFocus: false,
    retry: false,
  });
}

// UpdateNotification renders a small badge on the TopBar edge. It only appears
// when a newer release is available. Clicking it opens a popover with a short
// changelog preview and a link into the Settings page for the full changelog.
export function UpdateNotification() {
  const { data } = useUpdateInfo();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const panelId = useId();

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  useEffect(() => {
    if (!open) return;
    // Move focus into the popover so keyboard and screen-reader users land on it.
    panelRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  // Nothing to show unless GitHub reported a strictly newer version.
  if (!data || !data.update_available) return null;

  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };

  return (
    <div
      ref={ref}
      className="relative"
      // Tabbing out of the popover closes it.
      onBlur={(e) => {
        if (open && !e.currentTarget.contains(e.relatedTarget as Node | null)) setOpen(false);
      }}
    >
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`Update available: ${data.latest}`}
        title={`Update available: ${data.latest}`}
        className={`relative flex h-9 w-9 items-center justify-center rounded-lg transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 ${
          open ? "bg-hover text-fg" : "text-fg-muted"
        }`}
      >
        <ArrowUpCircle className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        {/* Edge dot — signals an available update (the name says it in words). */}
        <span className="absolute right-2 top-2 h-1.5 w-1.5 rounded-full bg-accent-500 ring-2 ring-surface" aria-hidden="true" />
      </button>

      {open && (
        <div
          ref={panelRef}
          id={panelId}
          role="dialog"
          aria-labelledby={titleId}
          tabIndex={-1}
          // Phones: full width minus 16px gutters under the top bar; from sm
          // a right-aligned popover. Never taller than the visible viewport.
          className="fixed inset-x-4 top-[calc(4rem+env(safe-area-inset-top))] z-50 flex max-h-[calc(100dvh-5rem-env(safe-area-inset-top))] flex-col sm:left-auto sm:right-4 sm:w-[min(34rem,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-float)] outline-none animate-in fade-in-0 zoom-in-[0.98] duration-150"
          style={{ outline: "none" }}
        >
          <div className="flex shrink-0 items-start justify-between gap-3 border-b border-line px-4 py-3">
            <div className="flex min-w-0 items-center gap-3">
              <IconTile icon={ArrowUpCircle} size="md" tone="blue" />
              <div className="min-w-0">
              <h2 id={titleId} className="text-[13px] font-semibold text-fg">Update available</h2>
              <p className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[12px] text-fg-muted">
                <span className="truncate font-mono">{data.current}</span>
                <span className="text-fg-faint" aria-hidden="true">→</span>
                <span className="sr-only">to</span>
                <span className="truncate font-mono font-medium text-fg">{data.latest}</span>
              </p>
              </div>
            </div>
            <button
              type="button"
              onClick={close}
              aria-label="Close"
              className="-mr-1.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg [@media(pointer:coarse)]:h-9 [@media(pointer:coarse)]:w-9 text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            </button>
          </div>

          {data.changelog && (
            <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-4 py-3 [scrollbar-gutter:stable]" tabIndex={0} role="region" aria-label="Changelog">
              <Suspense fallback={null}>
                <ChangelogMarkdown changelog={data.changelog} compact />
              </Suspense>
            </div>
          )}

          <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-line bg-subtle px-4 py-2.5">
            <Link
              to={dashboard("/settings#system")}
              onClick={() => setOpen(false)}
              className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-transparent bg-action [@media(pointer:coarse)]:h-9 px-3 text-[13px] font-medium text-action-fg shadow-[var(--shadow-card)] transition-colors hover:bg-action-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-subtle"
            >
              <FileText className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
              Full changelog
            </Link>
            {data.html_url && (
              <a
                href={data.html_url}
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex min-h-6 items-center gap-1 rounded text-[12px] [@media(pointer:coarse)]:min-h-9 [@media(pointer:coarse)]:px-1 font-medium text-link transition-colors hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                Release notes
                <ExternalLink className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                <span className="sr-only">(opens in a new tab)</span>
              </a>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
