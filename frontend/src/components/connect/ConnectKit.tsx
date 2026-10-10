import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { AlertTriangle, ArrowLeft, Check, CheckCircle2, ChevronRight, Copy, ExternalLink, Loader2, RefreshCw, X, type LucideIcon } from "lucide-react";
import type { DeviceCode } from "../../lib/api";
import { cn } from "@/lib/utils";
import { ProviderLogo } from "../ProviderLogo";
import { useToast } from "../Toast";

// The connect kit is the shared vocabulary of every "connect a provider"
// dialog: one Radix-backed shell, a method picker, numbered steps, a device
// code / browser-approval waiting view, a token form, and success / error
// states. Provider-specific dialogs compose these and only describe what is
// actually different about that provider.

// ── Shell ────────────────────────────────────────────────────────────────────

export function ConnectDialog({
  title,
  description,
  logo,
  onClose,
  onBack,
  width = "md",
  children,
  footer,
}: {
  title: string;
  description?: ReactNode;
  logo?: { icon?: string; name: string };
  onClose: () => void;
  /** Shows a back arrow, e.g. to return to the method picker. */
  onBack?: () => void;
  width?: "md" | "lg";
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/45 data-[state=open]:animate-in data-[state=open]:fade-in-0" />
        <Dialog.Content
          className={cn(
            "fixed left-1/2 top-1/2 z-50 flex max-h-[min(88vh,760px)] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-float)] focus:outline-none",
            "data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-[0.98]",
            width === "lg" ? "max-w-xl" : "max-w-md",
          )}
          onOpenAutoFocus={(e) => {
            // Focus the first form control rather than the close button.
            const target = (e.currentTarget as HTMLElement).querySelector<HTMLElement>("input, textarea, [data-autofocus]");
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
                className="-ml-1.5 mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
              >
                <ArrowLeft className="h-4 w-4" />
              </button>
            )}
            {logo && <ProviderLogo icon={logo.icon} name={logo.name} size={28} className="mt-0.5" />}
            <div className="min-w-0 flex-1">
              <Dialog.Title className="text-[15px] font-semibold tracking-[-0.01em] text-fg">{title}</Dialog.Title>
              {description ? (
                <Dialog.Description className="mt-0.5 text-[13px] leading-5 text-fg-muted">{description}</Dialog.Description>
              ) : (
                <Dialog.Description className="sr-only">{title}</Dialog.Description>
              )}
            </div>
            <Dialog.Close
              aria-label="Close"
              className="-mr-1.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
          {footer && <div className="flex items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">{footer}</div>}
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
  className,
}: {
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  busy?: boolean;
  type?: "button" | "submit";
  className?: string;
}) {
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled || busy}
      data-autofocus
      className={cn(
        "inline-flex h-9 items-center justify-center gap-1.5 rounded-lg bg-primary px-3.5 text-[13px] font-medium text-primary-fg transition-opacity hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-elevated)] [&_svg]:size-4",
        className,
      )}
    >
      {busy && <Loader2 className="animate-spin" />}
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
        "inline-flex h-9 items-center justify-center gap-1.5 rounded-lg border border-line-strong bg-surface px-3.5 text-[13px] font-medium text-fg transition-colors hover:bg-hover disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 [&_svg]:size-4",
        className,
      )}
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
    <div className="space-y-2">
      {intro && <p className="mb-3 text-[13px] text-fg-muted">{intro}</p>}
      {methods.map((m) => (
        <button
          key={m.id}
          type="button"
          onClick={() => onSelect(m.id)}
          className="group flex w-full items-center gap-3 rounded-xl border border-line bg-surface px-3.5 py-3 text-left transition-colors hover:border-line-strong hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
        >
          <m.icon className="h-[18px] w-[18px] shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2 text-[13px] font-medium text-fg">
              {m.title}
              {m.recommended && <span className="rounded-md border border-line px-1.5 text-[11px] font-medium text-fg-muted">Recommended</span>}
            </span>
            <span className="mt-0.5 block text-[12.5px] leading-5 text-fg-muted">{m.description}</span>
          </span>
          <ChevronRight className="h-4 w-4 shrink-0 text-fg-faint transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}

// ── Steps ────────────────────────────────────────────────────────────────────

export function Steps({ title = "How it works", steps }: { title?: string; steps: ReactNode[] }) {
  return (
    <div className="rounded-xl border border-line bg-subtle px-4 py-3">
      <p className="mb-2 text-[12px] font-medium text-fg-muted">{title}</p>
      <ol className="space-y-2">
        {steps.map((s, i) => (
          <li key={i} className="flex items-start gap-2.5 text-[13px] leading-5 text-fg">
            <span className="mt-px flex h-5 w-5 shrink-0 items-center justify-center rounded-full border border-line-strong font-mono text-[11px] text-fg-muted">{i + 1}</span>
            <span className="min-w-0">{s}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

export function InlineCode({ children }: { children: ReactNode }) {
  return <code className="rounded-md border border-line bg-surface px-1 py-px font-mono text-[12px] text-fg">{children}</code>;
}

export function ExternalTextLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 font-medium text-accent-500 hover:underline dark:text-accent-400">
      {children}
      <ExternalLink className="h-3 w-3" aria-hidden="true" />
    </a>
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
  const [copied, setCopied] = useState(false);
  const url = code.verification_uri_complete || code.verification_uri;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code.user_code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      /* clipboard blocked: the code stays visible to type manually */
    }
  };
  return (
    <div className="space-y-4">
      {code.user_code && (
        <div className="rounded-xl border border-line bg-subtle px-4 py-3.5 text-center">
          <p className="text-[12px] text-fg-muted">Enter this code on the verification page</p>
          <div className="mt-1.5 flex items-center justify-center gap-2">
            <span className="font-mono text-[24px] font-semibold tracking-[0.18em] text-fg">{code.user_code}</span>
            <button
              type="button"
              onClick={copy}
              aria-label="Copy code"
              className="flex h-7 w-7 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              {copied ? <Check className="h-4 w-4 text-ok" /> : <Copy className="h-4 w-4" />}
            </button>
          </div>
        </div>
      )}
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        data-autofocus
        className="flex h-9 w-full items-center justify-center gap-1.5 rounded-lg bg-primary text-[13px] font-medium text-primary-fg transition-opacity hover:opacity-85 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-elevated)]"
      >
        <ExternalLink className="h-4 w-4" />
        {openLabel}
      </a>
      <div className="flex items-center gap-2.5 rounded-xl border border-line px-3.5 py-2.5" role="status" aria-live="polite">
        <Loader2 className="h-4 w-4 shrink-0 animate-spin text-fg-muted" />
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-medium text-fg">Waiting for approval</p>
          <p className="text-[12px] text-fg-muted">{hint ?? "Approve the request in the other tab. This dialog finishes on its own."}</p>
        </div>
        <span className="font-mono text-[12px] tabular-nums text-fg-faint">{formatElapsed(elapsed)}</span>
      </div>
      {elapsed > 240 && (
        <p className="flex items-start gap-2 text-[12px] text-warn">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          Taking a while? Check that the page opened and wasn't blocked as a popup — codes usually expire after 5 minutes.
        </p>
      )}
      {children}
    </div>
  );
}

function formatElapsed(s: number): string {
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function Starting({ text }: { text: string }) {
  return (
    <div className="flex flex-col items-center gap-3 py-8" role="status">
      <Loader2 className="h-6 w-6 animate-spin text-fg-muted" />
      <p className="text-[13px] text-fg-muted">{text}</p>
    </div>
  );
}

export function Connected({ name }: { name: string }) {
  return (
    <div className="flex flex-col items-center gap-2 py-8 text-center" role="status">
      <CheckCircle2 className="h-8 w-8 text-ok" strokeWidth={1.75} />
      <p className="text-[14px] font-medium text-fg">{name} connected</p>
      <p className="text-[12.5px] text-fg-muted">Refreshing accounts…</p>
    </div>
  );
}

export function ConnectError({ message, onRetry, onClose }: { message: string; onRetry?: () => void; onClose: () => void }) {
  return (
    <div className="space-y-4">
      <div role="alert" className="flex items-start gap-2.5 rounded-xl border border-bad/30 bg-bad/5 px-3.5 py-3">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-bad" />
        <p className="break-words text-[13px] leading-5 text-bad">{message || "Something went wrong."}</p>
      </div>
      <div className="flex justify-end gap-2">
        <SecondaryAction onClick={onClose}>Close</SecondaryAction>
        {onRetry && (
          <PrimaryAction onClick={onRetry}>
            <RefreshCw />
            Try again
          </PrimaryAction>
        )}
      </div>
    </div>
  );
}

export function FormError({ message }: { message?: string }) {
  if (!message) return null;
  return (
    <p role="alert" className="flex items-start gap-1.5 text-[12.5px] leading-5 text-bad">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span className="break-words">{message}</span>
    </p>
  );
}

// ── Form fields ──────────────────────────────────────────────────────────────

export function TextField({
  label,
  hint,
  optional,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: ReactNode; optional?: boolean }) {
  return (
    <label className="block space-y-1.5">
      <span className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
      </span>
      <input
        {...props}
        className={cn(
          "h-9 w-full rounded-lg border border-line bg-surface px-3 text-[13px] text-fg placeholder:text-fg-faint transition-colors hover:border-line-strong focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25 disabled:opacity-60",
          props.className,
        )}
      />
      {hint && <span className="block text-[12px] leading-5 text-fg-muted">{hint}</span>}
    </label>
  );
}

export function TextAreaField({
  label,
  hint,
  ...props
}: React.TextareaHTMLAttributes<HTMLTextAreaElement> & { label?: string; hint?: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      {label && <span className="block text-[12.5px] font-medium text-fg">{label}</span>}
      <textarea
        spellCheck={false}
        {...props}
        className={cn(
          "w-full rounded-lg border border-line bg-surface px-3 py-2 font-mono text-[12px] leading-5 text-fg placeholder:text-fg-faint transition-colors hover:border-line-strong focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25",
          props.className,
        )}
      />
      {hint && <span className="block text-[12px] leading-5 text-fg-muted">{hint}</span>}
    </label>
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
