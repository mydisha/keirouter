// Layout primitives shared by the Settings tabs: a card with a divided body,
// "label + description left, control right" rows, a footer save bar and quiet
// inline notes. Kept local to Settings so the shared ui kit stays untouched.
import type { ReactNode } from "react";
import { AlertTriangle, Info, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "../../components/ui";

export function SettingsCard({
  title,
  description,
  action,
  footer,
  children,
  className,
}: {
  title?: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  footer?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      {title && (
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-3">
          <div className="min-w-0 max-w-3xl">
            <h2 className="text-[13px] font-semibold text-fg">{title}</h2>
            {description && <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">{description}</p>}
          </div>
          {action && <div className="flex shrink-0 flex-wrap items-center gap-2">{action}</div>}
        </header>
      )}
      {children && <div className="divide-y divide-line">{children}</div>}
      {footer}
    </section>
  );
}

// SettingRow: label + one-line description on the left, control on the right.
// `nested` indents rows that only apply while their parent toggle is on.
export function SettingRow({
  label,
  description,
  error,
  nested,
  children,
  stack,
}: {
  label: ReactNode;
  description?: ReactNode;
  error?: string;
  nested?: boolean;
  children?: ReactNode;
  /** Put the control under the text instead of beside it (wide controls). */
  stack?: boolean;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 px-4 py-3.5",
        !stack && "sm:flex-row sm:items-center sm:justify-between sm:gap-6",
        nested && "sm:pl-9",
      )}
    >
      <div className="min-w-0 max-w-2xl">
        <div className="flex flex-wrap items-center gap-2 text-[13px] font-medium text-fg">{label}</div>
        {description && <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">{description}</p>}
        {error && (
          <p role="alert" className="mt-1 text-[12px] leading-5 text-bad">
            {error}
          </p>
        )}
      </div>
      {children && <div className={cn("min-w-0 max-w-full", !stack && "sm:shrink-0")}>{children}</div>}
    </div>
  );
}

// ToggleRow renders the whole row as a <label> so the text names the switch
// and clicking anywhere on the row flips it.
export function ToggleRow({
  label,
  description,
  nested,
  children,
}: {
  label: ReactNode;
  description?: ReactNode;
  nested?: boolean;
  children: ReactNode;
}) {
  return (
    <label
      className={cn(
        "flex cursor-pointer items-center justify-between gap-6 px-4 py-3.5 transition-colors hover:bg-hover/60",
        nested && "sm:pl-9",
      )}
    >
      <span className="min-w-0 max-w-2xl">
        <span className="flex flex-wrap items-center gap-2 text-[13px] font-medium text-fg">{label}</span>
        {description && <span className="mt-0.5 block text-[12px] leading-5 text-fg-muted">{description}</span>}
      </span>
      <span className="shrink-0">{children}</span>
    </label>
  );
}

// SaveBar is the consistent dirty-state footer: status · Discard · Save.
export function SaveBar({
  dirty,
  saving,
  onDiscard,
  onSave,
  error,
  saveLabel = "Save changes",
}: {
  dirty: boolean;
  saving: boolean;
  onDiscard: () => void;
  onSave: () => void;
  error?: string;
  saveLabel?: string;
}) {
  return (
    <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
      <span className={cn("mr-auto min-w-0 text-[12.5px]", error && !saving ? "text-bad" : "text-fg-muted")} aria-live="polite">
        {saving ? "Saving…" : error ? error : dirty ? "Unsaved changes" : "All changes saved"}
      </span>
      {dirty && !saving && (
        <Button variant="ghost" onClick={onDiscard}>
          Discard
        </Button>
      )}
      <Button disabled={!dirty || saving} onClick={onSave}>
        {saving && <Loader2 className="animate-spin" />}
        {saveLabel}
      </Button>
    </div>
  );
}

// Note is an inline callout. Neutral by default; warn/bad only when the text
// describes a real risk.
export function Note({
  tone = "neutral",
  children,
  className,
}: {
  tone?: "neutral" | "warn" | "bad";
  children: ReactNode;
  className?: string;
}) {
  const Icon = tone === "neutral" ? Info : AlertTriangle;
  return (
    <div
      role={tone === "neutral" ? "note" : "alert"}
      className={cn(
        "flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-[12px] leading-5",
        tone === "neutral" && "border-line bg-subtle text-fg-muted",
        tone === "warn" && "border-warn/30 bg-warn/5 text-fg",
        tone === "bad" && "border-bad/30 bg-bad/5 text-fg",
        className,
      )}
    >
      <Icon
        className={cn("mt-0.5 h-4 w-4 shrink-0", tone === "neutral" ? "text-fg-faint" : tone === "warn" ? "text-warn" : "text-bad")}
        strokeWidth={1.75}
        aria-hidden="true"
      />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

// FormLabel / FormHint mirror ConnectKit's field styling for stacked fields.
export function FormField({
  label,
  hint,
  optional,
  error,
  htmlFor,
  children,
}: {
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  error?: string;
  htmlFor?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
      </label>
      {children}
      {error ? (
        <p role="alert" className="text-[12px] leading-5 text-bad">
          {error}
        </p>
      ) : (
        hint && <div className="text-[12px] leading-5 text-fg-muted">{hint}</div>
      )}
    </div>
  );
}

export function Code({ children }: { children: ReactNode }) {
  return <code className="rounded-md bg-subtle px-1 py-px font-mono text-[11.5px] text-fg">{children}</code>;
}

// UnitInput is a compact numeric input with a trailing unit label.
export function UnitInput({
  unit,
  className,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { unit: string }) {
  return (
    <div className={cn("relative w-32", className)}>
      <input
        type="number"
        {...props}
        className="h-9 w-full rounded-lg border border-line bg-surface pl-3 pr-11 text-right text-[13px] tabular-nums text-fg placeholder:text-fg-faint transition-colors hover:border-line-strong focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25 aria-[invalid=true]:border-bad/60 disabled:opacity-60"
      />
      <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-[12px] text-fg-faint">{unit}</span>
    </div>
  );
}

export const inputClass =
  "h-9 w-full rounded-lg border border-line bg-surface px-3 text-[13px] text-fg placeholder:text-fg-faint transition-colors hover:border-line-strong focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25 aria-[invalid=true]:border-bad/60 disabled:opacity-60";
