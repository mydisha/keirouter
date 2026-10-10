import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { AlertTriangle, ArrowLeft, Check, ChevronRight, Copy, ExternalLink, Loader2, RefreshCw, X, type LucideIcon } from "lucide-react";
import type { DeviceCode } from "../../lib/api";
import { cn } from "@/lib/utils";
import { ProviderLogo } from "../ProviderLogo";
import { Badge, IconTile } from "../ui";
import { useToast } from "../Toast";

// The connect kit is the shared vocabulary of every "connect a provider"
// dialog: one Radix-backed shell, a method picker, numbered steps, a device
// code / browser-approval waiting view, a token form, and success / error
// states. Provider-specific dialogs compose these and only describe what is
// actually different about that provider.

// ── Shell ────────────────────────────────────────────────────────────────────

// Dialogs render in a portal outside <main>, so they don't inherit the page's
// section tone. Every connect dialog belongs to the Providers group (teal);
// `tone` lets a caller from another section override it.
type DialogTone = "blue" | "violet" | "teal" | "orange" | "slate";

export function ConnectDialog({
  title,
  description,
  logo,
  onClose,
  onBack,
  width = "md",
  toolbar,
  children,
  footer,
  tone = "teal",
}: {
  title: string;
  description?: ReactNode;
  logo?: { icon?: string; name: string };
  onClose: () => void;
  /** Shows a back arrow, e.g. to return to the method picker. */
  onBack?: () => void;
  width?: "md" | "lg" | "xl";
  /** Non-scrolling strip under the header (search, filters). */
  toolbar?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  tone?: DialogTone;
}) {
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/45 data-[state=open]:animate-in data-[state=open]:fade-in-0" />
        <Dialog.Content
          {...(description ? {} : { "aria-describedby": undefined })}
          data-tone={tone}
          className={cn(
            "fixed left-1/2 top-1/2 z-50 flex max-h-[min(88vh,760px)] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-float)] focus:outline-none",
            "data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-[0.98]",
            width === "xl" ? "max-w-3xl" : width === "lg" ? "max-w-xl" : "max-w-md",
          )}
          onOpenAutoFocus={(e) => {
            // Focus the first form control rather than the close button.
            const target = (e.currentTarget as HTMLElement).querySelector<HTMLElement>("input:not([type=hidden]):not([type=file]), textarea, select, [data-autofocus]");
            if (target) {
              e.preventDefault();
              target.focus();
            }
          }}
        >
          <div className="flex items-start gap-3 border-b border-line px-5 py-4">
            {onBack && (
              <button
                type="button"
                onClick={onBack}
                aria-label="Back"
                className="-ml-1.5 mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                <ArrowLeft className="h-4 w-4" aria-hidden="true" />
              </button>
            )}
            {logo && <ProviderLogo icon={logo.icon} name={logo.name} size={28} className="mt-0.5" />}
            <div className="min-w-0 flex-1 self-center">
              <Dialog.Title className="text-[15px] font-semibold tracking-[-0.01em] text-fg">{title}</Dialog.Title>
              {description && <Dialog.Description className="mt-0.5 text-[13px] leading-5 text-fg-muted">{description}</Dialog.Description>}
            </div>
            <Dialog.Close
              aria-label="Close"
              className="-mr-1.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </Dialog.Close>
          </div>
          {toolbar && <div className="border-b border-line px-5 py-3">{toolbar}</div>}
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
          {footer && <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">{footer}</div>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

// ── Buttons used inside dialogs ──────────────────────────────────────────────

export function PrimaryAction({
  children,
  onClick,
  disabled,
  busy,
  type = "button",
  form,
  className,
}: {
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  busy?: boolean;
  type?: "button" | "submit";
  /** Associates a footer submit button with a form in the dialog body. */
  form?: string;
  className?: string;
}) {
  return (
    <button
      type={type}
      form={form}
      onClick={onClick}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      data-autofocus
      className={cn(
        "inline-flex h-9 items-center justify-center gap-1.5 rounded-lg border border-transparent bg-action px-3.5 text-[13px] font-medium text-action-fg shadow-[var(--shadow-card)] transition-colors hover:bg-action-hover disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-surface [&_svg]:size-4",
        className,
      )}
    >
      {busy && <Loader2 className="animate-spin" aria-hidden="true" />}
      {children}
    </button>
  );
}

export function SecondaryAction({ children, onClick, disabled, className }: { children: ReactNode; onClick?: () => void; disabled?: boolean; className?: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "inline-flex h-9 items-center justify-center gap-1.5 rounded-lg border border-line-strong bg-surface px-3.5 text-[13px] font-medium text-fg transition-colors hover:bg-hover disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [&_svg]:size-4",
        className,
      )}
    >
      {children}
    </button>
  );
}

// TextButton is the quiet inline action for fallbacks ("Paste the URL instead").
export function TextButton({ children, onClick, ...aria }: { children: ReactNode; onClick: () => void; "aria-expanded"?: boolean; "aria-controls"?: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      {...aria}
      className="inline-flex min-h-6 items-center rounded-md text-[12.5px] font-medium text-fg-muted underline-offset-2 hover:text-fg hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
    >
      {children}
    </button>
  );
}

// ── Method picker ────────────────────────────────────────────────────────────

export interface ConnectMethod<T extends string> {
  id: T;
  title: string;
  description: string;
  icon: LucideIcon;
  recommended?: boolean;
}

export function MethodPicker<T extends string>({ methods, onSelect, intro }: { methods: ConnectMethod<T>[]; onSelect: (id: T) => void; intro?: string }) {
  return (
    <div>
      {intro && <p className="mb-3 text-[13px] text-fg-muted">{intro}</p>}
      <ul className="space-y-2" aria-label="Connection methods">
        {methods.map((m) => (
          <li key={m.id}>
            <button
              type="button"
              onClick={() => onSelect(m.id)}
              className="group flex w-full items-center gap-3 rounded-xl border border-line bg-surface px-3.5 py-3 text-left transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <IconTile icon={m.icon} size="md" />
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2 text-[13px] font-medium text-fg">
                  {m.title}
                  {m.recommended && <Badge tone="secondary">Recommended</Badge>}
                </span>
                <span className="mt-0.5 block text-[12.5px] leading-5 text-fg-muted">{m.description}</span>
              </span>
              <ChevronRight className="h-4 w-4 shrink-0 text-fg-faint transition-[color,transform] group-hover:translate-x-0.5 group-hover:text-tone" aria-hidden="true" />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ── Steps ────────────────────────────────────────────────────────────────────

// Steps is a short numbered list of what the user does next. Keep each step
// to a few words; the title is optional.
export function Steps({ title, steps }: { title?: string; steps: ReactNode[] }) {
  return (
    <div className="rounded-xl border border-line bg-subtle px-4 py-3">
      {title && <h3 className="mb-2 text-[12px] font-medium text-fg-muted">{title}</h3>}
      <ol className="space-y-2">
        {steps.map((s, i) => (
          <li key={i} className="flex items-start gap-2.5 text-[13px] leading-5 text-fg">
            <span aria-hidden="true" className="mt-px flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-tone-soft font-mono text-[11px] font-semibold text-tone ring-1 ring-inset ring-tone-ring">{i + 1}</span>
            <span className="min-w-0">{s}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

export function InlineCode({ children }: { children: ReactNode }) {
  return <code className="break-all rounded-md border border-line bg-surface px-1 py-px font-mono text-[12px] text-fg">{children}</code>;
}

export function ExternalTextLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 rounded-sm font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
      {children}
      <ExternalLink className="h-3 w-3" aria-hidden="true" />
      <span className="sr-only"> (opens in a new tab)</span>
    </a>
  );
}

// CopyButton is the small icon button next to a code or identifier. It is
// 32px square (above the 24px target minimum) and announces the copy.
export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      /* clipboard blocked: the value stays visible to copy manually */
    }
  };
  return (
    <>
      <button
        type="button"
        onClick={copy}
        aria-label={label}
        className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        {copied ? <Check className="h-4 w-4 text-ok" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}
      </button>
      <span className="sr-only" role="status">
        {copied ? "Copied" : ""}
      </span>
    </>
  );
}

// ── Device / browser-approval flow ──────────────────────────────────────────

export type DeviceStatus = "idle" | "starting" | "waiting" | "done" | "error";

export interface DevicePollOptions {
  /** Starts the flow and returns the device code (or login state). */
  start: () => Promise<DeviceCode>;
  /** One poll; resolves "complete" when the account exists. */
  poll: (deviceCode: string) => Promise<{ status: string; slow_down?: boolean }>;
  /** Display name used in toasts ("Kiro connected"). */
  providerName: string;
  /** Opens the verification URL in a new tab as soon as the flow starts. */
  autoOpen?: boolean;
  /** Polling interval floor in seconds (Kimchi polls immediately after a callback). */
  minInterval?: number;
  onConnected: () => void;
}

// useDevicePoll runs any "get a code, approve elsewhere, poll until the token
// lands" flow: start, poll with server-directed slow-down, an elapsed timer,
// cleanup on unmount, and pollNow() for callers that learn about approval
// early (a postMessage from a callback page).
export function useDevicePoll(opts: DevicePollOptions) {
  const qc = useQueryClient();
  const toast = useToast();
  const [status, setStatus] = useState<DeviceStatus>("idle");
  const [code, setCode] = useState<DeviceCode | null>(null);
  const [error, setError] = useState("");
  const [elapsed, setElapsed] = useState(0);
  const pollTimer = useRef<number | undefined>(undefined);
  const clock = useRef<number | undefined>(undefined);
  const codeRef = useRef<DeviceCode | null>(null);
  const intervalRef = useRef(5);
  const optsRef = useRef(opts);
  optsRef.current = opts;

  const stopTimers = () => {
    window.clearTimeout(pollTimer.current);
    window.clearInterval(clock.current);
  };
  useEffect(() => stopTimers, []);

  // schedule polls after `seconds`; the regular cadence never drops below
  // minInterval, while pollNow() passes 0 to check immediately.
  const schedule = useCallback((deviceCode: string, seconds: number) => {
    window.clearTimeout(pollTimer.current);
    pollTimer.current = window.setTimeout(async () => {
      const o = optsRef.current;
      try {
        const res = await o.poll(deviceCode);
        if (res.status === "complete") {
          stopTimers();
          setStatus("done");
          qc.invalidateQueries({ queryKey: ["accounts"] });
          toast.success(`${o.providerName} connected`, "The account is ready for routing.");
          window.setTimeout(o.onConnected, 1100);
          return;
        }
        if (res.slow_down) intervalRef.current += 5;
        schedule(deviceCode, Math.max(o.minInterval ?? 1, intervalRef.current));
      } catch (e) {
        stopTimers();
        setError((e as Error).message);
        setStatus("error");
        toast.error(`${o.providerName} authorization failed`, (e as Error).message);
      }
    }, Math.max(0, seconds) * 1000);
  }, [qc, toast]);

  const start = useCallback(async () => {
    const o = optsRef.current;
    stopTimers();
    setStatus("starting");
    setError("");
    setElapsed(0);
    try {
      const dc = await o.start();
      codeRef.current = dc;
      intervalRef.current = Math.max(o.minInterval ?? 1, dc.interval || 5);
      setCode(dc);
      setStatus("waiting");
      clock.current = window.setInterval(() => setElapsed((s) => s + 1), 1000);
      if (o.autoOpen) window.open(dc.verification_uri_complete || dc.verification_uri, "_blank", "noopener");
      schedule(dc.device_code, intervalRef.current);
    } catch (e) {
      setError((e as Error).message);
      setStatus("error");
      toast.error(`Couldn't start ${o.providerName} authorization`, (e as Error).message);
    }
  }, [schedule, toast]);

  // pollNow checks immediately, e.g. when a callback page reports success.
  const pollNow = useCallback(() => {
    if (codeRef.current) schedule(codeRef.current.device_code, 0);
  }, [schedule]);

  return { status, code, error, elapsed, start, pollNow };
}

export function DeviceWaiting({
  code,
  elapsed,
  openLabel = "Open verification page",
  hint,
  children,
}: {
  code: DeviceCode;
  elapsed: number;
  openLabel?: string;
  /** What the user should do on the other page. */
  hint?: string;
  children?: ReactNode;
}) {
  const url = code.verification_uri_complete || code.verification_uri;
  const slow = elapsed > 240;
  return (
    <div className="space-y-4">
      {code.user_code && (
        <div className="rounded-xl border border-line bg-subtle px-4 py-3.5 text-center">
          <p className="text-[12px] text-fg-muted">Your code</p>
          <div className="mt-1 flex items-center justify-center gap-1.5">
            <span className="font-mono text-[24px] font-semibold tracking-[0.18em] text-fg">{code.user_code}</span>
            <CopyButton value={code.user_code} label="Copy code" />
          </div>
        </div>
      )}
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        data-autofocus
        className="flex h-9 w-full items-center justify-center gap-1.5 rounded-lg bg-action text-[13px] font-medium text-action-fg shadow-[var(--shadow-card)] transition-colors hover:bg-action-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
      >
        <ExternalLink className="h-4 w-4" aria-hidden="true" />
        {openLabel}
        <span className="sr-only"> (opens in a new tab)</span>
      </a>
      <div className="flex items-center gap-2.5 rounded-xl border border-line px-3.5 py-2.5">
        <Loader2 className="h-4 w-4 shrink-0 animate-spin text-tone" aria-hidden="true" />
        {/* The live region holds only the status text: the ticking timer stays
            outside it so screen readers aren't interrupted every second. */}
        <div className="min-w-0 flex-1" role="status" aria-live="polite">
          <p className="text-[13px] font-medium text-fg">Waiting for approval</p>
          <p className="text-[12px] text-fg-muted">{hint ?? (code.user_code ? "Enter the code on that page and approve." : "Approve access on that page.")}</p>
          {slow && (
            <p className="mt-1 flex items-start gap-1.5 text-[12px] text-warn">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              Still waiting? Check the page wasn't blocked. Codes expire after about 5 minutes.
            </p>
          )}
        </div>
        <span className="font-mono text-[12px] tabular-nums text-fg-faint" aria-hidden="true">{formatElapsed(elapsed)}</span>
      </div>
      {children}
    </div>
  );
}

function formatElapsed(s: number): string {
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function Starting({ text }: { text: string }) {
  return (
    <div className="flex flex-col items-center gap-3 py-8" role="status" aria-live="polite">
      <Loader2 className="h-6 w-6 animate-spin text-tone" aria-hidden="true" />
      <p className="text-[13px] text-fg-muted">{text}</p>
    </div>
  );
}

export function Connected({ name }: { name: string }) {
  return (
    <div className="flex flex-col items-center gap-2 py-8 text-center" role="status" aria-live="polite">
      <IconTile icon={Check} size="lg" tone="ok" className="mb-1" />
      <p className="text-[14px] font-semibold text-fg">{name} connected</p>
      <p className="text-[12.5px] text-fg-muted">Refreshing accounts…</p>
    </div>
  );
}

export function ConnectError({ message, onRetry, onClose }: { message: string; onRetry?: () => void; onClose: () => void }) {
  return (
    <div className="space-y-4">
      <div role="alert" className="flex items-start gap-2.5 rounded-xl border border-bad/30 bg-bad/5 px-3.5 py-3">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-bad" aria-hidden="true" />
        <p className="break-words text-[13px] leading-5 text-bad">{message || "Something went wrong. Try again."}</p>
      </div>
      <div className="flex justify-end gap-2">
        <SecondaryAction onClick={onClose}>Close</SecondaryAction>
        {onRetry && (
          <PrimaryAction onClick={onRetry}>
            <RefreshCw aria-hidden="true" />
            Try again
          </PrimaryAction>
        )}
      </div>
    </div>
  );
}

export function FormError({ message, id }: { message?: string; id?: string }) {
  if (!message) return null;
  return (
    <p id={id} role="alert" className="flex items-start gap-1.5 text-[12.5px] leading-5 text-bad">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span className="break-words">{message}</span>
    </p>
  );
}

// ── Form fields ──────────────────────────────────────────────────────────────

const fieldClass =
  "w-full rounded-lg border bg-surface text-[13px] text-fg placeholder:text-fg-faint transition-colors focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-60";

function FieldLabel({ htmlFor, label, optional, required }: { htmlFor: string; label: string; optional?: boolean; required?: boolean }) {
  return (
    <label htmlFor={htmlFor} className="flex items-baseline justify-between gap-2 text-[12.5px] font-medium text-fg">
      {label}
      {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
      {required && <span className="text-[12px] font-normal text-fg-faint">Required</span>}
    </label>
  );
}

// useFieldIds wires a control to its hint and error text.
function useFieldIds(id: string | undefined, hint: ReactNode, error: string | undefined, describedBy: string | undefined) {
  const auto = useId();
  const fieldId = id ?? auto;
  const hintId = hint ? `${fieldId}-hint` : undefined;
  const errorId = error ? `${fieldId}-error` : undefined;
  const ids = [describedBy, errorId, hintId].filter(Boolean).join(" ") || undefined;
  return { fieldId, hintId, errorId, describedBy: ids };
}

export function TextField({
  label,
  hint,
  optional,
  error,
  required,
  id,
  "aria-describedby": ariaDescribedBy,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: ReactNode; optional?: boolean; error?: string }) {
  const f = useFieldIds(id, hint, error, ariaDescribedBy);
  return (
    <div className="space-y-1.5">
      <FieldLabel htmlFor={f.fieldId} label={label} optional={optional} required={required} />
      <input
        {...props}
        id={f.fieldId}
        aria-required={required || undefined}
        aria-invalid={error ? true : undefined}
        aria-describedby={f.describedBy}
        className={cn(fieldClass, "h-9 px-3", error ? "border-bad" : "border-input hover:border-fg-faint", props.className)}
      />
      {error && <FieldError id={f.errorId!} message={error} />}
      {hint && (
        <p id={f.hintId} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

export function TextAreaField({
  label,
  hint,
  error,
  required,
  id,
  "aria-describedby": ariaDescribedBy,
  ...props
}: React.TextareaHTMLAttributes<HTMLTextAreaElement> & { label?: string; hint?: ReactNode; error?: string }) {
  const f = useFieldIds(id, hint, error, ariaDescribedBy);
  return (
    <div className="space-y-1.5">
      {label && <FieldLabel htmlFor={f.fieldId} label={label} required={required} />}
      <textarea
        spellCheck={false}
        {...props}
        id={f.fieldId}
        aria-required={required || undefined}
        aria-invalid={error ? true : undefined}
        aria-describedby={f.describedBy}
        className={cn(fieldClass, "px-3 py-2 font-mono text-[12px] leading-5", error ? "border-bad" : "border-input hover:border-fg-faint", props.className)}
      />
      {error && <FieldError id={f.errorId!} message={error} />}
      {hint && (
        <p id={f.hintId} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

export function SelectField({
  label,
  hint,
  id,
  children,
  ...props
}: React.SelectHTMLAttributes<HTMLSelectElement> & { label: string; hint?: ReactNode }) {
  const f = useFieldIds(id, hint, undefined, undefined);
  return (
    <div className="space-y-1.5">
      <FieldLabel htmlFor={f.fieldId} label={label} />
      <select {...props} id={f.fieldId} aria-describedby={f.describedBy} className={cn(fieldClass, "h-9 border-input px-2.5 hover:border-fg-faint", props.className)}>
        {children}
      </select>
      {hint && (
        <p id={f.hintId} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

function FieldError({ id, message }: { id: string; message: string }) {
  return (
    <p id={id} className="flex items-start gap-1.5 text-[12.5px] leading-5 text-bad">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span className="break-words">{message}</span>
    </p>
  );
}

// Advanced is the collapsed "more options" group for fields most people skip.
export function Advanced({ children, label = "More options", defaultOpen = false }: { children: ReactNode; label?: string; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const id = useId();
  return (
    <div className="border-t border-line pt-3">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-controls={id}
        className="inline-flex min-h-6 items-center gap-1 rounded-md text-[12.5px] font-medium text-fg-muted hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", open && "rotate-90")} aria-hidden="true" />
        {label}
      </button>
      <div id={id} hidden={!open} className="mt-3 space-y-3.5">
        {children}
      </div>
    </div>
  );
}

// ── Token import ─────────────────────────────────────────────────────────────

// TokenImport is the "paste a credential from the vendor's app" flow used by
// Cursor, Command Code and Kiro's import methods.
export function useTokenImport(submit: (value: string) => Promise<unknown>, providerName: string, onConnected: () => void) {
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  const run = async (value: string) => {
    if (!value.trim()) {
      setError("Paste a value first.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await submit(value.trim());
      setDone(true);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success(`${providerName} connected`, "The account is ready for routing.");
      window.setTimeout(onConnected, 1000);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, done, run, setError };
}
