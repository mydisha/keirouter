// Reusable UI primitives styled with the KeiRouter design system: neutral
// surfaces, hairline borders, monochrome primary actions, colour only for meaning.
import {
  Children,
  cloneElement,
  createContext,
  isValidElement,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactElement,
  type ReactNode,
} from "react";
import type {
  ButtonHTMLAttributes,
  InputHTMLAttributes,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from "react";
import { createPortal } from "react-dom";
import { AlertCircle, ArrowDownRight, ArrowRight, ArrowUpRight, Loader2, X, type LucideIcon } from "lucide-react";
import type { Delta } from "@/lib/format";

// joinIds merges space-separated id lists (aria-describedby etc.), dropping blanks.
function joinIds(...ids: (string | undefined | false | null)[]): string | undefined {
  const out = ids.filter(Boolean).join(" ").trim();
  return out || undefined;
}

// keepInView scrolls a horizontally overflowing strip (tabs, segmented
// options) so `child` is fully visible, with a little breathing room. Instant,
// not smooth, so it never fights reduced-motion preferences.
function keepInView(container: HTMLElement | null, child: Element | null | undefined, pad = 16) {
  if (!container || !child || container.scrollWidth <= container.clientWidth) return;
  const box = container.getBoundingClientRect();
  const r = child.getBoundingClientRect();
  if (r.left < box.left + pad) container.scrollLeft += r.left - box.left - pad;
  else if (r.right > box.right - pad) container.scrollLeft += r.right - box.right + pad;
}

// FieldContext lets <Field hint error> wire its hint / error ids into the
// Input / Select / Textarea it wraps without the page passing ids around.
interface FieldControlContext {
  id?: string;
  describedBy?: string;
  invalid?: boolean;
  required?: boolean;
}
const FieldContext = createContext<FieldControlContext | null>(null);

/** Returns aria props for a custom control rendered inside <Field>. */
export function useFieldControl(): FieldControlContext {
  return useContext(FieldContext) ?? {};
}

// Visually hidden text for screen readers. Inline styles rather than a class
// so it can never be overridden by a utility.
export function VisuallyHidden({ children, id, as = "span" }: { children: ReactNode; id?: string; as?: "span" | "div" | "p" }) {
  const Tag = as;
  return (
    <Tag id={id} className="sr-only">
      {children}
    </Tag>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={`overflow-hidden rounded-2xl border border-line bg-surface shadow-paper ${className}`}
    >
      {children}
    </div>
  );
}

// Skeleton is a shimmering placeholder block sized via className. Showing a
// page's shape while its data loads reads as faster than a centered spinner and
// avoids layout shift when the real content arrives. The shimmer animation and
// reduced-motion handling live in index.css (skeleton-shimmer).
// Skeletons are aria-hidden; put aria-busy="true" on the region they fill (or
// render <LoadingRegion>) so assistive tech knows content is still loading.
export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`skeleton-shimmer rounded-lg ${className}`} aria-hidden="true" />;
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

// LoadingRegion wraps skeleton placeholders: it marks the region busy and
// gives screen readers one short "Loading…" message instead of silence.
export function LoadingRegion({
  label = "Loading",
  className = "",
  children,
}: {
  label?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div aria-busy="true" className={className}>
      <span role="status" className="sr-only">
        {label}…
      </span>
      {children}
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
        {Icon && <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${iconColor}`} aria-hidden="true" />}
        <div>
          <h2 className="text-heading font-medium">{title}</h2>
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
    <div className="flex flex-col gap-3 border-b border-line px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between sm:px-5">
      <div className="min-w-0">
        <h2 className="text-heading font-medium">{title}</h2>
        {description && <p className="mt-0.5 max-w-2xl text-[13px] leading-5 text-fg-muted">{description}</p>}
      </div>
      {action && <div className="flex min-w-0 flex-wrap items-center gap-2">{action}</div>}
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
        {Icon && <Icon className="h-4 w-4 text-fg-faint" aria-hidden="true" />}
        <h3 className="text-[13px] font-medium text-fg-muted">
          {title}
        </h3>
        <div className="flex-1 border-t border-line" />
      </div>
      <div className="space-y-4">
        {children}
      </div>
    </div>
  );
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "ghost" | "quiet" | "danger";
  /** sm for toolbars, icon for a square icon-only button (give it an aria-label). */
  size?: "md" | "sm" | "icon";
};

// Every button is ink or paper; the brand blue never fills a button. One
// primary per view, outlined secondaries, quiet for toolbar icon buttons.
export function Button({ variant = "primary", size = "md", className = "", ...props }: ButtonProps) {
  const base =
    "press inline-flex select-none items-center justify-center gap-1.5 rounded-lg font-medium disabled:pointer-events-none disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas [&_svg:not([class*='size-'])]:size-4 [&_svg]:shrink-0";
  const sizes = {
    md: "min-h-9 px-3 py-1.5 text-[13px] pointer-coarse:min-h-11",
    sm: "min-h-8 px-2.5 text-[12.5px] pointer-coarse:min-h-11",
    icon: "size-9 text-[13px] pointer-coarse:size-11",
  };
  const variants = {
    primary: "border border-transparent bg-action text-action-fg shadow-paper hover:bg-action-hover",
    secondary: "border border-input bg-surface text-fg shadow-paper hover:bg-hover",
    ghost: "border border-line-strong bg-surface text-fg hover:bg-hover",
    quiet: "border border-transparent text-fg-muted hover:bg-hover hover:text-fg",
    danger: "border border-transparent bg-bad-soft text-bad hover:bg-bad hover:text-white",
  };
  return <button className={`${base} ${sizes[size]} ${variants[variant]} ${className}`} {...props} />;
}

// fieldAria merges an enclosing <Field>'s hint / error wiring into a control's
// own aria props. Explicit props on the control always win.
function useFieldAria(props: { id?: string; "aria-describedby"?: string; "aria-invalid"?: InputHTMLAttributes<HTMLInputElement>["aria-invalid"]; "aria-required"?: InputHTMLAttributes<HTMLInputElement>["aria-required"] }) {
  const field = useContext(FieldContext);
  if (!field) return {};
  return {
    id: props.id ?? field.id,
    "aria-describedby": joinIds(props["aria-describedby"], field.describedBy),
    "aria-invalid": props["aria-invalid"] ?? (field.invalid ? true : undefined),
    "aria-required": props["aria-required"] ?? (field.required ? true : undefined),
  };
}

const invalidControl = "aria-[invalid=true]:border-bad aria-[invalid=true]:hover:border-bad";

export function Input({ className = "", ...props }: InputHTMLAttributes<HTMLInputElement>) {
  const fieldAria = useFieldAria(props);
  return (
    <input
      className={`min-h-9 w-full rounded-lg border border-input bg-surface px-3 py-1.5 text-[13px] shadow-paper transition-[border-color,box-shadow,background-color] placeholder:text-fg-faint hover:border-fg-faint focus:border-ring focus:outline-none focus-visible:ring-2 focus-visible:ring-tone-ring pointer-coarse:min-h-11 disabled:cursor-not-allowed disabled:bg-subtle disabled:opacity-60 ${invalidControl} ${className}`}
      {...props}
      {...fieldAria}
    />
  );
}

export function Select({ className = "", children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  const fieldAria = useFieldAria(props);
  return (
    <select
      className={`min-h-9 w-full rounded-lg border border-input bg-surface px-3 py-1.5 text-[13px] shadow-paper transition-[border-color,box-shadow,background-color] hover:border-fg-faint focus:border-ring focus:outline-none focus-visible:ring-2 focus-visible:ring-tone-ring pointer-coarse:min-h-11 disabled:cursor-not-allowed disabled:bg-subtle disabled:opacity-60 ${invalidControl} ${className}`}
      {...props}
      {...fieldAria}
    >
      {children}
    </select>
  );
}

// Textarea matches Input: border-input boundary (≥ 3:1), accent focus ring.
export function Textarea({ className = "", ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const fieldAria = useFieldAria(props);
  return (
    <textarea
      className={`min-h-20 w-full rounded-lg border border-input bg-surface px-3 py-2 text-[13px] leading-5 shadow-paper transition-[border-color,box-shadow,background-color] placeholder:text-fg-faint hover:border-fg-faint focus:border-ring focus:outline-none focus-visible:ring-2 focus-visible:ring-tone-ring disabled:cursor-not-allowed disabled:bg-subtle disabled:opacity-60 ${invalidControl} ${className}`}
      {...props}
      {...fieldAria}
    />
  );
}

const NATIVE_CONTROLS = new Set(["input", "select", "textarea"]);

/**
 * Field is a labelled form row. Children are wrapped in a <label>, so a single
 * control inside is named by `label` automatically.
 *
 * Optional extras (all additive):
 * - `hint`: one line under the control, linked via aria-describedby.
 * - `error`: validation message under the control; sets aria-invalid and is
 *   linked via aria-describedby. Renders in place of nothing when empty.
 * - `optional` / `required`: a right-aligned text marker next to the label.
 *
 * <Input>, <Select> and <Textarea> pick the wiring up from context; a bare
 * native <input>/<select>/<textarea> child is cloned with the same props. For
 * any other custom control call `useFieldControl()` inside it.
 */
export function Field({
  label,
  children,
  hint,
  error,
  optional,
  required,
  className,
}: {
  label: string;
  children: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  optional?: boolean;
  required?: boolean;
  className?: string;
}) {
  const uid = useId();
  const hintId = `${uid}-hint`;
  const errorId = `${uid}-error`;
  const extended = hint != null || !!error || optional || required || className;

  if (!extended) {
    return (
      <label className="block space-y-1.5">
        <span className="text-[12.5px] font-medium text-fg">{label}</span>
        {children}
      </label>
    );
  }

  const ctx: FieldControlContext = {
    describedBy: joinIds(hint != null && hintId, !!error && errorId),
    invalid: !!error,
    required,
  };
  const only = Children.count(children) === 1 ? Children.only(children) : null;
  const content =
    only && isValidElement(only) && typeof only.type === "string" && NATIVE_CONTROLS.has(only.type)
      ? (() => {
          const el = only as ReactElement<Record<string, unknown>>;
          return cloneElement(el, {
            "aria-describedby": joinIds(el.props["aria-describedby"] as string | undefined, ctx.describedBy),
            "aria-invalid": (el.props["aria-invalid"] as boolean | undefined) ?? (ctx.invalid ? true : undefined),
            "aria-required": (el.props["aria-required"] as boolean | undefined) ?? (required ? true : undefined),
          });
        })()
      : children;

  return (
    <div className={`space-y-1.5 ${className ?? ""}`}>
      <label className="block space-y-1.5">
        <span className="flex items-baseline justify-between gap-2 text-[12.5px] font-medium text-fg">
          {label}
          {required ? (
            <span className="text-[12px] font-normal text-fg-muted">Required</span>
          ) : optional ? (
            <span className="text-[12px] font-normal text-fg-faint">Optional</span>
          ) : null}
        </span>
        <FieldContext.Provider value={ctx}>{content}</FieldContext.Provider>
      </label>
      {hint != null && (
        <p id={hintId} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="flex items-start gap-1.5 text-[12px] leading-5 text-bad">
          <AlertCircle className="mt-[3px] h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          <span>{error}</span>
        </p>
      )}
    </div>
  );
}

export function Badge({
  children,
  tone = "neutral",
  title,
  className = "",
}: {
  children: ReactNode;
  tone?: "neutral" | "accent" | "secondary" | "danger" | "warning" | "success";
  title?: string;
  className?: string;
}) {
  const tones = {
    neutral: "bg-subtle text-fg-muted",
    accent: "bg-brand-soft text-brand-ink",
    secondary: "bg-brand-soft text-brand-ink",
    danger: "bg-bad-soft text-bad",
    warning: "bg-warn-soft text-warn",
    success: "bg-ok-soft text-ok",
  };
  return (
    <span
      className={`inline-flex h-5 items-center gap-1 whitespace-nowrap rounded-md px-1.5 text-[11.5px] font-medium [&>svg]:size-3 ${tones[tone]} ${className}`}
      title={title}
    >
      {children}
    </span>
  );
}

// StatusDot is the small filled circle used next to "Healthy" / "Active" labels.
const STATUS_DOT_TEXT = {
  success: "OK",
  secondary: "Inactive",
  danger: "Error",
  warning: "Warning",
} as const;

// StatusDot never relies on colour alone: with `label` it is an image named by
// the label; without one it carries sr-only text for its tone. Pass
// `decorative` when the same status is already written next to it.
export function StatusDot({
  tone = "success",
  label,
  decorative,
}: {
  tone?: "success" | "danger" | "warning" | "secondary";
  label?: string;
  /** Hide from assistive tech because visible text beside it says the same. */
  decorative?: boolean;
}) {
  const colors = {
    success: "bg-ok",
    secondary: "bg-fg-faint",
    danger: "bg-bad",
    warning: "bg-warn",
  };
  const dot = `inline-block h-1.5 w-1.5 shrink-0 rounded-full ${colors[tone]}`;
  if (decorative) return <span className={dot} aria-hidden="true" />;
  if (label) return <span className={dot} role="img" aria-label={label} />;
  return (
    <>
      <span className={dot} aria-hidden="true" />
      <span className="sr-only">{STATUS_DOT_TEXT[tone]}</span>
    </>
  );
}

/**
 * EmptyState is the one "nothing here yet" surface: a title that says what is
 * missing, one line on what to do, and at most one primary plus one quieter
 * action. No icon: the sentence and the action carry it.
 *
 * - `variant="inline"` (default) sits inside an existing surface.
 * - `variant="card"` is a standalone dashed surface (a whole page/section is empty).
 * - `size="sm"` is the compact form for dashboard panels.
 */
export function EmptyState({
  title,
  hint,
  action,
  secondaryAction,
  variant = "inline",
  size = "md",
  titleAs: Title = "p",
  className = "",
}: {
  title: ReactNode;
  hint?: ReactNode;
  /** One primary action, e.g. <Button>Add pool</Button>. */
  action?: ReactNode;
  /** A quieter follow-up, e.g. a docs or "Clear filters" link. */
  secondaryAction?: ReactNode;
  variant?: "inline" | "card";
  size?: "md" | "sm";
  /** Heading level when the empty state is the page's main content (e.g. "not found"). */
  titleAs?: "p" | "h1" | "h2" | "h3";
  className?: string;
}) {
  const sm = size === "sm";
  return (
    <div
      className={`flex flex-col items-center text-center ${sm ? "px-5 py-7" : "px-6 py-10"} ${
        variant === "card" ? "rounded-2xl border border-dashed border-line-strong bg-surface" : ""
      } ${className}`}
    >
      <Title className={`font-medium text-fg ${sm ? "text-[13px]" : "text-heading"}`}>{title}</Title>
      {hint && <p className={`mx-auto mt-1 max-w-md text-pretty leading-5 text-fg-muted ${sm ? "text-[12.5px]" : "text-[13px]"}`}>{hint}</p>}
      {(action || secondaryAction) && (
        <div className={`flex flex-wrap items-center justify-center gap-x-4 gap-y-2 ${sm ? "mt-3" : "mt-4"}`}>
          {action}
          {secondaryAction}
        </div>
      )}
    </div>
  );
}

// ErrorBanner is the consistent inline error surface used inside forms and
// cards. For transient feedback prefer a toast; use this for persistent,
// in-context errors (failed loads, validation summaries).
export function ErrorBanner({
  message,
  className = "",
  id,
  action,
}: {
  message: string;
  className?: string;
  /** Lets a field point aria-describedby / aria-errormessage at this banner. */
  id?: string;
  /** Optional recovery control, e.g. a "Try again" button. */
  action?: ReactNode;
}) {
  return (
    <div
      role="alert"
      id={id}
      className={`flex items-start gap-2.5 rounded-xl bg-bad-soft px-3.5 py-2.5 ${className}`}
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-bad" aria-hidden="true" />
      <p className="min-w-0 flex-1 overflow-hidden break-words text-[13px] leading-snug text-bad">{message}</p>
      {action && <div className="-my-1 shrink-0">{action}</div>}
    </div>
  );
}

// ErrorCard is a full-card error state for failed data loads.
export function ErrorCard({ message, action }: { message: string; action?: ReactNode }) {
  return (
    <Card className="flex flex-col items-center gap-2 px-6 py-12 text-center">
      <div role="alert" className="flex flex-col items-center gap-2">
        <AlertCircle className="h-5 w-5 text-bad" aria-hidden="true" />
        <p className="text-[13px] text-bad">{message}</p>
      </div>
      {action && <div className="mt-2">{action}</div>}
    </Card>
  );
}

export function Spinner({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center py-10" role="status">
      <Loader2 className="size-5 animate-spin text-fg-muted" aria-hidden="true" />
      <span className="sr-only">{label}</span>
    </div>
  );
}

// SegmentedControl renders the Gentle / Balanced / Strong style toggle group.
// It is a radiogroup with a roving tabindex: Tab enters on the checked option,
// arrow keys / Home / End move and select, as native radios do.
export function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
  label,
  "aria-labelledby": labelledBy,
  className = "",
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string; disabled?: boolean }[];
  /** Accessible name for the group, e.g. "Time range". */
  label?: string;
  "aria-labelledby"?: string;
  className?: string;
}) {
  const enabled = options.filter((o) => !o.disabled);
  const focusValue = enabled.some((o) => o.value === value) ? value : enabled[0]?.value;

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const keys = ["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp", "Home", "End"];
    if (!keys.includes(event.key) || enabled.length === 0) return;
    event.preventDefault();
    const current = Math.max(0, enabled.findIndex((o) => o.value === value));
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? enabled.length - 1
          : (current + (event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : -1) + enabled.length) % enabled.length;
    const target = enabled[next];
    onChange(target.value);
    event.currentTarget.querySelector<HTMLButtonElement>(`[data-value="${CSS.escape(target.value)}"]`)?.focus();
  };

  // Too wide for a phone? The group scrolls sideways (scrollbar hidden) and
  // keeps the checked option in view.
  const groupRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    keepInView(groupRef.current, groupRef.current?.querySelector('[aria-checked="true"]'), 8);
  }, [value]);

  return (
    <div
      ref={groupRef}
      className={`inline-flex max-w-full overflow-x-auto overscroll-x-contain rounded-xl bg-subtle p-0.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden ${className}`}
      role="radiogroup"
      aria-label={label}
      aria-labelledby={labelledBy}
      onKeyDown={onKeyDown}
    >
      {options.map((opt) => {
        const checked = value === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            data-value={opt.value}
            aria-checked={checked}
            disabled={opt.disabled}
            tabIndex={opt.value === focusValue ? 0 : -1}
            onClick={() => onChange(opt.value)}
            className={`press h-7 shrink-0 whitespace-nowrap rounded-lg px-2.5 text-[12px] font-medium [@media(pointer:coarse)]:h-8 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 ${
              checked
                ? "bg-surface text-fg shadow-paper ring-1 ring-line"
                : "text-fg-muted hover:text-fg"
            }`}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

// Elements rendered in a portal on top of a Modal (menus, popovers, the model
// picker, confirm dialogs, toasts) live outside the dialog's DOM. Focus moving
// into them is not "escaping" the modal, so the trap leaves it alone.
const FLOATING_LAYER_SELECTOR = [
  "[data-floating-layer]",
  "[data-radix-popper-content-wrapper]",
  "[role='alertdialog']",
  "[role='dialog']",
  "[data-toast-region]",
].join(",");

function inFloatingLayer(dialog: HTMLElement | null, node: EventTarget | Element | null): boolean {
  if (!(node instanceof Element)) return false;
  const layer = node.closest(FLOATING_LAYER_SELECTOR);
  return !!layer && layer !== dialog && !dialog?.contains(layer);
}

// Footers rendered inside a Modal's children (`border-t … bg-subtle` as the
// last child of the body or of a form directly inside it, or anything marked
// data-modal-footer) stick to the bottom of the scrolling body, so Save /
// Cancel stay reachable however long the form is.
const STICKY_MODAL_FOOTERS = [
  "[&>.border-t.bg-subtle:last-child]:sticky [&>.border-t.bg-subtle:last-child]:bottom-0 [&>.border-t.bg-subtle:last-child]:z-[2]",
  "[&>*>.border-t.bg-subtle:last-child]:sticky [&>*>.border-t.bg-subtle:last-child]:bottom-0 [&>*>.border-t.bg-subtle:last-child]:z-[2]",
].join(" ");

/**
 * ModalFooter is the standard dialog action row. On phones the buttons stack
 * full width (primary last, matching tab order); from `sm` they sit in a right-aligned row.
 * Pass it as Modal's `footer` prop, or render it as the last child of the
 * dialog body / form, where it sticks to the bottom while the body scrolls.
 */
export function ModalFooter({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      data-modal-footer
      className={`sticky bottom-0 z-[2] flex shrink-0 flex-col gap-2 border-t border-line bg-subtle px-5 py-3 sm:flex-row sm:flex-wrap sm:items-center sm:justify-end [&>a]:w-full [&>button]:w-full sm:[&>a]:w-auto sm:[&>button]:w-auto ${className}`}
    >
      {children}
    </div>
  );
}

// Modal is a reusable dialog overlay. Escape and backdrop click close it.
// Focus moves into the dialog on open (an element marked data-modal-autofocus,
// an already-focused autoFocus field, or the close button), is trapped while
// open, and returns to the opener on close.
//
// Layout: full width minus 16px gutters on phones, never taller than the
// viewport. The header (and the optional `footer`) stay put while the body
// scrolls.
export function Modal({
  open,
  onClose,
  title,
  subtitle,
  children,
  footer,
  maxWidth = "max-w-lg",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  subtitle?: string;
  children: ReactNode;
  /** Action row rendered outside the scrolling body (see ModalFooter). */
  footer?: ReactNode;
  maxWidth?: string;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  const titleId = useId();
  const subtitleId = useId();

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
      // A field that took focus through autoFocus keeps it.
      if (dialog && document.activeElement instanceof HTMLElement && dialog.contains(document.activeElement) && document.activeElement !== dialog) return;
      const initial = dialog?.querySelector<HTMLElement>("[data-modal-autofocus]:not([data-modal-close])")
        ?? dialog?.querySelector<HTMLElement>("[data-modal-close]")
        ?? focusableElements()[0]
        ?? dialog;
      initial?.focus();
    });

    const onKey = (event: KeyboardEvent) => {
      // A nested layer (menu, picker, confirm dialog) already handled it.
      if (event.defaultPrevented) return;
      if (event.key === "Escape") {
        if (inFloatingLayer(dialog, event.target)) return;
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== "Tab" || !dialog) return;
      if (inFloatingLayer(dialog, document.activeElement)) return;
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
      if (!dialog || !(event.target instanceof Node) || dialog.contains(event.target)) return;
      if (inFloatingLayer(dialog, event.target)) return;
      dialog.focus();
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

  // Portalled to <body> so the overlay always covers the whole viewport: inside
  // <main> an ancestor's transform (page-in animation), overflow or stacking
  // context would otherwise trap the `fixed` backdrop within the page content.
  return createPortal(
    <div
      className="fixed inset-0 z-50 overflow-y-auto overscroll-contain bg-overlay animate-overlay-in"
      onClick={onClose}
    >
      {/* The overlay can still scroll as a last resort (e.g. 400% zoom). */}
      <div
        className="flex min-h-full items-center justify-center p-4"
        style={{
          paddingTop: "max(1rem, env(safe-area-inset-top))",
          paddingBottom: "max(1rem, env(safe-area-inset-bottom))",
        }}
      >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={subtitle ? subtitleId : undefined}
        tabIndex={-1}
        className={`flex max-h-[calc(100dvh-2rem)] w-full ${maxWidth} flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-lift animate-dialog-in`}
        style={{ maxHeight: "calc(100dvh - max(1rem, env(safe-area-inset-top)) - max(1rem, env(safe-area-inset-bottom)))" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line py-3.5 pl-5 pr-3 sm:pr-5">
          <div className="min-w-0">
            <h2 id={titleId} className="text-title font-medium">{title}</h2>
            {subtitle && <p id={subtitleId} className="mt-0.5 text-[13px] text-fg-muted">{subtitle}</p>}
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            data-modal-autofocus
            data-modal-close
            className="press flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-11"
          >
            <X className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
        <div className={`min-h-0 flex-1 overflow-y-auto overscroll-contain ${STICKY_MODAL_FOOTERS}`}>{children}</div>
        {footer && <ModalFooter>{footer}</ModalFooter>}
      </div>
      </div>
    </div>,
    document.body,
  );
}

/** Ids that tie a TabBar tab to its panel. Use with <TabPanel> or spread on your own panel. */
export function tabIds(idPrefix: string, value: string) {
  const safe = value.replace(/[^A-Za-z0-9_-]/g, "_");
  return { tab: `${idPrefix}-tab-${safe}`, panel: `${idPrefix}-panel-${safe}` };
}

// TabBar renders a horizontal tab navigation strip (WAI-ARIA tabs pattern):
// arrow keys / Home / End move between tabs and activate them, only the active
// tab is in the Tab order. Pass `idPrefix` to get aria-controls wiring, then
// wrap the content in <TabPanel idPrefix value>.
export function TabBar<T extends string>({
  tabs,
  active,
  onChange,
  label,
  idPrefix,
}: {
  tabs: { value: T; label: string; icon?: LucideIcon; count?: number }[];
  active: T;
  onChange: (v: T) => void;
  /** Accessible name for the tab list, e.g. "Provider sections". */
  label?: string;
  /** Enables tab ↔ panel ids (`${idPrefix}-tab-…` / `${idPrefix}-panel-…`). */
  idPrefix?: string;
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
  const hasActive = tabs.some((t) => t.value === active);
  // On phones the strip scrolls sideways; keep the active tab fully in view
  // (on mount too, e.g. when ?tab= deep-links to the last tab).
  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    keepInView(listRef.current, listRef.current?.querySelector('[aria-selected="true"]'));
  }, [active]);

  return (
    <div
      ref={listRef}
      className="flex gap-1 overflow-x-auto overscroll-x-contain border-b border-line px-1 pb-px [@media(pointer:coarse)]:[scrollbar-width:none] [@media(pointer:coarse)]:[&::-webkit-scrollbar]:hidden"
      role="tablist"
      aria-label={label}
      onKeyDown={onKeyDown}
    >
      {tabs.map((tab, i) => {
        const isActive = tab.value === active;
        const ids = idPrefix ? tabIds(idPrefix, tab.value) : null;
        return (
          <button
            key={tab.value}
            type="button"
            role="tab"
            id={ids?.tab}
            aria-controls={ids?.panel}
            aria-selected={isActive}
            tabIndex={isActive || (!hasActive && i === 0) ? 0 : -1}
            onClick={() => onChange(tab.value)}
            className={`relative flex shrink-0 items-center gap-1.5 whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors duration-150 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
              isActive
                ? "text-fg"
                : "text-fg-muted hover:text-fg"
            }`}
          >
            {tab.icon && <tab.icon className="h-4 w-4 shrink-0" aria-hidden="true" />}
            {tab.label}
            {tab.count != null && (
              <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{tab.count}</span>
            )}
            {isActive && (
              <span className="absolute inset-x-2 -bottom-px h-0.5 bg-brand-ink" aria-hidden="true" />
            )}
          </button>
        );
      })}
    </div>
  );
}

/** The panel for a TabBar tab rendered with the same `idPrefix`. */
export function TabPanel({
  idPrefix,
  value,
  className,
  children,
}: {
  idPrefix: string;
  value: string;
  className?: string;
  children: ReactNode;
}) {
  const ids = tabIds(idPrefix, value);
  return (
    <div role="tabpanel" id={ids.panel} aria-labelledby={ids.tab} tabIndex={0} className={`focus:outline-none ${className ?? ""}`}>
      {children}
    </div>
  );
}

export function Toggle({
  checked,
  onChange,
  label,
  disabled,
  id,
  "aria-labelledby": labelledBy,
  "aria-describedby": describedBy,
  className = "",
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  /** Extra classes for the switch (layout only; visuals are fixed). */
  className?: string;
  /** Accessible name when no visible <label> points at this switch. */
  label?: string;
  disabled?: boolean;
  id?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      id={id}
      aria-checked={checked}
      aria-label={label}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      // The visible track is 36×20; an invisible ::before extends the hit
      // area to 44×32 (44×40 on touch) without changing the visuals.
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors duration-200 before:absolute before:-inset-x-1 before:-inset-y-1.5 before:content-[''] [@media(pointer:coarse)]:before:-inset-y-2.5 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas disabled:cursor-not-allowed disabled:opacity-50 ${
        checked ? "bg-action" : "bg-input"
      } ${className}`}
    >
      <span
        aria-hidden="true"
        className={`inline-block h-4 w-4 transform rounded-full bg-surface shadow-paper transition-transform duration-200 ease-out-soft ${
          checked ? "translate-x-[18px]" : "translate-x-0.5"
        }`}
      />
    </button>
  );
}

// TablePagination is the footer row shown below a paginated table. It renders
// a total count on the left and Prev / page-indicator / Next on the right.
// The page indicator is a polite live region so a page change is announced.
export function TablePagination({
  page,
  pages,
  total,
  onPage,
  label = "Pagination",
}: {
  page: number;
  pages: number;
  total: number;
  onPage: (p: number) => void;
  /** Accessible name for the pagination nav when a page has several tables. */
  label?: string;
}) {
  if (pages <= 1) return null;
  const btn =
    "inline-flex h-9 items-center justify-center rounded-lg border border-transparent px-3 font-medium text-fg-muted transition-colors hover:border-line hover:bg-surface hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40";
  return (
    <nav aria-label={label} className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-2.5 text-xs sm:flex-row sm:items-center sm:justify-between">
      <span className="text-fg-muted">{total.toLocaleString()} total</span>
      <div className="flex items-center justify-between gap-1 sm:justify-start">
        <button type="button" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label="Previous page" className={btn}>
          Previous
        </button>
        <span className="min-w-16 px-2 py-1 text-center font-medium tabular-nums" aria-live="polite" aria-atomic="true">
          <span aria-hidden="true">{page} / {pages}</span>
          <span className="sr-only">Page {page} of {pages}</span>
        </span>
        <button type="button" disabled={page >= pages} onClick={() => onPage(page + 1)} aria-label="Next page" className={btn}>
          Next
        </button>
      </div>
    </nav>
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

// ── Glyphs & section titles ──────────────────────────────────────────────────
// Icons sit bare beside their text, never in a tinted tile. Colour only when
// the icon itself is a status.

export type GlyphTone = "muted" | "brand" | "ok" | "warn" | "bad";

const glyphTone: Record<GlyphTone, string> = {
  muted: "text-fg-faint",
  brand: "text-brand-ink",
  ok: "text-ok",
  warn: "text-warn",
  bad: "text-bad",
};

export function Glyph({
  icon: Icon,
  size = "md",
  tone = "muted",
  className = "",
}: {
  icon: LucideIcon;
  /** md 16px (rows, headers) · lg 20px (a status that is the whole message). */
  size?: "md" | "lg";
  tone?: GlyphTone;
  className?: string;
}) {
  return <Icon aria-hidden="true" className={`shrink-0 ${size === "lg" ? "size-5" : "size-4"} ${glyphTone[tone]} ${className}`} />;
}

/**
 * SectionTitle is the standard card/section header: an optional tone-coloured
 * icon, a 13px title, an optional one-line subtitle and right-aligned actions.
 * Use it inside a card's header row (`border-b border-line px-4 py-3`).
 */
export function SectionTitle({
  icon: Icon,
  title,
  subtitle,
  action,
  id,
  as: Heading = "h2",
}: {
  icon?: LucideIcon;
  title: ReactNode;
  subtitle?: ReactNode;
  action?: ReactNode;
  id?: string;
  as?: "h2" | "h3";
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-2.5">
        {Icon && <Icon className="h-4 w-4 shrink-0 text-fg-faint" aria-hidden="true" />}
        <div className="min-w-0">
          <Heading id={id} className="text-heading font-medium text-fg">
            {title}
          </Heading>
          {subtitle && <p className="mt-0.5 text-[12px] text-fg-muted">{subtitle}</p>}
        </div>
      </div>
      {action && <div className="flex min-w-0 flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

/**
 * KpiGrid + Kpi: the metric strip. One surface split by hairlines; each cell
 * has a label, the figure and an optional hint (delta / "of N"). Pass
 * `tone="ok" | "warn" | "bad"` only when the value itself is a status.
 */
export function KpiGrid({ children, cols = 4, label }: { children: ReactNode; cols?: 2 | 3 | 4 | 5 | 6; label?: string }) {
  const grid = { 2: "sm:grid-cols-2", 3: "sm:grid-cols-3", 4: "sm:grid-cols-2 xl:grid-cols-4", 5: "sm:grid-cols-3 xl:grid-cols-5", 6: "sm:grid-cols-3 xl:grid-cols-6" }[cols];
  return (
    <dl aria-label={label} className={`grid grid-cols-1 gap-px overflow-hidden rounded-2xl border border-line bg-line shadow-paper ${grid}`}>
      {children}
    </dl>
  );
}

/** A period-over-period change: arrow, signed text, coloured by whether it is good. */
export function DeltaBadge({ delta, label = "vs previous period", className = "" }: { delta: Delta; label?: string; className?: string }) {
  const Arrow = delta.dir === "up" ? ArrowUpRight : delta.dir === "down" ? ArrowDownRight : ArrowRight;
  const tone = delta.tone === "good" ? "bg-ok-soft text-ok" : delta.tone === "bad" ? "bg-bad-soft text-bad" : "bg-subtle text-fg-muted";
  return (
    <span className={`inline-flex h-5 shrink-0 items-center gap-0.5 whitespace-nowrap rounded-md px-1.5 text-[11.5px] font-medium tabular-nums ${tone} ${className}`}>
      <Arrow className="size-3" aria-hidden="true" />
      {delta.text}
      <span className="sr-only">
        {" "}
        {label}
        {delta.tone === "good" ? ", better" : delta.tone === "bad" ? ", worse" : ""}
      </span>
    </span>
  );
}

const kpiTone: Partial<Record<GlyphTone, string>> = { ok: "text-ok", warn: "text-warn", bad: "text-bad" };

export function Kpi({
  label,
  value,
  hint,
  delta,
  deltaLabel,
  tone,
  valueClassName = "",
}: {
  label: ReactNode;
  value: ReactNode;
  /** Secondary line: a breakdown or context ("6.2M in · 514k out"). */
  hint?: ReactNode;
  /** Change vs the previous period; `null` shows "No prior data". */
  delta?: Delta | null;
  /** Screen-reader context for the delta, e.g. "vs previous 7 days". */
  deltaLabel?: string;
  /** Only when the value itself is a status. */
  tone?: "ok" | "warn" | "bad";
  valueClassName?: string;
}) {
  const footer = delta !== undefined || hint;
  return (
    <div className="bg-surface px-5 py-4">
      <dt className="truncate text-meta text-fg-muted">{label}</dt>
      <dd className={`mt-1 min-w-0 font-display text-[1.625rem] leading-tight text-fg ${tone ? kpiTone[tone] : ""} ${valueClassName}`}>{value}</dd>
      {footer && (
        <dd className="mt-1.5 flex min-h-5 min-w-0 items-center gap-2 text-[12px] text-fg-muted">
          {delta ? <DeltaBadge delta={delta} label={deltaLabel} /> : delta === null ? <span className="shrink-0 text-fg-faint">No prior data</span> : null}
          {hint && <span className="min-w-0 truncate tabular-nums">{hint}</span>}
        </dd>
      )}
    </div>
  );
}
