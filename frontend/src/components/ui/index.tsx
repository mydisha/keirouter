// Reusable UI primitives styled with the KeiRouter design system: neutral
// surfaces, hairline borders, monochrome primary actions, colour only for meaning.
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import type {
  ButtonHTMLAttributes,
  InputHTMLAttributes,
  SelectHTMLAttributes,
} from "react";
import { AlertCircle, Inbox, X, type LucideIcon } from "lucide-react";

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={`overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-card)] ${className}`}
    >
      {children}
    </div>
  );
}

// Skeleton is a shimmering placeholder block sized via className. Showing a
// page's shape while its data loads reads as faster than a centered spinner and
// avoids layout shift when the real content arrives. The shimmer animation and
// reduced-motion handling live in index.css (.skeleton).
export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`skeleton ${className}`} aria-hidden="true" />;
}

// SkeletonText renders a stack of skeleton lines for paragraph-like content.
// The last line is shortened to mimic natural text flow.
export function SkeletonText({ lines = 3, className = "" }: { lines?: number; className?: string }) {
  return (
    <div className={`space-y-2 ${className}`} aria-hidden="true">
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton key={i} className={`h-3.5 ${i === lines - 1 ? "w-2/3" : "w-full"}`} />
      ))}
    </div>
  );
}

// SectionHeader is the in-card header with an optional rounded icon chip, used
// across Settings/Endpoints-style panels in the attachment.
export function SectionHeader({
  title,
  description,
  icon: Icon,
  iconTone = "accent",
  action,
}: {
  title: ReactNode;
  description?: string;
  icon?: LucideIcon;
  iconTone?: "accent" | "neutral" | "danger" | "secondary";
  action?: ReactNode;
}) {
  // Icons render as quiet glyphs, not tinted chips; only danger keeps colour
  // because it carries meaning.
  const iconColor = iconTone === "danger" ? "text-bad" : "text-fg-faint";
  return (
    <div className="flex flex-col gap-3 px-4 pb-3 pt-4 sm:flex-row sm:items-start sm:justify-between sm:px-5">
      <div className="flex min-w-0 items-start gap-2.5">
        {Icon && <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${iconColor}`} strokeWidth={1.75} aria-hidden="true" />}
        <div>
          <h2 className="text-[14px] font-semibold tracking-[-0.005em]">{title}</h2>
          {description && <p className="mt-0.5 text-[13px] text-fg-muted">{description}</p>}
        </div>
      </div>
      {action}
    </div>
  );
}

// CardHeader keeps the lighter divider-style header for list cards.
export function CardHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3 border-b border-[var(--border)] px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between sm:px-5">
      <div className="min-w-0">
        <h2 className="text-[14px] font-semibold tracking-[-0.005em]">{title}</h2>
        {description && <p className="mt-0.5 max-w-2xl text-[13px] leading-5 text-fg-muted">{description}</p>}
      </div>
      {action && <div className="flex shrink-0 flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

// SettingsSection groups related cards under a labeled heading with a subtle divider.
// Improves scanability on long settings pages.
export function SettingsSection({
  title,
  icon: Icon,
  children,
}: {
  title: string;
  icon?: LucideIcon;
  children: ReactNode;
}) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2.5 pt-2">
        {Icon && <Icon className="h-4 w-4 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
        <h3 className="text-[13px] font-semibold text-fg-muted">
          {title}
        </h3>
        <div className="flex-1 border-t border-[var(--border)]" />
      </div>
      <div className="space-y-4">
        {children}
      </div>
    </div>
  );
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "ghost" | "danger";
};

export function Button({ variant = "primary", className = "", ...props }: ButtonProps) {
  const base =
    "inline-flex min-h-9 items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-[13px] font-medium transition-[background-color,border-color,color,opacity] duration-150 disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg)] [&_svg]:size-4 [&_svg]:shrink-0";
  // Primary is monochrome (ink / paper). Colour is reserved for data and
  // meaning, so "secondary" and "ghost" are both neutral surfaces.
  const variants = {
    primary: "border border-transparent bg-primary text-primary-fg hover:opacity-85",
    secondary:
      "border border-line-strong bg-surface text-fg hover:bg-hover",
    ghost:
      "border border-line bg-surface text-fg hover:border-line-strong hover:bg-hover",
    danger:
      "border border-bad/35 bg-surface text-bad hover:bg-bad/10",
  };
  return <button className={`${base} ${variants[variant]} ${className}`} {...props} />;
}

export function Input({ className = "", ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={`min-h-9 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[13px] transition-[border-color,box-shadow,background-color] placeholder:text-fg-faint hover:border-[var(--border-strong)] focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25 disabled:cursor-not-allowed disabled:bg-[var(--bg-subtle)] disabled:opacity-60 ${className}`}
      {...props}
    />
  );
}

export function Select({ className = "", children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={`min-h-9 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[13px] transition-[border-color,box-shadow,background-color] hover:border-[var(--border-strong)] focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25 disabled:cursor-not-allowed disabled:bg-[var(--bg-subtle)] disabled:opacity-60 ${className}`}
      {...props}
    >
      {children}
    </select>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="text-xs font-medium text-[var(--text-muted)]">{label}</span>
      {children}
    </label>
  );
}

export function Badge({
  children,
  tone = "neutral",
  title,
}: {
  children: ReactNode;
  tone?: "neutral" | "accent" | "secondary" | "danger" | "warning" | "success";
  title?: string;
}) {
  const tones = {
    neutral: "border-line bg-subtle text-fg-muted",
    accent: "border-transparent bg-accent-500/10 text-accent-600 dark:text-accent-300",
    secondary: "border-transparent bg-secondary-500/10 text-secondary-600 dark:text-secondary-300",
    danger: "border-transparent bg-bad/10 text-bad",
    warning: "border-transparent bg-warn/12 text-warn",
    success: "border-transparent bg-ok/10 text-ok",
  };
  return (
    <span
      className={`inline-flex h-5 items-center gap-1 whitespace-nowrap rounded-md border px-1.5 text-[11.5px] font-medium ${tones[tone]}`}
      title={title}
    >
      {children}
    </span>
  );
}

// StatusDot is the small filled circle used next to "Healthy" / "Active" labels.
export function StatusDot({ tone = "success", label }: { tone?: "success" | "danger" | "warning" | "secondary"; label?: string }) {
  const colors = {
    success: "bg-ok",
    secondary: "bg-fg-faint",
    danger: "bg-bad",
    warning: "bg-warn",
  };
  return <span className={`inline-block h-1.5 w-1.5 rounded-full ${colors[tone]}`} role="img" aria-label={label || tone} />;
}

export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="px-6 py-14 text-center">
      <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-full border border-[var(--border)] bg-[var(--bg-subtle)]" aria-hidden="true">
        <Inbox className="h-4 w-4 text-[var(--text-muted)]" />
      </div>
      <p className="text-sm font-semibold text-[var(--text)]">{title}</p>
      {hint && <p className="mx-auto mt-1.5 max-w-md text-sm leading-5 text-[var(--text-muted)]">{hint}</p>}
    </div>
  );
}

// ErrorBanner is the consistent inline error surface used inside forms and
// cards. For transient feedback prefer a toast; use this for persistent,
// in-context errors (failed loads, validation summaries).
export function ErrorBanner({ message, className = "" }: { message: string; className?: string }) {
  return (
    <div
      role="alert"
      className={`flex items-start gap-2.5 rounded-lg border border-[color:var(--color-danger)]/30 bg-[color:var(--color-danger)]/10 px-3.5 py-2.5 ${className}`}
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-[color:var(--color-danger)]" strokeWidth={2} />
      <p className="text-sm leading-snug break-words overflow-hidden text-[color:var(--color-danger)]">{message}</p>
    </div>
  );
}

// ErrorCard is a full-card error state for failed data loads.
export function ErrorCard({ message }: { message: string }) {
  return (
    <Card className="flex flex-col items-center gap-2 px-6 py-12 text-center">
      <AlertCircle className="h-6 w-6 text-[color:var(--color-danger)]" strokeWidth={2} />
      <p className="text-sm text-[color:var(--color-danger)]">{message}</p>
    </Card>
  );
}

export function Spinner() {
  return (
    <div className="flex items-center justify-center py-10" role="status" aria-label="Loading">
      <div className="h-5 w-5 animate-spin rounded-full border-2 border-line-strong border-t-fg-muted" />
    </div>
  );
}

export function StatCard({
  icon: Icon,
  iconTone = "accent",
  label,
  value,
  delta,
}: {
  icon: LucideIcon;
  iconTone?: "accent" | "warning" | "danger";
  label: string;
  value: string;
  delta?: { text: string; direction?: "up" | "down" | "flat" };
}) {
  const deltaColor =
    delta?.direction === "up"
      ? "text-ok"
      : delta?.direction === "down"
        ? "text-bad"
        : "text-fg-muted";
  const iconColor = iconTone === "danger" ? "text-bad" : iconTone === "warning" ? "text-warn" : "text-fg-faint";

  return (
    <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] px-4 py-3.5 shadow-[var(--shadow-card)]">
      <div className="flex items-center gap-1.5 text-[12px] font-medium text-fg-muted">
        <Icon className={`h-3.5 w-3.5 shrink-0 ${iconColor}`} strokeWidth={1.75} aria-hidden="true" />
        <span className="truncate">{label}</span>
      </div>
      <p className="mt-1.5 text-2xl font-semibold tracking-[-0.02em] tabular-nums text-fg">{value}</p>
      {delta && <p className={`mt-1 text-[12px] font-medium tabular-nums ${deltaColor}`}>{delta.text}</p>}
    </div>
  );
}

// SegmentedControl renders the Gentle / Balanced / Strong style toggle group.
export function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
}) {
  return (
    <div className="inline-flex rounded-xl border border-line bg-subtle p-0.5" role="radiogroup">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          role="radio"
          aria-checked={value === opt.value}
          onClick={() => onChange(opt.value)}
          className={`h-7 rounded-lg px-2.5 text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 ${
            value === opt.value
              ? "bg-surface text-fg shadow-[0_0_0_1px_var(--border-strong)]"
              : "text-[var(--text-muted)] hover:text-[var(--text)]"
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

// Toggle is a small accessible switch.
// Modal is a reusable dialog overlay. Escape and backdrop click close it.
export function Modal({
  open,
  onClose,
  title,
  subtitle,
  children,
  maxWidth = "max-w-lg",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  subtitle?: string;
  children: ReactNode;
	maxWidth?: string;
}) {
	const dialogRef = useRef<HTMLDivElement>(null);
	const onCloseRef = useRef(onClose);
	const titleId = useId();

	useEffect(() => {
		onCloseRef.current = onClose;
	}, [onClose]);

	useEffect(() => {
		if (!open) return;
		const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		const dialog = dialogRef.current;
		const focusableSelector = [
			"button:not([disabled])",
			"[href]",
			"input:not([disabled])",
			"select:not([disabled])",
			"textarea:not([disabled])",
			"[tabindex]:not([tabindex='-1'])",
			"[contenteditable='true']",
		].join(",");
		const focusableElements = () =>
			Array.from(dialog?.querySelectorAll<HTMLElement>(focusableSelector) ?? [])
				.filter((element) => element.getAttribute("aria-hidden") !== "true");

		const frame = window.requestAnimationFrame(() => {
			const initial = dialog?.querySelector<HTMLElement>("[data-modal-autofocus]")
				?? focusableElements()[0]
				?? dialog;
			initial?.focus();
		});

		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				event.preventDefault();
				onCloseRef.current();
				return;
			}
			if (event.key !== "Tab" || !dialog) return;
			const elements = focusableElements();
			if (elements.length === 0) {
				event.preventDefault();
				dialog.focus();
				return;
			}
			const first = elements[0];
			const last = elements[elements.length - 1];
			const active = document.activeElement;
			if (event.shiftKey && (active === first || !dialog.contains(active))) {
				event.preventDefault();
				last.focus();
			} else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
				event.preventDefault();
				first.focus();
			}
		};
		const onFocusIn = (event: FocusEvent) => {
			if (dialog && event.target instanceof Node && !dialog.contains(event.target)) {
				dialog.focus();
			}
		};
		document.addEventListener("keydown", onKey);
		document.addEventListener("focusin", onFocusIn);
		return () => {
			window.cancelAnimationFrame(frame);
			document.removeEventListener("keydown", onKey);
			document.removeEventListener("focusin", onFocusIn);
			if (previousFocus?.isConnected) previousFocus.focus();
		};
	}, [open]);

  if (!open) return null;

	return (
		<div
			className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4 animate-in fade-in-0 duration-150"
			onClick={onClose}
		>
			<div
				ref={dialogRef}
				role="dialog"
				aria-modal="true"
				aria-labelledby={titleId}
				tabIndex={-1}
				className={`w-full ${maxWidth} rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)] animate-in fade-in-0 zoom-in-[0.98] duration-150`}
				onClick={(e) => e.stopPropagation()}
			>
				<div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3.5">
					<div>
						<h2 id={titleId} className="text-[15px] font-semibold tracking-[-0.01em]">{title}</h2>
            {subtitle && <p className="mt-0.5 text-[13px] text-fg-muted">{subtitle}</p>}
          </div>
					<button
						type="button"
						onClick={onClose}
						aria-label="Close"
						data-modal-autofocus
            className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

// TabBar renders a horizontal tab navigation strip.
export function TabBar<T extends string>({
  tabs,
  active,
  onChange,
}: {
  tabs: { value: T; label: string; icon?: LucideIcon }[];
  active: T;
  onChange: (v: T) => void;
}) {
  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
    const controls = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
    const current = controls.indexOf(document.activeElement as HTMLButtonElement);
    if (current < 0 || controls.length === 0) return;
    event.preventDefault();
    const next = event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? controls.length - 1
        : (current + (event.key === 'ArrowRight' ? 1 : -1) + controls.length) % controls.length;
    controls[next]?.focus();
    controls[next]?.click();
  };

  return (
    <div className="flex gap-1 overflow-x-auto border-b border-[var(--border)] px-1 pb-px" role="tablist" onKeyDown={onKeyDown}>
      {tabs.map((tab) => {
        const isActive = tab.value === active;
        return (
          <button
            key={tab.value}
            type="button"
            role="tab"
            aria-selected={isActive}
            onClick={() => onChange(tab.value)}
            className={`relative flex items-center gap-1.5 whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors duration-150 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 ${
              isActive
                ? "text-fg"
                : "text-fg-muted hover:text-fg"
            }`}
          >
            {tab.icon && <tab.icon className="h-4 w-4 shrink-0" strokeWidth={1.75} />}
            {tab.label}
            {isActive && (
              <span className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-500" />
            )}
          </button>
        );
      })}
    </div>
  );
}

export function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg)] ${
        checked ? "bg-accent-500" : "bg-ink-300 dark:bg-ink-700"
      }`}
    >
      <span
        className={`inline-block h-4 w-4 transform rounded-full bg-white shadow-sm transition-transform ${
          checked ? "translate-x-[18px]" : "translate-x-0.5"
        }`}
      />
    </button>
  );
}

// TablePagination is the footer row shown below a paginated table. It renders
// a total count on the left and Prev / page-indicator / Next on the right.
export function TablePagination({
  page,
  pages,
  total,
  onPage,
}: {
  page: number;
  pages: number;
  total: number;
  onPage: (p: number) => void;
}) {
  if (pages <= 1) return null;
  return (
    <div className="flex flex-col gap-2 border-t border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-2.5 text-xs sm:flex-row sm:items-center sm:justify-between">
      <span className="text-[var(--text-muted)]">{total.toLocaleString()} total</span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          disabled={page <= 1}
          onClick={() => onPage(page - 1)}
          className="inline-flex h-9 items-center justify-center rounded-lg border border-transparent px-3 font-medium text-[var(--text-muted)] transition-colors hover:border-[var(--border)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-40"
        >
          Previous
        </button>
        <span className="min-w-16 px-2 py-1 text-center font-medium tabular-nums">{page} / {pages}</span>
        <button
          type="button"
          disabled={page >= pages}
          onClick={() => onPage(page + 1)}
          className="inline-flex h-9 items-center justify-center rounded-lg border border-transparent px-3 font-medium text-[var(--text-muted)] transition-colors hover:border-[var(--border)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-40"
        >
          Next
        </button>
      </div>
    </div>
  );
}

// useClientPagination splits a client-side array into pages. Returns the
// current page slice, page number, total pages, and a setter. Pass `pageSize`
// to control how many rows appear per page.
export function useClientPagination<T>(items: T[], pageSize = 10) {
  const [page, setPage] = useState(1);
  const pages = Math.max(1, Math.ceil(items.length / pageSize));
  const clampedPage = Math.min(page, pages);
  const paged = useMemo(
    () => items.slice((clampedPage - 1) * pageSize, clampedPage * pageSize),
    [items, clampedPage, pageSize],
  );
  return { page: clampedPage, pages, paged, setPage, total: items.length };
}
