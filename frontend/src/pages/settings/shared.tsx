// Layout primitives shared by the Settings tabs: a card with a divided body,
// "label + description left, control right" rows, a footer save bar, an info
// tooltip, a "Learn more" disclosure and an accessible segmented control.
// Kept local to Settings so the shared ui kit stays untouched.
import {
  isValidElement,
  cloneElement,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactElement,
  type ReactNode,
} from "react";
import { AlertTriangle, ChevronRight, Info, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button, Toggle } from "../../components/ui";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../../components/ui/tooltip";

// Shared focus ring for local interactive elements.
export const focusRing = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

// ids derived from a control id: SettingRow renders its label, description
// and error with these so the control can point at them.
export const rowIds = (id: string) => ({
  label: `${id}-label`,
  desc: `${id}-desc`,
  error: `${id}-error`,
});

// describedBy joins the description/error ids that apply to a control.
export function describedBy(id: string, { desc = true, error }: { desc?: boolean; error?: string | false } = {}) {
  const ids = rowIds(id);
  return [desc && ids.desc, error && ids.error].filter(Boolean).join(" ") || undefined;
}

export function SettingsCard({
  title,
  description,
  action,
  footer,
  children,
  className,
  tone = "default",
  busy,
}: {
  title?: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  footer?: ReactNode;
  children?: ReactNode;
  className?: string;
  /** "danger" marks the card as the danger zone. */
  tone?: "default" | "danger";
  busy?: boolean;
}) {
  const headingId = useId();
  return (
    <section
      aria-labelledby={title ? headingId : undefined}
      aria-busy={busy || undefined}
      className={cn(
        "overflow-hidden rounded-2xl border bg-surface shadow-[var(--shadow-card)]",
        tone === "danger" ? "border-bad/40" : "border-line",
        className,
      )}
    >
      {title && (
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-3">
          <div className="min-w-0 max-w-3xl">
            <h2 id={headingId} className={cn("text-[13px] font-semibold", tone === "danger" ? "text-bad" : "text-fg")}>
              {title}
            </h2>
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
// Pass `controlId` (the id of the control) to wire the label, description and
// error to it; set `labelable={false}` when the control is not a form element
// (segmented groups, buttons), then point it at `${controlId}-label` with
// aria-labelledby. `nested` indents rows that only apply while their parent
// toggle is on.
export function SettingRow({
  label,
  description,
  info,
  infoLabel,
  error,
  nested,
  children,
  stack,
  controlId,
  labelable = true,
}: {
  label: ReactNode;
  description?: ReactNode;
  /** Longer explanation, shown in an info tooltip next to the label. */
  info?: ReactNode;
  /** Accessible name for the info button when `label` isn't plain text. */
  infoLabel?: string;
  error?: string;
  nested?: boolean;
  children?: ReactNode;
  /** Put the control under the text instead of beside it (wide controls). */
  stack?: boolean;
  controlId?: string;
  labelable?: boolean;
}) {
  const ids = controlId ? rowIds(controlId) : null;
  const labelClass = "text-[13px] font-medium text-fg";
  return (
    <div
      className={cn(
        "flex flex-col gap-3 px-4 py-3.5",
        !stack && "sm:flex-row sm:items-center sm:justify-between sm:gap-6",
        nested && "sm:pl-9",
      )}
    >
      <div className="min-w-0 max-w-2xl">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {ids && labelable ? (
            <label htmlFor={controlId} id={ids.label} className={labelClass}>
              {label}
            </label>
          ) : (
            <span id={ids?.label} className={cn(labelClass, "flex flex-wrap items-center gap-2")}>
              {label}
            </span>
          )}
          {info && (
            <InfoTip label={infoLabel ?? (typeof label === "string" ? `About ${label.toLowerCase()}` : "More information")}>
              {info}
            </InfoTip>
          )}
        </div>
        {description && (
          <div id={ids?.desc} className="mt-0.5 text-[12px] leading-5 text-fg-muted">
            {description}
          </div>
        )}
        {error && (
          <p id={ids?.error} role="alert" className="mt-1 text-[12px] leading-5 text-bad">
            {error}
          </p>
        )}
      </div>
      {children && <div className={cn("min-w-0 max-w-full", !stack && "sm:shrink-0")}>{children}</div>}
    </div>
  );
}

// ToggleRow renders a switch named by the row label (aria-labelledby) and
// described by the row description. Clicking the label text flips it too.
export function ToggleRow({
  label,
  description,
  info,
  nested,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  description?: ReactNode;
  info?: ReactNode;
  nested?: boolean;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  const id = useId();
  const ids = rowIds(id);
  return (
    <div className={cn("flex items-center justify-between gap-6 px-4 py-3.5", nested && "sm:pl-9")}>
      <div className="min-w-0 max-w-2xl">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <label htmlFor={id} id={ids.label} className="cursor-pointer text-[13px] font-medium text-fg">
            {label}
          </label>
          {info && <InfoTip label={`About ${label.toLowerCase()}`}>{info}</InfoTip>}
        </div>
        {description && (
          <p id={ids.desc} className="mt-0.5 text-[12px] leading-5 text-fg-muted">
            {description}
          </p>
        )}
      </div>
      <Toggle aria-labelledby={ids.label}
        id={id}
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        aria-describedby={description ? ids.desc : undefined}
      />
    </div>
  );
}

// InfoTip is a small "i" button that reveals a longer explanation on hover,
// focus or tap. Escape or clicking elsewhere closes it.
export function InfoTip({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip open={open} onOpenChange={setOpen}>
        <TooltipTrigger asChild>
          <button
            type="button"
            aria-label={label}
            // Radix closes tooltips on press; make press toggle instead so the
            // tip also works on touch screens.
            onPointerDown={(e) => e.preventDefault()}
            onClick={(e) => {
              e.preventDefault();
              setOpen((o) => !o);
            }}
            className={cn(
              "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-hover hover:text-fg",
              focusRing,
            )}
          >
            <Info className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent side="top" className="max-w-xs">
          {children}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

// Disclosure is a quiet "Learn more" expander for long explanations.
export function Disclosure({
  summary,
  children,
  className,
  defaultOpen,
}: {
  summary: ReactNode;
  children: ReactNode;
  className?: string;
  defaultOpen?: boolean;
}) {
  return (
    <details className={cn("group", className)} open={defaultOpen}>
      <summary
        className={cn(
          "inline-flex min-h-6 cursor-pointer list-none items-center gap-1 rounded-md text-[12.5px] font-medium text-link hover:underline [&::-webkit-details-marker]:hidden",
          focusRing,
        )}
      >
        <ChevronRight className="h-3.5 w-3.5 transition-transform group-open:rotate-90" strokeWidth={1.75} aria-hidden="true" />
        {summary}
      </summary>
      <div className="mt-2">{children}</div>
    </details>
  );
}

// Segmented is a radiogroup of small buttons with roving focus: arrow keys
// move and select, Tab leaves the group.
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  "aria-label": ariaLabel,
  "aria-labelledby": labelledBy,
  "aria-describedby": describedById,
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const current = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  );
  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const keys = ["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp", "Home", "End"];
    if (!keys.includes(e.key)) return;
    e.preventDefault();
    const n = options.length;
    const next =
      e.key === "Home"
        ? 0
        : e.key === "End"
          ? n - 1
          : (current + (e.key === "ArrowRight" || e.key === "ArrowDown" ? 1 : -1) + n) % n;
    onChange(options[next].value);
    ref.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus();
  };
  return (
    <div
      ref={ref}
      role="radiogroup"
      aria-label={ariaLabel}
      aria-labelledby={labelledBy}
      aria-describedby={describedById}
      onKeyDown={onKeyDown}
      className="inline-flex rounded-xl border border-line bg-subtle p-0.5"
    >
      {options.map((opt, i) => {
        const on = i === current;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(opt.value)}
            className={cn(
              "h-7 whitespace-nowrap rounded-lg px-2.5 text-[12px] font-medium transition-colors",
              focusRing,
              on ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
            )}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
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
      <span role="status" className={cn("mr-auto min-w-0 text-[12.5px]", error && !saving ? "text-bad" : "text-fg-muted")}>
        {saving ? "Saving…" : error ? `Couldn't save: ${error}` : dirty ? "Unsaved changes" : "All changes saved"}
      </span>
      {dirty && !saving && (
        <Button variant="ghost" onClick={onDiscard}>
          Discard
        </Button>
      )}
      <Button disabled={!dirty || saving} onClick={onSave}>
        {saving && <Loader2 className="animate-spin" aria-hidden="true" />}
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
      role="note"
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

// FormField mirrors ConnectKit's field styling for stacked fields. When its
// child is a single element, the hint/error ids are attached to it through
// aria-describedby (and aria-invalid when there is an error).
export function FormField({
  label,
  hint,
  optional,
  required,
  error,
  htmlFor,
  children,
}: {
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  required?: boolean;
  error?: string;
  htmlFor: string;
  children: ReactNode;
}) {
  const hintId = `${htmlFor}-hint`;
  const errorId = `${htmlFor}-error`;
  const described = error ? errorId : hint ? hintId : undefined;
  const control =
    isValidElement(children) && described
      ? cloneElement(children as ReactElement<Record<string, unknown>>, {
          "aria-describedby": described,
          ...(error ? { "aria-invalid": true } : {}),
        })
      : children;
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
        {required && <span className="text-[12px] font-normal text-fg-faint">Required</span>}
      </label>
      {control}
      {error ? (
        <p id={errorId} role="alert" className="text-[12px] leading-5 text-bad">
          {error}
        </p>
      ) : (
        hint && (
          <div id={hintId} className="text-[12px] leading-5 text-fg-muted">
            {hint}
          </div>
        )
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
        className="h-9 w-full rounded-lg border border-input bg-surface pl-3 pr-11 text-right text-[13px] tabular-nums text-fg placeholder:text-fg-faint transition-colors hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 aria-[invalid=true]:border-bad disabled:opacity-60"
      />
      <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-[12px] text-fg-muted" aria-hidden="true">
        {unit}
      </span>
    </div>
  );
}

export const inputClass =
  "h-9 w-full rounded-lg border border-input bg-surface px-3 text-[13px] text-fg placeholder:text-fg-faint transition-colors hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 aria-[invalid=true]:border-bad disabled:opacity-60";
